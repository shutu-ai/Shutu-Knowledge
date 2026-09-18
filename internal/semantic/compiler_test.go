package semantic

import (
	"reflect"
	"testing"

	"github.com/shutu-ai/shutu-knowledge/internal/documentir"
)

func TestBuiltinCompilerProducesProvenancedHierarchy(t *testing.T) {
	documents := []SourceDocument{
		sourceDocumentForCompile("doc-1", "Vector Retrieval", "# Vector Retrieval\n\nThe vector service uses 1024-dimensional vectors.", 7, 3),
		sourceDocumentForCompile("doc-2", "Vector Retrieval", "# Vector Retrieval\n\nThe vector service uses 1024-dimensional vectors.", 9, 4),
	}
	first, err := Compile("base-1", 11, documents, 1_000)
	if err != nil {
		t.Fatalf("Compile: %v", err)
	}
	if first.Compiler != BuiltinCompiler || first.Model != "deterministic" || first.PromptVersion != "none" {
		t.Fatalf("compiler provenance changed: %+v", first)
	}
	counts := map[UnitKind]int{}
	for _, unit := range first.Units {
		counts[unit.Type]++
	}
	if !reflect.DeepEqual(counts, map[UnitKind]int{UnitFact: 1, UnitConcept: 1, UnitTopic: 1, UnitSummary: 2}) {
		t.Fatalf("unit counts = %+v, compilation = %+v", counts, first)
	}
	if len(first.Relations) != 0 {
		t.Fatalf("PH2 unexpectedly emitted semantic relations: %+v", first.Relations)
	}

	var fact, concept, topic, summary Unit
	for _, unit := range first.Units {
		switch unit.Type {
		case UnitFact:
			fact = unit
		case UnitConcept:
			concept = unit
		case UnitTopic:
			topic = unit
		case UnitSummary:
			if summary.ID == "" {
				summary = unit
			}
		}
	}
	if len(fact.Sources) != 2 {
		t.Fatalf("identical fact was not deduplicated across documents: %+v", fact.Sources)
	}
	for _, source := range fact.Sources {
		if source.NodeID == "" || source.ChunkID == "" || source.DocumentID == "" {
			t.Fatalf("fact source is not exact evidence: %+v", source)
		}
	}
	if len(concept.DerivedFrom) != 1 || concept.DerivedFrom[0] != fact.ID {
		t.Fatalf("concept is not derived from fact: %+v", concept)
	}
	if len(topic.DerivedFrom) != 1 || topic.DerivedFrom[0] != concept.ID {
		t.Fatalf("topic is not derived from concept: %+v", topic)
	}
	sources, err := ResolveEvidence(first.Units, summary.ID)
	if err != nil {
		t.Fatalf("ResolveEvidence summary: %v", err)
	}
	if len(sources) == 0 {
		t.Fatal("summary resolves to no exact evidence")
	}

	second, err := Compile("base-1", 11, documents, 1_000)
	if err != nil {
		t.Fatalf("Compile second: %v", err)
	}
	if !reflect.DeepEqual(first, second) {
		t.Fatal("builtin compiler output is not deterministic")
	}
}

func TestBuiltinCompilerFallsBackToHeadingProvenance(t *testing.T) {
	document := sourceDocumentForCompile("doc-outline", "Release Runbook", "# Release Runbook\n\nOverview introduction only.", 2, 2)
	compilation, err := Compile("base-outline", 1, []SourceDocument{document}, 1_234)
	if err != nil {
		t.Fatalf("Compile: %v", err)
	}
	var found bool
	for _, unit := range compilation.Units {
		if unit.Type != UnitConcept {
			continue
		}
		if len(unit.DerivedFrom) == 0 && len(unit.Sources) > 0 {
			found = true
		}
	}
	if !found {
		t.Fatalf("heading-only concept lacks direct provenance: %+v", compilation.Units)
	}
}

func TestCompileRejectsMissingIR(t *testing.T) {
	if _, err := Compile("base", 1, []SourceDocument{{DocumentID: "d", IndexGeneration: 1, SourceVersion: 1}}, 1); err == nil {
		t.Fatal("Compile accepted a source without Document IR")
	}
}

func sourceDocumentForCompile(id, title, text string, generation, version int64) SourceDocument {
	ir := documentir.FromText(title, text, "test", "v1")
	if err := ir.BindDocument(id); err != nil {
		panic(err)
	}
	chunks := map[string][]string{}
	for _, node := range ir.Nodes {
		if node.Type == documentir.TypeParagraph {
			chunks[node.ID] = []string{"chunk-" + id + "-" + node.ID}
		}
	}
	return SourceDocument{
		BaseID: "base-1", DocumentID: id, Title: title,
		IndexGeneration: generation, SourceVersion: version, IR: ir,
		ChunkIDsByNode: chunks, UpdatedAt: 500,
	}
}
