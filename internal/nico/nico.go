// Package nico 负责访问 nico seiga（静画）页面与 API。
package nico

import (
	"bytes"
	"context"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"golang.org/x/net/html"

	"nico-seiga-archiver/internal/extract"
	"nico-seiga-archiver/internal/htmlx"
	"nico-seiga-archiver/internal/httpclient"
	"nico-seiga-archiver/internal/logx"
	"nico-seiga-archiver/internal/util"
)

// ErrLoginRequired 表示页面需要登录（通常是搜索页）。
var ErrLoginRequired = errors.New("该页面需要登录 nico 帐号：请在 cfg 中设置 nico_cookie")

// Illust 是一张静画（投稿）的信息。
type Illust struct {
	URL         string
	ID          string
	Title       string
	Author      string
	AuthorID    string
	AuthorURL   string
	Description string
	// Truncated 表示简介是被截断的（以 … 结尾）。
	Truncated bool
	// DescriptionFromAPI 表示简介取自 API（通常完整）。
	DescriptionFromAPI bool
}

// Item 是列表页里的一个条目。
type Item struct {
	URL   string
	ID    string
	Title string
}

// ListPage 是一个列表页的解析结果。
type ListPage struct {
	URL     string
	Items   []Item
	NextURL string
}

// Client 是 seiga 客户端。
type Client struct {
	hc    *httpclient.Client
	log   *logx.Logger
	delay time.Duration

	mu        sync.Mutex
	userCache map[string]string
}

// New 创建客户端。
func New(hc *httpclient.Client, log *logx.Logger, delay time.Duration) *Client {
	return &Client{hc: hc, log: log, delay: delay, userCache: map[string]string{}}
}

var (
	reIllustID = regexp.MustCompile(`(?i)/seiga/im(\d+)`)
	reUserID   = regexp.MustCompile(`/user/illust/(\d+)`)
	reAnyID    = regexp.MustCompile(`^\d+$`)
)

// NormalizeIllustURL 把各种形式的输入规范成静画页面地址。
func NormalizeIllustURL(input string) (pageURL, id string, err error) {
	input = strings.TrimSpace(input)
	if m := reIllustID.FindStringSubmatch(input); m != nil {
		return "https://seiga.nicovideo.jp/seiga/im" + m[1], m[1], nil
	}
	trimmed := strings.TrimPrefix(strings.TrimPrefix(strings.ToLower(input), "im"), "im")
	if reAnyID.MatchString(trimmed) {
		return "https://seiga.nicovideo.jp/seiga/im" + trimmed, trimmed, nil
	}
	return "", "", fmt.Errorf("无法识别的静画地址: %s", input)
}

// NormalizeUserURL 解析作者主页地址，返回规范 URL 与作者 ID。
func NormalizeUserURL(input string) (pageURL, id string, err error) {
	input = strings.TrimSpace(input)
	if m := reUserID.FindStringSubmatch(input); m != nil {
		return "https://seiga.nicovideo.jp/user/illust/" + m[1], m[1], nil
	}
	if reAnyID.MatchString(input) {
		return "https://seiga.nicovideo.jp/user/illust/" + input, input, nil
	}
	return "", "", fmt.Errorf("无法识别的作者主页地址: %s", input)
}

// SearchURLFromKeyword 用关键词构造静画搜索地址。
func SearchURLFromKeyword(keyword string) string {
	q := strings.TrimSpace(keyword)
	u := url.URL{
		Scheme:   "https",
		Host:     "seiga.nicovideo.jp",
		Path:     "/search/" + q,
		RawQuery: "target=illust",
	}
	return u.String()
}

// reTagPath 匹配 /tag/<name> 或 tag/<name> 形式。
var reTagPath = regexp.MustCompile(`(?i)(?:^|/)tag/([^/?#]+)`)

// NormalizeTagURL 把「tag 名」「tag/xxx」「完整 tag 页面地址」统一成规范地址。
// 返回值 tag 是解码后的 tag 名（例如 VOICEROID、立ち絵）。
func NormalizeTagURL(input string) (pageURL, tag string, err error) {
	raw := strings.TrimSpace(input)
	if raw == "" {
		return "", "", fmt.Errorf("tag 不能为空")
	}
	// 已经是完整 URL 或带 /tag/ 的路径。
	if m := reTagPath.FindStringSubmatch(raw); m != nil {
		name, derr := url.PathUnescape(m[1])
		if derr != nil {
			name = m[1]
		}
		name = strings.TrimSpace(name)
		if name == "" {
			return "", "", fmt.Errorf("无法识别的 tag 地址: %s", input)
		}
		return "https://seiga.nicovideo.jp/tag/" + url.PathEscape(name), name, nil
	}
	if strings.Contains(raw, "://") {
		return "", "", fmt.Errorf("不是 tag 页面地址: %s（应形如 https://seiga.nicovideo.jp/tag/VOICEROID）", input)
	}
	// 直接给 tag 名。
	name := strings.TrimPrefix(raw, "#")
	name = strings.TrimSpace(name)
	// 用户可能粘贴了已经编码过的 tag。
	if decoded, derr := url.PathUnescape(name); derr == nil {
		name = decoded
	}
	if name == "" {
		return "", "", fmt.Errorf("tag 不能为空")
	}
	return "https://seiga.nicovideo.jp/tag/" + url.PathEscape(name), name, nil
}

// TagProgressKey 返回 tag 索引用于记录进度的键。
func TagProgressKey(tag string) string { return "tag:" + tag }

// SearchProgressKey 返回搜索索引用于记录进度的键。
func SearchProgressKey(keywordOrURL string) string {
	return "search:" + strings.TrimSpace(keywordOrURL)
}

// AuthorProgressKey 返回作者索引用于记录进度的键。
func AuthorProgressKey(userID string) string { return "author:" + userID }

// WithPage 给 URL 设置 page 参数。
func WithPage(rawURL string, page int) (string, error) {
	u, err := url.Parse(strings.TrimSpace(rawURL))
	if err != nil {
		return "", fmt.Errorf("URL 无效: %w", err)
	}
	if page <= 0 {
		page = 1
	}
	q := u.Query()
	q.Set("page", strconv.Itoa(page))
	u.RawQuery = q.Encode()
	return u.String(), nil
}

// PageOf 读取 URL 中的 page 参数（默认 1）。
func PageOf(rawURL string) int {
	u, err := url.Parse(rawURL)
	if err != nil {
		return 1
	}
	n, err := strconv.Atoi(u.Query().Get("page"))
	if err != nil || n <= 0 {
		return 1
	}
	return n
}

// FetchIllust 获取单张静画的信息。
// 优先走 XML API（简介完整），失败时退回解析页面 HTML。
func (c *Client) FetchIllust(ctx context.Context, input string) (*Illust, error) {
	pageURL, id, err := NormalizeIllustURL(input)
	if err != nil {
		return nil, err
	}
	ill := &Illust{URL: pageURL, ID: id}

	// 1) 官方 API：/api/illust/info?id=xxx，未登录也能拿到完整简介。
	apiOK := false
	if apiInfo, err := c.fetchIllustAPI(ctx, id); err == nil && apiInfo != nil {
		apiOK = true
		ill.Title = apiInfo.Image.Title
		ill.Description = apiInfo.Image.Description
		ill.AuthorID = apiInfo.Image.UserID
		ill.DescriptionFromAPI = true
		if apiInfo.Image.UserID != "" {
			ill.AuthorURL = "https://seiga.nicovideo.jp/user/illust/" + apiInfo.Image.UserID
			if name, err := c.FetchUser(ctx, apiInfo.Image.UserID); err == nil {
				ill.Author = name
			}
		}
	} else if err != nil && c.log != nil {
		c.log.Debugf("静画 API 获取失败，改用页面解析: %v", err)
	}

	// API 已经把标题/作者/简介都拿到了，就不必再抓页面。
	if apiOK && ill.Title != "" && ill.Description != "" && ill.Author != "" {
		if strings.HasSuffix(strings.TrimSpace(ill.Description), "…") {
			ill.Truncated = true
		}
		return ill, nil
	}

	// 2) 页面 HTML：补齐标题/作者/简介。
	doc, finalURL, err := c.getHTML(ctx, pageURL)
	if err != nil {
		if apiOK || ill.Title != "" || ill.Description != "" {
			// API 已经拿到核心信息，页面失败不致命。
			if c.log != nil {
				c.log.Warnf("静画页面获取失败（%v），仅使用 API 数据", err)
			}
			if ill.Author == "" {
				ill.Author = "unknown"
			}
			if ill.Title == "" {
				ill.Title = "無題"
			}
			return ill, nil
		}
		return nil, err
	}
	ill.URL = stripQuery(finalURL)

	if t := pageTitle(doc); ill.Title == "" {
		ill.Title = t
	}
	if a, aid := pageAuthor(doc); ill.Author == "" || ill.AuthorID == "" {
		if ill.Author == "" {
			ill.Author = a
		}
		if ill.AuthorID == "" {
			ill.AuthorID = aid
			if aid != "" {
				ill.AuthorURL = "https://seiga.nicovideo.jp/user/illust/" + aid
				if ill.Author == "" {
					if name, err := c.FetchUser(ctx, aid); err == nil {
						ill.Author = name
					}
				}
			}
		}
	}
	if ill.Description == "" {
		ill.Description = pageDescription(doc)
	}
	if ill.Description == "" {
		if m := htmlx.MetaContent(doc, "property", "og:description"); m != "" {
			ill.Description = m
		}
	}
	if strings.HasSuffix(strings.TrimSpace(ill.Description), "…") || strings.HasSuffix(strings.TrimSpace(ill.Description), "...") {
		ill.Truncated = true
	}
	if ill.Title == "" {
		ill.Title = "無題"
	}
	if ill.Author == "" {
		ill.Author = "unknown"
	}
	return ill, nil
}

// FetchUser 通过 API 获取作者昵称。
func (c *Client) FetchUser(ctx context.Context, userID string) (string, error) {
	userID = strings.TrimSpace(userID)
	if userID == "" {
		return "", errors.New("空的作者 ID")
	}
	c.mu.Lock()
	if v, ok := c.userCache[userID]; ok {
		c.mu.Unlock()
		return v, nil
	}
	c.mu.Unlock()

	apiURL := "https://seiga.nicovideo.jp/api/user/info?id=" + url.QueryEscape(userID)
	body, err := c.getBytes(ctx, apiURL)
	if err != nil {
		return "", err
	}
	var resp struct {
		User struct {
			ID       string `xml:"id"`
			Nickname string `xml:"nickname"`
		} `xml:"user"`
	}
	if err := xml.Unmarshal(body, &resp); err != nil {
		return "", err
	}
	name := strings.TrimSpace(resp.User.Nickname)
	if name != "" {
		c.mu.Lock()
		c.userCache[userID] = name
		c.mu.Unlock()
	}
	return name, nil
}

// ListPage 抓取并解析一个列表页（搜索页或作者主页）。
func (c *Client) ListPage(ctx context.Context, rawURL string) (*ListPage, error) {
	doc, finalURL, err := c.getHTML(ctx, rawURL)
	if err != nil {
		return nil, err
	}
	return parseListPage(doc, finalURL), nil
}

// parseListPage 从 DOM 中解析列表项与下一页地址。
func parseListPage(doc *html.Node, finalURL string) *ListPage {
	page := &ListPage{URL: finalURL}

	container := htmlx.FirstByClass(doc, "illust_list")
	if container == nil {
		container = doc
	}
	seen := map[string]bool{}
	for _, a := range htmlx.FindAll(container, func(n *html.Node) bool { return htmlx.IsTag(n, "a") }) {
		href := htmlx.Attr(a, "href")
		m := reIllustID.FindStringSubmatch(href)
		if m == nil {
			continue
		}
		id := m[1]
		if seen[id] {
			continue
		}
		seen[id] = true
		item := Item{
			URL: "https://seiga.nicovideo.jp/seiga/im" + id,
			ID:  id,
		}
		// 标题：优先 .title，其次 img@alt。
		if li := closestListItem(a); li != nil {
			if t := htmlx.FirstByClass(li, "title"); t != nil {
				item.Title = htmlx.RawText(t)
			}
			if item.Title == "" {
				if img := htmlx.FirstTag(li, "img"); img != nil {
					item.Title = htmlx.Attr(img, "alt")
				}
			}
		}
		page.Items = append(page.Items, item)
	}

	// 下一页：li.next a[href] 或 a[rel=next]
	next := htmlx.Find(doc, func(n *html.Node) bool {
		return htmlx.IsTag(n, "a") && htmlx.Attr(n, "rel") == "next" && htmlx.Attr(n, "href") != ""
	})
	if next == nil {
		next = htmlx.Find(doc, func(n *html.Node) bool {
			return htmlx.IsTag(n, "li") && htmlx.HasClass(n, "next")
		})
		if next != nil {
			next = htmlx.FirstTag(next, "a")
		}
	}
	if next != nil {
		if abs, err := httpclient.ResolveURL(finalURL, htmlx.Attr(next, "href")); err == nil {
			page.NextURL = abs
		}
	}
	return page
}

func closestListItem(n *html.Node) *html.Node {
	for p := n.Parent; p != nil; p = p.Parent {
		if htmlx.IsTag(p, "li") && htmlx.HasClass(p, "list_item") {
			return p
		}
		if htmlx.IsTag(p, "body") {
			break
		}
	}
	return nil
}

// ---------- 解析辅助 ----------

func pageTitle(doc *html.Node) string {
	candidates := []*html.Node{
		htmlx.FirstByClass(doc, "illust_title"),
		htmlx.Find(doc, func(n *html.Node) bool { return htmlx.IsTag(n, "div") && htmlx.HasClass(n, "lg_ttl_illust") }),
	}
	for _, cand := range candidates {
		if cand == nil {
			continue
		}
		if htmlx.IsTag(cand, "div") {
			if h := htmlx.FirstTag(cand, "h1"); h != nil {
				if t := htmlx.RawText(h); t != "" {
					return t
				}
			}
			continue
		}
		if t := htmlx.RawText(cand); t != "" {
			return t
		}
	}
	if h := htmlx.FirstTag(doc, "h1"); h != nil {
		if t := htmlx.RawText(h); t != "" && !strings.Contains(t, "ニコニコ") {
			return t
		}
	}
	if t := htmlx.MetaContent(doc, "property", "og:title"); t != "" {
		return cleanNicoTitle(t)
	}
	if t := htmlx.FirstTag(doc, "title"); t != nil {
		return cleanNicoTitle(htmlx.RawText(t))
	}
	return ""
}

func cleanNicoTitle(t string) string {
	t = strings.TrimSpace(t)
	for _, suffix := range []string{" - ニコニコ静画 (イラスト)", " - ニコニコ静画", "｜ニコニコ静画", " | ニコニコ静画"} {
		t = strings.TrimSuffix(t, suffix)
	}
	return strings.TrimSpace(t)
}

func pageAuthor(doc *html.Node) (name, id string) {
	// 面包屑里的作者链接最可靠。
	if pankuzu := htmlx.FirstByClass(doc, "sg_pankuzu"); pankuzu != nil {
		for _, a := range htmlx.FindAll(pankuzu, func(n *html.Node) bool { return htmlx.IsTag(n, "a") }) {
			href := htmlx.Attr(a, "href")
			if m := reUserID.FindStringSubmatch(href); m != nil {
				id = m[1]
				name = cleanAuthorName(htmlx.RawText(a))
				return name, id
			}
		}
	}
	// 其他位置的作者链接。
	for _, a := range htmlx.FindAll(doc, func(n *html.Node) bool { return htmlx.IsTag(n, "a") }) {
		href := htmlx.Attr(a, "href")
		if m := reUserID.FindStringSubmatch(href); m != nil {
			t := cleanAuthorName(htmlx.RawText(a))
			if t == "" || strings.Contains(t, "さんのイラスト") {
				continue
			}
			return t, m[1]
		}
	}
	return "", ""
}

func cleanAuthorName(s string) string {
	s = htmlx.Unescape(strings.TrimSpace(s))
	s = strings.ReplaceAll(s, "\u00a0", " ")
	for _, suffix := range []string{"さんのイラスト", "さんの作品", "さん"} {
		if strings.HasSuffix(s, suffix) {
			s = strings.TrimSuffix(s, suffix)
			break
		}
	}
	return strings.TrimSpace(s)
}

func pageDescription(doc *html.Node) string {
	var cands []*html.Node
	for _, id := range []string{"ko_illust_body", "illust_body", "ko_illust_description"} {
		if n := htmlx.ByID(doc, id); n != nil {
			cands = append(cands, n)
		}
	}
	for _, cls := range []string{"illust_body", "illust_description", "lg_txt_illust", "illust_caption"} {
		cands = append(cands, htmlx.ByClass(doc, cls)...)
	}
	best := ""
	for _, n := range cands {
		t := strings.TrimSpace(textSkippingStruck(n))
		if len([]rune(t)) > len([]rune(best)) {
			best = t
		}
	}
	return best
}

// textSkippingStruck 取节点文本，但整段跳过被删除线标记的子元素
// （<s> <del> <strike> 或 style 含 line-through）。
func textSkippingStruck(n *html.Node) string {
	var b strings.Builder
	var walk func(*html.Node)
	walk = func(node *html.Node) {
		if isStruckNode(node) {
			return
		}
		if node.Type == html.TextNode {
			b.WriteString(node.Data)
			return
		}
		if node.Type == html.ElementNode {
			switch strings.ToLower(node.Data) {
			case "br":
				b.WriteString("\n")
				return
			case "script", "style", "noscript":
				return
			}
		}
		for c := node.FirstChild; c != nil; c = c.NextSibling {
			walk(c)
		}
	}
	walk(n)
	return b.String()
}

func isStruckNode(n *html.Node) bool {
	if n == nil || n.Type != html.ElementNode {
		return false
	}
	switch strings.ToLower(n.Data) {
	case "s", "del", "strike", "strikethrough":
		return true
	}
	style := strings.ToLower(htmlx.Attr(n, "style"))
	return strings.Contains(style, "line-through")
}

// ---------- HTTP 辅助 ----------

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

func (c *Client) getHTML(ctx context.Context, rawURL string) (*html.Node, string, error) {
	body, finalURL, err := c.getBody(ctx, rawURL)
	if err != nil {
		return nil, "", err
	}
	doc, err := htmlx.Parse(bytes.NewReader(body))
	if err != nil {
		return nil, "", err
	}
	return doc, finalURL, nil
}

func (c *Client) getBytes(ctx context.Context, rawURL string) ([]byte, error) {
	body, _, err := c.getBody(ctx, rawURL)
	return body, err
}

func (c *Client) getBody(ctx context.Context, rawURL string) ([]byte, string, error) {
	if err := c.sleep(ctx); err != nil {
		return nil, "", err
	}
	pageCtx, cancel := c.hc.PageContext(ctx)
	defer cancel()
	resp, err := c.hc.Get(pageCtx, rawURL)
	if err != nil {
		return nil, "", err
	}
	defer resp.Body.Close()

	final := rawURL
	if resp.Request != nil && resp.Request.URL != nil {
		final = resp.Request.URL.String()
		if isLoginURL(resp.Request.URL) {
			io.Copy(io.Discard, io.LimitReader(resp.Body, 4096))
			return nil, final, ErrLoginRequired
		}
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 32<<20))
	if err != nil {
		return nil, final, err
	}
	if resp.StatusCode >= 400 {
		return nil, final, fmt.Errorf("HTTP %d: %s", resp.StatusCode, rawURL)
	}
	if bytes.Contains(body, []byte("Just a moment...")) || bytes.Contains(body, []byte("cf-browser-verification")) {
		return nil, final, fmt.Errorf("被 Cloudflare 拦截（请检查代理是否可用）: %s", rawURL)
	}
	return body, final, nil
}

func isLoginURL(u *url.URL) bool {
	if u == nil {
		return false
	}
	host := strings.ToLower(u.Host)
	return strings.Contains(host, "account.nicovideo.jp") || strings.HasPrefix(u.Path, "/login")
}

func stripQuery(raw string) string {
	u, err := url.Parse(raw)
	if err != nil {
		return raw
	}
	u.RawQuery = ""
	u.Fragment = ""
	return u.String()
}

// ---------- XML API ----------

type illustAPIResponse struct {
	Image struct {
		ID          string `xml:"id"`
		UserID      string `xml:"user_id"`
		Title       string `xml:"title"`
		Description string `xml:"description"`
		Created     string `xml:"created"`
	} `xml:"image"`
}

func (c *Client) fetchIllustAPI(ctx context.Context, id string) (*illustAPIResponse, error) {
	apiURL := "https://seiga.nicovideo.jp/api/illust/info?id=" + url.QueryEscape(id)
	body, err := c.getBytes(ctx, apiURL)
	if err != nil {
		return nil, err
	}
	var resp illustAPIResponse
	if err := xml.Unmarshal(body, &resp); err != nil {
		return nil, err
	}
	if resp.Image.ID == "" {
		return nil, fmt.Errorf("API 未返回静画信息: %s", id)
	}
	resp.Image.Title = strings.TrimSpace(resp.Image.Title)
	resp.Image.Description = normalizeDescription(resp.Image.Description)
	return &resp, nil
}

func normalizeDescription(s string) string {
	s = strings.ReplaceAll(s, "\r\n", "\n")
	s = strings.ReplaceAll(s, "\r", "\n")
	s = markStrikethrough(s)
	extra := anchorGetuploaderLinks(s)
	s = stripHTML(s)
	if len(extra) > 0 {
		s = strings.TrimSpace(s) + "\n" + strings.Join(extra, "\n")
	}
	return strings.TrimSpace(s)
}

// 删除线哨兵：与 internal/extract 约定一致。
const (
	strikeMarkOpen  = "\x01"
	strikeMarkClose = "\x02"
)

var (
	reStrikeOpenTag   = regexp.MustCompile(`(?i)<\s*(?:s|del|strike|strikethrough)\b[^>]*>`)
	reStrikeCloseTag  = regexp.MustCompile(`(?i)<\s*/\s*(?:s|del|strike|strikethrough)\s*>`)
	reAnyHTMLTag      = regexp.MustCompile(`(?i)<\s*(/?)\s*[a-z0-9]+\b[^>]*>`)
	reStrikeMarkCheck = regexp.MustCompile(`[\x01\x02]`)
)

// markStrikethrough 把简介里的删除线统一成哨兵字符，交由 extract 判断哪些链接要跳过。
//
// 支持的写法见 extract.MaskStrike：<s>/<del>/<strike>、style 含 line-through、
// ~~文本~~、Unicode 组合删除线。标签不配对时不做标记，避免误伤整段文字。
func markStrikethrough(s string) string {
	return extract.MaskStrike(s)
}

var reAnchorHref = regexp.MustCompile(`(?i)<a\b[^>]*?href\s*=\s*["']([^"']+)["']`)

// anchorGetuploaderLinks 把简介中 <a href="...">文字</a> 的目标地址也提取出来，
// 以防链接地址藏在属性里（正文只写了「こちら」之类）。
// 被删除线划掉的 <a> 会被跳过。
func anchorGetuploaderLinks(s string) []string {
	var out []string
	seen := map[string]bool{}
	for _, loc := range reAnchorHref.FindAllStringSubmatchIndex(s, -1) {
		if loc[2] < 0 || loc[3] < 0 {
			continue
		}
		u := html.UnescapeString(strings.TrimSpace(s[loc[2]:loc[3]]))
		if u == "" || seen[u] {
			continue
		}
		if !strings.Contains(strings.ToLower(u), "getuploader.com") {
			continue
		}
		seen[u] = true
		if struckAt(s, loc[0]) {
			// 被划掉的链接也保留下来（包上哨兵），让下游能记录「因删除线跳过」。
			out = append(out, strikeMarkOpen+u+strikeMarkClose)
			continue
		}
		out = append(out, u)
	}
	return out
}

// struckAt 判断位置 pos（字节下标）是否处于删除线哨兵之间。
func struckAt(s string, pos int) bool {
	if pos <= 0 {
		return false
	}
	if pos > len(s) {
		pos = len(s)
	}
	depth := 0
	for i := 0; i < pos; i++ {
		switch s[i] {
		case strikeMarkOpen[0]:
			depth++
		case strikeMarkClose[0]:
			if depth > 0 {
				depth--
			}
		}
	}
	return depth > 0
}

var (
	reBreakTag = regexp.MustCompile(`(?i)<\s*(?:br|/p|/div|/li|/h[1-6])\s*/?\s*>`)
	reAnyTag   = regexp.MustCompile(`<[^>\n]{1,300}>`)
)

// stripHTML 把 API 返回的简介中偶尔混入的 HTML 标签处理成纯文本。
func stripHTML(s string) string {
	if !strings.Contains(s, "<") {
		return s
	}
	s = reBreakTag.ReplaceAllString(s, "\n")
	s = reAnyTag.ReplaceAllString(s, "")
	s = html.UnescapeString(s)
	return s
}

// Summarize 返回用于日志的一行摘要。
func (i *Illust) Summarize() string {
	return fmt.Sprintf("%s / %s / im%s", util.Truncate(i.Title, 40), i.Author, i.ID)
}
