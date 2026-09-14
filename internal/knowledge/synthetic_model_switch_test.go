package knowledge

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/shutu-ai/shutu-knowledge/internal/embedding"
)

type alwaysFailingEmbedder struct {
	model string
	calls int
}

func (e *alwaysFailingEmbedder) Embed(context.Context, []string) ([][]float64, error) {
	e.calls++
	return nil, errors.New("provider offline")
}

func (e *alwaysFailingEmbedder) ModelKey() string { return "fake:" + e.model }

func TestSyntheticCorpusModelSwitchFailureAndDisable(t *testing.T) {
	const (
		documentCount = 400
		searchLimit   = 50
	)
	service, _ := newSearchFixture(t)
	base, err := service.CreateBase("Synthetic Model Switch", "", "", BaseConfig{})
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()

	buildStart := time.Now()
	ids := make([]string, 0, documentCount)
	for index := 0; index < documentCount; index++ {
		content := fmt.Sprintf(`# Corpus %04d

The database keeps corpus %04d rows on disk.

The database engine uses transactions for corpus %04d.

Recovery replays committed corpus %04d writes exactly once.

Model identity partitions corpus %04d vector spaces.`, index, index, index, index, index)
		document, err := service.AddTextDocument(ctx, base.ID,
			fmt.Sprintf("Corpus Document %04d", index), content)
		if err != nil {
			t.Fatal(err)
		}
		ids = append(ids, document.ID)
	}
	buildElapsed := time.Since(buildStart)

	activeModelCounts := func() map[string]int {
		t.Helper()
		rows, err := service.store.db.Query(`SELECT COALESCE(c.embedding_model, ''), COUNT(*)
			FROM chunks c JOIN documents d ON d.id = c.doc_id
			WHERE c.base_id = ? AND c.index_generation = d.active_index_generation
			GROUP BY COALESCE(c.embedding_model, '')`, base.ID)
		if err != nil {
			t.Fatal(err)
		}
		defer rows.Close()
		counts := map[string]int{}
		for rows.Next() {
			var model string
			var count int
			if err := rows.Scan(&model, &count); err != nil {
				t.Fatal(err)
			}
			counts[model] = count
		}
		if err := rows.Err(); err != nil {
			t.Fatal(err)
		}
		return counts
	}
	modelACounts := activeModelCounts()
	modelAChunks := modelACounts["fake:a"]
	if len(modelACounts) != 1 || modelAChunks == 0 {
		t.Fatalf("model A corpus baseline = %+v", modelACounts)
	}

	// B has the same dimension on purpose. Model identity, not vector width,
	// must partition every one of these documents.
	modelB := &fakeEmbedder{model: "b"}
	service.SetProviders(modelB, nil)
	migrateStart := time.Now()
	for _, id := range ids {
		if _, err := service.ReindexDocument(ctx, id); err != nil {
			t.Fatal(err)
		}
	}
	migrateElapsed := time.Since(migrateStart)
	modelBCounts := activeModelCounts()
	if len(modelBCounts) != 1 || modelBCounts["fake:b"] != modelAChunks {
		t.Fatalf("model B corpus = %+v, want %d fake:b", modelBCounts, modelAChunks)
	}

	vectorSearchStart := time.Now()
	vectorResult, err := service.Search(ctx, SearchRequest{
		Query: "database", Mode: "vector", BaseIDs: []string{base.ID},
		TopK: searchLimit,
	})
	vectorElapsed := time.Since(vectorSearchStart)
	if err != nil || vectorResult.Mode != "vector" || vectorResult.Total != searchLimit {
		t.Fatalf("model B vector search = %v %+v", err, vectorResult)
	}
	for _, hit := range vectorResult.Hits {
		if hit.IndexGeneration != 2 || hit.SourceVersion != 2 {
			t.Fatalf("model B search mixed generations: %+v", hit)
		}
	}

	// Every document attempts C and every call fails. Published B state must
	// remain the authoritative fallback rather than partially switching.
	modelC := &alwaysFailingEmbedder{model: "c"}
	service.SetProviders(modelC, nil)
	failureStart := time.Now()
	for _, id := range ids {
		if _, err := service.ReindexDocument(ctx, id); err != nil {
			t.Fatal(err)
		}
	}
	failureElapsed := time.Since(failureStart)
	if modelC.calls == 0 {
		t.Fatal("failed model migration did not call provider")
	}
	afterFailure := activeModelCounts()
	if len(afterFailure) != 1 || afterFailure["fake:b"] != modelAChunks {
		t.Fatalf("failed C mutated active vectors: %+v", afterFailure)
	}
	var cVectors int
	if err := service.store.db.QueryRow(`SELECT COUNT(*) FROM chunks
		WHERE embedding_model = 'fake:c'`).Scan(&cVectors); err != nil {
		t.Fatal(err)
	}
	if cVectors != 0 {
		t.Fatalf("failed model C left %d vectors", cVectors)
	}
	for _, id := range ids {
		document, err := service.store.getDocument(id)
		if err != nil {
			t.Fatal(err)
		}
		if document.EmbeddingModel != "fake:b" || document.ActiveIndexGen != 2 ||
			document.SourceVersion != 2 || document.ErrorCode != ErrEmbeddingProvider {
			t.Fatalf("failed C changed %s: %+v", id, document)
		}
	}

	// Disabled embedding creates an explicit lexical generation; it must not
	// reinterpret B vectors or leave active vectors behind.
	service.global.Embedding.Provider = "none"
	service.SetProviders(embedding.New(embedding.Config{Provider: "none"}), nil)
	disableStart := time.Now()
	for _, id := range ids {
		if _, err := service.ReindexDocument(ctx, id); err != nil {
			t.Fatal(err)
		}
	}
	disableElapsed := time.Since(disableStart)
	disabledCounts := activeModelCounts()
	if len(disabledCounts) != 1 || disabledCounts[""] != modelAChunks {
		t.Fatalf("disabled corpus = %+v, want %d lexical chunks", disabledCounts, modelAChunks)
	}
	lexicalSearchStart := time.Now()
	lexicalResult, err := service.Search(ctx, SearchRequest{
		Query: "database", Mode: "vector", BaseIDs: []string{base.ID},
		TopK: searchLimit,
	})
	lexicalElapsed := time.Since(lexicalSearchStart)
	if err != nil || lexicalResult.Mode != "lexical" || lexicalResult.Total != searchLimit {
		t.Fatalf("disabled vector search = %v %+v", err, lexicalResult)
	}
	var activeVectors int
	if err := service.store.db.QueryRow(`SELECT COUNT(*) FROM chunks c
		JOIN documents d ON d.id = c.doc_id
		WHERE c.base_id = ? AND c.index_generation = d.active_index_generation
		  AND c.embedding IS NOT NULL`, base.ID).Scan(&activeVectors); err != nil {
		t.Fatal(err)
	}
	if activeVectors != 0 {
		t.Fatalf("disabled generation retained %d active vectors", activeVectors)
	}

	t.Logf("synthetic model switch docs=%d chunks=%d build=%s migrateB=%s vectorSearch=%s failC=%s disable=%s lexicalSearch=%s",
		documentCount, modelAChunks, buildElapsed, migrateElapsed, vectorElapsed,
		failureElapsed, disableElapsed, lexicalElapsed)
}
