// Package proxy 负责读取系统代理设置。
package proxy

import (
	"context"
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

// 约定：返回值 "none" 表示明确不使用代理，"" 表示没有找到代理。
const (
	Auto = "auto"
	None = "none"
)

// Result 是代理探测结果。
type Result struct {
	// URL 形如 http://127.0.0.1:7897 或 socks5://127.0.0.1:1080；空表示直连。
	URL string
	// Source 说明代理来源，便于用户排查。
	Source string
}

// Detect 读取系统代理。
//
// 顺序：环境变量 -> 平台特定配置（Linux: gsettings / KDE kioslaverc；macOS: scutil）。
// 如果系统里明确配置了「不使用代理」，返回 Source="system(none)"。
func Detect() Result {
	if r, ok := fromEnv(); ok {
		return r
	}
	switch runtime.GOOS {
	case "darwin":
		if r, ok := fromScutil(); ok {
			return r
		}
	case "windows":
		if r, ok := fromWindows(); ok {
			return r
		}
	default:
		if r, ok := fromGSettings(); ok {
			return r
		}
		if r, ok := fromKioslaverc(); ok {
			return r
		}
	}
	return Result{URL: "", Source: "system(none)"}
}

// Resolve 根据配置值（auto/none/直接 URL）解析出最终代理地址。
func Resolve(setting string) Result {
	switch strings.ToLower(strings.TrimSpace(setting)) {
	case "", Auto:
		return Detect()
	case None, "off", "no", "direct", "直连":
		return Result{URL: "", Source: "config(none)"}
	default:
		u := Normalize(setting)
		return Result{URL: u, Source: "config"}
	}
}

// Normalize 规范化代理地址（补全 scheme）。
func Normalize(s string) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return ""
	}
	if !strings.Contains(s, "://") {
		s = "http://" + s
	}
	return s
}

// Parse 返回可用于 http.Transport 的代理 URL。
func Parse(s string) (*url.URL, error) {
	s = Normalize(s)
	if s == "" {
		return nil, nil
	}
	u, err := url.Parse(s)
	if err != nil {
		return nil, fmt.Errorf("代理地址无效: %w", err)
	}
	if u.Host == "" {
		return nil, fmt.Errorf("代理地址缺少主机: %s", s)
	}
	return u, nil
}

func fromEnv() (Result, bool) {
	keys := []string{
		"HTTPS_PROXY", "https_proxy",
		"HTTP_PROXY", "http_proxy",
		"ALL_PROXY", "all_proxy",
	}
	for _, k := range keys {
		v := strings.TrimSpace(os.Getenv(k))
		if v == "" {
			continue
		}
		if strings.EqualFold(v, "none") {
			return Result{URL: "", Source: "env(" + k + "=none)"}, true
		}
		return Result{URL: Normalize(v), Source: "env(" + k + ")"}, true
	}
	return Result{}, false
}

func fromGSettings() (Result, bool) {
	if _, err := exec.LookPath("gsettings"); err != nil {
		return Result{}, false
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	mode := gs(ctx, "org.gnome.system.proxy", "mode")
	switch strings.Trim(mode, `'`) {
	case "none":
		return Result{URL: "", Source: "gsettings(none)"}, true
	case "manual":
		// 优先 https 代理，其次 http，最后 socks。
		for _, schema := range []string{"org.gnome.system.proxy.https", "org.gnome.system.proxy.http", "org.gnome.system.proxy.socks"} {
			host := strings.Trim(gs(ctx, schema, "host"), `'`)
			port := strings.Trim(gs(ctx, schema, "port"), `'`)
			if host == "" || port == "" || port == "0" {
				continue
			}
			scheme := "http"
			if strings.HasSuffix(schema, ".socks") {
				scheme = "socks5"
			}
			return Result{URL: fmt.Sprintf("%s://%s:%s", scheme, host, port), Source: "gsettings(" + schema + ")"}, true
		}
		return Result{}, false
	case "auto":
		// PAC 无法在此实现，退回到环境变量/直连。
		return Result{URL: "", Source: "gsettings(pac-unsupported)"}, true
	}
	return Result{}, false
}

func gs(ctx context.Context, schema, key string) string {
	out, err := exec.CommandContext(ctx, "gsettings", "get", schema, key).Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

func fromKioslaverc() (Result, bool) {
	home, err := os.UserHomeDir()
	if err != nil {
		return Result{}, false
	}
	data, err := os.ReadFile(filepath.Join(home, ".config", "kioslaverc"))
	if err != nil {
		return Result{}, false
	}
	var httpProxy, httpsProxy, socksProxy, proxyType string
	for _, line := range strings.Split(string(data), "\n") {
		key, val, ok := strings.Cut(strings.TrimSpace(line), "=")
		if !ok {
			continue
		}
		switch strings.TrimSpace(key) {
		case "httpProxy":
			httpProxy = strings.TrimSpace(val)
		case "httpsProxy":
			httpsProxy = strings.TrimSpace(val)
		case "socksProxy":
			socksProxy = strings.TrimSpace(val)
		case "ProxyType":
			proxyType = strings.TrimSpace(val)
		}
	}
	if proxyType == "0" {
		return Result{URL: "", Source: "kioslaverc(none)"}, true
	}
	for _, v := range []string{httpsProxy, httpProxy, socksProxy} {
		if v == "" || v == "http://" {
			continue
		}
		if strings.HasPrefix(v, "socks://") {
			v = "socks5://" + strings.TrimPrefix(v, "socks://")
		}
		return Result{URL: Normalize(v), Source: "kioslaverc"}, true
	}
	return Result{}, false
}

func fromWindows() (Result, bool) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	const key = `HKCU\Software\Microsoft\Windows\CurrentVersion\Internet Settings`
	out, err := exec.CommandContext(ctx, "reg", "query", key, "/v", "ProxyEnable").Output()
	if err != nil || !strings.Contains(strings.ToLower(string(out)), "0x1") {
		return Result{}, false
	}
	out, err = exec.CommandContext(ctx, "reg", "query", key, "/v", "ProxyServer").Output()
	if err != nil {
		return Result{}, false
	}
	_, val, ok := strings.Cut(string(out), "REG_SZ")
	if !ok {
		return Result{}, false
	}
	val = strings.TrimSpace(val)
	// ProxyServer 可能是 "host:port" 或 "http=host:port;https=host:port"。
	if strings.Contains(val, "=") {
		best := ""
		for _, part := range strings.Split(val, ";") {
			k, v, ok := strings.Cut(part, "=")
			if !ok {
				continue
			}
			if strings.EqualFold(k, "https") || strings.EqualFold(k, "http") {
				best = v
				break
			}
		}
		val = best
	}
	if val == "" {
		return Result{}, false
	}
	return Result{URL: Normalize(val), Source: "registry"}, true
}

func fromScutil() (Result, bool) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, "scutil", "--proxy").Output()
	if err != nil {
		return Result{}, false
	}
	vals := map[string]string{}
	for _, line := range strings.Split(string(out), "\n") {
		k, v, ok := strings.Cut(strings.TrimSpace(line), " : ")
		if ok {
			vals[strings.TrimSpace(k)] = strings.TrimSpace(v)
		}
	}
	if vals["HTTPSEnable"] == "1" && vals["HTTPSProxy"] != "" {
		return Result{URL: fmt.Sprintf("http://%s:%s", vals["HTTPSProxy"], vals["HTTPSPort"]), Source: "scutil(HTTPS)"}, true
	}
	if vals["HTTPEnable"] == "1" && vals["HTTPProxy"] != "" {
		return Result{URL: fmt.Sprintf("http://%s:%s", vals["HTTPProxy"], vals["HTTPPort"]), Source: "scutil(HTTP)"}, true
	}
	if vals["SOCKSEnable"] == "1" && vals["SOCKSProxy"] != "" {
		return Result{URL: fmt.Sprintf("socks5://%s:%s", vals["SOCKSProxy"], vals["SOCKSPort"]), Source: "scutil(SOCKS)"}, true
	}
	return Result{}, false
}
