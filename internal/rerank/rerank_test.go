package rerank

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"
)

func TestRemoteRerankMapsScores(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		if body["query"] != "q" {
			t.Fatalf("query: %v", body["query"])
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"results": []map[string]any{
				{"index": 1, "relevance_score": 0.9},
				{"index": 0, "relevance_score": 0.4},
			},
		})
	}))
	defer server.Close()
	provider := New(Config{BaseURL: server.URL, Model: "bge", Client: server.Client()})
	scores, err := provider.Rerank(context.Background(), "q", []string{"a", "b"})
	if err != nil || len(scores) != 2 || scores[0] != 0.4 || scores[1] != 0.9 {
		t.Fatalf("scores: %v %v", scores, err)
	}
}

func TestRemoteRerankValidation(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"results": []map[string]any{{"index": 0, "relevance_score": 0.5}}})
	}))
	defer server.Close()
	provider := New(Config{BaseURL: server.URL, Model: "m", Client: server.Client()})
	_, err := provider.Rerank(context.Background(), "q", []string{"a", "b"})
	if err == nil || !containsCode(err.Error(), CodeInvalidResponse) {
		t.Fatalf("expected invalid_response, got %v", err)
	}
	bad := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"results": []map[string]any{{"index": 0, "relevance_score": 1.5}}})
	}))
	defer bad.Close()
	provider = New(Config{BaseURL: bad.URL, Model: "m", Client: bad.Client()})
	if _, err := provider.Rerank(context.Background(), "q", []string{"a"}); err == nil || !containsCode(err.Error(), CodeInvalidResponse) {
		t.Fatalf("expected invalid_response for range, got %v", err)
	}
}

func TestCircuitBreakerTripsAndRecovers(t *testing.T) {
	var failures atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		if failures.Load() < 3 {
			failures.Add(1)
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"results": []map[string]any{{"index": 0, "relevance_score": 0.5}}})
	}))
	defer server.Close()
	provider := New(Config{BaseURL: server.URL, Model: "m", Client: server.Client(), FailureThreshold: 3, OpenDuration: 30 * time.Millisecond})
	ctx := context.Background()
	for i := 0; i < 3; i++ {
		if _, err := provider.Rerank(ctx, "q", []string{"a"}); err == nil {
			t.Fatal("expected failure")
		}
	}
	if _, err := provider.Rerank(ctx, "q", []string{"a"}); err == nil || !containsCode(err.Error(), CodeCircuitOpen) {
		t.Fatalf("expected circuit_open, got %v", err)
	}
	time.Sleep(40 * time.Millisecond)
	scores, err := provider.Rerank(ctx, "q", []string{"a"})
	if err != nil || len(scores) != 1 || scores[0] != 0.5 {
		t.Fatalf("half-open recovery: %v %v", scores, err)
	}
}

func containsCode(haystack, needle string) bool {
	for i := 0; i+len(needle) <= len(haystack); i++ {
		if haystack[i:i+len(needle)] == needle {
			return true
		}
	}
	return false
}
