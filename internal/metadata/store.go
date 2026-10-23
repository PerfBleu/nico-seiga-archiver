// Package metadata 负责把下载结果记录到 JSON 文件。
package metadata

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// Record 是一个文件对应的元数据。
type Record struct {
	NicoURL        string    `json:"nico_url,omitempty"`
	NicoID         string    `json:"nico_id,omitempty"`
	Title          string    `json:"title,omitempty"`
	Author         string    `json:"author,omitempty"`
	AuthorURL      string    `json:"author_url,omitempty"`
	GetuploaderURL string    `json:"getuploader_url"`
	Filename       string    `json:"filename,omitempty"`
	FilePath       string    `json:"file_path,omitempty"`
	Size           int64     `json:"size,omitempty"`
	MD5            string    `json:"md5,omitempty"`
	SHA256         string    `json:"sha256,omitempty"`
	PasswordUsed   bool      `json:"password_used"`
	AlreadyExisted bool      `json:"already_existed,omitempty"`
	DirectURL      string    `json:"direct_url,omitempty"`
	DownloadedAt   time.Time `json:"downloaded_at"`
	Status         string    `json:"status"`
	Error          string    `json:"error,omitempty"`
}

// 状态取值。
const (
	StatusSuccess = "success"
	StatusFailed  = "failed"
)

// Store 是元数据存储。
type Store struct {
	mu      sync.Mutex
	path    string
	records []Record
	index   map[string]int
}

// Open 打开（或创建）元数据文件。
func Open(path string) (*Store, error) {
	s := &Store{path: path, index: map[string]int{}}
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return s, nil
		}
		return nil, err
	}
	if len(strings.TrimSpace(string(data))) == 0 {
		return s, nil
	}
	if err := json.Unmarshal(data, &s.records); err != nil {
		// 兼容逐行 JSON 的情况。
		var recs []Record
		for _, line := range strings.Split(string(data), "\n") {
			line = strings.TrimSpace(line)
			if line == "" || line == "[" || line == "]" {
				continue
			}
			line = strings.TrimSuffix(strings.TrimSuffix(line, ","), ",")
			var r Record
			if err := json.Unmarshal([]byte(line), &r); err == nil {
				recs = append(recs, r)
			}
		}
		if len(recs) == 0 {
			return nil, fmt.Errorf("解析元数据文件失败: %w", err)
		}
		s.records = recs
	}
	s.reindex()
	return s, nil
}

func (s *Store) reindex() {
	s.index = make(map[string]int, len(s.records))
	for i, r := range s.records {
		s.index[key(r.GetuploaderURL, r.Filename)] = i
	}
}

func key(guURL, filename string) string {
	return strings.TrimSpace(guURL) + "|" + strings.TrimSpace(filename)
}

// Add 写入一条记录（同一 getuploader URL + 文件名会覆盖旧记录）并立即落盘。
func (s *Store) Add(r Record) error {
	if r.DownloadedAt.IsZero() {
		r.DownloadedAt = time.Now()
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	k := key(r.GetuploaderURL, r.Filename)
	if i, ok := s.index[k]; ok {
		s.records[i] = r
	} else {
		s.records = append(s.records, r)
		s.index[k] = len(s.records) - 1
	}
	return s.saveLocked()
}

// Find 查找同一个 getuploader 链接的记录（文件名可为空表示任意）。
func (s *Store) Find(guURL, filename string) (*Record, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if filename != "" {
		if i, ok := s.index[key(guURL, filename)]; ok {
			r := s.records[i]
			return &r, true
		}
		return nil, false
	}
	for i := len(s.records) - 1; i >= 0; i-- {
		if s.records[i].GetuploaderURL == guURL {
			r := s.records[i]
			return &r, true
		}
	}
	return nil, false
}

// IsDownloaded 判断某个 getuploader 链接是否已成功下载且文件仍在。
func (s *Store) IsDownloaded(guURL string) bool {
	r, ok := s.Find(guURL, "")
	if !ok || r.Status != StatusSuccess {
		return false
	}
	if r.FilePath == "" {
		return false
	}
	if _, err := os.Stat(r.FilePath); err != nil {
		return false
	}
	return true
}

// Records 返回所有记录的副本。
func (s *Store) Records() []Record {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]Record, len(s.records))
	copy(out, s.records)
	return out
}

// Path 返回元数据文件路径。
func (s *Store) Path() string { return s.path }

// Save 落盘。
func (s *Store) Save() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.saveLocked()
}

func (s *Store) saveLocked() error {
	if err := os.MkdirAll(filepath.Dir(s.path), 0o755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(s.records, "", "  ")
	if err != nil {
		return err
	}
	tmp := s.path + ".tmp"
	if err := os.WriteFile(tmp, append(data, '\n'), 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, s.path)
}
