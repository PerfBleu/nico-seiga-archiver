package cli

import (
	"strings"
	"testing"
)

func TestParseOptions(t *testing.T) {
	o, err := parseOptions([]string{"nico-tag", "VOICEROID", "2", "--dry-run", "-d", "~/seiga", "--proxy=http://127.0.0.1:7897", "--delay", "0", "--timeout", "30", "-q"})
	if err != nil {
		t.Fatal(err)
	}
	if !o.dryRun || !o.quiet || o.force || o.restart {
		t.Errorf("布尔参数解析错误: %+v", o)
	}
	if o.dir != "~/seiga" {
		t.Errorf("dir = %q", o.dir)
	}
	if o.proxy != "http://127.0.0.1:7897" {
		t.Errorf("proxy = %q", o.proxy)
	}
	// delay 内部用 +1 表示「已设置」，所以 0 会变成 1。
	if o.delay != 1 {
		t.Errorf("delay = %d, want 1（表示显式设置为 0）", o.delay)
	}
	if o.timeout != 30 {
		t.Errorf("timeout = %d", o.timeout)
	}
	want := []string{"nico-tag", "VOICEROID", "2"}
	if len(o.argv) != len(want) {
		t.Fatalf("argv = %v, want %v", o.argv, want)
	}
	for i := range want {
		if o.argv[i] != want[i] {
			t.Errorf("argv = %v, want %v", o.argv, want)
		}
	}
}

func TestParseOptionsErrors(t *testing.T) {
	if _, err := parseOptions([]string{"--nope"}); err == nil {
		t.Error("未知参数应当报错")
	}
	if _, err := parseOptions([]string{"--dir"}); err == nil {
		t.Error("缺少值的参数应当报错")
	}
	if _, err := parseOptions([]string{"--delay", "abc"}); err == nil {
		t.Error("非法 delay 应当报错")
	}
	if _, err := parseOptions([]string{"--timeout", "0"}); err == nil {
		t.Error("timeout 为 0 应当报错")
	}
}

func TestParseOptionsTerminator(t *testing.T) {
	o, err := parseOptions([]string{"down", "--", "--not-a-flag", "-x"})
	if err != nil {
		t.Fatal(err)
	}
	if len(o.argv) != 3 || o.argv[1] != "--not-a-flag" || o.argv[2] != "-x" {
		t.Errorf("argv = %v", o.argv)
	}
}

func TestParseOptionsKeys(t *testing.T) {
	o, err := parseOptions([]string{"-c", "/tmp/x.json", "--ua", "UA/1.0", "--cookie", "user_session=abc"})
	if err != nil {
		t.Fatal(err)
	}
	if o.userAgent != "UA/1.0" {
		t.Errorf("ua = %q", o.userAgent)
	}
	if o.cookie != "user_session=abc" {
		t.Errorf("cookie = %q", o.cookie)
	}
	if len(o.argv) != 0 {
		t.Errorf("argv 应为空: %v", o.argv)
	}
}

func TestInferCommand(t *testing.T) {
	cases := []struct {
		name string
		in   []string
		want string
	}{
		{"tag url", []string{"https://seiga.nicovideo.jp/tag/VOICEROID"}, "nico-tag"},
		{"tag url with page", []string{"https://seiga.nicovideo.jp/tag/VOICEROID?page=2", "3"}, "nico-tag"},
		{"author url", []string{"https://seiga.nicovideo.jp/user/illust/39911624"}, "nico-author"},
		{"illust url", []string{"https://seiga.nicovideo.jp/seiga/im11691748"}, "nico-down"},
		{"illust id", []string{"im11691748"}, "nico-down"},
		{"digits id", []string{"11691748"}, "nico-down"},
		{"getuploader", []string{"https://uu.getuploader.com/harinashi/download/71", "2525"}, "down"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := inferCommand(tc.in)
			if len(got) == 0 || got[0] != tc.want {
				t.Errorf("inferCommand(%v) = %v, want 前缀 %s", tc.in, got, tc.want)
			}
			// 原始参数必须完整保留。
			if len(got) != len(tc.in)+1 {
				t.Errorf("参数数量变化: %v -> %v", tc.in, got)
			}
		})
	}
}

func TestInferCommandKeepsKnown(t *testing.T) {
	for _, in := range [][]string{
		{"nico-tag", "VOICEROID"},
		{"down", "https://uu.getuploader.com/a/download/1"},
		{"help"},
		{"--dry-run", "nico-down", "im1"},
	} {
		got := inferCommand(in)
		if strings.Join(got, " ") != strings.Join(in, " ") {
			t.Errorf("inferCommand(%v) = %v, 不应改写", in, got)
		}
	}
}

func TestExtractConfigFlag(t *testing.T) {
	path, rest := ExtractConfigFlag([]string{"nico-tag", "VOICEROID", "-c", "/tmp/a.json", "--dry-run"})
	if path != "/tmp/a.json" {
		t.Errorf("path = %q", path)
	}
	if strings.Join(rest, " ") != "nico-tag VOICEROID --dry-run" {
		t.Errorf("rest = %v", rest)
	}

	path, rest = ExtractConfigFlag([]string{"--config=/tmp/b.json", "probe"})
	if path != "/tmp/b.json" {
		t.Errorf("path = %q", path)
	}
	if strings.Join(rest, " ") != "probe" {
		t.Errorf("rest = %v", rest)
	}

	path, rest = ExtractConfigFlag([]string{"probe"})
	if path != "" || len(rest) != 1 {
		t.Errorf("path=%q rest=%v", path, rest)
	}
}
