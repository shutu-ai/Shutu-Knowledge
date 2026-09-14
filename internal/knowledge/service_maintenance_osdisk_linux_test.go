//go:build linux

package knowledge

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/shutu-ai/shutu-knowledge/internal/config"
	"github.com/shutu-ai/shutu-knowledge/internal/jobs"
	"github.com/shutu-ai/shutu-knowledge/internal/storage"
)

func TestCorpusRawStoreOSDiskFullPreservesAndRecovers(t *testing.T) {
	const (
		corpusCount   = 600
		corpusBytes   = 2048
		tmpfsCapacity = 4 << 20
		faultRepeats  = 200
	)

	const maxExternalVolumeBytes = 16 << 20
	externalVolume := strings.TrimSpace(os.Getenv("SHUTU_TEST_RAW_VOLUME"))
	externalAck := strings.TrimSpace(os.Getenv("SHUTU_TEST_RAW_VOLUME_ACK"))
	usingTmpfs := externalVolume == ""
	if _, err := os.Stat(filepath.Join(os.Getenv("SHUTU_KNOWLEDGE_HOME"), "knowledge.db")); err == nil {
		t.Fatal("unexpected shared data home")
	}

	home := t.TempDir()
	var mountPoint string
	if usingTmpfs {
		if syscall.Geteuid() != 0 {
			t.Skip("OS disk ceiling requires root to mount a constrained filesystem")
		}
		mountPoint = filepath.Join(home, "constrained-raw")
		if err := os.MkdirAll(mountPoint, 0o700); err != nil {
			t.Fatal(err)
		}
		if err := syscall.Mount("tmpfs", mountPoint, "tmpfs", 0,
			fmt.Sprintf("size=%d,mode=0700", tmpfsCapacity)); err != nil {
			t.Skipf("kernel denied constrained tmpfs: %v", err)
		}
	} else {
		if externalAck != "dedicated-destructive-volume" {
			t.Fatal("external volume fill requires SHUTU_TEST_RAW_VOLUME_ACK=dedicated-destructive-volume")
		}
		if !filepath.IsAbs(externalVolume) || filepath.Clean(externalVolume) == string(filepath.Separator) {
			t.Fatalf("external volume must be an absolute dedicated mount: %q", externalVolume)
		}
		info, err := os.Stat(externalVolume)
		if err != nil {
			t.Fatalf("stat external raw volume: %v", err)
		}
		if !info.IsDir() {
			t.Fatalf("external raw volume is not a directory: %s", externalVolume)
		}
		var capacityStat syscall.Statfs_t
		if err := syscall.Statfs(externalVolume, &capacityStat); err != nil {
			t.Fatalf("stat external raw volume filesystem: %v", err)
		}
		capacity := int64(capacityStat.Blocks) * int64(capacityStat.Bsize)
		if capacity <= 0 || capacity > maxExternalVolumeBytes {
			t.Fatalf("external raw volume capacity = %d bytes, want > 0 and <= %d",
				capacity, maxExternalVolumeBytes)
		}
		mountPoint = filepath.Clean(externalVolume)
	}
	rawDir := filepath.Join(mountPoint, "raw")
	if _, err := os.Lstat(rawDir); !os.IsNotExist(err) {
		t.Fatalf("raw directory must be absent before drill: %s", rawDir)
	}
	for index := 0; index < 4096; index++ {
		filler := filepath.Join(mountPoint, fmt.Sprintf("filler-%d.bin", index))
		if _, err := os.Lstat(filler); !os.IsNotExist(err) {
			t.Fatalf("raw volume contains reserved filler path: %s", filler)
		}
	}
	var fillerFiles []string
	t.Cleanup(func() {
		for _, path := range fillerFiles {
			_ = os.Remove(path)
		}
		if err := os.RemoveAll(rawDir); err != nil {
			t.Errorf("remove test raw store: %v", err)
		}
		if usingTmpfs {
			if err := syscall.Unmount(mountPoint, 0); err != nil {
				t.Errorf("unmount constrained raw filesystem: %v", err)
			}
		}
	})

	db, err := storage.Open(filepath.Join(home, "knowledge.db"))
	if err != nil {
		t.Fatalf("open database: %v", err)
	}
	raw, err := storage.NewRawFileStore(rawDir)
	if err != nil {
		t.Fatalf("open constrained raw store: %v", err)
	}
	manager := jobs.New(db, 2)
	if err := manager.Start(context.Background()); err != nil {
		t.Fatalf("start jobs: %v", err)
	}
	service := NewService(db, raw, config.Defaults(), manager)
	t.Cleanup(func() {
		manager.Stop()
		_ = db.Close()
	})

	base, err := service.CreateBase("OS Disk Full Corpus", "", "", BaseConfig{
		SmartChunk:    boolPtr(false),
		ChunkSize:     2048,
		ChunkOverlap:  0,
		SemanticChunk: boolPtr(false),
	})
	if err != nil {
		t.Fatalf("create base: %v", err)
	}
	ctx := context.Background()
	buildStart := time.Now()
	expectedRaw := make(map[string]string, corpusCount+1)
	docs := make([]Document, 0, corpusCount)
	for index := 0; index < corpusCount; index++ {
		marker := fmt.Sprintf("enospcactive%06d", index)
		data := corpusMaintenanceBytes(marker, corpusBytes)
		doc, err := service.AddFileDocument(ctx, base.ID, fmt.Sprintf("corpus-%06d.md", index), data, "")
		if err != nil {
			t.Fatalf("import corpus document %d: %v", index, err)
		}
		if doc.Status != StatusReady || doc.ChunkCount != 1 || doc.RawFilePath == "" {
			t.Fatalf("corpus document %d did not publish cleanly: %+v", index, doc)
		}
		expectedRaw[doc.RawFilePath] = hashBytes(data)
		docs = append(docs, doc)
	}
	buildElapsed := time.Since(buildStart)

	assertActiveRaw := func(want int, wantMarkers map[string]bool) {
		t.Helper()
		active, err := raw.ListAll()
		if err != nil {
			t.Fatal(err)
		}
		if len(active) != want {
			t.Fatalf("active raw files = %d, want %d", len(active), want)
		}
		for _, rel := range active {
			if strings.Contains(rel, ".tmp-") {
				t.Fatalf("raw staging residue survived: %s", rel)
			}
			if wantHash, ok := expectedRaw[rel]; ok {
				data, err := raw.Read(rel)
				if err != nil || hashBytes(data) != wantHash {
					t.Fatalf("published raw changed %s: %v", rel, err)
				}
			} else if len(wantMarkers) > 0 && !wantMarkers[rel] {
				t.Fatalf("unexpected raw file after recovery: %s", rel)
			}
		}
	}
	t.Logf("mounted corpus build complete: elapsed=%s files=%d", time.Since(buildStart), corpusCount)
	assertActiveRaw(corpusCount, nil)
	faultMarker := "enospcquartzsatellite"
	faultText := fmt.Sprintf("# Fault document\n\nThe %s must remain intact. ", faultMarker) +
		strings.Repeat("Fault corpus payload forces a real raw allocation and recovers after capacity returns. ", faultRepeats)
	faultData := []byte(faultText)
	faultDemand := int64(len(faultData))
	var statfsBefore syscall.Statfs_t
	if err := syscall.Statfs(mountPoint, &statfsBefore); err != nil {
		t.Fatalf("stat constrained filesystem: %v", err)
	}
	freeBefore := int64(statfsBefore.Bavail) * int64(statfsBefore.Bsize)
	if freeBefore < faultDemand {
		t.Fatalf("free constrained bytes = %d, want at least fault demand %d", freeBefore, faultDemand)
	}

	// Consume almost all free capacity with ordinary files. The remaining
	// demand is deliberately larger than the remaining space, so the import's
	// real temp write enters the kernel's ENOSPC path.
	reserve := int64(4 << 10)
	fillerChunk := int64(1 << 20)
	fillerBuffer := make([]byte, fillerChunk)
	for freeBefore > reserve {
		writeSize := freeBefore - reserve
		if writeSize > fillerChunk {
			writeSize = fillerChunk
		}
		path := filepath.Join(mountPoint, fmt.Sprintf("filler-%d.bin", len(fillerFiles)))
		err := os.WriteFile(path, fillerBuffer[:writeSize], 0o600)
		if err != nil {
			_ = os.Remove(path)
			if !errors.Is(err, syscall.ENOSPC) {
				t.Fatalf("fill constrained filesystem: %v", err)
			}
			_ = os.Remove(path)
			fillerChunk /= 2
			if fillerChunk < 4096 {
				break
			}
			continue
		}
		fillerFiles = append(fillerFiles, path)
		if err := syscall.Statfs(mountPoint, &statfsBefore); err != nil {
			t.Fatalf("stat constrained filesystem after filler: %v", err)
		}
		freeBefore = int64(statfsBefore.Bavail) * int64(statfsBefore.Bsize)
	}
	if freeBefore >= faultDemand {
		t.Fatalf("could not constrain filesystem: free=%d faultDemand=%d", freeBefore, faultDemand)
	}
	faultID, err := jobs.NewID()
	if err != nil {
		t.Fatal(err)
	}
	faultStart := time.Now()
	_, faultErr := service.AddFileDocumentWithID(ctx, base.ID, faultID,
		"fault-document.md", faultData, "")
	faultElapsed := time.Since(faultStart)
	if faultErr == nil {
		t.Fatal("import succeeded with no OS capacity for raw bytes")
	}
	if !strings.Contains(strings.ToLower(faultErr.Error()), "no space left on device") {
		t.Fatalf("unexpected OS disk-full error: %v", faultErr)
	}

	var activeDocs, readyDocs, failedDocs int64
	if err := db.QueryRow(`SELECT COUNT(*) FROM documents WHERE lifecycle_state = 'active'`).Scan(&activeDocs); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRow(`SELECT COUNT(*) FROM documents WHERE status = ?`, StatusReady).Scan(&readyDocs); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRow(`SELECT COUNT(*) FROM documents WHERE status = ?`, StatusFailed).Scan(&failedDocs); err != nil {
		t.Fatal(err)
	}
	if activeDocs != corpusCount+1 || readyDocs != corpusCount || failedDocs != 1 {
		t.Fatalf("post-fault documents: active=%d ready=%d failed=%d",
			activeDocs, readyDocs, failedDocs)
	}
	faulted, err := service.store.getDocument(faultID)
	if err != nil {
		t.Fatalf("load faulted document: %v", err)
	}
	if faulted.Status != StatusFailed || faulted.ErrorCode != ErrParseFailed ||
		faulted.RawFilePath != "" || faulted.ChunkCount != 0 {
		t.Fatalf("faulted document state: %+v", faulted)
	}
	assertActiveRaw(corpusCount, nil)
	for _, index := range []int{0, corpusCount / 2, corpusCount - 1} {
		result, err := service.Search(ctx, SearchRequest{
			Query: fmt.Sprintf("enospcactive%06d", index), Mode: "lexical", TopK: 4,
		})
		if err != nil || result.Total < 1 || result.Hits[0].DocID != docs[index].ID {
			t.Fatalf("published corpus %d became unavailable: total=%d err=%v", index, result.Total, err)
		}
	}
	leaked, err := service.Search(ctx, SearchRequest{Query: faultMarker, Mode: "lexical", TopK: 4})
	if err != nil || leaked.Total != 0 {
		t.Fatalf("faulted document leaked into search: total=%d err=%v", leaked.Total, err)
	}
	var integrity string
	if err := db.QueryRow(`PRAGMA integrity_check`).Scan(&integrity); err != nil || integrity != "ok" {
		t.Fatalf("SQLite integrity after raw ENOSPC = %q err=%v", integrity, err)
	}

	for _, path := range fillerFiles {
		if err := os.Remove(path); err != nil {
			t.Fatalf("restore OS capacity: %v", err)
		}
	}
	fillerFiles = nil
	recoveryStart := time.Now()
	recovered, err := service.AddFileDocumentWithID(ctx, base.ID, faultID,
		"fault-document.md", faultData, "")
	recoveryElapsed := time.Since(recoveryStart)
	if err != nil {
		t.Fatalf("retry after OS capacity restored: %v", err)
	}
	recovered, err = service.store.getDocument(recovered.ID)
	if err != nil {
		t.Fatalf("load recovered document: %v", err)
	}
	if recovered.Status != StatusReady || recovered.ChunkCount <= 1 || recovered.RawFilePath == "" {
		t.Fatalf("recovered document: %+v", recovered)
	}
	expectedRaw[recovered.RawFilePath] = hashBytes(faultData)
	assertActiveRaw(corpusCount+1, nil)
	result, err := service.Search(ctx, SearchRequest{
		Query: faultMarker, Mode: "lexical", TopK: 4,
	})
	if err != nil || result.Total < 1 || result.Hits[0].DocID != faultID || result.Hits[0].IndexGeneration != 1 {
		t.Fatalf("recovered search: total=%d result=%+v err=%v", result.Total, result, err)
	}

	t.Logf("OS raw ENOSPC corpus docs=%d corpusRawBytes=%d faultBytes=%d freeBeforeFault=%d build=%s fault=%s recovery=%s",
		corpusCount, corpusCount*corpusBytes, faultDemand, freeBefore,
		buildElapsed, faultElapsed, recoveryElapsed)
}
