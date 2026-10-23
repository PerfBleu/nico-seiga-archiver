// Package httpclient 封装带代理、重试、Cookie 的 HTTP 客户端。
package httpclient

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"strings"
	"time"

	"nico-seiga-archiver/internal/logx"
	"nico-seiga-archiver/internal/proxy"
)

// Options 客户端参数。
type Options struct {
	Proxy     string // auto/none/URL
	UserAgent string
	Timeout   time.Duration
	// NicoCookie 形如 "user_session=xxxx; ..."，只对 nicovideo.jp 域名附加。
	NicoCookie string
	Log        *logx.Logger
	Debug      bool
}

// Client 是封装后的 HTTP 客户端。
type Client struct {
	HTTP *http.Client
	Log  *logx.Logger
	UA   string
	// ProxyInfo 记录代理来源。
	ProxyInfo proxy.Result
	// PageTimeout 用于页面/API 请求的超时（下载文件不受此限制）。
	PageTimeout time.Duration
	debug       bool
}

// New 创建客户端。
func New(opts Options) (*Client, error) {
	if opts.Timeout <= 0 {
		opts.Timeout = 120 * time.Second
	}
	jar, err := cookiejar.New(nil)
	if err != nil {
		return nil, err
	}
	pr := proxy.Resolve(opts.Proxy)
	pu, err := proxy.Parse(pr.URL)
	if err != nil {
		return nil, err
	}
	transport := &http.Transport{
		Proxy:                 http.ProxyURL(pu),
		DialContext:           (&net.Dialer{Timeout: 30 * time.Second, KeepAlive: 30 * time.Second}).DialContext,
		ForceAttemptHTTP2:     true,
		MaxIdleConns:          100,
		MaxIdleConnsPerHost:   16,
		IdleConnTimeout:       90 * time.Second,
		TLSHandshakeTimeout:   20 * time.Second,
		ExpectContinueTimeout: 1 * time.Second,
		ResponseHeaderTimeout: 60 * time.Second,
	}
	if pu == nil {
		transport.Proxy = nil
	}
	var rt http.RoundTripper = transport
	if strings.TrimSpace(opts.NicoCookie) != "" {
		rt = &cookieInjector{base: rt, cookie: strings.TrimSpace(opts.NicoCookie)}
	}
	if opts.Log != nil && opts.Debug {
		rt = &loggingTransport{base: rt, log: opts.Log}
	}
	ua := opts.UserAgent
	if strings.TrimSpace(ua) == "" {
		ua = "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/124.0.0.0 Safari/537.36"
	}
	c := &http.Client{
		Transport: rt,
		Jar:       jar,
		// 不用 Client.Timeout：它会把大文件下载也算进去。页面请求用 PageContext 控制超时。
	}
	return &Client{HTTP: c, Log: opts.Log, UA: ua, ProxyInfo: pr, PageTimeout: opts.Timeout, debug: opts.Debug}, nil
}

// PageContext 为页面/API 请求添加超时。
func (c *Client) PageContext(ctx context.Context) (context.Context, context.CancelFunc) {
	if c.PageTimeout <= 0 {
		return ctx, func() {}
	}
	return context.WithTimeout(ctx, c.PageTimeout)
}

// Request 新建一个带默认头的请求。
func (c *Client) Request(ctx context.Context, method, rawurl string, body io.Reader) (*http.Request, error) {
	req, err := http.NewRequestWithContext(ctx, method, rawurl, body)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", c.UA)
	req.Header.Set("Accept", "text/html,application/xhtml+xml,application/xml;q=0.9,image/avif,image/webp,*/*;q=0.8")
	req.Header.Set("Accept-Language", "ja,en-US;q=0.8,en;q=0.6,zh-CN;q=0.5")
	return req, nil
}

// Get 发起 GET 并返回响应；对 429/5xx 做有限次重试。
func (c *Client) Get(ctx context.Context, rawurl string) (*http.Response, error) {
	return c.doWithRetry(ctx, http.MethodGet, rawurl, nil, "")
}

// GetOnce 只发起一次 GET，不重试（用于连通性检查）。
func (c *Client) GetOnce(ctx context.Context, rawurl string) (*http.Response, error) {
	req, err := c.Request(ctx, http.MethodGet, rawurl, nil)
	if err != nil {
		return nil, err
	}
	return c.HTTP.Do(req)
}

// PostForm 提交表单（url.Values 已编码）。
func (c *Client) PostForm(ctx context.Context, rawurl, form string, referer string) (*http.Response, error) {
	return c.doWithRetry(ctx, http.MethodPost, rawurl, strings.NewReader(form), referer)
}

func (c *Client) doWithRetry(ctx context.Context, method, rawurl string, body io.Reader, referer string) (*http.Response, error) {
	var bodyBytes []byte
	if body != nil {
		b, err := io.ReadAll(body)
		if err != nil {
			return nil, err
		}
		bodyBytes = b
	}
	const maxAttempts = 3
	var lastErr error
	for attempt := 1; attempt <= maxAttempts; attempt++ {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		var rd io.Reader
		if bodyBytes != nil {
			rd = strings.NewReader(string(bodyBytes))
		}
		req, err := c.Request(ctx, method, rawurl, rd)
		if err != nil {
			return nil, err
		}
		if bodyBytes != nil {
			req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		}
		if referer != "" {
			req.Header.Set("Referer", referer)
		}
		if c.Log != nil {
			c.Log.Debugf("%s %s", method, rawurl)
		}
		resp, err := c.HTTP.Do(req)
		if err != nil {
			// 用户取消（Ctrl+C）或超时：不再重试，也不打重试日志。
			if ctxErr := ctx.Err(); ctxErr != nil {
				return nil, ctxErr
			}
			lastErr = err
			if attempt < maxAttempts && retryableError(err) {
				wait := time.Duration(attempt) * 2 * time.Second
				if c.Log != nil {
					c.Log.Warnf("请求失败（%v），%s 后重试...", err, wait)
				}
				select {
				case <-time.After(wait):
					continue
				case <-ctx.Done():
					return nil, ctx.Err()
				}
			}
			return nil, err
		}
		if resp.StatusCode == http.StatusTooManyRequests || resp.StatusCode >= 500 {
			lastErr = fmt.Errorf("HTTP %d: %s", resp.StatusCode, rawurl)
			if attempt < maxAttempts {
				io.Copy(io.Discard, io.LimitReader(resp.Body, 4096))
				resp.Body.Close()
				wait := time.Duration(attempt) * 3 * time.Second
				if c.Log != nil {
					c.Log.Warnf("服务端返回 %d，%s 后重试...", resp.StatusCode, wait)
				}
				select {
				case <-time.After(wait):
					continue
				case <-ctx.Done():
					return nil, ctx.Err()
				}
			}
		}
		return resp, nil
	}
	return nil, lastErr
}

func retryableError(err error) bool {
	var ne net.Error
	if errors.As(err, &ne) {
		return true
	}
	msg := err.Error()
	for _, s := range []string{"connection reset", "EOF", "timeout", "temporary failure"} {
		if strings.Contains(strings.ToLower(msg), s) {
			return true
		}
	}
	return false
}

// ResolveURL 把相对地址解析成绝对地址。
func ResolveURL(base, ref string) (string, error) {
	b, err := url.Parse(base)
	if err != nil {
		return "", err
	}
	r, err := url.Parse(ref)
	if err != nil {
		return "", err
	}
	return b.ResolveReference(r).String(), nil
}

// CookieHeaderFor 返回 cookie jar 中某个 URL 的 Cookie 头（调试用）。
func (c *Client) CookieHeaderFor(rawurl string) string {
	u, err := url.Parse(rawurl)
	if err != nil {
		return ""
	}
	cs := c.HTTP.Jar.Cookies(u)
	parts := make([]string, 0, len(cs))
	for _, ck := range cs {
		parts = append(parts, ck.Name+"="+ck.Value)
	}
	return strings.Join(parts, "; ")
}

type cookieInjector struct {
	base   http.RoundTripper
	cookie string
}

func (c *cookieInjector) RoundTrip(req *http.Request) (*http.Response, error) {
	host := strings.ToLower(req.URL.Hostname())
	if strings.HasSuffix(host, "nicovideo.jp") || host == "nicovideo.jp" {
		if req.Header.Get("Cookie") == "" {
			req = req.Clone(req.Context())
			req.Header.Set("Cookie", c.cookie)
		}
	}
	return c.base.RoundTrip(req)
}

type loggingTransport struct {
	base http.RoundTripper
	log  *logx.Logger
}

func (t *loggingTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	resp, err := t.base.RoundTrip(req)
	if err != nil {
		t.log.Debugf("  <- 错误: %v", err)
		return nil, err
	}
	t.log.Debugf("  <- %d %s (%s)", resp.StatusCode, req.URL.Redacted(), resp.Header.Get("Content-Type"))
	return resp, nil
}
