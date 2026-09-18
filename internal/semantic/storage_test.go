package semantic

import (
	"context"
	"errors"
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
	if version < 19 {
		t.Fatalf("schema version = %d, want at least 19", version)
	}
	for _, table := range []string{"bases", "documents", "chunks", "knowledge_compilations", "knowledge_units"} {
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
