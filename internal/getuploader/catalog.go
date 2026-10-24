package getuploader

import (
	"context"
	"fmt"
	"net/url"
	"regexp"
	"strconv"
	"strings"

	"golang.org/x/net/html"

	"nico-seiga-archiver/internal/htmlx"
	"nico-seiga-archiver/internal/httpclient"
	"nico-seiga-archiver/internal/util"
)

// CatalogRef 描述一个上传者目录（getuploader 主页）。
type CatalogRef struct {
	// Root 是规范化的目录根地址，例如 https://ux.getuploader.com/SHINOBILI7/
	Root string
	// Account 是路径里的上传者账号（SHINOBILI7）。
	Account string
	// Sort/Order 是列表排序方式，默认 date/desc。
	Sort  string
	Order string
}

// CatalogItem 是目录页里的一个文件。
type CatalogItem struct {
	// URL 是下载页地址（不是最终文件直链）。
	URL string
	// ID 是 /download/<id> 里的编号。
	ID       string
	Title    string
	Comment  string
	Original string
	SizeText string
	Date     string
	// Locked 表示列表里带了锁图标（一般意味着需要下载密码）。
	Locked bool
}

// CatalogPage 是一个目录页的解析结果。
type CatalogPage struct {
	URL string
	// Account 是上传者账号；Name 是上传者显示名（如「色々7」）。
	Account string
	Name    string
	// Summary 是页面上的说明文字（第 1 页通常是目录简介，第 2 页起是件数）。
	Summary string
	// Page 是当前页码（解析不出来时为 0）。
	Page int
	// MaxPage 是分页器里出现的最大页码（解析不出来时为 0）。
	MaxPage int
	Items   []CatalogItem
	NextURL string
	PrevURL string
}

// Key 返回用于记录进度的键，例如 catalog:SHINOBILI7。
//
// 只用账号，不用主机名：同一个账号在 ux/uu 等别名主机上应当共用进度。
func (r CatalogRef) Key() string {
	if r.Account == "" {
		return "catalog:" + strings.ToLower(r.Root)
	}
	return "catalog:" + strings.ToLower(r.Account)
}

// PageURL 返回目录第 page 页的地址（page <= 1 时就是目录根地址）。
func (r CatalogRef) PageURL(page int) string {
	if page <= 1 {
		return r.Root
	}
	sortKey, order := r.Sort, r.Order
	if sortKey == "" {
		sortKey = "date"
	}
	if order == "" {
		order = "desc"
	}
	return r.Root + "index/" + sortKey + "/" + order + "/" + strconv.Itoa(page)
}

// Label 返回便于日志阅读的描述。
func (r CatalogRef) Label() string {
	if r.Account == "" {
		return r.Root
	}
	return "目录 " + r.Account
}

var (
	// /<account>/download/<id>
	reCatalogDownloadPath = regexp.MustCompile(`(?i)^/([^/]+)/download/(\d+)`)
	// /<account>/index/<sort>/<order>/<page>
	reCatalogIndexPath = regexp.MustCompile(`(?i)^/([^/]+)/index/([^/]+)/([^/]+)/(\d+)`)
)

// ParseCatalogURL 解析并规范化上传者目录地址。
//
// 接受的形式：
//
//	https://ux.getuploader.com/SHINOBILI7/
//	https://ux.getuploader.com/SHINOBILI7
//	ux.getuploader.com/SHINOBILI7
//	https://uu.getuploader.com/SHINOBILI7/index/filesize/asc/2
func ParseCatalogURL(input string) (CatalogRef, error) {
	var ref CatalogRef
	raw := strings.TrimSpace(input)
	if raw == "" {
		return ref, fmt.Errorf("目录地址不能为空")
	}
	if !strings.Contains(raw, "://") {
		raw = "https://" + raw
	}
	u, err := url.Parse(raw)
	if err != nil {
		return ref, fmt.Errorf("目录地址无效: %w", err)
	}
	host := strings.ToLower(u.Hostname())
	if host != "getuploader.com" && !strings.HasSuffix(host, ".getuploader.com") {
		return ref, fmt.Errorf("不是 getuploader 目录地址: %s", input)
	}
	path := u.EscapedPath()
	if m := reCatalogDownloadPath.FindStringSubmatch(path); m != nil {
		return ref, fmt.Errorf("%s 是下载页而不是目录页，请改用 down 命令", input)
	}

	segs := splitPath(u.Path)
	if len(segs) == 0 {
		return ref, fmt.Errorf("目录地址缺少上传者账号: %s（应形如 https://ux.getuploader.com/账号/）", input)
	}
	account := segs[0]
	switch strings.ToLower(account) {
	case "thumbnail", "search", "ranking", "rss", "inquire", "upload", "signup", "users":
		return ref, fmt.Errorf("不支持该页面: %s（请使用上传者主页，如 https://ux.getuploader.com/账号/）", input)
	}

	ref.Account = account
	ref.Root = fmt.Sprintf("%s://%s/%s/", u.Scheme, u.Host, account)
	ref.Sort, ref.Order = "date", "desc"
	if m := reCatalogIndexPath.FindStringSubmatch(path); m != nil {
		if sortKey := strings.TrimSpace(m[2]); sortKey != "" {
			ref.Sort = sortKey
		}
		if order := strings.TrimSpace(m[3]); order != "" {
			ref.Order = order
		}
	}
	// 只保留「一覧」类页面：路径里出现 thumbnail 等仍按账号根地址处理。
	return ref, nil
}

func splitPath(p string) []string {
	parts := strings.Split(strings.Trim(p, "/"), "/")
	out := make([]string, 0, len(parts))
	for _, s := range parts {
		if s != "" {
			out = append(out, s)
		}
	}
	return out
}

// FetchCatalogPage 抓取并解析一个目录页。
func (c *Client) FetchCatalogPage(ctx context.Context, rawURL string) (*CatalogPage, error) {
	doc, err := c.loadPage(ctx, rawURL, rawURL)
	if err != nil {
		return nil, err
	}
	return parseCatalogPage(doc, rawURL), nil
}

// parseCatalogPage 从 DOM 中解析目录页。
func parseCatalogPage(doc *html.Node, pageURL string) *CatalogPage {
	page := &CatalogPage{URL: pageURL}
	if u, err := url.Parse(pageURL); err == nil {
		if segs := splitPath(u.Path); len(segs) > 0 {
			page.Account = segs[0]
		}
		if m := reCatalogIndexPath.FindStringSubmatch(u.EscapedPath()); m != nil {
			if n, err := strconv.Atoi(m[4]); err == nil {
				page.Page = n
			}
		}
	}
	if page.Page == 0 {
		page.Page = 1
	}
	page.Name = catalogName(doc)
	page.Summary = catalogSummary(doc)
	page.Items = catalogItems(doc, pageURL)

	// 分页：li.next / li.previous 里的链接最可靠。
	if next := pagerLink(doc, "next"); next != "" {
		if abs, err := httpclient.ResolveURL(pageURL, next); err == nil {
			page.NextURL = abs
		}
	}
	if prev := pagerLink(doc, "previous"); prev != "" {
		if abs, err := httpclient.ResolveURL(pageURL, prev); err == nil {
			page.PrevURL = abs
		}
	}
	page.MaxPage = pagerMaxPage(doc)
	return page
}

// catalogName 取上传者显示名（<h1>名字 <small>uploader.jp</small></h1>）。
func catalogName(doc *html.Node) string {
	h1 := htmlx.FirstTag(doc, "h1")
	if h1 == nil {
		return ""
	}
	var b strings.Builder
	for c := h1.FirstChild; c != nil; c = c.NextSibling {
		if c.Type == html.TextNode {
			b.WriteString(c.Data)
		}
	}
	name := strings.TrimSpace(b.String())
	if name == "" {
		// 兜底：整段文本里去掉站点名。
		name = strings.TrimSpace(htmlx.RawText(h1))
		name = strings.TrimSpace(strings.TrimSuffix(name, "uploader.jp"))
	}
	return name
}

// catalogSummary 取页面说明：优先 <h2 class="lead">。
func catalogSummary(doc *html.Node) string {
	if lead := htmlx.FirstByClass(doc, "lead"); lead != nil {
		if t := strings.TrimSpace(htmlx.RawText(lead)); t != "" {
			return t
		}
	}
	if h2 := htmlx.FirstTag(doc, "h2"); h2 != nil {
		return strings.TrimSpace(htmlx.RawText(h2))
	}
	return ""
}

// catalogItems 解析目录列表里的所有文件行。
func catalogItems(doc *html.Node, pageURL string) []CatalogItem {
	var items []CatalogItem
	seen := map[string]bool{}
	for _, tr := range htmlx.ByTag(doc, "tr") {
		// 每行第一个 /download/<id> 链接就是文件入口。
		link := htmlx.Find(tr, func(n *html.Node) bool {
			if !htmlx.IsTag(n, "a") {
				return false
			}
			return reCatalogDownloadPath.MatchString(escapedPath(htmlx.Attr(n, "href"), pageURL))
		})
		if link == nil {
			continue
		}
		abs, err := httpclient.ResolveURL(pageURL, htmlx.Attr(link, "href"))
		if err != nil {
			continue
		}
		id := ""
		if m := reCatalogDownloadPath.FindStringSubmatch(escapedPath(abs, pageURL)); m != nil {
			id = m[2]
		}
		key := abs
		if seen[key] {
			continue
		}
		seen[key] = true

		item := CatalogItem{URL: abs, ID: id}
		item.Title = strings.TrimSpace(htmlx.RawText(link))
		if item.Title == "" {
			item.Title = strings.TrimSpace(htmlx.Attr(link, "title"))
		}

		tds := htmlx.ByTag(tr, "td")
		if len(tds) >= 2 {
			item.Comment = cleanCatalogComment(htmlx.RawText(tds[1]))
		}
		if len(tds) >= 5 {
			item.Original = strings.TrimSpace(htmlx.RawText(tds[2]))
			item.SizeText = strings.TrimSpace(htmlx.RawText(tds[3]))
			item.Date = strings.TrimSpace(htmlx.RawText(tds[4]))
		}
		item.Locked = htmlx.Find(tr, func(n *html.Node) bool {
			return htmlx.HasClass(n, "glyphicon-lock")
		}) != nil
		items = append(items, item)
	}
	return items
}

func cleanCatalogComment(s string) string {
	s = strings.ReplaceAll(s, "\u00a0", " ")
	return strings.TrimSpace(strings.Join(strings.Fields(s), " "))
}

// escapedPath 把可能是相对地址的 href 转成用于正则匹配的路径。
func escapedPath(href, base string) string {
	href = strings.TrimSpace(href)
	if href == "" {
		return ""
	}
	if abs, err := httpclient.ResolveURL(base, href); err == nil {
		if u, err := url.Parse(abs); err == nil {
			return u.EscapedPath()
		}
	}
	return href
}

// pagerLink 取分页器里「上一页/下一页」的 href。
func pagerLink(doc *html.Node, class string) string {
	li := htmlx.Find(doc, func(n *html.Node) bool {
		return htmlx.IsTag(n, "li") && htmlx.HasClass(n, class)
	})
	if li == nil {
		return ""
	}
	if a := htmlx.FirstTag(li, "a"); a != nil {
		return htmlx.Attr(a, "href")
	}
	return ""
}

// pagerMaxPage 取分页器里出现的最大页码。
func pagerMaxPage(doc *html.Node) int {
	max := 0
	for _, ul := range htmlx.ByClass(doc, "pagination") {
		for _, a := range htmlx.ByTag(ul, "a") {
			if n, err := strconv.Atoi(strings.TrimSpace(htmlx.RawText(a))); err == nil && n > max {
				max = n
			}
			if m := reCatalogIndexPath.FindStringSubmatch(escapedPath(htmlx.Attr(a, "href"), "")); m != nil {
				if n, err := strconv.Atoi(m[4]); err == nil && n > max {
					max = n
				}
			}
		}
	}
	return max
}

// HasNext 判断是否应当继续抓取下一页。
//
// 优先看分页器的「次へ」链接；但分页器窗口在页数很多时可能被截断（只显示前几页），
// 所以只有当「下一页页码不超过 MaxPage」时才采信 MaxPage，避免提前收工。
func (p *CatalogPage) HasNext() bool {
	if p.NextURL == "" {
		return false
	}
	if p.MaxPage <= 0 {
		return true
	}
	next := pageNumberOf(p.NextURL)
	if next == 0 {
		return true
	}
	if next > p.MaxPage {
		// 分页器窗口被截断，MaxPage 不可信，继续按链接走。
		return true
	}
	return p.Page < p.MaxPage
}

// pageNumberOf 从目录页地址里解析页码：/<账号>/index/<sort>/<order>/<page> 或 ?page=N。
func pageNumberOf(raw string) int {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil {
		return 0
	}
	if m := reCatalogIndexPath.FindStringSubmatch(u.EscapedPath()); m != nil {
		if n, err := strconv.Atoi(m[4]); err == nil {
			return n
		}
	}
	if v := u.Query().Get("page"); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			return n
		}
	}
	return 0
}

// CatalogDestName 返回用于建目录的名字。
// 优先用上传者账号（唯一，避免多个目录都叫「アップローダー」时串到一个文件夹）。
func (p *CatalogPage) CatalogDestName() string {
	if account := util.SanitizeName(strings.TrimSpace(p.Account)); account != "" && account != "unknown" {
		return account
	}
	if name := util.SanitizeName(strings.TrimSpace(p.Name)); name != "" && name != "unknown" {
		return name
	}
	return "unknown"
}
