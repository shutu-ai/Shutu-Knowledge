package knowledge

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"golang.org/x/sys/windows"
)

// Windows exposes a rename collision through a no-share handle. This is a
// real OS-level lock, not an injected error from the raw-store hook.
func TestCorpusStorageReconcileFileLockStopsAndConverges(t *testing.T) {
	const (
		documentCount = 600
		orphanCount   = 1200
		lockIndex     = orphanCount / 2
		rawFileBytes  = 2048
		orphanBytes   = orphanCount * rawFileBytes
		priorOrphans  = lockIndex
		priorBytes    = priorOrphans * rawFileBytes
	)

	f := newFixture(t)
	base, err := f.service.CreateBase("Raw File Lock Corpus", "", "", BaseConfig{
		SmartChunk:    boolPtr(false),
		ChunkSize:     2048,
		ChunkOverlap:  0,
		SemanticChunk: boolPtr(false),
	})
	if err != nil {
		t.Fatalf("create base: %v", err)
	}
	ctx := context.Background()
	expectedActive := make(map[string]string, documentCount)
	expectedOrphans := make(map[string][]byte, orphanCount)
	var activeBytes int64

	buildStart := time.Now()
	for index := 0; index < documentCount; index++ {
		marker := fmt.Sprintf("rawfilelockactive%06d", index)
		data := corpusMaintenanceBytes(marker, rawFileBytes)
		fileName := fmt.Sprintf("active-%06d.md", index)
		doc, err := f.service.AddFileDocument(ctx, base.ID, fileName, data, "")
		if err != nil {
			t.Fatalf("import document %d: %v", index, err)
		}
		if doc.Status != StatusReady || doc.ChunkCount != 1 || doc.RawFilePath == "" {
			t.Fatalf("document %d did not publish cleanly: %+v", index, doc)
		}
		expectedActive[doc.RawFilePath] = hashBytes(data)
		activeBytes += int64(len(data))
	}
	for index := 0; index < orphanCount; index++ {
		marker := fmt.Sprintf("rawfilelockorphan%06d", index)
		data := corpusMaintenanceBytes(marker, rawFileBytes)
		relative := fmt.Sprintf("orphans/orphan-%06d.bin", index)
		if _, err := f.raw.WriteRel(base.ID, relative, data); err != nil {
			t.Fatalf("write orphan %d: %v", index, err)
		}
		expectedOrphans[base.ID+"/"+relative] = data
	}
	buildElapsed := time.Since(buildStart)

	activeTree, err := f.raw.ListAll()
	if err != nil {
		t.Fatal(err)
	}
	if len(activeTree) != documentCount+orphanCount {
		t.Fatalf("active raw files = %d, want %d", len(activeTree), documentCount+orphanCount)
	}
	for rel, wantHash := range expectedActive {
		data, err := f.raw.Read(rel)
		if err != nil || hashBytes(data) != wantHash {
			t.Fatalf("active baseline %s: %v", rel, err)
		}
	}

	lockedRel := fmt.Sprintf("%s/orphans/orphan-%06d.bin", base.ID, lockIndex)
	lockedFull := filepath.Join(f.raw.Root(), filepath.FromSlash(lockedRel))
	handle, err := windows.CreateFile(
		windows.StringToUTF16Ptr(lockedFull),
		windows.GENERIC_READ,
		0,
		nil,
		windows.OPEN_EXISTING,
		windows.FILE_ATTRIBUTE_NORMAL,
		0,
	)
	if err != nil {
		t.Fatalf("open no-share lock on %s: %v", lockedRel, err)
	}
	defer func() { _ = windows.CloseHandle(handle) }()

	lockStart := time.Now()
	blocked, err := f.service.ReconcileStorageSafe(ctx, StorageReconcileOptions{Quarantine: true}, nil)
	lockElapsed := time.Since(lockStart)
	if err == nil {
		t.Fatalf("locked rename succeeded: %+v", blocked)
	}
	if !strings.Contains(strings.ToLower(err.Error()), "access is denied") &&
		!strings.Contains(strings.ToLower(err.Error()), "sharing violation") &&
		!strings.Contains(strings.ToLower(err.Error()), "being used by another process") {
		t.Fatalf("locked rename returned unexpected error: %v", err)
	}
	if blocked.Orphans != priorOrphans+1 || blocked.OrphanBytes != priorBytes+rawFileBytes ||
		blocked.Quarantined != priorOrphans || blocked.QuarantineBytes != priorBytes {
		t.Fatalf("locked partial result: %+v", blocked)
	}
	activeTree, err = f.raw.ListAll()
	if err != nil {
		t.Fatal(err)
	}
	if len(activeTree) != documentCount+orphanCount-priorOrphans {
		t.Fatalf("active raw files after locked pass = %d, want %d",
			len(activeTree), documentCount+orphanCount-priorOrphans)
	}
	retainedCount, retainedSize, err := f.raw.QuarantineStats()
	if err != nil || retainedCount != priorOrphans || retainedSize != priorBytes {
		t.Fatalf("locked quarantine stats: %d %d %v", retainedCount, retainedSize, err)
	}
	for index := 0; index < priorOrphans; index++ {
		rel := fmt.Sprintf("%s/orphans/orphan-%06d.bin", base.ID, index)
		got, err := f.raw.Read(storageQuarantinePath(rel))
		if err != nil || string(got) != string(expectedOrphans[rel]) {
			t.Fatalf("quarantine before lock changed %s: %d bytes %v", rel, len(got), err)
		}
	}
	// A no-share lock intentionally prevents another open while maintenance
	// is stopped. Metadata is safe to inspect; bytes are verified after the
	// lock release moves the file into quarantine.
	lockedInfo, err := os.Stat(lockedFull)
	if err != nil || lockedInfo.Size() != rawFileBytes {
		t.Fatalf("locked active source changed: %+v %v", lockedInfo, err)
	}
	for rel, wantHash := range expectedActive {
		data, err := f.raw.Read(rel)
		if err != nil || hashBytes(data) != wantHash {
			t.Fatalf("referenced raw changed during lock: %s %v", rel, err)
		}
	}

	retryStart := time.Now()
	retryLocked, err := f.service.ReconcileStorageSafe(ctx, StorageReconcileOptions{Quarantine: true}, nil)
	retryLockedElapsed := time.Since(retryStart)
	if err == nil {
		t.Fatalf("retry while locked succeeded: %+v", retryLocked)
	}
	if retryLocked.Orphans != 1 || retryLocked.OrphanBytes != rawFileBytes ||
		retryLocked.Quarantined != 0 || retryLocked.QuarantineBytes != 0 {
		t.Fatalf("retry while locked result: %+v", retryLocked)
	}

	if err := windows.CloseHandle(handle); err != nil {
		t.Fatalf("release lock: %v", err)
	}
	recoveryStart := time.Now()
	recovered, err := f.service.ReconcileStorageSafe(ctx, StorageReconcileOptions{Quarantine: true}, nil)
	recoveryElapsed := time.Since(recoveryStart)
	if err != nil {
		t.Fatalf("recover after lock release: %v", err)
	}
	remaining := orphanCount - priorOrphans
	if recovered.Orphans != remaining || recovered.OrphanBytes != int64(remaining*rawFileBytes) ||
		recovered.Quarantined != remaining || recovered.QuarantineBytes != int64(remaining*rawFileBytes) {
		t.Fatalf("recovery result: %+v", recovered)
	}
	if count, bytes, err := f.raw.QuarantineStats(); err != nil || count != orphanCount || bytes != orphanBytes {
		t.Fatalf("recovered quarantine stats: %d %d %v", count, bytes, err)
	}
	if active, err := f.raw.ListAll(); err != nil || len(active) != documentCount {
		t.Fatalf("active tree after recovery: %d files %v", len(active), err)
	}
	for rel, want := range expectedOrphans {
		got, err := f.raw.Read(storageQuarantinePath(rel))
		if err != nil || string(got) != string(want) {
			t.Fatalf("recovered quarantine content %s: %d bytes %v", rel, len(got), err)
		}
	}
	for rel, wantHash := range expectedActive {
		data, err := f.raw.Read(rel)
		if err != nil || hashBytes(data) != wantHash {
			t.Fatalf("referenced raw changed after recovery: %s %v", rel, err)
		}
	}

	targetIndex := documentCount / 2
	targetMarker := fmt.Sprintf("rawfilelockactive%06d", targetIndex)
	searchStart := time.Now()
	search, err := f.service.Search(ctx, SearchRequest{
		Query: targetMarker, Mode: "lexical", TopK: 4,
	})
	if err != nil {
		t.Fatalf("search after lock recovery: %v", err)
	}
	searchElapsed := time.Since(searchStart)
	if search.Total < 1 {
		t.Fatalf("target disappeared after lock recovery: %+v", search)
	}

	if err := f.raw.PurgeQuarantine(); err != nil {
		t.Fatal(err)
	}
	if count, bytes, err := f.raw.QuarantineStats(); err != nil || count != 0 || bytes != 0 {
		t.Fatalf("purged quarantine stats: %d %d %v", count, bytes, err)
	}

	databaseInfo, err := os.Stat(filepath.Join(os.Getenv("SHUTU_KNOWLEDGE_HOME"), "knowledge.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("synthetic raw file-lock corpus docs=%d referencedRaw=%d orphans=%d rawFileBytes=%d activeBytes=%d orphanBytes=%d dbBytes=%d build=%s lockedPass=%s retryLocked=%s recovery=%s search=%s",
		documentCount, documentCount, orphanCount, rawFileBytes, activeBytes, orphanBytes,
		databaseInfo.Size(), buildElapsed, lockElapsed, retryLockedElapsed,
		recoveryElapsed, searchElapsed)
}

func storageQuarantinePath(relativePath string) string {
	return "quarantine/" + relativePath
}
