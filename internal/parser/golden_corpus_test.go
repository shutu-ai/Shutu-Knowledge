package parser

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/shutu-ai/shutu-knowledge/internal/documentir"
)

type goldenManifest struct {
	Version  string   `json:"version"`
	PDF      []string `json:"pdf"`
	Office   []string `json:"office"`
	Language []string `json:"languages"`
	Expected struct {
		Slides     int    `json:"presentation.pptx.slides"`
		Sheet      string `json:"spreadsheet.xlsx.sheet"`
		Cell       string `json:"spreadsheet.xlsx.cell"`
		TableCells int    `json:"tables.docx.tableCells"`
	} `json:"expected"`
}

func goldenCorpusFile(t *testing.T, parts ...string) []byte {
	t.Helper()
	path := filepath.Join(append([]string{"..", "..", "testdata", "document_intelligence"}, parts...)...)
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read golden corpus %s: %v", path, err)
	}
	return data
}

func TestGoldenCorpusManifestAndIR(t *testing.T) {
	var manifest goldenManifest
	if err := json.Unmarshal(goldenCorpusFile(t, "manifest.json"), &manifest); err != nil {
		t.Fatal(err)
	}
	if manifest.Version != "document-intelligence-golden-v1" || len(manifest.PDF) != 6 || len(manifest.Office) != 4 {
		t.Fatalf("unexpected manifest: %+v", manifest)
	}

	pdfBodies := map[string]string{
		"simple.pdf":      "BT /F1 18 Tf 72 720 Td (Revenue Overview) Tj ET\nBT /F1 12 Tf 72 690 Td (APAC revenue Q4 150) Tj ET",
		"multicolumn.pdf": "BT /F1 18 Tf 72 720 Td (Left Column) Tj ET\nBT /F1 12 Tf 72 690 Td (Right Column) Tj ET",
		"tables.pdf":      "BT /F1 18 Tf 72 720 Td (Revenue Table) Tj ET\nBT /F1 12 Tf 72 690 Td (APAC 150 Q4) Tj ET",
		"figures.pdf":     "BT /F1 18 Tf 72 720 Td (Figure 1) Tj ET\nBT /F1 12 Tf 72 690 Td (Revenue trend caption) Tj ET",
		"scanned.pdf":     "BT /F1 12 Tf 72 690 Td (OCR fallback evidence) Tj ET",
		"long.pdf":        "BT /F1 18 Tf 72 720 Td (Long report) Tj ET\nBT /F1 12 Tf 72 690 Td (Page one bounded evidence) Tj ET",
	}
	for _, name := range manifest.PDF {
		result := parseOK(t, name, pdfFixture(t, pdfBodies[name]))
		if result.IR == nil {
			t.Fatalf("%s has no golden IR", name)
		}
		pages := 0
		headings := 0
		for _, node := range result.IR.Nodes {
			if node.Type == documentir.TypePage {
				pages++
			}
			if node.Type == documentir.TypeHeading {
				headings++
			}
		}
		if pages != 1 || len(result.IR.Nodes) < 3 {
			t.Fatalf("%s golden IR: pages=%d nodes=%d", name, pages, len(result.IR.Nodes))
		}
		if name == "simple.pdf" && headings == 0 {
			t.Fatalf("%s lost heading semantics", name)
		}
	}

	structured := zipFixture(t, map[string]string{"word/document.xml": string(goldenCorpusFile(t, "office", "structured.docx.xml"))})
	structuredResult := parseOK(t, "structured.docx", structured)
	if !hasNodeText(structuredResult.IR, documentir.TypeHeading, "Revenue Overview") {
		t.Fatal("structured.docx lost heading node")
	}
	table := zipFixture(t, map[string]string{"word/document.xml": string(goldenCorpusFile(t, "office", "tables.docx.xml"))})
	tableResult := parseOK(t, "tables.docx", table)
	if countNodes(tableResult.IR, documentir.TypeTableCell) != manifest.Expected.TableCells {
		t.Fatalf("tables.docx table cells=%d want %d", countNodes(tableResult.IR, documentir.TypeTableCell), manifest.Expected.TableCells)
	}

	files := map[string]string{}
	for slide := 1; slide <= manifest.Expected.Slides; slide++ {
		files["ppt/slides/slide"+itoa(slide)+".xml"] = "<p:sld xmlns:p=\"p\" xmlns:a=\"a\"><a:t>Slide " + itoa(slide) + " APAC</a:t></p:sld>"
	}
	presentation := parseOK(t, "presentation.pptx", zipFixture(t, files))
	if countNodes(presentation.IR, documentir.TypeSlide) != manifest.Expected.Slides {
		t.Fatalf("presentation slides=%d want %d", countNodes(presentation.IR, documentir.TypeSlide), manifest.Expected.Slides)
	}

	workbook := zipFixture(t, map[string]string{
		"xl/workbook.xml":          string(goldenCorpusFile(t, "office", "workbook.xml")),
		"xl/sharedStrings.xml":     string(goldenCorpusFile(t, "office", "sharedStrings.xml")),
		"xl/worksheets/sheet1.xml": string(goldenCorpusFile(t, "office", "sheet1.xml")),
	})
	spreadsheet := parseOK(t, "spreadsheet.xlsx", workbook)
	if !hasSheetCell(spreadsheet.IR, manifest.Expected.Sheet, manifest.Expected.Cell) {
		t.Fatalf("spreadsheet lost %s!%s anchor", manifest.Expected.Sheet, manifest.Expected.Cell)
	}

	for _, name := range []string{"english.md", "中文.md", "mixed.md"} {
		result := parseOK(t, name, goldenCorpusFile(t, name))
		if result.IR == nil || strings.TrimSpace(result.Text) == "" || countNodes(result.IR, documentir.TypeHeading) != 1 {
			t.Fatalf("language corpus %s lost IR semantics: %+v", name, result)
		}
	}
}

func countNodes(ir *documentir.Document, typ string) int {
	count := 0
	if ir == nil {
		return 0
	}
	for _, node := range ir.Nodes {
		if node.Type == typ {
			count++
		}
	}
	return count
}

func hasNodeText(ir *documentir.Document, typ, want string) bool {
	if ir == nil {
		return false
	}
	for _, node := range ir.Nodes {
		if node.Type == typ && strings.Contains(node.Text, want) {
			return true
		}
	}
	return false
}

func hasSheetCell(ir *documentir.Document, sheet, cell string) bool {
	if ir == nil {
		return false
	}
	for _, node := range ir.Nodes {
		if node.Type == documentir.TypeTableCell && node.SheetName == sheet && node.SourceAnchor.CellRange == cell {
			return true
		}
	}
	return false
}

func itoa(value int) string {
	const digits = "0123456789"
	if value == 0 {
		return "0"
	}
	var out [20]byte
	i := len(out)
	for value > 0 {
		i--
		out[i] = digits[value%10]
		value /= 10
	}
	return string(out[i:])
}
