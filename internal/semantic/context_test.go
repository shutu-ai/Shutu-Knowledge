package semantic

import (
	"strings"
	"testing"
)

func TestCompileContextCombinesOrientationFactsEvidenceAndCitations(t *testing.T) {
	compilation := searchCompilationFixture(t)
	evidenceItems := []ContextEvidence{
		{ChunkID: "chunk-1", DocumentID: "doc-search", DocumentTitle: "Vector Retrieval",
			Heading: "Vector Retrieval", Text: "The vector service uses 1024-dimensional vectors.",
			Citation: "doc-search#chunk-1", Score: 1},
		{ChunkID: "chunk-1", DocumentID: "doc-search", DocumentTitle: "Vector Retrieval",
			Heading: "Vector Retrieval", Text: "The vector service uses 1024-dimensional vectors.",
			Citation: "doc-search#chunk-1", Score: 1},
	}
	pkg, err := CompileContext(compilation, evidenceItems, ContextCompileOptions{Query: "vector retrieval", TokenBudget: 2048})
	if err != nil {
		t.Fatal(err)
	}
	if pkg.CompilerVersion != "semantic-evidence-v1" || pkg.Generation != compilation.Generation {
		t.Fatalf("context scope = %+v", pkg)
	}
	if len(pkg.KnowledgeSummary) == 0 || len(pkg.Concepts) == 0 || len(pkg.Facts) == 0 {
		t.Fatalf("context lacks semantic orientation: %+v", pkg)
	}
	if len(pkg.Evidence) != 1 || pkg.Evidence[0].ChunkID != "chunk-1" {
		t.Fatalf("context evidence = %+v", pkg.Evidence)
	}
	if len(pkg.Citations) != 1 || !strings.Contains(pkg.Citations[0], "doc-search#chunk-1") {
		t.Fatalf("context citations = %+v", pkg.Citations)
	}
	if pkg.Diagnostics.EvidenceDeduplicated != 1 || pkg.Diagnostics.EvidenceSelected != 1 {
		t.Fatalf("context diagnostics = %+v", pkg.Diagnostics)
	}
	if pkg.EstimatedTokens > pkg.TokenBudget {
		t.Fatalf("context tokens = %d, budget %d", pkg.EstimatedTokens, pkg.TokenBudget)
	}
	for _, section := range []string{"## Knowledge orientation", "## Relevant concepts", "## Critical facts", "## Exact evidence", "## Citations"} {
		if !strings.Contains(pkg.RenderedContext, section) {
			t.Fatalf("rendered context missing %q:\n%s", section, pkg.RenderedContext)
		}
	}
}

func TestCompileContextRespectsLowBudget(t *testing.T) {
	compilation := searchCompilationFixture(t)
	evidenceItems := make([]ContextEvidence, 0, 8)
	for i := 0; i < 8; i++ {
		evidenceItems = append(evidenceItems, ContextEvidence{
			ChunkID: string(rune('a' + i)), DocumentID: "doc-search", DocumentTitle: "Vector Retrieval",
			Text:  strings.Repeat("detailed vector evidence ", 100),
			Score: float64(8-i) / 8,
		})
	}
	pkg, err := CompileContext(compilation, evidenceItems, ContextCompileOptions{Query: "vector retrieval", TokenBudget: 512})
	if err != nil {
		t.Fatal(err)
	}
	if pkg.EstimatedTokens > pkg.TokenBudget {
		t.Fatalf("low-budget tokens = %d, budget %d", pkg.EstimatedTokens, pkg.TokenBudget)
	}
	if len(pkg.Evidence) == len(evidenceItems) {
		t.Fatal("low-budget context failed to drop evidence")
	}
	if pkg.Diagnostics.EvidenceDropped == 0 {
		t.Fatalf("low-budget diagnostics = %+v", pkg.Diagnostics)
	}
}

func TestCompileContextRejectsEmptyQuery(t *testing.T) {
	if _, err := CompileContext(searchCompilationFixture(t), nil, ContextCompileOptions{}); err == nil {
		t.Fatal("empty context query accepted")
	}
}
