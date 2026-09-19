package semantic

import (
	"strings"
	"testing"
)

func TestSearchCompilationActivatesConceptTopicSummary(t *testing.T) {
	compilation := searchCompilationFixture(t)
	response, err := SearchCompilation(compilation, SearchOptions{Query: "vector retrieval", TopK: 5})
	if err != nil {
		t.Fatal(err)
	}
	if response.Generation != compilation.Generation || response.ScoringVersion != "temporal-lexical-v1" {
		t.Fatalf("search diagnostics = %+v", response)
	}
	kinds := map[UnitKind]bool{}
	for _, hit := range response.Hits {
		kinds[hit.Unit.Type] = true
		if hit.Score <= 0 || hit.Coverage <= 0 || len(hit.MatchedTerms) == 0 {
			t.Fatalf("weak hit diagnostics: %+v", hit)
		}
		if len(hit.Evidence) == 0 {
			t.Fatalf("semantic hit lacks evidence closure: %+v", hit.Unit)
		}
		for _, source := range hit.Evidence {
			if source.NodeID == "" || source.ChunkID == "" {
				t.Fatalf("semantic hit substituted non-exact evidence: %+v", source)
			}
		}
	}
	if !kinds[UnitConcept] || !kinds[UnitTopic] || !kinds[UnitSummary] {
		t.Fatalf("expected concept/topic/summary activation, got %+v", kinds)
	}
	if kinds[UnitFact] {
		t.Fatalf("fact unexpectedly returned by default search: %+v", kinds)
	}
}

func TestSearchCompilationHonorsKindFilterAndRejectsEmptyQuery(t *testing.T) {
	compilation := searchCompilationFixture(t)
	response, err := SearchCompilation(compilation, SearchOptions{Query: "vector retrieval", TopK: 1, Kinds: []UnitKind{UnitTopic}})
	if err != nil {
		t.Fatal(err)
	}
	if len(response.Hits) != 1 || response.Hits[0].Unit.Type != UnitTopic {
		t.Fatalf("topic filter result = %+v", response.Hits)
	}
	if _, err := SearchCompilation(compilation, SearchOptions{Query: "   "}); err == nil || !strings.Contains(err.Error(), "empty") {
		t.Fatalf("empty query error = %v", err)
	}
}

func TestSearchTermsHandleCJK(t *testing.T) {
	terms := SearchTerms("向量检索 hybrid")
	joined := strings.Join(terms, ",")
	for _, expected := range []string{"向量", "量检", "检索", "hybrid"} {
		if !strings.Contains(joined, expected) {
			t.Fatalf("terms %q missing %q", joined, expected)
		}
	}
}

func searchCompilationFixture(t *testing.T) Compilation {
	t.Helper()
	compilation, err := Compile("base-search", 4, []SourceDocument{
		sourceDocumentForCompile("doc-search", "Vector Retrieval", "# Vector Retrieval\n\nThe vector service uses 1024-dimensional vectors.", 4, 4),
	}, 4_000)
	if err != nil {
		t.Fatal(err)
	}
	return compilation
}

func TestGlobalSearchReturnsHierarchicalOrientationWithoutLexicalOverlap(t *testing.T) {
	response, err := SearchCompilation(searchCompilationFixture(t), SearchOptions{Query: "summarize the whole knowledge base", TopK: 5})
	if err != nil {
		t.Fatal(err)
	}
	if response.Routing == nil || response.Routing.Intent != IntentGlobal {
		t.Fatalf("global routing = %+v", response.Routing)
	}
	topic := false
	for _, hit := range response.Hits {
		if hit.Unit.Type == UnitTopic {
			topic = true
		}
	}
	if !topic {
		t.Fatalf("global search returned no topic: %+v", response.Hits)
	}
}
