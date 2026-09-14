package knowledge

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestSyntheticHistoricalCorpusLongReaderGCDrill(t *testing.T) {
	const (
		documentCount = 600
		chunksPerDoc  = 20
		chunkCount    = documentCount * chunksPerDoc
		activeChunks  = chunkCount
		retiredChunks = chunkCount
	)
	f := newFixture(t)
	base := f.createBase(t)
	ctx := context.Background()
	fill := strings.Repeat(" historical corpus retrieval alpha beta gamma delta epsilon", 8)

	buildStart := time.Now()
	tx, err := f.service.store.db.Begin()
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
		docID := fmt.Sprintf("synthetic-history-%05d", documentIndex)
		title := fmt.Sprintf("Synthetic history %05d corpusunicorn%05d", documentIndex, documentIndex)
		rawText := title + fill
		if _, err := docStmt.Exec(docID, base.ID, title, rawText, len(rawText), chunksPerDoc); err != nil {
			t.Fatal(err)
		}
		for generation := int64(1); generation <= 2; generation++ {
			sourceVersion := generation
			if _, err := generationStmt.Exec(docID, generation, sourceVersion, chunksPerDoc); err != nil {
				t.Fatal(err)
			}
			for chunkIndex := 0; chunkIndex < chunksPerDoc; chunkIndex++ {
				text := fmt.Sprintf("%s generation%d chunk%02d %s",
					title, generation, chunkIndex, fill)
				chunkID := fmt.Sprintf("synthetic-history-%05d-g%d-%02d",
					documentIndex, generation, chunkIndex)
				if _, err := chunkStmt.Exec(chunkID, docID, base.ID, chunkIndex,
					text, title, generation); err != nil {
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

	target := fmt.Sprintf("synthetic-history-%05d", documentCount/2)
	oldRawPath, err := f.raw.WriteVersion(base.ID, target, 1, ".txt",
		[]byte("synthetic historical raw bytes"))
	if err != nil {
		t.Fatal(err)
	}
	currentRawPath, err := f.raw.WriteVersion(base.ID, target, 2, ".txt",
		[]byte("synthetic current raw bytes"))
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

	searchStart := time.Now()
	search, err := f.service.Search(ctx, SearchRequest{
		Query: fmt.Sprintf("corpusunicorn%05d", documentCount/2),
		Mode:  "lexical", TopK: chunksPerDoc,
	})
	if err != nil {
		t.Fatal(err)
	}
	if search.Total != chunksPerDoc {
		t.Fatalf("target search total = %d, want %d", search.Total, chunksPerDoc)
	}
	for _, hit := range search.Hits {
		if hit.DocID != target || hit.IndexGeneration != 2 || hit.SourceVersion != 2 {
			t.Fatalf("search returned wrong active identity: %+v", hit)
		}
	}
	searchElapsed := time.Since(searchStart)

	readerTx, err := f.service.store.db.ReadDB().BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = readerTx.Rollback() }()
	readerStart := time.Now()
	beforeGC, err := f.service.store.listChunksByGenerationRange(
		ctx, readerTx, target, 1, 0, int64(chunksPerDoc-1))
	if err != nil {
		t.Fatal(err)
	}
	if len(beforeGC) != chunksPerDoc || beforeGC[0].IndexGeneration != 1 {
		t.Fatalf("pinned reader before GC = %d chunks, identity %+v",
			len(beforeGC), beforeGC[0])
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
	var collectedRaw []string
	for _, docID := range ids {
		rawPaths, err := f.service.store.pruneRetiredGenerations(docID, 2)
		if err != nil {
			t.Fatal(err)
		}
		collectedRaw = append(collectedRaw, rawPaths...)
	}
	gcElapsed := time.Since(gcStart)

	// The WAL read snapshot is pinned at the first query. GC may reclaim the
	// rows on the writer, but this reader must continue to see one complete
	// generation instead of a partial or generation-mixed view.
	afterGC, err := f.service.store.listChunksByGenerationRange(
		ctx, readerTx, target, 1, 0, int64(chunksPerDoc-1))
	if err != nil {
		t.Fatal(err)
	}
	readerElapsed := time.Since(readerStart)
	if len(afterGC) != chunksPerDoc {
		t.Fatalf("pinned reader after GC = %d chunks, want %d", len(afterGC), chunksPerDoc)
	}
	for index := range beforeGC {
		if beforeGC[index].ID != afterGC[index].ID || beforeGC[index].Text != afterGC[index].Text {
			t.Fatalf("pinned reader changed at chunk %d", index)
		}
	}
	if err := readerTx.Commit(); err != nil {
		t.Fatal(err)
	}

	var remainingOldChunks, remainingOldMappings int
	if err := f.service.store.db.QueryRow(`SELECT COUNT(*) FROM chunks
		WHERE index_generation = 1`).Scan(&remainingOldChunks); err != nil {
		t.Fatal(err)
	}
	if err := f.service.store.db.QueryRow(`SELECT COUNT(*) FROM document_generations
		WHERE index_generation = 1`).Scan(&remainingOldMappings); err != nil {
		t.Fatal(err)
	}
	if remainingOldChunks != 0 || remainingOldMappings != 0 {
		t.Fatalf("GC left retired data: chunks=%d mappings=%d",
			remainingOldChunks, remainingOldMappings)
	}
	var currentActiveChunks int
	if err := f.service.store.db.QueryRow(`SELECT COUNT(*) FROM chunks
		WHERE index_generation = 2`).Scan(&currentActiveChunks); err != nil {
		t.Fatal(err)
	}
	if currentActiveChunks != activeChunks {
		t.Fatalf("active chunks = %d, want %d", currentActiveChunks, activeChunks)
	}

	currentWindow, err := f.service.GetDocumentContext(ctx, target, ContextOptions{
		AnchorIndex: new(int), Before: new(int), After: new(int),
	})
	if err != nil || currentWindow.IndexGeneration != 2 || currentWindow.SourceVersion != 2 {
		t.Fatalf("current context after GC = %+v %v", currentWindow, err)
	}
	oldGeneration, oldSourceVersion := int64(1), int64(1)
	if _, err := f.service.GetDocumentContext(ctx, target, ContextOptions{
		AnchorIndex: new(int), SourceVersion: &oldSourceVersion,
		IndexGeneration: &oldGeneration,
	}); !errors.Is(err, ErrHistoricalEvidenceExpired) {
		t.Fatalf("expired historical context error = %v", err)
	}
	foundOldRaw := false
	for _, path := range collectedRaw {
		if path == oldRawPath {
			foundOldRaw = true
			break
		}
	}
	if !foundOldRaw {
		t.Fatalf("GC did not report historical raw path %s", oldRawPath)
	}
	if err := f.raw.Delete(oldRawPath); err != nil {
		t.Fatal(err)
	}
	if data, err := f.raw.Read(oldRawPath); data != nil || err != nil {
		t.Fatalf("historical raw remained after purge = %q %v", data, err)
	}
	if data, err := f.raw.Read(currentRawPath); err != nil || string(data) != "synthetic current raw bytes" {
		t.Fatalf("current raw survived check = %q %v", data, err)
	}

	databaseInfo, err := os.Stat(filepath.Join(os.Getenv("SHUTU_KNOWLEDGE_HOME"), "knowledge.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("synthetic historical corpus docs=%d chunks=%d active=%d retired=%d build=%s search=%s gc=%s pinnedReader=%s dbBytes=%d",
		documentCount, chunkCount, activeChunks, retiredChunks, buildElapsed,
		searchElapsed, gcElapsed, readerElapsed, databaseInfo.Size())
}
