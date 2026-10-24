package cli

import "testing"

func TestParseCatalogArgs(t *testing.T) {
	cases := []struct {
		name     string
		args     []string
		wantPW   string
		wantPage int
		wantErr  bool
	}{
		{name: "只有 URL", args: []string{"https://ux.getuploader.com/X/"}, wantPW: "", wantPage: 1},
		{name: "密码", args: []string{"url", "2525"}, wantPW: "2525", wantPage: 1},
		{name: "密码+页码", args: []string{"url", "2525", "2"}, wantPW: "2525", wantPage: 2},
		{name: "字母密码", args: []string{"url", "abcd1234", "3"}, wantPW: "abcd1234", wantPage: 3},
		{name: "占位符 + 页码", args: []string{"url", "-", "3"}, wantPW: "", wantPage: 3},
		{name: "none 占位", args: []string{"url", "none", "4"}, wantPW: "", wantPage: 4},
		{name: "全角密码", args: []string{"url", "２５２５"}, wantPW: "２５２５", wantPage: 1},
		{name: "页码非法", args: []string{"url", "pw", "abc"}, wantErr: true},
		{name: "页码为 0", args: []string{"url", "pw", "0"}, wantErr: true},
		{name: "参数过多", args: []string{"url", "pw", "2", "extra"}, wantErr: true},
		{name: "没有参数", args: nil, wantErr: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			pw, page, err := parseCatalogArgs(tc.args)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("期望报错，得到 pw=%q page=%d", pw, page)
				}
				return
			}
			if err != nil {
				t.Fatalf("parseCatalogArgs(%v): %v", tc.args, err)
			}
			if pw != tc.wantPW {
				t.Errorf("password = %q, want %q", pw, tc.wantPW)
			}
			if page != tc.wantPage {
				t.Errorf("page = %d, want %d", page, tc.wantPage)
			}
		})
	}
}

func TestInferCommandCatalog(t *testing.T) {
	cases := []struct {
		in   []string
		want string
	}{
		{[]string{"https://ux.getuploader.com/SHINOBILI7/"}, "down-catalog"},
		{[]string{"https://uu.getuploader.com/harinashi/", "2525", "2"}, "down-catalog"},
		// 下载页仍然走 down。
		{[]string{"https://uu.getuploader.com/harinashi/download/71", "2525"}, "down"},
	}
	for _, tc := range cases {
		got := inferCommand(tc.in)
		if len(got) == 0 || got[0] != tc.want {
			t.Errorf("inferCommand(%v) = %v, want 前缀 %s", tc.in, got, tc.want)
		}
		if len(got) != len(tc.in)+1 {
			t.Errorf("参数数量变化: %v -> %v", tc.in, got)
		}
	}
}
