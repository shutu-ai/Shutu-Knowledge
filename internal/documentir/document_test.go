package documentir

import "testing"

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

func TestBindDocumentRejectsUnknownVersion(t *testing.T) {
	doc := &Document{IRVersion: "document-ir/v9"}
	if err := doc.BindDocument("doc"); err == nil {
		t.Fatal("unknown IR version accepted")
	}
}
