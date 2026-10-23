// Package extract 从简介文本中提取 getuploader 链接和密码。
package extract

import (
	"regexp"
	"strings"

	"nico-seiga-archiver/internal/util"
)

// Link 是一个 getuploader 下载页链接（可能带密码）。
type Link struct {
	URL      string
	Password string
	// PasswordSource 说明密码是怎么来的：keyword / after-url / bracket / digits / none
	PasswordSource string
	// Context 是链接附近的原始文本片段，便于日志与人工核对。
	Context string
}

var (
	// 允许省略 scheme，也允许 getuploader.com 的各种子域名。
	reURL = regexp.MustCompile(`(?i)(?:https?://)?(?:[a-z0-9-]+\.)*getuploader\.com/[^\s<>"' \x{3000}【】（）()「」『』\[\]]+`)

	// 带关键词的密码，例如「パスワード：2525」「pass【2525】」「pw=2525」
	rePassKeyword = regexp.MustCompile(`(?i)(?:パスワード|パスコード|合言葉|暗証番号|パス|キーコード|キー|pass(?:word)?|pw|key)\s*(?:は|を|:|：|=|＝|＞|>|-|–|—|【|\[|（|\(|「|『)?\s*([A-Za-z0-9!@#$%^&*_\-\.]{2,24})`)

	// URL 后面紧跟的数字/字母组合，例如「.../download/71 2525」
	reAfterURLDigits = regexp.MustCompile(`^[\s\x{3000}、,，。:：=＝\-–—|｜/／]*([A-Za-z0-9]{3,16})\b`)

	// URL 后面紧跟的括号内容，例如「…/71（2525）」
	reBracket = regexp.MustCompile(`^\s*[【\[（(「『]\s*([A-Za-z0-9!@#$%^&*_\-\.]{2,24})\s*[】\]）)」』]`)

	reTrailing = `.,;:!?'"”’、。，；：！？】」』）)]}`
)

// Links 从文本中提取所有 getuploader 下载页链接。
// 同一个 URL 只保留第一次出现的密码信息。
func Links(text string) []Link {
	text = normalizeWidth(text)
	matches := reURL.FindAllStringIndex(text, -1)
	if len(matches) == 0 {
		return nil
	}

	var out []Link
	seen := map[string]bool{}
	for i, m := range matches {
		raw := text[m[0]:m[1]]
		raw = strings.TrimRight(raw, reTrailing+"…")
		normalized, ok := NormalizeURL(raw)
		if !ok || seen[normalized] {
			continue
		}
		seen[normalized] = true

		// 每个链接的上下文：上一个链接之后 ~ 下一个链接之前。
		start := m[0] - 200
		if start < 0 {
			start = 0
		}
		if i > 0 {
			if prev := matches[i-1][1]; prev > start {
				start = prev
			}
		}
		end := m[1] + 300
		if end > len(text) {
			end = len(text)
		}
		if i+1 < len(matches) && matches[i+1][0] < end {
			end = matches[i+1][0]
		}
		seg := text[start:end]

		pw, source := passwordInSegment(seg, m[1]-start)
		out = append(out, Link{
			URL:            normalized,
			Password:       pw,
			PasswordSource: source,
			Context:        util.OneLine(seg),
		})
	}
	return out
}

// NormalizeURL 规范化 getuploader 链接。
func NormalizeURL(raw string) (string, bool) {
	raw = strings.TrimSpace(raw)
	raw = strings.TrimRight(raw, reTrailing+"…")
	if raw == "" {
		return "", false
	}
	lower := strings.ToLower(raw)
	if !strings.Contains(lower, "getuploader.com/") {
		return "", false
	}
	if !strings.Contains(lower, "/download/") {
		// 只处理具体的下载页，跳过上传者首页等。
		return "", false
	}
	if !strings.Contains(lower, "://") {
		raw = "https://" + raw
	}
	// getuploader.com 裸域名的下载页实际在 uu 子域上。
	raw = strings.Replace(raw, "://www.getuploader.com/", "://uu.getuploader.com/", 1)
	raw = strings.Replace(raw, "://getuploader.com/", "://uu.getuploader.com/", 1)
	return raw, true
}

func passwordInSegment(seg string, urlEnd int) (string, string) {
	if urlEnd > len(seg) {
		urlEnd = len(seg)
	}
	after := seg[urlEnd:]
	before := seg[:urlEnd]
	if urlEnd > 240 {
		before = before[urlEnd-240:]
	}

	// 1) 优先看 URL 之后（同一段内）的密码，避免把上一个链接的密码误用。
	if m := rePassKeyword.FindStringSubmatch(after); m != nil {
		if pw := cleanPassword(m[1]); pw != "" {
			return pw, "keyword"
		}
	}
	// 2) URL 紧跟括号内容。
	if m := reBracket.FindStringSubmatch(after); m != nil {
		if pw := cleanPassword(m[1]); pw != "" {
			return pw, "bracket"
		}
	}
	// 3) URL 后面同一行的数字/字母。
	line := after
	if idx := strings.IndexAny(line, "\n"); idx >= 0 {
		line = line[:idx]
	}
	if m := reAfterURLDigits.FindStringSubmatch(line); m != nil {
		if pw := cleanPassword(m[1]); pw != "" {
			return pw, "after-url"
		}
	}
	// 4) 再看看 URL 前面的最后一段文字。
	if urlEnd > 240 {
		before = seg[urlEnd-240 : urlEnd]
	}
	if m := rePassKeyword.FindStringSubmatch(before); m != nil {
		if pw := cleanPassword(m[1]); pw != "" {
			return pw, "keyword-before"
		}
	}
	// 5) 前置文本里找「4 位数字」这种最常见的手写密码。
	if pw := findBareDigits(before); pw != "" {
		return pw, "digits"
	}
	return "", ""
}

func findBareDigits(s string) string {
	// 只从 URL 前面的最后 120 个字符里找，降低误判。
	if len(s) > 120 {
		s = s[len(s)-120:]
	}
	re := regexp.MustCompile(`(?:^|[\s\x{3000}、,，/／|｜:：=＝【\[（(「『])(\d{3,8})(?:$|[\s\x{3000}、,，/／|｜】\]）)」』])`)
	m := re.FindStringSubmatch(s)
	if m == nil {
		return ""
	}
	return m[1]
}

func cleanPassword(s string) string {
	s = strings.TrimSpace(s)
	s = strings.Trim(s, `"'“”‘’`)
	s = strings.TrimRight(s, reTrailing)
	s = strings.TrimSpace(s)
	if s == "" {
		return ""
	}
	lower := strings.ToLower(s)
	switch lower {
	case "pass", "password", "url", "getuploader", "download", "http", "https", "zip", "rar", "7z":
		return ""
	}
	// 纯字母且长度 <= 3 的，多半是误提取（如「pass」的残留）。
	if len(s) <= 2 {
		return ""
	}
	return s
}

// normalizeWidth 把全角英文/数字/常见符号转成半角，长度保持不变（便于下标对应）。
func normalizeWidth(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	for _, r := range s {
		switch {
		case r >= '０' && r <= '９':
			b.WriteRune('0' + (r - '０'))
		case r >= 'Ａ' && r <= 'Ｚ':
			b.WriteRune('A' + (r - 'Ａ'))
		case r >= 'ａ' && r <= 'ｚ':
			b.WriteRune('a' + (r - 'ａ'))
		case r == '：':
			b.WriteRune(':')
		case r == '＝':
			b.WriteRune('=')
		case r == '　':
			b.WriteRune(' ')
		default:
			b.WriteRune(r)
		}
	}
	return b.String()
}
