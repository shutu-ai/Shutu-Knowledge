package storage

import (
	"path/filepath"
	"strings"
	"testing"
)

func openTestDB(t *testing.T) *DB {
	t.Helper()
	db, err := Open(filepath.Join(t.TempDir(), "knowledge.db"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db
}

func TestMigrateAppliesAndIsIdempotent(t *testing.T) {
	db := openTestDB(t)
	v, err := SchemaVersion(db.DB)
	if err != nil {
		t.Fatal(err)
	}
	if v < 1 {
		t.Fatalf("expected applied migrations, got version %d", v)
	}
	// Re-running must not fail or duplicate.
	if err := Migrate(db.DB); err != nil {
		t.Fatalf("re-migrate: %v", err)
	}
	var count int
	if err := db.QueryRow(`SELECT COUNT(*) FROM bases`).Scan(&count); err != nil {
		t.Fatalf("bases table missing: %v", err)
	}
	if err := db.QueryRow(`SELECT COUNT(*) FROM chunks`).Scan(&count); err != nil {
		t.Fatalf("chunks table missing: %v", err)
	}
	// FTS5 must be functional (external-content table with triggers).
	if _, err := db.Exec(`INSERT INTO chunks (id, doc_id, base_id, idx, text, context, created_at) VALUES ('c1','d1','b1',0,'hello world','t','0')`); err != nil {
		t.Fatalf("insert chunk: %v", err)
	}
	rows, err := db.Query(`SELECT fts_rowid FROM chunk_fts WHERE chunk_fts MATCH 'hello'`)
	if err != nil {
		t.Fatalf("fts match: %v", err)
	}
	defer rows.Close()
	if !rows.Next() {
		t.Fatal("fts did not index inserted chunk")
	}
}

func TestRawStoreRoundTripAndGuards(t *testing.T) {
	root := filepath.Join(t.TempDir(), "raw")
	store, err := NewRawFileStore(root)
	if err != nil {
		t.Fatal(err)
	}
	rel, err := store.Write("base1", "doc1", ".pdf", []byte("data"))
	if err != nil {
		t.Fatalf("write: %v", err)
	}
	if rel != "base1/doc1.pdf" {
		t.Fatalf("rel path: %s", rel)
	}
	data, err := store.Read(rel)
	if err != nil || string(data) != "data" {
		t.Fatalf("read: %v %q", err, data)
	}
	tree, err := store.WriteRel("base1", "sub/dir/note.md", []byte("tree"))
	if err != nil {
		t.Fatalf("writeRel: %v", err)
	}
	if tree != "base1/sub/dir/note.md" {
		t.Fatalf("tree path: %s", tree)
	}
	all, err := store.ListAll()
	if err != nil || len(all) != 2 {
		t.Fatalf("listAll: %v %v", all, err)
	}
	if err := store.DeleteBase("base1"); err != nil {
		t.Fatalf("deleteBase: %v", err)
	}
	data, err = store.Read(rel)
	if err != nil || data != nil {
		t.Fatalf("read after delete: %v %v", data, err)
	}
	for _, bad := range []string{"../escape", "a/../b", `a\..\b`, "", "a/b"} {
		if _, err := store.Write(bad, "doc", ".txt", nil); err == nil {
			t.Fatalf("expected error for segment %q", bad)
		}
	}
	if _, err := store.pathOf("../outside"); err == nil || !strings.Contains(err.Error(), "unsafe") {
		t.Fatalf("pathOf escape guard: %v", err)
	}
}
