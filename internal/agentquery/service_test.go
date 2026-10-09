package agentquery

import (
	"context"
	"path/filepath"
	"strconv"
	"testing"

	"github.com/shutu-ai/shutu-knowledge/internal/config"
	"github.com/shutu-ai/shutu-knowledge/internal/knowledge"
	"github.com/shutu-ai/shutu-knowledge/internal/storage"
)

func newQueryFixture(t *testing.T) (*Service, knowledge.Base) {
	t.Helper()
	home := t.TempDir()
	db, err := storage.Open(filepath.Join(home, "knowledge.db"))
	if err != nil {
		t.Fatalf("open storage: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	raw, err := storage.NewRawFileStore(filepath.Join(home, "raw"))
	if err != nil {
		t.Fatalf("open raw store: %v", err)
	}
	service := knowledge.NewService(db, raw, config.Defaults())
	base, err := service.CreateBase("Agent Query", "shared contract fixture", "", knowledge.BaseConfig{})
	if err != nil {
		t.Fatalf("create base: %v", err)
	}
	_, err = service.AddTextDocument(context.Background(), base.ID, "KPI overview",
		"# KPI overview\n\nThe KPI is calculated from the SDR counter and refreshed every fifteen minutes.")
	if err != nil {
		t.Fatalf("add text: %v", err)
	}
	return New(service), base
}

func TestAgentQueryEvidenceAndCapabilityContract(t *testing.T) {
	ctx := context.Background()
	service, base := newQueryFixture(t)

	result, err := service.Search(ctx, Request{
		Query: "KPI", BaseIDs: []string{base.ID}, Mode: ModeEvidence, TopK: 5,
	})
	if err != nil {
		t.Fatalf("evidence search: %v", err)
	}
	if result.Mode != ModeEvidence || len(result.Evidence.Hits) == 0 {
		t.Fatalf("evidence result: mode=%q hits=%d", result.Mode, len(result.Evidence.Hits))
	}
	if len(result.EvidenceSnapshots) != 1 || result.EvidenceSnapshots[0].BaseID != base.ID ||
		result.EvidenceSnapshots[0].IndexGeneration == 0 || result.EvidenceSnapshots[0].SourceVersion == 0 {
		t.Fatalf("evidence snapshots: %+v", result.EvidenceSnapshots)
	}

	capabilities, err := service.Capabilities(ctx)
	if err != nil {
		t.Fatalf("capabilities: %v", err)
	}
	if len(capabilities.Bases) != 1 || capabilities.Bases[0].BaseID != base.ID {
		t.Fatalf("capability bases: %+v", capabilities.Bases)
	}
	if capabilities.Bases[0].SemanticReady {
		t.Fatal("semantic unexpectedly ready before compilation")
	}

	if _, err := service.Search(ctx, Request{
		Query: "KPI", BaseIDs: []string{"missing"}, Mode: ModeEvidence,
	}); err == nil {
		t.Fatal("unknown base was accepted")
	}
}

func TestAgentQuerySemanticHybridGenerationAndContext(t *testing.T) {
	ctx := context.Background()
	service, base := newQueryFixture(t)

	missing, err := service.Search(ctx, Request{
		Query: "KPI", BaseIDs: []string{base.ID}, Mode: ModeSemantic, TopK: 5,
	})
	if err == nil {
		t.Fatalf("semantic search before compilation error = %v, want error", err)
	}
	if missing.Evidence != nil && len(missing.Evidence.Hits) != 0 {
		t.Fatalf("semantic failure leaked evidence: %+v", missing.Evidence.Hits)
	}
	if len(missing.Diagnostics) == 0 || missing.Diagnostics[0].Code != "SEMANTIC_NOT_READY" {
		t.Fatalf("missing semantic diagnostics: %+v", missing.Diagnostics)
	}

	if _, err := service.knowledge.CompileSemanticMemory(ctx, base.ID); err != nil {
		t.Fatalf("compile semantic memory: %v", err)
	}
	compilation, err := service.knowledge.GetSemanticCompilation(ctx, base.ID)
	if err != nil {
		t.Fatalf("read compilation: %v", err)
	}

	missingPinnedContext, err := service.Context(ctx, ContextRequest{
		Query: "KPI", BaseIDs: []string{base.ID}, Generation: Generation("999"),
	})
	if err != nil {
		t.Fatalf("missing pinned context: %v", err)
	}
	if len(missingPinnedContext.Packages) != 0 || !missingPinnedContext.Degraded || missingPinnedContext.Diagnostics[0].Code != "SEMANTIC_NOT_READY" {
		t.Fatalf("missing pinned context result: %+v", missingPinnedContext)
	}

	semantic, err := service.Search(ctx, Request{
		Query: "KPI", BaseIDs: []string{base.ID}, Mode: ModeSemantic, TopK: 5,
	})
	if err != nil {
		t.Fatalf("semantic search: %v", err)
	}
	if len(semantic.Semantic) != 1 || len(semantic.Semantic[0].Response.Hits) == 0 {
		t.Fatalf("semantic result: %+v", semantic.Semantic)
	}
	if semantic.Snapshots[0].SemanticGeneration != compilation.Generation {
		t.Fatalf("semantic snapshot generation = %d, want %d", semantic.Snapshots[0].SemanticGeneration, compilation.Generation)
	}

	hybrid, err := service.Search(ctx, Request{
		Query: "KPI", BaseIDs: []string{base.ID}, Mode: ModeHybrid, TopK: 5,
		IncludeStructured: true,
	})
	if err != nil {
		t.Fatalf("hybrid search: %v", err)
	}
	if len(hybrid.Evidence.Hits) == 0 || len(hybrid.Semantic) == 0 || hybrid.Degraded {
		t.Fatalf("hybrid result: evidence=%d semantic=%d degraded=%t diagnostics=%+v",
			len(hybrid.Evidence.Hits), len(hybrid.Semantic), hybrid.Degraded, hybrid.Diagnostics)
	}

	pinned, err := service.Search(ctx, Request{
		Query: "KPI", BaseIDs: []string{base.ID}, Mode: ModeSemantic,
		Generation: Generation(strconv.FormatInt(compilation.Generation, 10)), TopK: 5,
	})
	if err != nil {
		t.Fatalf("pinned semantic search: %v", err)
	}
	if pinned.Snapshots[0].SemanticSource != "pinned" || pinned.Snapshots[0].SemanticGeneration != compilation.Generation {
		t.Fatalf("pinned snapshot: %+v", pinned.Snapshots)
	}

}

func TestAgentQueryContextMissingSemanticReportsDegradation(t *testing.T) {
	ctx := context.Background()
	service, base := newQueryFixture(t)
	result, err := service.Context(ctx, ContextRequest{Query: "KPI", BaseIDs: []string{base.ID}})
	if err != nil {
		t.Fatalf("missing semantic context: %v", err)
	}
	if len(result.Packages) != 0 || !result.Degraded || len(result.Diagnostics) == 0 {
		t.Fatalf("missing semantic context result: %+v", result)
	}
	if result.Diagnostics[0].Code != "SEMANTIC_NOT_READY" {
		t.Fatalf("diagnostic code: %+v", result.Diagnostics)
	}
}

func TestAgentQueryContextAfterCompilation(t *testing.T) {
	ctx := context.Background()
	service, base := newQueryFixture(t)
	if _, err := service.knowledge.CompileSemanticMemory(ctx, base.ID); err != nil {
		t.Fatalf("compile semantic memory: %v", err)
	}
	compilation, err := service.knowledge.GetSemanticCompilation(ctx, base.ID)
	if err != nil {
		t.Fatalf("read compilation: %v", err)
	}
	result, err := service.Context(ctx, ContextRequest{
		Query: "How is the KPI calculated?", BaseIDs: []string{base.ID}, TokenBudget: 2048,
	})
	if err != nil {
		t.Fatalf("context: %v", err)
	}
	if len(result.Packages) != 1 || result.Packages[0].Package.Query == "" {
		t.Fatalf("context packages: %+v", result.Packages)
	}

	pinned, err := service.Context(ctx, ContextRequest{
		Query: "How is the KPI calculated?", BaseIDs: []string{base.ID},
		Generation: Generation(strconv.FormatInt(compilation.Generation, 10)), TokenBudget: 2048,
	})
	if err != nil {
		t.Fatalf("pinned context: %v", err)
	}
	if len(pinned.Packages) != 1 || pinned.Packages[0].Generation != compilation.Generation || pinned.Degraded {
		t.Fatalf("pinned context result: %+v", pinned)
	}
}
