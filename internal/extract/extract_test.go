package extract

import "testing"

func TestLinks(t *testing.T) {
	cases := []struct {
		name    string
		text    string
		wantURL string
		wantPW  string
		wantSrc string
		wantN   int
	}{
		{
			name:    "pass bracket",
			text:    "配布先【https://uu.getuploader.com/harinashi/download/71】pass【2525】",
			wantURL: "https://uu.getuploader.com/harinashi/download/71",
			wantPW:  "2525",
			wantSrc: "keyword",
			wantN:   1,
		},
		{
			name:    "japanese password line",
			text:    "◆立ち絵配布URL\nhttps://ux.getuploader.com/harinashi/download/42\nパスワード：96310\n",
			wantURL: "https://ux.getuploader.com/harinashi/download/42",
			wantPW:  "96310",
			wantSrc: "keyword",
			wantN:   1,
		},
		{
			name:    "no password",
			text:    "テキストデータはこちら（https://uu.getuploader.com/harinashi/download/70）です。",
			wantURL: "https://uu.getuploader.com/harinashi/download/70",
			wantPW:  "",
			wantSrc: "",
			wantN:   1,
		},
		{
			name:    "full width kept as-is",
			text:    "https://uu.getuploader.com/harinashi/download/9\nｐａｓｓ：２５２５",
			wantURL: "https://uu.getuploader.com/harinashi/download/9",
			wantPW:  "２５２５",
			wantSrc: "keyword",
			wantN:   1,
		},
		{
			name:    "full width digits after url",
			text:    "配布先 https://uu.getuploader.com/harinashi/download/31 ２５２５",
			wantURL: "https://uu.getuploader.com/harinashi/download/31",
			wantPW:  "２５２５",
			wantSrc: "after-url",
			wantN:   1,
		},
		{
			name:    "full width bracket",
			text:    "https://uu.getuploader.com/harinashi/download/32（９９９９）",
			wantURL: "https://uu.getuploader.com/harinashi/download/32",
			wantPW:  "９９９９",
			wantSrc: "bracket",
			wantN:   1,
		},
		{
			name:    "full width keyword and digits",
			text:    "https://uu.getuploader.com/harinashi/download/33\nパスワード：６３０１２",
			wantURL: "https://uu.getuploader.com/harinashi/download/33",
			wantPW:  "６３０１２",
			wantSrc: "keyword",
			wantN:   1,
		},
		{
			name:    "bare host gets scheme and uu",
			text:    "https://getuploader.com/harinashi/download/12",
			wantURL: "https://uu.getuploader.com/harinashi/download/12",
			wantN:   1,
		},
		{
			name:    "two links with own passwords",
			text:    "立ち絵はこちら\nhttps://uu.getuploader.com/a/download/1\nパスワード：1111\n規約はこちら\nhttps://uu.getuploader.com/a/download/2\nパスワード：2222\n",
			wantURL: "https://uu.getuploader.com/a/download/1",
			wantPW:  "1111",
			wantSrc: "keyword",
			wantN:   2,
		},
		{
			name:    "after url digits",
			text:    "https://uu.getuploader.com/harinashi/download/5 2525",
			wantURL: "https://uu.getuploader.com/harinashi/download/5",
			wantPW:  "2525",
			wantSrc: "after-url",
			wantN:   1,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			links := Links(tc.text)
			if len(links) != tc.wantN {
				t.Fatalf("got %d links, want %d: %+v", len(links), tc.wantN, links)
			}
			if tc.wantN == 0 {
				return
			}
			l := links[0]
			if l.URL != tc.wantURL {
				t.Errorf("url = %q, want %q", l.URL, tc.wantURL)
			}
			if l.Password != tc.wantPW {
				t.Errorf("password = %q, want %q", l.Password, tc.wantPW)
			}
			if tc.wantSrc != "" && l.PasswordSource != tc.wantSrc {
				t.Errorf("source = %q, want %q", l.PasswordSource, tc.wantSrc)
			}
		})
	}
}

func TestTwoLinksPasswords(t *testing.T) {
	text := "立ち絵はこちら\nhttps://uu.getuploader.com/a/download/1\nパスワード：1111\n規約はこちら\nhttps://uu.getuploader.com/a/download/2\nパスワード：2222\n"
	links := Links(text)
	if len(links) != 2 {
		t.Fatalf("got %d links, want 2: %+v", len(links), links)
	}
	if links[0].Password != "1111" || links[1].Password != "2222" {
		t.Errorf("passwords = %q, %q; want 1111, 2222", links[0].Password, links[1].Password)
	}
	if links[0].URL != "https://uu.getuploader.com/a/download/1" || links[1].URL != "https://uu.getuploader.com/a/download/2" {
		t.Errorf("urls = %q, %q", links[0].URL, links[1].URL)
	}
}

func TestNormalizeURL(t *testing.T) {
	bad := []string{
		"https://uu.getuploader.com/harinashi/",
		"https://uploader.jp/rule.html",
		"",
	}
	for _, s := range bad {
		if u, ok := NormalizeURL(s); ok {
			t.Errorf("NormalizeURL(%q) = %q, want not ok", s, u)
		}
	}
}

func TestPasswordAlternates(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want []string
	}{
		{"empty", "", nil},
		{"half width only", "2525", []string{"2525"}},
		{"full width first", "２５２５", []string{"２５２５", "2525"}},
		{"full width alnum", "ＡＢＣ１２３", []string{"ＡＢＣ１２３", "ABC123"}},
		{"full width mixed", "２５A", []string{"２５A", "25A"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := PasswordAlternates(tc.in)
			if len(got) != len(tc.want) {
				t.Fatalf("got %v, want %v", got, tc.want)
			}
			for i := range got {
				if got[i] != tc.want[i] {
					t.Errorf("got %v, want %v", got, tc.want)
				}
			}
		})
	}
}

func TestLinkAlternates(t *testing.T) {
	links := Links("https://uu.getuploader.com/harinashi/download/9\nパスワード：２５２５")
	if len(links) != 1 {
		t.Fatalf("got %d links", len(links))
	}
	// 全角密码优先，其次半角。
	got := links[0].Alternates()
	if len(got) != 2 || got[0] != "２５２５" || got[1] != "2525" {
		t.Errorf("Alternates = %v", got)
	}
}

func TestHalfWidthKeepsOrder(t *testing.T) {
	if got := ToHalfWidth("２５２５"); got != "2525" {
		t.Errorf("ToHalfWidth = %q", got)
	}
	if got := ToHalfWidth("パスワード"); got != "パスワード" {
		t.Errorf("片假名不应被转换: %q", got)
	}
}

// URL 里出现全角字符时，仍应能正确识别（宽度只用于匹配，不改写原文）。
func TestLinksWithFullWidthContext(t *testing.T) {
	text := "配布場所：https://uu.getuploader.com/harinashi/download/71\nパスワード：２５２５\n※全角です"
	links := Links(text)
	if len(links) != 1 {
		t.Fatalf("got %d links: %+v", len(links), links)
	}
	if links[0].URL != "https://uu.getuploader.com/harinashi/download/71" {
		t.Errorf("url = %q", links[0].URL)
	}
	if links[0].Password != "２５２５" {
		t.Errorf("password = %q, want ２５２５", links[0].Password)
	}
}
