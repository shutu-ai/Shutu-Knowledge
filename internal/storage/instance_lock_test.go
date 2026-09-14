package storage

import (
	"path/filepath"
	"strings"
	"testing"
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
