package nico

import (
	"strings"
	"testing"

	"nico-seiga-archiver/internal/extract"
	"nico-seiga-archiver/internal/htmlx"
)

func TestMarkStrikethrough(t *testing.T) {
	in := "旧: <s>https://uu.getuploader.com/a/download/1</s>\n新: https://uu.getuploader.com/a/download/2"
	out := markStrikethrough(in)
	if !strings.Contains(out, strikeMarkOpen) || !strings.Contains(out, strikeMarkClose) {
		t.Fatalf("未生成删除线标记: %q", out)
	}
	if strings.Contains(out, "<s>") || strings.Contains(out, "</s>") {
		t.Errorf("<s> 应被替换: %q", out)
	}
}

func TestMarkStrikethroughStyle(t *testing.T) {
	in := `<span style="text-decoration: line-through">https://uu.getuploader.com/a/download/1</span>`
	out := markStrikethrough(in)
	if !strings.Contains(out, strikeMarkOpen) {
		t.Fatalf("style 删除线未被识别: %q", out)
	}
}

func TestMarkStrikethroughUnbalanced(t *testing.T) {
	// 不配对的标签视为无效，不应把后面所有内容都划掉。
	in := "<s>oops\nhttps://uu.getuploader.com/a/download/1"
	out := markStrikethrough(in)
	if strings.Contains(out, strikeMarkOpen) || strings.Contains(out, strikeMarkClose) {
		t.Errorf("不配对时应丢弃标记: %q", out)
	}
}

func TestNormalizeDescriptionSkipsStruck(t *testing.T) {
	raw := "旧: <s>https://uu.getuploader.com/a/download/1</s><br>新: https://uu.getuploader.com/a/download/2<br>パスワード：2525"
	desc := normalizeDescription(raw)

	usable, skipped := extract.LinksWithSkipped(desc)
	if len(skipped) != 1 || skipped[0] != "https://uu.getuploader.com/a/download/1" {
		t.Fatalf("skipped = %v", skipped)
	}
	if len(usable) != 1 || usable[0].URL != "https://uu.getuploader.com/a/download/2" {
		t.Fatalf("usable = %+v", usable)
	}
	if usable[0].Password != "2525" {
		t.Errorf("password = %q", usable[0].Password)
	}
}

func TestNormalizeDescriptionStyleStrike(t *testing.T) {
	raw := `<span style="text-decoration: line-through">https://uu.getuploader.com/a/download/1</span><br>` +
		`https://uu.getuploader.com/a/download/2`
	desc := normalizeDescription(raw)
	usable, skipped := extract.LinksWithSkipped(desc)
	if len(usable) != 1 || usable[0].URL != "https://uu.getuploader.com/a/download/2" {
		t.Fatalf("usable = %+v（desc=%q）", usable, desc)
	}
	if len(skipped) != 1 || skipped[0] != "https://uu.getuploader.com/a/download/1" {
		t.Fatalf("skipped = %v（desc=%q）", skipped, desc)
	}
}

func TestAnchorHrefInStrikeSkipped(t *testing.T) {
	raw := `<s><a href="https://uu.getuploader.com/a/download/1">旧</a></s><br><a href="https://uu.getuploader.com/a/download/2">新</a>`
	desc := normalizeDescription(raw)
	usable, skipped := extract.LinksWithSkipped(desc)
	if len(skipped) != 1 || skipped[0] != "https://uu.getuploader.com/a/download/1" {
		t.Fatalf("skipped = %v（desc=%q）", skipped, desc)
	}
	if len(usable) != 1 || usable[0].URL != "https://uu.getuploader.com/a/download/2" {
		t.Fatalf("usable = %+v", usable)
	}
}

func TestPageDescriptionSkipsStruckNodes(t *testing.T) {
	fixture := `<html><body>
	<div class="illust_body">
		<p>旧: <s>https://uu.getuploader.com/a/download/1</s></p>
		<p>新: https://uu.getuploader.com/a/download/2</p>
		<p><span style="text-decoration: line-through">https://uu.getuploader.com/a/download/3</span></p>
	</div>
	</body></html>`
	doc, err := htmlx.Parse(strings.NewReader(fixture))
	if err != nil {
		t.Fatal(err)
	}
	desc := pageDescription(doc)
	usable, skipped := extract.LinksWithSkipped(desc)
	if len(usable) != 1 || usable[0].URL != "https://uu.getuploader.com/a/download/2" {
		t.Fatalf("usable = %+v（desc=%q）", usable, desc)
	}
	if len(skipped) != 0 {
		t.Errorf("页面路径直接剔除删除线节点，不应留下 skipped: %v", skipped)
	}
	if strings.Contains(desc, "download/1") || strings.Contains(desc, "download/3") {
		t.Errorf("删除线内容未被剔除: %q", desc)
	}
}

func TestStruckAt(t *testing.T) {
	s := string(strikeMarkOpen) + "abc" + string(strikeMarkClose) + "def"
	if !struckAt(s, 1) {
		t.Error("abc 内部应判定为删除线")
	}
	if struckAt(s, 5) {
		t.Error("def 应判定为非删除线")
	}
}
