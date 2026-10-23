// Package util 放一些通用的小工具。
package util

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"unicode/utf8"
)

// SanitizeName 把任意字符串变成安全的文件名/目录名。
func SanitizeName(s string) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return "unknown"
	}
	var b strings.Builder
	for _, r := range s {
		switch r {
		case '/', '\\', ':', '*', '?', '"', '<', '>', '|', 0:
			b.WriteRune('_')
		default:
			if r < 0x20 || r == 0x7f {
				b.WriteRune('_')
				continue
			}
			b.WriteRune(r)
		}
	}
	out := strings.TrimSpace(b.String())
	out = strings.Trim(out, ".")
	out = strings.TrimSpace(out)
	if out == "" {
		out = "unknown"
	}
	// 防止超过文件系统长度限制：按 rune 截断，尽量保留扩展名。
	if utf8.RuneCountInString(out) > 120 {
		ext := filepath.Ext(out)
		base := strings.TrimSuffix(out, ext)
		if utf8.RuneCountInString(ext) > 20 {
			ext = ""
		}
		runes := []rune(base)
		if len(runes) > 110 {
			runes = runes[:110]
		}
		out = string(runes) + ext
	}
	return out
}

// HumanBytes 人类可读的字节数。
func HumanBytes(n int64) string {
	const unit = 1024
	if n < unit {
		return fmt.Sprintf("%d B", n)
	}
	div, exp := int64(unit), 0
	for v := n / unit; v >= unit; v /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %cB", float64(n)/float64(div), "KMGTPE"[exp])
}

// EnsureDir 确保目录存在。
func EnsureDir(dir string) error {
	return os.MkdirAll(dir, 0o755)
}

// Truncate 按 rune 截断字符串。
func Truncate(s string, maxRunes int) string {
	if maxRunes <= 0 {
		return ""
	}
	r := []rune(s)
	if len(r) <= maxRunes {
		return s
	}
	return string(r[:maxRunes]) + "…"
}

// OneLine 把多行文本压成单行（用于日志）。
func OneLine(s string) string {
	s = strings.ReplaceAll(s, "\r", "")
	s = strings.ReplaceAll(s, "\n", " ")
	s = strings.Join(strings.Fields(s), " ")
	return strings.TrimSpace(s)
}

// SplitArgs 以 shell 风格切分命令（支持单引号和双引号）。
func SplitArgs(line string) []string {
	var (
		args    []string
		current strings.Builder
		inWord  bool
		quote   rune
		escape  bool
	)
	for _, r := range line {
		switch {
		case escape:
			current.WriteRune(r)
			inWord = true
			escape = false
		case r == '\\' && quote != '\'':
			escape = true
			inWord = true
		case quote != 0:
			if r == quote {
				quote = 0
			} else {
				current.WriteRune(r)
			}
			inWord = true
		case r == '\'' || r == '"':
			quote = r
			inWord = true
		case r == ' ' || r == '\t':
			if inWord {
				args = append(args, current.String())
				current.Reset()
				inWord = false
			}
		default:
			current.WriteRune(r)
			inWord = true
		}
	}
	if escape {
		current.WriteRune('\\')
	}
	if inWord {
		args = append(args, current.String())
	}
	return args
}
