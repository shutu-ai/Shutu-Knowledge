package scheduler

import (
	"context"
	"testing"
	"time"
)

func TestSemaphoreBoundsAndReleases(t *testing.T) {
	semaphore := NewSemaphore(1)
	release, err := semaphore.Acquire(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	if _, err := semaphore.Acquire(ctx); err == nil {
		t.Fatal("second acquire unexpectedly succeeded")
	}
	release()
	release()
	release2, err := semaphore.Acquire(context.Background())
	if err != nil {
		t.Fatalf("release did not restore capacity: %v", err)
	}
	release2()
}
