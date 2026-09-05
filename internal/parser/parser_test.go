package parser

import (
	"archive/zip"
	"bytes"
	"strings"
	"testing"

	"golang.org/x/text/encoding/simplifiedchinese"
)

func zipFixture(t *testing.T, files map[string]string) []byte {
	t.Helper()
	var buf bytes.Buffer
	writer := zip.NewWriter(&buf)
	for name, content := range files {
		entry, err := writer.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := entry.Write([]byte(content)); err != nil {
			t.Fatal(err)
		}
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func parseOK(t *testing.T, fileName string, data []byte) Result {
	t.Helper()
	result, err := NewRegistry().Parse(fileName, data)
	if err != nil {
		t.Fatalf("parse %s: %v", fileName, err)
	}
	return result
}

func TestParseDocx(t *testing.T) {
	docx := zipFixture(t, map[string]string{
		"word/document.xml": `<?xml version="1.0"?><w:document xmlns:w="w"><w:body><w:p><w:r><w:t>First paragraph</w:t></w:r></w:p><w:p><w:r><w:t>第二段</w:t><w:br/><w:t>same para</w:t></w:r></w:p></w:body></w:document>`,
	})
	result := parseOK(t, "report.docx", docx)
	if !strings.Contains(result.Text, "First paragraph") || !strings.Contains(result.Text, "第二段") || !strings.Contains(result.Text, "same para") {
		t.Fatalf("docx text: %q", result.Text)
	}
}

func TestParsePptx(t *testing.T) {
	pptx := zipFixture(t, map[string]string{
		"ppt/slides/slide1.xml": `<p:sld xmlns:p="p"><p:txBody><a:t xmlns:a="a">Slide one title</a:t></p:txBody></p:sld>`,
		"ppt/slides/slide2.xml": `<p:sld xmlns:p="p"><p:txBody><a:t xmlns:a="a">Second slide</a:t></p:txBody></p:sld>`,
		"ppt/theme/theme1.xml":  `<a:theme xmlns:a="a"><a:t>theme text must be ignored</a:t></a:theme>`,
	})
	result := parseOK(t, "deck.pptx", pptx)
	if strings.Contains(result.Text, "theme text") {
		t.Fatalf("theme leaked into text: %q", result.Text)
	}
	index1 := strings.Index(result.Text, "Slide one title")
	index2 := strings.Index(result.Text, "Second slide")
	if index1 < 0 || index2 < 0 || index1 > index2 {
		t.Fatalf("slide order wrong: %q", result.Text)
	}
}

func TestParseXlsx(t *testing.T) {
	xlsx := zipFixture(t, map[string]string{
		"xl/sharedStrings.xml":     `<sst><si><t>Name</t></si><si><r><t>Rich</t></r><r><t>Text</t></r></si></sst>`,
		"xl/worksheets/sheet1.xml": `<worksheet><row><c t="s"><v>0</v></c><c t="s"><v>1</v></c></row><row><c><v>42</v></c><c t="b"><v>1</v></c></row></worksheet>`,
	})
	result := parseOK(t, "data.xlsx", xlsx)
	lines := strings.Split(result.Text, "\n")
	if len(lines) < 2 {
		t.Fatalf("xlsx text: %q", result.Text)
	}
	if lines[0] != "Name\tRichText" {
		t.Fatalf("shared string row: %q", lines[0])
	}
	if lines[1] != "42\t1" {
		t.Fatalf("value row: %q", lines[1])
	}
}

func TestParseEpub(t *testing.T) {
	epub := zipFixture(t, map[string]string{
		"OEBPS/nav.xhtml":      `<html><body><nav><a>Chapter 1</a></nav></body></html>`,
		"OEBPS/chapter1.xhtml": `<html><head><title>My Book</title></head><body><h1>Chapter 1</h1><p>Hello epub</p></body></html>`,
	})
	result := parseOK(t, "book.epub", epub)
	if result.Title != "My Book" {
		t.Fatalf("epub title: %q", result.Title)
	}
	if !strings.Contains(result.Text, "# Chapter 1") || !strings.Contains(result.Text, "Hello epub") {
		t.Fatalf("epub text: %q", result.Text)
	}
	if strings.Contains(result.Text, "Chapter 1</a>") {
		t.Fatalf("nav leaked: %q", result.Text)
	}
}

func TestParseHTMLStripsChrome(t *testing.T) {
	html := `<html><head><title>Doc</title><style>body{}</style></head><body><nav>menu</nav><h1>Title</h1><script>alert(1)</script><p>Body text with  spaces</p><ul><li>one</li><li>two</li></ul></body></html>`
	result := parseOK(t, "page.html", []byte(html))
	if result.Title != "Doc" {
		t.Fatalf("title: %q", result.Title)
	}
	for _, banned := range []string{"alert(1)", "body{}", "menu"} {
		if strings.Contains(result.Text, banned) {
			t.Fatalf("chrome leaked (%s): %q", banned, result.Text)
		}
	}
	for _, want := range []string{"# Title", "Body text with spaces", "- one", "- two"} {
		if !strings.Contains(result.Text, want) {
			t.Fatalf("missing %q in %q", want, result.Text)
		}
	}
}

func TestDecodeTextGB18030Fallback(t *testing.T) {
	encoded, err := simplifiedchinese.GB18030.NewEncoder().Bytes([]byte("中文测试文本，用于编码回退验证。"))
	if err != nil {
		t.Fatal(err)
	}
	got := DecodeText(encoded)
	if !strings.Contains(got, "中文测试文本") {
		t.Fatalf("gb18030 decode failed: %q", got)
	}
}

func TestRegistryDispatchAndRejections(t *testing.T) {
	registry := NewRegistry()
	got := registry.SupportedExtensions()
	if len(got) < 12 || !contains(got, "pdf") || !contains(got, "epub") {
		t.Fatalf("supported: %v", got)
	}
	if _, err := registry.Parse("archive.rar", []byte("x")); err == nil || !strings.Contains(err.Error(), "unsupported") {
		t.Fatalf("expected unsupported error, got %v", err)
	}
	// Legacy OLE formats are intentionally unsupported until the optional
	// external helper phase.
	if _, err := registry.Parse("legacy.doc", []byte("x")); err == nil {
		t.Fatal("legacy .doc should be unsupported in phase 2")
	}
	// Empty text fails with a parse error, not success.
	if _, err := registry.Parse("empty.txt", []byte("   ")); err == nil {
		t.Fatal("empty text should error")
	}
}

func contains(list []string, want string) bool {
	for _, item := range list {
		if item == want {
			return true
		}
	}
	return false
}
