package knowledge

import (
	"testing"

	"github.com/shutu-ai/shutu-knowledge/internal/documentir"
)

func TestDerivedKnowledgeIDsIncludeProvenance(t *testing.T) {
	doc := &documentir.Document{
		Nodes: []documentir.Node{
			{ID: "node-a", Type: documentir.TypeSection, Text: "Same text"},
			{ID: "node-b", Type: documentir.TypeSection, Text: "Same text"},
		},
	}
	records := deriveKnowledge("doc", 1, doc)
	seen := map[string]bool{}
	for _, record := range records {
		if seen[record.ID] {
			t.Fatalf("duplicate derived knowledge id %s", record.ID)
		}
		seen[record.ID] = true
	}
}
