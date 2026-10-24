// Package archiver 串联 nico seiga 与 getuploader，完成批量下载。
package archiver

import (
	"context"
	"errors"
	"fmt"
	"io"
	"path/filepath"
	"strings"
	"time"

	"nico-seiga-archiver/internal/config"
	"nico-seiga-archiver/internal/extract"
	"nico-seiga-archiver/internal/getuploader"
	"nico-seiga-archiver/internal/httpclient"
	"nico-seiga-archiver/internal/logx"
	"nico-seiga-archiver/internal/metadata"
	"nico-seiga-archiver/internal/nico"
	"nico-seiga-archiver/internal/proxy"
	"nico-seiga-archiver/internal/state"
	"nico-seiga-archiver/internal/util"
)

// ErrCanceled 表示用户主动放弃了某个操作（例如在密码提示处按 Ctrl+C）。
var ErrCanceled = errors.New("用户取消")

// Stats 统计一次任务的结果。
type Stats struct {
	Illusts      int
	LinksFound   int
	Downloaded   int
	Skipped      int
	FailedFiles  int
	IllustErrors int
	// StruckLinks 是被删除线划掉、主动跳过的链接数量。
	StruckLinks int
	// CatalogPages/CatalogItems 是 down-catalog 的目录页数与文件数。
	CatalogPages int
	CatalogItems int
}

func (s Stats) String() string {
	parts := make([]string, 0, 6)
	if s.CatalogItems > 0 || (s.Illusts == 0 && s.CatalogPages > 0) {
		parts = append(parts, fmt.Sprintf("目录页 %d 页", s.CatalogPages), fmt.Sprintf("目录文件 %d 个", s.CatalogItems))
	} else {
		parts = append(parts, fmt.Sprintf("投稿 %d 个", s.Illusts), fmt.Sprintf("发现链接 %d 个", s.LinksFound))
	}
	parts = append(parts,
		fmt.Sprintf("下载成功 %d 个", s.Downloaded),
		fmt.Sprintf("跳过 %d 个", s.Skipped),
		fmt.Sprintf("失败 %d 个", s.FailedFiles),
	)
	if s.IllustErrors > 0 {
		parts = append(parts, fmt.Sprintf("投稿抓取失败 %d 个", s.IllustErrors))
	}
	if s.StruckLinks > 0 {
		parts = append(parts, fmt.Sprintf("删除线跳过 %d 个", s.StruckLinks))
	}
	return strings.Join(parts, "，")
}

// Archiver 是任务执行器。
type Archiver struct {
	cfg   *config.Config
	log   *logx.Logger
	hc    *httpclient.Client
	nico  *nico.Client
	gu    *getuploader.Client
	store *metadata.Store

	// AskPassword 在自动提取不到密码时被调用；为 nil 表示不询问。
	AskPassword func(hint string) (string, error)
	// Force 为 true 时即使文件已存在也重新下载。
	Force bool
	// DryRun 为 true 时只扫描和报告，不真正下载。
	DryRun bool
	// Restart 为 true 时忽略历史进度，一律从第 1 页开始。
	Restart bool

	ProxyInfo proxy.Result

	progress *state.Store

	visited map[string]bool
	stats   Stats
}

// New 构建执行器。
func New(cfg *config.Config, log *logx.Logger) (*Archiver, error) {
	hc, err := httpclient.New(httpclient.Options{
		Proxy:      cfg.Proxy,
		UserAgent:  cfg.UserAgent,
		Timeout:    cfg.Timeout(),
		NicoCookie: cfg.NicoCookie,
		Log:        log,
		Debug:      strings.EqualFold(cfg.LogLevel, "debug"),
	})
	if err != nil {
		return nil, err
	}
	store, err := metadata.Open(cfg.MetadataPath())
	if err != nil {
		return nil, err
	}
	progress, err := openProgress()
	if err != nil {
		// 进度只是锦上添花，坏了不该挡住下载。
		log.Warnf("读取下载进度失败（将忽略历史页码）: %v", err)
		progress, _ = state.Open("")
	}
	delay := cfg.Delay()
	return &Archiver{
		cfg:       cfg,
		log:       log,
		hc:        hc,
		nico:      nico.New(hc, log, delay),
		gu:        getuploader.New(hc, log, delay),
		store:     store,
		progress:  progress,
		ProxyInfo: hc.ProxyInfo,
		visited:   map[string]bool{},
	}, nil
}

func openProgress() (*state.Store, error) {
	path, err := config.ProgressPath()
	if err != nil {
		return nil, err
	}
	return state.Open(path)
}

// Stats 返回当前统计。
func (a *Archiver) Stats() Stats { return a.stats }

// ResetStats 清空统计（每条命令开始时调用）。
func (a *Archiver) ResetStats() { a.stats = Stats{} }

// MetadataPath 返回元数据文件路径。
func (a *Archiver) MetadataPath() string { return a.store.Path() }

// ProgressPath 返回进度文件路径。
func (a *Archiver) ProgressPath() string {
	if a.progress == nil {
		return ""
	}
	return a.progress.Path()
}

// ProgressEntries 返回所有索引的进度。
func (a *Archiver) ProgressEntries() []state.Entry {
	if a.progress == nil {
		return nil
	}
	return a.progress.Entries()
}

// ResumePage 返回某个索引历史记录的页码。
func (a *Archiver) ResumePage(key string) (state.Entry, bool) {
	if a.progress == nil || key == "" {
		return state.Entry{}, false
	}
	return a.progress.Get(key)
}

// ClearProgress 清除某个索引的进度（key 为空表示全部）。
func (a *Archiver) ClearProgress(key string) error {
	if a.progress == nil {
		return nil
	}
	if key == "" {
		return a.progress.ClearAll()
	}
	return a.progress.Clear(key)
}

func (a *Archiver) destDirFor(author string) string {
	return filepath.Join(a.cfg.DownloadDir, util.SanitizeName(author))
}

// ProcessIllust 处理一张静画投稿：抓信息 -> 提取链接 -> 逐个下载。
// ProcessIllust 处理一张静画投稿：抓信息 -> 提取链接 -> 逐个下载。
func (a *Archiver) ProcessIllust(ctx context.Context, rawURL string) (*nico.Illust, error) {
	ill, err := a.nico.FetchIllust(ctx, rawURL)
	if err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		a.stats.IllustErrors++
		return nil, err
	}
	if a.visited[ill.ID] {
		a.log.Debugf("本次任务已处理过 im%s，跳过", ill.ID)
		return ill, nil
	}
	a.visited[ill.ID] = true
	a.stats.Illusts++

	a.log.Stepf("投稿: %s", ill.Summarize())
	a.log.Infof("  作者: %s", ill.Author)
	if ill.Truncated && !ill.DescriptionFromAPI {
		a.log.Warnf("  简介被截断，可能漏掉链接（可在 cfg 中设置 nico_cookie 以获取完整简介）")
	}

	// 被删除线划掉的链接一律跳过，只在日志里留个记录。
	usable, skipped := extract.LinksWithSkipped(ill.Description)
	for _, u := range skipped {
		a.log.Warnf("  链接被删除线划掉，跳过: %s", u)
	}
	a.stats.StruckLinks += len(skipped)
	if len(usable) == 0 {
		if len(skipped) > 0 {
			a.log.Warnf("  简介中的 %d 个 getuploader 链接都被划掉了，未找到可用链接", len(skipped))
		} else {
			a.log.Warnf("  简介中未找到 getuploader 下载链接")
		}
		return ill, nil
	}
	a.stats.LinksFound += len(usable)
	if len(skipped) > 0 {
		a.log.Infof("  发现 %d 个可用的 getuploader 链接（跳过了 %d 个被划掉的）", len(usable), len(skipped))
	} else {
		a.log.Infof("  发现 %d 个可用的 getuploader 链接", len(usable))
	}

	for _, link := range usable {
		if err := ctx.Err(); err != nil {
			return ill, err
		}
		a.downloadLink(ctx, ill, link)
	}
	return ill, nil
}

func (a *Archiver) downloadLink(ctx context.Context, ill *nico.Illust, link extract.Link) {
	if !a.Force && a.store.IsDownloaded(link.URL) {
		a.log.Infof("  已下载过，跳过: %s", link.URL)
		a.stats.Skipped++
		return
	}
	destDir := a.destDirFor(ill.Author)
	password := link.Password
	if password != "" {
		if hw := extract.ToHalfWidth(password); hw != password {
			a.log.Infof("  链接: %s（密码: %s，全角；来源: %s）", link.URL, password, link.PasswordSource)
		} else {
			a.log.Infof("  链接: %s（密码: %s，来源: %s）", link.URL, password, link.PasswordSource)
		}
	} else {
		a.log.Infof("  链接: %s（未在简介中找到密码）", link.URL)
	}

	hint := fmt.Sprintf("投稿「%s」\n链接: %s\n简介片段: %s", ill.Title, link.URL, util.Truncate(link.Context, 300))

	if a.DryRun {
		target := filepath.Join(destDir, "(未知文件名)")
		if password != "" {
			a.log.Infof("  [dry-run] 将下载（密码 %s）到 %s", password, target)
		} else {
			a.log.Infof("  [dry-run] 将下载到 %s", target)
		}
		return
	}

	out, err := a.downloadFile(ctx, downloadOptions{
		URL:      link.URL,
		DestDir:  destDir,
		Password: password,
		Hint:     hint,
		Indent:   "  ",
	})
	if err == nil {
		if out.Canceled {
			a.stats.Skipped++
			a.log.Infof("  已跳过该链接（用户取消）")
			return
		}
		a.recordSuccess(ill, link.URL, out.Result, out.PasswordUsed != "")
		a.stats.Downloaded++
		return
	}

	a.stats.FailedFiles++
	if ctx.Err() != nil {
		// Ctrl+C 取消任务时不记成失败，避免污染元数据。
		a.stats.FailedFiles--
		a.log.Warnf("  已取消: %s", link.URL)
		return
	}
	a.log.Errorf("  下载失败: %s (%v)", link.URL, err)
	_ = a.store.Add(metadata.Record{
		NicoURL:        ill.URL,
		NicoID:         ill.ID,
		Title:          ill.Title,
		Author:         ill.Author,
		AuthorURL:      ill.AuthorURL,
		GetuploaderURL: link.URL,
		PasswordUsed:   out.PasswordUsed != "",
		Status:         metadata.StatusFailed,
		Error:          errString(err),
	})
}

func isPasswordError(err error) bool {
	return errors.Is(err, getuploader.ErrPasswordRequired) || errors.Is(err, getuploader.ErrPasswordRejected)
}

func (a *Archiver) recordSuccess(ill *nico.Illust, guURL string, res *getuploader.Result, passwordUsed bool) {
	rec := metadata.Record{
		GetuploaderURL: guURL,
		Filename:       res.Filename,
		FilePath:       res.Path,
		Size:           res.Bytes,
		MD5:            res.MD5,
		SHA256:         res.SHA256,
		PasswordUsed:   passwordUsed,
		AlreadyExisted: res.AlreadyExisted,
		DirectURL:      res.DirectURL,
		Status:         metadata.StatusSuccess,
	}
	if ill != nil {
		rec.NicoURL = ill.URL
		rec.NicoID = ill.ID
		rec.Title = ill.Title
		rec.Author = ill.Author
		rec.AuthorURL = ill.AuthorURL
	}
	if err := a.store.Add(rec); err != nil {
		a.log.Errorf("  写入元数据失败: %v", err)
	}
}

// ProcessList 逐页处理列表页（搜索结果 / tag / 作者主页）。
//
// progressKey 非空时会把处理到的页码记录到进度文件，下次未指定页码即可续传。
func (a *Archiver) ProcessList(ctx context.Context, rawURL string, startPage int, progressKey, label string) error {
	current, err := nico.WithPage(rawURL, startPage)
	if err != nil {
		return err
	}
	maxPages := a.cfg.MaxPages
	if maxPages <= 0 {
		maxPages = 500
	}
	record := func(page int, completed bool) {
		if a.DryRun {
			// 只扫描不下载时不更新进度，避免污染下一次正式运行。
			return
		}
		if a.progress == nil || progressKey == "" {
			return
		}
		if err := a.progress.Set(progressKey, label, page, completed); err != nil {
			a.log.Debugf("记录下载进度失败: %v", err)
		}
	}
	lastPage := startPage
	for i := 0; i < maxPages; i++ {
		if err := ctx.Err(); err != nil {
			return err
		}
		page, err := a.nico.ListPage(ctx, current)
		if err != nil {
			return err
		}
		pageNo := nico.PageOf(current)
		lastPage = pageNo
		a.log.Stepf("列表第 %d 页: %s", pageNo, current)
		if len(page.Items) == 0 {
			a.log.Warnf("  本页没有解析到投稿，结束")
			break
		}
		a.log.Infof("  本页 %d 个投稿", len(page.Items))
		for _, item := range page.Items {
			if err := ctx.Err(); err != nil {
				return err
			}
			if _, err := a.ProcessIllust(ctx, item.URL); err != nil {
				if ctx.Err() != nil {
					return ctx.Err()
				}
				a.log.Errorf("处理投稿失败 %s: %v", item.URL, err)
			}
		}
		// 每处理完一页就落盘，中断后也能接着来。
		record(pageNo, false)
		if page.NextURL == "" || page.NextURL == current {
			a.log.Infof("  已到最后一页")
			record(pageNo, true)
			break
		}
		current = page.NextURL
	}
	if a.progress != nil && progressKey != "" && !a.DryRun {
		a.log.Infof("进度已记录：%s → 第 %d 页（下次未指定页码时从这里继续）", progressKey, lastPage)
	}
	a.log.Infof("%s", a.stats.String())
	return nil
}

// DirectDownload 直接下载一个 getuploader 链接（down 命令）。
func (a *Archiver) DirectDownload(ctx context.Context, guURL, password string) (*getuploader.Result, error) {
	if a.DryRun {
		a.log.Infof("[dry-run] 将下载: %s（密码: %s）", guURL, orNoPassword(password))
		return nil, nil
	}
	if !a.Force && a.store.IsDownloaded(guURL) {
		a.log.Infof("该链接已下载过，跳过: %s", guURL)
		a.stats.Skipped++
		return nil, nil
	}

	out, err := a.downloadFile(ctx, downloadOptions{
		URL:      guURL,
		DestDir:  a.cfg.DownloadDir,
		Password: password,
		Hint:     "getuploader 下载页\n" + guURL,
	})
	switch {
	case err == nil && out.Canceled:
		a.log.Infof("已取消该下载。")
		return nil, nil
	case err == nil:
		a.recordSuccess(nil, guURL, out.Result, out.PasswordUsed != "")
		a.stats.Downloaded++
		return out.Result, nil
	}

	if ctx.Err() != nil {
		a.log.Warnf("已取消: %s", guURL)
		return nil, ctx.Err()
	}
	a.stats.FailedFiles++
	_ = a.store.Add(metadata.Record{
		GetuploaderURL: guURL,
		PasswordUsed:   out.PasswordUsed != "",
		Status:         metadata.StatusFailed,
		Error:          errString(err),
	})
	return nil, err
}

func errString(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}

func orDefault(s, def string) string {
	if strings.TrimSpace(s) == "" {
		return def
	}
	return s
}

func orNoPassword(p string) string {
	if strings.TrimSpace(p) == "" {
		return "无"
	}
	return p
}

// ProbeResult 是一次连通性检查的结果。
type ProbeResult struct {
	Name   string
	URL    string
	OK     bool
	Detail string
}

// Probe 检查 nico seiga 与 getuploader 是否可达（用于 cfg test）。
func (a *Archiver) Probe(ctx context.Context) []ProbeResult {
	targets := []struct {
		name string
		url  string
		kind string
	}{
		{"nico seiga API", "https://seiga.nicovideo.jp/api/illust/info?id=11691748", "xml"},
		{"getuploader 下载页", "https://uu.getuploader.com/harinashi/download/71", "html"},
	}
	results := make([]ProbeResult, 0, len(targets))
	for _, t := range targets {
		r := ProbeResult{Name: t.name, URL: t.url}
		tctx, cancel := context.WithTimeout(ctx, 15*time.Second)
		resp, err := a.hc.GetOnce(tctx, t.url)
		if err != nil {
			cancel()
			r.Detail = err.Error()
			results = append(results, r)
			continue
		}
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 8192))
		resp.Body.Close()
		cancel()
		if resp.StatusCode >= 400 {
			r.Detail = fmt.Sprintf("HTTP %d", resp.StatusCode)
			results = append(results, r)
			continue
		}
		text := string(body)
		switch {
		case strings.Contains(text, "Just a moment..."), strings.Contains(text, "Enable JavaScript and cookies to continue"):
			r.Detail = "被 Cloudflare 拦截（代理可能不可用）"
		case strings.Contains(text, "account.nicovideo.jp") && t.kind == "xml":
			r.Detail = "被重定向到登录页（Cookie 失效？）"
		case t.kind == "xml" && !strings.Contains(text, "<response>"):
			r.Detail = "返回内容异常"
		case t.kind == "html" && !strings.Contains(text, "getuploader"):
			r.Detail = "返回内容异常"
		default:
			r.OK = true
			r.Detail = "OK"
		}
		results = append(results, r)
	}
	return results
}
