package getuploader

import (
	"testing"
	"unicode/utf8"
)

func TestMaskPassword(t *testing.T) {
	cases := []struct {
		in   string
		want string
	}{
		{"", ""},
		{"a", "*"},
		{"ab", "**"},
		{"2525", "2**5"},
		{"２５２５", "２**５"}, // 全角也要按 rune 处理，不能切坏 UTF-8
		{"あいうえ", "あ**え"},
	}
	for _, tc := range cases {
		got := maskPassword(tc.in)
		if got != tc.want {
			t.Errorf("maskPassword(%q) = %q, want %q", tc.in, got, tc.want)
		}
		if !utf8.ValidString(got) {
			t.Errorf("maskPassword(%q) 产生了非法 UTF-8: %q", tc.in, got)
		}
	}
}

func TestParseSizeText(t *testing.T) {
	cases := map[string]int64{
		"1 GB":  1 << 30,
		"4 KB":  4 << 10,
		"512 B": 512,
		"???":   0,
		"":      0,
	}
	for in, want := range cases {
		if got := ParseSizeText(in); got != want {
			t.Errorf("ParseSizeText(%q) = %d, want %d", in, got, want)
		}
	}
	// 小数会按浮点截断，只检查大致范围。
	got := ParseSizeText("11.1 MB")
	if got < 11<<20 || got > 12<<20 {
		t.Errorf("ParseSizeText(\"11.1 MB\") = %d, 超出预期范围", got)
	}
}
