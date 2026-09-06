package extension

import (
	"testing"

	"github.com/shutu-ai/shutu-knowledge/internal/evidence"
	"github.com/shutu-ai/shutu-knowledge/internal/knowledge"
)

func TestAutoQueryPlanningAndSignals(t *testing.T) {
	plan := planAutoQuery("那它现在呢?", []string{"What is the retry budget?"})
	if plan.primary != "现在" || plan.enhanced != "现在 retry budget" {
		t.Fatalf("history plan: %#v", plan)
	}
	if len(extractStrictIdentifiers("see ABC-123 now")) != 1 {
		t.Fatal("compound identifier was not detected")
	}
	if !containsStrictIdentifier("token ABC-123.", "abc-123") {
		t.Fatal("identifier evidence match failed")
	}
	if containsStrictIdentifier("token ABC-1234.", "abc-123") {
		t.Fatal("identifier boundary was ignored")
	}
}

func TestInjectedEvidenceDedupPreservesFreshAnchor(t *testing.T) {
	hit := knowledge.SearchHit{
		ChunkID: "anchor",
		ContextWindow: &evidence.Window{
			AnchorChunkID: "anchor",
			Before: []evidence.Excerpt{
				{ChunkID: "old"}, {ChunkID: "new-before"},
			},
			After: []evidence.Excerpt{
				{ChunkID: "old-after"},
			},
		},
	}
	pruned, visible := pruneInjectedEvidence(hit, map[string]bool{"old": true, "old-after": true})
	if !visible || pruned.ContextWindow.AnchorChunkID != "anchor" ||
		len(pruned.ContextWindow.Before) != 1 || len(pruned.ContextWindow.After) != 0 ||
		!pruned.ContextWindow.HasMoreAfter {
		t.Fatalf("pruned evidence: %#v %v", pruned, visible)
	}
	if _, visible := pruneInjectedEvidence(hit, map[string]bool{"anchor": true}); visible {
		t.Fatal("injected anchor was visible")
	}
}

func TestAutoRelevanceGateDistinguishesLaneScores(t *testing.T) {
	strongLexical := []knowledge.SearchHit{{Score: 0.00001}}
	if !autoRelevanceGate("lexical", strongLexical) {
		t.Fatal("clear lexical winner was rejected")
	}
	weakRerank := []knowledge.SearchHit{{RerankScore: 0.1, Score: 0.1}}
	if autoRelevanceGate("lexical", weakRerank) {
		t.Fatal("weak rerank score passed the absolute floor")
	}
	flatLexical := []knowledge.SearchHit{{Score: 0.01}, {Score: 0.0095}}
	if autoRelevanceGate("lexical", flatLexical) {
		t.Fatal("flat weak lexical set was accepted")
	}
	clearLexical := []knowledge.SearchHit{{Score: 0.02}, {Score: 0.01}}
	if !autoRelevanceGate("lexical", clearLexical) {
		t.Fatal("clear lexical lead was rejected")
	}
}
