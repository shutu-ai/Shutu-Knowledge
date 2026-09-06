package knowledge

import (
	"context"
	"fmt"
	"math"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/shutu-ai/shutu-knowledge/internal/config"
	"github.com/shutu-ai/shutu-knowledge/internal/jobs"
	"github.com/shutu-ai/shutu-knowledge/internal/storage"
)

// benchmarkQuery is one stable retrieval question. RelevantTitles may name
// several equally valid documents for multi-document recall.
type benchmarkQuery struct {
	text        string
	relevant    []string
	evidenceHas string
	mode        string
}

type benchmarkFixture struct {
	service *Service
	base    Base
	docs    map[string]string
	cleanup func()
}

// benchmarkEmbedder is deterministic and compact so the benchmark exercises
// vector fusion without downloading a model or measuring GPU inference.
type benchmarkEmbedder struct{}

var benchmarkConcepts = []string{
	"database", "cloud", "数据库", "向量", "kubernetes", "风险",
	"duplicate", "long", "finance", "archive",
}

func (benchmarkEmbedder) Embed(_ context.Context, texts []string) ([][]float64, error) {
	out := make([][]float64, 0, len(texts))
	for _, text := range texts {
		vector := make([]float64, len(benchmarkConcepts))
		lower := strings.ToLower(text)
		for i, concept := range benchmarkConcepts {
			if strings.Contains(lower, strings.ToLower(concept)) {
				vector[i] = 1
			}
		}
		var length float64
		for _, value := range vector {
			length += value * value
		}
		length = math.Sqrt(length)
		if length > 0 {
			for i := range vector {
				vector[i] /= length
			}
		} else {
			vector[0] = 1
		}
		out = append(out, vector)
	}
	return out, nil
}

func (benchmarkEmbedder) ModelKey() string { return "benchmark:deterministic" }

func newBenchmarkFixture(b *testing.B) *benchmarkFixture {
	b.Helper()
	home := b.TempDir()
	b.Setenv("SHUTU_KNOWLEDGE_HOME", home)
	db, err := storage.Open(filepath.Join(home, "knowledge.db"))
	if err != nil {
		b.Fatal(err)
	}
	raw, err := storage.NewRawFileStore(filepath.Join(home, "raw"))
	if err != nil {
		b.Fatal(err)
	}
	manager := jobs.New(db, 4)
	if err := manager.Start(context.Background()); err != nil {
		b.Fatal(err)
	}
	cfg := config.Defaults()
	cfg.Embedding.Provider = "openai"
	cfg.Embedding.Batch = 32
	service := NewService(db, raw, cfg, manager)
	service.SetProviders(benchmarkEmbedder{}, nil)
	base, err := service.CreateBase("Benchmark", "", "", BaseConfig{})
	if err != nil {
		b.Fatal(err)
	}
	fixture := &benchmarkFixture{
		service: service, base: base, docs: map[string]string{},
		cleanup: func() {
			manager.Stop()
			_ = db.Close()
		},
	}
	fixture.importCorpus(b)
	return fixture
}

func benchmarkCorpus() []Document {
	long := strings.Repeat("The long operations analysis discusses database capacity, cloud cost, and quarterly review detail. ", 20)
	docs := []Document{
		{Title: "Storage Handbook", RawText: "The production database stores durable rows. A database index speeds exact and semantic lookup."},
		{Title: "Cloud Platform", RawText: "云平台提供数据库、cloud storage 与向量服务。数据库备份每周执行一次。"},
		{Title: "Vector Retrieval", RawText: "向量检索把查询映射到语义空间。数据库索引与向量检索可以互补。"},
		{Title: "Kubernetes Rollout", RawText: "Kubernetes rollout uses a readiness gate. The platform rolls pods out in small batches."},
		{Title: "Risk Finance", RawText: "finance teams track 风险 limits. Investment review records exposure by quarter."},
		{Title: "Duplicate Alpha", RawText: "Shared duplicate paragraph body. Authoritative marker zq42 identifies the alpha record."},
		{Title: "Duplicate Beta", RawText: "Shared duplicate paragraph body. This beta copy has another marker."},
		{Title: "Long Operations", RawText: long + " The operations database conclusion mentions cloud cost."},
		{Title: "Archive Ledger", RawText: "Archive records remain immutable. Ledger retention follows the compliance policy."},
	}
	for i := 0; i < 110; i++ {
		docs = append(docs, Document{
			Title:   fmt.Sprintf("Filler Record %03d", i),
			RawText: fmt.Sprintf("Unrelated record %03d covers scheduling notes, facilities inventory, and routine mailbox maintenance.", i),
		})
	}
	return docs
}

func (f *benchmarkFixture) importCorpus(b *testing.B) {
	b.Helper()
	for _, doc := range benchmarkCorpus() {
		imported, err := f.service.AddTextDocument(context.Background(), f.base.ID, doc.Title, doc.RawText)
		if err != nil {
			b.Fatal(err)
		}
		f.docs[imported.Title] = imported.ID
	}
	if len(f.docs) != len(benchmarkCorpus()) {
		b.Fatalf("corpus size: %d", len(f.docs))
	}
}

func benchmarkQueries() []benchmarkQuery {
	return []benchmarkQuery{
		{text: "database durable rows", relevant: []string{"Storage Handbook"}, evidenceHas: "durable rows", mode: "lexical"},
		{text: "云数据库备份", relevant: []string{"Cloud Platform"}, evidenceHas: "数据库备份", mode: "lexical"},
		{text: "向量检索语义空间", relevant: []string{"Vector Retrieval"}, evidenceHas: "语义空间", mode: "lexical"},
		{text: "cloud storage service", relevant: []string{"Cloud Platform", "Long Operations"}, evidenceHas: "cloud", mode: "hybrid"},
		{text: "zq42 duplicate alpha", relevant: []string{"Duplicate Alpha"}, evidenceHas: "zq42", mode: "lexical"},
		{text: "long operations quarterly review", relevant: []string{"Long Operations"}, evidenceHas: "quarterly review", mode: "lexical"},
	}
}

type retrievalMetrics struct {
	HitAt1       float64
	HitAt3       float64
	RecallAt3    float64
	MRR          float64
	ContextRecal float64
}

func evaluateQuery(service *Service, ctx context.Context, query benchmarkQuery) (retrievalMetrics, error) {
	result, err := service.Search(ctx, SearchRequest{
		Query: query.text, TopK: 3, Mode: query.mode,
	})
	if err != nil {
		return retrievalMetrics{}, err
	}
	relevant := map[string]bool{}
	for _, title := range query.relevant {
		relevant[title] = true
	}
	var metrics retrievalMetrics
	seen := map[string]bool{}
	for rank, hit := range result.Hits {
		title := hit.DocumentTitle
		seen[title] = true
		if relevant[title] {
			if rank == 0 {
				metrics.HitAt1 = 1
			}
			if rank < 3 {
				metrics.HitAt3 = 1
				metrics.MRR = math.Max(metrics.MRR, 1/float64(rank+1))
			}
		}
		if strings.Contains(hit.Text, query.evidenceHas) ||
			(hit.ContextWindow != nil && strings.Contains(hit.Text+hit.ContextWindow.AnchorChunkID+hit.ContextWindow.Anchor.Text, query.evidenceHas)) {
			metrics.ContextRecal = 1
		}
	}
	found := 0
	for title := range relevant {
		if seen[title] {
			found++
		}
	}
	metrics.RecallAt3 = float64(found) / float64(len(query.relevant))
	return metrics, nil
}

func aggregateMetrics(values []retrievalMetrics) retrievalMetrics {
	var out retrievalMetrics
	for _, value := range values {
		out.HitAt1 += value.HitAt1
		out.HitAt3 += value.HitAt3
		out.RecallAt3 += value.RecallAt3
		out.MRR += value.MRR
		out.ContextRecal += value.ContextRecal
	}
	count := float64(len(values))
	out.HitAt1 /= count
	out.HitAt3 /= count
	out.RecallAt3 /= count
	out.MRR /= count
	out.ContextRecal /= count
	return out
}

func TestRetrievalBenchmarkCorpusQuality(t *testing.T) {
	fixture := newBenchmarkFixtureT(t)
	defer fixture.cleanup()
	ctx := context.Background()
	var values []retrievalMetrics
	for _, query := range benchmarkQueries() {
		metrics, err := evaluateQuery(fixture.service, ctx, query)
		if err != nil {
			t.Fatal(err)
		}
		values = append(values, metrics)
	}
	metrics := aggregateMetrics(values)
	if metrics.HitAt3 < 0.84 || metrics.MRR < 0.75 || metrics.ContextRecal < 0.84 {
		t.Fatalf("retrieval quality below contract: %+v", metrics)
	}
}

func newBenchmarkFixtureT(t *testing.T) *benchmarkFixture {
	t.Helper()
	home := t.TempDir()
	t.Setenv("SHUTU_KNOWLEDGE_HOME", home)
	db, err := storage.Open(filepath.Join(home, "knowledge.db"))
	if err != nil {
		t.Fatal(err)
	}
	raw, err := storage.NewRawFileStore(filepath.Join(home, "raw"))
	if err != nil {
		t.Fatal(err)
	}
	manager := jobs.New(db, 4)
	if err := manager.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	cfg := config.Defaults()
	cfg.Embedding.Provider = "openai"
	service := NewService(db, raw, cfg, manager)
	service.SetProviders(benchmarkEmbedder{}, nil)
	base, err := service.CreateBase("Benchmark", "", "", BaseConfig{})
	if err != nil {
		t.Fatal(err)
	}
	fixture := &benchmarkFixture{
		service: service, base: base, docs: map[string]string{},
		cleanup: func() {
			manager.Stop()
			_ = db.Close()
		},
	}
	// importCorpus uses testing.B.Fatal; for the correctness test the
	// expected count check is equivalent and cannot panic for the fixture.
	for _, doc := range benchmarkCorpus() {
		imported, err := service.AddTextDocument(context.Background(), base.ID, doc.Title, doc.RawText)
		if err != nil {
			t.Fatal(err)
		}
		fixture.docs[imported.Title] = imported.ID
	}
	return fixture
}

func BenchmarkCorpusIngestion(b *testing.B) {
	fixture := newBenchmarkFixture(b)
	defer fixture.cleanup()
	docs := benchmarkCorpus()
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		doc := docs[i%len(docs)]
		if _, err := fixture.service.AddTextDocument(context.Background(), fixture.base.ID,
			fmt.Sprintf("bench-ingest-%d-%s", b.N, doc.Title), doc.RawText+" "+fmt.Sprint(i)); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkChunking(b *testing.B) {
	fixture := newBenchmarkFixture(b)
	defer fixture.cleanup()
	text := benchmarkCorpus()[7].RawText
	opts := fixture.service.chunkOptions(BaseConfig{})
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if pieces := fixture.service.structuralPieces(text, opts); len(pieces) == 0 {
			b.Fatal("no chunks")
		}
	}
}

func BenchmarkEmbedding(b *testing.B) {
	fixture := newBenchmarkFixture(b)
	defer fixture.cleanup()
	ctx := context.Background()
	docs, err := fixture.service.ListDocuments(fixture.base.ID)
	if err != nil {
		b.Fatal(err)
	}
	sort.Slice(docs, func(i, j int) bool { return docs[i].ID < docs[j].ID })
	docID := docs[0].ID
	doc, chunks, err := fixture.service.GetDocument(docID, true)
	if err != nil {
		b.Fatal(err)
	}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if code, err := fixture.service.embedChunks(ctx, &doc, chunks); code != "" || err != nil {
			b.Fatalf("embed: %s %v", code, err)
		}
	}
}

func BenchmarkRetrievalLanes(b *testing.B) {
	fixture := newBenchmarkFixture(b)
	defer fixture.cleanup()
	for _, mode := range []string{"lexical", "vector", "hybrid"} {
		b.Run(mode, func(b *testing.B) {
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				if _, err := fixture.service.Search(context.Background(), SearchRequest{
					Query: "database cloud 向量检索", TopK: 10, Mode: mode,
				}); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

func BenchmarkRerank(b *testing.B) {
	fixture := newBenchmarkFixture(b)
	defer fixture.cleanup()
	fixture.service.SetProviders(benchmarkEmbedder{}, benchmarkReranker{})
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := fixture.service.Search(context.Background(), SearchRequest{
			Query: "database cloud 向量检索", TopK: 10, Mode: "hybrid",
		}); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkEndToEndRAG(b *testing.B) {
	fixture := newBenchmarkFixture(b)
	defer fixture.cleanup()
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		title := fmt.Sprintf("bench-rag-%d-%d", b.N, i)
		if _, err := fixture.service.AddTextDocument(context.Background(), fixture.base.ID, title,
			fmt.Sprintf("RAG database cloud 向量检索 answer %d", i)); err != nil {
			b.Fatal(err)
		}
		if _, err := fixture.service.Search(context.Background(), SearchRequest{
			Query: "RAG database cloud 向量检索", TopK: 4, Mode: "hybrid",
		}); err != nil {
			b.Fatal(err)
		}
	}
}

type benchmarkReranker struct{}

func (benchmarkReranker) Rerank(_ context.Context, query string, texts []string) ([]float64, error) {
	queryLower := strings.ToLower(query)
	scores := make([]float64, len(texts))
	for i, text := range texts {
		textLower := strings.ToLower(text)
		score := 0.1
		for _, word := range strings.Fields(queryLower) {
			if strings.Contains(textLower, word) {
				score += 0.1
			}
		}
		scores[i] = math.Min(1, score)
	}
	return scores, nil
}

func (benchmarkReranker) ModelKey() string { return "rerank:benchmark" }
