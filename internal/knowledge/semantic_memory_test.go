package knowledge

import (
	"context"
	"errors"
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

	after, err := service.Search(ctx, SearchRequest{BaseID: base.ID, Query: "1024-dimensional vectors", TopK: 2})
	if err != nil || len(after.Hits) == 0 {
		t.Fatalf("0.3 search after compilation: hits=%d err=%v", len(after.Hits), err)
	}
	second, err := service.CompileSemanticMemory(ctx, base.ID)
	if err != nil {
		t.Fatalf("second CompileSemanticMemory: %v", err)
	}
	if second.Generation <= compiled.Generation {
		t.Fatalf("generation did not advance: %d then %d", compiled.Generation, second.Generation)
	}
	active, err := service.GetSemanticCompilation(ctx, base.ID)
	if err != nil || active.Generation != second.Generation {
		t.Fatalf("active generation = %d, want %d: %v", active.Generation, second.Generation, err)
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
