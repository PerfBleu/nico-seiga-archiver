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
	"nico-seiga-archiver/internal/util"
)

// Stats 统计一次任务的结果。
type Stats struct {
	Illusts      int
	LinksFound   int
	Downloaded   int
	Skipped      int
	FailedFiles  int
	IllustErrors int
}

func (s Stats) String() string {
	return fmt.Sprintf("投稿 %d 个，发现链接 %d 个，下载成功 %d 个，跳过 %d 个，失败 %d 个，投稿抓取失败 %d 个",
		s.Illusts, s.LinksFound, s.Downloaded, s.Skipped, s.FailedFiles, s.IllustErrors)
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

	ProxyInfo proxy.Result

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
	delay := cfg.Delay()
	return &Archiver{
		cfg:       cfg,
		log:       log,
		hc:        hc,
		nico:      nico.New(hc, log, delay),
		gu:        getuploader.New(hc, log, delay),
		store:     store,
		ProxyInfo: hc.ProxyInfo,
		visited:   map[string]bool{},
	}, nil
}

// Stats 返回当前统计。
func (a *Archiver) Stats() Stats { return a.stats }

// ResetStats 清空统计（每条命令开始时调用）。
func (a *Archiver) ResetStats() { a.stats = Stats{} }

// MetadataPath 返回元数据文件路径。
func (a *Archiver) MetadataPath() string { return a.store.Path() }

func (a *Archiver) destDirFor(author string) string {
	return filepath.Join(a.cfg.DownloadDir, util.SanitizeName(author))
}

// ProcessIllust 处理一张静画投稿：抓信息 -> 提取链接 -> 逐个下载。
func (a *Archiver) ProcessIllust(ctx context.Context, rawURL string) (*nico.Illust, error) {
	ill, err := a.nico.FetchIllust(ctx, rawURL)
	if err != nil {
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

	links := extract.Links(ill.Description)
	if len(links) == 0 {
		a.log.Warnf("  简介中未找到 getuploader 下载链接")
		return ill, nil
	}
	a.stats.LinksFound += len(links)
	a.log.Infof("  发现 %d 个 getuploader 链接", len(links))

	for _, link := range links {
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
		a.log.Infof("  链接: %s（密码: %s，来源: %s）", link.URL, password, link.PasswordSource)
	} else {
		a.log.Infof("  链接: %s（未在简介中找到密码）", link.URL)
	}

	hint := fmt.Sprintf("投稿「%s」\n链接: %s\n简介片段: %s", ill.Title, link.URL, util.Truncate(link.Context, 300))

	if a.DryRun {
		target := filepath.Join(destDir, "(未知文件名)")
		if link.Password != "" {
			a.log.Infof("  [dry-run] 将下载（密码 %s）到 %s", link.Password, target)
		} else {
			a.log.Infof("  [dry-run] 将下载到 %s", target)
		}
		return
	}

	var lastErr error
	for attempt := 1; attempt <= 3; attempt++ {
		res, err := a.gu.Download(ctx, link.URL, password, destDir, a.Force)
		if err == nil {
			a.recordSuccess(ill, link.URL, res, password != "")
			a.stats.Downloaded++
			return
		}
		lastErr = err
		if errors.Is(err, getuploader.ErrPasswordRequired) || errors.Is(err, getuploader.ErrPasswordRejected) {
			if a.AskPassword == nil || !a.cfg.AskPassword {
				break
			}
			reason := "需要下载密码"
			if errors.Is(err, getuploader.ErrPasswordRejected) {
				reason = "密码错误，请重新输入"
			}
			a.log.Warnf("  %s", reason)
			np, perr := a.AskPassword(hint)
			if perr != nil {
				lastErr = perr
				break
			}
			if strings.TrimSpace(np) == "" {
				break
			}
			password = strings.TrimSpace(np)
			a.log.Infof("  使用手动输入的密码重试...")
			continue
		}
		break
	}

	a.stats.FailedFiles++
	a.log.Errorf("  下载失败: %s (%v)", link.URL, lastErr)
	_ = a.store.Add(metadata.Record{
		NicoURL:        ill.URL,
		NicoID:         ill.ID,
		Title:          ill.Title,
		Author:         ill.Author,
		AuthorURL:      ill.AuthorURL,
		GetuploaderURL: link.URL,
		PasswordUsed:   password != "",
		Status:         metadata.StatusFailed,
		Error:          errString(lastErr),
	})
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

// ProcessList 逐页处理列表页（搜索结果或作者主页）。
func (a *Archiver) ProcessList(ctx context.Context, rawURL string, startPage int) error {
	current, err := nico.WithPage(rawURL, startPage)
	if err != nil {
		return err
	}
	maxPages := a.cfg.MaxPages
	if maxPages <= 0 {
		maxPages = 500
	}
	for i := 0; i < maxPages; i++ {
		if err := ctx.Err(); err != nil {
			return err
		}
		page, err := a.nico.ListPage(ctx, current)
		if err != nil {
			return err
		}
		a.log.Stepf("列表第 %d 页: %s", nico.PageOf(current), current)
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
				a.log.Errorf("处理投稿失败 %s: %v", item.URL, err)
			}
		}
		if page.NextURL == "" || page.NextURL == current {
			a.log.Infof("  已到最后一页")
			break
		}
		current = page.NextURL
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
	res, err := a.gu.Download(ctx, guURL, password, a.cfg.DownloadDir, a.Force)
	if err != nil && (errors.Is(err, getuploader.ErrPasswordRequired) || errors.Is(err, getuploader.ErrPasswordRejected)) {
		if a.AskPassword != nil && a.cfg.AskPassword {
			if np, perr := a.AskPassword("getuploader 下载页\n" + guURL); perr == nil && strings.TrimSpace(np) != "" {
				password = strings.TrimSpace(np)
				res, err = a.gu.Download(ctx, guURL, password, a.cfg.DownloadDir, a.Force)
			}
		}
	}
	if err != nil {
		a.stats.FailedFiles++
		_ = a.store.Add(metadata.Record{
			GetuploaderURL: guURL,
			PasswordUsed:   password != "",
			Status:         metadata.StatusFailed,
			Error:          errString(err),
		})
		return nil, err
	}
	a.recordSuccess(nil, guURL, res, password != "")
	a.stats.Downloaded++
	return res, nil
}

func errString(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
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
