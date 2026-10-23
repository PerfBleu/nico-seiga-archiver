// Package htmlx 是对 x/net/html 的一层薄封装，提供简单的查找/取文本工具。
package htmlx

import (
	"io"
	"strings"

	"golang.org/x/net/html"
)

// Parse 解析 HTML。
func Parse(r io.Reader) (*html.Node, error) {
	return html.Parse(r)
}

// Walk 先序遍历 DOM。fn 返回 false 时不再深入该节点的子树。
func Walk(n *html.Node, fn func(*html.Node) bool) {
	if n == nil {
		return
	}
	if !fn(n) {
		return
	}
	for c := n.FirstChild; c != nil; c = c.NextSibling {
		Walk(c, fn)
	}
}

// FindAll 返回所有匹配的节点（先序）。
func FindAll(root *html.Node, match func(*html.Node) bool) []*html.Node {
	var out []*html.Node
	Walk(root, func(n *html.Node) bool {
		if match(n) {
			out = append(out, n)
		}
		return true
	})
	return out
}

// Find 返回第一个匹配的节点。
func Find(root *html.Node, match func(*html.Node) bool) *html.Node {
	var found *html.Node
	Walk(root, func(n *html.Node) bool {
		if found != nil {
			return false
		}
		if match(n) {
			found = n
			return false
		}
		return true
	})
	return found
}

// Attr 取属性值。
func Attr(n *html.Node, key string) string {
	if n == nil {
		return ""
	}
	for _, a := range n.Attr {
		if strings.EqualFold(a.Key, key) {
			return a.Val
		}
	}
	return ""
}

// HasClass 判断节点是否含有某个 class。
func HasClass(n *html.Node, class string) bool {
	if n == nil {
		return false
	}
	for _, c := range strings.Fields(Attr(n, "class")) {
		if c == class {
			return true
		}
	}
	return false
}

// HasClassContains 判断节点 class 中是否包含某段字符串。
func HasClassContains(n *html.Node, sub string) bool {
	if n == nil {
		return false
	}
	return strings.Contains(Attr(n, "class"), sub)
}

// IsTag 判断节点标签名。
func IsTag(n *html.Node, tag string) bool {
	return n != nil && n.Type == html.ElementNode && strings.EqualFold(n.Data, tag)
}

// ByTag 按标签名查找。
func ByTag(root *html.Node, tag string) []*html.Node {
	return FindAll(root, func(n *html.Node) bool { return IsTag(n, tag) })
}

// ByClass 按 class 查找。
func ByClass(root *html.Node, class string) []*html.Node {
	return FindAll(root, func(n *html.Node) bool { return HasClass(n, class) })
}

// FirstByClass 返回第一个指定 class 的节点。
func FirstByClass(root *html.Node, class string) *html.Node {
	return Find(root, func(n *html.Node) bool { return HasClass(n, class) })
}

// ByID 按 id 查找。
func ByID(root *html.Node, id string) *html.Node {
	return Find(root, func(n *html.Node) bool { return Attr(n, "id") == id })
}

// FirstTag 返回第一个指定标签。
func FirstTag(root *html.Node, tag string) *html.Node {
	return Find(root, func(n *html.Node) bool { return IsTag(n, tag) })
}

// MetaContent 取 meta 标签的 content。
func MetaContent(root *html.Node, key, value string) string {
	n := Find(root, func(n *html.Node) bool {
		return IsTag(n, "meta") && strings.EqualFold(Attr(n, key), value)
	})
	return Attr(n, "content")
}

// Text 返回节点的可见文本，块级元素/换行会被转换成换行符。
func Text(n *html.Node) string {
	if n == nil {
		return ""
	}
	var b strings.Builder
	writeText(n, &b)
	return normalize(b.String())
}

func writeText(n *html.Node, b *strings.Builder) {
	switch n.Type {
	case html.TextNode:
		b.WriteString(n.Data)
		return
	case html.ElementNode:
		switch strings.ToLower(n.Data) {
		case "script", "style", "noscript", "svg", "head":
			return
		case "br":
			b.WriteString("\n")
			return
		case "p", "div", "li", "tr", "h1", "h2", "h3", "h4", "h5", "h6", "section", "article", "blockquote", "dd", "dt":
			b.WriteString("\n")
			for c := n.FirstChild; c != nil; c = c.NextSibling {
				writeText(c, b)
			}
			b.WriteString("\n")
			return
		}
	case html.DocumentNode:
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			writeText(c, b)
		}
		return
	}
	for c := n.FirstChild; c != nil; c = c.NextSibling {
		writeText(c, b)
	}
}

// RawText 返回节点的原始文本（不做换行处理）。
func RawText(n *html.Node) string {
	if n == nil {
		return ""
	}
	var b strings.Builder
	var rec func(*html.Node)
	rec = func(n *html.Node) {
		switch n.Type {
		case html.TextNode:
			b.WriteString(n.Data)
		case html.ElementNode:
			if strings.EqualFold(n.Data, "script") || strings.EqualFold(n.Data, "style") {
				return
			}
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			rec(c)
		}
	}
	rec(n)
	return strings.TrimSpace(b.String())
}

func normalize(s string) string {
	s = strings.ReplaceAll(s, "\r\n", "\n")
	s = strings.ReplaceAll(s, "\r", "\n")
	lines := strings.Split(s, "\n")
	out := make([]string, 0, len(lines))
	blank := 0
	for _, line := range lines {
		line = strings.TrimSpace(line)
		// 中日文之间的空格通常是排版用的，这里只压缩连续空白。
		line = strings.Join(strings.Fields(line), " ")
		if line == "" {
			blank++
			if blank > 1 {
				continue
			}
			out = append(out, "")
			continue
		}
		blank = 0
		out = append(out, line)
	}
	return strings.TrimSpace(strings.Join(out, "\n"))
}

// HasText 判断节点文本中是否包含某个片段。
func HasText(n *html.Node, sub string) bool {
	return strings.Contains(Text(n), sub)
}

// Unescape 简单地反转 HTML 实体（仅用于少量场景）。
func Unescape(s string) string {
	r := strings.NewReplacer(
		"&amp;", "&",
		"&lt;", "<",
		"&gt;", ">",
		"&quot;", `"`,
		"&#39;", "'",
	)
	return r.Replace(s)
}
