package retrieval

import (
	"math"
	"reflect"
	"testing"
)

func TestTokenize(t *testing.T) {
	tokens := Tokenize("Hello world 你好世界 a")
	if !reflect.DeepEqual(tokens, []string{"hello", "world", "你好", "好世", "世界"}) {
		t.Fatalf("tokens: %v", tokens)
	}
	// A single CJK char must survive as a unigram.
	if got := Tokenize("中"); !reflect.DeepEqual(got, []string{"中"}) {
		t.Fatalf("single cjk: %v", got)
	}
}

func TestReciprocalRankFusionWeightsAndDeterminism(t *testing.T) {
	fused := ReciprocalRankFusion([][]string{{"a", "b"}, {"b", "c"}}, []float64{2, 1})
	if math.Abs(fused["a"]-(2.0/61.0)) > 1e-9 {
		t.Fatalf("a score: %v", fused["a"])
	}
	expectedB := 2.0/62.0 + 1.0/61.0
	if math.Abs(fused["b"]-expectedB) > 1e-9 {
		t.Fatalf("b score: %v", fused["b"])
	}
	// Deterministic: repeated calls give identical maps.
	again := ReciprocalRankFusion([][]string{{"a", "b"}, {"b", "c"}}, []float64{2, 1})
	if !reflect.DeepEqual(fused, again) {
		t.Fatal("fusion is not deterministic")
	}
}

func TestBM25RankingTrend(t *testing.T) {
	docs := []CorpusDoc{
		{ID: "queue", Text: "queueing theory explains queues and queue delays in queue systems"},
		{ID: "cooking", Text: "recipes for cooking dinner with pasta and sauce"},
	}
	scorer := BuildBm25(docs)
	query := Tokenize("queue")
	if scorer.Score("queue", query) <= scorer.Score("cooking", query) {
		t.Fatal("relevant doc must outrank irrelevant doc")
	}
	if NormalizeBm25(0) != 0 || NormalizeBm25(1) != 0.5 {
		t.Fatal("normalize mapping broken")
	}
}

func TestMaximalMarginalRelevanceDiversity(t *testing.T) {
	// Two near-identical vectors (same doc) and one distinct (other doc).
	sameA := []float32{1, 0, 0}
	sameB := []float32{0.99, 0.1, 0}
	distinct := []float32{0, 1, 0}
	hits := []RankedHit{
		{ID: "dup1", Score: 0.9, Embedding: sameA},
		{ID: "dup2", Score: 0.85, Embedding: sameB},
		{ID: "distinct", Score: 0.7, Embedding: distinct},
	}
	out := MaximalMarginalRelevance(hits, []float32{1, 0, 0}, 0.5, 12)
	firstTwo := map[string]bool{out[0].ID: true, out[1].ID: true}
	if !firstTwo["distinct"] {
		t.Fatalf("MMR must surface the distinct doc early: %v", ids(out))
	}
	// Lambda 1 disables diversity: pure relevance order.
	out = MaximalMarginalRelevance(hits, []float32{1, 0, 0}, 1, 12)
	if out[0].ID != "dup1" || out[1].ID != "dup2" {
		t.Fatalf("lambda=1 should keep relevance order: %v", ids(out))
	}
	// Hits without embeddings are appended, never dropped.
	out = MaximalMarginalRelevance(append(hits, RankedHit{ID: "no-emb", Score: 0.1}), []float32{1, 0, 0}, 0.5, 12)
	if out[len(out)-1].ID != "no-emb" {
		t.Fatalf("embedding-less hits must keep position: %v", ids(out))
	}
}

func TestRankModesThresholdAndDegrade(t *testing.T) {
	docs := []CorpusDoc{
		{ID: "v1", Text: "vector topic words"},
		{ID: "l1", Text: "lexical words words words"},
	}
	embeddings := map[string][]float32{
		"v1": {1, 0},
		"l1": {0, 1},
	}
	queryVector := []float32{1, 0}

	// auto with vectors -> hybrid; both lanes contribute scores.
	hits := Rank("lexical words", Candidates{Docs: docs, Embedding: embeddings}, RankOptions{Mode: "auto", TopK: 5, QueryVector: queryVector})
	if len(hits) != 2 {
		t.Fatalf("hybrid hits: %v", ids(hits))
	}
	for _, hit := range hits {
		if !hit.HasLexical || !hit.HasVector {
			t.Fatalf("hybrid hit missing lane scores: %+v", hit)
		}
	}
	// vector mode with no query vector degrades to lexical results.
	hits = Rank("lexical words", Candidates{Docs: docs, Embedding: embeddings}, RankOptions{Mode: "vector", TopK: 5})
	if len(hits) == 0 || hits[0].ID != "l1" {
		t.Fatalf("vector degrade: %v", ids(hits))
	}
	// Threshold filters low-relevance vector results.
	hits = Rank("anything", Candidates{Docs: docs, Embedding: embeddings}, RankOptions{Mode: "vector", TopK: 5, Threshold: 0.5, QueryVector: queryVector})
	if len(hits) != 1 || hits[0].ID != "v1" {
		t.Fatalf("threshold: %v", ids(hits))
	}
	// TopK bounds the result count.
	hits = Rank("words", Candidates{Docs: docs, Embedding: embeddings}, RankOptions{Mode: "lexical", TopK: 1})
	if len(hits) != 1 {
		t.Fatalf("topk: %v", ids(hits))
	}
}

func ids(hits []RankedHit) []string {
	out := make([]string, 0, len(hits))
	for _, hit := range hits {
		out = append(out, hit.ID)
	}
	return out
}
