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

	for _, name := range manifest.PDF {
		result := parseOK(t, name, goldenCorpusFile(t, name))
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
		wantPages := 1
		if name == "long.pdf" {
			wantPages = 105
		}
		if pages != wantPages || len(result.IR.Nodes) < wantPages*2+1 {
			t.Fatalf("%s golden IR: pages=%d want=%d nodes=%d", name, pages, wantPages, len(result.IR.Nodes))
		}
		if name == "simple.pdf" && headings == 0 {
			t.Fatalf("%s lost heading semantics", name)
		}
		if name == "tables.pdf" && (countNodes(result.IR, documentir.TypeTable) != 1 || countNodes(result.IR, documentir.TypeTableCell) != 3) {
			t.Fatalf("%s lost table semantics", name)
		}
		if name == "figures.pdf" && (countNodes(result.IR, documentir.TypeFigure) != 1 || countNodes(result.IR, documentir.TypeCaption) != 1) {
			t.Fatalf("%s lost figure semantics", name)
		}
	}

	structured := goldenCorpusFile(t, "office", "structured.docx")
	structuredResult := parseOK(t, "structured.docx", structured)
	if !hasNodeText(structuredResult.IR, documentir.TypeHeading, "Revenue Overview") {
		t.Fatal("structured.docx lost heading node")
	}
	table := goldenCorpusFile(t, "office", "tables.docx")
	tableResult := parseOK(t, "tables.docx", table)
	if countNodes(tableResult.IR, documentir.TypeTableCell) != manifest.Expected.TableCells {
		t.Fatalf("tables.docx table cells=%d want %d", countNodes(tableResult.IR, documentir.TypeTableCell), manifest.Expected.TableCells)
	}

	presentation := parseOK(t, "presentation.pptx", goldenCorpusFile(t, "office", "presentation.pptx"))
	if countNodes(presentation.IR, documentir.TypeSlide) != manifest.Expected.Slides {
		t.Fatalf("presentation slides=%d want %d", countNodes(presentation.IR, documentir.TypeSlide), manifest.Expected.Slides)
	}

	workbook := goldenCorpusFile(t, "office", "spreadsheet.xlsx")
	spreadsheet := parseOK(t, "spreadsheet.xlsx", workbook)
	if !hasSheetCell(spreadsheet.IR, manifest.Expected.Sheet, manifest.Expected.Cell) {
		t.Fatalf("spreadsheet lost %s!%s anchor", manifest.Expected.Sheet, manifest.Expected.Cell)
	}
	formulaFound, mergedFound := false, false
	for _, node := range spreadsheet.IR.Nodes {
		if node.Type != documentir.TypeTableCell {
			continue
		}
		if node.SourceAnchor.CellRange == "B2" && node.Metadata["formula"] == "SUM(A2:A2)" {
			formulaFound = true
		}
		if node.SourceAnchor.CellRange == "A1" && node.Metadata["merged"] == "true" {
			mergedFound = true
		}
	}
	if !formulaFound || !mergedFound {
		t.Fatalf("spreadsheet lost formula/merged semantics")
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
