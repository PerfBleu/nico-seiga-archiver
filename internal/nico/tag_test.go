package nico

import (
	"net/url"
	"testing"
)

func TestNormalizeTagURL(t *testing.T) {
	cases := []struct {
		in      string
		wantTag string
	}{
		{"VOICEROID", "VOICEROID"},
		{"立ち絵", "立ち絵"},
		{"#立ち絵", "立ち絵"},
		{"tag/VOICEROID", "VOICEROID"},
		{"/tag/VOICEROID", "VOICEROID"},
		{"https://seiga.nicovideo.jp/tag/VOICEROID", "VOICEROID"},
		{"https://seiga.nicovideo.jp/tag/VOICEROID?page=3", "VOICEROID"},
		{"https://seiga.nicovideo.jp/tag/%E7%AB%8B%E3%81%A1%E7%B5%B5", "立ち絵"},
		{"seiga.nicovideo.jp/tag/VOICEROID", "VOICEROID"},
	}
	for _, tc := range cases {
		pageURL, tag, err := NormalizeTagURL(tc.in)
		if err != nil {
			t.Fatalf("NormalizeTagURL(%q): %v", tc.in, err)
		}
		if tag != tc.wantTag {
			t.Errorf("NormalizeTagURL(%q) tag = %q, want %q", tc.in, tag, tc.wantTag)
		}
		if pageURL != "https://seiga.nicovideo.jp/tag/"+url.PathEscape(tc.wantTag) {
			t.Errorf("NormalizeTagURL(%q) url = %q", tc.in, pageURL)
		}
	}
}

func TestNormalizeTagURLInvalid(t *testing.T) {
	for _, in := range []string{"", "   ", "https://seiga.nicovideo.jp/illust/ranking"} {
		if _, _, err := NormalizeTagURL(in); err == nil {
			t.Errorf("NormalizeTagURL(%q) 应当报错", in)
		}
	}
}

func TestProgressKeys(t *testing.T) {
	if got := TagProgressKey("VOICEROID"); got != "tag:VOICEROID" {
		t.Errorf("TagProgressKey = %q", got)
	}
	if got := AuthorProgressKey("39911624"); got != "author:39911624" {
		t.Errorf("AuthorProgressKey = %q", got)
	}
	if got := SearchProgressKey("ボイロ"); got != "search:ボイロ" {
		t.Errorf("SearchProgressKey = %q", got)
	}
}
