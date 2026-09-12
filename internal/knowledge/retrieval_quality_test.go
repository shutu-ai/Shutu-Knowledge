package knowledge

import (
	"context"
	"strings"
	"testing"
)

type qualityReranker struct{}

func (qualityReranker) Rerank(_ context.Context, _ string, texts []string) ([]float64, error) {
	scores := make([]float64, len(texts))
	for i, text := range texts {
		switch {
		case strings.Contains(text, "AI4App"):
			scores[i] = 0.95
		case strings.Contains(text, "Code Agent"):
			scores[i] = 0.65
		default:
			scores[i] = 0.10
		}
	}
	return scores, nil
}

func (qualityReranker) ModelKey() string { return "rerank:quality-fixture" }

func TestGDERAGGoldenRegression(t *testing.T) {
	service, _ := newSearchFixture(t)
	// The fixture intentionally keeps distractor documents that mention the
	// product, so this tests answer-bearing chunk ranking rather than document
	// existence alone.
	base, err := service.CreateBase("GDE 26.3", "golden retrieval corpus", "", BaseConfig{})
	if err != nil {
		t.Fatal(err)
	}
	guide := "# Code Agent编排指南\n\n26.3版本中Code Agent整体规划了以下能力：AI4App辅助应用开发；AI4Model辅助模型编排；AI4UI辅助UI编排；AI4Script辅助脚本编排；AI4Flow辅助流程与服务编排；AI4Assist智能知识问答。"
	for title, text := range map[string]string{
		"GDE_26.3.0_Code Agent编排指南.pdf": guide,
		"GDE_26.3.0_GDE产品描述.pdf":        "GDE 26.3产品描述提到 Code Agent，但不列出能力定义。",
		"GDE_26.3.0_快速入门.pdf":           "快速入门介绍 GDE 的基本使用流程。",
		"GDE_26.3.0_API Fabric功能描述.pdf": "API Fabric提供接口能力。",
	} {
		if _, err := service.AddTextDocument(context.Background(), base.ID, title, text); err != nil {
			t.Fatal(err)
		}
	}
	cases := []struct {
		id    string
		query string
	}{
		{"GDE-RAG-001", "Code Agent有哪些能力？"},
		{"GDE-RAG-002", "Code Agent整体规划了哪些能力？"},
		{"GDE-RAG-003", "AI4App、AI4Model、AI4UI、AI4Script、AI4Flow、AI4Assist分别是什么？"},
		{"GDE-RAG-004", "26.3版本中Code Agent有哪些能力？"},
	}
	for _, tc := range cases {
		result, err := service.Search(context.Background(), SearchRequest{Query: tc.query, Mode: "lexical", TopK: 4, Debug: true})
		if err != nil {
			t.Fatalf("%s search: %v", tc.id, err)
		}
		if result.Total == 0 || result.Hits[0].DocumentTitle != "GDE_26.3.0_Code Agent编排指南.pdf" {
			t.Fatalf("%s answer-bearing chunk not top1: %+v", tc.id, result.Hits)
		}
		if result.Diagnostics == nil || len(result.Diagnostics.BM25) == 0 || len(result.Diagnostics.Final) == 0 {
			t.Fatalf("%s missing diagnostic stages: %+v", tc.id, result.Diagnostics)
		}
	}
}

func TestMMRUsesRelevanceAfterRerank(t *testing.T) {
	service, _ := newSearchFixture(t)
	service.global.Retrieval.MMR = true
	service.global.Retrieval.MMRDiversity = 0.75
	base, err := service.CreateBase("MMR", "", "", BaseConfig{})
	if err != nil {
		t.Fatal(err)
	}
	for title, text := range map[string]string{
		"answer-guide":  "Code Agent AI4App answer-bearing content",
		"related-guide": "Code Agent related background",
		"weak-guide":    "Code Agent incidental mention",
	} {
		if _, err := service.AddTextDocument(context.Background(), base.ID, title, text); err != nil {
			t.Fatal(err)
		}
	}
	service.SetProviders(nil, qualityReranker{})
	result, err := service.Search(context.Background(), SearchRequest{
		Query: "Code Agent", TopK: 3, Mode: "hybrid", MMR: true, Debug: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Total == 0 || result.Hits[0].DocumentTitle != "answer-guide" {
		t.Fatalf("MMR displaced answer-bearing chunk: %+v", result.Hits)
	}
	if result.Diagnostics == nil || len(result.Diagnostics.MMRInput) == 0 || len(result.Diagnostics.MMROutput) == 0 {
		t.Fatalf("MMR diagnostics missing: %+v", result.Diagnostics)
	}
}

func TestNegativeRetrievalDoesNotFabricateLexicalContext(t *testing.T) {
	fixture := newFixture(t)
	base, err := fixture.service.CreateBase("GDE", "", "", BaseConfig{})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.service.AddTextDocument(context.Background(), base.ID, "Product", "API Fabric and data governance documentation"); err != nil {
		t.Fatal(err)
	}
	for _, query := range []string{"GDE如何配置5G AMF Registration Reject？", "GDE是否支持量子计算调度？"} {
		result, err := fixture.service.Search(context.Background(), SearchRequest{Query: query, Mode: "lexical", TopK: 4, Debug: true})
		if err != nil {
			t.Fatal(err)
		}
		if result.Total != 0 || result.Diagnostics == nil || len(result.Diagnostics.Final) != 0 {
			t.Fatalf("negative query fabricated evidence: %q %+v", query, result)
		}
	}
}

func TestNegativeVectorRetrievalDoesNotFillTopK(t *testing.T) {
	service, _ := newSearchFixture(t)
	base, err := service.CreateBase("GDE", "", "", BaseConfig{})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.AddTextDocument(context.Background(), base.ID, "Database", "database storage rows"); err != nil {
		t.Fatal(err)
	}
	result, err := service.Search(context.Background(), SearchRequest{Query: "cooking", Mode: "vector", TopK: 4, Debug: true})
	if err != nil {
		t.Fatal(err)
	}
	if result.Total != 0 {
		t.Fatalf("weak vector evidence filled TopK: %+v", result)
	}
}
