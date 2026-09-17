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

func TestSubmitDoesNotWaitForSQLiteWriter(t *testing.T) {
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

	tx, err := db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Exec(`INSERT INTO kv (key, value) VALUES (?, ?)`, "submit-lock", "held"); err != nil {
		_ = tx.Rollback()
		t.Fatal(err)
	}

	started := make(chan struct{})
	submitDone := make(chan struct{})
	var id string
	var submitErr error
	go func() {
		id, submitErr = manager.SubmitIOWithProgress("reindex_document", "base", 1, func(context.Context, func(ProgressUpdate)) error {
			close(started)
			return nil
		})
		close(submitDone)
	}()

	select {
	case <-submitDone:
	case <-time.After(200 * time.Millisecond):
		t.Fatal("job submission waited for the SQLite writer")
	}
	if submitErr != nil {
		t.Fatal(submitErr)
	}
	job, ok := manager.Status(id)
	if !ok || job.Status != StatusPending {
		t.Fatalf("pending in-memory job was not visible: %+v %v", job, ok)
	}
	if err := tx.Rollback(); err != nil {
		t.Fatal(err)
	}

	select {
	case <-started:
	case <-time.After(2 * time.Second):
		t.Fatal("job did not start after the SQLite writer was released")
	}
}

func TestStopWithContextBoundsNonCooperativeLegacyTask(t *testing.T) {
	db, err := storage.Open(filepath.Join(t.TempDir(), "knowledge.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	manager := New(db, 1)
	startCtx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if err := manager.Start(startCtx); err != nil {
		t.Fatal(err)
	}

	started := make(chan struct{})
	release := make(chan struct{})
	_, err = manager.SubmitWithProgress("legacy-stuck", "base", 1, func(context.Context, func(ProgressUpdate)) error {
		close(started)
		<-release
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	select {
	case <-started:
	case <-time.After(2 * time.Second):
		t.Fatal("legacy task did not start")
	}

	stopCtx, stopCancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer stopCancel()
	startedAt := time.Now()
	manager.StopWithContext(stopCtx)
	if elapsed := time.Since(startedAt); elapsed > time.Second {
		t.Fatalf("StopWithContext waited for non-cooperative task: %s", elapsed)
	}

	close(release)
	manager.StopWithContext(context.Background())
}
