package extract

import "testing"

func TestStrikeHTMLSkipped(t *testing.T) {
	// 旧链接被 <s> 划掉，新链接可用。
	text := "旧: <s>https://uu.getuploader.com/a/download/1</s>\n新: https://uu.getuploader.com/a/download/2\nパスワード：2525"
	usable, skipped := LinksWithSkipped(text)
	if len(usable) != 1 || usable[0].URL != "https://uu.getuploader.com/a/download/2" {
		t.Fatalf("usable = %+v", usable)
	}
	if len(skipped) != 1 || skipped[0] != "https://uu.getuploader.com/a/download/1" {
		t.Fatalf("skipped = %v", skipped)
	}
	if usable[0].Password != "2525" {
		t.Errorf("password = %q", usable[0].Password)
	}
}

func TestStrikeDelAndStrikeTags(t *testing.T) {
	for _, tag := range []string{"s", "del", "strike"} {
		text := "<" + tag + ">https://uu.getuploader.com/a/download/9</" + tag + ">\nhttps://uu.getuploader.com/a/download/10"
		usable, skipped := LinksWithSkipped(text)
		if len(usable) != 1 || usable[0].URL != "https://uu.getuploader.com/a/download/10" {
			t.Errorf("%s: usable = %+v", tag, usable)
		}
		if len(skipped) != 1 {
			t.Errorf("%s: skipped = %v", tag, skipped)
		}
	}
}

func TestStrikeMarkdown(t *testing.T) {
	text := "~~https://uu.getuploader.com/a/download/1~~\nhttps://uu.getuploader.com/a/download/2"
	usable, skipped := LinksWithSkipped(text)
	if len(usable) != 1 || usable[0].URL != "https://uu.getuploader.com/a/download/2" {
		t.Fatalf("usable = %+v", usable)
	}
	if len(skipped) != 1 || skipped[0] != "https://uu.getuploader.com/a/download/1" {
		t.Fatalf("skipped = %v", skipped)
	}
}

func TestStrikeUnbalancedMarkdown(t *testing.T) {
	// 只有一个 ~~ 时应视为普通文本，链接仍然可用。
	text := "~~https://uu.getuploader.com/a/download/1"
	usable, skipped := LinksWithSkipped(text)
	if len(usable) != 1 {
		t.Fatalf("usable = %+v", usable)
	}
	if len(skipped) != 0 {
		t.Errorf("skipped = %v", skipped)
	}
}

func TestStrikeCombining(t *testing.T) {
	// h̶t̶t̶p̶s̶:̶/̶/̶... 这种组合删除线。
	struck := ""
	for _, r := range "https://uu.getuploader.com/a/download/1" {
		struck += string(r) + "\u0336"
	}
	text := struck + "\nhttps://uu.getuploader.com/a/download/2"
	usable, skipped := LinksWithSkipped(text)
	if len(usable) != 1 || usable[0].URL != "https://uu.getuploader.com/a/download/2" {
		t.Fatalf("usable = %+v", usable)
	}
	if len(skipped) != 1 {
		t.Fatalf("skipped = %v", skipped)
	}
}

func TestStrikePasswordNotUsed(t *testing.T) {
	// 旧密码在删除线里，不能拿来用；应该用未划掉的密码。
	text := "https://uu.getuploader.com/a/download/1\n<s>パスワード：1111</s>\nパスワード：2222"
	usable, _ := LinksWithSkipped(text)
	if len(usable) != 1 {
		t.Fatalf("usable = %+v", usable)
	}
	if usable[0].Password != "2222" {
		t.Errorf("password = %q, want 2222（删除线里的密码应被忽略）", usable[0].Password)
	}
}

func TestStrikeAllSkipped(t *testing.T) {
	text := "<s>https://uu.getuploader.com/a/download/1</s>"
	usable, skipped := LinksWithSkipped(text)
	if len(usable) != 0 {
		t.Fatalf("usable = %+v", usable)
	}
	if len(skipped) != 1 {
		t.Fatalf("skipped = %v", skipped)
	}
}

func TestStrikeDedupSkipped(t *testing.T) {
	text := "<s>https://uu.getuploader.com/a/download/1</s>\n<s>https://uu.getuploader.com/a/download/1</s>"
	_, skipped := LinksWithSkipped(text)
	if len(skipped) != 1 {
		t.Errorf("skipped 应去重: %v", skipped)
	}
}

func TestStrikeFullWidthTilde(t *testing.T) {
	text := "～～https://uu.getuploader.com/a/download/1～～\nhttps://uu.getuploader.com/a/download/2"
	usable, skipped := LinksWithSkipped(text)
	if len(usable) != 1 || usable[0].URL != "https://uu.getuploader.com/a/download/2" {
		t.Fatalf("usable = %+v", usable)
	}
	if len(skipped) != 1 {
		t.Fatalf("skipped = %v", skipped)
	}
}

func TestStrikeDoesNotBreakNormalTilde(t *testing.T) {
	// 单个波浪线不是删除线。
	text := "〜〜 https://uu.getuploader.com/a/download/1"
	usable, skipped := LinksWithSkipped(text)
	if len(usable) != 1 {
		t.Fatalf("usable = %+v", usable)
	}
	if len(skipped) != 0 {
		t.Errorf("skipped = %v", skipped)
	}
}

func TestStrikeSelfClosingTags(t *testing.T) {
	// <br/> 之类的自闭合标签不应打乱删除线配对。
	text := "<s>https://uu.getuploader.com/a/download/1<br/></s>\nhttps://uu.getuploader.com/a/download/2"
	usable, skipped := LinksWithSkipped(text)
	if len(usable) != 1 || usable[0].URL != "https://uu.getuploader.com/a/download/2" {
		t.Errorf("usable = %+v", usable)
	}
	if len(skipped) != 1 {
		t.Errorf("skipped = %v", skipped)
	}
}

func TestStrikeNested(t *testing.T) {
	text := "<del><s>https://uu.getuploader.com/a/download/1</s></del>\nhttps://uu.getuploader.com/a/download/2"
	usable, skipped := LinksWithSkipped(text)
	if len(usable) != 1 || usable[0].URL != "https://uu.getuploader.com/a/download/2" {
		t.Errorf("usable = %+v", usable)
	}
	if len(skipped) != 1 {
		t.Errorf("skipped = %v", skipped)
	}
}

func TestLinksIgnoresStruck(t *testing.T) {
	// Links 是 LinksWithSkipped 的简化版，只返回可用链接。
	text := "<s>https://uu.getuploader.com/a/download/1</s>\nhttps://uu.getuploader.com/a/download/2"
	links := Links(text)
	if len(links) != 1 || links[0].URL != "https://uu.getuploader.com/a/download/2" {
		t.Errorf("links = %+v", links)
	}
}
