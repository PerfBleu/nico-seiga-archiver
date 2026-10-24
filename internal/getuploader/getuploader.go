// Package getuploader 实现 getuploader / uploader.jp 的密码认证与文件下载。
package getuploader

import (
	"context"
	"crypto/md5"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"hash"
	"io"
	"mime"
	"net/http"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"golang.org/x/net/html"

	"nico-seiga-archiver/internal/htmlx"
	"nico-seiga-archiver/internal/httpclient"
	"nico-seiga-archiver/internal/logx"
	"nico-seiga-archiver/internal/util"
)

// ErrPasswordRequired 表示该下载页需要密码。
var ErrPasswordRequired = errors.New("该下载页需要密码")

// ErrPasswordRejected 表示密码错误。
var ErrPasswordRejected = errors.New("下载密码错误")

// Meta 是下载页上的文件信息。
type Meta struct {
	PageURL      string
	Filename     string
	SizeText     string
	MD5          string
	NeedPassword bool
	Token        string
	DirectURL    string
}

// Result 是一次下载的结果。
type Result struct {
	Meta
	DirectURL      string
	Path           string
	Bytes          int64
	MD5            string
	SHA256         string
	AlreadyExisted bool
}

// Client 是 getuploader 客户端。
type Client struct {
	hc    *httpclient.Client
	log   *logx.Logger
	delay time.Duration
}

// New 创建客户端。
func New(hc *httpclient.Client, log *logx.Logger, delay time.Duration) *Client {
	return &Client{hc: hc, log: log, delay: delay}
}

// Download 完成「打开下载页 -> 输入密码 -> 确认下载 -> 保存文件」的整个流程。
func (c *Client) Download(ctx context.Context, pageURL, password, destDir string, force bool) (*Result, error) {
	res := &Result{Meta: Meta{PageURL: pageURL}}

	doc, err := c.loadPage(ctx, pageURL, pageURL)
	if err != nil {
		return nil, err
	}
	res.Meta = parseMeta(doc, pageURL)
	res.DirectURL = res.Meta.DirectURL
	c.logInfof("下载页: %s | 文件: %s | 大小: %s", pageURL, orDefault(res.Filename, "(未知)"), orDefault(res.SizeText, "(未知)"))
	if res.MD5 != "" {
		c.logDebugf("页面 MD5: %s", res.MD5)
	}

	// 需要密码：提交密码。
	if res.DirectURL == "" && res.NeedPassword {
		if strings.TrimSpace(password) == "" {
			return res, ErrPasswordRequired
		}
		c.logInfof("提交下载密码: %s", maskPassword(password))
		nextDoc, err := c.submit(ctx, pageURL, pageURL, url.Values{
			"password": {password},
			"yes":      {"認証"},
		})
		if err != nil {
			return res, err
		}
		if nextDoc == nil {
			return res, errors.New("密码提交后返回了非 HTML 响应")
		}
		newMeta := parseMeta(nextDoc, pageURL)
		if newMeta.NeedPassword && newMeta.Token == "" {
			return res, ErrPasswordRejected
		}
		if newMeta.Filename != "" {
			res.Filename = newMeta.Filename
		}
		if newMeta.SizeText != "" {
			res.SizeText = newMeta.SizeText
		}
		if newMeta.MD5 != "" {
			res.MD5 = newMeta.MD5
		}
		res.Token = newMeta.Token
		res.DirectURL = newMeta.DirectURL
		c.logInfof("密码认证成功")
	}

	// 点击「下载」按钮，换取真实文件地址。
	var direct string
	switch {
	case res.DirectURL != "":
		direct = res.DirectURL
	case res.Token != "":
		c.logInfof("请求下载链接...")
		d, err := c.confirm(ctx, pageURL, res.Token)
		if err != nil {
			return res, err
		}
		direct = d
	default:
		return res, errors.New("页面上没有找到密码框或下载按钮，链接可能已失效")
	}

	if direct == "" {
		return res, errors.New("未能从页面中解析出真实下载地址")
	}
	res.DirectURL = direct
	c.logDebugf("真实地址: %s", direct)

	// 下载文件。
	if err := util.EnsureDir(destDir); err != nil {
		return res, fmt.Errorf("创建目录失败: %w", err)
	}
	if err := c.sleep(ctx); err != nil {
		return res, err
	}
	req, err := c.hc.Request(ctx, http.MethodGet, direct, nil)
	if err != nil {
		return res, err
	}
	req.Header.Set("Referer", pageURL)
	resp, err := c.hc.HTTP.Do(req)
	if err != nil {
		return res, fmt.Errorf("下载失败: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		return res, fmt.Errorf("下载失败: HTTP %d", resp.StatusCode)
	}
	if isHTMLResponse(resp) {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
		if isCloudflare(body) {
			return res, errors.New("被 Cloudflare 拦截，请检查代理设置")
		}
		return res, fmt.Errorf("下载地址返回了 HTML 而不是文件：%s", util.Truncate(util.OneLine(string(body)), 120))
	}
	return c.saveResponse(ctx, resp, res, direct, destDir, force)
}

// loadPage 抓取并解析页面。
func (c *Client) loadPage(ctx context.Context, rawURL, referer string) (*html.Node, error) {
	if err := c.sleep(ctx); err != nil {
		return nil, err
	}
	pageCtx, cancel := c.hc.PageContext(ctx)
	defer cancel()
	resp, err := c.hc.Get(pageCtx, rawURL)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		return nil, fmt.Errorf("打开页面失败: HTTP %d %s", resp.StatusCode, rawURL)
	}
	if !isHTMLResponse(resp) {
		return nil, errors.New("页面返回了非 HTML 内容")
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 16<<20))
	if err != nil {
		return nil, err
	}
	if isCloudflare(body) {
		return nil, errors.New("被 Cloudflare 拦截，请检查代理设置（需要能正常访问 uploader.jp 的代理）")
	}
	return html.Parse(strings.NewReader(string(body)))
}

// submit 提交表单并解析返回的页面。
func (c *Client) submit(ctx context.Context, pageURL, referer string, values url.Values) (*html.Node, error) {
	if err := c.sleep(ctx); err != nil {
		return nil, err
	}
	pageCtx, cancel := c.hc.PageContext(ctx)
	defer cancel()
	resp, err := c.hc.PostForm(pageCtx, pageURL, values.Encode(), referer)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		return nil, fmt.Errorf("提交表单失败: HTTP %d", resp.StatusCode)
	}
	if !isHTMLResponse(resp) {
		return nil, errors.New("表单返回了非 HTML 内容")
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 16<<20))
	if err != nil {
		return nil, err
	}
	if isCloudflare(body) {
		return nil, errors.New("被 Cloudflare 拦截，请检查代理设置")
	}
	return html.Parse(strings.NewReader(string(body)))
}

// confirm 提交 token，返回真实下载地址。
func (c *Client) confirm(ctx context.Context, pageURL, token string) (string, error) {
	doc, err := c.submit(ctx, pageURL, pageURL, url.Values{
		"token": {token},
		"yes":   {"ダウンロード"},
	})
	if err != nil {
		return "", err
	}
	if u := directURLFromDoc(doc); u != "" {
		return u, nil
	}
	return "", errors.New("确认下载后仍未找到真实下载地址")
}

// saveResponse 把响应写入文件，并计算哈希。
func (c *Client) saveResponse(ctx context.Context, resp *http.Response, res *Result, direct, destDir string, force bool) (*Result, error) {
	pageMD5 := res.MD5 // 页面声明的 MD5（密码认证后可能才出现）
	filename := filenameFor(resp, direct, res.Filename)
	if filename == "" {
		filename = "getuploader-download.bin"
	}
	safeName := util.SanitizeName(filename)
	finalPath := filepath.Join(destDir, safeName)

	expected := resp.ContentLength
	if !force {
		if info, err := os.Stat(finalPath); err == nil {
			if expected <= 0 || info.Size() == expected {
				md5sum, sha, err := hashFile(finalPath)
				if err == nil {
					if pageMD5 != "" && !strings.EqualFold(md5sum, pageMD5) {
						c.logWarnf("已存在的文件 MD5 与页面不一致，重新下载: %s", safeName)
					} else {
						res.Filename = safeName
						res.Path = finalPath
						res.Bytes = info.Size()
						res.MD5 = md5sum
						res.SHA256 = sha
						res.AlreadyExisted = true
						c.logInfof("文件已存在，跳过下载: %s (%s)", finalPath, util.HumanBytes(info.Size()))
						return res, nil
					}
				}
			}
		}
	}

	tmpPath := finalPath + ".part"
	_ = os.Remove(tmpPath)
	f, err := os.Create(tmpPath)
	if err != nil {
		return res, fmt.Errorf("创建文件失败: %w", err)
	}
	hMD5 := md5.New()
	hSHA := sha256.New()
	pw := &progressWriter{
		total: expected,
		log:   c.log,
		label: safeName,
	}
	writer := io.MultiWriter(f, hMD5, hSHA, pw)

	c.logInfof("开始下载 -> %s", finalPath)
	start := time.Now()
	n, copyErr := copyWithContext(ctx, writer, resp.Body)
	syncErr := f.Sync()
	closeErr := f.Close()
	if copyErr != nil {
		_ = os.Remove(tmpPath)
		return res, fmt.Errorf("下载中断: %w", copyErr)
	}
	if syncErr != nil || closeErr != nil {
		_ = os.Remove(tmpPath)
		return res, fmt.Errorf("写入文件失败: %v %v", syncErr, closeErr)
	}
	if expected > 0 && n != expected {
		_ = os.Remove(tmpPath)
		return res, fmt.Errorf("文件大小不完整：期望 %d，实际 %d", expected, n)
	}
	if err := os.Rename(tmpPath, finalPath); err != nil {
		_ = os.Remove(tmpPath)
		return res, fmt.Errorf("重命名文件失败: %w", err)
	}

	res.Filename = safeName
	res.Path = finalPath
	res.Bytes = n
	res.MD5 = hex.EncodeToString(hMD5.Sum(nil))
	res.SHA256 = hex.EncodeToString(hSHA.Sum(nil))

	if pageMD5 != "" && !strings.EqualFold(res.MD5, pageMD5) {
		c.logWarnf("MD5 校验不一致（页面: %s / 实际: %s）", pageMD5, res.MD5)
	} else if pageMD5 != "" {
		c.logInfof("MD5 校验通过")
	}
	elapsed := time.Since(start)
	speed := float64(n) / elapsed.Seconds()
	c.logInfof("下载完成: %s (%s, %.1f MB/s, 用时 %s)", finalPath, util.HumanBytes(n), speed/1024/1024, elapsed.Round(time.Millisecond))
	return res, nil
}

func copyWithContext(ctx context.Context, dst io.Writer, src io.Reader) (int64, error) {
	buf := make([]byte, 128*1024)
	var written int64
	for {
		if err := ctx.Err(); err != nil {
			return written, err
		}
		n, rerr := src.Read(buf)
		if n > 0 {
			if _, werr := dst.Write(buf[:n]); werr != nil {
				return written, werr
			}
			written += int64(n)
		}
		if rerr == io.EOF {
			return written, nil
		}
		if rerr != nil {
			return written, rerr
		}
	}
}

// ---------- 解析 ----------

func parseMeta(doc *html.Node, pageURL string) Meta {
	m := Meta{PageURL: pageURL}

	// 密码框。
	m.NeedPassword = htmlx.Find(doc, func(n *html.Node) bool {
		return htmlx.IsTag(n, "input") && strings.EqualFold(htmlx.Attr(n, "type"), "password")
	}) != nil

	// token。
	if tok := htmlx.Find(doc, func(n *html.Node) bool {
		return htmlx.IsTag(n, "input") && strings.EqualFold(htmlx.Attr(n, "name"), "token")
	}); tok != nil {
		m.Token = htmlx.Attr(tok, "value")
	}

	// meta refresh 直接给出的地址。
	m.DirectURL = directURLFromDoc(doc)

	// 文件名/容量/MD5：解析 Download Details 表格。
	for _, tr := range htmlx.ByTag(doc, "tr") {
		th := htmlx.FirstTag(tr, "th")
		td := htmlx.FirstTag(tr, "td")
		if th == nil || td == nil {
			continue
		}
		key := strings.TrimSpace(htmlx.RawText(th))
		val := strings.TrimSpace(htmlx.RawText(td))
		switch {
		case key == "ファイル":
			m.Filename = val
		case key == "容量":
			m.SizeText = val
		}
	}
	// MD5：页面上是只读 input。
	if m.MD5 == "" {
		for _, in := range htmlx.ByTag(doc, "input") {
			v := strings.TrimSpace(htmlx.Attr(in, "value"))
			if reMD5.MatchString(v) {
				m.MD5 = strings.ToLower(v)
				break
			}
		}
	}
	// 文件名兜底：h1.lead 中的 <strong> 或 og:title。
	if m.Filename == "" {
		if lead := htmlx.FirstByClass(doc, "lead"); lead != nil {
			if s := htmlx.FirstTag(lead, "strong"); s != nil {
				m.Filename = strings.TrimSpace(htmlx.RawText(s))
			}
		}
	}
	if m.Filename == "" {
		if og := htmlx.MetaContent(doc, "property", "og:title"); og != "" {
			if i := strings.Index(og, " ("); i > 0 {
				m.Filename = strings.TrimSpace(og[:i])
			}
		}
	}
	return m
}

var reMD5 = regexp.MustCompile(`^[a-fA-F0-9]{32}$`)

var reMetaRefresh = regexp.MustCompile(`(?i)^\s*\d+\s*;\s*url\s*=\s*(.+?)\s*$`)

func directURLFromDoc(doc *html.Node) string {
	if doc == nil {
		return ""
	}
	for _, meta := range htmlx.ByTag(doc, "meta") {
		if !strings.EqualFold(htmlx.Attr(meta, "http-equiv"), "refresh") {
			continue
		}
		content := htmlx.Attr(meta, "content")
		if m := reMetaRefresh.FindStringSubmatch(content); m != nil {
			u := strings.Trim(m[1], `"'`)
			u = strings.ReplaceAll(u, " ", "%20")
			return u
		}
	}
	// 有些页面会直接给出 Download Now 链接。
	for _, a := range htmlx.ByTag(doc, "a") {
		href := htmlx.Attr(a, "href")
		if strings.Contains(href, "downloadx.getuploader.com") {
			return href
		}
	}
	return ""
}

func filenameFor(resp *http.Response, direct, pageFilename string) string {
	if cd := resp.Header.Get("Content-Disposition"); cd != "" {
		if _, params, err := mime.ParseMediaType(cd); err == nil {
			if fn := params["filename"]; fn != "" {
				return fn
			}
		}
	}
	if direct != "" {
		if u, err := url.Parse(direct); err == nil {
			base := path.Base(u.Path)
			if base != "" && base != "/" && base != "." {
				if dec, err := url.PathUnescape(base); err == nil && dec != "" {
					return dec
				}
				return base
			}
		}
	}
	return pageFilename
}

func isHTMLResponse(resp *http.Response) bool {
	ct := strings.ToLower(resp.Header.Get("Content-Type"))
	return strings.HasPrefix(ct, "text/html") || strings.HasPrefix(ct, "application/xhtml")
}

func isCloudflare(body []byte) bool {
	s := string(body)
	return strings.Contains(s, "Just a moment...") || strings.Contains(s, "cf-browser-verification") || strings.Contains(s, "Enable JavaScript and cookies to continue")
}

func hashFile(p string) (md5sum, sha string, err error) {
	f, err := os.Open(p)
	if err != nil {
		return "", "", err
	}
	defer f.Close()
	hMD5 := md5.New()
	hSHA := sha256.New()
	if _, err := io.Copy(io.MultiWriter(hMD5, hSHA), f); err != nil {
		return "", "", err
	}
	return hex.EncodeToString(hMD5.Sum(nil)), hex.EncodeToString(hSHA.Sum(nil)), nil
}

func (c *Client) sleep(ctx context.Context) error {
	if c.delay <= 0 {
		return nil
	}
	select {
	case <-time.After(c.delay):
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func orDefault(s, def string) string {
	if strings.TrimSpace(s) == "" {
		return def
	}
	return s
}

func maskPassword(p string) string {
	rs := []rune(p)
	if len(rs) <= 2 {
		return strings.Repeat("*", len(rs))
	}
	return string(rs[0]) + strings.Repeat("*", len(rs)-2) + string(rs[len(rs)-1])
}

func (c *Client) logInfof(format string, args ...any) {
	if c.log != nil {
		c.log.Infof(format, args...)
	}
}

func (c *Client) logWarnf(format string, args ...any) {
	if c.log != nil {
		c.log.Warnf(format, args...)
	}
}

func (c *Client) logDebugf(format string, args ...any) {
	if c.log != nil {
		c.log.Debugf(format, args...)
	}
}

type progressWriter struct {
	total   int64
	log     *logx.Logger
	label   string
	last    time.Time
	start   time.Time
	written int64
}

func (w *progressWriter) Write(p []byte) (int, error) {
	n := len(p)
	w.written += int64(n)
	now := time.Now()
	if w.start.IsZero() {
		w.start = now
		w.last = now
	}
	if w.log != nil && now.Sub(w.last) >= 2*time.Second {
		w.last = now
		elapsed := now.Sub(w.start).Seconds()
		speed := float64(w.written) / elapsed
		if w.total > 0 {
			pct := float64(w.written) / float64(w.total) * 100
			w.log.Infof("  进度 %5.1f%%  %s / %s  (%s/s)", pct, util.HumanBytes(w.written), util.HumanBytes(w.total), util.HumanBytes(int64(speed)))
		} else {
			w.log.Infof("  已下载 %s  (%s/s)", util.HumanBytes(w.written), util.HumanBytes(int64(speed)))
		}
	}
	return n, nil
}

// Hasher 组合哈希（预留给将来的校验需求）。
type Hasher struct {
	MD5    hash.Hash
	SHA256 hash.Hash
}

// NewHasher 创建组合哈希。
func NewHasher() *Hasher {
	return &Hasher{MD5: md5.New(), SHA256: sha256.New()}
}

// Sum 返回十六进制摘要。
func (h *Hasher) Sum() (string, string) {
	return hex.EncodeToString(h.MD5.Sum(nil)), hex.EncodeToString(h.SHA256.Sum(nil))
}

// ParseSizeText 把「11.1 MB」之类的文本转成字节数（粗略）。
func ParseSizeText(s string) int64 {
	s = strings.TrimSpace(strings.ToUpper(s))
	re := regexp.MustCompile(`^([0-9.]+)\s*(B|KB|MB|GB|TB)$`)
	m := re.FindStringSubmatch(s)
	if m == nil {
		return 0
	}
	f, err := strconv.ParseFloat(m[1], 64)
	if err != nil {
		return 0
	}
	mult := map[string]float64{"B": 1, "KB": 1 << 10, "MB": 1 << 20, "GB": 1 << 30, "TB": 1 << 40}[m[2]]
	return int64(f * mult)
}
