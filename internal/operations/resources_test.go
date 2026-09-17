package operations

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestFilesystemResourceSamplerReportsDurableAndTemporaryBytes(t *testing.T) {
	root := t.TempDir()
	database := filepath.Join(root, "knowledge.db")
	if err := os.WriteFile(database, []byte("db"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(database+"-wal", []byte("wal"), 0o600); err != nil {
		t.Fatal(err)
	}
	uploads := filepath.Join(root, "uploads")
	if err := os.MkdirAll(filepath.Join(uploads, "nested"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(uploads, "stable.bin"), []byte("stable"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(uploads, "nested", ".partial.tmp-123"), []byte("temp"), 0o600); err != nil {
		t.Fatal(err)
	}

	snapshot := NewFilesystemResourceSampler(database, "", uploads)()
	if snapshot.DatabaseBytes != 2 || snapshot.WALBytes != 3 {
		t.Fatalf("database footprint = %+v", snapshot)
	}
	if snapshot.UploadBytes != 10 || snapshot.RawBytes != 0 || snapshot.TempBytes != 4 {
		t.Fatalf("staging footprint = %+v", snapshot)
	}
	if snapshot.HeapBytes == 0 || snapshot.PeakRSSBytes == 0 {
		t.Fatalf("heap measurement was not populated: %+v", snapshot)
	}
	if snapshot.DiskFreeBytes == 0 {
		t.Fatalf("disk free-space measurement was not populated: %+v", snapshot)
	}
}

func TestFilesystemResourceSamplerCachesDirectoryWalksButRefreshesCheapFields(t *testing.T) {
	root := t.TempDir()
	database := filepath.Join(root, "knowledge.db")
	uploads := filepath.Join(root, "uploads")
	if err := os.MkdirAll(uploads, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(database, []byte("db"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(uploads, "stable.bin"), []byte("one"), 0o600); err != nil {
		t.Fatal(err)
	}

	sampler := newFilesystemResourceSampler(database, "", uploads, time.Hour)
	first := sampler()
	if first.UploadBytes != 3 {
		t.Fatalf("initial upload footprint = %+v", first)
	}
	if err := os.WriteFile(filepath.Join(uploads, "new.bin"), []byte("two"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(database, []byte("database grew"), 0o600); err != nil {
		t.Fatal(err)
	}
	second := sampler()
	if second.UploadBytes != first.UploadBytes {
		t.Fatalf("directory walk was not cached: first=%+v second=%+v", first, second)
	}
	if second.DatabaseBytes != int64(len("database grew")) {
		t.Fatalf("cheap database field was not refreshed: %+v", second)
	}
}
