package knowledge

import (
	"context"
	"strings"
	"testing"

	"github.com/shutu-ai/shutu-knowledge/internal/documentir"
)

func TestStructuredIRIsPublishedWithSearchAndDeletedWithDocument(t *testing.T) {
	f := newFixture(t)
	base := f.createBase(t)
	doc, err := f.service.AddTextDocument(context.Background(), base.ID, "Storage guide", "# Storage\n\nSQLite remains the local evidence store.")
	if err != nil {
		t.Fatal(err)
	}
	var nodes, links int
	if err := f.service.store.db.QueryRow(`SELECT COUNT(*) FROM document_nodes WHERE doc_id = ?`, doc.ID).Scan(&nodes); err != nil {
		t.Fatal(err)
	}
	if err := f.service.store.db.QueryRow(`SELECT COUNT(*) FROM chunk_node_links WHERE doc_id = ?`, doc.ID).Scan(&links); err != nil {
		t.Fatal(err)
	}
	if nodes < 3 || links == 0 {
		t.Fatalf("published IR nodes=%d links=%d", nodes, links)
	}
	ir, err := f.service.GetDocumentIR(context.Background(), doc.ID)
	if err != nil {
		t.Fatal(err)
	}
	if ir.IRVersion != "document-ir/v1" || len(ir.Nodes) != nodes {
		t.Fatalf("IR metadata: version=%q nodes=%d", ir.IRVersion, len(ir.Nodes))
	}
	derived, err := f.service.GetDerivedKnowledge(context.Background(), doc.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(derived) < 2 {
		t.Fatalf("derived knowledge missing: %+v", derived)
	}
	foundOutline := false
	for _, item := range derived {
		if item.Kind == "document_outline" && strings.Contains(item.Content, "Storage") {
			foundOutline = true
		}
	}
	if !foundOutline {
		t.Fatalf("deterministic document outline missing: %+v", derived)
	}
	result, err := f.service.Search(context.Background(), SearchRequest{Query: "evidence store", BaseID: base.ID, Mode: "lexical", TopK: 3})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Hits) != 1 || result.Hits[0].Citation == nil || len(result.Hits[0].NodeIDs) == 0 {
		t.Fatalf("search provenance missing: %+v", result.Hits)
	}
	if err := f.service.DeleteDocument(doc.ID); err != nil {
		t.Fatal(err)
	}
	if err := f.service.store.db.QueryRow(`SELECT COUNT(*) FROM document_nodes WHERE doc_id = ?`, doc.ID).Scan(&nodes); err != nil {
		t.Fatal(err)
	}
	if nodes != 0 {
		t.Fatalf("IR nodes survived delete: %d", nodes)
	}
}

func TestStructuredContextIncludesHeadingTableAndMatchedRow(t *testing.T) {
	ir := &documentir.Document{Nodes: []documentir.Node{
		{ID: "doc", Type: documentir.TypeDocument},
		{ID: "table", Type: documentir.TypeTable, Text: "Revenue by region", ParentID: "doc"},
		{ID: "header", Type: documentir.TypeTableRow, Text: "Region | Q4", ParentID: "table", Order: 1},
		{ID: "row", Type: documentir.TypeTableRow, Text: "APAC | 150", ParentID: "table", Order: 2},
		{ID: "cell", Type: documentir.TypeTableCell, Text: "150", ParentID: "row", HeadingPath: []string{"Revenue"}},
	}}
	value := structuralContext(ir, []string{"cell"})
	for _, want := range []string{"Revenue", "Revenue by region", "APAC | 150", "Region | Q4"} {
		if !strings.Contains(value, want) {
			t.Fatalf("structural context missing %q: %q", want, value)
		}
	}
}
