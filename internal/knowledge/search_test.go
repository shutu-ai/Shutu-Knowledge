package knowledge

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/shutu-ai/shutu-knowledge/internal/evidence"
)

// fakeEmbedder maps topic keywords to stable vectors; callCount and dim
// sequence support reuse/dimension tests.
type fakeEmbedder struct {
	model   string
	dimSeq  []int
	call    int
	embeds  int
	failNth int
}

func (f *fakeEmbedder) Embed(_ context.Context, texts []string) ([][]float64, error) {
	f.call++
	if f.failNth > 0 && f.call == f.failNth {
		return nil, errors.New("provider down")
	}
	dim := 2
	if len(f.dimSeq) > 0 {
		dim = f.dimSeq[minInt(f.call-1, len(f.dimSeq)-1)]
	}
	out := make([][]float64, 0, len(texts))
	for _, text := range texts {
		f.embeds++
		vector := make([]float64, dim)
		switch {
		case strings.Contains(text, "database"):
			vector[0] = 1
		case strings.Contains(text, "cooking"):
			vector[1] = 1
		default:
			if dim > 2 {
				vector[2] = 1
			} else {
				vector[0] = 0.7
				vector[1] = 0.7
			}
		}
		out = append(out, vector)
	}
	return out, nil
}

func (f *fakeEmbedder) ModelKey() string { return "fake:" + f.model }

type fakeReranker struct {
	scores []float64
	err    error
	calls  int
}

func (f *fakeReranker) Rerank(_ context.Context, _ string, texts []string) ([]float64, error) {
	f.calls++
	if f.err != nil {
		return nil, f.err
	}
	if len(f.scores) != len(texts) {
		return nil, fmt.Errorf("fake scores length %d != %d", len(f.scores), len(texts))
	}
	return f.scores, nil
}

func (f *fakeReranker) ModelKey() string { return "rerank:fake" }

func newSearchFixture(t *testing.T) (*Service, *fakeEmbedder) {
	t.Helper()
	f := newFixture(t)
	// Activate the vector phase: the guard reads the config provider, while
	// the concrete provider is the test fake.
	f.service.global.Embedding.Provider = "openai"
	embedder := &fakeEmbedder{model: "a"}
	f.service.SetProviders(embedder, nil)
	return f.service, embedder
}

func TestHybridSearchRanksAndExplains(t *testing.T) {
	service, _ := newSearchFixture(t)
	base, err := service.CreateBase("Tech", "", "", BaseConfig{})
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if _, err := service.AddTextDocument(ctx, base.ID, "Database Guide", "# Storage\n\nThe database engine keeps rows on disk."); err != nil {
		t.Fatal(err)
	}
	if _, err := service.AddTextDocument(ctx, base.ID, "Cooking Notes", "# Kitchen\n\nSimmer the sauce for cooking pasta."); err != nil {
		t.Fatal(err)
	}
	result, err := service.Search(ctx, SearchRequest{Query: "database", TopK: 3})
	if err != nil {
		t.Fatal(err)
	}
	if result.Mode != "hybrid" || result.Total == 0 {
		t.Fatalf("mode/total: %+v", result)
	}
	top := result.Hits[0]
	if top.DocumentTitle != "Database Guide" {
		t.Fatalf("top hit: %+v", top)
	}
	if top.VectorScore <= 0 || top.LexicalScore <= 0 {
		t.Fatalf("lane scores missing: %+v", top)
	}
	window := top.ContextWindow
	if window == nil || !strings.Contains(evidence.Serialize(*window), ">>>") {
		t.Fatalf("context window missing anchor: %+v", window)
	}
	if result.ElapsedMS < 0 || result.Reranked {
		t.Fatalf("unexpected result flags: %+v", result)
	}
}

func TestSearchFailClosed(t *testing.T) {
	service, _ := newSearchFixture(t)
	base, _ := service.CreateBase("B", "", "", BaseConfig{})
	ctx := context.Background()
	if _, err := service.AddTextDocument(ctx, base.ID, "Doc", "database content here"); err != nil {
		t.Fatal(err)
	}
	// Explicitly empty base scope matches nothing.
	result, err := service.Search(ctx, SearchRequest{Query: "database", BaseIDs: []string{}})
	if err != nil || result.Total != 0 {
		t.Fatalf("empty scope: %v %+v", err, result)
	}
	// Disabled invocation matches nothing.
	if err := service.SetEnabledScope(boolPtr(false), nil); err != nil {
		t.Fatal(err)
	}
	result, err = service.Search(ctx, SearchRequest{Query: "database"})
	if err != nil || result.Total != 0 {
		t.Fatalf("disabled scope: %v %+v", err, result)
	}
	// Empty query matches nothing.
	result, err = service.Search(ctx, SearchRequest{Query: "   "})
	if err != nil || result.Total != 0 {
		t.Fatalf("empty query: %v %+v", err, result)
	}
}

func TestSearchVectorThreshold(t *testing.T) {
	service, _ := newSearchFixture(t)
	base, _ := service.CreateBase("B", "", "", BaseConfig{})
	ctx := context.Background()
	if _, err := service.AddTextDocument(ctx, base.ID, "Database Guide", "database storage rows"); err != nil {
		t.Fatal(err)
	}
	// Orthogonal query vectors score 0 and are filtered by a threshold.
	result, err := service.Search(ctx, SearchRequest{Query: "database", Mode: "vector", Threshold: 0.5})
	if err != nil {
		t.Fatal(err)
	}
	if result.Total != 1 {
		t.Fatalf("relevant hit should survive: %+v", result)
	}
	result, err = service.Search(ctx, SearchRequest{Query: "cooking", Mode: "vector", Threshold: 0.5})
	if err != nil || result.Total != 0 {
		t.Fatalf("orthogonal hits should be filtered: %v %+v", err, result)
	}
}

func TestRerankerAppliedAndDegraded(t *testing.T) {
	service, _ := newSearchFixture(t)
	base, _ := service.CreateBase("B", "", "", BaseConfig{})
	ctx := context.Background()
	if _, err := service.AddTextDocument(ctx, base.ID, "Guide", "database intro\n\nbeta details about the database engine internals"); err != nil {
		t.Fatal(err)
	}
	// Applied: reranker promotes the second chunk.
	fake := &fakeReranker{scores: []float64{0.1, 0.9}}
	service.SetProviders(nil, fake)
	result, err := service.Search(ctx, SearchRequest{Query: "database", TopK: 2})
	if err != nil {
		t.Fatal(err)
	}
	if !result.Reranked || result.Rerank == nil || result.Rerank.Status != "applied" {
		t.Fatalf("rerank status: %+v", result.Rerank)
	}
	if len(result.Hits) != 2 || result.Hits[0].RerankScore != 0.9 {
		t.Fatalf("rerank order: %+v", result.Hits)
	}
	// Degraded: failures keep the fused order and report status.
	failing := &fakeReranker{err: errors.New("boom")}
	service.SetProviders(nil, failing)
	result, err = service.Search(ctx, SearchRequest{Query: "database", TopK: 2})
	if err != nil {
		t.Fatal(err)
	}
	if result.Reranked || result.Rerank.Status != "degraded" || result.Total == 0 {
		t.Fatalf("degraded rerank: %+v", result)
	}
}

func TestIngestEmbeddingDimensionMismatchDegrades(t *testing.T) {
	service, _ := newSearchFixture(t)
	// Two chunks, batch size 1, dims 2 then 3 -> second batch mismatches.
	service.global.Embedding.Batch = 1
	service.SetProviders(&fakeEmbedder{model: "a", dimSeq: []int{2, 3}}, nil)
	base, _ := service.CreateBase("B", "", "", BaseConfig{})
	doc, err := service.AddTextDocument(context.Background(), base.ID, "Guide", "database part one\n\npart two text")
	if err != nil {
		t.Fatal(err)
	}
	// Degradation records the error code; the import itself succeeded.
	if doc.Status != StatusReady || doc.ErrorCode != ErrDimensionMismatch {
		t.Fatalf("doc should degrade to lexical-ready: %+v", doc)
	}
	chunks, err := service.ListChunks(doc.ID, 0, 0)
	if err != nil || len(chunks) == 0 {
		t.Fatalf("chunks must survive: %v", err)
	}
}

func TestEmbeddingFailureDegrades(t *testing.T) {
	service, _ := newSearchFixture(t)
	provider := &fakeEmbedder{model: "a", failNth: 1}
	service.SetProviders(provider, nil)
	base, _ := service.CreateBase("B", "", "", BaseConfig{})
	doc, err := service.AddTextDocument(context.Background(), base.ID, "Guide", "database body")
	if err != nil {
		t.Fatal(err)
	}
	if doc.Status != StatusReady || doc.ErrorCode != ErrEmbeddingProvider {
		t.Fatalf("lexical degradation: %+v", doc)
	}
	// Search still works on the lexical lane.
	result, err := service.Search(context.Background(), SearchRequest{Query: "database"})
	if err != nil || result.Total == 0 {
		t.Fatalf("lexical search after degradation: %v %+v", err, result)
	}
}

func TestVectorHashReuseOnReindex(t *testing.T) {
	service, embedder := newSearchFixture(t)
	base, _ := service.CreateBase("B", "", "", BaseConfig{})
	ctx := context.Background()
	if _, err := service.AddTextDocument(ctx, base.ID, "Guide", "database content"); err != nil {
		t.Fatal(err)
	}
	before := embedder.embeds
	if _, err := service.ReindexDocument(ctx, firstDocID(service, base.ID)); err != nil {
		t.Fatal(err)
	}
	if embedder.embeds != before {
		t.Fatalf("expected full hash reuse, embeds went %d -> %d", before, embedder.embeds)
	}
}

func TestMultiQueryFusionBroadensRecall(t *testing.T) {
	service, _ := newSearchFixture(t)
	base, _ := service.CreateBase("B", "", "", BaseConfig{})
	ctx := context.Background()
	if _, err := service.AddTextDocument(ctx, base.ID, "Database Guide", "database engine rows"); err != nil {
		t.Fatal(err)
	}
	if _, err := service.AddTextDocument(ctx, base.ID, "Cooking Notes", "cooking pasta sauce"); err != nil {
		t.Fatal(err)
	}
	// The primary query targets the database doc; the extra targets cooking.
	result, err := service.Search(ctx, SearchRequest{Query: "database", Queries: []string{"cooking"}, TopK: 5})
	if err != nil {
		t.Fatal(err)
	}
	titles := map[string]bool{}
	for _, hit := range result.Hits {
		titles[hit.DocumentTitle] = true
	}
	if !titles["Database Guide"] || !titles["Cooking Notes"] {
		t.Fatalf("multi-query recall too narrow: %+v", titles)
	}
}

func TestStatsDetectStaleEmbeddings(t *testing.T) {
	service, embedder := newSearchFixture(t)
	base, _ := service.CreateBase("B", "", "", BaseConfig{})
	if _, err := service.AddTextDocument(context.Background(), base.ID, "Guide", "database rows"); err != nil {
		t.Fatal(err)
	}
	// Swap the vector space: stored vectors now count as stale.
	service.SetProviders(&fakeEmbedder{model: "b"}, nil)
	_ = embedder
	stats, err := service.Stats(base.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !stats.Embedded || stats.StaleChunks == 0 || stats.Dimensions != 2 {
		t.Fatalf("stale detection: %+v", stats)
	}
}

func firstDocID(service *Service, baseID string) string {
	docs, _ := service.ListDocuments(baseID)
	if len(docs) == 0 {
		return ""
	}
	return docs[0].ID
}

func boolPtr(v bool) *bool { return &v }
