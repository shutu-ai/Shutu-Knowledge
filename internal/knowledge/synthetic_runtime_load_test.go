package knowledge

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/shutu-ai/shutu-knowledge/internal/storage"
)

type cancellableGatedEmbedder struct {
	model   string
	started chan struct{}
	release chan struct{}
}

func (e *cancellableGatedEmbedder) Embed(ctx context.Context, texts []string) ([][]float64, error) {
	close(e.started)
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-e.release:
		out := make([][]float64, 0, len(texts))
		for _, text := range texts {
			vector := make([]float64, 2)
			if strings.Contains(text, "database") {
				vector[0] = 1
			} else {
				vector[1] = 1
			}
			out = append(out, vector)
		}
		return out, nil
	}
}

func (e *cancellableGatedEmbedder) ModelKey() string { return "fake:" + e.model }

// TestSyntheticRuntimeLoadCancelAndRetry exercises a mixed-length document
// large enough to span several bounded embedding batches. Cancel must retain
// the published generation, and the retry must replace it atomically.
func TestSyntheticRuntimeLoadCancelAndRetry(t *testing.T) {
	service, _ := newSearchFixture(t)
	base, err := service.CreateBase("Synthetic Runtime Load", "", "", BaseConfig{})
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	var content strings.Builder
	content.WriteString("# Synthetic runtime load\n")
	for i := 0; i < 90; i++ {
		switch i % 3 {
		case 0:
			fmt.Fprintf(&content, "\nShort database row %d.\n", i)
		case 1:
			fmt.Fprintf(&content, "\nDatabase row %d stores a medium recovery record used by the bounded embedding runtime.\n", i)
		default:
			fmt.Fprintf(&content, "\nDatabase row %d stores a longer recovery and replay record. The text intentionally varies token length so the runtime cannot assume every batch has identical tensor cost. Retry and cancellation boundaries must preserve committed progress.\n", i)
		}
	}
	document, err := service.AddTextDocument(ctx, base.ID, "Synthetic Runtime Load", content.String())
	if err != nil {
		t.Fatal(err)
	}

	activeCount := func() (int, int, int64) {
		var chunks, vectors int
		var generation int64
		err := service.store.db.QueryRow(`SELECT COUNT(*), SUM(CASE WHEN c.embedding IS NOT NULL THEN 1 ELSE 0 END),
			COALESCE(MAX(c.index_generation), 0)
			FROM chunks c JOIN documents d ON d.id = c.doc_id
			WHERE c.doc_id = ? AND c.index_generation = d.active_index_generation`, document.ID).
			Scan(&chunks, &vectors, &generation)
		if err != nil {
			t.Fatal(err)
		}
		return chunks, vectors, generation
	}
	beforeChunks, beforeVectors, beforeGeneration := activeCount()
	if beforeChunks < 20 || beforeVectors != beforeChunks {
		t.Fatalf("synthetic baseline chunks=%d vectors=%d", beforeChunks, beforeVectors)
	}

	gated := &cancellableGatedEmbedder{model: "b", started: make(chan struct{}), release: make(chan struct{})}
	service.SetProviders(gated, nil)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		_, err := service.ReindexDocument(ctx, document.ID)
		done <- err
	}()
	<-gated.started
	cancel()
	close(gated.release)
	if err := <-done; !errors.Is(err, context.Canceled) && !errors.Is(err, storage.ErrWriteUnknown) {
		t.Fatalf("cancelled reindex error = %v, want context.Canceled", err)
	}
	restored, err := service.store.getDocument(document.ID)
	if err != nil {
		t.Fatal(err)
	}
	if restored.ActiveIndexGen != beforeGeneration || restored.IndexState == IndexStateActive && restored.DesiredIndexGen <= restored.ActiveIndexGen {
		t.Fatalf("cancelled reindex changed active generation=%d state=%s desired=%d", restored.ActiveIndexGen, restored.IndexState, restored.DesiredIndexGen)
	}
	if _, vectors, generation := activeCount(); vectors != beforeVectors || generation != beforeGeneration {
		t.Fatalf("cancel changed active vectors=%d generation=%d; want %d/%d", vectors, generation, beforeVectors, beforeGeneration)
	}

	service.SetProviders(&fakeEmbedder{model: "c"}, nil)
	if _, err := service.ReindexDocument(context.Background(), document.ID); err != nil {
		t.Fatal(err)
	}
	chunks, vectors, generation := activeCount()
	if chunks != beforeChunks || vectors != beforeVectors || generation <= beforeGeneration {
		t.Fatalf("retry chunks=%d vectors=%d generation=%d; want %d/%d newer generation", chunks, vectors, generation, beforeChunks, beforeVectors)
	}
}
