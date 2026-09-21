package documentir

import (
	"fmt"
	"testing"
)

func TestBindDocumentIsDeterministicAndBuildsTree(t *testing.T) {
	first := FromText("Guide", "# Intro\n\nA paragraph.", "text", "test")
	second := FromText("Guide", "# Intro\n\nA paragraph.", "text", "test")
	if err := first.BindDocument("doc-1"); err != nil {
		t.Fatal(err)
	}
	if err := second.BindDocument("doc-1"); err != nil {
		t.Fatal(err)
	}
	if len(first.Nodes) != len(second.Nodes) {
		t.Fatalf("node count differs: %d/%d", len(first.Nodes), len(second.Nodes))
	}
	for i := range first.Nodes {
		if first.Nodes[i].ID != second.Nodes[i].ID {
			t.Fatalf("node %d ID differs: %q/%q", i, first.Nodes[i].ID, second.Nodes[i].ID)
		}
		if first.Nodes[i].DocumentID != "doc-1" {
			t.Fatalf("node %d has wrong document ID", i)
		}
	}
	if len(first.Nodes[0].ChildrenIDs) != 1 || first.Nodes[0].ChildrenIDs[0] != first.Nodes[1].ID {
		t.Fatalf("root children not rebuilt: %#v", first.Nodes[0].ChildrenIDs)
	}
	if first.Nodes[2].ParentID != first.Nodes[1].ID {
		t.Fatalf("paragraph parent = %q, want %q", first.Nodes[2].ParentID, first.Nodes[1].ID)
	}
}

func TestBindDocumentBuildsLargeTreeWithoutLosingChildren(t *testing.T) {
	const sections = 5000
	doc := &Document{IRVersion: Version}
	doc.Nodes = append(doc.Nodes, Node{ID: "tmp:root", Type: TypeDocument})
	for i := 0; i < sections; i++ {
		sectionID := fmt.Sprintf("tmp:section/%d", i)
		paragraphID := fmt.Sprintf("tmp:paragraph/%d", i)
		doc.Nodes = append(doc.Nodes, Node{ID: sectionID, Type: TypeSection, ParentID: "tmp:root"})
		doc.Nodes = append(doc.Nodes, Node{ID: paragraphID, Type: TypeParagraph, ParentID: sectionID})
	}
	if err := doc.BindDocument("large-doc"); err != nil {
		t.Fatal(err)
	}
	if len(doc.Nodes[0].ChildrenIDs) != sections {
		t.Fatalf("root children = %d, want %d", len(doc.Nodes[0].ChildrenIDs), sections)
	}
	if len(doc.Nodes[1].ChildrenIDs) != 1 || doc.Nodes[1].ChildrenIDs[0] != doc.Nodes[2].ID {
		t.Fatalf("first section children = %#v, want first paragraph", doc.Nodes[1].ChildrenIDs)
	}
}

func TestBindDocumentRejectsUnknownVersion(t *testing.T) {
	doc := &Document{IRVersion: "document-ir/v9"}
	if err := doc.BindDocument("doc"); err == nil {
		t.Fatal("unknown IR version accepted")
	}
}
