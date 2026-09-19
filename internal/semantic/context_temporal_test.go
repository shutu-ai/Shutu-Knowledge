package semantic

import (
	"testing"
)

func TestCompileContextExplicitVersionFiltersAndRefusesUnknownFallback(t *testing.T) {
	compilation := Compilation{BaseID: "temporal-context", Generation: 1}
	newUnit := func(version, value string) Unit {
		return Unit{
			ID: "unit-" + version, BaseID: compilation.BaseID, Generation: 1,
			Type: UnitFact, Title: "Release " + version, CanonicalKey: "fact:" + version,
			Content: "Release " + version + " supports replication.", Status: UnitActive,
			Version: version, Metadata: map[string]string{"temporal_version": version},
			Sources: []EvidenceSource{{DocumentID: "doc-" + version, IndexGeneration: 1, SourceVersion: 1, ChunkID: "chunk-" + version}},
		}
	}
	compilation.Units = []Unit{newUnit("26.2", "128k"), newUnit("26.3", "1m")}
	evidence := []ContextEvidence{
		{ChunkID: "chunk-26.2", DocumentID: "doc-26.2", DocumentTitle: "2020-01-01-release-v26.2.md", Text: "Release 26.2 supports replication.", Citation: "doc-26.2#chunk-26.2", Score: 1},
		{ChunkID: "chunk-26.3", DocumentID: "doc-26.3", DocumentTitle: "2021-01-01-release-v26.3.md", Text: "Release 26.3 supports replication.", Citation: "doc-26.3#chunk-26.3", Score: 2},
	}
	pkg, err := CompileContext(compilation, evidence, ContextCompileOptions{Query: "What was true in version 26.2?"})
	if err != nil { t.Fatal(err) }
	if len(pkg.Evidence) != 1 || pkg.Evidence[0].Version != "26.2" {
		t.Fatalf("explicit context = %+v", pkg.Evidence)
	}
	unknown, err := CompileContext(compilation, evidence, ContextCompileOptions{Query: "What is documented in version 99.9?"})
	if err != nil { t.Fatal(err) }
	if len(unknown.Evidence) != 0 {
		t.Fatalf("unknown version fell back: %+v", unknown.Evidence)
	}
}
