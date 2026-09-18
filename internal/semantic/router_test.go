package semantic

import (
	"strings"
	"testing"
)

func TestRouteQueryCoversAutoFamilies(t *testing.T) {
	cases := []struct {
		query     string
		intent    QueryIntent
		primary   string
		semantic  bool
		relations bool
		hierarchy bool
	}{
		{query: "What is the embedding dimension?", intent: IntentFact, primary: RetrievalEvidence, semantic: true},
		{query: "Summarize the whole knowledge base", intent: IntentGlobal, primary: RetrievalHierarchy, semantic: true, hierarchy: true},
		{query: "How are alerts configured across documents?", intent: IntentCrossDocument, primary: RetrievalHybridLane, semantic: true, hierarchy: true},
		{query: "compare rollback and retry", intent: IntentComparison, primary: RetrievalHybridLane, semantic: true, hierarchy: true},
		{query: "How does retry affect rollout?", intent: IntentMultiHop, primary: RetrievalRelation, semantic: true, relations: true},
		{query: "What is current in version 0.3?", intent: IntentTemporal, primary: RetrievalEvidence, semantic: true},
		{query: "What does 本文 say about isolation?", intent: IntentLocalDocument, primary: RetrievalEvidence, semantic: true},
		{query: "最新版本的行为是什么", intent: IntentTemporal, primary: RetrievalEvidence, semantic: true},
	}
	for _, testCase := range cases {
		plan := RouteQuery(testCase.query)
		if plan.Intent != testCase.intent || plan.PrimaryRetrieval != testCase.primary ||
			plan.UseSemanticMemory != testCase.semantic || plan.UseRelations != testCase.relations ||
			plan.UseHierarchicalSummary != testCase.hierarchy || !plan.UseEvidence {
			t.Fatalf("RouteQuery(%q) = %+v, want intent=%s primary=%s", testCase.query, plan, testCase.intent, testCase.primary)
		}
		if plan.Confidence <= 0 || plan.RouterVersion != QueryRouterVersion {
			t.Fatalf("invalid routing confidence/version: %+v", plan)
		}
		if !strings.Contains(plan.Describe(), "intent="+string(testCase.intent)) {
			t.Fatalf("diagnostics missing intent: %s", plan.Describe())
		}
	}
}

func TestRouteQueryIsDeterministicAndDoesNotMutateQuery(t *testing.T) {
	query := " 总结整个知识库 "
	first := RouteQuery(query)
	second := RouteQuery(query)
	if first.Query != strings.TrimSpace(query) || first.Describe() != second.Describe() {
		t.Fatalf("router is not deterministic: %+v %+v", first, second)
	}
}
