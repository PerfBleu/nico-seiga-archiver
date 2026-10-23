package state

import (
	"os"
	"path/filepath"
	"testing"
)

func TestStoreRoundTrip(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "progress.json")

	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := s.Get("tag:VOICEROID"); ok {
		t.Fatal("空存储不应有记录")
	}
	if err := s.Set("tag:VOICEROID", "tag「VOICEROID」", 3, false); err != nil {
		t.Fatal(err)
	}
	if err := s.Set("author:39911624", "作者 39911624", 2, true); err != nil {
		t.Fatal(err)
	}

	// 模拟重新启动。
	s2, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	e, ok := s2.Get("tag:VOICEROID")
	if !ok || e.LastPage != 3 || e.Completed {
		t.Fatalf("tag 进度读取错误: %+v ok=%v", e, ok)
	}
	if e.Label != "tag「VOICEROID」" {
		t.Errorf("label = %q", e.Label)
	}
	ae, ok := s2.Get("author:39911624")
	if !ok || !ae.Completed || ae.LastPage != 2 {
		t.Fatalf("author 进度读取错误: %+v", ae)
	}

	// 覆盖更新。
	if err := s2.Set("tag:VOICEROID", "tag「VOICEROID」", 5, true); err != nil {
		t.Fatal(err)
	}
	e, _ = s2.Get("tag:VOICEROID")
	if e.LastPage != 5 || !e.Completed {
		t.Fatalf("更新后进度错误: %+v", e)
	}

	entries := s2.Entries()
	if len(entries) != 2 {
		t.Fatalf("entries = %d, want 2", len(entries))
	}
	if entries[0].Key != "author:39911624" {
		t.Errorf("entries 应按 key 排序，首项 = %q", entries[0].Key)
	}

	// 清除。
	if err := s2.Clear("tag:VOICEROID"); err != nil {
		t.Fatal(err)
	}
	if _, ok := s2.Get("tag:VOICEROID"); ok {
		t.Fatal("Clear 后仍能读到记录")
	}
	if err := s2.ClearAll(); err != nil {
		t.Fatal(err)
	}
	if len(s2.Entries()) != 0 {
		t.Fatal("ClearAll 后仍有记录")
	}
}

func TestStoreCorruptedFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "progress.json")
	if err := os.WriteFile(path, []byte("{not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Open(path); err == nil {
		t.Fatal("损坏的进度文件应当报错")
	}
}
