package knowledge

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/base64"
	"errors"
	"fmt"
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/shutu-ai/shutu-knowledge/internal/config"
	"github.com/shutu-ai/shutu-knowledge/internal/storage"
)

func TestSampleCaptionPNGIsMeaningful(t *testing.T) {
	img, err := png.Decode(bytes.NewReader(sampleCaptionPNG()))
	if err != nil {
		t.Fatalf("decode sample caption PNG: %v", err)
	}
	if got := img.Bounds().Size(); got.X != 512 || got.Y != 320 {
		t.Fatalf("unexpected sample caption dimensions: %v", got)
	}
	if img.At(110, 200) == img.At(10, 10) {
		t.Fatal("sample caption PNG is blank")
	}
}

type concurrencyEmbedder struct {
	mu      sync.Mutex
	current int
	max     int
	failNth int
	calls   int
}

func (e *concurrencyEmbedder) Embed(_ context.Context, texts []string) ([][]float64, error) {
	e.mu.Lock()
	e.calls++
	e.current++
	if e.current > e.max {
		e.max = e.current
	}
	call := e.calls
	e.mu.Unlock()
	defer func() {
		e.mu.Lock()
		e.current--
		e.mu.Unlock()
	}()
	if e.failNth > 0 && call == e.failNth {
		return nil, fmt.Errorf("provider down")
	}
	out := make([][]float64, 0, len(texts))
	for range texts {
		out = append(out, []float64{1, 0})
	}
	time.Sleep(30 * time.Millisecond)
	return out, nil
}

func (e *concurrencyEmbedder) MaxConcurrent() int {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.max
}

func (e *concurrencyEmbedder) ModelKey() string { return "fake:concurrent" }

func TestAddFilesUsesBoundedParallelIngestion(t *testing.T) {
	f := newFixture(t)
	base := f.createBase(t)
	embedder := &concurrencyEmbedder{}
	f.service.global.Embedding.Provider = "openai"
	f.service.SetProviders(embedder, nil)

	items := make([]AddFilesItem, 0, 10)
	for index := 0; index < 10; index++ {
		content := fmt.Sprintf("# File %d\n\nunique parallel content %d", index, index)
		items = append(items, AddFilesItem{
			FileName:      fmt.Sprintf("file-%d.md", index),
			ContentBase64: base64.StdEncoding.EncodeToString([]byte(content)),
		})
	}
	result, err := f.service.AddFiles(context.Background(), base.ID, items, "rename", "")
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Accepted) != len(items) {
		t.Fatalf("accepted count: %+v", result)
	}
	for _, accepted := range result.Accepted {
		if accepted.Skipped || accepted.ID == "" || accepted.Title == "" {
			t.Fatalf("accepted item: %+v", accepted)
		}
	}
	if observed := embedder.MaxConcurrent(); observed <= 1 || observed > 5 {
		t.Fatalf("observed concurrency %d, want 2..5", observed)
	}
}

type fixture struct {
	service  *Service
	raw      *storage.RawFileStore
	shutdown func()
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	home := t.TempDir()
	t.Setenv("SHUTU_KNOWLEDGE_HOME", home)
	db, err := storage.Open(filepath.Join(home, "knowledge.db"))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	raw, err := storage.NewRawFileStore(filepath.Join(home, "raw"))
	if err != nil {
		t.Fatalf("raw store: %v", err)
	}
	service := NewService(db, raw, config.Defaults())
	f := &fixture{service: service, raw: raw}
	f.shutdown = func() {
		_ = db.Close()
	}
	t.Cleanup(f.shutdown)
	return f
}

func (f *fixture) createBase(t *testing.T) Base {
	t.Helper()
	base, err := f.service.CreateBase("Research", "papers", "Work", BaseConfig{})
	if err != nil {
		t.Fatalf("create base: %v", err)
	}
	return base
}

func TestBaseLifecycle(t *testing.T) {
	f := newFixture(t)
	base := f.createBase(t)

	got, err := f.service.GetBase(base.ID)
	if err != nil || got.Name != "Research" || got.Group != "Work" {
		t.Fatalf("get base: %v %+v", err, got)
	}
	newName := "Papers"
	updated, err := f.service.RenameBase(base.ID, &newName, nil, nil, nil)
	if err != nil || updated.Name != "Papers" {
		t.Fatalf("rename base: %v %+v", err, updated)
	}
	bases, err := f.service.ListBases()
	if err != nil || len(bases) != 1 || bases[0].Name != "Papers" {
		t.Fatalf("list bases: %v %+v", err, bases)
	}
	if err := f.service.DeleteBase(base.ID); err != nil {
		t.Fatalf("delete base: %v", err)
	}
	if _, err := f.service.GetBase(base.ID); err != ErrNotFound {
		t.Fatalf("expected not found after delete, got %v", err)
	}
}

func TestCommitHookRollbackProtectsDocumentFence(t *testing.T) {
	f := newFixture(t)
	base := f.createBase(t)
	doc, err := f.service.AddTextDocument(context.Background(), base.ID, "atomic", "atomic business effect")
	if err != nil {
		t.Fatalf("add text: %v", err)
	}
	before, _, err := f.service.GetDocument(doc.ID, false)
	if err != nil {
		t.Fatalf("get document before reindex: %v", err)
	}
	failure := errors.New("marker write failed")
	commitHook := func(tx *sql.Tx) error {
		_, _ = tx.Exec(`INSERT INTO operation_items(operation_id, item_key, attempt, state, commit_marker, updated_at)
			VALUES ('hook-test', 'atomic', 1, 'committed', 1, 1)`)
		return failure
	}
	if _, err := f.service.DeleteDocumentWithProgress(WithCommitHook(context.Background(), commitHook), doc.ID, nil); !errors.Is(err, failure) {
		t.Fatalf("delete with failed marker: got %v, want %v", err, failure)
	}
	stillActive, _, err := f.service.GetDocument(doc.ID, false)
	if err != nil || stillActive.LifecycleState != LifecycleActive {
		t.Fatalf("delete fence escaped rollback: err=%v doc=%+v", err, stillActive)
	}

	if _, err := f.service.ReindexDocument(WithCommitHook(context.Background(), commitHook), doc.ID); !errors.Is(err, failure) {
		t.Fatalf("reindex with failed marker: got %v, want %v", err, failure)
	}
	after, _, err := f.service.GetDocument(doc.ID, false)
	if err != nil {
		t.Fatalf("get document after reindex: %v", err)
	}
	if after.ActiveIndexGen != before.ActiveIndexGen {
		t.Fatalf("reindex generation escaped rollback: before=%d after=%d", before.ActiveIndexGen, after.ActiveIndexGen)
	}
}

func TestBaseDeleteCommitHookRollbackProtectsFence(t *testing.T) {
	f := newFixture(t)
	base := f.createBase(t)
	failure := errors.New("base marker write failed")
	hook := func(*sql.Tx) error { return failure }
	if err := f.service.DeleteBaseWithContext(WithCommitHook(context.Background(), hook), base.ID); !errors.Is(err, failure) {
		t.Fatalf("delete base with failed marker: got %v, want %v", err, failure)
	}
	stillActive, err := f.service.GetBase(base.ID)
	if err != nil || stillActive.LifecycleState != LifecycleActive {
		t.Fatalf("base delete fence escaped rollback: err=%v base=%+v", err, stillActive)
	}
}

func TestTextImportLifecycleAndChunks(t *testing.T) {
	f := newFixture(t)
	base := f.createBase(t)
	content := "# Overview\n\nKnowledge bases hold documents.\n\n## Details\n\nThey store chunks with headings."
	doc, err := f.service.AddTextDocument(context.Background(), base.ID, "Overview", content)
	if err != nil {
		t.Fatalf("add text: %v", err)
	}
	if doc.Status != StatusReady || doc.ChunkCount < 2 {
		t.Fatalf("unexpected doc state: %+v", doc)
	}
	if doc.CharCount == 0 || doc.TokenCount == 0 {
		t.Fatalf("counts missing: %+v", doc)
	}
	chunks, err := f.service.ListChunks(doc.ID, 0, 0)
	if err != nil || len(chunks) != doc.ChunkCount {
		t.Fatalf("list chunks: %v %d", err, len(chunks))
	}
	first := chunks[0]
	if first.Heading != "Overview" || !strings.Contains(first.Context, "Overview") {
		t.Fatalf("chunk metadata: %+v", first)
	}
	if first.EmbeddingHash == "" || first.EmbeddingText == "" {
		t.Fatalf("embedding text hash missing: %+v", first)
	}
	// Summary view carries status.
	summaries, err := f.service.ListDocuments(base.ID)
	if err != nil || len(summaries) != 1 || summaries[0].Status != StatusReady {
		t.Fatalf("summaries: %v %+v", err, summaries)
	}
}

func TestFileImportReindexAndDeleteCascade(t *testing.T) {
	f := newFixture(t)
	base := f.createBase(t)
	data := []byte("# Imported\n\nHello from a file.\n")
	doc, err := f.service.AddFileDocument(context.Background(), base.ID, "note.md", data, "")
	if err != nil {
		t.Fatalf("add file: %v", err)
	}
	if doc.RawFilePath == "" || doc.ContentHash == "" {
		t.Fatalf("raw metadata missing: %+v", doc)
	}
	if stored, err := f.raw.Read(doc.RawFilePath); err != nil || string(stored) != string(data) {
		t.Fatalf("raw copy: %v %q", err, stored)
	}
	// Mutate stored text, then reindex from the raw copy restores it.
	current, _, err := f.service.GetDocument(doc.ID, false)
	if err != nil {
		t.Fatal(err)
	}
	current.RawText = "mutated"
	if _, err := f.service.ReindexDocument(context.Background(), doc.ID); err != nil {
		t.Fatalf("reindex: %v", err)
	}
	restored, _, err := f.service.GetDocument(doc.ID, false)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(restored.RawText, "Hello from a file.") {
		t.Fatalf("reindex did not restore text: %q", restored.RawText)
	}
	if err := f.service.DeleteDocument(doc.ID); err != nil {
		t.Fatalf("delete: %v", err)
	}
	tree, err := f.raw.ListAll()
	if err != nil || len(tree) != 0 {
		t.Fatalf("raw tree after delete: %v %v", tree, err)
	}
}

func TestHistoricalRawSurvivesUnpublishedCurrentPathReplacement(t *testing.T) {
	f := newFixture(t)
	base := f.createBase(t)
	data := []byte("# Citation\n\nold published raw bytes\n")
	doc, err := f.service.AddFileDocument(context.Background(), base.ID, "citation.md", data, "")
	if err != nil {
		t.Fatalf("add file: %v", err)
	}
	generation, version := doc.ActiveIndexGen, doc.SourceVersion
	storedDoc, _, err := f.service.GetDocument(doc.ID, false)
	if err != nil {
		t.Fatal(err)
	}
	generation, version = storedDoc.ActiveIndexGen, storedDoc.SourceVersion
	raw, err := f.service.GetRawFileForCitation(doc.ID, RawCitationOptions{
		IndexGeneration: &generation, SourceVersion: &version,
	})
	if err != nil || string(raw.Bytes) != string(data) {
		t.Fatalf("pinned raw: %+v %v", raw, err)
	}

	// Publish a second immutable generation, then simulate the legacy race:
	// replacement bytes reach the current shared path before activation. The
	// old citation must still resolve its pinned version.
	reindexed, err := f.service.ReindexDocument(context.Background(), doc.ID)
	if err != nil {
		t.Fatalf("reindex: %v", err)
	}
	reindexed, err = f.service.store.getDocument(doc.ID)
	if err != nil {
		t.Fatal(err)
	}
	if reindexed.ActiveIndexGen != generation+1 || reindexed.SourceVersion != version+1 {
		t.Fatalf("reindex identity: %+v", reindexed)
	}
	replacement := []byte("# Citation\n\nunpublished replacement bytes\n")
	if _, err := f.raw.Write(base.ID, doc.ID, ".md", replacement); err != nil {
		t.Fatal(err)
	}
	current, err := f.service.GetRawFileForCitation(doc.ID, RawCitationOptions{
		IndexGeneration: &reindexed.ActiveIndexGen,
		SourceVersion:   &reindexed.SourceVersion,
	})
	if err != nil || string(current.Bytes) != string(data) {
		t.Fatalf("current immutable raw: %+v %v", current, err)
	}
	if _, err := f.service.GetRawFileForCitation(doc.ID, RawCitationOptions{
		IndexGeneration: &generation,
		SourceVersion:   &version,
	}); err != nil || string(raw.Bytes) != string(data) {
		t.Fatalf("retained historical raw: %+v %v", raw, err)
	}
}

func TestDirectoryParseFailureRetainsSourceForReindex(t *testing.T) {
	f := newFixture(t)
	base := f.createBase(t)
	root := t.TempDir()
	path := filepath.Join(root, "broken.md")
	if err := os.WriteFile(path, []byte("   "), 0o600); err != nil {
		t.Fatal(err)
	}

	if _, err := f.service.RunDirectoryImport(context.Background(), base.ID, root, nil); err == nil {
		t.Fatal("directory import should report the parse failure")
	}
	docs, err := f.service.ListDocuments(base.ID)
	if err != nil {
		t.Fatal(err)
	}
	var failed DocumentSummary
	for _, doc := range docs {
		if doc.Title == "broken.md" {
			failed = doc
			break
		}
	}
	if failed.ID == "" {
		t.Fatalf("failed document not tracked: %+v", docs)
	}
	full, _, err := f.service.GetDocument(failed.ID, false)
	if err != nil {
		t.Fatal(err)
	}
	if full.RawFilePath == "" {
		t.Fatalf("failed directory import lost raw source: %+v", full)
	}

	// Simulate a parser becoming able to read the same source after the
	// original failed import. Reindex must use the preserved source rather
	// than the source-less failure placeholder created by the old flow.
	if _, err := f.raw.WriteVersion(base.ID, failed.ID, 1, ".md", []byte("recovered content")); err != nil {
		t.Fatal(err)
	}
	if recovered, err := f.raw.Read(full.RawFilePath); err != nil || string(recovered) != "recovered content" {
		t.Fatalf("versioned recovery source: %q %v", recovered, err)
	}
	if _, err := f.service.ReindexDocument(context.Background(), failed.ID); err != nil {
		t.Fatalf("reindex recovered source: %v", err)
	}
	reindexed, _, err := f.service.GetDocument(failed.ID, false)
	if err != nil {
		t.Fatal(err)
	}
	if reindexed.Status != StatusReady || reindexed.ErrorCode != "" || reindexed.RawText != "recovered content" {
		t.Fatalf("reindexed document: %+v", reindexed)
	}
}

func TestAddFilesConflictStrategiesAndDedup(t *testing.T) {
	f := newFixture(t)
	base := f.createBase(t)
	ctx := context.Background()
	files := []AddFilesItem{{FileName: "a.md", ContentBase64: "IyBEb2MKCmNvbnRlbnQ="}} // "# Doc\n\ncontent"

	// Detect with an existing same-name document reports a conflict.
	if _, err := f.service.AddTextDocument(ctx, base.ID, "a.md", "existing"); err != nil {
		t.Fatal(err)
	}
	result, err := f.service.AddFiles(ctx, base.ID, files, "detect", "")
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != "conflicts" || len(result.Conflicts) != 1 {
		t.Fatalf("conflict detection: %+v", result)
	}
	// Rename resolves to a.md_1.
	result, err = f.service.AddFiles(ctx, base.ID, files, "rename", "")
	if err != nil || result.Status != "added" || len(result.Accepted) != 1 || result.Accepted[0].Title != "a_1.md" {
		t.Fatalf("rename strategy: %+v %v", result, err)
	}
	// Identical bytes are skipped as duplicates.
	result, err = f.service.AddFiles(ctx, base.ID, files, "rename", "")
	if err != nil || len(result.Accepted) != 1 || !result.Accepted[0].Skipped {
		t.Fatalf("dedup: %+v %v", result, err)
	}
	// Replace with fresh content removes the original and imports anew.
	fresh := []AddFilesItem{{FileName: "a.md", ContentBase64: "IyBEb2MKCmZyZXNoIGNvbnRlbnQ="}} // "# Doc\n\nfresh content"
	result, err = f.service.AddFiles(ctx, base.ID, fresh, "replace", "")
	if err != nil || len(result.Accepted) != 1 || result.Accepted[0].Skipped {
		t.Fatalf("replace: %+v %v", result, err)
	}
	docs, err := f.service.ListDocuments(base.ID)
	if err != nil {
		t.Fatal(err)
	}
	count := 0
	for _, d := range docs {
		if d.Title == "a.md" {
			count++
		}
	}
	if count != 1 {
		t.Fatalf("replace left %d a.md documents", count)
	}
}

func TestAddFilesReplaceReimportsSameContentAfterTitleCollision(t *testing.T) {
	f := newFixture(t)
	base := f.createBase(t)
	ctx := context.Background()
	content := "# Same\n\nidentical replacement content"
	if _, err := f.service.AddTextDocument(ctx, base.ID, "same.md", content); err != nil {
		t.Fatal(err)
	}

	result, err := f.service.AddFiles(ctx, base.ID, []AddFilesItem{{
		FileName:      "same.md",
		ContentBase64: base64.StdEncoding.EncodeToString([]byte(content)),
	}}, "replace", "")
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != "added" || len(result.Accepted) != 1 || result.Accepted[0].Skipped {
		t.Fatalf("same-content replace was skipped: %+v", result)
	}

	docs, err := f.service.ListDocuments(base.ID)
	if err != nil {
		t.Fatal(err)
	}
	var same []DocumentSummary
	for _, doc := range docs {
		if doc.Title == "same.md" {
			same = append(same, doc)
		}
	}
	if len(same) != 1 || same[0].Status != StatusReady {
		t.Fatalf("same-content replacement result: %+v", same)
	}
	full, _, err := f.service.GetDocument(same[0].ID, false)
	if err != nil || full.RawText != content {
		t.Fatalf("same-content replacement content: %+v %v", full, err)
	}
}

func TestAddFilesUsesResolvedConflictStrategy(t *testing.T) {
	f := newFixture(t)
	f.service.global.Workflow.ConflictStrategy = "replace"
	files := []AddFilesItem{{FileName: "a.md", ContentBase64: "IyBEb2MKCmZyZXNoIGNvbnRlbnQ="}}

	base, err := f.service.CreateBase("Global Strategy", "", "", BaseConfig{})
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if _, err := f.service.AddTextDocument(ctx, base.ID, "a.md", "existing"); err != nil {
		t.Fatal(err)
	}
	if _, err := f.service.AddFiles(ctx, base.ID, files, "", ""); err != nil {
		t.Fatal(err)
	}
	docs, err := f.service.ListDocuments(base.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(docs) != 1 || docs[0].Title != "a.md" {
		t.Fatalf("global conflict strategy was not applied: %+v", docs)
	}

	override, err := f.service.CreateBase("Base Strategy", "", "", BaseConfig{ConflictStrategy: "rename"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.service.AddTextDocument(ctx, override.ID, "a.md", "existing"); err != nil {
		t.Fatal(err)
	}
	if _, err := f.service.AddFiles(ctx, override.ID, files, "", ""); err != nil {
		t.Fatal(err)
	}
	docs, err = f.service.ListDocuments(override.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(docs) != 2 || docs[0].Title != "a.md" || docs[1].Title != "a_1.md" {
		t.Fatalf("base conflict override was not applied: %+v", docs)
	}
}

func TestAddFilesBatchLimit(t *testing.T) {
	f := newFixture(t)
	base := f.createBase(t)
	ctx := context.Background()
	items := make([]AddFilesItem, 0, MaxBatchFiles+1)
	for index := 0; index <= MaxBatchFiles; index++ {
		content := fmt.Sprintf("# Boundary %d\n\nunique batch limit content %d", index, index)
		items = append(items, AddFilesItem{
			FileName:      fmt.Sprintf("batch-%02d.md", index),
			ContentBase64: base64.StdEncoding.EncodeToString([]byte(content)),
		})
	}
	if _, err := f.service.AddFiles(ctx, base.ID, items[:MaxBatchFiles], "rename", ""); err != nil {
		t.Fatalf("maximum batch: %v", err)
	}
	docs, err := f.service.ListDocuments(base.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(docs) != MaxBatchFiles {
		t.Fatalf("maximum batch documents: %d", len(docs))
	}
	if _, err := f.service.AddFiles(ctx, base.ID, items, "rename", ""); err == nil ||
		!strings.Contains(err.Error(), "batch too large") {
		t.Fatalf("oversized batch error: %v", err)
	}
}

func TestGroupsAndScope(t *testing.T) {
	f := newFixture(t)
	base := f.createBase(t)
	if _, err := f.service.CreateGroup("Personal"); err != nil {
		t.Fatal(err)
	}
	groups, err := f.service.ListGroups()
	if err != nil || len(groups) != 2 {
		t.Fatalf("groups: %v %+v", err, groups)
	}
	if _, err := f.service.RenameGroup("Work", "Job"); err != nil {
		t.Fatal(err)
	}
	base2, err := f.service.GetBase(base.ID)
	if err != nil || base2.Group != "Job" {
		t.Fatalf("rename group should move bases: %v %+v", err, base2)
	}
	if err := f.service.DeleteGroup("Job"); err != nil {
		t.Fatal(err)
	}
	base3, _ := f.service.GetBase(base.ID)
	if base3.Group != "" {
		t.Fatalf("group delete should ungroup: %+v", base3)
	}

	enabled, ids, err := f.service.EnabledScope()
	if err != nil || !enabled || len(ids) != 0 {
		t.Fatalf("default scope: %v %v %v", enabled, ids, err)
	}
	all := []string{base.ID}
	if err := f.service.SetEnabledScope(nil, &all); err != nil {
		t.Fatal(err)
	}
	_, ids, _ = f.service.EnabledScope()
	if len(ids) != 1 || ids[0] != base.ID {
		t.Fatalf("pinned ids: %v", ids)
	}
}

func TestRecoverInterrupted(t *testing.T) {
	f := newFixture(t)
	base := f.createBase(t)
	// A crash left a placeholder without source: it must become failed.
	hopeless := f.service.newDocument(base.ID, "hopeless", "text")
	if err := f.service.store.putDocument(hopeless); err != nil {
		t.Fatal(err)
	}
	// A crash mid-embed leaves rawText: it must be marked for resume.
	resumable, err := f.service.AddTextDocument(context.Background(), base.ID, "resume", "text content")
	if err != nil {
		t.Fatal(err)
	}
	resumable.Status = StatusProcessing
	resumable.Incomplete = true
	if err := f.service.store.putDocument(resumable); err != nil {
		t.Fatal(err)
	}
	// A directory container has no resumable importer after its job is lost;
	// it must not remain visible as an active scan after restart.
	directory := f.service.newDocument(base.ID, "directory", "directory")
	directory.Status = StatusProcessing
	directory.Phase = PhaseParsing
	if err := f.service.store.putDocument(directory); err != nil {
		t.Fatal(err)
	}
	resumed, failed, err := f.service.RecoverInterrupted(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if resumed != 1 || failed != 2 {
		t.Fatalf("recovery counts: resumed=%d failed=%d", resumed, failed)
	}
	got, _, err := f.service.GetDocument(hopeless.ID, false)
	if err != nil || got.Status != StatusFailed || got.ErrorCode != ErrInterrupted {
		t.Fatalf("hopeless doc: %v %+v", err, got)
	}
	got2, _, _ := f.service.GetDocument(resumable.ID, false)
	if got2.Status != StatusPending {
		t.Fatalf("resumable doc status: %+v", got2)
	}
	got3, _, _ := f.service.GetDocument(directory.ID, false)
	if got3.Status != StatusFailed || got3.ErrorCode != ErrInterrupted {
		t.Fatalf("directory doc: %+v", got3)
	}
}

func TestRecoverInterruptedUsesBoundedCancellableBatches(t *testing.T) {
	f := newFixture(t)
	base := f.createBase(t)
	const documentCount = 300
	err := f.service.store.db.WriteTx(context.Background(), storage.ControlWrite, nil, func(tx *sql.Tx) error {
		for i := 0; i < documentCount; i++ {
			doc := f.service.newDocument(base.ID, fmt.Sprintf("recovery-%03d", i), "text")
			doc.Status = StatusProcessing
			doc.Incomplete = true
			if err := upsertDocument(context.Background(), tx, doc); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	_, failed, err := f.service.RecoverInterrupted(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if failed != documentCount {
		t.Fatalf("recovered failed documents = %d, want %d", failed, documentCount)
	}

	cancelled, cancel := context.WithCancel(context.Background())
	cancel()
	if _, _, err := f.service.RecoverInterrupted(cancelled); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled recovery error = %v", err)
	}
}

func TestDirectoryDeleteCleanupUsesBoundedPages(t *testing.T) {
	f := newFixture(t)
	base := f.createBase(t)
	root := f.service.newDocument(base.ID, "paged-directory", "directory")
	root.Status = StatusReady
	if err := f.service.store.putDocument(root); err != nil {
		t.Fatal(err)
	}
	const childCount = cleanupDocumentPageSize + 2
	err := f.service.store.db.WriteTx(context.Background(), storage.ControlWrite, nil, func(tx *sql.Tx) error {
		for i := 0; i < childCount; i++ {
			child := f.service.newDocument(base.ID, fmt.Sprintf("paged-child-%03d", i), "text")
			child.ParentDirectoryID = root.ID
			child.Status = StatusReady
			if err := upsertDocument(context.Background(), tx, child); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	removed, err := f.service.DeleteDirectoryRecursiveWithProgress(context.Background(), root.ID, nil)
	if err != nil {
		t.Fatal(err)
	}
	if removed != childCount+1 {
		t.Fatalf("paged directory cleanup removed %d documents, want %d", removed, childCount+1)
	}
	if docs, err := f.service.ListDocuments(base.ID); err != nil || len(docs) != 0 {
		t.Fatalf("paged directory cleanup left visible documents: %v %v", docs, err)
	}
}

func TestReindexBaseBatches(t *testing.T) {
	f := newFixture(t)
	base := f.createBase(t)
	if _, err := f.service.AddTextDocument(context.Background(), base.ID, "one", "first document body"); err != nil {
		t.Fatal(err)
	}
	if _, err := f.service.AddTextDocument(context.Background(), base.ID, "two", "second document body"); err != nil {
		t.Fatal(err)
	}
	err := f.service.ForEachReindexDocumentBatch(context.Background(), base.ID, 2, func(_ int, _ int, documents []DocumentSummary) error {
		for _, document := range documents {
			if document.SourceType == "directory" {
				continue
			}
			if _, err := f.service.ReindexDocument(context.Background(), document.ID); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		t.Fatalf("reindex batches: %v", err)
	}
	stats, err := f.service.Stats(base.ID)
	if err != nil || stats.ChunkCount < 2 {
		t.Fatalf("stats after reindex: %+v %v", stats, err)
	}
}

func TestForEachReindexDocumentBatchUsesStableBoundedWindow(t *testing.T) {
	f := newFixture(t)
	base := f.createBase(t)
	for _, title := range []string{"one", "two", "three"} {
		if _, err := f.service.AddTextDocument(context.Background(), base.ID, title, title+" body"); err != nil {
			t.Fatal(err)
		}
	}
	var seen []string
	var batchSizes []int
	err := f.service.ForEachReindexDocumentBatch(context.Background(), base.ID, 2, func(start, total int, documents []DocumentSummary) error {
		if total != 3 {
			t.Fatalf("planned total = %d, want 3", total)
		}
		batchSizes = append(batchSizes, len(documents))
		for _, document := range documents {
			seen = append(seen, document.ID)
		}
		if len(batchSizes) == 1 {
			if _, err := f.service.AddTextDocument(context.Background(), base.ID, "late", "created after the reindex window"); err != nil {
				t.Fatal(err)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(seen) != 3 || len(batchSizes) != 2 || batchSizes[0] != 2 || batchSizes[1] != 1 {
		t.Fatalf("bounded batches = %v, seen = %d", batchSizes, len(seen))
	}
	if late, err := f.service.FindDocumentByTitle(base.ID, "late"); err != nil || late.ID == "" {
		t.Fatalf("late document was not created: %+v %v", late, err)
	}
}

func TestReindexIdempotencyKeyTracksTargetAndConfigSnapshot(t *testing.T) {
	f := newFixture(t)
	base := f.createBase(t)
	first, err := f.service.AddTextDocument(context.Background(), base.ID, "one", "one body")
	if err != nil {
		t.Fatal(err)
	}
	second, err := f.service.AddTextDocument(context.Background(), base.ID, "two", "two body")
	if err != nil {
		t.Fatal(err)
	}
	key, err := f.service.ReindexIdempotencyKey(context.Background(), base.ID, []string{first.ID})
	if err != nil {
		t.Fatal(err)
	}
	repeated, err := f.service.ReindexIdempotencyKey(context.Background(), base.ID, []string{first.ID})
	if err != nil || repeated != key {
		t.Fatalf("repeat key = %q, %v; want %q", repeated, err, key)
	}
	ordered, err := f.service.ReindexIdempotencyKey(context.Background(), base.ID, []string{first.ID, second.ID})
	if err != nil {
		t.Fatal(err)
	}
	reversed, err := f.service.ReindexIdempotencyKey(context.Background(), base.ID, []string{second.ID, first.ID})
	if err != nil || reversed != ordered {
		t.Fatalf("order-sensitive batch keys = %q and %q (%v)", ordered, reversed, err)
	}
	baseKey, err := f.service.ReindexIdempotencyKey(context.Background(), base.ID, nil)
	if err != nil || baseKey == ordered {
		t.Fatalf("base key = %q, batch key = %q, err = %v", baseKey, ordered, err)
	}
	if _, err := f.service.AddTextDocument(context.Background(), base.ID, "three", "three body"); err != nil {
		t.Fatal(err)
	}
	changedBaseKey, err := f.service.ReindexIdempotencyKey(context.Background(), base.ID, nil)
	if err != nil || changedBaseKey == baseKey {
		t.Fatalf("source mutation did not change base key: before=%q after=%q err=%v", baseKey, changedBaseKey, err)
	}
	cfg := f.service.GlobalConfig()
	cfg.Embedding.Model = "changed-for-key-test"
	f.service.SetGlobalConfig(cfg)
	changed, err := f.service.ReindexIdempotencyKey(context.Background(), base.ID, []string{first.ID})
	if err != nil || changed == key {
		t.Fatalf("config change did not change key: before=%q after=%q err=%v", key, changed, err)
	}
	if len(key) != len("reindex.auto.")+64 || len(changed) != len("reindex.auto.")+64 {
		t.Fatalf("unexpected key shape: %q", key)
	}
}

func TestListDocumentsPageIsBounded(t *testing.T) {
	f := newFixture(t)
	base := f.createBase(t)
	for _, title := range []string{"one", "two", "three"} {
		if _, err := f.service.AddTextDocument(context.Background(), base.ID, title, title+" body"); err != nil {
			t.Fatal(err)
		}
	}
	first, err := f.service.ListDocumentsPageContext(context.Background(), base.ID, 2, 0)
	if err != nil {
		t.Fatal(err)
	}
	second, err := f.service.ListDocumentsPageContext(context.Background(), base.ID, 2, 2)
	if err != nil {
		t.Fatal(err)
	}
	if first.Total != 3 || len(first.Documents) != 2 || !first.HasMore || second.Total != 3 || len(second.Documents) != 1 || second.HasMore {
		t.Fatalf("pages = %+v / %+v", first, second)
	}
}

func TestIndexingStatusIsBoundedAndCancellable(t *testing.T) {
	f := newFixture(t)
	base := f.createBase(t)
	for index := 0; index < maxIndexingStatusItems+1; index++ {
		doc := f.service.newDocument(base.ID, fmt.Sprintf("active-%03d", index), "text")
		doc.Status = StatusPending
		if index%2 == 0 {
			doc.Status = StatusProcessing
			doc.Phase = PhaseParsing
		}
		if err := f.service.store.putDocument(doc); err != nil {
			t.Fatalf("seed indexing status %d: %v", index, err)
		}
	}

	status, err := f.service.IndexingStatusContext(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(status) != maxIndexingStatusItems {
		t.Fatalf("indexing status length = %d, want %d", len(status), maxIndexingStatusItems)
	}
	for _, item := range status {
		if item.DocID == "" || item.BaseID != base.ID || (item.Phase != "" && item.Phase != PhaseParsing) {
			t.Fatalf("invalid indexing status item: %+v", item)
		}
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := f.service.IndexingStatusContext(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled indexing status error = %v, want context.Canceled", err)
	}
}

func TestReindexAndRawCitationHonorCanceledContext(t *testing.T) {
	f := newFixture(t)
	base := f.createBase(t)
	doc, err := f.service.AddFileDocument(context.Background(), base.ID, "cancel.md", []byte("cancel-aware source"), "")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := f.service.ReindexDocument(ctx, doc.ID); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled reindex error = %v, want context.Canceled", err)
	}
	if _, err := f.service.ReindexIdempotencyKey(ctx, base.ID, []string{doc.ID}); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled reindex idempotency key error = %v, want context.Canceled", err)
	}
	if _, err := f.service.GetRawFileForCitationContext(ctx, doc.ID, RawCitationOptions{}); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled raw citation error = %v, want context.Canceled", err)
	}
}

func TestPutChunkVectorsHonorsCanceledContext(t *testing.T) {
	f := newFixture(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	err := f.service.store.PutChunkVectorsContext(ctx, "missing", 1, "fake:model", map[string][]float64{
		"hash": {1, 0},
	})
	if !errors.Is(err, context.Canceled) && !errors.Is(err, storage.ErrWriteUnknown) {
		t.Fatalf("cancelled vector write error = %v, want context.Canceled or storage.ErrWriteUnknown", err)
	}
}

func TestEmbeddedChunkCountHonorsCanceledContext(t *testing.T) {
	f := newFixture(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	if _, err := f.service.store.countEmbeddedChunksByDocGenerationContext(ctx, "missing", 1); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled embedded chunk count error = %v, want context.Canceled", err)
	}
}

func TestContentHashLookupHonorsCanceledContext(t *testing.T) {
	f := newFixture(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	if _, err := f.service.hasContentHashContext(ctx, "missing", "hash"); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled content hash lookup error = %v, want context.Canceled", err)
	}
}

func TestRunDirectoryImportHonorsCanceledContext(t *testing.T) {
	f := newFixture(t)
	base := f.createBase(t)
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "cancel.md"), []byte("cancel-aware directory source"), 0o600); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := f.service.RunDirectoryImport(ctx, base.ID, root, nil); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled directory import error = %v, want context.Canceled", err)
	}
	if _, err := f.service.FindDirectoryByPath(base.ID, root); !errors.Is(err, ErrNotFound) {
		t.Fatalf("cancelled directory import created a container: %v", err)
	}
	container, err := f.service.CreateDirectory(base.ID, "tracked", "", root)
	if err != nil {
		t.Fatal(err)
	}
	rescanCtx, cancelRescan := context.WithCancel(context.Background())
	cancelRescan()
	if _, err := f.service.RunDirectoryRescan(rescanCtx, container.ID, nil); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled directory rescan error = %v, want context.Canceled", err)
	}
}

func TestDirectoryChildTitleLookupHonorsCanceledContext(t *testing.T) {
	f := newFixture(t)
	base := f.createBase(t)
	root := t.TempDir()
	path := filepath.Join(root, "existing.md")
	if err := os.WriteFile(path, []byte("directory child"), 0o600); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err := f.service.importChildFile(ctx, base.ID, "directory-id", directoryEntry{
		absPath: path, relPath: "existing.md", fileName: "existing.md", size: int64(len("directory child")),
	}, newDirectorySyncIndex(nil))
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("directory child title lookup with canceled context = %v, want context.Canceled", err)
	}
}

func TestImportConflictLookupsHonorCanceledContext(t *testing.T) {
	f := newFixture(t)
	base := f.createBase(t)
	if _, err := f.service.AddTextDocument(context.Background(), base.ID, "existing.md", "existing"); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := f.service.FindDocumentByTitleContext(ctx, base.ID, "existing.md"); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled title lookup error = %v, want context.Canceled", err)
	}
	if _, err := f.service.RenameAvailableContext(ctx, base.ID, "existing.md"); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled rename lookup error = %v, want context.Canceled", err)
	}
	if _, err := f.service.DetectConflictsContext(ctx, base.ID, []string{"existing.md"}); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled conflict lookup error = %v, want context.Canceled", err)
	}
}

func TestCustomRerankerDeleteHonorsCanceledContext(t *testing.T) {
	f := newFixture(t)
	if _, err := f.service.RegisterCustomReranker("owner/model"); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := f.service.DeleteCustomRerankerContext(ctx, "owner/model"); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled custom reranker delete error = %v, want context.Canceled", err)
	}
	if err := f.service.SaveRerankSelfTestContext(ctx, RerankSelfTest{ID: "owner/model"}); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled reranker self-test save error = %v, want context.Canceled", err)
	}
}

func TestBaseAndDocumentReadPathsHonorCanceledContext(t *testing.T) {
	f := newFixture(t)
	base := f.createBase(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	if _, err := f.service.GetBaseWithContext(ctx, base.ID); !errors.Is(err, context.Canceled) {
		t.Errorf("cancelled base lookup error = %v, want context.Canceled", err)
	}
	if _, err := f.service.ListBasesContext(ctx); !errors.Is(err, context.Canceled) {
		t.Errorf("cancelled base list error = %v, want context.Canceled", err)
	}
	if _, err := f.service.ListGroupsContext(ctx); !errors.Is(err, context.Canceled) {
		t.Errorf("cancelled group list error = %v, want context.Canceled", err)
	}
	if _, err := f.service.EnabledScopeStateContext(ctx); !errors.Is(err, context.Canceled) {
		t.Errorf("cancelled scope state error = %v, want context.Canceled", err)
	}
	if err := f.service.SetEnabledScopeWithContext(ctx, nil, nil); !errors.Is(err, context.Canceled) {
		t.Errorf("cancelled scope update error = %v, want context.Canceled", err)
	}
	if _, err := f.service.StatsContext(ctx, base.ID); !errors.Is(err, context.Canceled) {
		t.Errorf("cancelled base stats error = %v, want context.Canceled", err)
	}
	if _, err := f.service.ListDocumentsContext(ctx, base.ID); !errors.Is(err, context.Canceled) {
		t.Errorf("cancelled document list error = %v, want context.Canceled", err)
	}
	if _, err := f.service.ListDocumentChildrenContext(ctx, base.ID, "", 50, 0); !errors.Is(err, context.Canceled) {
		t.Errorf("cancelled document children error = %v, want context.Canceled", err)
	}
	if _, _, err := f.service.GetDocumentWithContext(ctx, "missing", false); !errors.Is(err, context.Canceled) {
		t.Errorf("cancelled document lookup error = %v, want context.Canceled", err)
	}
	if _, err := f.service.ListChunksContext(ctx, "missing", 20, 0); !errors.Is(err, context.Canceled) {
		t.Errorf("cancelled chunk list error = %v, want context.Canceled", err)
	}
	if _, err := f.service.ListSearchHistoryContext(ctx, 20); !errors.Is(err, context.Canceled) {
		t.Errorf("cancelled search history list error = %v, want context.Canceled", err)
	}
	if err := f.service.DeleteSearchHistoryContext(ctx, ""); !errors.Is(err, context.Canceled) {
		t.Errorf("cancelled search history delete error = %v, want context.Canceled", err)
	}
	if _, err := f.service.SaveSearchHistoryContext(ctx, SearchRequest{Query: "cancelled"}, SearchResult{}); !errors.Is(err, context.Canceled) {
		t.Errorf("cancelled search history save error = %v, want context.Canceled", err)
	}
	if _, err := f.service.Search(ctx, SearchRequest{Query: "cancelled", Filter: &SearchFilter{TitleIncludes: "cancelled"}}); !errors.Is(err, context.Canceled) {
		t.Errorf("cancelled filtered search error = %v, want context.Canceled", err)
	}
	anchorIndex := 0
	if _, err := f.service.GetDocumentContext(ctx, "missing", ContextOptions{AnchorIndex: &anchorIndex}); !errors.Is(err, context.Canceled) {
		t.Errorf("cancelled document context error = %v, want context.Canceled", err)
	}
	if _, err := f.service.AddTextDocumentWithID(ctx, base.ID, "cancelled-text", "cancelled", "body"); !errors.Is(err, context.Canceled) {
		t.Errorf("cancelled text import error = %v, want context.Canceled", err)
	}
	if _, err := f.service.AddFileDocumentWithID(ctx, base.ID, "cancelled-file", "cancelled.md", []byte("body"), ""); !errors.Is(err, context.Canceled) {
		t.Errorf("cancelled file import error = %v, want context.Canceled", err)
	}
	if _, err := f.service.AddFiles(ctx, base.ID, []AddFilesItem{{FileName: "cancelled.md", ContentBase64: base64.StdEncoding.EncodeToString([]byte("body"))}}, "", ""); !errors.Is(err, context.Canceled) {
		t.Errorf("cancelled batch import error = %v, want context.Canceled", err)
	}
	if _, err := f.service.AddUrlDocumentWithID(ctx, base.ID, "cancelled-url", "https://example.invalid", "", nil); !errors.Is(err, context.Canceled) {
		t.Errorf("cancelled URL import error = %v, want context.Canceled", err)
	}
	if _, err := f.service.RestoreBaseWithID(ctx, base.ID, "cancelled-restore", "", nil, nil); !errors.Is(err, context.Canceled) {
		t.Errorf("cancelled restore error = %v, want context.Canceled", err)
	}
	if _, err := f.service.CreateBaseWithContext(ctx, "cancelled-base", "", "", BaseConfig{}); !errors.Is(err, context.Canceled) && !errors.Is(err, storage.ErrWriteUnknown) {
		t.Errorf("cancelled base create error = %v, want context.Canceled or storage.ErrWriteUnknown", err)
	}
	name := "renamed"
	if _, err := f.service.RenameBaseWithContext(ctx, base.ID, &name, nil, nil, nil); !errors.Is(err, context.Canceled) {
		t.Errorf("cancelled base rename error = %v, want context.Canceled", err)
	}
	if _, err := f.service.CreateDirectoryWithContext(ctx, base.ID, "cancelled-directory", "", t.TempDir()); !errors.Is(err, context.Canceled) {
		t.Errorf("cancelled directory create error = %v, want context.Canceled", err)
	}
	if _, err := f.service.RepointSourceWithContext(ctx, "missing", t.TempDir()); !errors.Is(err, context.Canceled) {
		t.Errorf("cancelled source repoint error = %v, want context.Canceled", err)
	}
	if _, err := f.service.RenameDocumentWithContext(ctx, "missing", "renamed"); !errors.Is(err, context.Canceled) {
		t.Errorf("cancelled document rename error = %v, want context.Canceled", err)
	}
}

func TestRestoreSourcePagesUseMetadataOnly(t *testing.T) {
	f := newFixture(t)
	base := f.createBase(t)
	doc := f.service.newDocument(base.ID, "large-source", "text")
	doc.Status = StatusReady
	doc.RawText = strings.Repeat("source payload ", 2000)
	doc.CharCount = len(doc.RawText)
	if err := f.service.store.putDocument(doc); err != nil {
		t.Fatalf("seed large source: %v", err)
	}
	page, err := f.service.store.listDocumentsAfterContext(context.Background(), base.ID, 0, "", 1)
	if err != nil {
		t.Fatal(err)
	}
	if len(page) != 1 || page[0].RawText != "" {
		t.Fatalf("restore source page materialized raw text: len=%d rawBytes=%d", len(page), len(page[0].RawText))
	}
	full, err := f.service.store.getDocumentContext(context.Background(), page[0].ID)
	if err != nil || len(full.RawText) == 0 {
		t.Fatalf("full source lookup: err=%v rawBytes=%d", err, len(full.RawText))
	}
}

func TestReconcileStorageRemovesOrphansAndFixesCounts(t *testing.T) {
	f := newFixture(t)
	base := f.createBase(t)
	doc, err := f.service.AddTextDocument(context.Background(), base.ID, "Reconcile", "alpha\n\nbeta")
	if err != nil {
		t.Fatal(err)
	}
	orphan := filepath.Join(f.raw.Root(), base.ID, "orphan.bin")
	if err := os.MkdirAll(filepath.Dir(orphan), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(orphan, []byte("orphan"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := f.service.store.db.Exec(`UPDATE documents SET chunk_count = 99 WHERE id = ?`, doc.ID); err != nil {
		t.Fatal(err)
	}

	removed, fixed, err := f.service.ReconcileStorage()
	if err != nil || removed != 1 || fixed != 1 {
		t.Fatalf("reconcile: %d %d %v", removed, fixed, err)
	}
	if _, err := os.Stat(orphan); !os.IsNotExist(err) {
		t.Fatalf("orphan still exists: %v", err)
	}
	got, _, err := f.service.GetDocument(doc.ID, false)
	if err != nil || got.ChunkCount < 2 {
		t.Fatalf("fixed document: %+v %v", got, err)
	}
}
