package logx

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestAttachFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "logs", "archiver.log")

	var buf strings.Builder
	l := New(&buf, LevelInfo)
	if err := l.AttachFile(path); err != nil {
		t.Fatal(err)
	}
	if l.FilePath() != path {
		t.Errorf("FilePath = %q, want %q", l.FilePath(), path)
	}

	l.Infof("下载成功: %s", "a.zip")
	l.Warnf("有点问题")
	l.Stepf("阶段开始")
	l.Debugf("调试信息不应写入")

	l.Close()

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	text := string(data)
	if !strings.Contains(text, "下载成功: a.zip") {
		t.Errorf("日志文件缺少 INFO 内容:\n%s", text)
	}
	if !strings.Contains(text, "[WARN ]") {
		t.Errorf("日志文件缺少级别标记:\n%s", text)
	}
	if !strings.Contains(text, "[STEP]") {
		t.Errorf("日志文件缺少 STEP 标记:\n%s", text)
	}
	if strings.Contains(text, "调试信息") {
		t.Errorf("debug 级别不应写入:\n%s", text)
	}
	if strings.Contains(text, "\x1b[") {
		t.Errorf("日志文件不应包含颜色转义:\n%s", text)
	}
	// 文件里应带完整日期。
	if !strings.Contains(text, "20") || strings.Count(text, "-") < 4 {
		t.Errorf("日志文件缺少日期:\n%s", text)
	}
	if !strings.Contains(buf.String(), "下载成功: a.zip") {
		t.Errorf("终端输出缺失")
	}
}

func TestAttachFileAppends(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "a.log")

	l1 := New(&strings.Builder{}, LevelInfo)
	if err := l1.AttachFile(path); err != nil {
		t.Fatal(err)
	}
	l1.Infof("第一次")
	l1.Close()

	l2 := New(&strings.Builder{}, LevelInfo)
	if err := l2.AttachFile(path); err != nil {
		t.Fatal(err)
	}
	l2.Infof("第二次")
	l2.Close()

	data, _ := os.ReadFile(path)
	if !strings.Contains(string(data), "第一次") || !strings.Contains(string(data), "第二次") {
		t.Errorf("追加写入失败:\n%s", data)
	}
}

func TestDetachFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "a.log")

	l := New(&strings.Builder{}, LevelInfo)
	if err := l.AttachFile(path); err != nil {
		t.Fatal(err)
	}
	l.Infof("写入一")
	l.DetachFile()
	if l.FilePath() != "" {
		t.Errorf("DetachFile 后 FilePath 应为空")
	}
	l.Infof("写入二")

	data, _ := os.ReadFile(path)
	if strings.Contains(string(data), "写入二") {
		t.Errorf("Detach 后不应再写入文件:\n%s", data)
	}
}

func TestAttachEmptyPath(t *testing.T) {
	var buf strings.Builder
	l := New(&buf, LevelInfo)
	if err := l.AttachFile(""); err != nil {
		t.Fatal(err)
	}
	if l.FilePath() != "" {
		t.Errorf("空路径不应启用文件")
	}
	l.Infof("only console")
	if !strings.Contains(buf.String(), "only console") {
		t.Errorf("终端输出缺失")
	}
}
