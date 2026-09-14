package knowledge

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/shutu-ai/shutu-knowledge/internal/embedding"
	"github.com/shutu-ai/shutu-knowledge/internal/evidence"
	"github.com/shutu-ai/shutu-knowledge/internal/rerank"
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

type blockingEmbedder struct {
	model   string
	release chan struct{}
}

type gatedEmbedder struct {
	model   string
	started chan struct{}
	release chan struct{}
}

func (g *gatedEmbedder) Embed(_ context.Context, texts []string) ([][]float64, error) {
	close(g.started)
	<-g.release
	out := make([][]float64, 0, len(texts))
	for _, text := range texts {
		vector := make([]float64, 2)
		if strings.Contains(text, "database") {
			vector[0] = 1
		} else {
			vector[1] = 1
		}
		out = append(out, vector)
	}
	return out, nil
}

func (g *gatedEmbedder) ModelKey() string { return "fake:" + g.model }

func (b *blockingEmbedder) Embed(ctx context.Context, _ []string) ([][]float64, error) {
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-b.release:
		return nil, errors.New("released by test")
	}
}

func (b *blockingEmbedder) ModelKey() string { return "blocking:" + b.model }

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

func TestVectorSearchDoesNotMixSameDimensionModels(t *testing.T) {
	service, _ := newSearchFixture(t)
	base, err := service.CreateBase("Model Spaces", "", "", BaseConfig{})
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()

	// Both models use two dimensions on purpose: a length check alone must
	// not be mistaken for vector-space isolation.
	modelA := &fakeEmbedder{model: "a"}
	service.SetProviders(modelA, nil)
	docA, err := service.AddTextDocument(ctx, base.ID, "Model A Guide", "# Storage\n\nThe database keeps rows on disk")
	if err != nil {
		t.Fatal(err)
	}
	modelB := &fakeEmbedder{model: "b"}
	service.SetProviders(modelB, nil)
	docB, err := service.AddTextDocument(ctx, base.ID, "Model B Guide", "# Storage\n\nThe database keeps rows in another space")
	if err != nil {
		t.Fatal(err)
	}

	result, err := service.Search(ctx, SearchRequest{Query: "database", Mode: "vector", TopK: 5})
	if err != nil {
		t.Fatal(err)
	}
	if result.Total != 1 || len(result.Hits) != 1 {
		t.Fatalf("model-isolated search returned %d hits: %+v", result.Total, result)
	}
	if result.Hits[0].DocID != docB.ID || result.Hits[0].DocID == docA.ID {
		t.Fatalf("mixed model spaces: got %s, want %s", result.Hits[0].DocID, docB.ID)
	}
}

func TestModelSwitchFailureKeepsVectorSpaceAndDegrades(t *testing.T) {
	service, _ := newSearchFixture(t)
	base, err := service.CreateBase("Model Switch", "", "", BaseConfig{})
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	imported, err := service.AddTextDocument(ctx, base.ID, "Guide", "# Storage\n\nThe database keeps rows on disk\n\nThe engine uses transactions")
	if err != nil {
		t.Fatal(err)
	}
	activeA, err := service.store.getDocument(imported.ID)
	if err != nil {
		t.Fatal(err)
	}
	if activeA.EmbeddingModel != "fake:a" || activeA.ActiveIndexGen != 1 {
		t.Fatalf("model A baseline: %+v", activeA)
	}

	// Same dimensions on purpose: model identity, not width, is the fence.
	service.SetProviders(&fakeEmbedder{model: "b"}, nil)
	if _, err := service.ReindexDocument(ctx, imported.ID); err != nil {
		t.Fatal(err)
	}
	activeB, err := service.store.getDocument(imported.ID)
	if err != nil {
		t.Fatal(err)
	}
	if activeB.EmbeddingModel != "fake:b" || activeB.ActiveIndexGen != 2 ||
		activeB.SourceVersion != activeA.SourceVersion+1 {
		t.Fatalf("model B migration: %+v", activeB)
	}
	result, err := service.Search(ctx, SearchRequest{
		Query: "database", Mode: "vector", BaseIDs: []string{base.ID},
	})
	if err != nil || result.Total != 2 || len(result.Hits) != 2 {
		t.Fatalf("model B search: %v %+v", err, result)
	}
	for _, hit := range result.Hits {
		if hit.DocID != imported.ID || hit.IndexGeneration != 2 {
			t.Fatalf("model B search mixed evidence: %+v", hit)
		}
	}
	var bVectors int
	if err := service.store.db.QueryRow(`SELECT COUNT(*) FROM chunks
		WHERE doc_id = ? AND index_generation = ? AND embedding_model = ?`,
		imported.ID, activeB.ActiveIndexGen, "fake:b").Scan(&bVectors); err != nil {
		t.Fatal(err)
	}
	if bVectors == 0 {
		t.Fatal("model B migration produced no vectors")
	}

	// A failed model C migration must not mutate the committed B index. The
	// existing document remains the authoritative lexical fallback.
	service.SetProviders(&fakeEmbedder{model: "c", failNth: 1}, nil)
	if _, err := service.ReindexDocument(ctx, imported.ID); err != nil {
		t.Fatal(err)
	}
	afterFailure, err := service.store.getDocument(imported.ID)
	if err != nil {
		t.Fatal(err)
	}
	if afterFailure.ActiveIndexGen != activeB.ActiveIndexGen ||
		afterFailure.SourceVersion != activeB.SourceVersion ||
		afterFailure.EmbeddingModel != "fake:b" ||
		afterFailure.ErrorCode != ErrEmbeddingProvider {
		t.Fatalf("failed migration mutated published state: %+v", afterFailure)
	}
	result, err = service.Search(ctx, SearchRequest{
		Query: "database", Mode: "vector", BaseIDs: []string{base.ID},
	})
	if err != nil || result.Mode != "lexical" || result.Total != 1 {
		t.Fatalf("old index did not provide lexical fallback: %v %+v", err, result)
	}
	for _, hit := range result.Hits {
		if hit.DocID != imported.ID || hit.IndexGeneration != activeB.ActiveIndexGen {
			t.Fatalf("old-index fallback mixed evidence: %+v", hit)
		}
	}
	var cVectors int
	if err := service.store.db.QueryRow(`SELECT COUNT(*) FROM chunks
		WHERE doc_id = ? AND embedding_model = ?`, imported.ID, "fake:c").Scan(&cVectors); err != nil {
		t.Fatal(err)
	}
	if cVectors != 0 {
		t.Fatalf("failed model C migration left vectors: %d", cVectors)
	}

	// Disabled embedding is an explicit new lexical generation, never reuse
	// or reinterpret either model's vectors.
	service.global.Embedding.Provider = "none"
	service.SetProviders(embedding.New(embedding.Config{Provider: "none"}), nil)
	result, err = service.Search(ctx, SearchRequest{
		Query: "database", Mode: "vector", BaseIDs: []string{base.ID},
	})
	if err != nil || result.Mode != "lexical" || result.Total != 1 {
		t.Fatalf("disabled embedding did not degrade to lexical: %v %+v", err, result)
	}
	if _, err := service.ReindexDocument(ctx, imported.ID); err != nil {
		t.Fatal(err)
	}
	disabled, err := service.store.getDocument(imported.ID)
	if err != nil {
		t.Fatal(err)
	}
	if disabled.EmbeddingReady || disabled.EmbeddingModel != "" || disabled.ErrorCode != "" {
		t.Fatalf("disabled embedding marked vectors ready: %+v", disabled)
	}
	var activeVectors int
	if err := service.store.db.QueryRow(`SELECT COUNT(*) FROM chunks c
		JOIN documents d ON d.id = c.doc_id
		WHERE c.doc_id = ? AND c.index_generation = d.active_index_generation
		  AND c.embedding IS NOT NULL`, imported.ID).Scan(&activeVectors); err != nil {
		t.Fatal(err)
	}
	if activeVectors != 0 {
		t.Fatalf("disabled lexical generation retained vectors: %d", activeVectors)
	}
}

func TestSearchModelWaitIsBounded(t *testing.T) {
	service, _ := newSearchFixture(t)
	base, err := service.CreateBase("Scheduler", "", "", BaseConfig{})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.AddTextDocument(context.Background(), base.ID, "Doc", "database content"); err != nil {
		t.Fatal(err)
	}
	service.global.Scheduler.ModelWaitMS = 100

	// Occupy the interactive model lane, as a slow embedding or rerank would.
	release := make(chan struct{})
	service.searchModelSlots <- struct{}{}
	t.Cleanup(func() { close(release); <-service.searchModelSlots })

	started := time.Now()
	_, err = service.Search(context.Background(), SearchRequest{Query: "database"})
	if !errors.Is(err, ErrModelSchedulerWait) {
		t.Fatalf("model wait error = %v, want ErrModelSchedulerWait", err)
	}
	if elapsed := time.Since(started); elapsed > time.Second {
		t.Fatalf("model wait returned after %s", elapsed)
	}
	if metrics := service.Metrics(); metrics.ModelSchedulerWaits != 1 {
		t.Fatalf("model scheduler wait metric = %d", metrics.ModelSchedulerWaits)
	}
}

func TestSearchEndToEndDeadline(t *testing.T) {
	service, _ := newSearchFixture(t)
	base, err := service.CreateBase("Deadline", "", "", BaseConfig{})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.AddTextDocument(context.Background(), base.ID, "Doc", "database content"); err != nil {
		t.Fatal(err)
	}
	service.global.Retrieval.SearchTimeoutMS = 100
	service.global.Scheduler.ModelWaitMS = 1000
	service.SetProviders(&blockingEmbedder{model: "slow", release: make(chan struct{})}, nil)

	started := time.Now()
	_, err = service.Search(context.Background(), SearchRequest{Query: "database"})
	if !errors.Is(err, ErrSearchTimeout) {
		t.Fatalf("search timeout error = %v, want ErrSearchTimeout", err)
	}
	if elapsed := time.Since(started); elapsed > time.Second {
		t.Fatalf("search deadline returned after %s", elapsed)
	}
	if metrics := service.Metrics(); metrics.SearchTimeouts != 1 {
		t.Fatalf("search timeout metric = %d", metrics.SearchTimeouts)
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
	// A saved empty pinned scope must also fail closed; no scope record is
	// the only state that searches all bases.
	empty := []string{}
	if err := service.SetEnabledScope(boolPtr(true), &empty); err != nil {
		t.Fatal(err)
	}
	if state, err := service.EnabledScopeState(); err != nil || !state.Explicit || len(state.BaseIDs) != 0 {
		t.Fatalf("explicit empty state: %+v %v", state, err)
	}
	result, err = service.Search(ctx, SearchRequest{Query: "database"})
	if err != nil || result.Total != 0 {
		t.Fatalf("explicit empty pinned scope: %v %+v", err, result)
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

func TestRerankerTimeoutDegradesQuickly(t *testing.T) {
	release := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		// The 10ms provider timeout cancels the request long before the test
		// releases the handler. Waiting on cancellation avoids a wall-clock
		// assertion that races scheduler latency on loaded hosts.
		select {
		case <-release:
		case <-time.After(2 * time.Second):
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()
	defer close(release)
	service, _ := newSearchFixture(t)
	base, _ := service.CreateBase("B", "", "", BaseConfig{})
	ctx := context.Background()
	if _, err := service.AddTextDocument(ctx, base.ID, "Guide", "database intro\n\nbeta details about the database engine internals"); err != nil {
		t.Fatal(err)
	}
	service.SetProviders(nil, rerank.New(rerank.Config{
		BaseURL: server.URL, Model: "blocked", Timeout: 10 * time.Millisecond,
		FailureThreshold: 1, OpenDuration: time.Second,
	}))
	started := time.Now()
	result, err := service.Search(ctx, SearchRequest{Query: "database", TopK: 2})
	if err != nil {
		t.Fatal(err)
	}
	// The provider timeout is 10ms; allow generous scheduler slack while still
	// proving the call is bounded far below the eight-second search deadline.
	if elapsed := time.Since(started); elapsed > 2*time.Second {
		t.Fatalf("timeout did not bound rerank latency: %s", elapsed)
	}
	if result.Reranked || result.Rerank == nil || result.Rerank.Status != "degraded" || result.Total == 0 {
		t.Fatalf("timeout degradation: %+v", result.Rerank)
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

func TestVectorHashReuseAcrossDocumentsMaterializesVectors(t *testing.T) {
	service, embedder := newSearchFixture(t)
	ctx := context.Background()
	firstBase, _ := service.CreateBase("First", "", "", BaseConfig{})
	secondBase, _ := service.CreateBase("Second", "", "", BaseConfig{})
	if _, err := service.AddTextDocument(ctx, firstBase.ID, "Guide", "database content"); err != nil {
		t.Fatal(err)
	}
	before := embedder.embeds
	second, err := service.AddTextDocument(ctx, secondBase.ID, "Guide", "database content")
	if err != nil {
		t.Fatal(err)
	}
	if embedder.embeds != before {
		t.Fatalf("expected cross-document hash reuse, embeds went %d -> %d", before, embedder.embeds)
	}
	result, err := service.Search(ctx, SearchRequest{Query: "database", Mode: "vector", BaseIDs: []string{secondBase.ID}})
	if err != nil || result.Total == 0 || result.Hits[0].DocID != second.ID {
		t.Fatalf("reused vector was not searchable in destination document: err=%v result=%+v", err, result)
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
