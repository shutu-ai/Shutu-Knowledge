package knowledge

import (
	"context"
	"errors"
	"testing"

	"github.com/shutu-ai/shutu-knowledge/internal/runtime"
)

type fakeKnowledgeRuntime struct {
	fail bool
}

func (f *fakeKnowledgeRuntime) Configured(capability string) bool {
	return capability == runtime.CapabilityEmbedding || capability == runtime.CapabilityOCR
}

func (f *fakeKnowledgeRuntime) Call(_ context.Context, capability string, params any, out any) error {
	if f.fail {
		return &runtime.Error{Code: "model_error", Message: "helper unavailable"}
	}
	switch capability {
	case runtime.CapabilityEmbedding:
		request := params.(map[string]any)
		if request["model"] != "qwen3" {
			panic("unexpected model " + request["model"].(string))
		}
		target := out.(*struct {
			Vectors [][]float64 `json:"vectors"`
		})
		target.Vectors = [][]float64{{0, 2}}
	case runtime.CapabilityOCR:
		target := out.(*struct {
			Text string `json:"text"`
		})
		target.Text = "ocr text"
	default:
		return errors.New("unexpected capability")
	}
	return nil
}

func TestRuntimeProvidersAndOCRAreWired(t *testing.T) {
	f := newFixture(t)
	helper := &fakeKnowledgeRuntime{}
	f.service.SetRuntime(helper)
	f.service.global.Embedding.Provider = "local"
	f.service.global.Embedding.Model = "qwen3"
	f.service.applyConfiguredProviders()

	dimensions, err := f.service.ProbeEmbeddingDimensions(context.Background(), "local", "", "qwen3", "")
	if err != nil || dimensions != 2 {
		t.Fatalf("local probe: %d %v", dimensions, err)
	}
	if got := f.service.EmbeddingModelKey(); got != "local:qwen3" {
		t.Fatalf("model key: %q", got)
	}
	if f.service.ocr == nil || !f.service.ocr.Available() {
		t.Fatal("OCR runtime was not attached")
	}

	f.service.SetRuntime(&fakeKnowledgeRuntime{fail: true})
	_, err = f.service.ProbeEmbeddingDimensions(context.Background(), "local", "", "qwen3", "")
	if err == nil || err.Error() != "local embedding runtime: model_error: helper unavailable" {
		t.Fatalf("expected isolated runtime error, got %v", err)
	}
}
