package knowledge

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/shutu-ai/shutu-knowledge/internal/storage"
)

func TestSyntheticCorpusStorageReconcileMovesExactlyOrphans(t *testing.T) {
	const (
		documentCount   = 1200
		orphanCount     = 1200
		expiredOrphans  = orphanCount / 2
		retainedOrphans = orphanCount - expiredOrphans
		rawFileBytes    = 2048
	)

	f := newFixture(t)
	base, err := f.service.CreateBase("Raw Reconcile Corpus", "", "", BaseConfig{
		SmartChunk:    boolPtr(false),
		ChunkSize:     2048,
		ChunkOverlap:  0,
		SemanticChunk: boolPtr(false),
	})
	if err != nil {
		t.Fatalf("create base: %v", err)
	}
	ctx := context.Background()
	docs := make([]Document, 0, documentCount)
	expectedActive := make(map[string]string, documentCount)
	expectedOrphans := make(map[string][]byte, orphanCount)
	var activeBytes, orphanBytes int64

	buildStart := time.Now()
	for index := 0; index < documentCount; index++ {
		marker := fmt.Sprintf("rawreconcileactive%06d", index)
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
		docs = append(docs, doc)
	}
	for index := 0; index < orphanCount; index++ {
		marker := fmt.Sprintf("rawreconcileorphan%06d", index)
		data := corpusMaintenanceBytes(marker, rawFileBytes)
		rel := fmt.Sprintf("%s/orphans/orphan-%06d.bin", base.ID, index)
		if _, err := f.raw.WriteRel(base.ID, fmt.Sprintf("orphans/orphan-%06d.bin", index), data); err != nil {
			t.Fatalf("write orphan %d: %v", index, err)
		}
		expectedOrphans[rel] = data
		orphanBytes += int64(len(data))
	}
	buildElapsed := time.Since(buildStart)

	activeTree, err := f.raw.ListAll()
	if err != nil {
		t.Fatal(err)
	}
	if len(activeTree) != documentCount+orphanCount {
		t.Fatalf("active raw files = %d, want %d", len(activeTree), documentCount+orphanCount)
	}

	dryProgress := newReconcileProgressLog(t)
	dryStart := time.Now()
	dryRun, err := f.service.ReconcileStorageSafe(ctx, StorageReconcileOptions{DryRun: true}, dryProgress.record)
	if err != nil {
		t.Fatalf("dry run: %v", err)
	}
	dryElapsed := time.Since(dryStart)
	if dryRun.Orphans != orphanCount || dryRun.OrphanBytes != orphanBytes ||
		dryRun.Quarantined != 0 || dryRun.QuarantineBytes != 0 {
		t.Fatalf("dry-run result: %+v", dryRun)
	}
	dryProgress.requirePhases()
	for rel := range expectedOrphans {
		if _, err := os.Stat(filepath.Join(f.raw.Root(), filepath.FromSlash(rel))); err != nil {
			t.Fatalf("dry run moved orphan %s: %v", rel, err)
		}
	}

	quarantineProgress := newReconcileProgressLog(t)
	quarantineStart := time.Now()
	quarantined, err := f.service.ReconcileStorageSafe(ctx, StorageReconcileOptions{Quarantine: true}, quarantineProgress.record)
	if err != nil {
		t.Fatalf("quarantine pass: %v", err)
	}
	quarantineElapsed := time.Since(quarantineStart)
	if quarantined.Orphans != orphanCount || quarantined.OrphanBytes != orphanBytes ||
		quarantined.Quarantined != orphanCount || quarantined.QuarantineBytes != orphanBytes {
		t.Fatalf("quarantine result: %+v", quarantined)
	}
	quarantineProgress.requirePhases()

	// Verify every move by content, not just the aggregate byte count.
	for rel, want := range expectedOrphans {
		retainedRel := storage.QuarantineDir + "/" + rel
		got, err := f.raw.Read(retainedRel)
		if err != nil || string(got) != string(want) {
			t.Fatalf("quarantine content %s: %d bytes %v", retainedRel, len(got), err)
		}
		if _, err := os.Stat(filepath.Join(f.raw.Root(), filepath.FromSlash(rel))); !os.IsNotExist(err) {
			t.Fatalf("quarantined raw stayed active %s: %v", rel, err)
		}
	}
	for rel, wantHash := range expectedActive {
		got, err := f.raw.Read(rel)
		if err != nil {
			t.Fatalf("read referenced raw %s: %v", rel, err)
		}
		if hashBytes(got) != wantHash {
			t.Fatalf("referenced raw changed: %s", rel)
		}
	}

	// Age an exact half deterministically. Retention must distinguish those
	// files from newly retained copies while leaving the other half intact.
	past := time.Now().Add(-2 * time.Hour)
	expiredRel := make(map[string]bool, expiredOrphans)
	for index := 0; index < orphanCount; index += 2 {
		rel := fmt.Sprintf("%s/orphans/orphan-%06d.bin", base.ID, index)
		quarantineRel := storage.QuarantineDir + "/" + rel
		full := filepath.Join(f.raw.Root(), filepath.FromSlash(quarantineRel))
		if err := os.Chtimes(full, past, past); err != nil {
			t.Fatalf("age quarantine file %s: %v", quarantineRel, err)
		}
		expiredRel[quarantineRel] = true
	}
	expiredStart := time.Now()
	expired, err := f.service.ReconcileStorageSafe(ctx, StorageReconcileOptions{
		Quarantine:         true,
		PurgeExpiredBefore: time.Now().Add(-time.Hour),
	}, nil)
	if err != nil {
		t.Fatalf("retention pass: %v", err)
	}
	expiredElapsed := time.Since(expiredStart)
	if expired.Orphans != 0 || expired.Quarantined != 0 ||
		expired.ExpiredPurged != expiredOrphans || expired.ExpiredBytes != orphanBytes/2 {
		t.Fatalf("retention result: %+v", expired)
	}
	for rel := range expectedOrphans {
		quarantineRel := storage.QuarantineDir + "/" + rel
		data, err := f.raw.Read(quarantineRel)
		switch {
		case expiredRel[quarantineRel]:
			if data != nil || err != nil {
				t.Fatalf("expired quarantine file remained %s: %d bytes %v", quarantineRel, len(data), err)
			}
		default:
			want := expectedOrphans[rel]
			if err != nil || string(data) != string(want) {
				t.Fatalf("retained quarantine file changed %s: %d bytes %v", quarantineRel, len(data), err)
			}
		}
	}

	purgeStart := time.Now()
	purged, err := f.service.ReconcileStorageSafe(ctx, StorageReconcileOptions{PurgeQuarantine: true}, nil)
	if err != nil {
		t.Fatalf("explicit purge: %v", err)
	}
	purgeElapsed := time.Since(purgeStart)
	if purged.Orphans != 0 || purged.Quarantined != retainedOrphans ||
		purged.QuarantineBytes != orphanBytes/2 || !purged.QuarantinePurged {
		t.Fatalf("explicit purge result: %+v", purged)
	}
	if count, bytes, err := f.raw.QuarantineStats(); err != nil || count != 0 || bytes != 0 {
		t.Fatalf("quarantine after purge: %d %d %v", count, bytes, err)
	}
	for rel := range expectedOrphans {
		if data, err := f.raw.Read(storage.QuarantineDir + "/" + rel); data != nil || err != nil {
			t.Fatalf("purge left quarantine content %s: %d bytes %v", rel, len(data), err)
		}
	}

	targetIndex := documentCount / 2
	target := docs[targetIndex]
	storedTarget, _, err := f.service.GetDocument(target.ID, false)
	if err != nil {
		t.Fatalf("reload target after maintenance: %v", err)
	}
	searchStart := time.Now()
	search, err := f.service.Search(ctx, SearchRequest{
		Query: fmt.Sprintf("rawreconcileactive%06d", targetIndex),
		Mode:  "lexical", TopK: 4,
	})
	if err != nil {
		t.Fatalf("search after maintenance: %v", err)
	}
	searchElapsed := time.Since(searchStart)
	if search.Total < 1 || search.Hits[0].DocID != target.ID {
		t.Fatalf("target disappeared after maintenance: total=%d hits=%d", search.Total, len(search.Hits))
	}
	generation, sourceVersion := storedTarget.ActiveIndexGen, storedTarget.SourceVersion
	rawFile, err := f.service.GetRawFileForCitation(target.ID, RawCitationOptions{
		IndexGeneration: &generation,
		SourceVersion:   &sourceVersion,
	})
	if err != nil {
		t.Fatalf("pinned raw after maintenance: %v", err)
	}
	if rawFile.IndexGeneration != generation || rawFile.SourceVersion != sourceVersion ||
		hashBytes(rawFile.Bytes) != expectedActive[target.RawFilePath] {
		t.Fatalf("pinned raw identity/content changed: %+v", rawFile)
	}

	databaseInfo, err := os.Stat(filepath.Join(os.Getenv("SHUTU_KNOWLEDGE_HOME"), "knowledge.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("synthetic raw reconcile corpus docs=%d referencedRaw=%d orphans=%d rawFileBytes=%d activeBytes=%d orphanBytes=%d dbBytes=%d build=%s dryRun=%s quarantine=%s retention=%s purge=%s search=%s total=%s",
		documentCount, documentCount, orphanCount, rawFileBytes, activeBytes, orphanBytes,
		databaseInfo.Size(), buildElapsed, dryElapsed, quarantineElapsed,
		expiredElapsed, purgeElapsed, searchElapsed, time.Since(buildStart))
}

func corpusMaintenanceBytes(marker string, size int) []byte {
	prefix := marker + " corpus raw maintenance alpha beta gamma delta epsilon "
	if len(prefix) > size {
		panic("maintenance corpus prefix exceeds file size")
	}
	data := make([]byte, size)
	copy(data, prefix)
	return data
}

func hashBytes(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

type reconcileProgressLog struct {
	t      *testing.T
	phases map[string]int
	last   map[string][2]int
}

func newReconcileProgressLog(t *testing.T) *reconcileProgressLog {
	return &reconcileProgressLog{t: t, phases: map[string]int{}, last: map[string][2]int{}}
}

func (l *reconcileProgressLog) record(phase string, completed, total int) {
	l.phases[phase]++
	if previous, ok := l.last[phase]; ok && completed < previous[0] {
		l.t.Fatalf("progress phase %s moved backwards: %d -> %d", phase, previous[0], completed)
	}
	l.last[phase] = [2]int{completed, total}
}

func (l *reconcileProgressLog) requirePhases() {
	for _, phase := range []string{"scanning", "counting", "ready"} {
		if l.phases[phase] == 0 {
			l.t.Fatalf("maintenance progress missing phase %s: %+v", phase, l.phases)
		}
	}
	ready, ok := l.last["ready"]
	if !ok || ready[0] != ready[1] || ready[1] == 0 {
		l.t.Fatalf("ready progress = %+v, want equal nonzero completed/total", ready)
	}
}
