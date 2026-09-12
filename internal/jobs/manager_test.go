package jobs

import (
	"context"
	"path/filepath"
	"sync"
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

func TestIOJobsAreSerializedWithoutBlockingNormalJobs(t *testing.T) {
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

	firstStarted := make(chan struct{})
	secondStarted := make(chan struct{})
	normalStarted := make(chan struct{})
	release := make(chan struct{})
	var releaseOnce sync.Once
	defer releaseOnce.Do(func() { close(release) })

	_, err = manager.SubmitIOWithProgress("reindex_base", "base-a", 1, func(ctx context.Context, _ func(ProgressUpdate)) error {
		close(firstStarted)
		select {
		case <-release:
			return nil
		case <-ctx.Done():
			return ctx.Err()
		}
	})
	if err != nil {
		t.Fatal(err)
	}
	_, err = manager.SubmitIOWithProgress("reindex_base", "base-b", 1, func(context.Context, func(ProgressUpdate)) error {
		close(secondStarted)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	normalID, err := manager.SubmitWithProgress("self-test-reranker", "", 1, func(context.Context, func(ProgressUpdate)) error {
		close(normalStarted)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}

	select {
	case <-firstStarted:
	case <-time.After(2 * time.Second):
		t.Fatal("first IO job did not start")
	}
	select {
	case <-normalStarted:
	case <-time.After(2 * time.Second):
		t.Fatal("normal job was blocked by IO queue")
	}
	select {
	case <-secondStarted:
		t.Fatal("second IO job started before the first one released")
	case <-time.After(150 * time.Millisecond):
	}

	releaseOnce.Do(func() { close(release) })
	deadline := time.Now().Add(2 * time.Second)
	for {
		job, ok := manager.Status(normalID)
		if ok && job.Status == StatusDone {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("normal job did not complete: %+v", job)
		}
		time.Sleep(5 * time.Millisecond)
	}
	select {
	case <-secondStarted:
	case <-time.After(2 * time.Second):
		t.Fatal("second IO job did not start after the first released")
	}
}
