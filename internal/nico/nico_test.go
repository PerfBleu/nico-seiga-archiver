package nico

import (
	"strings"
	"testing"

	"nico-seiga-archiver/internal/htmlx"
)

const authorListFixture = `<!DOCTYPE html>
<html><body>
<div id="main">
  <div class="controll">
    <ul class="pager">
      <li class="prev disabled">&lt;</li>
      <li class="current_index">1</li>
      <li> <a href="/user/illust/39911624?page=2"> 2 </a> </li>
      <li class="next"> <a href="/user/illust/39911624?page=2" rel="next"> &gt; </a> </li>
    </ul>
    <p class="page_count"><strong>1〜40</strong>件目を表示</p>
  </div>
  <div class="illust_list">
    <ul class="item_list autopagerize_page_element">
      <li class="list_item no_trim"><a href="/seiga/im11691748">
        <span class="thum"><img src="https://lohas.nicoseiga.jp/thumb/11691748qz" alt="水奈瀬コウ立ち絵配布"></span>
        <ul class="illust_info"><li class="title">水奈瀬コウ立ち絵配布</li></ul>
      </a></li>
      <li class="list_item no_trim"><a href="/seiga/im11230837">
        <span class="thum"><img src="https://lohas.nicoseiga.jp/thumb/11230837qz" alt="黒聡鵜月立ち絵素材"></span>
        <ul class="illust_info"><li class="title">黒聡鵜月立ち絵素材</li></ul>
      </a></li>
      <li class="list_item no_trim"><a href="/seiga/im11691748">
        <span class="thum"><img src="dup" alt="dup"></span>
        <ul class="illust_info"><li class="title">dup</li></ul>
      </a></li>
    </ul>
  </div>
</div>
</body></html>`

func TestParseListPage(t *testing.T) {
	doc, err := htmlx.Parse(strings.NewReader(authorListFixture))
	if err != nil {
		t.Fatal(err)
	}
	page := parseListPage(doc, "https://seiga.nicovideo.jp/user/illust/39911624?page=1")
	if len(page.Items) != 2 {
		t.Fatalf("items = %d, want 2: %+v", len(page.Items), page.Items)
	}
	if page.Items[0].ID != "11691748" || page.Items[0].Title != "水奈瀬コウ立ち絵配布" {
		t.Errorf("unexpected first item: %+v", page.Items[0])
	}
	if page.Items[0].URL != "https://seiga.nicovideo.jp/seiga/im11691748" {
		t.Errorf("unexpected url: %s", page.Items[0].URL)
	}
	if page.NextURL != "https://seiga.nicovideo.jp/user/illust/39911624?page=2" {
		t.Errorf("next = %q", page.NextURL)
	}
}

func TestNormalizeIllustURL(t *testing.T) {
	cases := map[string]string{
		"https://seiga.nicovideo.jp/seiga/im11691748":       "11691748",
		"https://seiga.nicovideo.jp/seiga/im11691748?foo=1": "11691748",
		"im11691748": "11691748",
		"11691748":   "11691748",
	}
	for in, wantID := range cases {
		page, id, err := NormalizeIllustURL(in)
		if err != nil {
			t.Fatalf("NormalizeIllustURL(%q): %v", in, err)
		}
		if id != wantID {
			t.Errorf("id = %q, want %q", id, wantID)
		}
		if page != "https://seiga.nicovideo.jp/seiga/im"+wantID {
			t.Errorf("page = %q", page)
		}
	}
	if _, _, err := NormalizeIllustURL("https://example.com/foo"); err == nil {
		t.Error("expected error for invalid url")
	}
}

func TestWithPageAndPageOf(t *testing.T) {
	u, err := WithPage("https://seiga.nicovideo.jp/search/ボイロ?target=illust", 3)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(u, "page=3") || !strings.Contains(u, "target=illust") {
		t.Errorf("unexpected url: %s", u)
	}
	if got := PageOf(u); got != 3 {
		t.Errorf("PageOf = %d, want 3", got)
	}
	if got := PageOf("https://seiga.nicovideo.jp/illust/"); got != 1 {
		t.Errorf("PageOf default = %d, want 1", got)
	}
}

func TestSearchURLFromKeyword(t *testing.T) {
	u := SearchURLFromKeyword("ボイロ")
	if !strings.HasPrefix(u, "https://seiga.nicovideo.jp/search/") {
		t.Errorf("unexpected url: %s", u)
	}
	if !strings.Contains(u, "target=illust") {
		t.Errorf("missing target: %s", u)
	}
}

func TestNormalizeDescription(t *testing.T) {
	in := `<p>立ち絵はこちら → <a href="https://uu.getuploader.com/a/download/1">規約</a></p><br>パスワード：1234`
	out := normalizeDescription(in)
	if !strings.Contains(out, "https://uu.getuploader.com/a/download/1") {
		t.Errorf("anchor url lost: %q", out)
	}
	if strings.Contains(out, "<") {
		t.Errorf("html tags not stripped: %q", out)
	}
	if !strings.Contains(out, "パスワード：1234") {
		t.Errorf("text lost: %q", out)
	}
}
