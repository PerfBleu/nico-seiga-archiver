package archiver

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"

	"nico-seiga-archiver/internal/getuploader"
	"nico-seiga-archiver/internal/metadata"
	"nico-seiga-archiver/internal/util"
)

// catalogRun 保存一轮目录任务里共享的状态。
type catalogRun struct {
	ref getuploader.CatalogRef
	// password 是当前最可能有效的密码：某个密码成功过之后，后续文件会优先用它。
	password string
	// name 是上传者显示名（写进元数据）。
	name string
	// destDir 是下载目录（<下载目录>/<上传者账号>）。
	destDir string
	// initialized 表示目录名已经确定。
	initialized bool
}

// ProcessCatalog 把 getuploader 上传者主页当作目录，逐页下载其中所有文件。
//
// password 会用于所有需要密码的子页面；未提供时按需询问用户。
func (a *Archiver) ProcessCatalog(ctx context.Context, ref getuploader.CatalogRef, password string, startPage int, progressKey, label string) error {
	if startPage <= 0 {
		startPage = 1
	}
	maxPages := a.cfg.MaxPages
	if maxPages <= 0 {
		maxPages = 500
	}
	if label == "" {
		label = ref.Label()
	}
	run := &catalogRun{ref: ref, password: strings.TrimSpace(password)}

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
	for pageNo := startPage; pageNo < startPage+maxPages; pageNo++ {
		if err := ctx.Err(); err != nil {
			return err
		}
		pageURL := ref.PageURL(pageNo)
		a.log.Stepf("目录第 %d 页: %s", pageNo, pageURL)
		page, err := a.gu.FetchCatalogPage(ctx, pageURL)
		if err != nil {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			if pageNo == startPage {
				a.stats.CatalogPages++
				return err
			}
			a.log.Warnf("  第 %d 页获取失败，结束: %v", pageNo, err)
			break
		}
		a.stats.CatalogPages++

		// 用第一页的信息确定目录名。
		if !run.initialized {
			run.initialized = true
			run.name = strings.TrimSpace(page.Name)
			run.destDir = filepath.Join(a.cfg.DownloadDir, page.CatalogDestName())
			if run.name != "" && run.name != page.Account {
				a.log.Infof("  上传者: %s（账号 %s）", run.name, page.Account)
			}
		}
		if page.Summary != "" {
			a.log.Infof("  %s", util.OneLine(page.Summary))
		}
		if len(page.Items) == 0 {
			a.log.Warnf("  本页没有解析到文件，结束")
			break
		}
		a.log.Infof("  本页 %d 个文件", len(page.Items))
		lastPage = pageNo

		for _, item := range page.Items {
			if err := ctx.Err(); err != nil {
				return err
			}
			a.downloadCatalogItem(ctx, run, item)
		}
		record(pageNo, false)
		if !page.HasNext() {
			a.log.Infof("  已到最后一页")
			record(pageNo, true)
			break
		}
	}

	if a.progress != nil && progressKey != "" && !a.DryRun {
		a.log.Infof("进度已记录：%s → 第 %d 页（下次未指定页码时从这里继续）", progressKey, lastPage)
	}
	a.log.Infof("%s", a.stats.String())
	return nil
}

// downloadCatalogItem 下载目录里的一个文件。
func (a *Archiver) downloadCatalogItem(ctx context.Context, run *catalogRun, item getuploader.CatalogItem) {
	if !a.Force && a.store.IsDownloaded(item.URL) {
		a.log.Infof("  已下载过，跳过: %s", util.Truncate(item.Title, 60))
		a.stats.Skipped++
		return
	}
	a.stats.CatalogItems++

	desc := util.Truncate(item.Title, 60)
	if item.SizeText != "" {
		a.log.Infof("  文件: %s（%s）", desc, item.SizeText)
	} else {
		a.log.Infof("  文件: %s", desc)
	}
	if a.DryRun {
		if run.password != "" {
			a.log.Infof("    [dry-run] 将下载（密码 %s）到 %s", run.password, run.destDir)
		} else {
			a.log.Infof("    [dry-run] 将下载到 %s", run.destDir)
		}
		return
	}

	hint := fmt.Sprintf("目录「%s」\n文件: %s\n链接: %s", orDefault(run.name, run.ref.Account), item.Title, item.URL)
	out, err := a.downloadFile(ctx, downloadOptions{
		URL:      item.URL,
		DestDir:  run.destDir,
		Password: run.password,
		Hint:     hint,
		Indent:   "    ",
	})
	if err == nil {
		if out.Canceled {
			a.stats.Skipped++
			a.log.Infof("    已跳过该文件（用户取消）")
			return
		}
		// 记住生效的密码，后续文件优先使用。
		if out.PasswordUsed != "" {
			run.password = out.PasswordUsed
		}
		a.recordCatalogSuccess(run, item, out.Result, out.PasswordUsed != "")
		a.stats.Downloaded++
		return
	}

	if ctx.Err() != nil {
		a.log.Warnf("    已取消: %s", desc)
		return
	}
	a.stats.FailedFiles++
	a.log.Errorf("    下载失败: %s (%v)", desc, err)
	_ = a.store.Add(metadata.Record{
		Author:         run.name,
		AuthorURL:      run.ref.Root,
		GetuploaderURL: item.URL,
		Filename:       item.Original,
		PasswordUsed:   out.PasswordUsed != "",
		Status:         metadata.StatusFailed,
		Error:          errString(err),
	})
}

// recordCatalogSuccess 写入目录下载的元数据。
func (a *Archiver) recordCatalogSuccess(run *catalogRun, item getuploader.CatalogItem, res *getuploader.Result, passwordUsed bool) {
	rec := metadata.Record{
		Author:         run.name,
		AuthorURL:      run.ref.Root,
		GetuploaderURL: item.URL,
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
	if err := a.store.Add(rec); err != nil {
		a.log.Errorf("    写入元数据失败: %v", err)
	}
}
