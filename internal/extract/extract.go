// Package extract 从简介文本中提取 getuploader 链接和密码。
//
// 注意：作者有时会用全角字符写密码（如「２５２５」），此时「２５２５」才是真密码，
// 转成半角反而会错。因此这里的密码一律保留原文宽度，交给调用方在失败后再尝试半角版本。
package extract

import (
	"regexp"
	"strings"
	"unicode/utf8"

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

// Alternates 返回依次尝试的密码候选：先原文（可能是全角），再半角版本。
// 两者相同时只返回一个。
func (l Link) Alternates() []string {
	return PasswordAlternates(l.Password)
}

// PasswordAlternates 返回密码的尝试顺序：原始写法优先，其次半角转换。
func PasswordAlternates(p string) []string {
	p = strings.TrimSpace(p)
	if p == "" {
		return nil
	}
	out := []string{p}
	if hw := ToHalfWidth(p); hw != p {
		out = append(out, hw)
	}
	return out
}

// 关键词与密码之间可能出现的连接符/助词。两边用 pwGap 允许空白（最多跨一行）。
const (
	pwGap = `[ \t\x{3000}]*(?:\r?\n[ \t\x{3000}]*)?`
	// 连接符：日文助词、冒号、等号、箭头、破折号、括号开头等
	pwSep = `(?:は|を|が|:|：|=|＝|＞|>|→|⇒|≫|-|–|—|ー|【|\[|（|\(|「|『|、|，|,)?`
	// 密码本体：ASCII 可见字符；全角字符在归一化视图里会映射成半角后再匹配。
	pwBody = `([A-Za-z0-9!@#$%^&*_\-\.]{2,24})`
)

var (
	// 允许省略 scheme，也允许 getuploader.com 的各种子域名。
	reURL = regexp.MustCompile(`(?i)(?:https?://)?(?:[a-z0-9-]+\.)*getuploader\.com/[^\s<>"' \x{3000}【】（）()「」『』\[\]]+`)

	// 日文关键词：パスワード / パスワードは / パスコード / パス / キー 等。
	// 长的写在前面，保证「パスワード」不会被「パス」抢先匹配。
	rePassJP = regexp.MustCompile(`(?:ダウンロードパスワード|パスワード|パスコード|暗証番号|合言葉|キーコード|パスキー|パス|キー)` + pwGap + pwSep + pwGap + pwBody)

	// 英文关键词：password / pass / pw / pwd / passwd / passcode / key。
	// 用 \b 避免匹配到 bypass、keyword 之类单词内部。
	rePassEN = regexp.MustCompile(`(?i)\b(?:password|passwd|passcode|pwd|pass|pw|key)\b` + pwGap + pwSep + pwGap + pwBody)

	// URL 后面紧跟的数字/字母组合，例如「.../download/71 2525」
	reAfterURLDigits = regexp.MustCompile(`^[\s\x{3000}、,，。:：=＝\-–—|｜/／]*([A-Za-z0-9]{3,16})\b`)

	// URL 后面紧跟的括号内容，例如「…/71（2525）」
	reBracket = regexp.MustCompile(`^\s*[【\[（(「『]\s*([A-Za-z0-9!@#$%^&*_\-\.]{2,24})\s*[】\]）)」』]`)

	// 前置文本里的裸数字密码，例如「2525 https://...」
	reBareDigits = regexp.MustCompile(`(?:^|[\s\x{3000}、,，/／|｜:：=＝【\[（(「『])(\d{3,8})(?:$|[\s\x{3000}、,，/／|｜】\]）)」』])`)

	// 所有「关键词 + 密码」形式的正则，按顺序尝试，取最靠近 URL 的那个。
	keywordPatterns = []*regexp.Regexp{rePassJP, rePassEN}

	reTrailing = `.,;:!?'"”’、。，；：！？】」』）)]}`
)

// view 是一个文本的两种视图：原文（保留全角）与宽度归一化（全角转半角）。
// 两者 rune 数量始终相同，因此同一 rune 下标可以互相映射。
type view struct {
	orig []rune
	norm []rune
}

func newView(s string) view {
	orig := []rune(s)
	norm := make([]rune, len(orig))
	for i, r := range orig {
		norm[i] = toHalfWidthRune(r)
	}
	return view{orig: orig, norm: norm}
}

// Links 从文本中提取所有 getuploader 下载页链接。
// 同一个 URL 只保留第一次出现的密码信息。
func Links(text string) []Link {
	v := newView(text)
	normText := string(v.norm)

	matches := reURL.FindAllStringIndex(normText, -1)
	if len(matches) == 0 {
		return nil
	}
	// 把字节下标换成 rune 下标，方便和原文对齐。
	idx := runeIndexMap(normText)
	var runeMatches [][2]int
	for _, m := range matches {
		runeMatches = append(runeMatches, [2]int{idx[m[0]], idx[m[1]]})
	}

	var out []Link
	seen := map[string]bool{}
	for i, m := range runeMatches {
		// URL 本身取归一化视图，这样全角写法的链接也能识别。
		raw := string(v.norm[m[0]:m[1]])
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
			if prev := runeMatches[i-1][1]; prev > start {
				start = prev
			}
		}
		end := m[1] + 300
		if end > len(v.orig) {
			end = len(v.orig)
		}
		if i+1 < len(runeMatches) && runeMatches[i+1][0] < end {
			end = runeMatches[i+1][0]
		}

		pw, source := passwordInSegment(v.orig[start:end], v.norm[start:end], m[1]-start)
		out = append(out, Link{
			URL:            normalized,
			Password:       pw,
			PasswordSource: source,
			Context:        util.OneLine(string(v.orig[start:end])),
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

// passwordInSegment 在 [orig, norm] 两个等长视图里找密码。
// urlEnd 是 URL 结束位置在该片段内的 rune 下标。
func passwordInSegment(orig, norm []rune, urlEnd int) (string, string) {
	if urlEnd < 0 {
		urlEnd = 0
	}
	if urlEnd > len(orig) {
		urlEnd = len(orig)
	}
	afterO, afterN := orig[urlEnd:], norm[urlEnd:]
	beforeO, beforeN := orig[:urlEnd], norm[:urlEnd]
	if len(beforeO) > 240 {
		beforeO = beforeO[len(beforeO)-240:]
		beforeN = beforeN[len(beforeN)-240:]
	}

	// 1) 优先看 URL 之后（同一段内）的密码，避免把上一个链接的密码误用。
	if pw, _ := findKeywordPassword(afterO, afterN); pw != "" {
		return pw, "keyword"
	}
	// 2) URL 紧跟括号内容。
	if pw := matchPassword(afterO, afterN, reBracket, 1); pw != "" {
		return pw, "bracket"
	}
	// 3) URL 后面同一行的数字/字母。
	if nl := indexRune(afterN, '\n'); nl >= 0 {
		lineO, lineN := afterO[:nl], afterN[:nl]
		if pw := matchPassword(lineO, lineN, reAfterURLDigits, 1); pw != "" {
			return pw, "after-url"
		}
	} else if pw := matchPassword(afterO, afterN, reAfterURLDigits, 1); pw != "" {
		return pw, "after-url"
	}
	// 4) 再看看 URL 前面的最后一段文字。
	if pw, _ := findKeywordPassword(beforeO, beforeN); pw != "" {
		return pw, "keyword-before"
	}
	// 5) 前置文本里找「4 位数字」这种最常见的手写密码。
	tailO, tailN := beforeO, beforeN
	if len(tailO) > 120 {
		tailO = tailO[len(tailO)-120:]
		tailN = tailN[len(tailN)-120:]
	}
	if pw := matchPassword(tailO, tailN, reBareDigits, 1); pw != "" {
		return pw, "digits"
	}
	return "", ""
}

// matchPassword 在归一化文本上匹配，再按同样的 rune 下标从原文里取出密码，
// 从而保留全角写法（如「２５２５」）。
func matchPassword(orig, norm []rune, re *regexp.Regexp, group int) string {
	if len(norm) == 0 {
		return ""
	}
	s := string(norm)
	loc := re.FindStringSubmatchIndex(s)
	if loc == nil {
		return ""
	}
	gi := group * 2
	if gi+1 >= len(loc) || loc[gi] < 0 || loc[gi+1] < 0 {
		return ""
	}
	gs := utf8.RuneCountInString(s[:loc[gi]])
	ge := utf8.RuneCountInString(s[:loc[gi+1]])
	if gs < 0 || ge > len(orig) || gs >= ge {
		return ""
	}
	rawPW := string(orig[gs:ge])
	normPW := string(norm[gs:ge])
	return cleanPassword(rawPW, normPW)
}

// findKeywordPassword 在所有「关键词 + 密码」正则里找出离片段开头最近的一个密码。
// 返回 (密码, 密码在原片段中的起始 rune 下标)。
func findKeywordPassword(orig, norm []rune) (string, int) {
	if len(norm) == 0 {
		return "", -1
	}
	s := string(norm)
	bestPos := -1
	bestPW := ""
	for _, re := range keywordPatterns {
		for _, loc := range re.FindAllStringSubmatchIndex(s, -1) {
			if len(loc) < 4 || loc[2] < 0 || loc[3] < 0 {
				continue
			}
			gs := utf8.RuneCountInString(s[:loc[2]])
			ge := utf8.RuneCountInString(s[:loc[3]])
			if ge > len(orig) || gs >= ge {
				continue
			}
			pw := cleanPassword(string(orig[gs:ge]), string(norm[gs:ge]))
			if pw == "" {
				continue
			}
			// 同一个密码可能被两个正则同时命中，保留位置更靠前的那个。
			if bestPos < 0 || gs < bestPos {
				bestPos = gs
				bestPW = pw
			}
			break // 该正则只取第一个有效匹配
		}
	}
	if bestPos < 0 {
		return "", -1
	}
	return bestPW, bestPos
}

// cleanPassword 清理候选密码；raw 保留原始宽度，norm 仅用于关键词排除等判断。
func cleanPassword(raw, norm string) string {
	raw = strings.TrimSpace(raw)
	raw = strings.Trim(raw, `"'“”‘’`)
	raw = strings.TrimRight(raw, reTrailing)
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ""
	}
	lower := strings.ToLower(strings.TrimSpace(norm))
	switch lower {
	case "pass", "password", "url", "getuploader", "download", "http", "https", "zip", "rar", "7z":
		return ""
	}
	// 纯字母且长度 <= 3 的，多半是误提取（如「pass」的残留）。
	if utf8.RuneCountInString(raw) <= 2 {
		return ""
	}
	return raw
}

// ToHalfWidth 把全角英文/数字/常见符号转成半角。
func ToHalfWidth(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	for _, r := range s {
		b.WriteRune(toHalfWidthRune(r))
	}
	return b.String()
}

func toHalfWidthRune(r rune) rune {
	switch {
	case r == '　': // 全角空格
		return ' '
	case r >= '０' && r <= '９':
		return '0' + (r - '０')
	case r >= 'Ａ' && r <= 'Ｚ':
		return 'A' + (r - 'Ａ')
	case r >= 'ａ' && r <= 'ｚ':
		return 'a' + (r - 'ａ')
	case r >= '！' && r <= '～': // 全角 ASCII 符号区（U+FF01–U+FF5E）
		return r - 0xFEE0
	default:
		return r
	}
}

// runeIndexMap 返回 字节下标 -> rune 下标 的映射表（长度 len(s)+1）。
func runeIndexMap(s string) []int {
	idx := make([]int, len(s)+1)
	ri := 0
	for bi := range s {
		idx[bi] = ri
		ri++
	}
	idx[len(s)] = ri
	return idx
}

func indexRune(rs []rune, target rune) int {
	for i, r := range rs {
		if r == target {
			return i
		}
	}
	return -1
}
