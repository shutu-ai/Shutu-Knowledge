//go:build !windows

package knowledge

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"syscall"
	"testing"
	"time"
)

// POSIX advisory locks do not make a destination invalid for rename. This
// drill intentionally records the platform semantics: the lock owner remains
// active, quarantine still preserves exact bytes, and explicit purge is the
// destructive second decision.
func TestPOSIXCorpusFileLockQuarantineConverges(t *testing.T) {
	const (
		documentCount = 600
		orphanCount   = 1200
		lockIndex     = orphanCount / 2
		rawFileBytes  = 2048
		orphanBytes   = orphanCount * rawFileBytes
	)

	f := newFixture(t)
	base, err := f.service.CreateBase("POSIX File Lock Corpus", "", "", BaseConfig{
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

	buildStart := time.Now()
	for index := 0; index < documentCount; index++ {
		data := corpusMaintenanceBytes(fmt.Sprintf("posixlockactive%06d", index), rawFileBytes)
		doc, err := f.service.AddFileDocument(ctx, base.ID, fmt.Sprintf("active-%06d.md", index), data, "")
		if err != nil {
			t.Fatalf("import document %d: %v", index, err)
		}
		if doc.Status != StatusReady || doc.ChunkCount != 1 || doc.RawFilePath == "" {
			t.Fatalf("document %d did not publish cleanly: %+v", index, doc)
		}
		expectedActive[doc.RawFilePath] = hashBytes(data)
	}
	for index := 0; index < orphanCount; index++ {
		relative := fmt.Sprintf("orphans/orphan-%06d.bin", index)
		data := corpusMaintenanceBytes(fmt.Sprintf("posixlockorphan%06d", index), rawFileBytes)
		if _, err := f.raw.WriteRel(base.ID, relative, data); err != nil {
			t.Fatalf("write orphan %d: %v", index, err)
		}
		expectedOrphans[base.ID+"/"+relative] = data
	}
	buildElapsed := time.Since(buildStart)

	lockRel := fmt.Sprintf("%s/orphans/orphan-%06d.bin", base.ID, lockIndex)
	lockFile, err := os.OpenFile(filepath.Join(f.raw.Root(), filepath.FromSlash(lockRel)),
		os.O_RDWR, 0o600)
	if err != nil {
		t.Fatalf("open lock file: %v", err)
	}
	if err := syscall.Flock(int(lockFile.Fd()), syscall.LOCK_EX); err != nil {
		_ = lockFile.Close()
		t.Fatalf("acquire advisory lock: %v", err)
	}
	defer func() { _ = lockFile.Close() }()

	quarantineStart := time.Now()
	result, err := f.service.ReconcileStorageSafe(ctx, StorageReconcileOptions{Quarantine: true}, nil)
	quarantineElapsed := time.Since(quarantineStart)
	if err != nil {
		t.Fatalf("quarantine under POSIX advisory lock: %v", err)
	}
	if result.Orphans != orphanCount || result.OrphanBytes != orphanBytes ||
		result.Quarantined != orphanCount || result.QuarantineBytes != orphanBytes {
		t.Fatalf("quarantine under lock result: %+v", result)
	}
	if count, bytes, err := f.raw.QuarantineStats(); err != nil || count != orphanCount || bytes != orphanBytes {
		t.Fatalf("quarantine stats under lock: %d %d %v", count, bytes, err)
	}
	if active, err := f.raw.ListAll(); err != nil || len(active) != documentCount {
		t.Fatalf("active tree under lock: %d files %v", len(active), err)
	}
	for rel, want := range expectedOrphans {
		got, err := f.raw.Read("quarantine/" + rel)
		if err != nil || string(got) != string(want) {
			t.Fatalf("locked orphan bytes changed %s: %d bytes %v", rel, len(got), err)
		}
	}
	for rel, wantHash := range expectedActive {
		data, err := f.raw.Read(rel)
		if err != nil || hashBytes(data) != wantHash {
			t.Fatalf("referenced raw changed %s: %v", rel, err)
		}
	}

	targetIndex := documentCount / 2
	searchStart := time.Now()
	search, err := f.service.Search(ctx, SearchRequest{
		Query: fmt.Sprintf("posixlockactive%06d", targetIndex), Mode: "lexical", TopK: 4,
	})
	if err != nil {
		t.Fatalf("search under lock: %v", err)
	}
	searchElapsed := time.Since(searchStart)
	if search.Total < 1 {
		t.Fatalf("target disappeared under lock: %+v", search)
	}
	if err := f.raw.PurgeQuarantine(); err != nil {
		t.Fatal(err)
	}
	if count, bytes, err := f.raw.QuarantineStats(); err != nil || count != 0 || bytes != 0 {
		t.Fatalf("purged quarantine stats: %d %d %v", count, bytes, err)
	}

	t.Logf("POSIX raw file-lock corpus docs=%d referencedRaw=%d orphans=%d rawFileBytes=%d orphanBytes=%d build=%s quarantine=%s search=%s",
		documentCount, documentCount, orphanCount, rawFileBytes, orphanBytes,
		buildElapsed, quarantineElapsed, searchElapsed)
}
