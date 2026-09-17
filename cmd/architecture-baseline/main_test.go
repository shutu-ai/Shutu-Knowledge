package main

import (
	"database/sql"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	_ "modernc.org/sqlite"
)

func TestNamedPathsRejectsDuplicateAndMalformedValues(t *testing.T) {
	var paths namedPaths
	if err := paths.Set("one=/tmp/one"); err != nil {
		t.Fatal(err)
	}
	if err := paths.Set("one=/tmp/two"); err == nil {
		t.Fatal("duplicate name accepted")
	}
	if err := paths.Set("bad"); err == nil {
		t.Fatal("malformed path accepted")
	}
}

func TestScanDatasetContainsMetadataOnlyAndStableOrdering(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "nested"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "nested", "note.md"), []byte("private marker\nsecond line"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "manifest.json"), []byte(`{"id":"model/demo","kind":"embedding","artifacts":["model.onnx"]}`), 0o600); err != nil {
		t.Fatal(err)
	}
	first, err := scanDataset("demo", root)
	if err != nil {
		t.Fatal(err)
	}
	second, err := scanDataset("demo", root)
	if err != nil {
		t.Fatal(err)
	}
	firstJSON, _ := json.Marshal(first)
	secondJSON, _ := json.Marshal(second)
	if string(firstJSON) != string(secondJSON) {
		t.Fatalf("scan is not stable:\n%s\n%s", firstJSON, secondJSON)
	}
	if first.Text.Files != 2 || first.Text.Lines != 3 || first.MaxDepth != 2 {
		t.Fatalf("text/depth summary = %+v", first)
	}
	if len(first.Models) != 1 || first.Models[0].ID != "model/demo" {
		t.Fatalf("models = %+v", first.Models)
	}
	data, _ := json.Marshal(first)
	if strings.Contains(string(data), "private marker") {
		t.Fatal("raw content leaked into report")
	}
}

func TestRedactSensitiveConfigValues(t *testing.T) {
	value := map[string]any{"apiKey": "secret", "nested": map[string]any{"password": "secret2", "model": "safe"}}
	redacted := redact(value, "")
	data, _ := json.Marshal(redacted)
	if strings.Contains(string(data), "secret") || !strings.Contains(string(data), "safe") {
		t.Fatalf("redaction = %s", data)
	}
}

func TestReportFingerprintIgnoresCollectionTimeAndHostVolatility(t *testing.T) {
	base := report{
		SchemaVersion:  1,
		GeneratedAtUTC: "2026-09-15T00:00:00Z",
		Tool:           "architecture-baseline-v1",
		Config:         &configSnapshot{Path: `C:\first\config.yaml`, SHA256: "config-sha"},
		Host:           hostSnapshot{OS: "windows", Arch: "amd64", CPUs: 16, AvailableBytes: 100, MemoryBytes: 200, ResourcePath: `C:\first`},
		Datasets:       []datasetSnapshot{{Name: "base", Root: `C:\first\dataset`, Files: []fileSnapshot{{Path: "a.txt", Bytes: 1, SHA256: "file-sha", Kind: "file"}}}},
		Databases:      []databaseSnapshot{{Name: "knowledge", Path: `C:\first\knowledge.db`, SHA256: "db-sha"}},
	}
	changedEnvironment := base
	changedEnvironment.GeneratedAtUTC = "2026-09-15T00:05:00Z"
	changedEnvironment.Host = hostSnapshot{OS: "windows", Arch: "amd64", CPUs: 32, AvailableBytes: 999, MemoryBytes: 400, ResourcePath: `D:\second`}
	changedEnvironment.Config = &configSnapshot{Path: `D:\second\config.yaml`, SHA256: "config-sha"}
	changedEnvironment.Datasets = []datasetSnapshot{{Name: "base", Root: `D:\second\dataset`, Files: []fileSnapshot{{Path: "a.txt", Bytes: 1, SHA256: "file-sha", Kind: "file"}}}}
	changedEnvironment.Databases = []databaseSnapshot{{Name: "knowledge", Path: `D:\second\knowledge.db`, SHA256: "db-sha"}}
	if first, second := reportFingerprint(base), reportFingerprint(changedEnvironment); first != second {
		t.Fatalf("fingerprint changed with collection time/host paths or capacity: %s != %s", first, second)
	}
}

func TestScanDatabaseIsReadOnlyAndReportsStorageMetadata(t *testing.T) {
	path := filepath.Join(t.TempDir(), "knowledge.db")
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	_, err = db.Exec(`
CREATE TABLE storage_format (id INTEGER PRIMARY KEY, format_version INTEGER, min_reader_version INTEGER, min_writer_version INTEGER, migration_status TEXT);
INSERT INTO storage_format VALUES (1, 2, 7, 7, 'ready');
CREATE TABLE documents (id TEXT, status TEXT, char_count INTEGER);
INSERT INTO documents VALUES ('doc-1', 'ready', 12);
CREATE TABLE chunks (embedding_model TEXT, embedding BLOB);
INSERT INTO chunks VALUES ('fake:model', '[0.1,0.2,0.3]');`)
	if err != nil {
		_ = db.Close()
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	got, err := scanDatabase("knowledge", path)
	if err != nil {
		t.Fatal(err)
	}
	if !got.StorageFormat.Available || got.StorageFormat.FormatVersion != 2 ||
		got.Documents["ready"] != 1 || got.Documents["characters"] != 12 || got.Chunks != 1 {
		t.Fatalf("database snapshot = %+v", got)
	}
	if len(got.EmbeddingModels) != 1 || got.EmbeddingModels[0].Model != "fake:model" ||
		len(got.EmbeddingModels[0].Dimensions) != 1 || got.EmbeddingModels[0].Dimensions[0] != 3 {
		t.Fatalf("embedding snapshot = %+v", got.EmbeddingModels)
	}
}
