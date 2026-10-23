// Package logx 提供带级别、带时间戳的日志输出，支持同时写入日志文件。
package logx

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// Level 日志级别。
type Level int

const (
	LevelDebug Level = iota
	LevelInfo
	LevelWarn
	LevelError
)

// Logger 是并发安全的日志器。
type Logger struct {
	mu    sync.Mutex
	out   io.Writer
	level Level
	color bool

	file     *os.File
	filePath string
}

// New 创建日志器。
func New(out io.Writer, level Level) *Logger {
	if out == nil {
		out = os.Stdout
	}
	return &Logger{out: out, level: level, color: true}
}

// SetLevel 调整日志级别。
func (l *Logger) SetLevel(level Level) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.level = level
}

// SetColor 开启/关闭颜色。
func (l *Logger) SetColor(on bool) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.color = on
}

// AttachFile 打开（或切换）日志文件，之后每行日志都会同时追加写入该文件。
// 重复调用同一路径不会重新打开。
func (l *Logger) AttachFile(path string) error {
	path = strings.TrimSpace(path)
	if path == "" {
		l.DetachFile()
		return nil
	}
	if !filepath.IsAbs(path) {
		abs, err := filepath.Abs(path)
		if err == nil {
			path = abs
		}
	}

	l.mu.Lock()
	if l.file != nil && l.filePath == path {
		l.mu.Unlock()
		return nil
	}
	l.mu.Unlock()

	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("创建日志目录失败: %w", err)
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return fmt.Errorf("打开日志文件失败: %w", err)
	}

	l.mu.Lock()
	defer l.mu.Unlock()
	if l.file != nil {
		_ = l.file.Sync()
		_ = l.file.Close()
	}
	l.file = f
	l.filePath = path
	return nil
}

// DetachFile 关闭日志文件，只输出到终端。
func (l *Logger) DetachFile() {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.file != nil {
		_ = l.file.Sync()
		_ = l.file.Close()
		l.file = nil
		l.filePath = ""
	}
}

// FilePath 返回当前日志文件路径（未启用时为空）。
func (l *Logger) FilePath() string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.filePath
}

// Close 关闭日志文件。
func (l *Logger) Close() { l.DetachFile() }

func (l *Logger) logf(level Level, prefix, format string, args ...any) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if level < l.level {
		return
	}
	msg := fmt.Sprintf(format, args...)
	msg = strings.TrimRight(msg, "\n")
	now := time.Now()

	if l.color {
		prefix = colorize(level, prefix)
	}
	fmt.Fprintf(l.out, "%s %s %s\n", now.Format("15:04:05"), prefix, msg)

	if l.file != nil {
		// 文件里写完整日期，且不带颜色转义，方便事后查找。
		fmt.Fprintf(l.file, "%s [%s] %s\n", now.Format("2006-01-02 15:04:05"), levelName(level), msg)
	}
}

// Stepf 用于标记一个阶段（视觉上更醒目）。
func (l *Logger) Stepf(format string, args ...any) {
	l.mu.Lock()
	if LevelInfo < l.level {
		l.mu.Unlock()
		return
	}
	msg := strings.TrimRight(fmt.Sprintf(format, args...), "\n")
	now := time.Now()
	if l.color {
		fmt.Fprintf(l.out, "%s \x1b[1;36m==>\x1b[0m  %s\n", now.Format("15:04:05"), msg)
	} else {
		fmt.Fprintf(l.out, "%s ==>  %s\n", now.Format("15:04:05"), msg)
	}
	if l.file != nil {
		fmt.Fprintf(l.file, "%s [STEP] %s\n", now.Format("2006-01-02 15:04:05"), msg)
	}
	l.mu.Unlock()
}

// Debugf 调试日志。
func (l *Logger) Debugf(format string, args ...any) { l.logf(LevelDebug, "DEBUG", format, args...) }

// Infof 普通日志。
func (l *Logger) Infof(format string, args ...any) { l.logf(LevelInfo, "INFO ", format, args...) }

// Warnf 警告日志。
func (l *Logger) Warnf(format string, args ...any) { l.logf(LevelWarn, "WARN ", format, args...) }

// Errorf 错误日志。
func (l *Logger) Errorf(format string, args ...any) { l.logf(LevelError, "ERROR", format, args...) }

func levelName(level Level) string {
	switch level {
	case LevelDebug:
		return "DEBUG"
	case LevelWarn:
		return "WARN "
	case LevelError:
		return "ERROR"
	default:
		return "INFO "
	}
}

func colorize(level Level, prefix string) string {
	switch level {
	case LevelWarn:
		return "\x1b[33m" + prefix + "\x1b[0m"
	case LevelError:
		return "\x1b[31m" + prefix + "\x1b[0m"
	case LevelDebug:
		return "\x1b[90m" + prefix + "\x1b[0m"
	default:
		return "\x1b[32m" + prefix + "\x1b[0m"
	}
}
