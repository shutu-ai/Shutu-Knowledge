package knowledge

import (
	"context"
	"os"
	"path/filepath"
	"testing"
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

	jobID, err := service.ImportDirectoryTree(context.Background(), base.ID, root)
	if err != nil {
		t.Fatal(err)
	}
	waitJob(t, service, jobID)

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
