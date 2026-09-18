package knowledge

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"testing"

	"github.com/shutu-ai/shutu-knowledge/internal/config"
	"github.com/shutu-ai/shutu-knowledge/internal/semantic"
	"github.com/shutu-ai/shutu-knowledge/internal/storage"
)

func TestCompileSemanticMemoryIsOptionalAndPreservesSearch(t *testing.T) {
	service := newSemanticMemoryTestService(t)
	defer service.close()
	ctx := context.Background()

	base, err := service.CreateBase("Semantic", "", "", BaseConfig{})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = service.AddTextDocument(ctx, base.ID, "Vector Guide", "# Vector Retrieval\n\nThe vector service uses 1024-dimensional vectors."); err != nil {
		t.Fatal(err)
	}
	if _, err = service.AddTextDocument(ctx, base.ID, "Vector Guide Copy", "# Vector Retrieval\n\nThe vector service uses 1024-dimensional vectors."); err != nil {
		t.Fatal(err)
	}

	if _, err := service.GetSemanticCompilation(ctx, base.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("uncompiled semantic memory error = %v, want ErrNotFound", err)
	}
	before, err := service.Search(ctx, SearchRequest{BaseID: base.ID, Query: "1024-dimensional vectors", TopK: 2})
	if err != nil || len(before.Hits) == 0 {
		t.Fatalf("0.3 search before compilation: hits=%d err=%v", len(before.Hits), err)
	}

	compiled, err := service.CompileSemanticMemory(ctx, base.ID)
	if err != nil {
		t.Fatalf("CompileSemanticMemory: %v", err)
	}
	counts := map[semantic.UnitKind]int{}
	var summary semantic.Unit
	for _, unit := range compiled.Units {
		counts[unit.Type]++
		if unit.Type == semantic.UnitSummary {
			summary = unit
		}
	}
	if counts[semantic.UnitFact] != 1 || counts[semantic.UnitConcept] < 1 ||
		counts[semantic.UnitTopic] < 1 || counts[semantic.UnitSummary] != 2 {
		t.Fatalf("unexpected compiled units: %+v", counts)
	}
	sources, err := service.ResolveSemanticUnitEvidence(ctx, summary.ID)
	if err != nil {
		t.Fatalf("ResolveSemanticUnitEvidence: %v", err)
	}
	if len(sources) == 0 {
		t.Fatal("compiled summary has no exact evidence")
	}
	for _, source := range sources {
		if source.NodeID == "" || source.DocumentID == "" {
			t.Fatalf("non-exact semantic provenance: %+v", source)
		}
	}

	memory, err := service.SearchSemanticMemory(ctx, base.ID, semantic.SearchOptions{Query: "vector retrieval", TopK: 5})
	if err != nil {
		t.Fatalf("SearchSemanticMemory: %v", err)
	}
	memoryKinds := map[semantic.UnitKind]bool{}
	for _, hit := range memory.Hits {
		memoryKinds[hit.Unit.Type] = true
		if len(hit.Evidence) == 0 {
			t.Fatalf("semantic hit has no exact evidence: %+v", hit)
		}
	}
	if !memoryKinds[semantic.UnitConcept] || !memoryKinds[semantic.UnitTopic] || !memoryKinds[semantic.UnitSummary] {
		t.Fatalf("semantic memory kinds = %+v", memoryKinds)
	}

	after, err := service.Search(ctx, SearchRequest{BaseID: base.ID, Query: "1024-dimensional vectors", TopK: 2})
	if err != nil || len(after.Hits) == 0 {
		t.Fatalf("0.3 search after compilation: hits=%d err=%v", len(after.Hits), err)
	}
	unchanged, err := service.CompileSemanticMemory(ctx, base.ID)
	if err != nil {
		t.Fatalf("no-op CompileSemanticMemory: %v", err)
	}
	if unchanged.Generation != compiled.Generation {
		t.Fatalf("unchanged compilation generation = %d, want %d", unchanged.Generation, compiled.Generation)
	}

	third, err := service.AddTextDocument(ctx, base.ID, "Incremental Vector", "# Vector Retrieval\n\nThe vector service uses 1024-dimensional vectors.")
	if err != nil {
		t.Fatal(err)
	}
	incremental, err := service.CompileSemanticMemory(ctx, base.ID)
	if err != nil {
		t.Fatalf("incremental CompileSemanticMemory: %v", err)
	}
	if incremental.Generation <= compiled.Generation {
		t.Fatalf("incremental generation did not advance: %d then %d", compiled.Generation, incremental.Generation)
	}
	summaryCount := 0
	for _, unit := range incremental.Units {
		if unit.Type == semantic.UnitSummary {
			summaryCount++
		}
	}
	if summaryCount != 3 {
		t.Fatalf("incremental summaries = %d, want 3", summaryCount)
	}

	if err := service.DeleteDocument(third.ID); err != nil {
		t.Fatal(err)
	}
	deleted, err := service.GetSemanticCompilation(ctx, base.ID)
	if err != nil {
		t.Fatalf("delete propagation left no active generation: %v", err)
	}
	if deleted.Generation <= incremental.Generation {
		t.Fatalf("delete generation did not advance: %d then %d", incremental.Generation, deleted.Generation)
	}
	for _, unit := range deleted.Units {
		if unit.Type != semantic.UnitSummary {
			continue
		}
		if unit.CanonicalKey == fmt.Sprintf("summary:doc:%s:source:%d", third.ID, third.SourceVersion) {
			t.Fatalf("deleted summary survived propagation: %+v", unit)
		}
		sources, err := service.ResolveSemanticUnitEvidence(ctx, unit.ID)
		if err != nil {
			t.Fatal(err)
		}
		for _, source := range sources {
			if source.DocumentID == third.ID {
				t.Fatalf("deleted evidence survived propagation: %+v", source)
			}
		}
	}
	noop, err := service.CompileSemanticMemory(ctx, base.ID)
	if err != nil {
		t.Fatalf("post-delete no-op compile: %v", err)
	}
	if noop.Generation != deleted.Generation {
		t.Fatalf("post-delete no-op generation = %d, want %d", noop.Generation, deleted.Generation)
	}
	active, err := service.GetSemanticCompilation(ctx, base.ID)
	if err != nil || active.Generation != deleted.Generation {
		t.Fatalf("active generation = %d, want %d: %v", active.Generation, deleted.Generation, err)
	}
}

type semanticMemoryTestService struct {
	*Service
	close func()
}

func newSemanticMemoryTestService(t *testing.T) *semanticMemoryTestService {
	t.Helper()
	home := t.TempDir()
	db, err := storage.Open(filepath.Join(home, "knowledge.db"))
	if err != nil {
		t.Fatal(err)
	}
	raw, err := storage.NewRawFileStore(filepath.Join(home, "raw"))
	if err != nil {
		t.Fatal(err)
	}
	cfg := config.Defaults()
	cfg.Embedding.Provider = "none"
	return &semanticMemoryTestService{
		Service: NewService(db, raw, cfg),
		close:   func() { _ = db.Close() },
	}
}
