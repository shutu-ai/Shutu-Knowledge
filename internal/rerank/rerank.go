// Package rerank provides the optional reranker step. Failures degrade to
// the original recall order: a reranker must never take Knowledge search
// down. Strict response validation and a small circuit breaker protect the
// latency budget against broken or hung providers.
package rerank

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"math"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/shutu-ai/shutu-knowledge/internal/httpx"
)

// Error codes surfaced in structured search diagnostics.
const (
	CodeProviderError   = "provider_error"
	CodeTimeout         = "timeout"
	CodeInvalidResponse = "invalid_response"
	CodeCircuitOpen     = "circuit_open"
	CodeDisabled        = "model_not_configured"
)

// Provider re-scores candidate texts against one query, index-aligned.
type Provider interface {
	Rerank(ctx context.Context, query string, texts []string) ([]float64, error)
	ModelKey() string
}

// Error carries a stable code plus retryability for structured status.
type Error struct {
	Code      string
	Message   string
	Retryable bool
}

func (e *Error) Error() string { return e.Code + ": " + e.Message }

// Config selects the remote provider.
type Config struct {
	BaseURL string
	Model   string
	APIKey  string
	Timeout time.Duration
	Client  *http.Client
	// Breaker tuning (defaults: 3 failures -> 5 min open).
	FailureThreshold int
	OpenDuration     time.Duration
}

// New builds the remote reranker wrapped in a circuit breaker.
func New(cfg Config) Provider {
	if cfg.Timeout <= 0 {
		cfg.Timeout = 60 * time.Second
	}
	threshold := cfg.FailureThreshold
	if threshold <= 0 {
		threshold = 3
	}
	openDuration := cfg.OpenDuration
	if openDuration <= 0 {
		openDuration = 5 * time.Minute
	}
	client := cfg.Client
	if client == nil {
		client = httpx.NewClient(cfg.Timeout)
	}
	remote := &remoteProvider{baseURL: strings.TrimRight(cfg.BaseURL, "/"), model: cfg.Model, apiKey: cfg.APIKey, client: client, timeout: cfg.Timeout}
	return &breaker{inner: remote, threshold: threshold, openDuration: openDuration}
}

type remoteProvider struct {
	baseURL string
	model   string
	apiKey  string
	client  *http.Client
	timeout time.Duration
}

func (p *remoteProvider) ModelKey() string { return "rerank:" + p.model }

func (p *remoteProvider) Rerank(ctx context.Context, query string, texts []string) ([]float64, error) {
	if len(texts) == 0 {
		return nil, nil
	}
	if p.model == "" {
		return nil, &Error{Code: CodeDisabled, Message: "rerank model is empty"}
	}
	if p.baseURL == "" {
		return nil, &Error{Code: CodeDisabled, Message: "rerank base URL is empty"}
	}
	body, err := json.Marshal(map[string]any{
		"model":            p.model,
		"query":            query,
		"documents":        texts,
		"top_n":            len(texts),
		"return_documents": false,
	})
	if err != nil {
		return nil, &Error{Code: CodeProviderError, Message: err.Error()}
	}
	callCtx, cancel := context.WithTimeout(ctx, p.timeout)
	defer cancel()
	req, err := http.NewRequestWithContext(callCtx, http.MethodPost, p.baseURL+"/rerank", bytes.NewReader(body))
	if err != nil {
		return nil, &Error{Code: CodeProviderError, Message: err.Error()}
	}
	req.Header.Set("Content-Type", "application/json")
	if p.apiKey != "" {
		req.Header.Set("Authorization", "Bearer "+p.apiKey)
	}
	resp, err := p.client.Do(req)
	if err != nil {
		if callCtx.Err() != nil {
			return nil, &Error{Code: CodeTimeout, Message: callCtx.Err().Error(), Retryable: true}
		}
		return nil, &Error{Code: CodeProviderError, Message: err.Error(), Retryable: true}
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, &Error{Code: CodeProviderError, Message: fmt.Sprintf("HTTP %d", resp.StatusCode), Retryable: true}
	}
	var payload struct {
		Results []struct {
			Index          int     `json:"index"`
			RelevanceScore float64 `json:"relevance_score"`
			Score          float64 `json:"score"`
		} `json:"results"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		return nil, &Error{Code: CodeInvalidResponse, Message: "decode: " + err.Error()}
	}
	if len(payload.Results) != len(texts) {
		return nil, &Error{Code: CodeInvalidResponse, Message: fmt.Sprintf("expected %d scores, got %d", len(texts), len(payload.Results))}
	}
	scores := make([]float64, len(texts))
	for _, result := range payload.Results {
		score := result.RelevanceScore
		if score == 0 {
			score = result.Score
		}
		if result.Index < 0 || result.Index >= len(scores) || math.IsNaN(score) || score < 0 || score > 1 {
			return nil, &Error{Code: CodeInvalidResponse, Message: "score out of range or index invalid"}
		}
		scores[result.Index] = score
	}
	return scores, nil
}

// breaker trips after consecutive failures and half-opens after the open
// window; success resets the counter.
type breaker struct {
	inner        Provider
	mu           sync.Mutex
	consecutive  int
	openUntil    time.Time
	threshold    int
	openDuration time.Duration
}

func (b *breaker) ModelKey() string { return b.inner.ModelKey() }

func (b *breaker) Rerank(ctx context.Context, query string, texts []string) ([]float64, error) {
	b.mu.Lock()
	if time.Now().Before(b.openUntil) {
		b.mu.Unlock()
		return nil, &Error{Code: CodeCircuitOpen, Message: "reranker circuit is open", Retryable: true}
	}
	b.mu.Unlock()

	scores, err := b.inner.Rerank(ctx, query, texts)
	b.mu.Lock()
	defer b.mu.Unlock()
	if err != nil {
		b.consecutive++
		if b.consecutive >= b.threshold {
			b.openUntil = time.Now().Add(b.openDuration)
			b.consecutive = 0
		}
		return scores, err
	}
	b.consecutive = 0
	return scores, nil
}
