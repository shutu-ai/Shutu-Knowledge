package knowledge

import (
	"context"
	"fmt"
	"strings"
	"testing"
)

func TestSQLiteDiskFullAtGenerationCommitPreservesActiveAndRecovers(t *testing.T) {
	f := newFixture(t)
	base, err := f.service.CreateBase("Disk full", "fault injection", "Work", BaseConfig{
		SmartChunk:      boolPtr(false),
		ChunkSize:       800,
		ChunkOverlap:    0,
		SemanticChunk:   boolPtr(false),
		ChunkTokenLimit: 0,
	})
	if err != nil {
		t.Fatalf("create fault base: %v", err)
	}
	ctx := context.Background()

	baselineMarker := "baseline-unique-zebra-marker"
	baselineContent := fmt.Sprintf(
		"# Baseline document\n\nThe %s is retrievable after a storage fault. %s\n",
		baselineMarker, strings.Repeat("baseline corpus payload for durable pages.\n\n", 500),
	)
	baseline, err := f.service.AddTextDocument(ctx, base.ID, "Baseline", baselineContent)
	if err != nil {
		t.Fatalf("import baseline: %v", err)
	}
	baseline, err = f.service.store.getDocument(baseline.ID)
	if err != nil {
		t.Fatalf("load persisted baseline: %v", err)
	}
	if baseline.Status != StatusReady || baseline.ActiveIndexGen != 1 {
		t.Fatalf("baseline = %+v", baseline)
	}

	faultMarker := "quartz-satellite-topaz"
	faultContent := fmt.Sprintf(
		"The %s must never leak from a failed generation. %s\n",
		faultMarker, strings.Repeat("fault corpus payload forces real page allocation.\n\n", 12000),
	)
	faultDoc := f.service.newDocument(base.ID, "Fault document", "text")
	faultDoc.RawText = faultContent
	faultDoc.CharCount = len([]rune(faultContent))
	if err := f.service.store.putDocument(faultDoc); err != nil {
		t.Fatalf("preallocate fault document: %v", err)
	}

	db := f.service.store.db
	var pageCountValue int64
	if err := db.QueryRow(`PRAGMA page_count`).Scan(&pageCountValue); err != nil {
		t.Fatalf("read page count: %v", err)
	}
	var pageSizeValue int64
	if err := db.QueryRow(`PRAGMA page_size`).Scan(&pageSizeValue); err != nil {
		t.Fatalf("read page size: %v", err)
	}
	var ftsRowsBefore int64
	if err := db.QueryRow(`SELECT COUNT(*) FROM chunk_fts_data`).Scan(&ftsRowsBefore); err != nil {
		t.Fatalf("read baseline FTS storage: %v", err)
	}
	// Leave a small header headroom for the processing-status UPDATE while the
	// oversized staged generation still forces real page allocation and hits
	// SQLITE_FULL inside putChunksReplace.
	diskCeiling := pageCountValue + 8
	if _, err := db.Exec(fmt.Sprintf(`PRAGMA max_page_count = %d`, diskCeiling)); err != nil {
		t.Fatalf("set SQLite disk-space ceiling: %v", err)
	}
	t.Cleanup(func() {
		if _, err := db.Exec(`PRAGMA max_page_count = 1073741823`); err != nil {
			t.Errorf("clear max_page_count: %v", err)
		}
	})

	faultID := faultDoc.ID
	t.Log("importing at SQLite page ceiling")
	_, faultErr := f.service.AddTextDocumentWithID(ctx, base.ID, faultID, "Fault document", faultContent)
	t.Logf("page-ceiling import returned: %v", faultErr)
	if faultErr == nil {
		t.Fatal("import succeeded at the SQLite page ceiling")
	}
	if !strings.Contains(faultErr.Error(), "database or disk is full") {
		t.Fatalf("unexpected fault error: %v", faultErr)
	}

	var activeDocs, activeChunks, generations, embeddedVectors, ftsRows int64
	if err := db.QueryRow(`SELECT COUNT(*) FROM documents WHERE lifecycle_state = 'active'`).Scan(&activeDocs); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRow(`SELECT COUNT(*) FROM chunks`).Scan(&activeChunks); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRow(`SELECT COUNT(*) FROM document_generations`).Scan(&generations); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRow(`SELECT COUNT(*) FROM chunks WHERE embedding IS NOT NULL`).Scan(&embeddedVectors); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRow(`SELECT COUNT(*) FROM chunk_fts_data`).Scan(&ftsRows); err != nil {
		t.Fatal(err)
	}
	if activeDocs != 2 || activeChunks != int64(baseline.ChunkCount) || generations != 1 ||
		embeddedVectors != 0 || ftsRows != ftsRowsBefore {
		t.Fatalf("post-fault state leaked a generation: docs=%d chunks=%d generations=%d vectors=%d fts=%d/%d baselineChunks=%d",
			activeDocs, activeChunks, generations, embeddedVectors, ftsRows, ftsRowsBefore, baseline.ChunkCount)
	}
	if got, err := f.service.store.getDocument(baseline.ID); err != nil ||
		got.ActiveIndexGen != 1 || got.SourceVersion != baseline.SourceVersion ||
		got.ChunkCount != baseline.ChunkCount || got.Status != StatusReady {
		t.Fatalf("baseline changed after fault: doc=%+v err=%v", got, err)
	}
	if _, err := f.service.store.getDocument(faultID); err != nil {
		t.Fatalf("preallocated recovery target disappeared: %v", err)
	}
	result, err := f.service.Search(ctx, SearchRequest{Query: baselineMarker, Mode: "lexical", TopK: 4})
	if err != nil || result.Total < 1 {
		t.Fatalf("baseline lexical search after disk full: total=%d err=%v", result.Total, err)
	}
	if result.Hits[0].DocID != baseline.ID || result.Hits[0].IndexGeneration != 1 {
		t.Fatalf("baseline search identity changed: %+v", result.Hits[0])
	}
	stale, err := f.service.Search(ctx, SearchRequest{Query: faultMarker, Mode: "lexical", TopK: 4})
	if err != nil || stale.Total != 0 {
		t.Fatalf("failed generation leaked into FTS: total=%d result=%+v err=%v", stale.Total, stale, err)
	}

	if _, err := db.Exec(`PRAGMA max_page_count = 1073741823`); err != nil {
		t.Fatalf("restore disk capacity: %v", err)
	}
	recovered, err := f.service.AddTextDocumentWithID(ctx, base.ID, faultID, "Fault document", faultContent)
	if err != nil {
		t.Fatalf("retry after capacity restored: %v", err)
	}
	recovered, err = f.service.store.getDocument(recovered.ID)
	if err != nil {
		t.Fatalf("load recovered document: %v", err)
	}
	if recovered.Status != StatusReady || recovered.ActiveIndexGen != 1 || recovered.ChunkCount == 0 {
		t.Fatalf("recovered document: status=%s generation=%d chunks=%d",
			recovered.Status, recovered.ActiveIndexGen, recovered.ChunkCount)
	}
	result, err = f.service.Search(ctx, SearchRequest{Query: faultMarker, Mode: "lexical", TopK: 4})
	if err != nil || result.Total < 1 || result.Hits[0].DocID != faultID || result.Hits[0].IndexGeneration != 1 {
		t.Fatalf("recovered search: result=%+v err=%v", result, err)
	}
	var integrity string
	if err := db.QueryRow(`PRAGMA integrity_check`).Scan(&integrity); err != nil || integrity != "ok" {
		t.Fatalf("SQLite integrity after disk-full recovery = %q err=%v", integrity, err)
	}
	t.Logf("disk-full baselinePages=%d pageSize=%d baselineChunks=%d recoveredChunks=%d fault=%v",
		diskCeiling, pageSizeValue, baseline.ChunkCount, recovered.ChunkCount, faultErr)
}
