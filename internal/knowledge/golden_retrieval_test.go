package knowledge

import (
	"archive/zip"
	"bytes"
	"context"
	"strings"
	"testing"

	"github.com/shutu-ai/shutu-knowledge/internal/documentir"
)

func goldenXLSX(t *testing.T) []byte {
	t.Helper()
	var out bytes.Buffer
	archive := zip.NewWriter(&out)
	parts := map[string]string{
		"xl/workbook.xml":          `<workbook><sheets><sheet name="Revenue" sheetId="1"/></sheets></workbook>`,
		"xl/sharedStrings.xml":     `<sst><si><t>Region</t></si><si><t>Q4</t></si><si><t>APAC</t></si><si><t>150</t></si></sst>`,
		"xl/worksheets/sheet1.xml": `<worksheet><sheetData><row><c r="A1" t="s"><v>0</v></c><c r="B1" t="s"><v>1</v></c></row><row><c r="A2" t="s"><v>2</v></c><c r="B2" t="s"><v>3</v></c></row></sheetData></worksheet>`,
	}
	for name, content := range parts {
		entry, err := archive.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := entry.Write([]byte(content)); err != nil {
			t.Fatal(err)
		}
	}
	if err := archive.Close(); err != nil {
		t.Fatal(err)
	}
	return out.Bytes()
}

func TestGoldenRetrievalAndCitationAccuracy(t *testing.T) {
	f := newFixture(t)
	base := f.createBase(t)
	financial, err := f.service.AddTextDocument(context.Background(), base.ID, "financial.md", "# Revenue Overview\n\nAPAC revenue in Q4 was 150 million.")
	if err != nil {
		t.Fatal(err)
	}
	result, err := f.service.Search(context.Background(), SearchRequest{BaseID: base.ID, Query: "APAC revenue Q4", Mode: "lexical", TopK: 3})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Hits) != 1 || result.Hits[0].DocID != financial.ID {
		t.Fatalf("financial golden retrieval: %+v", result.Hits)
	}
	hit := result.Hits[0]
	if hit.Citation == nil || hit.Citation.Document != "financial.md" || hit.Citation.Section != "Revenue Overview" ||
		!strings.Contains(hit.Citation.Snippet, "APAC") || hit.Citation.NodeID == "" || !containsString(hit.NodeIDs, hit.Citation.NodeID) {
		t.Fatalf("financial citation is not source-accurate: %+v", hit.Citation)
	}

	workbook, err := f.service.AddFileDocument(context.Background(), base.ID, "financial.xlsx", goldenXLSX(t), "")
	if err != nil {
		t.Fatal(err)
	}
	ir, err := f.service.GetDocumentIR(context.Background(), workbook.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !hasSheetCellForKnowledge(ir, "Revenue", "A2") {
		t.Fatal("stored spreadsheet IR lost A2")
	}
	spreadsheet, err := f.service.Search(context.Background(), SearchRequest{
		BaseID: base.ID, Query: "APAC 150", Mode: "lexical", TopK: 3,
		Filter: &SearchFilter{Structure: &StructureFilter{Sheets: []string{"Revenue"}, NodeTypes: []string{"table_cell"}}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(spreadsheet.Hits) != 1 || spreadsheet.Hits[0].DocID != workbook.ID {
		t.Fatalf("spreadsheet golden retrieval: %+v", spreadsheet.Hits)
	}
	cellHit := spreadsheet.Hits[0]
	if cellHit.Citation == nil || cellHit.Citation.Sheet != "Revenue" || cellHit.Citation.CellRange != "A2" ||
		!strings.Contains(cellHit.Citation.Snippet, "APAC") || !strings.Contains(cellHit.Citation.Snippet, "150") {
		t.Fatalf("spreadsheet citation is not row/cell accurate: citation=%+v anchor=%+v nodes=%v", cellHit.Citation, cellHit.SourceAnchor, cellHit.NodeIDs)
	}
}

func containsString(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}

func hasSheetCellForKnowledge(ir documentir.Document, sheet, cell string) bool {
	for _, node := range ir.Nodes {
		if node.Type == "table_cell" && node.SheetName == sheet && node.SourceAnchor.CellRange == cell {
			return true
		}
	}
	return false
}
