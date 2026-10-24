package getuploader

import (
	"strings"
	"testing"

	"nico-seiga-archiver/internal/htmlx"
)

func TestParseCatalogURL(t *testing.T) {
	cases := []struct {
		name    string
		in      string
		wantAcc string
		wantURL string
		wantKey string
	}{
		{
			name:    "带斜杠的主页",
			in:      "https://ux.getuploader.com/SHINOBILI7/",
			wantAcc: "SHINOBILI7",
			wantURL: "https://ux.getuploader.com/SHINOBILI7/",
			wantKey: "catalog:shinobili7",
		},
		{
			name:    "不带斜杠",
			in:      "https://ux.getuploader.com/SHINOBILI7",
			wantAcc: "SHINOBILI7",
			wantURL: "https://ux.getuploader.com/SHINOBILI7/",
			wantKey: "catalog:shinobili7",
		},
		{
			name:    "省略 scheme",
			in:      "ux.getuploader.com/SHINOBILI7/",
			wantAcc: "SHINOBILI7",
			wantURL: "https://ux.getuploader.com/SHINOBILI7/",
			wantKey: "catalog:shinobili7",
		},
		{
			name:    "列表中页",
			in:      "https://uu.getuploader.com/harinashi/index/date/desc/2",
			wantAcc: "harinashi",
			wantURL: "https://uu.getuploader.com/harinashi/",
			wantKey: "catalog:harinashi",
		},
		{
			name:    "缩略图页按账号根地址处理",
			in:      "https://ux.getuploader.com/SHINOBILI7/thumbnail",
			wantAcc: "SHINOBILI7",
			wantURL: "https://ux.getuploader.com/SHINOBILI7/",
			wantKey: "catalog:shinobili7",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ref, err := ParseCatalogURL(tc.in)
			if err != nil {
				t.Fatalf("ParseCatalogURL(%q): %v", tc.in, err)
			}
			if ref.Account != tc.wantAcc {
				t.Errorf("Account = %q, want %q", ref.Account, tc.wantAcc)
			}
			if ref.Root != tc.wantURL {
				t.Errorf("Root = %q, want %q", ref.Root, tc.wantURL)
			}
			if ref.Key() != tc.wantKey {
				t.Errorf("Key = %q, want %q", ref.Key(), tc.wantKey)
			}
		})
	}
}

func TestParseCatalogURLSort(t *testing.T) {
	ref, err := ParseCatalogURL("https://uu.getuploader.com/harinashi/index/filesize/asc/3")
	if err != nil {
		t.Fatal(err)
	}
	if ref.Sort != "filesize" || ref.Order != "asc" {
		t.Fatalf("sort/order = %q/%q", ref.Sort, ref.Order)
	}
	// 把排序带到后续页，避免翻页时顺序突变。
	if got := ref.PageURL(4); got != "https://uu.getuploader.com/harinashi/index/filesize/asc/4" {
		t.Errorf("PageURL(4) = %q", got)
	}
}

func TestParseCatalogURLErrors(t *testing.T) {
	cases := []string{
		"",
		"https://example.com/foo/",
		"https://uu.getuploader.com/harinashi/download/71",
		"https://uu.getuploader.com/",
		"https://uu.getuploader.com/thumbnail",
	}
	for _, in := range cases {
		if _, err := ParseCatalogURL(in); err == nil {
			t.Errorf("ParseCatalogURL(%q) 应当报错", in)
		}
	}
}

func TestCatalogPageURL(t *testing.T) {
	ref, err := ParseCatalogURL("https://ux.getuploader.com/SHINOBILI7/")
	if err != nil {
		t.Fatal(err)
	}
	if got := ref.PageURL(1); got != "https://ux.getuploader.com/SHINOBILI7/" {
		t.Errorf("PageURL(1) = %q", got)
	}
	if got := ref.PageURL(3); got != "https://ux.getuploader.com/SHINOBILI7/index/date/desc/3" {
		t.Errorf("PageURL(3) = %q", got)
	}
	if got := ref.PageURL(0); got != ref.Root {
		t.Errorf("PageURL(0) = %q", got)
	}
}

const catalogFixture = `<html><body>
<div class="page-header">
  <h1>色々7 <small>uploader.jp</small></h1>
</div>
<h2 class="lead"> 31 件中 16 - 30 件目を表示しています。</h2>
<table class="table">
  <thead><tr><th>ファイル</th><th>コメント</th><th>オリジナル</th><th>容量</th><th>日時</th><th>DL</th><th></th></tr></thead>
  <tbody>
    <tr><td><a href="https://uu.getuploader.com/SHINOBILI7/download/37" title="中部つるぎEX立ち絵1.02.zip">中部つるぎEX立ち絵1.02.zip</a></td>
        <td><span class="glyphicon glyphicon-lock" aria-hidden="true"></span> 目差分一部修正</td>
        <td>中部つるぎEX立ち絵1.02.zip</td><td>71.4 MB</td><td>26/09/04 22:18</td><td>13</td>
        <td><a class="button" href="https://uu.getuploader.com/SHINOBILI7/edit/37" rel="nofollow">編集</a></td></tr>
    <tr class="alt"><td><a href="https://uu.getuploader.com/SHINOBILI7/download/36" title="関西しのびEX立ち絵.zip">関西しのびEX立ち絵.zip</a></td>
        <td><span class="glyphicon glyphicon-lock" aria-hidden="true"></span> </td>
        <td>関西しのびEX立ち絵.zip</td><td>89.6 MB</td><td>25/08/23 14:01</td><td>94</td>
        <td><a class="button" href="https://uu.getuploader.com/SHINOBILI7/edit/36" rel="nofollow">編集</a></td></tr>
  </tbody>
</table>
<ul class="pagination pagination-sm">
  <li class="previous"><a href="https://uu.getuploader.com/SHINOBILI7/index/date/desc/1">前へ</a></li>
  <li class="page"><a href="https://uu.getuploader.com/SHINOBILI7/index/date/desc/1">1</a></li>
  <li class="active"><a href="https://uu.getuploader.com/SHINOBILI7/index/date/desc/2">2</a></li>
  <li class="page"><a href="https://uu.getuploader.com/SHINOBILI7/index/date/desc/3">3</a></li>
  <li class="next"><a href="https://uu.getuploader.com/SHINOBILI7/index/date/desc/3">次へ</a></li>
</ul>
</body></html>`

func TestParseCatalogPage(t *testing.T) {
	doc, err := htmlx.Parse(strings.NewReader(catalogFixture))
	if err != nil {
		t.Fatal(err)
	}
	pageURL := "https://ux.getuploader.com/SHINOBILI7/index/date/desc/2"
	page := parseCatalogPage(doc, pageURL)

	if page.Name != "色々7" {
		t.Errorf("Name = %q, want 色々7", page.Name)
	}
	if page.Account != "SHINOBILI7" {
		t.Errorf("Account = %q", page.Account)
	}
	if page.Page != 2 {
		t.Errorf("Page = %d, want 2", page.Page)
	}
	if page.MaxPage != 3 {
		t.Errorf("MaxPage = %d, want 3", page.MaxPage)
	}
	if !strings.Contains(page.Summary, "31 件中") {
		t.Errorf("Summary = %q", page.Summary)
	}
	if page.NextURL != "https://uu.getuploader.com/SHINOBILI7/index/date/desc/3" {
		t.Errorf("NextURL = %q", page.NextURL)
	}
	if page.PrevURL != "https://uu.getuploader.com/SHINOBILI7/index/date/desc/1" {
		t.Errorf("PrevURL = %q", page.PrevURL)
	}

	// 只应有 2 个文件（/edit/ 链接不能算进来）。
	if len(page.Items) != 2 {
		t.Fatalf("Items = %d, want 2: %+v", len(page.Items), page.Items)
	}
	first := page.Items[0]
	if first.ID != "37" || first.URL != "https://uu.getuploader.com/SHINOBILI7/download/37" {
		t.Errorf("first = %+v", first)
	}
	if first.Title != "中部つるぎEX立ち絵1.02.zip" {
		t.Errorf("Title = %q", first.Title)
	}
	if first.SizeText != "71.4 MB" {
		t.Errorf("SizeText = %q", first.SizeText)
	}
	if first.Date != "26/09/04 22:18" {
		t.Errorf("Date = %q", first.Date)
	}
	if first.Comment != "目差分一部修正" {
		t.Errorf("Comment = %q", first.Comment)
	}
	if !first.Locked {
		t.Error("Locked 应为 true")
	}
}

func TestParseCatalogPageNoPager(t *testing.T) {
	doc, err := htmlx.Parse(strings.NewReader(`<html><body>
	<h1>アップローダー <small>uploader.jp</small></h1>
	<table><tbody>
	<tr><td><a href="/harinashi/download/71">水奈瀬コウ立ち絵素材.zip</a></td><td></td>
	<td>水奈瀬コウ立ち絵素材.zip</td><td>11.1 MB</td><td>25/10/28 13:40</td><td>83</td></tr>
	</tbody></table>
	</body></html>`))
	if err != nil {
		t.Fatal(err)
	}
	page := parseCatalogPage(doc, "https://uu.getuploader.com/harinashi/")
	if len(page.Items) != 1 {
		t.Fatalf("Items = %+v", page.Items)
	}
	// 相对地址要能解析成绝对地址。
	if page.Items[0].URL != "https://uu.getuploader.com/harinashi/download/71" {
		t.Errorf("URL = %q", page.Items[0].URL)
	}
	if page.NextURL != "" {
		t.Errorf("NextURL = %q", page.NextURL)
	}
	if page.Name != "アップローダー" {
		t.Errorf("Name = %q", page.Name)
	}
}

func TestCatalogDestName(t *testing.T) {
	// 优先用账号：默认显示名常常是「アップローダー」，会把不同目录混到一个文件夹里。
	p := &CatalogPage{Account: "harinashi", Name: "アップローダー"}
	if got := p.CatalogDestName(); got != "harinashi" {
		t.Errorf("CatalogDestName = %q, want harinashi", got)
	}
	p2 := &CatalogPage{Name: "色々7"}
	if got := p2.CatalogDestName(); got != "色々7" {
		t.Errorf("CatalogDestName = %q, want 色々7", got)
	}
	p3 := &CatalogPage{}
	if got := p3.CatalogDestName(); got != "unknown" {
		t.Errorf("CatalogDestName = %q, want unknown", got)
	}
}

func TestCatalogHasNext(t *testing.T) {
	cases := []struct {
		name string
		page CatalogPage
		want bool
	}{
		{
			name: "没有下一页链接",
			page: CatalogPage{Page: 4, MaxPage: 4},
			want: false,
		},
		{
			name: "有下一页且未到最大页",
			page: CatalogPage{Page: 1, MaxPage: 3, NextURL: "https://uu.getuploader.com/A/index/date/desc/2"},
			want: true,
		},
		{
			name: "到了最大页且有链接（链接指向更后面，说明窗口被截断）",
			page: CatalogPage{Page: 5, MaxPage: 5, NextURL: "https://uu.getuploader.com/A/index/date/desc/6"},
			want: true,
		},
		{
			name: "最大页且链接不前进",
			page: CatalogPage{Page: 3, MaxPage: 3, NextURL: "https://uu.getuploader.com/A/index/date/desc/3"},
			want: false,
		},
		{
			name: "未知最大页时按链接走",
			page: CatalogPage{Page: 2, MaxPage: 0, NextURL: "https://uu.getuploader.com/A/index/date/desc/3"},
			want: true,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.page.HasNext(); got != tc.want {
				t.Errorf("HasNext() = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestPageNumberOf(t *testing.T) {
	cases := map[string]int{
		"https://uu.getuploader.com/A/index/date/desc/7":  7,
		"https://uu.getuploader.com/A/index/date/desc/7/": 7,
		"https://uu.getuploader.com/A/?page=4":            4,
		"https://uu.getuploader.com/A/":                   0,
		"":                                                0,
	}
	for in, want := range cases {
		if got := pageNumberOf(in); got != want {
			t.Errorf("pageNumberOf(%q) = %d, want %d", in, got, want)
		}
	}
}
