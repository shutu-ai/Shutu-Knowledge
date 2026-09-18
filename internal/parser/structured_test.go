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

func TestPDFIRCarriesHeuristicTableAndFigureStructure(t *testing.T) {
	data := pdfFixture(t, "BT /F1 12 Tf 72 720 Td (Table: Region | Revenue) Tj ET\nBT /F1 12 Tf 72 690 Td (Figure 1: Revenue trend) Tj ET\n")
	result, err := NewRegistry().Parse("report.pdf", data)
	if err != nil {
		t.Fatal(err)
	}
	if countIRNodes(result.IR, documentir.TypeTable) != 1 || countIRNodes(result.IR, documentir.TypeTableRow) != 1 || countIRNodes(result.IR, documentir.TypeTableCell) != 2 || countIRNodes(result.IR, documentir.TypeFigure) != 1 || countIRNodes(result.IR, documentir.TypeCaption) != 1 {
		t.Fatalf("PDF structure incomplete: %#v", result.IR.Nodes)
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

func TestDOCXIRCarriesListsTablesCaptionsAndSections(t *testing.T) {
	data := structuredZipFixture(t, map[string]string{
		"word/document.xml": `<w:document xmlns:w="w"><w:body>
<w:p><w:pPr><w:pStyle w:val="Heading1"/></w:pPr><w:r><w:t>Overview</w:t></w:r></w:p>
<w:p><w:pPr><w:numPr><w:ilvl w:val="0"/></w:numPr></w:pPr><w:r><w:t>First item</w:t></w:r></w:p>
<w:tbl><w:tr><w:tc><w:p><w:r><w:t>Region</w:t></w:r></w:p></w:tc><w:tc><w:p><w:r><w:t>Q4</w:t></w:r></w:p></w:tc></w:tr></w:tbl>
<w:p><w:pPr><w:pStyle w:val="Caption"/></w:pPr><w:r><w:t>Revenue figure</w:t></w:r></w:p>
<w:sectPr/></w:body></w:document>`,
	})
	result, err := NewRegistry().Parse("structured.docx", data)
	if err != nil {
		t.Fatal(err)
	}
	if countIRNodes(result.IR, documentir.TypeListItem) != 1 || countIRNodes(result.IR, documentir.TypeTable) != 1 || countIRNodes(result.IR, documentir.TypeTableRow) != 1 || countIRNodes(result.IR, documentir.TypeTableCell) != 2 || countIRNodes(result.IR, documentir.TypeCaption) != 1 || countIRNodes(result.IR, documentir.TypeSection) != 1 {
		t.Fatalf("DOCX structure incomplete: %#v", result.IR.Nodes)
	}
}

func TestPPTXIRCarriesTableFigureAndSpeakerNotes(t *testing.T) {
	data := structuredZipFixture(t, map[string]string{
		"ppt/slides/slide1.xml":           `<p:sld xmlns:p="p" xmlns:a="a"><p:sp><a:t>Retention title</a:t></p:sp><p:graphicFrame><a:tbl><a:tr><a:tc><a:t>Region</a:t></a:tc><a:tc><a:t>Q4</a:t></a:tc></a:tr></a:tbl></p:graphicFrame><p:pic/></p:sld>`,
		"ppt/notesSlides/notesSlide1.xml": `<p:notes xmlns:p="p" xmlns:a="a"><a:t>Discuss churn assumptions</a:t></p:notes>`,
	})
	result, err := NewRegistry().Parse("deck.pptx", data)
	if err != nil {
		t.Fatal(err)
	}
	if countIRNodes(result.IR, documentir.TypeTable) != 1 || countIRNodes(result.IR, documentir.TypeTableRow) != 1 || countIRNodes(result.IR, documentir.TypeTableCell) != 2 || countIRNodes(result.IR, documentir.TypeFigure) != 1 || countIRNodes(result.IR, documentir.TypeImage) != 1 || countIRNodes(result.IR, documentir.TypeFootnote) != 1 {
		t.Fatalf("PPTX structure incomplete: %#v", result.IR.Nodes)
	}
}

func TestXLSXIRCarriesFormulaHeadersUsedRangeAndMergedMetadata(t *testing.T) {
	data := structuredZipFixture(t, map[string]string{
		"xl/workbook.xml":          `<workbook><sheets><sheet name="Revenue" sheetId="1"/></sheets></workbook>`,
		"xl/sharedStrings.xml":     `<sst><si><t>Region</t></si><si><t>Q4</t></si><si><t>APAC</t></si></sst>`,
		"xl/worksheets/sheet1.xml": `<worksheet><sheetData><row><c r="A1" t="s"><v>0</v></c><c r="B1" t="s"><v>1</v></c></row><row><c r="A2" t="s"><v>2</v></c><c r="B2"><f>SUM(A2:A2)</f><v>150</v></c></row></sheetData><mergeCells><mergeCell ref="A1:B1"/></mergeCells></worksheet>`,
	})
	result, err := NewRegistry().Parse("revenue.xlsx", data)
	if err != nil {
		t.Fatal(err)
	}
	var formula, header, merged bool
	for _, node := range result.IR.Nodes {
		if node.Type != documentir.TypeTableCell {
			continue
		}
		if node.SourceAnchor.CellRange == "B2" && node.Metadata["formula"] == "SUM(A2:A2)" && node.Metadata["header_value"] == "Q4" {
			formula = true
		}
		if node.SourceAnchor.CellRange == "A1" && node.Metadata["header"] == "true" && node.Metadata["merged"] == "true" {
			header, merged = true, true
		}
	}
	usedRange := false
	for _, node := range result.IR.Nodes {
		if node.Type == documentir.TypeSheet && node.Metadata["used_range"] == "A1:B2" {
			usedRange = true
		}
	}
	if !usedRange || !formula || !header || !merged {
		t.Fatalf("XLSX metadata incomplete: %#v", result.IR.Nodes)
	}
}

func countIRNodes(ir *documentir.Document, typ string) int {
	count := 0
	if ir == nil {
		return count
	}
	for _, node := range ir.Nodes {
		if node.Type == typ {
			count++
		}
	}
	return count
}
