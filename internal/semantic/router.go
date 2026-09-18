package semantic

import (
	"fmt"
	"sort"
	"strings"
)

// QueryIntent is the lightweight AUTO-mode label used by 0.4 routing.
type QueryIntent string

const (
	IntentFact          QueryIntent = "fact"
	IntentLocalDocument QueryIntent = "local_document"
	IntentCrossDocument QueryIntent = "cross_document"
	IntentGlobal        QueryIntent = "global"
	IntentComparison    QueryIntent = "comparison"
	IntentMultiHop      QueryIntent = "multi_hop"
	IntentTemporal      QueryIntent = "temporal"
)

// Retrieval lanes selected by the router.
const (
	RetrievalEvidence   = "evidence"
	RetrievalSemantic   = "semantic"
	RetrievalRelation   = "relation"
	RetrievalHierarchy  = "hierarchy"
	RetrievalHybridLane = "hybrid"
)

const QueryRouterVersion = "lexical-rules-v1"

// QueryPlan is deterministic diagnostics plus lane selection. It is not an
// agent planner and never mutates the query.
type QueryPlan struct {
	Query                  string         `json:"query"`
	NormalizedQuery        string         `json:"normalizedQuery"`
	Intent                 QueryIntent    `json:"intent"`
	Confidence             float64        `json:"confidence"`
	Signals                map[string]int `json:"signals,omitempty"`
	PrimaryRetrieval       string         `json:"primaryRetrieval"`
	SecondaryRetrieval     string         `json:"secondaryRetrieval,omitempty"`
	UseSemanticMemory      bool           `json:"useSemanticMemory"`
	UseEvidence            bool           `json:"useEvidence"`
	UseRelations           bool           `json:"useRelations"`
	UseHierarchicalSummary bool           `json:"useHierarchicalSummary"`
	RouterVersion          string         `json:"routerVersion"`
}

// RouteQuery classifies the seven 0.4 query families with bounded multilingual
// lexical rules. Exact facts remain the default so ordinary RAG behavior is not
// forced through semantic/graph machinery.
func RouteQuery(query string) QueryPlan {
	normalized := normalizeSearchText(query)
	signals := map[string]int{}
	addSignals := func(intent QueryIntent, markers ...string) {
		for _, marker := range markers {
			marker = normalizeSearchText(marker)
			if marker != "" && strings.Contains(normalized, marker) {
				signals[string(intent)] += 2
			}
		}
	}
	addSignals(IntentGlobal, "overall", "whole knowledge base", "summarize", "summary",
		"overview", "what are the main", "全局", "整体", "总结", "概述", "主要讲", "全部")
	addSignals(IntentCrossDocument, "across documents", "across the documents", "all documents",
		"different documents", "multiple documents", "each document", "跨文档", "多个文档", "所有文档", "分别")
	addSignals(IntentComparison, "compare", "comparison", "difference", "differences", " versus ", " vs ",
		"比较", "区别", "对比", "差异")
	addSignals(IntentMultiHop, "how does", "lead to", "leads to", "because", "cause", "affect",
		"relationship", "connected", "chain", "影响", "导致", "因为", "关系", "链路", "依赖")
	addSignals(IntentTemporal, "current", "latest", "newest", "before", "after", "as of",
		"history", "historical", "version", "when", "当前", "最新", "之前", "之后", "历史", "版本", "何时", "时间")
	addSignals(IntentLocalDocument, "this document", "the document", "in this file", "本文", "该文档", "这个文件")

	// Exact-value/default fact signal is deliberately weaker than explicit
	// intent language; it wins only when no stronger signal exists.
	for _, marker := range []string{"what is", "how much", "how many", "which", "where", "多少", "是什么", "哪个", "在哪里"} {
		if strings.Contains(normalized, marker) {
			signals[string(IntentFact)] += 2
			break
		}
	}
	if strings.Contains(normalized, "?") || strings.Contains(normalized, "？") {
		signals[string(IntentFact)]++
	}

	intent := IntentFact
	best := signals[string(IntentFact)]
	intentOrder := []QueryIntent{IntentTemporal, IntentMultiHop, IntentComparison, IntentCrossDocument,
		IntentGlobal, IntentLocalDocument, IntentFact}
	for _, candidate := range intentOrder {
		if signals[string(candidate)] > best {
			intent = candidate
			best = signals[string(candidate)]
		}
	}
	total := 0
	for _, count := range signals {
		total += count
	}
	confidence := 0.35
	if total > 0 {
		confidence = float64(best) / float64(total)
		if confidence < 0.35 {
			confidence = 0.35
		}
	}
	if confidence > 1 {
		confidence = 1
	}
	plan := QueryPlan{
		Query: strings.TrimSpace(query), NormalizedQuery: normalized, Intent: intent,
		Confidence: confidence, Signals: signals, RouterVersion: QueryRouterVersion,
	}
	applyRouting(&plan)
	return plan
}

func applyRouting(plan *QueryPlan) {
	plan.UseEvidence = true
	switch plan.Intent {
	case IntentGlobal:
		plan.PrimaryRetrieval = RetrievalHierarchy
		plan.SecondaryRetrieval = RetrievalEvidence
		plan.UseSemanticMemory = true
		plan.UseHierarchicalSummary = true
	case IntentCrossDocument, IntentComparison:
		plan.PrimaryRetrieval = RetrievalHybridLane
		plan.SecondaryRetrieval = RetrievalEvidence
		plan.UseSemanticMemory = true
		plan.UseHierarchicalSummary = true
	case IntentMultiHop:
		plan.PrimaryRetrieval = RetrievalRelation
		plan.SecondaryRetrieval = RetrievalEvidence
		plan.UseSemanticMemory = true
		plan.UseRelations = true
	case IntentTemporal:
		plan.PrimaryRetrieval = RetrievalEvidence
		plan.SecondaryRetrieval = RetrievalSemantic
		plan.UseSemanticMemory = true
	default:
		plan.PrimaryRetrieval = RetrievalEvidence
		plan.SecondaryRetrieval = RetrievalSemantic
		plan.UseSemanticMemory = true
	}
}

// Describe returns a compact deterministic explanation for diagnostics.
func (p QueryPlan) Describe() string {
	keys := make([]string, 0, len(p.Signals))
	for key := range p.Signals {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	parts := make([]string, 0, len(keys))
	for _, key := range keys {
		parts = append(parts, fmt.Sprintf("%s=%d", key, p.Signals[key]))
	}
	return p.RouterVersion + " intent=" + string(p.Intent) + " confidence=" +
		fmt.Sprintf("%.2f", p.Confidence) + " primary=" + p.PrimaryRetrieval +
		" secondary=" + p.SecondaryRetrieval + " signals=[" + strings.Join(parts, ",") + "]"
}
