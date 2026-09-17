package storage

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestInstanceLockIsExclusiveUntilReleased(t *testing.T) {
	path := filepath.Join(t.TempDir(), "instance.lock.db")
	first, err := AcquireInstanceLock(path)
	if err != nil {
		t.Fatalf("acquire first lock: %v", err)
	}
	if _, err := AcquireInstanceLock(path); err == nil || !strings.Contains(err.Error(), "another Knowledge instance") {
		t.Fatalf("second lock error = %v, want ownership conflict", err)
	}
	if err := first.Release(); err != nil {
		t.Fatalf("release first lock: %v", err)
	}
	second, err := AcquireInstanceLock(path)
	if err != nil {
		t.Fatalf("acquire after release: %v", err)
	}
	if err := second.Release(); err != nil {
		t.Fatalf("release second lock: %v", err)
	}
}

func TestInstanceLockReleaseWithContextRemainsBoundedAndReleasesOwnership(t *testing.T) {
	path := filepath.Join(t.TempDir(), "instance.lock.db")
	first, err := AcquireInstanceLock(path)
	if err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	started := time.Now()
	_ = first.ReleaseWithContext(ctx)
	if elapsed := time.Since(started); elapsed > time.Second {
		t.Fatalf("canceled release waited too long: %s", elapsed)
	}

	second, err := AcquireInstanceLock(path)
	if err != nil {
		t.Fatalf("acquire after canceled release: %v", err)
	}
	if err := second.Release(); err != nil {
		t.Fatalf("release second lock: %v", err)
	}
}
