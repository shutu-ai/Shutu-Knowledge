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
