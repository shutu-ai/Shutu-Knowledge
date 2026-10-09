package agentquery

import (
	"context"
	"errors"
	"path/filepath"
	"strconv"
	"testing"

	"github.com/shutu-ai/shutu-knowledge/internal/config"
	"github.com/shutu-ai/shutu-knowledge/internal/knowledge"
	"github.com/shutu-ai/shutu-knowledge/internal/semantic"
	"github.com/shutu-ai/shutu-knowledge/internal/storage"
	"strings"
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
	if _, err := service.AddTextDocument(context.Background(), base.ID, "KPI overview",
		"# KPI overview\n\nThe KPI is calculated from the SDR counter and refreshed every fifteen minutes."); err != nil {
		t.Fatalf("add text: %v", err)
	}
	if _, err := service.AddTextDocument(context.Background(), base.ID, "Release v0.2",
		"# Release v0.2\n\nVersion 0.2 supports the 128k context window."); err != nil {
		t.Fatalf("add old release: %v", err)
	}
	if _, err := service.AddTextDocument(context.Background(), base.ID, "Release v0.3",
		"# Release v0.3\n\nVersion 0.3 supports the 1M context window."); err != nil {
		t.Fatalf("add new release: %v", err)
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

func TestAgentQueryStableErrorCodes(t *testing.T) {
	ctx := context.Background()
	service, base := newQueryFixture(t)

	_, err := service.Search(ctx, Request{Query: "", BaseIDs: []string{base.ID}})
	var invalid *Error
	if !errors.As(err, &invalid) || invalid.Code != ErrInvalidArgument {
		t.Fatalf("empty query error = %v", err)
	}

	_, err = service.Search(ctx, Request{Query: "KPI", BaseIDs: []string{"missing"}})
	var missing *Error
	if !errors.As(err, &missing) || missing.Code != ErrBaseNotFound || missing.BaseID != "missing" {
		t.Fatalf("unknown base error = %v", err)
	}

	_, err = service.Search(ctx, Request{
		Query: "KPI", BaseIDs: []string{base.ID},
		VersionPolicy: VersionPolicy{Type: VersionAsOf},
	})
	var version *Error
	if !errors.As(err, &version) || version.Code != ErrInvalidArgument {
		t.Fatalf("version policy error = %v", err)
	}
}

func TestAgentQueryVersionPolicyFiltersSemanticFacts(t *testing.T) {
	ctx := context.Background()
	service, base := newQueryFixture(t)
	if _, err := service.knowledge.CompileSemanticMemory(ctx, base.ID); err != nil {
		t.Fatalf("compile semantic memory: %v", err)
	}
	factKinds := []semantic.UnitKind{semantic.UnitFact}

	current, err := service.Search(ctx, Request{
		Query: "context window", BaseIDs: []string{base.ID}, Mode: ModeSemantic,
		SemanticKinds: factKinds, TopK: 10,
	})
	if err != nil {
		t.Fatalf("current semantic search: %v", err)
	}
	if containsFactText(current, "128k") || !containsFactText(current, "1M") {
		t.Fatalf("current policy facts: %+v", current.Semantic)
	}

	old, err := service.Search(ctx, Request{
		Query: "context window", BaseIDs: []string{base.ID}, Mode: ModeSemantic,
		SemanticKinds: factKinds, TopK: 10,
		VersionPolicy: VersionPolicy{Type: VersionAsOf, AsOf: "0.2"},
	})
	if err != nil {
		t.Fatalf("as-of semantic search: %v", err)
	}
	if !containsFactText(old, "128k") || containsFactText(old, "1M") {
		t.Fatalf("as-of policy facts: %+v", old.Semantic)
	}

	all, err := service.Search(ctx, Request{
		Query: "context window", BaseIDs: []string{base.ID}, Mode: ModeSemantic,
		SemanticKinds: factKinds, TopK: 10,
		VersionPolicy: VersionPolicy{Type: VersionAll},
	})
	if err != nil {
		t.Fatalf("all semantic search: %v", err)
	}
	if !containsFactText(all, "128k") || !containsFactText(all, "1M") {
		t.Fatalf("all policy facts: %+v", all.Semantic)
	}

	ranged, err := service.Search(ctx, Request{
		Query: "context window", BaseIDs: []string{base.ID}, Mode: ModeSemantic,
		SemanticKinds: factKinds, TopK: 10,
		VersionPolicy: VersionPolicy{Type: VersionRange, FromVersion: "0.2", ToVersion: "0.3"},
	})
	if err != nil {
		t.Fatalf("range semantic search: %v", err)
	}
	if !containsFactText(ranged, "128k") || !containsFactText(ranged, "1M") {
		t.Fatalf("range policy facts: %+v", ranged.Semantic)
	}
}

func TestAgentQueryContextHonorsVersionPolicy(t *testing.T) {
	ctx := context.Background()
	service, base := newQueryFixture(t)
	if _, err := service.knowledge.CompileSemanticMemory(ctx, base.ID); err != nil {
		t.Fatalf("compile semantic memory: %v", err)
	}
	result, err := service.Context(ctx, ContextRequest{
		Query: "What did the context window support?", BaseIDs: []string{base.ID},
		TokenBudget:   2048,
		VersionPolicy: VersionPolicy{Type: VersionAsOf, AsOf: "0.2"},
	})
	if err != nil {
		t.Fatalf("as-of context: %v", err)
	}
	if len(result.Packages) != 1 || result.Packages[0].Package.TemporalIntent != string(semantic.TemporalExplicitVersion) {
		t.Fatalf("as-of context result: %+v", result)
	}
	joined := strings.Join(result.Packages[0].Package.Facts, "\n")
	if !strings.Contains(joined, "128k") || strings.Contains(joined, "1M") {
		t.Fatalf("as-of context facts: %+v", result.Packages[0].Package.Facts)
	}
}

func containsFactText(result Result, needle string) bool {
	for _, base := range result.Semantic {
		for _, hit := range base.Response.Hits {
			if strings.Contains(strings.ToLower(hit.Unit.Content), strings.ToLower(needle)) {
				return true
			}
		}
	}
	return false
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
