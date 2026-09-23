package schemamodel

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/shutu-ai/shutu-knowledge/internal/storage"
)

func TestStoreReplacesDocumentModelAtomically(t *testing.T) {
	path := filepath.Join(t.TempDir(), "knowledge.db")
	db, err := storage.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	store := NewStore(db)
	ctx := context.Background()
	input := DocumentInput{
		BaseID: "base", DocumentID: "doc", Generation: 1, Title: "Workbook.xlsx",
		SheetName: "Fields", SourcePath: "fixture.xlsx",
		Rows: []SourceRow{
			{NodeID: "header", Text: strings.Join([]string{"字段名称", "数据类型", "字段含义"}, "\t"), Range: "A1:C1"},
			{NodeID: "field", Text: strings.Join([]string{"user_id", "STRING", "User identifier"}, "\t"), Range: "A2:C2"},
		},
	}
	model, detected := CompileDocument(input)
	if !detected || len(model.Fields) != 1 {
		t.Fatalf("compile detected=%v fields=%d", detected, len(model.Fields))
	}
	if err := store.ReplaceDocumentModel(ctx, input, model, 12, detected); err != nil {
		t.Fatal(err)
	}
	// Replacing the same generation must be idempotent, not create mixed rows.
	if err := store.ReplaceDocumentModel(ctx, input, model, 12, detected); err != nil {
		t.Fatal(err)
	}
	loaded, err := store.ActiveModels(ctx, "base")
	if err != nil {
		t.Fatal(err)
	}
	if len(loaded) != 1 || len(loaded[0].Fields) != 1 || loaded[0].Fields[0].Name != "user_id" {
		t.Fatalf("round trip: %+v", loaded)
	}
	if loaded[0].Fields[0].Source.Range != "A2:C2" {
		t.Fatalf("provenance round trip: %+v", loaded[0].Fields[0].Source)
	}
	// A later non-dictionary generation must atomically remove all entities.
	emptyInput := input
	emptyInput.Generation = 2
	emptyInput.Rows = nil
	emptyModel, detected := CompileDocument(emptyInput)
	if detected {
		t.Fatal("empty model incorrectly detected")
	}
	if err := store.ReplaceDocumentModel(ctx, emptyInput, emptyModel, 1, false); err != nil {
		t.Fatal(err)
	}
	loaded, err = store.ActiveModels(ctx, "base")
	if err != nil {
		t.Fatal(err)
	}
	if len(loaded) != 0 {
		t.Fatalf("absent generation retained entities: %+v", loaded)
	}
}
