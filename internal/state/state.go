// Package state 记录各类索引（搜索 / tag / 作者）的下载进度，
// 以便下次运行时「没有指定页码就从历史记录的那一页继续」。
package state

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

// Entry 是一个索引的进度记录。
type Entry struct {
	// Key 形如 "tag:VOICEROID"、"search:ボイロ"、"author:39911624"。
	Key   string `json:"key"`
	Label string `json:"label,omitempty"`
	// LastPage 是最后一次处理过的页码；下次未显式指定页码时从这一页重新开始。
	LastPage int `json:"last_page"`
	// Completed 表示上一次运行已经翻到最后一页。
	Completed bool      `json:"completed,omitempty"`
	UpdatedAt time.Time `json:"updated_at"`
}

// Store 是进度存储（JSON 文件）。
type Store struct {
	mu      sync.Mutex
	path    string
	entries map[string]Entry
}

// Open 打开（或创建）进度文件。
func Open(path string) (*Store, error) {
	s := &Store{path: path, entries: map[string]Entry{}}
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return s, nil
		}
		return nil, err
	}
	if strings.TrimSpace(string(data)) == "" {
		return s, nil
	}
	var list []Entry
	if err := json.Unmarshal(data, &list); err != nil {
		return nil, fmt.Errorf("解析进度文件失败: %w", err)
	}
	for _, e := range list {
		if e.Key == "" {
			continue
		}
		s.entries[e.Key] = e
	}
	return s, nil
}

// Path 返回进度文件路径。
func (s *Store) Path() string { return s.path }

// Get 查询某个索引的进度。
func (s *Store) Get(key string) (Entry, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	e, ok := s.entries[key]
	return e, ok
}

// Set 记录某个索引处理到的页码。
func (s *Store) Set(key, label string, page int, completed bool) error {
	if strings.TrimSpace(key) == "" {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.entries[key] = Entry{
		Key:       key,
		Label:     label,
		LastPage:  page,
		Completed: completed,
		UpdatedAt: time.Now(),
	}
	return s.saveLocked()
}

// Clear 删除某个索引的进度。
func (s *Store) Clear(key string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.entries[key]; !ok {
		return nil
	}
	delete(s.entries, key)
	return s.saveLocked()
}

// ClearAll 清空全部进度。
func (s *Store) ClearAll() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.entries = map[string]Entry{}
	return s.saveLocked()
}

// Entries 返回按 key 排序的进度列表。
func (s *Store) Entries() []Entry {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]Entry, 0, len(s.entries))
	for _, e := range s.entries {
		out = append(out, e)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Key < out[j].Key })
	return out
}

func (s *Store) saveLocked() error {
	if err := os.MkdirAll(filepath.Dir(s.path), 0o700); err != nil {
		return err
	}
	list := make([]Entry, 0, len(s.entries))
	for _, e := range s.entries {
		list = append(list, e)
	}
	sort.Slice(list, func(i, j int) bool { return list[i].Key < list[j].Key })
	data, err := json.MarshalIndent(list, "", "  ")
	if err != nil {
		return err
	}
	tmp := s.path + ".tmp"
	if err := os.WriteFile(tmp, append(data, '\n'), 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, s.path)
}
