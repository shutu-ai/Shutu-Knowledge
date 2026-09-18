package knowledge

import (
	"context"
	"strings"
	"testing"

	"github.com/shutu-ai/shutu-knowledge/internal/chunk"
	"github.com/shutu-ai/shutu-knowledge/internal/evidence"
	"github.com/shutu-ai/shutu-knowledge/internal/semantic"
)

type semanticBenchmarkCase struct {
	name           string
	query          string
	expectedIntent semantic.QueryIntent
	requiredDocs   []string
	forbiddenDocs  []string
	requiredTopics []string
}

func TestSemanticBenchmarkCoversReleaseGateQueryFamilies(t *testing.T) {
	service := newSemanticMemoryTestService(t)
	defer service.close()
	ctx := context.Background()
	base, docs := importSemanticBenchmarkCorpus(t, service, ctx)
	if _, err := service.CompileSemanticMemory(ctx, base.ID); err != nil {
		t.Fatal(err)
	}

	cases := []semanticBenchmarkCase{
		{
			name: "fact", query: "The vector service uses 1024-dimensional embeddings.",
			expectedIntent: semantic.IntentFact,
			requiredDocs:   []string{docs["vector"]},
		},
		{
			name: "global", query: "Summarize the whole knowledge base",
			expectedIntent: semantic.IntentGlobal,
			requiredDocs:   []string{docs["release-current"], docs["capacity"], docs["vector"]},
			requiredTopics: []string{"release runbook", "capacity planning", "vector retrieval"},
		},
		{
			name: "cross-document", query: "How do release 0.3 and vector retrieval differ? Release supports the 1M context window; vector retrieval uses 1024-dimensional embeddings.",
			expectedIntent: semantic.IntentComparison,
			requiredDocs:   []string{docs["release-current"], docs["vector"]},
		},
		{
			name: "multi-hop", query: "How does worker beta affect capacity planning?",
			expectedIntent: semantic.IntentMultiHop,
			requiredDocs:   []string{docs["release-current"], docs["capacity"]},
		},
		{
			name: "temporal-current", query: "Version 0.3 supports the 1M context window",
			expectedIntent: semantic.IntentTemporal,
			requiredDocs:   []string{docs["release-current"]},
			forbiddenDocs:  []string{docs["release-old"]},
		},
	}

	var totalBaseline, totalSemantic float64
	var totalBaselineTokens, totalSemanticTokens int
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			baselineDocs, baselineTokens := benchmarkEvidenceProjection(service, ctx, base.ID, testCase.query)
			pkg, err := service.CompileKnowledgeContext(ctx, base.ID, testCase.query, 2048)
			if err != nil {
				t.Fatal(err)
			}
			if pkg.Routing == nil || pkg.Routing.Intent != testCase.expectedIntent {
				t.Fatalf("routing = %+v, want %s", pkg.Routing, testCase.expectedIntent)
			}
			contextDocs := contextDocumentSet(pkg)
			baselineScore := documentCoverage(baselineDocs, testCase.requiredDocs)
			semanticScore := documentCoverage(contextDocs, testCase.requiredDocs)
			if testCase.name == "global" {
				semanticScore += benchmarkTopicCoverage(service, ctx, base.ID, testCase.query, testCase.requiredTopics)
			}
			if testCase.name == "multi-hop" {
				semanticScore += benchmarkFactCoverage(pkg, []string{"worker beta depends on the capacity scheduler", "the capacity scheduler uses the 1m context window for planning"})
			}
			if semanticScore+0.001 < baselineScore {
				t.Fatalf("%s semantic score %.2f below 0.3 baseline %.2f", testCase.name, semanticScore, baselineScore)
			}
			if containsAny(contextDocs, testCase.forbiddenDocs) {
				t.Fatalf("%s selected superseded evidence: %+v", testCase.name, contextDocs)
			}
			if pkg.EstimatedTokens > pkg.TokenBudget {
				t.Fatalf("%s token estimate %d exceeds budget %d", testCase.name, pkg.EstimatedTokens, pkg.TokenBudget)
			}
			t.Logf("scores: 0.3=%.3f 0.4=%.3f tokens: 0.3=%d 0.4=%d docs=%v required=%v",
				baselineScore, semanticScore, baselineTokens, pkg.EstimatedTokens, contextDocs, testCase.requiredDocs)
			totalBaseline += baselineScore
			totalSemantic += semanticScore
			totalBaselineTokens += baselineTokens
			totalSemanticTokens += pkg.EstimatedTokens
		})
	}

	// The release criterion is measurable global/cross-document improvement,
	// not merely feature count. Global topic orientation adds a dimension the
	// 0.3 evidence-only projection cannot represent.
	if totalSemantic <= totalBaseline {
		t.Fatalf("benchmark did not improve 0.3: semantic=%.3f baseline=%.3f", totalSemantic, totalBaseline)
	}
	t.Logf("benchmark proxy: 0.3=%.3f 0.4=%.3f baseline_tokens=%d semantic_tokens=%d",
		totalBaseline, totalSemantic, totalBaselineTokens, totalSemanticTokens)
}

func importSemanticBenchmarkCorpus(t *testing.T, service *semanticMemoryTestService, ctx context.Context) (Base, map[string]string) {
	t.Helper()
	base, err := service.CreateBase("Semantic Benchmark", "", "", BaseConfig{})
	if err != nil {
		t.Fatal(err)
	}
	corpus := []struct {
		key   string
		title string
		text  string
	}{
		{
			key: "release-old", title: "Release Runbook",
			text: "# Release Runbook\n\nVersion 0.2 supports the 128k context window. The gateway routes sessions through worker alpha.",
		},
		{
			key: "release-current", title: "Release Runbook Current",
			text: "# Release Runbook\n\nVersion 0.3 supports the 1M context window. The gateway routes sessions through worker beta. Worker beta depends on the capacity scheduler.",
		},
		{
			key: "capacity", title: "Capacity Planning",
			text: "# Capacity Planning\n\nWorker beta affects queue admission. The capacity scheduler uses the 1M context window for planning.",
		},
		{
			key: "vector", title: "Vector Retrieval",
			text: "# Vector Retrieval\n\nHybrid retrieval combines lexical and vector methods. The vector service uses 1024-dimensional embeddings.",
		},
	}
	docs := map[string]string{}
	for _, item := range corpus {
		document, err := service.AddTextDocument(ctx, base.ID, item.title, item.text)
		if err != nil {
			t.Fatal(err)
		}
		docs[item.key] = document.ID
	}
	return base, docs
}

func benchmarkEvidenceProjection(service *semanticMemoryTestService, ctx context.Context, baseID, query string) (map[string]bool, int) {
	result, err := service.Search(ctx, SearchRequest{BaseID: baseID, Query: query, TopK: 8, Mode: "hybrid"})
	if err != nil {
		panic(err)
	}
	docs := map[string]bool{}
	tokens := 0
	for _, hit := range result.Hits {
		docs[hit.DocID] = true
		text := hit.Text
		if hit.ContextWindow != nil {
			text = evidence.Serialize(*hit.ContextWindow)
		}
		tokens += chunkEstimateTokens(text)
	}
	return docs, tokens
}

func contextDocumentSet(pkg semantic.ContextPackage) map[string]bool {
	out := map[string]bool{}
	for _, item := range pkg.Evidence {
		out[item.DocumentID] = true
	}
	return out
}

func documentCoverage(actual map[string]bool, required []string) float64 {
	if len(required) == 0 {
		return 0
	}
	found := 0
	for _, documentID := range required {
		if actual[documentID] {
			found++
		}
	}
	return float64(found) / float64(len(required))
}

func benchmarkTopicCoverage(service *semanticMemoryTestService, ctx context.Context, baseID, query string, required []string) float64 {
	response, err := service.SearchSemanticMemory(ctx, baseID, semantic.SearchOptions{Query: query, TopK: 12})
	if err != nil {
		panic(err)
	}
	normalized := map[string]bool{}
	for _, hit := range response.Hits {
		if hit.Unit.Type == semantic.UnitTopic {
			normalized[strings.ToLower(hit.Unit.Title)] = true
			normalized[strings.ToLower(hit.Unit.CanonicalKey)] = true
		}
	}
	found := 0
	for _, topic := range required {
		if normalized[strings.ToLower(topic)] {
			found++
		}
	}
	return float64(found) / float64(len(required))
}

func benchmarkFactCoverage(pkg semantic.ContextPackage, required []string) float64 {
	joined := strings.ToLower(strings.Join(pkg.Facts, "\n"))
	found := 0
	for _, fact := range required {
		if strings.Contains(joined, strings.ToLower(fact)) {
			found++
		}
	}
	return float64(found) / float64(len(required))
}

func containsAny(actual map[string]bool, expected []string) bool {
	for _, value := range expected {
		if actual[value] {
			return true
		}
	}
	return false
}

func chunkEstimateTokens(text string) int {
	return chunk.EstimateTokens(text)
}
