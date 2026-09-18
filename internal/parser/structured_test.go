package parser

import (
	"archive/zip"
	"bytes"
	"testing"

	"github.com/shutu-ai/shutu-knowledge/internal/documentir"
)

func structuredZipFixture(t *testing.T, files map[string]string) []byte {
	t.Helper()
	var out bytes.Buffer
	zw := zip.NewWriter(&out)
	for name, content := range files {
		w, err := zw.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := w.Write([]byte(content)); err != nil {
			t.Fatal(err)
		}
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	return out.Bytes()
}

func TestPPTXResultCarriesSlideAnchors(t *testing.T) {
	data := structuredZipFixture(t, map[string]string{
		"ppt/slides/slide1.xml": `<p:sld xmlns:p="p" xmlns:a="a"><a:t>Churn</a:t></p:sld>`,
		"ppt/slides/slide2.xml": `<p:sld xmlns:p="p" xmlns:a="a"><a:t>Retention</a:t></p:sld>`,
	})
	result, err := NewRegistry().Parse("deck.pptx", data)
	if err != nil {
		t.Fatal(err)
	}
	if result.IR == nil || len(result.IR.Nodes) != 5 {
		t.Fatalf("unexpected IR: %#v", result.IR)
	}
	if result.IR.Nodes[1].Type != documentir.TypeSlide || result.IR.Nodes[1].SlideNumber != 1 {
		t.Fatalf("missing first slide anchor: %#v", result.IR.Nodes[1])
	}
}

func TestPDFResultCarriesPageBlockAndBBoxAnchors(t *testing.T) {
	data := pdfFixture(t, "BT /F1 18 Tf 72 720 Td (Architecture) Tj ET\nBT /F1 12 Tf 72 690 Td (Storage evidence) Tj ET\n")
	result, err := NewRegistry().Parse("guide.pdf", data)
	if err != nil {
		t.Fatal(err)
	}
	if result.IR == nil || len(result.IR.Nodes) < 3 {
		t.Fatalf("PDF IR too small: %#v", result.IR)
	}
	found := false
	for _, node := range result.IR.Nodes {
		if node.Type == documentir.TypeHeading && node.SourceAnchor.Page == 1 && node.BBox != nil {
			found = true
		}
	}
	if !found {
		t.Fatalf("PDF heading/page/bbox anchor missing: %#v", result.IR.Nodes)
	}
}

func TestXLSXResultCarriesCellAnchors(t *testing.T) {
	data := structuredZipFixture(t, map[string]string{
		"xl/workbook.xml":          `<workbook><sheets><sheet name="Revenue" sheetId="1"/></sheets></workbook>`,
		"xl/sharedStrings.xml":     `<sst><si><t>APAC</t></si><si><t>150</t></si></sst>`,
		"xl/worksheets/sheet1.xml": `<worksheet><sheetData><row><c r="A1" t="s"><v>0</v></c><c r="B1" t="s"><v>1</v></c></row></sheetData></worksheet>`,
	})
	result, err := NewRegistry().Parse("revenue.xlsx", data)
	if err != nil {
		t.Fatal(err)
	}
	if result.IR == nil {
		t.Fatal("XLSX result has no IR")
	}
	found := false
	for _, node := range result.IR.Nodes {
		if node.Type == documentir.TypeTableCell && node.SourceAnchor.CellRange == "A1" && node.SourceAnchor.Sheet == "Revenue" {
			found = true
		}
	}
	if !found {
		t.Fatalf("no A1 cell anchor in %#v", result.IR.Nodes)
	}
}
