package embedding

import (
	"context"
	"testing"

	"github.com/shutu-ai/shutu-knowledge/internal/runtime"
)

type fakeRuntime struct {
	request map[string]any
	vectors [][]float64
}

func (f *fakeRuntime) Configured(capability string) bool {
	return capability == runtime.CapabilityEmbedding
}

func (f *fakeRuntime) Call(_ context.Context, capability string, params, out any) error {
	if capability != runtime.CapabilityEmbedding {
		panic("unsupported capability " + capability)
	}
	f.request = params.(map[string]any)
	target := out.(*struct {
		Vectors [][]float64 `json:"vectors"`
	})
	target.Vectors = f.vectors
	return nil
}

func TestLocalEmbeddingContractAndValidation(t *testing.T) {
	helper := &fakeRuntime{vectors: [][]float64{{0, 2}, {2, 0}}}
	provider := NewLocal(LocalConfig{Runtime: helper, Model: "qwen3"})
	vectors, err := provider.Embed(context.Background(), []string{"a", "b"})
	if err != nil || len(vectors) != 2 || vectors[0][1] != 1 || vectors[1][0] != 1 {
		t.Fatalf("local embedding: %v %v", vectors, err)
	}
	if provider.ModelKey() != "local:qwen3" {
		t.Fatalf("model key: %s", provider.ModelKey())
	}
	if helper.request["model"] != "qwen3" {
		t.Fatalf("request model: %v", helper.request["model"])
	}

	misshaped := &fakeRuntime{vectors: [][]float64{{1}}}
	if _, err := NewLocal(LocalConfig{Runtime: misshaped, Model: "m"}).Embed(context.Background(), []string{"a", "b"}); err == nil {
		t.Fatal("expected vector count validation")
	}
}
