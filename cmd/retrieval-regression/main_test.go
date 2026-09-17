package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestValidateCaseUsesDefaultAndBoundsTopK(t *testing.T) {
	test := testCase{ID: "lexical-1", Query: "  retention  "}
	if err := validateCase(&test, "base-a"); err != nil {
		t.Fatal(err)
	}
	if test.Query != "retention" || test.BaseID != "base-a" || test.Mode != "hybrid" || test.TopK != 10 {
		t.Fatalf("normalized case = %+v", test)
	}
	tooLarge := testCase{ID: "large", Query: "q", TopK: 51}
	if err := validateCase(&tooLarge, "base-a"); err == nil {
		t.Fatal("topK > 50 unexpectedly accepted")
	}
}

func TestRetrievalScoresUseBestExpectedRank(t *testing.T) {
	hitAt1, hitAt3, mrr := retrievalScores(map[string]int{"doc-a": 4, "doc-b": 2}, 4)
	if hitAt1 != 0 || hitAt3 != 1 || mrr != 0.5 {
		t.Fatalf("scores = %v/%v/%v", hitAt1, hitAt3, mrr)
	}
}

func TestRunCaseCapturesVersionsDiagnosticsAndDependentChecksWithoutContent(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set("Content-Type", "application/json")
		switch {
		case request.Method == http.MethodPost && request.URL.Path == "/api/search":
			_, _ = writer.Write([]byte(`{"ok":true,"value":{"mode":"hybrid","total":1,"rerank":{"provider":"local","model":"reranker-v1","status":"applied","attempted":true,"applied":true,"candidateCount":1},"generations":[{"docId":"doc-a","baseId":"base-a","sourceVersion":7,"indexGeneration":3}],"hits":[{"chunkId":"chunk-a","docId":"doc-a","baseId":"base-a","index":0,"indexGeneration":3,"sourceVersion":7,"text":"PRIVATE SEARCH CONTENT","score":0.91}],"diagnostics":{"bm25":[{"chunkId":"chunk-a","docId":"doc-a","baseId":"base-a","bm25Rank":1,"bm25Score":2.1}],"vector":[],"rrf":[],"final":[{"chunkId":"chunk-a","docId":"doc-a","baseId":"base-a","finalRank":1}]}}}`))
		case request.Method == http.MethodGet && request.URL.Path == "/api/documents/doc-a/context":
			_, _ = writer.Write([]byte(`{"ok":true,"value":{"before":[],"anchor":{"text":"PRIVATE CONTEXT"},"after":[]}}`))
		case request.Method == http.MethodGet && request.URL.Path == "/api/documents/doc-a/raw":
			writer.Header().Set("X-Index-Generation", "3")
			writer.Header().Set("X-Source-Version", "7")
			_, _ = writer.Write([]byte("PRIVATE RAW CONTENT"))
		default:
			http.NotFound(writer, request)
		}
	}))
	defer server.Close()

	c := &client{baseURL: server.URL, http: server.Client(), timeout: time.Second}
	result := c.runCase(context.Background(), testCase{
		ID: "hybrid-1", Query: "private query", BaseID: "base-a", Mode: "hybrid", TopK: 4,
		ExpectedDocIDs: []string{"doc-a"}, NegativeDocIDs: []string{"doc-negative"}, Context: true, RawCitation: true,
	}, "")
	if result.Outcome != "passed" || result.Expected["doc-a"] != 1 || result.Hits[0].IndexGeneration != 3 || result.Hits[0].SourceVersion != 7 {
		t.Fatalf("case result = %+v", result)
	}
	if result.Rerank.Model != "reranker-v1" || result.DiagnosticCounts["bm25"] != 1 || result.Context.Outcome != "passed" || result.RawCitation.Outcome != "passed" || result.RawCitation.Generation != 3 || result.RawCitation.SourceVersion != 7 {
		t.Fatalf("metadata result = %+v", result)
	}
	encoded, err := json.Marshal(result)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(encoded), "private query") || strings.Contains(string(encoded), "PRIVATE") {
		t.Fatalf("query/content leaked into report: %s", encoded)
	}
}

func TestRunCaseFailsOnNegativeHit(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set("Content-Type", "application/json")
		_, _ = writer.Write([]byte(`{"ok":true,"value":{"total":1,"hits":[{"docId":"bad","baseId":"base-a"}]}}`))
	}))
	defer server.Close()
	c := &client{baseURL: server.URL, http: server.Client(), timeout: time.Second}
	result := c.runCase(context.Background(), testCase{ID: "negative", Query: "q", BaseID: "base-a", ExpectedDocIDs: []string{}, NegativeDocIDs: []string{"bad"}}, "")
	if result.Outcome != "failed" || len(result.NegativeHits) != 1 {
		t.Fatalf("negative result = %+v", result)
	}
}
