package semantic

import (
	"strings"
	"testing"
	"time"
)

func TestCompilationAndProvenanceChain(t *testing.T) {
	compilation := testCompilation(1)
	if err := compilation.Validate(); err != nil {
		t.Fatalf("Validate: %v", err)
	}
	if got := len(compilation.Units); got != 3 {
		t.Fatalf("units = %d, want 3", got)
	}
	sources, err := ResolveEvidence(compilation.Units, compilation.Units[2].ID)
	if err != nil {
		t.Fatalf("ResolveEvidence: %v", err)
	}
	if len(sources) != 1 || sources[0].NodeID == "" || sources[0].ChunkID == "" {
		t.Fatalf("page did not resolve through concept to exact evidence: %+v", sources)
	}
	if sources[0].DocumentID != "doc-1" || sources[0].IndexGeneration != 7 {
		t.Fatalf("unexpected source scope: %+v", sources[0])
	}
}

func TestValidationRejectsUnprovenKnowledgeAndBadIntervals(t *testing.T) {
	compilation := testCompilation(1)
	compilation.Units[1].DerivedFrom = nil
	if err := compilation.Validate(); err == nil || !strings.Contains(err.Error(), "neither evidence nor derived_from") {
		t.Fatalf("unproven concept error = %v", err)
	}

	compilation = testCompilation(1)
	compilation.Units[0].ValidFrom = 200
	compilation.Units[0].ValidTo = 100
	if err := compilation.Validate(); err == nil || !strings.Contains(err.Error(), "inverted validity") {
		t.Fatalf("interval error = %v", err)
	}

	compilation = testCompilation(1)
	compilation.Relations[0].Sources = nil
	if err := compilation.Validate(); err == nil || !strings.Contains(err.Error(), "relation") {
		t.Fatalf("unproven relation error = %v", err)
	}
}

func TestDeterministicIdentity(t *testing.T) {
	first := UnitID("base", 1, UnitFact, "fact:max-context")
	second := UnitID("base", 2, UnitFact, "fact:max-context")
	if first == second || !strings.HasPrefix(first, "ku_") {
		t.Fatalf("unit identity is not scoped and deterministic: %q %q", first, second)
	}
	relation := RelationID("base", 1, RelationSupports, first, second, "support")
	if !strings.HasPrefix(relation, "kr_") || relation == first {
		t.Fatalf("unexpected relation identity %q", relation)
	}
}

func testCompilation(generation int64) Compilation {
	const baseID = "base-1"
	now := time.Now().Unix()
	fact := Unit{
		ID:     UnitID(baseID, generation, UnitFact, "fact:vector-dimension"),
		BaseID: baseID, Generation: generation, Type: UnitFact,
		Title: "Vector dimension", CanonicalKey: "fact:vector-dimension",
		Content:    "The service uses 1024-dimensional vectors.",
		Confidence: 0.92, Status: UnitActive, CreatedAt: now, UpdatedAt: now,
		Sources: []EvidenceSource{{
			DocumentID: "doc-1", IndexGeneration: 7, SourceVersion: 3,
			NodeID: "node-1", ChunkID: "chunk-1", SourceAnchor: map[string]any{"page": 4},
		}},
	}
	concept := Unit{
		ID:     UnitID(baseID, generation, UnitConcept, "concept:vector-retrieval"),
		BaseID: baseID, Generation: generation, Type: UnitConcept,
		Title: "Vector retrieval", CanonicalKey: "concept:vector-retrieval",
		Content: "Vector retrieval maps queries and content into a shared embedding space.",
		Aliases: []string{"semantic retrieval"}, Confidence: 0.85,
		Status: UnitActive, CreatedAt: now, UpdatedAt: now,
		DerivedFrom: []string{fact.ID},
	}
	page := Unit{
		ID:     UnitID(baseID, generation, UnitKnowledgePage, "page:vector-retrieval"),
		BaseID: baseID, Generation: generation, Type: UnitKnowledgePage,
		Title: "Vector retrieval", CanonicalKey: "page:vector-retrieval",
		Content:    "# Vector retrieval\nThe service uses 1024-dimensional vectors.",
		Confidence: 0.8, Status: UnitActive, CreatedAt: now, UpdatedAt: now,
		DerivedFrom: []string{concept.ID},
	}
	relation := Relation{
		ID:     RelationID(baseID, generation, RelationSupports, fact.ID, concept.ID, "support:vector-dimension"),
		BaseID: baseID, Generation: generation, Type: RelationSupports,
		SubjectUnitID: fact.ID, ObjectUnitID: concept.ID,
		Predicate: "supports", Statement: "The dimension fact supports the vector-retrieval concept.",
		CanonicalKey: "support:vector-dimension", Confidence: 0.9,
		Status: UnitActive, CreatedAt: now, UpdatedAt: now,
		Sources: []EvidenceSource{{
			DocumentID: "doc-1", IndexGeneration: 7, SourceVersion: 3,
			NodeID: "node-1", ChunkID: "chunk-1", SourceOrder: 1,
		}},
	}
	return Compilation{
		BaseID: baseID, Generation: generation, State: CompilationBuilding,
		Compiler: "builtin", CompilerVersion: "v0", Model: "builtin",
		ModelVersion: "v1", PromptVersion: "v1", SourceDocumentIDs: []string{"doc-1"},
		Units: []Unit{fact, concept, page}, Relations: []Relation{relation}, CreatedAt: now,
	}
}
