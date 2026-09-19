package knowledge

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
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

	contextPackage, err := service.CompileKnowledgeContext(ctx, base.ID, "vector retrieval", 2048)
	if err != nil {
		t.Fatalf("CompileKnowledgeContext: %v", err)
	}
	if len(contextPackage.KnowledgeSummary) == 0 || len(contextPackage.Concepts) == 0 ||
		len(contextPackage.Facts) == 0 || len(contextPackage.Evidence) == 0 || len(contextPackage.Citations) == 0 {
		t.Fatalf("context package lacks semantic/evidence sections: %+v", contextPackage)
	}
	if contextPackage.Routing == nil || contextPackage.Routing.Intent != semantic.IntentFact {
		t.Fatalf("context routing = %+v", contextPackage.Routing)
	}
	globalPlan := service.PlanKnowledgeQuery("总结整个知识库")
	if globalPlan.Intent != semantic.IntentGlobal || !globalPlan.UseHierarchicalSummary {
		t.Fatalf("global plan = %+v", globalPlan)
	}
	if contextPackage.EstimatedTokens > contextPackage.TokenBudget {
		t.Fatalf("context package tokens = %d, budget %d", contextPackage.EstimatedTokens, contextPackage.TokenBudget)
	}

	wiki, err := service.GetSemanticWiki(ctx, base.ID)
	if err != nil {
		t.Fatalf("GetSemanticWiki: %v", err)
	}
	if !wiki.Regenerable || len(wiki.Pages) == 0 || len(wiki.Pages[0].Evidence) == 0 {
		t.Fatalf("wiki view = %+v", wiki)
	}
	if !strings.Contains(wiki.Pages[0].RenderMarkdown(), "Generated Wiki view") {
		t.Fatal("wiki markdown lacks generated-view boundary")
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
	return newSemanticMemoryTestServiceAt(t, t.TempDir())
}

func newSemanticMemoryTestServiceAt(t *testing.T, home string) *semanticMemoryTestService {
	t.Helper()
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

func TestSemanticCompilationRecoversPendingChangeAfterRestart(t *testing.T) {
	home := t.TempDir()
	ctx := context.Background()

	first := newSemanticMemoryTestServiceAt(t, home)
	base, err := first.CreateBase("Semantic Restart", "", "", BaseConfig{})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := first.AddTextDocument(ctx, base.ID, "Vector Guide",
		"# Vector Retrieval\n\nThe vector service uses 1024-dimensional vectors."); err != nil {
		t.Fatal(err)
	}
	first.close()

	second := newSemanticMemoryTestServiceAt(t, home)
	initial, err := second.CompileSemanticMemory(ctx, base.ID)
	if err != nil {
		t.Fatal(err)
	}
	if initial.Generation != 1 {
		t.Fatalf("initial generation = %d, want 1", initial.Generation)
	}
	if _, err := second.AddTextDocument(ctx, base.ID, "Vector Update",
		"# Vector Retrieval\n\nThe vector service uses 1024-dimensional vectors and reranking."); err != nil {
		t.Fatal(err)
	}
	second.close()

	third := newSemanticMemoryTestServiceAt(t, home)
	recovered, err := third.CompileSemanticMemory(ctx, base.ID)
	if err != nil {
		t.Fatalf("restart compilation: %v", err)
	}
	if recovered.Generation != 2 {
		t.Fatalf("recovered generation = %d, want 2", recovered.Generation)
	}
	summaries := 0
	for _, unit := range recovered.Units {
		if unit.Type == semantic.UnitSummary {
			summaries++
		}
		sources, err := third.ResolveSemanticUnitEvidence(ctx, unit.ID)
		if err != nil {
			t.Fatalf("restart provenance %s: %v", unit.ID, err)
		}
		if len(sources) == 0 {
			t.Fatalf("restart unit lacks evidence: %+v", unit)
		}
	}
	if summaries != 2 {
		t.Fatalf("restart summaries = %d, want 2", summaries)
	}
	third.close()
}

func TestSemanticMigrationUpgradesActive0_3DatabaseWithoutEvidenceRebuild(t *testing.T) {
	home := t.TempDir()
	ctx := context.Background()
	first := newSemanticMemoryTestServiceAt(t, home)
	base, err := first.CreateBase("Semantic Upgrade", "", "", BaseConfig{})
	if err != nil {
		t.Fatal(err)
	}
	document, err := first.AddTextDocument(ctx, base.ID, "Vector Migration",
		"# Vector Retrieval\n\nThe vector service uses 1024-dimensional vectors.")
	if err != nil {
		t.Fatal(err)
	}
	before, err := first.Search(ctx, SearchRequest{BaseID: base.ID, Query: "1024-dimensional vectors", TopK: 2})
	if err != nil || len(before.Hits) == 0 {
		t.Fatalf("0.3 search before downgrade: hits=%d err=%v", len(before.Hits), err)
	}
	first.close()

	legacy, err := storage.Open(filepath.Join(home, "knowledge.db"))
	if err != nil {
		t.Fatal(err)
	}
	for _, statement := range []string{
		`DROP TABLE knowledge_relation_sources`,
		`DROP TABLE knowledge_relations`,
		`DROP TABLE knowledge_unit_derived_from`,
		`DROP TABLE knowledge_unit_sources`,
		`DROP TABLE knowledge_units`,
		`DROP TABLE knowledge_compilations`,
		`DROP TABLE knowledge_compilation_queue`,
		`DELETE FROM schema_migrations WHERE version IN (19, 20)`,
	} {
		if _, err := legacy.Exec(statement); err != nil {
			t.Fatalf("simulate 0.3 schema (%s): %v", statement, err)
		}
	}
	if err := legacy.Close(); err != nil {
		t.Fatal(err)
	}

	upgraded, err := storage.Open(filepath.Join(home, "knowledge.db"))
	if err != nil {
		t.Fatalf("apply semantic migrations: %v", err)
	}
	version, err := storage.SchemaVersion(upgraded.DB)
	if err != nil {
		t.Fatal(err)
	}
	if version < 20 {
		t.Fatalf("upgraded schema version = %d, want at least 20", version)
	}
	if err := upgraded.Close(); err != nil {
		t.Fatal(err)
	}
	second := newSemanticMemoryTestServiceAt(t, home)
	after, err := second.Search(ctx, SearchRequest{BaseID: base.ID, Query: "1024-dimensional vectors", TopK: 2})
	if err != nil || len(after.Hits) == 0 {
		t.Fatalf("0.3 search after upgrade: hits=%d err=%v", len(after.Hits), err)
	}
	if after.Hits[0].IndexGeneration != before.Hits[0].IndexGeneration || after.Hits[0].ChunkID != before.Hits[0].ChunkID {
		t.Fatalf("upgrade rebuilt evidence: before=%+v after=%+v", before.Hits[0], after.Hits[0])
	}
	compiled, err := second.CompileSemanticMemory(ctx, base.ID)
	if err != nil {
		t.Fatalf("compile upgraded 0.3 KB: %v", err)
	}
	found := false
	for _, unit := range compiled.Units {
		for _, source := range unit.Sources {
			if source.DocumentID == document.ID {
				found = true
			}
		}
	}
	if !found {
		t.Fatal("upgraded compilation omitted legacy document provenance")
	}
	second.close()
}
func TestCompileKnowledgeContextBuildsAmbiguousHistoryRange(t *testing.T) {
	service := newSemanticMemoryTestService(t)
	defer service.close()
	ctx := context.Background()
	base, err := service.CreateBase("History Range", "", "", BaseConfig{})
	if err != nil {
		t.Fatal(err)
	}
	for index := 0; index < 12; index++ {
		version := fmt.Sprintf("0.%d", index)
		title := fmt.Sprintf("docs/release-v%s.md", version)
		content := fmt.Sprintf("# Release %s\n\nRelease %s added Open5GS feature %d.", version, version, index)
		if _, err := service.AddTextDocument(ctx, base.ID, title, content); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := service.CompileSemanticMemory(ctx, base.ID); err != nil {
		t.Fatal(err)
	}
	pkg, err := service.CompileKnowledgeContext(ctx, base.ID, "What happened in earlier Open5GS release stages?", 2048)
	if err != nil {
		t.Fatal(err)
	}
	if pkg.TemporalIntent != string(semantic.TemporalRangeHistory) {
		t.Fatalf("temporal intent=%q", pkg.TemporalIntent)
	}
	if len(pkg.Evidence) < 6 {
		t.Fatalf("history evidence=%d: %+v", len(pkg.Evidence), pkg.Evidence)
	}
	seen := map[string]bool{}
	for _, evidence := range pkg.Evidence {
		if evidence.TemporalStatus != "history" && evidence.TemporalStatus != "" {
			continue
		}
		if evidence.Version != "" {
			seen[evidence.Version] = true
		}
	}
	if len(seen) < 6 {
		t.Fatalf("history versions=%v evidence=%+v", seen, pkg.Evidence)
	}
}
