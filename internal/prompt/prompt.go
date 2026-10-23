// Package prompt 封装交互式输入：支持历史记录（上下翻页）、行内编辑、
// Tab 补全、密码隐藏输入，以及 Ctrl+C 取消。
package prompt

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"sync"

	"github.com/peterh/liner"
)

// ErrInterrupted 表示用户按下了 Ctrl+C。
var ErrInterrupted = errors.New("已中断")

// Reader 是交互式输入器。
//
// 当 stdin 是终端时使用 liner（支持 ↑/↓ 历史、←/→ 编辑、Tab 补全、Ctrl+C 取消）；
// 否则退化为按行读取，保证 `printf 'help\n' | archiver` 这类用法仍然可用。
type Reader struct {
	mu   sync.Mutex
	line *liner.State
	in   *bufio.Reader
	out  io.Writer
	tty  bool
}

// New 创建输入器。
func New(in io.Reader, out io.Writer) *Reader {
	r := &Reader{out: out}
	if f, ok := in.(*os.File); ok && isCharDevice(f) && isTerminalWriter(out) && liner.TerminalSupported() {
		st := liner.NewLiner()
		st.SetCtrlCAborts(true)   // Ctrl+C 直接返回 ErrInterrupted
		st.SetMultiLineMode(true) // 长命令行自动折行，编辑不错位
		r.line = st
		r.tty = true
		return r
	}
	r.in = bufio.NewReaderSize(in, 64*1024)
	return r
}

// Interactive 报告是否处于真正的终端交互模式。
func (r *Reader) Interactive() bool { return r.tty }

// Line 读取一行输入。Ctrl+C 时返回 ErrInterrupted。
//
// 注意：本方法不会自动写入历史，调用方用 AddHistory 决定是否记录，
// 这样含敏感信息的命令（如 Cookie）可以不留痕迹。
func (r *Reader) Line(p string) (string, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.line == nil {
		fmt.Fprint(r.out, p)
		line, err := r.in.ReadString('\n')
		if err != nil && line == "" {
			return "", err
		}
		return strings.TrimRight(line, "\r\n"), nil
	}
	s, err := r.line.Prompt(p)
	switch {
	case err == nil:
		return s, nil
	case errors.Is(err, liner.ErrPromptAborted):
		return "", ErrInterrupted
	default:
		return "", err
	}
}

// Password 读取密码（不回显、不记历史）。Ctrl+C 时返回 ErrInterrupted。
func (r *Reader) Password(p string) (string, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.line == nil {
		fmt.Fprint(r.out, p)
		line, err := r.in.ReadString('\n')
		if err != nil && line == "" {
			return "", err
		}
		return strings.TrimRight(line, "\r\n"), nil
	}
	s, err := r.line.PasswordPrompt(p)
	switch {
	case err == nil:
		return s, nil
	case errors.Is(err, liner.ErrPromptAborted):
		return "", ErrInterrupted
	default:
		return "", err
	}
}

// AddHistory 手动追加一条历史（例如密码提示里的输入不会自动进历史）。
func (r *Reader) AddHistory(s string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.line != nil && strings.TrimSpace(s) != "" {
		r.line.AppendHistory(s)
	}
}

// LoadHistory 从文件读取历史。
func (r *Reader) LoadHistory(path string) error {
	if path == "" {
		return nil
	}
	f, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	defer f.Close()
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.line != nil {
		_, err = r.line.ReadHistory(f)
	}
	return err
}

// SaveHistory 把历史写回文件（原子写入，最多保留 1000 条）。
func (r *Reader) SaveHistory(path string) error {
	if path == "" {
		return nil
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.line == nil {
		return nil
	}
	// liner 自身没有条数上限，这里截断后再写。
	tmp := path + ".tmp"
	f, err := os.Create(tmp)
	if err != nil {
		return err
	}
	// 借用 WriteHistory 写入临时文件，再裁剪。
	if _, err := r.line.WriteHistory(f); err != nil {
		f.Close()
		os.Remove(tmp)
		return err
	}
	if err := f.Close(); err != nil {
		os.Remove(tmp)
		return err
	}
	if data, err := os.ReadFile(tmp); err == nil {
		lines := strings.Split(strings.TrimRight(string(data), "\n"), "\n")
		if len(lines) > 1000 {
			lines = lines[len(lines)-1000:]
		}
		_ = os.WriteFile(tmp, []byte(strings.Join(lines, "\n")+"\n"), 0o600)
	}
	if err := os.MkdirAll(dirOf(path), 0o700); err != nil {
		os.Remove(tmp)
		return err
	}
	return os.Rename(tmp, path)
}

// SetCompleter 设置 Tab 补全函数。
func (r *Reader) SetCompleter(f func(string) []string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.line == nil || f == nil {
		return
	}
	r.line.SetCompleter(f)
}

// Close 恢复终端状态并释放资源。
func (r *Reader) Close() {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.line != nil {
		_ = r.line.Close()
		r.line = nil
	}
}

func isCharDevice(f *os.File) bool {
	st, err := f.Stat()
	if err != nil {
		return false
	}
	return st.Mode()&os.ModeCharDevice != 0
}

func isTerminalWriter(w io.Writer) bool {
	f, ok := w.(*os.File)
	if !ok {
		return false
	}
	return isCharDevice(f)
}

func dirOf(p string) string {
	i := strings.LastIndexAny(p, `/\`)
	if i <= 0 {
		return "."
	}
	return p[:i]
}
