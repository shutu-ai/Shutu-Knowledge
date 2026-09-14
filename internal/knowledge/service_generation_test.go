package knowledge

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/shutu-ai/shutu-knowledge/internal/jobs"
)

func TestHistoricalEvidenceFailsClosedAfterDeleteFence(t *testing.T) {
	f := newFixture(t)
	base := f.createBase(t)
	ctx := context.Background()
	doc, err := f.service.AddFileDocument(ctx, base.ID, "citation.md", []byte("# Citation\n\nhistorical raw bytes\n"), "")
	if err != nil {
		t.Fatalf("add file: %v", err)
	}
	if _, err := f.service.ReindexDocument(ctx, doc.ID); err != nil {
		t.Fatalf("reindex: %v", err)
	}
	published, err := f.service.store.getDocument(doc.ID)
	if err != nil {
		t.Fatal(err)
	}
	old := published.ActiveIndexGen - 1
	oldVersion := published.SourceVersion - 1
	if _, err := f.service.store.markDocumentTreeDeleting(doc.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := f.service.GetRawFileForCitation(doc.ID, RawCitationOptions{
		IndexGeneration: &old, SourceVersion: &oldVersion,
	}); !errors.Is(err, ErrHistoricalEvidenceExpired) {
		t.Fatalf("deleted raw citation error = %v, want ErrHistoricalEvidenceExpired", err)
	}
	anchor := 0
	if _, err := f.service.GetDocumentContext(ctx, doc.ID, ContextOptions{
		AnchorIndex:     &anchor,
		IndexGeneration: &old,
		SourceVersion:   &oldVersion,
	}); !errors.Is(err, ErrHistoricalEvidenceExpired) {
		t.Fatalf("deleted context citation error = %v, want ErrHistoricalEvidenceExpired", err)
	}
}

func TestStagedGenerationIsHiddenUntilActivated(t *testing.T) {
	f := newFixture(t)
	base := f.createBase(t)
	ctx := context.Background()
	doc, err := f.service.AddTextDocument(ctx, base.ID, "Database Guide", "# Storage\n\nthe database keeps rows on disk")
	if err != nil {
		t.Fatal(err)
	}
	oldDoc, err := f.service.store.getDocument(doc.ID)
	if err != nil {
		t.Fatal(err)
	}
	activeChunks, err := f.service.store.listChunksByDoc(doc.ID, 0, 0)
	if err != nil || len(activeChunks) == 0 {
		t.Fatalf("active chunks: %v %v", len(activeChunks), err)
	}
	staged := append([]Chunk(nil), activeChunks...)
	staged[0].Text = "unique freshly staged database sentence"
	staged[0].EmbeddingText = searchTextOf(staged[0])
	staged[0].EmbeddingHash = hashText(staged[0].EmbeddingText)
	generation, err := f.service.store.putChunksReplace(ctx, staged, "fake:a")
	if err != nil {
		t.Fatal(err)
	}
	if generation != oldDoc.ActiveIndexGen+1 {
		t.Fatalf("generation = %d, want %d", generation, oldDoc.ActiveIndexGen+1)
	}
	result, err := f.service.Search(ctx, SearchRequest{Query: "freshly staged", Mode: "lexical"})
	if err != nil {
		t.Fatal(err)
	}
	if result.Total != 0 {
		t.Fatalf("staged generation leaked into search: %+v", result)
	}
}

func TestPublishedDocumentReusePreventsDuplicateBusinessEffect(t *testing.T) {
	f := newFixture(t)
	base := f.createBase(t)
	ctx := context.Background()
	documentID := "published-before-parent-terminal"
	published, err := f.service.AddTextDocumentWithID(ctx, base.ID, documentID, "Published Guide", "the committed document body")
	if err != nil {
		t.Fatal(err)
	}
	before, err := f.service.store.getDocument(documentID)
	if err != nil {
		t.Fatal(err)
	}
	beforeChunks, err := f.service.store.listChunksByDoc(documentID, 0, 0)
	if err != nil || len(beforeChunks) == 0 {
		t.Fatalf("published chunks: %v %v", len(beforeChunks), err)
	}

	reused, err := f.service.AddTextDocumentWithID(ctx, base.ID, documentID, "Published Guide", "the committed document body")
	if err != nil {
		t.Fatal(err)
	}
	after, err := f.service.store.getDocument(documentID)
	if err != nil {
		t.Fatal(err)
	}
	afterChunks, err := f.service.store.listChunksByDoc(documentID, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	if reused.ID != published.ID || reused.SourceVersion != published.SourceVersion {
		t.Fatalf("reuse changed business result: before=%+v after=%+v", published, reused)
	}
	if after.ActiveIndexGen != before.ActiveIndexGen || after.SourceVersion != before.SourceVersion {
		t.Fatalf("reuse republished generation/source: before=%+v after=%+v", before, after)
	}
	if len(afterChunks) != len(beforeChunks) {
		t.Fatalf("chunk count changed from %d to %d", len(beforeChunks), len(afterChunks))
	}
	documents, err := f.service.ListDocuments(base.ID)
	if err != nil || len(documents) != 1 {
		t.Fatalf("document count after reuse: %v %v", documents, err)
	}
}

func TestSearchSnapshotKeepsOneGenerationAcrossLanes(t *testing.T) {
	service, _ := newSearchFixture(t)
	base, err := service.CreateBase("Snapshot", "", "", BaseConfig{})
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	doc, err := service.AddTextDocument(ctx, base.ID, "Old Guide", "# Storage\n\nthe database keeps old rows")
	if err != nil {
		t.Fatal(err)
	}
	oldDoc, err := service.store.getDocument(doc.ID)
	if err != nil {
		t.Fatal(err)
	}
	oldGeneration := oldDoc.ActiveIndexGen
	active, err := service.store.listChunksByDoc(doc.ID, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	staged := append([]Chunk(nil), active...)
	staged[0].Text = "unique freshly activated database sentence"
	staged[0].EmbeddingText = searchTextOf(staged[0])
	staged[0].EmbeddingHash = hashText(staged[0].EmbeddingText)
	staged[0].StageEmbedding = encodeEmbedding([]float64{1, 0})
	staged[0].StageEmbeddingModel = "fake:a"
	generation, err := service.store.putChunksReplace(ctx, staged, "fake:a")
	if err != nil {
		t.Fatal(err)
	}
	if generation != oldGeneration+1 {
		t.Fatalf("staged generation = %d, want %d", generation, oldGeneration+1)
	}

	gated := &gatedEmbedder{model: "a", started: make(chan struct{}), release: make(chan struct{})}
	service.SetProviders(gated, nil)
	type searchOutcome struct {
		result SearchResult
		err    error
	}
	done := make(chan searchOutcome, 1)
	go func() {
		result, searchErr := service.Search(ctx, SearchRequest{Query: "database", Mode: "hybrid", TopK: 3})
		done <- searchOutcome{result: result, err: searchErr}
	}()
	select {
	case <-gated.started:
	case <-time.After(2 * time.Second):
		t.Fatal("query embedding did not start")
	}

	current, err := service.store.getDocument(doc.ID)
	if err != nil {
		t.Fatal(err)
	}
	current.SourceVersion++
	current.Status = StatusReady
	current.ChunkCount = len(staged)
	if err := service.store.activateDocumentGeneration(ctx, doc.ID, current.MutationEpoch, generation, current); err != nil {
		t.Fatal(err)
	}
	close(gated.release)

	select {
	case outcome := <-done:
		if outcome.err != nil {
			t.Fatal(outcome.err)
		}
		result := outcome.result
		if result.Total == 0 || len(result.Generations) == 0 {
			t.Fatalf("snapshot search returned no source evidence: %+v", result)
		}
		for _, hit := range result.Hits {
			if hit.IndexGeneration != oldGeneration {
				t.Fatalf("mixed generation hit: %+v", hit)
			}
			if strings.Contains(hit.Text, "freshly activated") {
				t.Fatalf("post-snapshot generation leaked into vector/context: %+v", hit)
			}
		}
		for _, generation := range result.Generations {
			if generation.IndexGeneration != oldGeneration || generation.SourceVersion != oldDoc.SourceVersion {
				t.Fatalf("mixed generation evidence: %+v", generation)
			}
		}
	case <-time.After(5 * time.Second):
		t.Fatal("snapshot search did not finish")
	}

	activated, err := service.store.getDocument(doc.ID)
	if err != nil || activated.ActiveIndexGen != generation {
		t.Fatalf("activation state: doc=%+v err=%v", activated, err)
	}
}

func TestHistoricalContextSurvivesGenerationSwitchUntilRetention(t *testing.T) {
	f := newFixture(t)
	base := f.createBase(t)
	ctx := context.Background()
	doc, err := f.service.AddTextDocument(ctx, base.ID, "Versioned Guide", "# Storage\n\nhistorical generation sentence")
	if err != nil {
		t.Fatal(err)
	}
	old, err := f.service.store.getDocument(doc.ID)
	if err != nil {
		t.Fatal(err)
	}
	oldGeneration := old.ActiveIndexGen
	active, err := f.service.store.listChunksByDoc(doc.ID, 0, 0)
	if err != nil || len(active) == 0 {
		t.Fatalf("active chunks: %v %v", len(active), err)
	}
	staged := append([]Chunk(nil), active...)
	staged[0].Text = "current generation sentence"
	staged[0].EmbeddingText = searchTextOf(staged[0])
	staged[0].EmbeddingHash = hashText(staged[0].EmbeddingText)
	staged[0].StageEmbedding = encodeEmbedding([]float64{1, 0})
	staged[0].StageEmbeddingModel = "fake:a"
	generation, err := f.service.store.putChunksReplace(ctx, staged, "fake:a")
	if err != nil {
		t.Fatal(err)
	}
	if generation != oldGeneration+1 {
		t.Fatalf("staged generation = %d, want %d", generation, oldGeneration+1)
	}
	current := old
	current.SourceVersion++
	current.ChunkCount = len(staged)
	if err := f.service.store.activateDocumentGeneration(ctx, doc.ID, current.MutationEpoch, generation, current); err != nil {
		t.Fatal(err)
	}

	oldGenerationValue := oldGeneration
	oldSourceVersion := old.SourceVersion
	firstChunkIndex := 0
	oldWindow, err := f.service.GetDocumentContext(ctx, doc.ID, ContextOptions{
		AnchorIndex:     &firstChunkIndex,
		SourceVersion:   &oldSourceVersion,
		IndexGeneration: &oldGenerationValue,
		Before:          new(int),
		After:           new(int),
	})
	if err != nil {
		t.Fatal(err)
	}
	if oldWindow.IndexGeneration != oldGeneration || oldWindow.SourceVersion != old.SourceVersion {
		t.Fatalf("old window identity: %+v", oldWindow)
	}
	if oldWindow.Anchor.Text != "historical generation sentence" {
		t.Fatalf("old anchor text = %q", oldWindow.Anchor.Text)
	}

	wrongSourceVersion := old.SourceVersion + 1
	if _, err := f.service.GetDocumentContext(ctx, doc.ID, ContextOptions{
		AnchorIndex:     &firstChunkIndex,
		SourceVersion:   &wrongSourceVersion,
		IndexGeneration: &oldGenerationValue,
	}); !errors.Is(err, ErrHistoricalEvidenceExpired) {
		t.Fatalf("mismatched source version error = %v, want ErrHistoricalEvidenceExpired", err)
	}

	currentWindow, err := f.service.GetDocumentContext(ctx, doc.ID, ContextOptions{
		AnchorIndex: new(int),
		Before:      new(int),
		After:       new(int),
	})
	if err != nil {
		t.Fatal(err)
	}
	if currentWindow.IndexGeneration != generation || currentWindow.SourceVersion != current.SourceVersion {
		t.Fatalf("current window identity: %+v", currentWindow)
	}
	if currentWindow.Anchor.Text != "current generation sentence" {
		t.Fatalf("current anchor text = %q", currentWindow.Anchor.Text)
	}

	expired := now() - retiredGenerationRetentionMS - 1
	if _, err := f.service.store.db.Exec(`UPDATE chunks SET created_at = ?
		WHERE doc_id = ? AND index_generation = ?`, expired, doc.ID, oldGeneration); err != nil {
		t.Fatal(err)
	}
	if _, err := f.service.store.db.Exec(`UPDATE document_generations SET created_at = ?
		WHERE doc_id = ? AND index_generation = ?`, expired, doc.ID, oldGeneration); err != nil {
		t.Fatal(err)
	}
	rawPaths, err := f.service.store.pruneRetiredGenerations(doc.ID, generation)
	if err != nil {
		t.Fatal(err)
	}
	for _, rawPath := range rawPaths {
		if rawPath == "" {
			continue
		}
		if err := f.service.raw.Delete(rawPath); err != nil {
			t.Fatal(err)
		}
	}
	var oldChunks, oldMappings int
	if err := f.service.store.db.QueryRow(`SELECT COUNT(*) FROM chunks
		WHERE doc_id = ? AND index_generation = ?`, doc.ID, oldGeneration).Scan(&oldChunks); err != nil {
		t.Fatal(err)
	}
	if err := f.service.store.db.QueryRow(`SELECT COUNT(*) FROM document_generations
		WHERE doc_id = ? AND index_generation = ?`, doc.ID, oldGeneration).Scan(&oldMappings); err != nil {
		t.Fatal(err)
	}
	if oldChunks != 0 || oldMappings != 0 {
		t.Fatalf("retired generation remained: chunks=%d mappings=%d", oldChunks, oldMappings)
	}
	if _, err := f.service.GetDocumentContext(ctx, doc.ID, ContextOptions{
		AnchorIndex:     &firstChunkIndex,
		SourceVersion:   &oldSourceVersion,
		IndexGeneration: &oldGenerationValue,
	}); !errors.Is(err, ErrHistoricalEvidenceExpired) {
		t.Fatalf("expired historical error = %v, want ErrHistoricalEvidenceExpired", err)
	}
}

func TestDeleteFenceBlocksStaleIngestAndSearch(t *testing.T) {
	f := newFixture(t)
	base := f.createBase(t)
	ctx := context.Background()
	doc, err := f.service.AddTextDocument(ctx, base.ID, "Database Guide", "# Storage\n\nthe database keeps rows on disk")
	if err != nil {
		t.Fatal(err)
	}
	stale, err := f.service.store.getDocument(doc.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.service.store.markDocumentTreeDeleting(doc.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := f.service.store.getDocument(doc.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("deleted document remained readable: %v", err)
	}
	result, err := f.service.Search(ctx, SearchRequest{Query: "database", Mode: "lexical"})
	if err != nil {
		t.Fatal(err)
	}
	if result.Total != 0 {
		t.Fatalf("deleted document remained searchable: %+v", result)
	}
	stale.Status = StatusProcessing
	if err := f.service.store.startDocumentIngest(stale); !errors.Is(err, ErrConflict) {
		t.Fatalf("stale ingest was not fenced: %v", err)
	}
}

func TestDeleteDuringSearchExcludesFinalVisibility(t *testing.T) {
	service, _ := newSearchFixture(t)
	base, err := service.CreateBase("Lifecycle", "", "", BaseConfig{})
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	doc, err := service.AddTextDocument(ctx, base.ID, "Database Guide", "# Storage\n\nthe database keeps rows on disk")
	if err != nil {
		t.Fatal(err)
	}

	gated := &gatedEmbedder{model: "a", started: make(chan struct{}), release: make(chan struct{})}
	service.SetProviders(gated, nil)
	type searchOutcome struct {
		result SearchResult
		err    error
	}
	done := make(chan searchOutcome, 1)
	go func() {
		result, searchErr := service.Search(ctx, SearchRequest{
			Query: "database", Mode: "hybrid", TopK: 3, Debug: true,
		})
		done <- searchOutcome{result: result, err: searchErr}
	}()
	select {
	case <-gated.started:
	case <-time.After(2 * time.Second):
		t.Fatal("query embedding did not start")
	}

	if _, err := service.store.markDocumentTreeDeleting(doc.ID); err != nil {
		t.Fatal(err)
	}
	close(gated.release)

	select {
	case outcome := <-done:
		if outcome.err != nil {
			t.Fatal(outcome.err)
		}
		result := outcome.result
		if result.Total != 0 || len(result.Hits) != 0 || len(result.Generations) != 0 {
			t.Fatalf("logically deleted document remained visible: %+v", result)
		}
		if result.Diagnostics == nil {
			t.Fatal("debug search omitted diagnostics")
		}
		for _, stage := range [][]RetrievalDiagnosticCandidate{
			result.Diagnostics.BM25, result.Diagnostics.Vector, result.Diagnostics.RRF,
			result.Diagnostics.Final,
		} {
			if len(stage) != 0 {
				t.Fatalf("deleted document leaked into diagnostics: %+v", stage)
			}
		}
	case <-time.After(5 * time.Second):
		t.Fatal("search did not finish")
	}
}

func TestAncestorDeleteRejectsNewChildIDInjection(t *testing.T) {
	f := newFixture(t)
	base := f.createBase(t)
	root, err := f.service.CreateDirectory(base.ID, "Scanned Root", "", "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.service.CreateDirectory(base.ID, "Nested", root.ID, ""); err != nil {
		t.Fatal(err)
	}

	scan, err := f.service.store.listDocumentMetadata(base.ID)
	if err != nil || len(scan) != 2 {
		t.Fatalf("scan snapshot: %v %v", scan, err)
	}
	if _, err := f.service.store.markDocumentTreeDeleting(root.ID); err != nil {
		t.Fatal(err)
	}

	injected := f.service.newDocument(base.ID, "Injected Child", "file")
	injected.ParentDirectoryID = root.ID
	injected.Status = StatusReady
	if err := f.service.store.putDocument(injected); !errors.Is(err, ErrConflict) {
		t.Fatalf("new child injection error = %v, want ErrConflict", err)
	}
	if _, err := f.service.store.getDocument(injected.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("injected child remained readable: %v", err)
	}
	docs, err := f.service.ListDocuments(base.ID)
	if err != nil || len(docs) != 0 {
		t.Fatalf("deleted subtree remained visible: %v %v", docs, err)
	}
}

func TestDeleteFenceRejectsStaleMoveSnapshot(t *testing.T) {
	f := newFixture(t)
	base := f.createBase(t)
	root, err := f.service.CreateDirectory(base.ID, "Move Root", "", "")
	if err != nil {
		t.Fatal(err)
	}
	child, err := f.service.CreateDirectory(base.ID, "Nested", root.ID, "")
	if err != nil {
		t.Fatal(err)
	}

	snapshot, err := f.service.store.listDocumentMetadata(base.ID)
	if err != nil || len(snapshot) != 2 {
		t.Fatalf("move snapshot: %v %v", snapshot, err)
	}
	if _, err := f.service.store.markDocumentTreeDeleting(root.ID); err != nil {
		t.Fatal(err)
	}

	moved := 0
	for _, stale := range snapshot {
		stale.Title = "Moved " + stale.Title
		stale.SourcePath = filepath.Join(t.TempDir(), "moved-source")
		stale.ParentDirectoryID = ""
		if err := f.service.store.putDocument(stale); !errors.Is(err, ErrConflict) {
			t.Fatalf("stale move for %s error = %v, want ErrConflict", stale.ID, err)
		}
		moved++
	}
	if moved != 2 {
		t.Fatalf("stale move attempts = %d, want 2", moved)
	}
	for _, doc := range []Document{root, child} {
		current, err := f.service.store.getDocumentIncludingDeleting(doc.ID)
		if err != nil || current.LifecycleState != LifecycleDeleting {
			t.Fatalf("tombstone %s state: %+v %v", doc.ID, current, err)
		}
		if _, err := f.service.store.getDocument(doc.ID); !errors.Is(err, ErrNotFound) {
			t.Fatalf("deleted document %s remained readable: %v", doc.ID, err)
		}
	}
	docs, err := f.service.ListDocuments(base.ID)
	if err != nil || len(docs) != 0 {
		t.Fatalf("stale moves changed visible tree: %v %v", docs, err)
	}
}

func TestDirectoryDeleteTombstoneCleanupCanResume(t *testing.T) {
	f := newFixture(t)
	base := f.createBase(t)
	root := filepath.Join(t.TempDir(), "resume-tree")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "resume.md"), []byte("# resume cleanup"), 0o600); err != nil {
		t.Fatal(err)
	}
	document, err := f.service.RunDirectoryImport(context.Background(), base.ID, root, func(jobs.ProgressUpdate) {})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.service.store.markDocumentTreeDeleting(document.ID); err != nil {
		t.Fatal(err)
	}
	removed, err := f.service.DeleteDirectoryRecursiveWithProgress(context.Background(), document.ID, nil)
	if err != nil {
		t.Fatal(err)
	}
	if removed != 2 {
		t.Fatalf("resumed cleanup removed %d documents, want directory and file", removed)
	}
	if docs, err := f.service.ListDocuments(base.ID); err != nil || len(docs) != 0 {
		t.Fatalf("documents visible after resume: %v %v", docs, err)
	}
}
