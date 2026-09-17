package knowledge

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/shutu-ai/shutu-knowledge/internal/storage"
)

const (
	crossReaderChildEnv  = "KNOWLEDGE_CROSS_READER_CHILD"
	crossReaderDBEnv     = "KNOWLEDGE_CROSS_READER_DB"
	crossReaderBaseEnv   = "KNOWLEDGE_CROSS_READER_BASE"
	crossReaderTargetEnv = "KNOWLEDGE_CROSS_READER_TARGET"
	crossReaderWorkEnv   = "KNOWLEDGE_CROSS_READER_WORK"
	crossReaderWait      = 90 * time.Second
)

func TestCrossProcessHistoricalReaderSurvivesGenerationGC(t *testing.T) {
	if os.Getenv(crossReaderChildEnv) == "1" {
		if err := runCrossReaderChild(t); err != nil {
			t.Fatal(err)
		}
		return
	}

	const (
		documentCount = 120
		chunksPerDoc  = 20
		totalChunks   = documentCount * chunksPerDoc
	)
	f := newFixture(t)
	base := f.createBase(t)
	ctx := context.Background()
	target := fmt.Sprintf("cross-reader-%03d", documentCount/2)
	fill := strings.Repeat(" cross-process historical reader alpha beta gamma", 8)

	buildStart := time.Now()
	tx, err := f.service.store.db.DB.Begin()
	if err != nil {
		t.Fatal(err)
	}
	docStmt, err := tx.Prepare(`INSERT INTO documents
		(id, base_id, title, source_type, status, raw_text, char_count, chunk_count,
		 source_version, active_index_generation, created_at, updated_at)
		VALUES (?, ?, ?, 'text', 'ready', ?, ?, ?, 2, 2, 1, 1)`)
	if err != nil {
		t.Fatal(err)
	}
	generationStmt, err := tx.Prepare(`INSERT INTO document_generations
		(doc_id, index_generation, source_version, chunk_count, created_at)
		VALUES (?, ?, ?, ?, 1)`)
	if err != nil {
		t.Fatal(err)
	}
	chunkStmt, err := tx.Prepare(`INSERT INTO chunks
		(id, doc_id, base_id, idx, text, context, created_at, index_generation)
		VALUES (?, ?, ?, ?, ?, ?, 1, ?)`)
	if err != nil {
		t.Fatal(err)
	}
	for documentIndex := 0; documentIndex < documentCount; documentIndex++ {
		docID := fmt.Sprintf("cross-reader-%03d", documentIndex)
		title := fmt.Sprintf("Cross reader %03d corpusunicorn%03d", documentIndex, documentIndex)
		rawText := title + fill
		if _, err := docStmt.Exec(docID, base.ID, title, rawText, len(rawText), chunksPerDoc); err != nil {
			t.Fatal(err)
		}
		for generation := int64(1); generation <= 2; generation++ {
			if _, err := generationStmt.Exec(docID, generation, generation, chunksPerDoc); err != nil {
				t.Fatal(err)
			}
			for chunkIndex := 0; chunkIndex < chunksPerDoc; chunkIndex++ {
				text := fmt.Sprintf("%s generation%d chunk%02d %s",
					title, generation, chunkIndex, fill)
				if _, err := chunkStmt.Exec(
					fmt.Sprintf("cross-reader-%03d-g%d-%02d", documentIndex, generation, chunkIndex),
					docID, base.ID, chunkIndex, text, title, generation,
				); err != nil {
					t.Fatal(err)
				}
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

	oldRawPath, err := f.raw.WriteVersion(base.ID, target, 1, ".txt",
		[]byte("cross-process historical raw"))
	if err != nil {
		t.Fatal(err)
	}
	currentRawPath, err := f.raw.WriteVersion(base.ID, target, 2, ".txt",
		[]byte("cross-process current raw"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.service.store.db.Exec(`UPDATE document_generations
		SET raw_file_path = ? WHERE doc_id = ? AND index_generation = ?`,
		oldRawPath, target, 1); err != nil {
		t.Fatal(err)
	}
	if _, err := f.service.store.db.Exec(`UPDATE document_generations
		SET raw_file_path = ? WHERE doc_id = ? AND index_generation = ?`,
		currentRawPath, target, 2); err != nil {
		t.Fatal(err)
	}

	workspace := filepath.Join(t.TempDir(), "reader")
	if err := os.MkdirAll(workspace, 0o700); err != nil {
		t.Fatal(err)
	}
	readerStart := time.Now()
	testBinary, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	child := exec.Command(testBinary, "-test.run=^TestCrossProcessHistoricalReaderSurvivesGenerationGC$")
	var childLog bytes.Buffer
	child.Stdout = &childLog
	child.Stderr = &childLog
	child.Env = append(os.Environ(),
		crossReaderChildEnv+"=1",
		crossReaderDBEnv+"="+f.service.store.db.Path(),
		crossReaderBaseEnv+"="+base.ID,
		crossReaderTargetEnv+"="+target,
		crossReaderWorkEnv+"="+workspace,
	)
	if err := child.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if child.Process != nil {
			_ = child.Process.Kill()
			_ = child.Wait()
		}
	})

	waitForHistoricalReaderFile(t, filepath.Join(workspace, "pinned"))
	pinnedCount, err := os.ReadFile(filepath.Join(workspace, "pinned"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.TrimSpace(string(pinnedCount)) != strconv.Itoa(chunksPerDoc) {
		t.Fatalf("cross-process reader pinned %s chunks, want %d",
			string(pinnedCount), chunksPerDoc)
	}

	expired := now() - retiredGenerationRetentionMS - 1
	if _, err := f.service.store.db.Exec(`UPDATE chunks SET created_at = ?
		WHERE index_generation = 1`, expired); err != nil {
		t.Fatal(err)
	}
	if _, err := f.service.store.db.Exec(`UPDATE document_generations SET created_at = ?
		WHERE index_generation = 1`, expired); err != nil {
		t.Fatal(err)
	}
	gcStart := time.Now()
	ids := make([]string, 0, documentCount)
	rows, err := f.service.store.db.Query(`SELECT id FROM documents WHERE base_id = ?`, base.ID)
	if err != nil {
		t.Fatal(err)
	}
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			t.Fatal(err)
		}
		ids = append(ids, id)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	if err := rows.Close(); err != nil {
		t.Fatal(err)
	}
	if len(ids) != documentCount {
		t.Fatalf("corpus document count = %d, want %d", len(ids), documentCount)
	}
	for _, docID := range ids {
		if _, err := f.service.store.pruneRetiredGenerations(docID, 2); err != nil {
			t.Fatal(err)
		}
	}
	gcElapsed := time.Since(gcStart)
	if err := os.WriteFile(filepath.Join(workspace, "gc-done"), []byte("1"), 0o600); err != nil {
		t.Fatal(err)
	}

	waitForHistoricalReaderFile(t, filepath.Join(workspace, "reader-done"))
	if err := child.Wait(); err != nil {
		t.Fatalf("cross-process reader failed: %v: %s", err, childLog.String())
	}
	readerElapsed := time.Since(readerStart)

	var oldChunks, oldMappings int
	if err := f.service.store.db.QueryRow(`SELECT COUNT(*) FROM chunks
		WHERE index_generation = 1`).Scan(&oldChunks); err != nil {
		t.Fatal(err)
	}
	if err := f.service.store.db.QueryRow(`SELECT COUNT(*) FROM document_generations
		WHERE index_generation = 1`).Scan(&oldMappings); err != nil {
		t.Fatal(err)
	}
	if oldChunks != 0 || oldMappings != 0 {
		t.Fatalf("cross-process GC left retired data: chunks=%d mappings=%d",
			oldChunks, oldMappings)
	}
	oldGeneration, oldSourceVersion := int64(1), int64(1)
	if _, err := f.service.GetDocumentContext(ctx, target, ContextOptions{
		AnchorIndex: new(int), SourceVersion: &oldSourceVersion,
		IndexGeneration: &oldGeneration,
	}); !errors.Is(err, ErrHistoricalEvidenceExpired) {
		t.Fatalf("post-GC historical context error = %v", err)
	}
	if err := f.raw.Delete(oldRawPath); err != nil {
		t.Fatal(err)
	}
	if data, err := f.raw.Read(oldRawPath); data != nil || err != nil {
		t.Fatalf("historical raw remained = %q %v", data, err)
	}
	if data, err := f.raw.Read(currentRawPath); err != nil ||
		string(data) != "cross-process current raw" {
		t.Fatalf("current raw check = %q %v", data, err)
	}

	t.Logf("cross-process reader docs=%d chunks=%d retired=%d build=%s gc=%s reader=%s",
		documentCount, totalChunks*2, totalChunks, buildElapsed, gcElapsed, readerElapsed)
}

func runCrossReaderChild(t *testing.T) error {
	dbPath := os.Getenv(crossReaderDBEnv)
	baseID := os.Getenv(crossReaderBaseEnv)
	target := os.Getenv(crossReaderTargetEnv)
	workspace := os.Getenv(crossReaderWorkEnv)
	if dbPath == "" || baseID == "" || target == "" || workspace == "" {
		return fmt.Errorf("cross-reader child is missing database or scope environment")
	}
	db, err := storage.Open(dbPath)
	if err != nil {
		return err
	}
	defer func() { _ = db.Close() }()
	ctx := context.Background()
	readerTx, err := db.ReadDB().BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = readerTx.Rollback() }()

	// The snapshot is established by this query before the marker is visible
	// to the parent, so every subsequent read is provably the same WAL view.
	store := newStore(db)
	pinned, err := store.listChunksByGenerationRange(ctx, readerTx, target, 1, 0, 19)
	if err != nil {
		return err
	}
	var mappingSourceVersion int64
	if err := readerTx.QueryRowContext(ctx, `SELECT source_version
		FROM document_generations WHERE doc_id = ? AND index_generation = 1`,
		target).Scan(&mappingSourceVersion); err != nil {
		return err
	}
	if mappingSourceVersion != 1 {
		return fmt.Errorf("child pinned invalid mapping source version: %d", mappingSourceVersion)
	}
	if len(pinned) != 20 || pinned[0].IndexGeneration != 1 || pinned[0].SourceVersion != 2 {
		return fmt.Errorf("child pinned invalid generation view: count=%d first=%+v",
			len(pinned), pinned[0])
	}
	if err := os.WriteFile(filepath.Join(workspace, "pinned"),
		[]byte(strconv.Itoa(len(pinned))), 0o600); err != nil {
		return err
	}

	deadline := time.Now().Add(crossReaderWait)
	for {
		if _, err := os.Stat(filepath.Join(workspace, "gc-done")); err == nil {
			break
		} else if !os.IsNotExist(err) {
			return err
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("parent did not complete historical GC")
		}
		time.Sleep(5 * time.Millisecond)
	}

	afterGC, err := store.listChunksByGenerationRange(ctx, readerTx, target, 1, 0, 19)
	if err != nil {
		return err
	}
	var pinnedMappingSourceVersion int64
	if err := readerTx.QueryRowContext(ctx, `SELECT source_version
		FROM document_generations WHERE doc_id = ? AND index_generation = 1`,
		target).Scan(&pinnedMappingSourceVersion); err != nil {
		return err
	}
	if pinnedMappingSourceVersion != mappingSourceVersion {
		return fmt.Errorf("cross-process pinned mapping changed: %d -> %d",
			mappingSourceVersion, pinnedMappingSourceVersion)
	}
	if len(afterGC) != len(pinned) {
		return fmt.Errorf("cross-process pinned view changed: before=%d after=%d",
			len(pinned), len(afterGC))
	}
	for index := range pinned {
		if pinned[index].ID != afterGC[index].ID || pinned[index].Text != afterGC[index].Text {
			return fmt.Errorf("cross-process pinned chunk %d changed", index)
		}
	}
	if err := readerTx.Commit(); err != nil {
		return err
	}
	var oldChunks, oldMappings, activeChunks int
	if err := db.QueryRow(`SELECT COUNT(*) FROM chunks WHERE index_generation = 1`).Scan(&oldChunks); err != nil {
		return err
	}
	if err := db.QueryRow(`SELECT COUNT(*) FROM document_generations
		WHERE index_generation = 1`).Scan(&oldMappings); err != nil {
		return err
	}
	if err := db.QueryRow(`SELECT COUNT(*) FROM chunks WHERE index_generation = 2`).Scan(&activeChunks); err != nil {
		return err
	}
	if oldChunks != 0 || oldMappings != 0 || activeChunks == 0 {
		return fmt.Errorf("child post-commit counts old=%d/%d active=%d",
			oldChunks, oldMappings, activeChunks)
	}
	return os.WriteFile(filepath.Join(workspace, "reader-done"), []byte("1"), 0o600)
}

func waitForHistoricalReaderFile(t *testing.T, path string) {
	t.Helper()
	deadline := time.Now().Add(crossReaderWait)
	for {
		if _, err := os.Stat(path); err == nil {
			return
		} else if !os.IsNotExist(err) {
			t.Fatal(err)
		}
		if time.Now().After(deadline) {
			t.Fatalf("marker %s was not created", path)
		}
		time.Sleep(5 * time.Millisecond)
	}
}
