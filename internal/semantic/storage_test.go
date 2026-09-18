package semantic

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"testing"

	"github.com/shutu-ai/shutu-knowledge/internal/storage"
)

func TestStoreAtomicPublicationAndProvenance(t *testing.T) {
	db, store := openTestStore(t)
	defer db.Close()
	ctx := context.Background()
	first := testCompilation(1)

	if _, err := store.GetActiveCompilation(ctx, first.BaseID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("before activation error = %v, want ErrNotFound", err)
	}
	if err := store.CreateCompilation(ctx, first); err != nil {
		t.Fatalf("CreateCompilation: %v", err)
	}
	if err := store.ActivateCompilation(ctx, first.BaseID, first.Generation); err != nil {
		t.Fatalf("ActivateCompilation: %v", err)
	}
	active, err := store.GetActiveCompilation(ctx, first.BaseID)
	if err != nil {
		t.Fatalf("GetActiveCompilation: %v", err)
	}
	if active.State != CompilationActive || len(active.Units) != 3 || len(active.Relations) != 1 {
		t.Fatalf("active compilation round-trip = %+v", active)
	}
	fact := active.Units[indexOfUnit(t, active.Units, UnitFact)]
	concept := active.Units[indexOfUnit(t, active.Units, UnitConcept)]
	page := active.Units[indexOfUnit(t, active.Units, UnitKnowledgePage)]
	if len(fact.Sources) != 1 || fact.Sources[0].NodeID != "node-1" {
		t.Fatalf("unit sources did not round-trip: %+v", fact.Sources)
	}
	if len(page.DerivedFrom) != 1 || page.DerivedFrom[0] != concept.ID {
		t.Fatalf("derived_from did not round-trip: %+v", page)
	}

	sources, err := store.ResolveUnitEvidence(ctx, page.ID)
	if err != nil {
		t.Fatalf("ResolveUnitEvidence: %v", err)
	}
	if len(sources) != 1 || sources[0].ChunkID != "chunk-1" || sources[0].NodeID != "node-1" {
		t.Fatalf("resolved evidence = %+v", sources)
	}

	second := testCompilation(2)
	second.Units[0].Content = "The current service uses 2048-dimensional vectors."
	second.Units[0].ValidFrom = 200
	if err := store.CreateCompilation(ctx, second); err != nil {
		t.Fatalf("CreateCompilation second: %v", err)
	}
	if err := store.ActivateCompilation(ctx, second.BaseID, second.Generation); err != nil {
		t.Fatalf("ActivateCompilation second: %v", err)
	}
	next, err := store.GetActiveCompilation(ctx, second.BaseID)
	if err != nil || next.Generation != 2 {
		t.Fatalf("active second generation = %+v, %v", next, err)
	}
	old, err := store.GetCompilation(ctx, first.BaseID, first.Generation)
	if err != nil || old.State != CompilationRetired {
		t.Fatalf("old generation = %+v, %v", old, err)
	}
}

func TestStoreRejectsInvalidCompilationBeforeWrite(t *testing.T) {
	db, store := openTestStore(t)
	defer db.Close()
	invalid := testCompilation(1)
	invalid.Relations[0].Sources = nil
	if err := store.CreateCompilation(context.Background(), invalid); err == nil {
		t.Fatal("CreateCompilation accepted an unproven relation")
	}
	var count int
	if err := db.QueryRow(`SELECT COUNT(*) FROM knowledge_compilations`).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Fatalf("invalid compilation left %d rows", count)
	}
}

func TestMigrationIsAdditiveAndLegacyTablesRemain(t *testing.T) {
	db, _ := openTestStore(t)
	defer db.Close()
	version, err := storage.SchemaVersion(db.DB)
	if err != nil {
		t.Fatal(err)
	}
	if version < 20 {
		t.Fatalf("schema version = %d, want at least 20", version)
	}
	for _, table := range []string{"bases", "documents", "chunks", "knowledge_compilations", "knowledge_units", "knowledge_compilation_queue"} {
		var name string
		if err := db.QueryRow(`SELECT name FROM sqlite_master WHERE type = 'table' AND name = ?`, table).Scan(&name); err != nil {
			t.Fatalf("table %s absent after migration: %v", table, err)
		}
	}
}

func indexOfUnit(t *testing.T, units []Unit, kind UnitKind) int {
	t.Helper()
	for i, unit := range units {
		if unit.Type == kind {
			return i
		}
	}
	t.Fatalf("unit kind %q missing from %+v", kind, units)
	return -1
}

func openTestStore(t *testing.T) (*storage.DB, *Store) {
	t.Helper()
	db, err := storage.Open(filepath.Join(t.TempDir(), "knowledge.db"))
	if err != nil {
		t.Fatal(err)
	}
	return db, NewStore(db)
}

func TestDocumentChangeQueueSurvivesRestartAndResolution(t *testing.T) {
	db, store := openTestStore(t)
	defer db.Close()
	ctx := context.Background()
	change := DocumentChange{
		BaseID: "base-q", DocumentID: "doc-q", ChangeType: "updated",
		IndexGeneration: 3, SourceVersion: 4, ContentHash: "hash",
		CreatedAt: 100, UpdatedAt: 100,
	}
	if err := store.MarkDocumentChanged(ctx, change); err != nil {
		t.Fatal(err)
	}
	change.SourceVersion, change.UpdatedAt = 5, 200
	if err := store.MarkDocumentChanged(ctx, change); err != nil {
		t.Fatal(err)
	}
	pending, err := store.PendingDocumentChanges(ctx, "base-q")
	if err != nil {
		t.Fatal(err)
	}
	if len(pending) != 1 || pending[0].SourceVersion != 5 || pending[0].ResolvedGeneration != 0 {
		t.Fatalf("pending change did not collapse to latest state: %+v", pending)
	}
	if err := store.ResolveDocumentChanges(ctx, "base-q", 9, 300); err != nil {
		t.Fatal(err)
	}
	pending, err = store.PendingDocumentChanges(ctx, "base-q")
	if err != nil {
		t.Fatal(err)
	}
	if len(pending) != 0 {
		t.Fatalf("resolved queue still pending: %+v", pending)
	}
	var resolved int64
	if err := db.QueryRow(`SELECT resolved_generation FROM knowledge_compilation_queue
		WHERE base_id = ? AND doc_id = ?`, "base-q", "doc-q").Scan(&resolved); err != nil {
		t.Fatal(err)
	}
	if resolved != 9 {
		t.Fatalf("resolved generation = %d, want 9", resolved)
	}
}

func TestActiveGenerationReadersNeverSeePartialCompilation(t *testing.T) {
	db, store := openTestStore(t)
	defer db.Close()
	ctx := context.Background()
	first := testCompilation(1)
	second := testCompilation(2)
	if err := store.CreateCompilation(ctx, first); err != nil {
		t.Fatal(err)
	}
	if err := store.ActivateCompilation(ctx, first.BaseID, first.Generation); err != nil {
		t.Fatal(err)
	}
	if err := store.CreateCompilation(ctx, second); err != nil {
		t.Fatal(err)
	}

	done := make(chan error, 1)
	go func() {
		for i := 0; i < 200; i++ {
			active, err := store.GetActiveCompilation(ctx, first.BaseID)
			if err != nil {
				done <- err
				return
			}
			if active.Generation != first.Generation && active.Generation != second.Generation {
				done <- fmt.Errorf("unexpected active generation %d", active.Generation)
				return
			}
			if len(active.Units) != len(first.Units) || len(active.Relations) != len(first.Relations) {
				done <- fmt.Errorf("partial generation %d observed", active.Generation)
				return
			}
			for _, unit := range active.Units {
				if unit.Generation != active.Generation {
					done <- fmt.Errorf("generation %d exposed unit generation %d", active.Generation, unit.Generation)
					return
				}
			}
		}
		close(done)
	}()
	if err := store.ActivateCompilation(ctx, second.BaseID, second.Generation); err != nil {
		t.Fatal(err)
	}
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}

func TestDeleteBaseRemovesSemanticHistoryAndQueue(t *testing.T) {
	db, store := openTestStore(t)
	defer db.Close()
	ctx := context.Background()
	compilation := testCompilation(1)
	if err := store.CreateCompilation(ctx, compilation); err != nil {
		t.Fatal(err)
	}
	if err := store.MarkDocumentChanged(ctx, DocumentChange{
		BaseID: compilation.BaseID, DocumentID: "doc-1", ChangeType: "updated",
		CreatedAt: 1, UpdatedAt: 1,
	}); err != nil {
		t.Fatal(err)
	}
	if err := store.DeleteBase(ctx, compilation.BaseID); err != nil {
		t.Fatal(err)
	}
	for _, table := range []string{"knowledge_compilations", "knowledge_units", "knowledge_unit_sources", "knowledge_unit_derived_from", "knowledge_relations", "knowledge_relation_sources", "knowledge_compilation_queue"} {
		var count int
		if err := db.QueryRow(`SELECT COUNT(*) FROM ` + table).Scan(&count); err != nil {
			t.Fatal(err)
		}
		if count != 0 {
			t.Fatalf("%s retained %d rows after base deletion", table, count)
		}
	}
}
