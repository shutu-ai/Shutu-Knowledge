package rerank

import (
	"context"
	"testing"

	"github.com/shutu-ai/shutu-knowledge/internal/runtime"
)

type fakeRuntime struct {
	scores []float64
}

func (f *fakeRuntime) Configured(capability string) bool {
	return capability == runtime.CapabilityRerank
}

func (f *fakeRuntime) Call(_ context.Context, capability string, _ any, out any) error {
	if capability != runtime.CapabilityRerank {
		panic("unsupported capability " + capability)
	}
	target := out.(*struct {
		Scores []float64 `json:"scores"`
	})
	target.Scores = f.scores
	return nil
}

func TestLocalRerankMapsAndValidatesScores(t *testing.T) {
	provider := NewLocal(&fakeRuntime{scores: []float64{0.25, 0.75}}, "bge")
	scores, err := provider.Rerank(context.Background(), "q", []string{"a", "b"})
	if err != nil || len(scores) != 2 || scores[0] != 0.25 || scores[1] != 0.75 {
		t.Fatalf("local rerank: %v %v", scores, err)
	}
	if provider.ModelKey() != "local-rerank:bge" {
		t.Fatalf("model key: %s", provider.ModelKey())
	}
	invalid := NewLocal(&fakeRuntime{scores: []float64{1.5}}, "bge")
	_, err = invalid.Rerank(context.Background(), "q", []string{"a"})
	if err == nil || !containsCode(err.Error(), CodeInvalidResponse) {
		t.Fatalf("range validation: %v", err)
	}
}
