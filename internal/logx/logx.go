// Package logx 提供带级别、带时间戳的简单日志输出。
package logx

import (
	"fmt"
	"io"
	"os"
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

func (l *Logger) logf(level Level, prefix, format string, args ...any) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if level < l.level {
		return
	}
	msg := fmt.Sprintf(format, args...)
	msg = strings.TrimRight(msg, "\n")
	ts := time.Now().Format("15:04:05")
	if l.color {
		prefix = colorize(level, prefix)
	}
	fmt.Fprintf(l.out, "%s %s %s\n", ts, prefix, msg)
}

// Debugf 调试日志。
func (l *Logger) Debugf(format string, args ...any) { l.logf(LevelDebug, "DEBUG", format, args...) }

// Infof 普通日志。
func (l *Logger) Infof(format string, args ...any) { l.logf(LevelInfo, "INFO ", format, args...) }

// Warnf 警告日志。
func (l *Logger) Warnf(format string, args ...any) { l.logf(LevelWarn, "WARN ", format, args...) }

// Errorf 错误日志。
func (l *Logger) Errorf(format string, args ...any) { l.logf(LevelError, "ERROR", format, args...) }

// Stepf 用于标记一个阶段（视觉上更醒目）。
func (l *Logger) Stepf(format string, args ...any) {
	l.mu.Lock()
	if LevelInfo < l.level {
		l.mu.Unlock()
		return
	}
	msg := strings.TrimRight(fmt.Sprintf(format, args...), "\n")
	ts := time.Now().Format("15:04:05")
	if l.color {
		fmt.Fprintf(l.out, "%s \x1b[1;36m==>\x1b[0m  %s\n", ts, msg)
	} else {
		fmt.Fprintf(l.out, "%s ==>  %s\n", ts, msg)
	}
	l.mu.Unlock()
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
