package embedding

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestOpenAIProviderMapsIndexesAndNormalizes(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/embeddings" {
			t.Fatalf("path: %s", r.URL.Path)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"data": []map[string]any{
				{"index": 1, "embedding": []float64{3, 0}},
				{"index": 0, "embedding": []float64{0, 2}},
			},
		})
	}))
	defer server.Close()
	provider := New(Config{Provider: "openai", BaseURL: server.URL, Model: "text-embed", APIKey: "k", Client: server.Client()})
	vectors, err := provider.Embed(context.Background(), []string{"a", "b"})
	if err != nil {
		t.Fatal(err)
	}
	if len(vectors) != 2 || vectors[0][1] != 1 || vectors[1][0] != 1 {
		t.Fatalf("vectors: %v", vectors)
	}
	if provider.ModelKey() != "openai:text-embed" {
		t.Fatalf("model key: %s", provider.ModelKey())
	}
}

func TestOpenAIProviderErrors(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte("UPSTREAM-SECRET"))
	}))
	defer server.Close()
	provider := New(Config{Provider: "openai", BaseURL: server.URL, Model: "m", Client: server.Client()})
	_, err := provider.Embed(context.Background(), []string{"a"})
	if err == nil {
		t.Fatal("expected HTTP error")
	}
	if !strings.Contains(err.Error(), "HTTP 500") || strings.Contains(err.Error(), "UPSTREAM-SECRET") {
		t.Fatalf("provider error must expose status only: %v", err)
	}
	mismatch := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"data": []map[string]any{{"index": 0, "embedding": []float64{1}}}})
	}))
	defer mismatch.Close()
	provider = New(Config{Provider: "openai", BaseURL: mismatch.URL, Model: "m", Client: mismatch.Client()})
	if _, err := provider.Embed(context.Background(), []string{"a", "b"}); err == nil {
		t.Fatal("expected count mismatch error")
	}
}

func TestOllamaModernAndLegacy(t *testing.T) {
	legacyCalls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/embed":
			w.WriteHeader(http.StatusNotFound)
		case "/api/embeddings":
			legacyCalls++
			_ = json.NewEncoder(w).Encode(map[string]any{"embedding": []float64{1, 1}})
		}
	}))
	defer server.Close()
	provider := New(Config{Provider: "ollama", BaseURL: server.URL, Model: "nomic", Client: server.Client()})
	vectors, err := provider.Embed(context.Background(), []string{"a", "b"})
	if err != nil || len(vectors) != 2 || legacyCalls != 2 {
		t.Fatalf("legacy fallback: %v %v calls=%d", vectors, err, legacyCalls)
	}
	if provider.ModelKey() != "ollama:nomic" {
		t.Fatalf("model key: %s", provider.ModelKey())
	}
}

func TestNoneProviderFailsClosed(t *testing.T) {
	provider := New(Config{Provider: "none"})
	if _, err := provider.Embed(context.Background(), []string{"a"}); err != ErrNoProvider {
		t.Fatalf("expected ErrNoProvider, got %v", err)
	}
}
