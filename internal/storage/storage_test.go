package storage

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

const storageQuarantineTestDir = "quarantine"

func assertPOSIXMode(t *testing.T, path string, want os.FileMode) {
	t.Helper()
	if runtime.GOOS == "windows" {
		return
	}
	info, err := os.Stat(path)
	if err != nil || info.Mode().Perm() != want {
		t.Fatalf("mode %s: %v %v", path, info, err)
	}
}

func openTestDB(t *testing.T) *DB {
	t.Helper()
	db, err := Open(filepath.Join(t.TempDir(), "knowledge.db"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db
}

func TestDBDoesNotBypassWriterWhenWriterIsUnavailable(t *testing.T) {
	raw, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer raw.Close()
	db := &DB{DB: raw}

	if _, err := db.Exec("CREATE TABLE bypass_guard (value TEXT)"); !errors.Is(err, ErrWriterStopped) {
		t.Fatalf("Exec error = %v, want %v", err, ErrWriterStopped)
	}
	if err := db.Write(context.Background(), NormalWrite, func(context.Context, *sql.DB) error {
		t.Fatal("writer callback executed without a writer")
		return nil
	}); !errors.Is(err, ErrWriterStopped) {
		t.Fatalf("Write error = %v, want %v", err, ErrWriterStopped)
	}
	if err := db.WriteTx(context.Background(), NormalWrite, nil, func(*sql.Tx) error {
		t.Fatal("transaction callback executed without a writer")
		return nil
	}); !errors.Is(err, ErrWriterStopped) {
		t.Fatalf("WriteTx error = %v, want %v", err, ErrWriterStopped)
	}

	var tables int
	if err := raw.QueryRow("SELECT COUNT(*) FROM sqlite_master WHERE type = 'table' AND name = 'bypass_guard'").Scan(&tables); err != nil {
		t.Fatal(err)
	}
	if tables != 0 {
		t.Fatalf("bypass guard table count = %d, want 0", tables)
	}
}

func TestDBCloseWithContextHonorsCanceledShutdown(t *testing.T) {
	db := openTestDB(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	started := time.Now()
	err := db.CloseWithContext(ctx)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("CloseWithContext error = %v, want context.Canceled", err)
	}
	if elapsed := time.Since(started); elapsed > time.Second {
		t.Fatalf("canceled CloseWithContext took %s", elapsed)
	}
	// The explicit close above owns the handles; prevent the cleanup hook from
	// treating a second close error as the assertion under test.
	db = nil
}

func TestMigrateAppliesAndIsIdempotent(t *testing.T) {
	db := openTestDB(t)
	v, err := SchemaVersion(db.DB)
	if err != nil {
		t.Fatal(err)
	}
	if v < 1 {
		t.Fatalf("expected applied migrations, got version %d", v)
	}
	// Re-running must not fail or duplicate.
	if err := Migrate(db.DB); err != nil {
		t.Fatalf("re-migrate: %v", err)
	}
	var count int
	if err := db.QueryRow(`SELECT COUNT(*) FROM bases`).Scan(&count); err != nil {
		t.Fatalf("bases table missing: %v", err)
	}
	if err := db.QueryRow(`SELECT COUNT(*) FROM chunks`).Scan(&count); err != nil {
		t.Fatalf("chunks table missing: %v", err)
	}
	// FTS5 must be functional (external-content table with triggers).
	if _, err := db.Exec(`INSERT INTO chunks (id, doc_id, base_id, idx, text, context, created_at) VALUES ('c1','d1','b1',0,'hello world','t','0')`); err != nil {
		t.Fatalf("insert chunk: %v", err)
	}
	rows, err := db.Query(`SELECT rowid FROM chunk_fts WHERE chunk_fts MATCH 'hello'`)
	if err != nil {
		t.Fatalf("fts match: %v", err)
	}
	defer rows.Close()
	if !rows.Next() {
		t.Fatal("fts did not index inserted chunk")
	}
}

func TestStorageFormatCompatibilityEnvelope(t *testing.T) {
	db := openTestDB(t)
	info, err := StorageFormat(db.DB)
	if err != nil {
		t.Fatal(err)
	}
	if info.FormatVersion != CurrentStorageFormatVersion ||
		info.MinReaderVersion != MinStorageReaderVersion ||
		info.MinWriterVersion != MinStorageWriterVersion ||
		info.MigrationStatus != "ready" {
		t.Fatalf("format envelope: %+v", info)
	}
	if info.SchemaVersion < 16 || info.SupportedMigrations < info.SchemaVersion {
		t.Fatalf("migration bounds: %+v", info)
	}
	if err := Migrate(db.DB); err != nil {
		t.Fatal(err)
	}
	var operationColumns, itemTables int
	if err := db.QueryRow(`SELECT COUNT(*) FROM pragma_table_info('operations') WHERE name IN ('input_ref', 'expected_target_epoch', 'retained_until')`).Scan(&operationColumns); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRow(`SELECT COUNT(*) FROM sqlite_master WHERE type = 'table' AND name = 'operation_items'`).Scan(&itemTables); err != nil {
		t.Fatal(err)
	}
	if operationColumns != 3 || itemTables != 1 {
		t.Fatalf("operation envelope schema columns=%d itemTables=%d", operationColumns, itemTables)
	}
}

func TestStorageMetadataContextHonorsCancellation(t *testing.T) {
	db := openTestDB(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := SchemaVersionContext(ctx, db.DB); !errors.Is(err, context.Canceled) {
		t.Fatalf("schema version error = %v, want context.Canceled", err)
	}
	if _, err := StorageFormatContext(ctx, db.DB); !errors.Is(err, context.Canceled) {
		t.Fatalf("storage format error = %v, want context.Canceled", err)
	}
}

func TestStorageFormatRejectsIncompatibleDatabase(t *testing.T) {
	t.Run("future schema", func(t *testing.T) {
		db := openTestDB(t)
		path := db.Path()
		if _, err := db.Exec(`INSERT INTO schema_migrations (version, name) VALUES (999999, 'future')`); err != nil {
			t.Fatal(err)
		}
		if err := db.Close(); err != nil {
			t.Fatal(err)
		}
		_, err := Open(path)
		if !errors.Is(err, ErrSchemaTooNew) {
			t.Fatalf("future schema error = %v, want ErrSchemaTooNew", err)
		}
	})
	t.Run("future writer", func(t *testing.T) {
		db := openTestDB(t)
		path := db.Path()
		if _, err := db.Exec(`UPDATE storage_format SET min_writer_version = 9 WHERE id = 1`); err != nil {
			t.Fatal(err)
		}
		if err := db.Close(); err != nil {
			t.Fatal(err)
		}
		_, err := Open(path)
		if !errors.Is(err, ErrStorageTooNew) {
			t.Fatalf("future writer error = %v, want ErrStorageTooNew", err)
		}
	})
	t.Run("interrupted migration", func(t *testing.T) {
		db := openTestDB(t)
		path := db.Path()
		if _, err := db.Exec(`UPDATE storage_format SET migration_status = 'migrating' WHERE id = 1`); err != nil {
			t.Fatal(err)
		}
		if err := db.Close(); err != nil {
			t.Fatal(err)
		}
		_, err := Open(path)
		if !errors.Is(err, ErrStorageUnavailable) {
			t.Fatalf("interrupted migration error = %v, want ErrStorageUnavailable", err)
		}
	})
}

func TestRawStoreRoundTripAndGuards(t *testing.T) {
	root := filepath.Join(t.TempDir(), "raw")
	store, err := NewRawFileStore(root)
	if err != nil {
		t.Fatal(err)
	}
	rel, err := store.Write("base1", "doc1", ".pdf", []byte("data"))
	if err != nil {
		t.Fatalf("write: %v", err)
	}
	if rel != "base1/doc1.pdf" {
		t.Fatalf("rel path: %s", rel)
	}
	assertPOSIXMode(t, filepath.Join(root, "base1", "doc1.pdf"), 0o600)
	assertPOSIXMode(t, filepath.Join(root, "base1"), 0o700)
	data, err := store.Read(rel)
	if err != nil || string(data) != "data" {
		t.Fatalf("read: %v %q", err, data)
	}
	tree, err := store.WriteRel("base1", "sub/dir/note.md", []byte("tree"))
	if err != nil {
		t.Fatalf("writeRel: %v", err)
	}
	if tree != "base1/sub/dir/note.md" {
		t.Fatalf("tree path: %s", tree)
	}
	all, err := store.ListAll()
	if err != nil || len(all) != 2 {
		t.Fatalf("listAll: %v %v", all, err)
	}
	if err := store.DeleteBase("base1"); err != nil {
		t.Fatalf("deleteBase: %v", err)
	}
	data, err = store.Read(rel)
	if err != nil || data != nil {
		t.Fatalf("read after delete: %v %v", data, err)
	}
	for _, bad := range []string{"../escape", "a/../b", `a\..\b`, "", "a/b"} {
		if _, err := store.Write(bad, "doc", ".txt", nil); err == nil {
			t.Fatalf("expected error for segment %q", bad)
		}
	}
	if _, err := store.pathOf("../outside"); err == nil || !strings.Contains(err.Error(), "unsafe") {
		t.Fatalf("pathOf escape guard: %v", err)
	}
}

func TestIsolatedBackupRestoreAndIncompatibleReaderDrill(t *testing.T) {
	home := t.TempDir()
	sourcePath := filepath.Join(home, "source.db")
	backupPath := filepath.Join(home, "rollback.db")
	restorePath := filepath.Join(home, "restored.db")

	source, err := Open(sourcePath)
	if err != nil {
		t.Fatal(err)
	}
	_, err = source.Exec(`INSERT INTO bases (id, name, description, grp, config, created_at, updated_at)
		VALUES ('base-backup', 'Backup', '', '', '{}', 1, 1)`)
	if err != nil {
		t.Fatal(err)
	}
	_, err = source.Exec(`INSERT INTO documents (id, base_id, title, source_type, status, created_at)
		VALUES ('doc-backup', 'base-backup', 'Backup Doc', 'text', 'ready', 1)`)
	if err != nil {
		t.Fatal(err)
	}
	_, err = source.Exec(`INSERT INTO chunks (id, doc_id, base_id, idx, text, context, created_at)
		VALUES ('chunk-backup', 'doc-backup', 'base-backup', 0, 'rollback marker delta', 'Backup Doc', 1)`)
	if err != nil {
		t.Fatal(err)
	}
	formatAtBackup, err := StorageFormat(source.DB)
	if err != nil {
		t.Fatal(err)
	}
	// Quiescent backup: stop writers, close WAL handles, then copy the main
	// database. The hash records the exact rollback point.
	if err := source.Close(); err != nil {
		t.Fatal(err)
	}
	backupBytes, err := os.ReadFile(sourcePath)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(backupPath, backupBytes, 0o600); err != nil {
		t.Fatal(err)
	}
	backupHash := sha256.Sum256(backupBytes)

	// After the backup, the source advances. The isolated restore must return
	// to the recorded point and clearly expose that later delta as absent.
	source, err = Open(sourcePath)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := source.Exec(`INSERT INTO bases (id, name, description, grp, config, created_at, updated_at)
		VALUES ('base-post-backup', 'Post Backup', '', '', '{}', 2, 2)`); err != nil {
		t.Fatal(err)
	}
	if err := source.Close(); err != nil {
		t.Fatal(err)
	}

	restoreBytes, err := os.ReadFile(backupPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(restorePath, restoreBytes, 0o600); err != nil {
		t.Fatal(err)
	}
	restoreHash := sha256.Sum256(restoreBytes)
	if backupHash != restoreHash {
		t.Fatal("restore bytes do not match recorded backup hash")
	}
	restored, err := Open(restorePath)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = restored.Close() })
	formatAtRestore, err := StorageFormat(restored.DB)
	if err != nil {
		t.Fatal(err)
	}
	if formatAtRestore != formatAtBackup {
		t.Fatalf("format changed during restore: backup=%+v restore=%+v", formatAtBackup, formatAtRestore)
	}
	var baseCount int
	if err := restored.QueryRow(`SELECT COUNT(*) FROM bases`).Scan(&baseCount); err != nil {
		t.Fatal(err)
	}
	if baseCount != 1 {
		t.Fatalf("restored base count = %d, want backup point", baseCount)
	}
	var rows int
	if err := restored.QueryRow(`SELECT COUNT(*) FROM chunk_fts WHERE chunk_fts MATCH 'rollback'`).Scan(&rows); err != nil {
		t.Fatal(err)
	}
	if rows != 1 {
		t.Fatalf("restored FTS rows = %d, want 1", rows)
	}

	source, err = Open(sourcePath)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = source.Close() })
	if err := source.QueryRow(`SELECT COUNT(*) FROM bases`).Scan(&baseCount); err != nil {
		t.Fatal(err)
	}
	if baseCount != 2 {
		t.Fatalf("source base count = %d, want backup plus one delta", baseCount)
	}
}

func TestSyntheticLargeDatabaseBackupRollbackDrill(t *testing.T) {
	const (
		documentCount = 1200
		chunksPerDoc  = 20
		chunkCount    = documentCount * chunksPerDoc
		deltaChunks   = 500
	)
	home := t.TempDir()
	sourcePath := filepath.Join(home, "large-source.db")
	backupPath := filepath.Join(home, "large-rollback.db")
	restorePath := filepath.Join(home, "large-restored.db")
	fill := strings.Repeat(" rollback corpus alpha beta gamma delta epsilon zeta", 10)

	source, err := Open(sourcePath)
	if err != nil {
		t.Fatal(err)
	}
	buildStart := time.Now()
	tx, err := source.Begin()
	if err != nil {
		t.Fatal(err)
	}
	baseStmt, err := tx.Prepare(`INSERT INTO bases
		(id, name, description, grp, config, created_at, updated_at)
		VALUES (?, 'Synthetic Rollback', '', '', '{}', 1, 1)`)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := baseStmt.Exec("synthetic-backup-base"); err != nil {
		t.Fatal(err)
	}
	if err := baseStmt.Close(); err != nil {
		t.Fatal(err)
	}
	docStmt, err := tx.Prepare(`INSERT INTO documents
		(id, base_id, title, source_type, status, lifecycle_state, source_version,
		 active_index_generation, raw_text, char_count, chunk_count, created_at, updated_at)
		VALUES (?, 'synthetic-backup-base', ?, 'text', 'ready', 'active', 1, 1, ?, ?, ?, 1, 1)`)
	if err != nil {
		t.Fatal(err)
	}
	generationStmt, err := tx.Prepare(`INSERT INTO document_generations
		(doc_id, index_generation, source_version, chunk_count, created_at,
		 raw_file_path, content_hash)
		VALUES (?, 1, 1, ?, 1, '', '')`)
	if err != nil {
		t.Fatal(err)
	}
	chunkStmt, err := tx.Prepare(`INSERT INTO chunks
		(id, doc_id, base_id, idx, text, heading, context, created_at, index_generation)
		VALUES (?, ?, ?, ?, ?, NULL, ?, 1, 1)`)
	if err != nil {
		t.Fatal(err)
	}
	for documentIndex := 0; documentIndex < documentCount; documentIndex++ {
		docID := fmt.Sprintf("large-doc-%05d", documentIndex)
		title := fmt.Sprintf("Large document %05d backupsentinel%05d", documentIndex, documentIndex)
		rawText := title + fill
		if _, err := docStmt.Exec(docID, title, rawText, len(rawText), chunksPerDoc); err != nil {
			t.Fatal(err)
		}
		if _, err := generationStmt.Exec(docID, chunksPerDoc); err != nil {
			t.Fatal(err)
		}
		for chunkIndex := 0; chunkIndex < chunksPerDoc; chunkIndex++ {
			chunkID := fmt.Sprintf("large-chunk-%05d-%02d", documentIndex, chunkIndex)
			text := fmt.Sprintf("backupsentinel%05d chunk %02d %s",
				documentIndex, chunkIndex, fill)
			if _, err := chunkStmt.Exec(chunkID, docID, "synthetic-backup-base",
				chunkIndex, text, title); err != nil {
				t.Fatal(err)
			}
		}
	}
	if err := chunkStmt.Close(); err != nil {
		t.Fatal(err)
	}
	if err := generationStmt.Close(); err != nil {
		t.Fatal(err)
	}
	if err := docStmt.Close(); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	buildElapsed := time.Since(buildStart)
	if err := source.Close(); err != nil {
		t.Fatal(err)
	}

	backupBytes, err := os.ReadFile(sourcePath)
	if err != nil {
		t.Fatal(err)
	}
	backupStart := time.Now()
	if err := os.WriteFile(backupPath, backupBytes, 0o600); err != nil {
		t.Fatal(err)
	}
	backupElapsed := time.Since(backupStart)
	backupHash := sha256.Sum256(backupBytes)
	backupSize := int64(len(backupBytes))

	// Advance the source past the rollback point. The restored copy must not
	// contain this delta.
	source, err = Open(sourcePath)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := source.Exec(`INSERT INTO bases
		(id, name, description, grp, config, created_at, updated_at)
		VALUES ('post-backup-base', 'Post Backup', '', '', '{}', 2, 2)`); err != nil {
		t.Fatal(err)
	}
	deltaTx, err := source.Begin()
	if err != nil {
		t.Fatal(err)
	}
	deltaDocStmt, err := deltaTx.Prepare(`INSERT INTO documents
		(id, base_id, title, source_type, status, created_at)
		VALUES ('post-backup-doc', 'post-backup-base', 'Post Backup', 'text', 'ready', 2)`)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := deltaDocStmt.Exec(); err != nil {
		t.Fatal(err)
	}
	if err := deltaDocStmt.Close(); err != nil {
		t.Fatal(err)
	}
	deltaChunkStmt, err := deltaTx.Prepare(`INSERT INTO chunks
		(id, doc_id, base_id, idx, text, context, created_at)
		VALUES (?, 'post-backup-doc', 'post-backup-base', ?, ?, 'post-backup marker', 2)`)
	if err != nil {
		t.Fatal(err)
	}
	for chunkIndex := 0; chunkIndex < deltaChunks; chunkIndex++ {
		if _, err := deltaChunkStmt.Exec(fmt.Sprintf("post-backup-chunk-%03d", chunkIndex),
			chunkIndex, fmt.Sprintf("postbackupsentinel chunk %03d %s", chunkIndex, fill)); err != nil {
			t.Fatal(err)
		}
	}
	if err := deltaChunkStmt.Close(); err != nil {
		t.Fatal(err)
	}
	if err := deltaTx.Commit(); err != nil {
		t.Fatal(err)
	}
	var sourceDeltaChunks int
	if err := source.QueryRow(`SELECT COUNT(*) FROM chunks
		WHERE base_id = 'post-backup-base'`).Scan(&sourceDeltaChunks); err != nil {
		t.Fatal(err)
	}
	if sourceDeltaChunks != deltaChunks {
		t.Fatalf("source post-backup chunks = %d, want %d", sourceDeltaChunks, deltaChunks)
	}
	if err := source.Close(); err != nil {
		t.Fatal(err)
	}
	sourceAfterDelta, err := os.Stat(sourcePath)
	if err != nil {
		t.Fatal(err)
	}

	restoreStart := time.Now()
	restoredBytes, err := os.ReadFile(backupPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(restorePath, restoredBytes, 0o600); err != nil {
		t.Fatal(err)
	}
	restoreElapsed := time.Since(restoreStart)
	if sha256.Sum256(restoredBytes) != backupHash {
		t.Fatal("restored bytes do not match the recorded backup hash")
	}

	restoreVerifyStart := time.Now()
	restored, err := Open(restorePath)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = restored.Close() })
	formatAtBackup, err := StorageFormat(restored.DB)
	if err != nil {
		t.Fatal(err)
	}
	if formatAtBackup.FormatVersion != CurrentStorageFormatVersion ||
		formatAtBackup.MinReaderVersion != MinStorageReaderVersion ||
		formatAtBackup.MinWriterVersion != MinStorageWriterVersion {
		t.Fatalf("restored format envelope = %+v", formatAtBackup)
	}
	var restoredChunks, restoredDocs, restoredGenerations, postBackupBases, postBackupChunks int
	if err := restored.QueryRow(`SELECT COUNT(*) FROM chunks`).Scan(&restoredChunks); err != nil {
		t.Fatal(err)
	}
	if err := restored.QueryRow(`SELECT COUNT(*) FROM documents`).Scan(&restoredDocs); err != nil {
		t.Fatal(err)
	}
	if err := restored.QueryRow(`SELECT COUNT(*) FROM document_generations`).Scan(&restoredGenerations); err != nil {
		t.Fatal(err)
	}
	if err := restored.QueryRow(`SELECT COUNT(*) FROM bases WHERE id = 'post-backup-base'`).Scan(&postBackupBases); err != nil {
		t.Fatal(err)
	}
	if err := restored.QueryRow(`SELECT COUNT(*) FROM chunks
		WHERE base_id = 'post-backup-base'`).Scan(&postBackupChunks); err != nil {
		t.Fatal(err)
	}
	if restoredChunks != chunkCount || restoredDocs != documentCount ||
		restoredGenerations != documentCount || postBackupBases != 0 || postBackupChunks != 0 {
		t.Fatalf("restored shape = chunks/docs/generations/postbase/postchunks %d/%d/%d/%d/%d",
			restoredChunks, restoredDocs, restoredGenerations, postBackupBases, postBackupChunks)
	}
	var ftsRows int
	if err := restored.QueryRow(`SELECT COUNT(*) FROM chunk_fts
		WHERE chunk_fts MATCH 'backupsentinel00421'`).Scan(&ftsRows); err != nil {
		t.Fatal(err)
	}
	if ftsRows != chunksPerDoc {
		t.Fatalf("restored FTS retrieval rows = %d, want %d", ftsRows, chunksPerDoc)
	}
	var generationOrphans int
	if err := restored.QueryRow(`SELECT COUNT(*) FROM documents d
		LEFT JOIN document_generations g
		  ON g.doc_id = d.id AND g.index_generation = d.active_index_generation
		WHERE g.doc_id IS NULL`).Scan(&generationOrphans); err != nil {
		t.Fatal(err)
	}
	if generationOrphans != 0 {
		t.Fatalf("restored generation mapping orphans = %d", generationOrphans)
	}

	// Exercise deletion after validation; the external-content FTS trigger must
	// remove indexed terms with the chunk rows.
	if _, err := restored.Exec(`DELETE FROM chunks WHERE doc_id = 'large-doc-00000'`); err != nil {
		t.Fatal(err)
	}
	if _, err := restored.Exec(`DELETE FROM documents WHERE id = 'large-doc-00000'`); err != nil {
		t.Fatal(err)
	}
	var remainingChunks, deletedFTS int
	if err := restored.QueryRow(`SELECT COUNT(*) FROM chunks
		WHERE doc_id = 'large-doc-00000'`).Scan(&remainingChunks); err != nil {
		t.Fatal(err)
	}
	if err := restored.QueryRow(`SELECT COUNT(*) FROM chunk_fts
		WHERE chunk_fts MATCH 'backupsentinel00000'`).Scan(&deletedFTS); err != nil {
		t.Fatal(err)
	}
	if remainingChunks != 0 || deletedFTS != 0 {
		t.Fatalf("restored delete validation chunks=%d fts=%d", remainingChunks, deletedFTS)
	}
	restoreVerifyElapsed := time.Since(restoreVerifyStart)

	t.Logf("synthetic backup docs=%d chunks=%d build=%s backup=%s restoreCopy=%s verify=%s bytes=%d deltaBytes=%d sha256=%x",
		documentCount, chunkCount, buildElapsed, backupElapsed, restoreElapsed,
		restoreVerifyElapsed, backupSize, sourceAfterDelta.Size()-backupSize, backupHash)
}

func TestRawStoreQuarantineIsOutsideActiveScan(t *testing.T) {
	root := filepath.Join(t.TempDir(), "raw")
	store, err := NewRawFileStore(root)
	if err != nil {
		t.Fatal(err)
	}
	active, err := store.Write("base1", "active", ".txt", []byte("active"))
	if err != nil {
		t.Fatal(err)
	}
	orphan, err := store.Write("base1", "orphan", ".bin", []byte("bytes"))
	if err != nil {
		t.Fatal(err)
	}
	quarantined, size, err := store.Quarantine(orphan)
	if err != nil {
		t.Fatalf("quarantine: %v", err)
	}
	if size != 5 || quarantined != "quarantine/base1/orphan.bin" {
		t.Fatalf("quarantine result: %s %d", quarantined, size)
	}
	if oldData, err := store.Read(orphan); err != nil || oldData != nil {
		t.Fatalf("old path error: %v %q", err, oldData)
	}
	data, err := store.Read(quarantined)
	if err != nil || string(data) != "bytes" {
		t.Fatalf("quarantine read: %v %q", err, data)
	}
	activeTree, err := store.ListAll()
	if err != nil || len(activeTree) != 1 || activeTree[0] != active {
		t.Fatalf("active scan included quarantine: %v %v", activeTree, err)
	}
	count, bytes, err := store.QuarantineStats()
	if err != nil || count != 1 || bytes != 5 {
		t.Fatalf("quarantine stats: %d %d %v", count, bytes, err)
	}
	if err := store.PurgeQuarantine(); err != nil {
		t.Fatal(err)
	}
	if purgedData, err := store.Read(quarantined); err != nil || purgedData != nil {
		t.Fatalf("purge left readable file: %v %q", err, purgedData)
	}
}

func TestRawStorePurgesExpiredQuarantineOnly(t *testing.T) {
	root := filepath.Join(t.TempDir(), "raw")
	store, err := NewRawFileStore(root)
	if err != nil {
		t.Fatal(err)
	}
	active, err := store.Write("base1", "active", ".txt", []byte("active"))
	if err != nil {
		t.Fatal(err)
	}
	_, err = store.Write("base1", "expired", ".bin", []byte("expired"))
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := store.Quarantine("base1/expired.bin"); err != nil {
		t.Fatal(err)
	}
	old := filepath.Join(root, storageQuarantineTestDir, "base1", "expired.bin")
	past := time.Now().Add(-time.Hour)
	if err := os.Chtimes(old, past, past); err != nil {
		t.Fatal(err)
	}
	count, bytes, err := store.PurgeExpiredQuarantine(time.Now())
	if err != nil || count != 1 || bytes != 7 {
		t.Fatalf("expired quarantine purge: %d %d %v", count, bytes, err)
	}
	tree, err := store.ListAll()
	if err != nil || len(tree) != 1 || tree[0] != active {
		t.Fatalf("active tree after expired purge: %v %v", tree, err)
	}
	if _, _, err := store.QuarantineStats(); err != nil {
		t.Fatal(err)
	}
}

func TestRawStoreQuarantineCollisionSuffixAdvances(t *testing.T) {
	root := filepath.Join(t.TempDir(), "raw")
	store, err := NewRawFileStore(root)
	if err != nil {
		t.Fatal(err)
	}
	firstPath, err := store.Write("base1", "orphan", ".bin", []byte("first"))
	if err != nil {
		t.Fatal(err)
	}
	first, _, err := store.Quarantine(firstPath)
	if err != nil || first != "quarantine/base1/orphan.bin" {
		t.Fatalf("first quarantine: %s %v", first, err)
	}
	secondPath, err := store.Write("base1", "orphan", ".bin", []byte("second"))
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan struct{})
	var second string
	var quarantineErr error
	go func() {
		second, _, quarantineErr = store.Quarantine(secondPath)
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("quarantine collision did not terminate")
	}
	if quarantineErr != nil || second != "quarantine/base1/orphan.bin.1" {
		t.Fatalf("second quarantine: %s %v", second, quarantineErr)
	}
	data, err := store.Read(second)
	if err != nil || string(data) != "second" {
		t.Fatalf("collision content: %q %v", data, err)
	}
}

func TestRawStoreWritesReplaceAtomicallyAndConverge(t *testing.T) {
	root := filepath.Join(t.TempDir(), "raw")
	store, err := NewRawFileStore(root)
	if err != nil {
		t.Fatal(err)
	}
	rel, err := store.Write("base1", "source", ".txt", []byte("first"))
	if err != nil {
		t.Fatal(err)
	}

	// Repeated replacement models a retried raw copy. Publication by rename
	// bounds disk to the latest complete file plus any temp that a crash has
	// not yet removed; successful writes leave no staging residue.
	const replacements = 25
	want := []byte("replacement body")
	for range replacements {
		if _, err := store.Write("base1", "source", ".txt", want); err != nil {
			t.Fatal(err)
		}
	}
	data, err := store.Read(rel)
	if err != nil || string(data) != string(want) {
		t.Fatalf("replacement read: %q %v", data, err)
	}
	tempFiles, err := filepath.Glob(filepath.Join(root, "base1", ".*.tmp-*"))
	if err != nil || len(tempFiles) != 0 {
		t.Fatalf("write temp residue: %v %v", tempFiles, err)
	}
	active, err := store.ListAll()
	if err != nil || len(active) != 1 || active[0] != rel {
		t.Fatalf("active disk state: %v %v", active, err)
	}

	quarantined, size, err := store.Quarantine(rel)
	if err != nil || size != int64(len(want)) {
		t.Fatalf("quarantine after replacements: %s %d %v", quarantined, size, err)
	}
	active, err = store.ListAll()
	if err != nil || len(active) != 0 {
		t.Fatalf("active state after quarantine: %v %v", active, err)
	}
	count, bytes, err := store.QuarantineStats()
	if err != nil || count != 1 || bytes != int64(len(want)) {
		t.Fatalf("disk convergence stats: %d %d %v", count, bytes, err)
	}
	if err := store.PurgeQuarantine(); err != nil {
		t.Fatal(err)
	}
	count, bytes, err = store.QuarantineStats()
	if err != nil || count != 0 || bytes != 0 {
		t.Fatalf("purged disk stats: %d %d %v", count, bytes, err)
	}
}

func TestMaintainSQLOptimizesFTSAndThresholdVacuum(t *testing.T) {
	db := openTestDB(t)
	assertPOSIXMode(t, db.Path(), 0o600)
	if _, err := db.Exec(`INSERT INTO chunks (id, doc_id, base_id, idx, text, context, created_at)
		VALUES ('c1','d1','b1',0,'hello world','maintenance','0')`); err != nil {
		t.Fatal(err)
	}
	result, err := db.MaintainSQLite(true, true, 1<<30)
	if err != nil {
		t.Fatal(err)
	}
	if !result.FTSOptimized || result.Vacuumed {
		t.Fatalf("threshold maintenance: %+v", result)
	}
	if result.DatabaseBytes <= 0 || result.DatabaseBytesEnd <= 0 {
		t.Fatalf("database sizes: %+v", result)
	}

	forced, err := db.MaintainSQLite(false, true, 0)
	if err != nil {
		t.Fatal(err)
	}
	if forced.FTSOptimized || !forced.Vacuumed || forced.DatabaseBytesEnd == 0 {
		t.Fatalf("forced vacuum: %+v", forced)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := db.MaintainSQLiteContext(ctx, true, true, 0); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled maintenance error = %v", err)
	}
}
