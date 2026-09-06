package embedding

import (
	"context"
	"fmt"
	"math"

	"github.com/shutu-ai/shutu-knowledge/internal/runtime"
)

// LocalConfig selects a model served by the isolated helper process.
type LocalConfig struct {
	Runtime runtime.Caller
	Model   string
}

type localProvider struct {
	runtime runtime.Caller
	model   string
}

// NewLocal returns a Provider backed by a supervised helper process. It
// performs no inference inside the Knowledge process.
func NewLocal(cfg LocalConfig) Provider {
	return &localProvider{runtime: cfg.Runtime, model: cfg.Model}
}

func (p *localProvider) ModelKey() string { return "local:" + p.model }

func (p *localProvider) Embed(ctx context.Context, texts []string) ([][]float64, error) {
	if len(texts) == 0 {
		return nil, nil
	}
	if p.runtime == nil || !p.runtime.Configured(runtime.CapabilityEmbedding) {
		return nil, fmt.Errorf("local embedding runtime is not configured")
	}
	if p.model == "" {
		return nil, fmt.Errorf("local embedding model is empty")
	}
	var payload struct {
		Vectors [][]float64 `json:"vectors"`
	}
	err := p.runtime.Call(ctx, runtime.CapabilityEmbedding, map[string]any{
		"model": p.model, "texts": texts,
	}, &payload)
	if err != nil {
		return nil, fmt.Errorf("local embedding runtime: %w", err)
	}
	if len(payload.Vectors) != len(texts) {
		return nil, fmt.Errorf("local embedding returned %d vectors for %d inputs", len(payload.Vectors), len(texts))
	}
	dimensions := 0
	for _, vector := range payload.Vectors {
		if len(vector) == 0 {
			return nil, fmt.Errorf("local embedding returned an empty vector")
		}
		if dimensions == 0 {
			dimensions = len(vector)
		}
		if len(vector) != dimensions {
			return nil, fmt.Errorf("local embedding returned inconsistent dimensions")
		}
		for _, value := range vector {
			if math.IsNaN(value) || math.IsInf(value, 0) {
				return nil, fmt.Errorf("local embedding returned a non-finite value")
			}
		}
	}
	for index := range payload.Vectors {
		payload.Vectors[index] = Normalize(payload.Vectors[index])
	}
	return payload.Vectors, nil
}
