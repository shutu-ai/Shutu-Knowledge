// Package embedding abstracts embedding backends behind one interface.
// Business code never depends on a concrete model; providers return one
// L2-normalized vector per input text.
package embedding

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"math"
	"net/http"
	"strings"
	"time"

	"github.com/shutu-ai/shutu-knowledge/internal/httpx"
)

// Provider generates embeddings. ModelKey identifies the producing
// provider:model pair; vectors from different keys are never mixed.
type Provider interface {
	// Embed returns one L2-normalized vector per input, input order preserved.
	Embed(ctx context.Context, texts []string) ([][]float64, error)
	// ModelKey is the stable identity of the vector space.
	ModelKey() string
}

// ErrNoProvider is returned when the configured provider is "none".
var ErrNoProvider = fmt.Errorf("embedding provider is none; configure an endpoint or use lexical search")

// Config selects and parameterizes a provider.
type Config struct {
	Provider string // openai | ollama | none
	BaseURL  string
	Model    string
	APIKey   string
	Timeout  time.Duration
	Client   *http.Client
}

// New builds the configured provider. "none" returns a provider whose Embed
// always fails with ErrNoProvider (callers treat lexical-only as normal).
func New(cfg Config) Provider {
	if cfg.Timeout <= 0 {
		cfg.Timeout = 60 * time.Second
	}
	client := cfg.Client
	if client == nil {
		client = httpx.NewClient(cfg.Timeout)
	}
	switch cfg.Provider {
	case "openai":
		return &openAIProvider{baseURL: strings.TrimRight(cfg.BaseURL, "/"), model: cfg.Model, apiKey: cfg.APIKey, client: client}
	case "ollama":
		base := strings.TrimRight(cfg.BaseURL, "/")
		if base == "" {
			base = "http://127.0.0.1:11434"
		}
		return &ollamaProvider{baseURL: base, model: cfg.Model, client: client}
	default:
		return noneProvider{}
	}
}

type noneProvider struct{}

func (noneProvider) Embed(context.Context, []string) ([][]float64, error) { return nil, ErrNoProvider }
func (noneProvider) ModelKey() string                                     { return "none" }

// ── OpenAI-compatible ────────────────────────────────────────────────────────

type openAIProvider struct {
	baseURL string
	model   string
	apiKey  string
	client  *http.Client
}

func (p *openAIProvider) ModelKey() string { return "openai:" + p.model }

func (p *openAIProvider) Embed(ctx context.Context, texts []string) ([][]float64, error) {
	if len(texts) == 0 {
		return nil, nil
	}
	if p.model == "" {
		return nil, fmt.Errorf("embedding model is empty")
	}
	if p.baseURL == "" {
		return nil, fmt.Errorf("embedding base URL is empty")
	}
	body, err := json.Marshal(map[string]any{"model": p.model, "input": texts})
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, p.baseURL+"/embeddings", bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	if p.apiKey != "" {
		req.Header.Set("Authorization", "Bearer "+p.apiKey)
	}
	resp, err := p.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("embedding request failed: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("embedding request failed: HTTP %d", resp.StatusCode)
	}
	var payload struct {
		Data []struct {
			Index     int       `json:"index"`
			Embedding []float64 `json:"embedding"`
		} `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		return nil, fmt.Errorf("embedding response decode: %w", err)
	}
	if len(payload.Data) != len(texts) {
		return nil, fmt.Errorf("embedding response did not return one vector per input")
	}
	out := make([][]float64, len(texts))
	for _, entry := range payload.Data {
		if entry.Index < 0 || entry.Index >= len(out) || len(entry.Embedding) == 0 {
			return nil, fmt.Errorf("embedding response is malformed")
		}
		out[entry.Index] = Normalize(entry.Embedding)
	}
	return out, nil
}

// ── Ollama ───────────────────────────────────────────────────────────────────

type ollamaProvider struct {
	baseURL string
	model   string
	client  *http.Client
}

func (p *ollamaProvider) ModelKey() string { return "ollama:" + p.model }

func (p *ollamaProvider) Embed(ctx context.Context, texts []string) ([][]float64, error) {
	if len(texts) == 0 {
		return nil, nil
	}
	if p.model == "" {
		return nil, fmt.Errorf("embedding model is empty")
	}
	body, err := json.Marshal(map[string]any{"model": p.model, "input": texts})
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, p.baseURL+"/api/embed", bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := p.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("ollama embedding failed: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusOK {
		var payload struct {
			Embeddings [][]float64 `json:"embeddings"`
		}
		if err := json.NewDecoder(resp.Body).Decode(&payload); err == nil && len(payload.Embeddings) == len(texts) {
			out := make([][]float64, len(texts))
			for i, v := range payload.Embeddings {
				out[i] = Normalize(v)
			}
			return out, nil
		}
	}
	// Legacy endpoint: one prompt per call.
	out := make([][]float64, 0, len(texts))
	for _, text := range texts {
		legacyBody, _ := json.Marshal(map[string]any{"model": p.model, "prompt": text})
		legacyReq, err := http.NewRequestWithContext(ctx, http.MethodPost, p.baseURL+"/api/embeddings", bytes.NewReader(legacyBody))
		if err != nil {
			return nil, err
		}
		legacyReq.Header.Set("Content-Type", "application/json")
		legacyResp, err := p.client.Do(legacyReq)
		if err != nil {
			return nil, fmt.Errorf("ollama embedding failed: %w", err)
		}
		var payload struct {
			Embedding []float64 `json:"embedding"`
		}
		err = json.NewDecoder(legacyResp.Body).Decode(&payload)
		_ = legacyResp.Body.Close()
		if err != nil || len(payload.Embedding) == 0 {
			return nil, fmt.Errorf("ollama embedding response missing a vector")
		}
		out = append(out, Normalize(payload.Embedding))
	}
	return out, nil
}

// Normalize L2-normalizes a vector (zero vectors pass through unchanged).
func Normalize(vector []float64) []float64 {
	var sum float64
	for _, value := range vector {
		sum += value * value
	}
	length := math.Sqrt(sum)
	if length == 0 {
		return vector
	}
	out := make([]float64, len(vector))
	for i, value := range vector {
		out[i] = value / length
	}
	return out
}
