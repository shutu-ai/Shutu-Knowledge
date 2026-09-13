package knowledge

import (
	"context"
	"encoding/json"
	"sync"
	"testing"

	"github.com/shutu-ai/shutu-knowledge/internal/config"
	"github.com/shutu-ai/shutu-knowledge/internal/runtime"
)

type recordingBaseRuntime struct {
	mu    sync.Mutex
	calls []struct {
		capability string
		model      string
	}
}

func (r *recordingBaseRuntime) Configured(capability string) bool {
	return capability == runtime.CapabilityEmbedding || capability == runtime.CapabilityRerank
}

func (r *recordingBaseRuntime) Call(_ context.Context, capability string, params any, out any) error {
	request := params.(map[string]any)
	model, _ := request["model"].(string)
	r.mu.Lock()
	r.calls = append(r.calls, struct {
		capability string
		model      string
	}{capability: capability, model: model})
	r.mu.Unlock()

	var response any
	switch capability {
	case runtime.CapabilityEmbedding:
		response = map[string]any{"vectors": [][]float64{{1, 0, 0}}}
	case runtime.CapabilityRerank:
		documents, _ := request["documents"].([]string)
		scores := make([]float64, len(documents))
		for i := range scores {
			scores[i] = 0.8 - float64(i)*0.1
		}
		response = map[string]any{"scores": scores}
	default:
		return &runtime.Error{Code: "unsupported", Message: capability}
	}
	data, err := json.Marshal(response)
	if err != nil {
		return err
	}
	return json.Unmarshal(data, out)
}

func (r *recordingBaseRuntime) modelsFor(capability string) []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	var models []string
	for _, call := range r.calls {
		if call.capability == capability {
			models = append(models, call.model)
		}
	}
	return models
}

func TestKnowledgeBaseModelOverridesAreUsedForIngestAndSearch(t *testing.T) {
	f := newFixture(t)
	helper := &recordingBaseRuntime{}
	f.service.SetRuntime(helper)

	base, err := f.service.CreateBase("Local models", "", "", BaseConfig{
		EmbeddingProvider: "local",
		EmbeddingModel:    "base-embedding",
		RerankEnabled:     boolRef(true),
		RerankModel:       "base-rerank",
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.service.AddTextDocument(context.Background(), base.ID, "first", "alpha answer"); err != nil {
		t.Fatal(err)
	}
	if _, err := f.service.AddTextDocument(context.Background(), base.ID, "second", "beta answer"); err != nil {
		t.Fatal(err)
	}

	counts, err := f.service.store.VectorModelCounts(base.ID)
	if err != nil {
		t.Fatal(err)
	}
	if counts["local:base-embedding"] != 2 {
		t.Fatalf("base vectors were not stored under the override model: %+v", counts)
	}
	documents, err := f.service.ListDocuments(base.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(documents) != 2 {
		t.Fatalf("document summaries: %+v", documents)
	}
	for _, document := range documents {
		if !document.EmbeddingReady {
			t.Fatalf("document did not report embedding readiness: %+v", document)
		}
	}

	lexicalBase, err := f.service.CreateBase("Lexical only", "", "", BaseConfig{})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.service.AddTextDocument(context.Background(), lexicalBase.ID, "lexical", "no vector"); err != nil {
		t.Fatal(err)
	}
	lexicalDocuments, err := f.service.ListDocuments(lexicalBase.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(lexicalDocuments) != 1 || lexicalDocuments[0].EmbeddingReady {
		t.Fatalf("lexical document incorrectly reported embedding readiness: %+v", lexicalDocuments)
	}
	for _, model := range helper.modelsFor(runtime.CapabilityEmbedding) {
		if model != "base-embedding" {
			t.Fatalf("unexpected embedding model: %q", model)
		}
	}

	result, err := f.service.Search(context.Background(), SearchRequest{
		Query: "alpha", BaseID: base.ID, Mode: "hybrid", TopK: 2,
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Mode != "hybrid" || !result.Reranked || result.Rerank == nil || !result.Rerank.Applied || result.Rerank.Model != "local-rerank:base-rerank" {
		t.Fatalf("base model search was not applied: %+v", result)
	}
	for _, model := range helper.modelsFor(runtime.CapabilityRerank) {
		if model != "base-rerank" {
			t.Fatalf("unexpected rerank model: %q", model)
		}
	}
}

func TestResolveBaseConfigUsesGlobalOnlyWhenBaseDoesNotOverride(t *testing.T) {
	global := configForModelOverrideTest()
	local := ResolveBaseConfig(global, BaseConfig{
		EmbeddingProvider: "local",
		EmbeddingModel:    "base-embedding",
		RerankEnabled:     boolRef(true),
		RerankModel:       "base-rerank",
	})
	if local.EmbeddingProvider != "local" || local.EmbeddingModel != "base-embedding" || local.EmbeddingBaseURL != "" || local.RerankModel != "base-rerank" || local.RerankBaseURL != "" || local.RerankEnabled == nil || !*local.RerankEnabled {
		t.Fatalf("base overrides were not preserved: %+v", local)
	}
	inherited := ResolveBaseConfig(global, BaseConfig{})
	if inherited.EmbeddingProvider != "openai" || inherited.EmbeddingModel != "global-embedding" || inherited.EmbeddingBaseURL != "http://embedding" || inherited.RerankModel != "global-rerank" || inherited.RerankBaseURL != "http://rerank" || inherited.RerankEnabled == nil || !*inherited.RerankEnabled {
		t.Fatalf("global defaults were not inherited: %+v", inherited)
	}
}

func boolRef(value bool) *bool { return &value }

func configForModelOverrideTest() config.Config {
	cfg := config.Defaults()
	cfg.Embedding.Provider = "openai"
	cfg.Embedding.BaseURL = "http://embedding"
	cfg.Embedding.Model = "global-embedding"
	cfg.Rerank.Enabled = true
	cfg.Rerank.BaseURL = "http://rerank"
	cfg.Rerank.Model = "global-rerank"
	return cfg
}
