package jobs

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/shutu-ai/shutu-knowledge/internal/storage"
)

func TestCompletedProgressPreservesLargeTotals(t *testing.T) {
	db, err := storage.Open(filepath.Join(t.TempDir(), "knowledge.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	manager := New(db, 1)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if err := manager.Start(ctx); err != nil {
		t.Fatal(err)
	}
	defer manager.Stop()

	reported := make(chan struct{})
	release := make(chan struct{})
	id, err := manager.SubmitWithProgress("import_directory", "base", 1000, func(_ context.Context, report func(ProgressUpdate)) error {
		report(ProgressUpdate{Completed: 101, Total: 1000, Phase: "scanning"})
		close(reported)
		<-release
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}

	select {
	case <-reported:
	case <-time.After(2 * time.Second):
		t.Fatal("progress was not reported")
	}
	job, ok := manager.Status(id)
	if !ok {
		t.Fatal("job disappeared before completion")
	}
	if job.Progress != 101 || job.Total != 1000 {
		t.Fatalf("progress was truncated: %+v", job)
	}

	close(release)
	deadline := time.Now().Add(2 * time.Second)
	for {
		job, ok = manager.Status(id)
		if ok && job.Status == StatusDone {
			if job.Progress != 1000 {
				t.Fatalf("completed progress did not reach total: %+v", job)
			}
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("job did not complete: %+v", job)
		}
		time.Sleep(5 * time.Millisecond)
	}
}
