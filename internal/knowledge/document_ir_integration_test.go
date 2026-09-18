package knowledge

import (
	"context"
	"testing"
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
	if err != nil { t.Fatal(err) }
	if len(derived) < 2 { t.Fatalf("derived knowledge missing: %+v", derived) }
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
