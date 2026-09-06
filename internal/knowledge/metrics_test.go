package knowledge

import (
	"context"
	"testing"
)

func TestMetricsObserveIngestSearchModelAndJobFailures(t *testing.T) {
	service, _ := newSearchFixture(t)
	base, err := service.CreateBase("Metrics", "", "", BaseConfig{})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.AddTextDocument(context.Background(), base.ID, "Guide", "database part one\n\npart two text"); err != nil {
		t.Fatal(err)
	}
	result, err := service.Search(context.Background(), SearchRequest{Query: "database", TopK: 4})
	if err != nil {
		t.Fatal(err)
	}
	if result.Total == 0 {
		t.Fatal("expected search hit")
	}

	metrics := service.Metrics()
	if metrics.Imports != 1 || metrics.ChunkCount < 2 || metrics.EmbeddingDurationMS < 0 ||
		metrics.Searches != 1 || metrics.CandidateCount == 0 || metrics.ContextCount != int64(result.Total) {
		t.Fatalf("successful metrics: %+v", metrics)
	}

	broken := &fakeEmbedder{model: "a", failNth: 1}
	service.SetProviders(broken, nil)
	if _, err := service.AddTextDocument(context.Background(), base.ID, "Broken", "database degradation"); err != nil {
		t.Fatal(err)
	}
	metrics = service.Metrics()
	if metrics.Imports != 2 || metrics.ModelErrors == 0 {
		t.Fatalf("model error metrics: %+v", metrics)
	}

	service.ObserveJobFailure("import_test")
	if metrics = service.Metrics(); metrics.JobFailures != 1 {
		t.Fatalf("job failure metric: %+v", metrics)
	}
}
