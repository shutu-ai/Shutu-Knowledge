package knowledge

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"time"

	"github.com/shutu-ai/shutu-knowledge/internal/storage"
)

func TestReconcileStorageSafeQuarantinesAndPurgesOrphans(t *testing.T) {
	f := newFixture(t)
	base := f.createBase(t)
	orphan := filepath.Join(f.raw.Root(), base.ID, "orphan.bin")
	if err := os.MkdirAll(filepath.Dir(orphan), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(orphan, []byte("bytes"), 0o600); err != nil {
		t.Fatal(err)
	}

	dryRun, err := f.service.ReconcileStorageSafe(context.Background(), StorageReconcileOptions{DryRun: true}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if dryRun.Orphans != 1 || dryRun.OrphanBytes != 5 || dryRun.Quarantined != 0 {
		t.Fatalf("dry run: %+v", dryRun)
	}
	if _, err := os.Stat(orphan); err != nil {
		t.Fatalf("dry run changed active raw: %v", err)
	}

	quarantined, err := f.service.ReconcileStorageSafe(context.Background(), StorageReconcileOptions{Quarantine: true}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if quarantined.Orphans != 1 || quarantined.Quarantined != 1 || quarantined.QuarantineBytes != 5 {
		t.Fatalf("quarantine pass: %+v", quarantined)
	}
	if _, err := os.Stat(orphan); !os.IsNotExist(err) {
		t.Fatalf("quarantined raw remained active: %v", err)
	}
	retained := filepath.Join(f.raw.Root(), storage.QuarantineDir, base.ID, "orphan.bin")
	if _, err := os.Stat(retained); err != nil {
		t.Fatalf("retained raw missing: %v", err)
	}
	past := time.Now().Add(-time.Hour)
	if err := os.Chtimes(retained, past, past); err != nil {
		t.Fatal(err)
	}
	expired, err := f.service.ReconcileStorageSafe(context.Background(), StorageReconcileOptions{
		Quarantine: true, PurgeExpiredBefore: time.Now(),
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if expired.ExpiredPurged != 1 || expired.ExpiredBytes != 5 || expired.Quarantined != 0 {
		t.Fatalf("retention pass: %+v", expired)
	}
	if _, err := os.Stat(retained); !os.IsNotExist(err) {
		t.Fatalf("expired quarantine file remained: %v", err)
	}

	purged, err := f.service.ReconcileStorageSafe(context.Background(), StorageReconcileOptions{PurgeQuarantine: true}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !purged.QuarantinePurged || purged.QuarantineBytes != 0 {
		t.Fatalf("purge pass: %+v", purged)
	}
	if _, err := os.Stat(filepath.Join(f.raw.Root(), storage.QuarantineDir)); !os.IsNotExist(err) {
		t.Fatalf("quarantine directory remained: %v", err)
	}
}
