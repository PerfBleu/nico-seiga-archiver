package archiver

import (
	"context"
	"errors"
	"strings"

	"nico-seiga-archiver/internal/extract"
	"nico-seiga-archiver/internal/getuploader"
)

// downloadOptions 描述一次「带密码尝试」的下载。
type downloadOptions struct {
	// URL 是 getuploader 下载页地址。
	URL string
	// DestDir 是文件保存目录。
	DestDir string
	// Password 是首选密码（可能是全角写法）。
	Password string
	// Hint 在需要询问用户时展示。
	Hint string
	// Indent 是日志缩进，子任务用两个空格。
	Indent string
	// MaxAsks 是最多询问几次密码（<=0 时默认 2 次）。
	MaxAsks int
}

// downloadOutcome 是一次下载的结果。
type downloadOutcome struct {
	Result *getuploader.Result
	// PasswordUsed 是实际生效的密码，空字符串表示没有用到密码。
	PasswordUsed string
	// Canceled 表示用户在密码提示处按了 Ctrl+C。
	Canceled bool
}

// downloadFile 完成「尝试密码 → 必要时询问 → 下载」的流程。
//
// 密码按「原文（可能是全角）→ 半角」的顺序尝试；只有密码类错误才换下一个候选，
// 网络/页面结构问题会立即返回，避免无谓的重复请求。
func (a *Archiver) downloadFile(ctx context.Context, opts downloadOptions) (downloadOutcome, error) {
	indent := opts.Indent
	maxAsks := opts.MaxAsks
	if maxAsks <= 0 {
		maxAsks = 2
	}
	var (
		out      downloadOutcome
		lastErr  error
		attempts int
	)
	tryAll := func(candidates []string) bool {
		if len(candidates) == 0 {
			candidates = []string{""}
		}
		for i, pw := range candidates {
			if i > 0 {
				a.log.Infof("%s密码未通过，改用半角写法重试: %s", indent, pw)
			}
			attempts++
			res, err := a.gu.Download(ctx, opts.URL, pw, opts.DestDir, a.Force)
			if err == nil {
				out.Result = res
				if strings.TrimSpace(pw) != "" {
					out.PasswordUsed = pw
				}
				return true
			}
			lastErr = err
			// 不是密码问题（网络、页面结构等）就没必要换密码了。
			if !isPasswordError(err) {
				return false
			}
		}
		return false
	}

	if tryAll(extract.PasswordAlternates(opts.Password)) {
		return out, nil
	}

	for isPasswordError(lastErr) && a.AskPassword != nil && a.cfg.AskPassword && maxAsks > 0 && attempts < 6 {
		reason := "需要下载密码"
		if errors.Is(lastErr, getuploader.ErrPasswordRejected) {
			reason = "密码错误，请重新输入"
		}
		a.log.Warnf("%s%s", indent, reason)
		np, perr := a.AskPassword(opts.Hint)
		if errors.Is(perr, ErrCanceled) {
			out.Canceled = true
			return out, nil
		}
		if perr != nil {
			lastErr = perr
			break
		}
		if strings.TrimSpace(np) == "" {
			break
		}
		maxAsks--
		a.log.Infof("%s使用手动输入的密码重试...", indent)
		if tryAll(extract.PasswordAlternates(np)) {
			return out, nil
		}
	}
	if out.Result != nil {
		return out, nil
	}
	return out, lastErr
}
