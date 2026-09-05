package knowledge

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/shutu-ai/shutu-knowledge/internal/config"
	"github.com/shutu-ai/shutu-knowledge/internal/jobs"
	"github.com/shutu-ai/shutu-knowledge/internal/storage"
)

type fixture struct {
	service  *Service
	raw      *storage.RawFileStore
	jobs     *jobs.Manager
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
	manager := jobs.New(db, 2)
	if err := manager.Start(context.Background()); err != nil {
		t.Fatalf("start jobs: %v", err)
	}
	service := NewService(db, raw, config.Defaults(), manager)
	f := &fixture{service: service, raw: raw, jobs: manager}
	f.shutdown = func() {
		manager.Stop()
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
	if stored, err := f.raw.Read(doc.RawFilePath); err != nil || stored != nil {
		t.Fatalf("raw read after delete should be nil,nil: %v", err)
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
	resumed, failed, err := f.service.RecoverInterrupted(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if resumed != 1 || failed != 1 {
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
}

func TestReindexBaseJob(t *testing.T) {
	f := newFixture(t)
	base := f.createBase(t)
	if _, err := f.service.AddTextDocument(context.Background(), base.ID, "one", "first document body"); err != nil {
		t.Fatal(err)
	}
	if _, err := f.service.AddTextDocument(context.Background(), base.ID, "two", "second document body"); err != nil {
		t.Fatal(err)
	}
	jobID, err := f.service.ReindexBase(context.Background(), base.ID)
	if err != nil {
		t.Fatalf("submit reindex: %v", err)
	}
	deadline := time.Now().Add(5 * time.Second)
	for {
		job, ok := f.jobs.Status(jobID)
		if !ok {
			t.Fatal("job missing")
		}
		if job.Status == jobs.StatusDone {
			break
		}
		if job.Status == jobs.StatusFailed {
			t.Fatalf("job failed: %s", job.Error)
		}
		if time.Now().After(deadline) {
			t.Fatalf("job did not finish: %+v", job)
		}
		time.Sleep(20 * time.Millisecond)
	}
	stats, err := f.service.Stats(base.ID)
	if err != nil || stats.ChunkCount < 2 {
		t.Fatalf("stats after reindex: %+v %v", stats, err)
	}
}
