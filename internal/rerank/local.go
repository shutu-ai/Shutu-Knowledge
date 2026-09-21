package rerank

import (
	"context"
	"fmt"
	"math"
	"time"

	"github.com/shutu-ai/shutu-knowledge/internal/runtime"
)

type localProvider struct {
	runtime runtime.Caller
	model   string
}

// NewLocal returns a helper-process reranker protected by the same circuit
// breaker as the remote provider. Local model failures degrade search.
func NewLocal(manager runtime.Caller, model string) Provider {
	return &breaker{inner: &localProvider{runtime: manager, model: model}, threshold: 3, openDuration: 5 * time.Minute}
}

func (p *localProvider) ModelKey() string { return "local-rerank:" + p.model }

func (p *localProvider) Rerank(ctx context.Context, query string, texts []string) ([]float64, error) {
	if len(texts) == 0 {
		return nil, nil
	}
	if p.runtime == nil || !p.runtime.Configured(runtime.CapabilityRerank) {
		return nil, &Error{Code: CodeDisabled, Message: "local rerank runtime is not configured"}
	}
	if p.model == "" {
		return nil, &Error{Code: CodeDisabled, Message: "local rerank model is empty"}
	}
	// A 60-candidate cross-encoder request can allocate one very large
	// padded ONNX tensor and dominate the search deadline. Keep native memory
	// bounded with small, sequential micro-batches; candidate order is stable.
	const batchSize = 8
	scores := make([]float64, 0, len(texts))
	for start := 0; start < len(texts); start += batchSize {
		if err := ctx.Err(); err != nil {
			return nil, &Error{Code: CodeTimeout, Message: err.Error(), Retryable: true}
		}
		end := start + batchSize
		if end > len(texts) {
			end = len(texts)
		}
		batch := texts[start:end]
		var payload struct {
			Scores []float64 `json:"scores"`
		}
		err := p.runtime.Call(ctx, runtime.CapabilityRerank, map[string]any{
			"model": p.model, "query": query, "documents": batch,
		}, &payload)
		if err != nil {
			return nil, &Error{Code: CodeProviderError, Message: err.Error(), Retryable: true}
		}
		if len(payload.Scores) != len(batch) {
			return nil, &Error{Code: CodeInvalidResponse, Message: fmt.Sprintf("expected %d scores, got %d", len(batch), len(payload.Scores))}
		}
		for _, score := range payload.Scores {
			if math.IsNaN(score) || score < 0 || score > 1 {
				return nil, &Error{Code: CodeInvalidResponse, Message: "score out of range"}
			}
			scores = append(scores, score)
		}
	}
	return scores, nil
}
