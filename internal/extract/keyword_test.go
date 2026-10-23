package extract

import "testing"

// TestKeywordVariants 覆盖真实简介里常见的「关键词 + 密码」写法。
func TestKeywordVariants(t *testing.T) {
	cases := []struct {
		name string
		pw   string
		url  string
	}{
		{"日文 パスワード：", "パスワード：２５２５", "２５２５"},
		{"日文 パスワードは", "パスワードは2525", "2525"},
		{"日文 パスワード 空格", "パスワード 2525", "2525"},
		{"日文 パスワード 换行", "パスワード\n2525", "2525"},
		{"日文 パスワード 冒号换行", "パスワード：\n2525", "2525"},
		{"日文 パスワード 箭头", "パスワード→2525", "2525"},
		{"日文 パスワード 括号", "パスワード（2525）", "2525"},
		{"日文 パスワード 方括号", "パスワード【2525】", "2525"},
		{"日文 パスコード", "パスコード:2525", "2525"},
		{"日文 パス", "パス:2525", "2525"},
		{"日文 合言葉", "合言葉は2525", "2525"},
		{"日文 暗証番号", "暗証番号：2525", "2525"},
		{"英文 password", "password: abcd1234", "abcd1234"},
		{"英文 Password 大写", "Password = abcd1234", "abcd1234"},
		{"英文 password 无空格", "password:abcd1234", "abcd1234"},
		{"英文 pass", "pass: abcd1234", "abcd1234"},
		{"英文 pass 无冒号", "pass abcd1234", "abcd1234"},
		{"英文 PASS 大写", "PASS: abcd1234", "abcd1234"},
		{"英文 pw", "pw: abcd1234", "abcd1234"},
		{"英文 PW 大写", "PW=abcd1234", "abcd1234"},
		{"英文 pwd", "pwd: abcd1234", "abcd1234"},
		{"英文 passwd", "passwd: abcd1234", "abcd1234"},
		{"英文 passcode", "passcode: abcd1234", "abcd1234"},
		{"英文 key", "key: abcd1234", "abcd1234"},
		{"英文 password 带空格值", "password : 2525", "2525"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			text := "https://uu.getuploader.com/a/download/1\n" + tc.pw
			links := Links(text)
			if len(links) != 1 {
				t.Fatalf("got %d links: %+v", len(links), links)
			}
			got := links[0].Alternates()[0]
			if got != tc.url {
				t.Errorf("password = %q, want %q（原文: %q）", got, tc.url, tc.pw)
			}
		})
	}
}

// TestKeywordBeforeURL 密码写在链接前面。
func TestKeywordBeforeURL(t *testing.T) {
	cases := []string{
		"パスワード：2525\nhttps://uu.getuploader.com/a/download/1",
		"password: 2525\nhttps://uu.getuploader.com/a/download/1",
		"pw 2525\nhttps://uu.getuploader.com/a/download/1",
	}
	for _, text := range cases {
		links := Links(text)
		if len(links) != 1 {
			t.Fatalf("%q: got %d links", text, len(links))
		}
		if got := links[0].Password; got != "2525" {
			t.Errorf("%q: password = %q, want 2525", text, got)
		}
		if links[0].PasswordSource != "keyword-before" {
			t.Errorf("%q: source = %q", text, links[0].PasswordSource)
		}
	}
}

// TestKeywordNotInsideWord 避免把 bypass / keyword 之类误当成关键词。
func TestKeywordNotInsideWord(t *testing.T) {
	for _, text := range []string{
		"https://uu.getuploader.com/a/download/1\nbypass 2525",
		"https://uu.getuploader.com/a/download/1\nkeyword: 2525",
	} {
		links := Links(text)
		if len(links) != 1 {
			t.Fatalf("%q: got %d links", text, len(links))
		}
		if links[0].PasswordSource == "keyword" {
			t.Errorf("%q: 不应把单词内部的片段当成关键词，得到 %q", text, links[0].Password)
		}
	}
}

// TestFullWidthKeyword 全角英文关键词也要能识别。
func TestFullWidthKeyword(t *testing.T) {
	links := Links("https://uu.getuploader.com/a/download/1\nｐａｓｓｗｏｒｄ：２５２５")
	if len(links) != 1 {
		t.Fatalf("got %d links", len(links))
	}
	if got := links[0].Password; got != "２５２５" {
		t.Errorf("password = %q, want ２５２５", got)
	}
	if alts := links[0].Alternates(); len(alts) != 2 || alts[1] != "2525" {
		t.Errorf("alternates = %v", alts)
	}
}

// TestKeywordPreferNearest 同时出现多个关键词时，取最靠近 URL 的那个。
func TestKeywordPreferNearest(t *testing.T) {
	text := "https://uu.getuploader.com/a/download/1\nパスワード：1111\npassword: 2222"
	links := Links(text)
	if len(links) != 1 {
		t.Fatalf("got %d links", len(links))
	}
	if links[0].Password != "1111" {
		t.Errorf("password = %q, want 1111", links[0].Password)
	}
}

// TestFullWidthSymbolNormalization 全角符号区应整体映射为半角。
func TestFullWidthSymbolNormalization(t *testing.T) {
	cases := map[string]string{
		"！＂＃＄％＆": `!"#$%&`,
		"：＝～＠":   ":=~@",
		"２５２５":   "2525",
		"　":      " ",
		"パスワード":  "パスワード", // 日文不应被改动
		"ﾊﾟｽ":    "ﾊﾟｽ",   // 半角片假名也不动
	}
	for in, want := range cases {
		if got := ToHalfWidth(in); got != want {
			t.Errorf("ToHalfWidth(%q) = %q, want %q", in, got, want)
		}
	}
}
