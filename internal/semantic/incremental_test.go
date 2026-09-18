package semantic

import (
	"testing"
)

func TestMergeIncrementalPreservesUnchangedCrossDocumentSupport(t *testing.T) {
	doc1 := sourceDocumentForCompile("doc-1", "Vector Guide", "# Vector Retrieval\n\nThe vector service uses 1024-dimensional vectors.", 7, 3)
	doc2 := sourceDocumentForCompile("doc-2", "Vector Update", "# Vector Retrieval\n\nThe vector service uses 1024-dimensional vectors. The update provides reranking.", 9, 4)
	previous, err := Compile("base-inc", 1, []SourceDocument{doc1, doc2}, 1_000)
	if err != nil {
		t.Fatal(err)
	}
	delta, err := Compile("base-inc", 2, []SourceDocument{doc2}, 2_000)
	if err != nil {
		t.Fatal(err)
	}
	merged, err := MergeIncremental(previous, delta, map[string]bool{"doc-2": true}, nil,
		[]string{"doc-1", "doc-2"}, 2_000)
	if err != nil {
		t.Fatalf("MergeIncremental: %v", err)
	}
	if merged.Generation != 2 || merged.State != CompilationBuilding {
		t.Fatalf("merged scope = %+v", merged)
	}
	byType := map[UnitKind][]Unit{}
	for _, unit := range merged.Units {
		byType[unit.Type] = append(byType[unit.Type], unit)
	}
	if len(byType[UnitSummary]) != 2 {
		t.Fatalf("summary count = %d, want 2", len(byType[UnitSummary]))
	}
	var sharedFact, newFact *Unit
	for i := range byType[UnitFact] {
		unit := byType[UnitFact][i]
		if unit.CanonicalKey == sharedVectorFactKey(t, previous) {
			sharedFact = &byType[UnitFact][i]
		} else {
			newFact = &byType[UnitFact][i]
		}
	}
	if sharedFact == nil || len(sharedFact.Sources) != 2 {
		t.Fatalf("shared fact lost cross-document support: %+v", sharedFact)
	}
	if newFact == nil {
		t.Fatal("dirty-document fact was not merged")
	}
	for _, unit := range merged.Units {
		if unit.ID == previous.Units[0].ID {
			t.Fatal("old generation unit ID was reused")
		}
	}
	if _, err := ResolveEvidence(merged.Units, byType[UnitSummary][0].ID); err != nil {
		t.Fatalf("merged provenance: %v", err)
	}
}

func TestMergeIncrementalPropagatesDeleteWithoutOrphans(t *testing.T) {
	doc1 := sourceDocumentForCompile("doc-1", "Vector Guide", "# Vector Retrieval\n\nThe vector service uses 1024-dimensional vectors.", 7, 3)
	doc2 := sourceDocumentForCompile("doc-2", "Vector Update", "# Vector Retrieval\n\nThe vector service uses 1024-dimensional vectors.", 9, 4)
	previous, err := Compile("base-del", 1, []SourceDocument{doc1, doc2}, 1_000)
	if err != nil {
		t.Fatal(err)
	}
	deletedDelta := emptyDeltaForTest("base-del", 2, 2_000)
	merged, err := MergeIncremental(previous, deletedDelta, nil, map[string]bool{"doc-2": true},
		[]string{"doc-1"}, 2_000)
	if err != nil {
		t.Fatalf("MergeIncremental delete: %v", err)
	}
	var sharedFact *Unit
	for i, unit := range merged.Units {
		if unit.Type == UnitFact {
			sharedFact = &merged.Units[i]
			break
		}
	}
	if sharedFact == nil {
		t.Fatal("fact supported by the remaining document was removed")
	}
	if len(sharedFact.Sources) != 1 || sharedFact.Sources[0].DocumentID != "doc-1" {
		t.Fatalf("deleted source survived: %+v", sharedFact.Sources)
	}
	for _, unit := range merged.Units {
		sources, err := ResolveEvidence(merged.Units, unit.ID)
		if err != nil {
			t.Fatalf("ResolveEvidence %s: %v", unit.ID, err)
		}
		for _, source := range sources {
			if source.DocumentID == "doc-2" {
				t.Fatalf("orphan provenance survived: %+v", source)
			}
		}
	}
	if err := merged.Validate(); err != nil {
		t.Fatalf("merged validation: %v", err)
	}
}

func sharedVectorFactKey(t *testing.T, compilation Compilation) string {
	t.Helper()
	for _, unit := range compilation.Units {
		if unit.Type == UnitFact && len(unit.Sources) == 2 {
			return unit.CanonicalKey
		}
	}
	t.Fatal("shared fact missing")
	return ""
}

func emptyDeltaForTest(baseID string, generation, timestamp int64) Compilation {
	return Compilation{
		BaseID: baseID, Generation: generation, State: CompilationBuilding,
		Compiler: BuiltinCompiler, CompilerVersion: BuiltinCompilerVersion,
		Model: BuiltinModel, ModelVersion: BuiltinModelVersion,
		PromptVersion: BuiltinPromptVersion, CreatedAt: timestamp,
	}
}
