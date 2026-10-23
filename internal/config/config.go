// Package config 负责配置文件的读写。
package config

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"nico-seiga-archiver/internal/proxy"
)

// DefaultUserAgent 是本工具默认使用的 UA。
const DefaultUserAgent = "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/124.0.0.0 Safari/537.36"

// Config 是持久化配置。
type Config struct {
	// DownloadDir 下载根目录，文件会存放在 <DownloadDir>/<作者名>/ 下。
	DownloadDir string `json:"download_dir"`
	// Proxy 代理设置："auto" 读取系统代理，"none" 直连，或直接写代理地址。
	Proxy string `json:"proxy"`
	// NicoCookie 用于访问 seiga.nicovideo.jp 的 Cookie 头（搜索和完整简介需要登录）。
	NicoCookie string `json:"nico_cookie"`
	// UserAgent 自定义 UA。
	UserAgent string `json:"user_agent"`
	// DelayMS 每次请求之间的等待时间（毫秒），避免打扰站点。
	DelayMS int `json:"delay_ms"`
	// TimeoutSec 单次 HTTP 请求超时（秒）。
	TimeoutSec int `json:"timeout_sec"`
	// AskPassword 当自动提取不到密码时是否交互询问。
	AskPassword bool `json:"ask_password"`
	// MetadataFile 元数据文件名（相对 DownloadDir，或绝对路径）。
	MetadataFile string `json:"metadata_file"`
	// LogLevel debug/info/warn/error
	LogLevel string `json:"log_level"`
	// MaxPages 单次批量任务最多翻多少页（防止死循环）。
	MaxPages int `json:"max_pages"`
}

// Default 返回默认配置，下载目录为当前工作目录。
func Default() *Config {
	cwd, err := os.Getwd()
	if err != nil {
		cwd = "."
	}
	return &Config{
		DownloadDir:  cwd,
		Proxy:        proxy.Auto,
		UserAgent:    DefaultUserAgent,
		DelayMS:      1000,
		TimeoutSec:   60,
		AskPassword:  true,
		MetadataFile: "metadata.json",
		LogLevel:     "info",
		MaxPages:     500,
	}
}

// Path 返回配置文件路径。
// 可用环境变量 NICOSEIGA_CONFIG 指定自定义路径（便于测试或多套配置）。
func Path() (string, error) {
	if v := strings.TrimSpace(os.Getenv("NICOSEIGA_CONFIG")); v != "" {
		return expandHome(v), nil
	}
	dir, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "nico-seiga-archiver", "config.json"), nil
}

// Load 读取配置；文件不存在时返回默认配置。
func Load() (*Config, string, error) {
	path, err := Path()
	if err != nil {
		cfg := Default()
		return cfg, "", err
	}
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return Default(), path, nil
	}
	if err != nil {
		return Default(), path, err
	}
	cfg := Default()
	if err := json.Unmarshal(data, cfg); err != nil {
		return Default(), path, fmt.Errorf("解析配置文件失败: %w", err)
	}
	cfg.Normalize()
	return cfg, path, nil
}

// Normalize 补齐空字段。
func (c *Config) Normalize() {
	def := Default()
	if strings.TrimSpace(c.DownloadDir) == "" {
		c.DownloadDir = def.DownloadDir
	}
	if strings.TrimSpace(c.Proxy) == "" {
		c.Proxy = proxy.Auto
	}
	if strings.TrimSpace(c.UserAgent) == "" {
		c.UserAgent = DefaultUserAgent
	}
	if c.DelayMS < 0 {
		c.DelayMS = def.DelayMS
	}
	if c.TimeoutSec <= 0 {
		c.TimeoutSec = def.TimeoutSec
	}
	if strings.TrimSpace(c.MetadataFile) == "" {
		c.MetadataFile = "metadata.json"
	}
	if strings.TrimSpace(c.LogLevel) == "" {
		c.LogLevel = "info"
	}
	if c.MaxPages <= 0 {
		c.MaxPages = def.MaxPages
	}
	c.DownloadDir = expandHome(c.DownloadDir)
}

// Save 原子写入配置文件（权限 0600，因为可能包含 Cookie）。
func (c *Config) Save() (string, error) {
	path, err := Path()
	if err != nil {
		return "", err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return "", err
	}
	data, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return "", err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, append(data, '\n'), 0o600); err != nil {
		return "", err
	}
	if err := os.Rename(tmp, path); err != nil {
		return "", err
	}
	return path, nil
}

// MetadataPath 返回元数据文件绝对路径。
func (c *Config) MetadataPath() string {
	if filepath.IsAbs(c.MetadataFile) {
		return c.MetadataFile
	}
	return filepath.Join(c.DownloadDir, c.MetadataFile)
}

// Timeout 返回请求超时。
func (c *Config) Timeout() time.Duration {
	return time.Duration(c.TimeoutSec) * time.Second
}

// Delay 返回请求间隔。
func (c *Config) Delay() time.Duration {
	return time.Duration(c.DelayMS) * time.Millisecond
}

// MaskedCookie 返回用于展示的 Cookie 摘要。
func (c *Config) MaskedCookie() string {
	if strings.TrimSpace(c.NicoCookie) == "" {
		return "(未设置)"
	}
	parts := strings.Split(c.NicoCookie, ";")
	names := make([]string, 0, len(parts))
	for _, p := range parts {
		name, _, ok := strings.Cut(strings.TrimSpace(p), "=")
		if ok && name != "" {
			names = append(names, name)
		}
	}
	return fmt.Sprintf("已设置 (%s)", strings.Join(names, ", "))
}

func expandHome(p string) string {
	if p == "~" || strings.HasPrefix(p, "~/") {
		if home, err := os.UserHomeDir(); err == nil {
			return filepath.Join(home, strings.TrimPrefix(p, "~"))
		}
	}
	return p
}
