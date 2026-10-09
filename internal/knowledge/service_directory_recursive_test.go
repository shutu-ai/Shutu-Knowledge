package knowledge

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/shutu-ai/shutu-knowledge/internal/storage"
)

func TestDirectoryTreeImportsMultipleNestedLevels(t *testing.T) {
	service, _ := newSearchFixture(t)
	base, _ := service.CreateBase("Recursive", "", "", BaseConfig{})
	root := t.TempDir()
	write := func(rel, content string) {
		t.Helper()
		path := filepath.Join(root, rel)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	write("root.md", "root content")
	write("level-one/one.md", "one content")
	write("level-one/level-two/two.txt", "two content")
	write("level-one/level-two/level-three/three.json", `{"value":"three"}`)
	write("level-one/level-two/ignored.bin", "unsupported")

	if _, err := service.RunDirectoryImport(context.Background(), base.ID, root, nil); err != nil {
		t.Fatal(err)
	}

	docs, err := service.ListDocuments(base.ID)
	if err != nil {
		t.Fatal(err)
	}
	byPath := make(map[string]DocumentSummary, len(docs))
	for _, doc := range docs {
		if doc.SourceType == "directory" || doc.SourceType == "file" {
			byPath[doc.SourcePath] = doc
		}
	}

	rootDoc, ok := byPath[root]
	if !ok || rootDoc.SourceType != "directory" || rootDoc.ParentDirID != "" {
		t.Fatalf("root directory was not tracked: %+v", rootDoc)
	}
	levelOne, ok := byPath[filepath.Join(root, "level-one")]
	if !ok || levelOne.SourceType != "directory" || levelOne.ParentDirID != rootDoc.ID {
		t.Fatalf("first nested directory relationship: %+v", levelOne)
	}
	levelTwo, ok := byPath[filepath.Join(root, "level-one", "level-two")]
	if !ok || levelTwo.SourceType != "directory" || levelTwo.ParentDirID != levelOne.ID {
		t.Fatalf("second nested directory relationship: %+v", levelTwo)
	}
	levelThree, ok := byPath[filepath.Join(root, "level-one", "level-two", "level-three")]
	if !ok || levelThree.SourceType != "directory" || levelThree.ParentDirID != levelTwo.ID {
		t.Fatalf("third nested directory relationship: %+v", levelThree)
	}

	for rel, parentID := range map[string]string{
		"root.md":                     rootDoc.ID,
		"level-one/one.md":            levelOne.ID,
		"level-one/level-two/two.txt": levelTwo.ID,
		"level-one/level-two/level-three/three.json": levelThree.ID,
	} {
		doc, ok := byPath[filepath.Join(root, filepath.FromSlash(rel))]
		if !ok || doc.SourceType != "file" || doc.ParentDirID != parentID || doc.Status != StatusReady {
			t.Fatalf("nested file %s was not imported under its directory: %+v", rel, doc)
		}
	}
	if _, ok := byPath[filepath.Join(root, "level-one", "level-two", "ignored.bin")]; ok {
		t.Fatal("unsupported nested file should not be imported")
	}
}

func TestDirectoryImportSkipsOfficeOwnerTempFiles(t *testing.T) {
	service, _ := newSearchFixture(t)
	base, err := service.CreateBase("Owner Files", "", "", BaseConfig{})
	if err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "dictionary.md"), []byte("valid content"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "~$dictionary.xlsm"), []byte("not a zip archive"), 0o644); err != nil {
		t.Fatal(err)
	}
	rootDoc, err := service.RunDirectoryImport(context.Background(), base.ID, root, nil)
	if err != nil {
		t.Fatalf("owner lock file failed directory import: %v", err)
	}
	docs, err := service.ListDocuments(base.ID)
	if err != nil {
		t.Fatal(err)
	}
	if rootDoc.Status != StatusReady {
		t.Fatalf("root directory = %+v", rootDoc)
	}
	for _, doc := range docs {
		if doc.SourcePath == filepath.Join(root, "~$dictionary.xlsm") {
			t.Fatalf("owner lock file was tracked: %+v", doc)
		}
	}
}

func TestDirectoryRescanClearsStaleNestedDirectoryStatus(t *testing.T) {
	service, _ := newSearchFixture(t)
	base, err := service.CreateBase("Stale Nested", "", "", BaseConfig{})
	if err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	nested := filepath.Join(root, "nested")
	if err := os.MkdirAll(nested, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(nested, "good.txt"), []byte("good content"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := service.RunDirectoryImport(context.Background(), base.ID, root, nil); err != nil {
		t.Fatal(err)
	}
	docs, err := service.ListDocuments(base.ID)
	if err != nil {
		t.Fatal(err)
	}
	var rootDoc, nestedDoc DocumentSummary
	for _, doc := range docs {
		if doc.SourceType != "directory" {
			continue
		}
		switch doc.SourcePath {
		case root:
			rootDoc = doc
		case nested:
			nestedDoc = doc
		}
	}
	if rootDoc.ID == "" || nestedDoc.ID == "" {
		t.Fatalf("tracked directories missing: %+v", docs)
	}

	stale, _, err := service.GetDocument(nestedDoc.ID, false)
	if err != nil {
		t.Fatal(err)
	}
	stale.Status = StatusFailed
	stale.ErrorCode = ErrSourceMissing
	stale.ErrorMessage = "old targeted scan failure"
	if err := service.store.putDocument(stale); err != nil {
		t.Fatal(err)
	}

	if _, err := service.RunDirectoryRescan(context.Background(), rootDoc.ID, nil); err != nil {
		t.Fatal(err)
	}
	docs, err = service.ListDocuments(base.ID)
	if err != nil {
		t.Fatal(err)
	}
	for _, doc := range docs {
		if doc.ID != nestedDoc.ID && doc.ID != rootDoc.ID {
			continue
		}
		if doc.Status != StatusReady || doc.ErrorCode != "" || doc.ErrorMessage != "" {
			t.Fatalf("directory %s kept stale failure: %+v", doc.SourcePath, doc)
		}
	}
}

func TestReindexFailedChildClearsDirectoryFailure(t *testing.T) {
	f := newFixture(t)
	service := f.service
	base, err := service.CreateBase("Repair Child", "", "", BaseConfig{})
	if err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	emptyPath := filepath.Join(root, "empty.md")
	goodPath := filepath.Join(root, "good.txt")
	if err := os.WriteFile(emptyPath, []byte("   "), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(goodPath, []byte("good content"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := service.RunDirectoryImport(context.Background(), base.ID, root, nil); err == nil {
		t.Fatal("directory import with empty file should fail")
	}
	docs, err := service.ListDocuments(base.ID)
	if err != nil {
		t.Fatal(err)
	}
	var rootDoc, failedDoc DocumentSummary
	for _, doc := range docs {
		switch doc.SourcePath {
		case root:
			rootDoc = doc
		case emptyPath:
			failedDoc = doc
		}
	}
	if rootDoc.ID == "" || failedDoc.ID == "" {
		t.Fatalf("root or failed child missing: %+v", docs)
	}
	if rootDoc.Status != StatusFailed || failedDoc.Status != StatusFailed {
		t.Fatalf("pre-repair statuses: root=%q child=%q", rootDoc.Status, failedDoc.Status)
	}

	parseable := []byte("now parseable content")
	prepared, _, err := service.GetDocument(failedDoc.ID, false)
	if err != nil {
		t.Fatal(err)
	}
	rawPath, err := f.raw.WriteVersion(base.ID, prepared.ID, prepared.SourceVersion+1, ".md", parseable)
	if err != nil {
		t.Fatal(err)
	}
	prepared.RawFilePath = rawPath
	if err := service.store.putDocument(prepared); err != nil {
		t.Fatal(err)
	}
	repaired, err := service.ReindexDocument(context.Background(), failedDoc.ID)
	if err != nil {
		t.Fatal(err)
	}
	if repaired.Status != StatusReady {
		t.Fatalf("repaired child = %+v", repaired)
	}
	rootFull, _, err := service.GetDocument(rootDoc.ID, false)
	if err != nil {
		t.Fatal(err)
	}
	if rootFull.Status != StatusReady || rootFull.ErrorCode != "" || rootFull.ErrorMessage != "" {
		t.Fatalf("root kept failed status after child repair: %+v", rootFull)
	}
}

func TestDirectorySyncStateWritesPropagateCancellation(t *testing.T) {
	f := newFixture(t)
	base := f.createBase(t)
	root := filepath.Join(t.TempDir(), "cancelled-directory")
	container, err := f.service.CreateDirectory(base.ID, "cancelled-directory", "", root)
	if err != nil {
		t.Fatal(err)
	}
	nestedRoot := filepath.Join(root, "nested")
	nested, err := f.service.CreateDirectory(base.ID, "nested", container.ID, nestedRoot)
	if err != nil {
		t.Fatal(err)
	}
	index := newDirectorySyncIndex(nil)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	if err := f.service.updateDirectorySyncProgressContext(ctx, container.ID, 1); !errors.Is(err, context.Canceled) {
		t.Fatalf("progress cancellation error = %v, want context.Canceled", err)
	}
	if err := f.service.markDirectorySyncFailedContext(ctx, container.ID, errors.New("scan failed")); !errors.Is(err, context.Canceled) {
		t.Fatalf("failure-state cancellation error = %v, want context.Canceled", err)
	}
	entry := directoryEntry{
		absPath:  filepath.Join(root, "failed.md"),
		relPath:  "failed.md",
		fileName: "failed.md",
	}
	if _, err := f.service.recordChildFailureContext(ctx, base.ID, container.ID, entry, errors.New("parse failed"), index); err == nil ||
		(!errors.Is(err, context.Canceled) && !errors.Is(err, storage.ErrWriteUnknown)) {
		t.Fatalf("child-failure cancellation error = %v, want cancellation or storage.ErrWriteUnknown", err)
	}
	index = newDirectorySyncIndex([]Document{nested})
	nestedEntry := directoryEntry{
		absPath:  filepath.Join(nestedRoot, "nested-failed.md"),
		relPath:  filepath.Join("nested", "nested-failed.md"),
		fileName: "nested-failed.md",
	}
	failedID, err := f.service.recordChildFailureContext(context.Background(), base.ID, nested.ID, nestedEntry, errors.New("nested parse failed"), index)
	if err != nil {
		t.Fatalf("record nested child failure: %v", err)
	}
	failed, _, err := f.service.GetDocument(failedID, false)
	if err != nil {
		t.Fatal(err)
	}
	if failed.ParentDirectoryID != nested.ID || failed.Status != StatusFailed {
		t.Fatalf("nested failed child = %+v", failed)
	}
}
