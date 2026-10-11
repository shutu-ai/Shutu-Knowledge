package semantic

import (
	"reflect"
	"strings"
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

func TestCompilerMarksOlderVersionedFactSuperseded(t *testing.T) {
	documents := []SourceDocument{
		sourceDocumentForCompile("doc-old", "Release", "# Release\n\nVersion 0.2 supports the 128k context window.", 1, 1),
		sourceDocumentForCompile("doc-new", "Release", "# Release\n\nVersion 0.3 supports the 1M context window.", 2, 2),
	}
	compilation, err := Compile("base-temporal", 1, documents, 1_000)
	if err != nil {
		t.Fatal(err)
	}
	var oldFact, newFact *Unit
	for i, unit := range compilation.Units {
		if unit.Type != UnitFact {
			continue
		}
		if strings.Contains(strings.ToLower(unit.Content), "128k") {
			oldFact = &compilation.Units[i]
		}
		if strings.Contains(strings.ToLower(unit.Content), "1m") {
			newFact = &compilation.Units[i]
		}
	}
	if oldFact == nil || newFact == nil {
		t.Fatalf("versioned facts missing: %+v", compilation.Units)
	}
	if oldFact.Status != UnitSuperseded || oldFact.SupersededBy != newFact.ID || newFact.Status != UnitActive {
		t.Fatalf("temporal lifecycle = old %+v new %+v", *oldFact, *newFact)
	}
	response, err := SearchCompilation(compilation, SearchOptions{Query: "version context window", Kinds: []UnitKind{UnitFact}})
	if err != nil {
		t.Fatal(err)
	}
	for _, hit := range response.Hits {
		if hit.Unit.ID == oldFact.ID {
			t.Fatalf("superseded fact returned as active: %+v", hit.Unit)
		}
	}
}

func TestCompilerMarksSameVersionContradictionConflicted(t *testing.T) {
	documents := []SourceDocument{
		sourceDocumentForCompile("doc-a", "Release", "# Release\n\nVersion 0.3 supports the 1M context window.", 1, 1),
		sourceDocumentForCompile("doc-b", "Release Copy", "# Release\n\nVersion 0.3 supports the 2M context window.", 2, 2),
	}
	compilation, err := Compile("base-conflict", 1, documents, 1_000)
	if err != nil {
		t.Fatal(err)
	}
	conflicted := 0
	for _, unit := range compilation.Units {
		if unit.Type != UnitFact || !strings.Contains(strings.ToLower(unit.Content), "context window") {
			continue
		}
		if unit.Status != UnitConflicted || unit.Metadata["temporal_conflict"] != "same-version" {
			t.Fatalf("same-version fact not marked conflicted: %+v", unit)
		}
		conflicted++
	}
	if conflicted != 2 {
		t.Fatalf("conflicted fact count = %d, want 2", conflicted)
	}
	factSearch, err := SearchCompilation(compilation, SearchOptions{Query: "version context window", Kinds: []UnitKind{UnitFact}})
	if err != nil {
		t.Fatal(err)
	}
	if len(factSearch.Hits) != 0 {
		t.Fatalf("conflicted facts leaked into active fact search: %+v", factSearch.Hits)
	}
}

func TestTemporalSearchCanRecallSupersededHistory(t *testing.T) {
	documents := []SourceDocument{
		sourceDocumentForCompile("doc-old-history", "Release", "# Release\n\nVersion 0.2 supports the 128k context window.", 1, 1),
		sourceDocumentForCompile("doc-new-history", "Release", "# Release\n\nVersion 0.3 supports the 1M context window.", 2, 2),
	}
	compilation, err := Compile("base-history", 1, documents, 1_000)
	if err != nil {
		t.Fatal(err)
	}
	response, err := SearchCompilation(compilation, SearchOptions{Query: "historical Version 0.2 supports the 128k context window", Kinds: []UnitKind{UnitFact}})
	if err != nil {
		t.Fatal(err)
	}
	if len(response.Hits) == 0 {
		t.Fatal("historical temporal search returned no superseded fact")
	}
	found := false
	for _, hit := range response.Hits {
		if strings.Contains(strings.ToLower(hit.Unit.Content), "128k") && hit.Unit.Status == UnitSuperseded {
			found = true
		}
	}
	if !found {
		t.Fatalf("historical superseded fact missing: %+v", response.Hits)
	}
}

func TestCompilerCleansProjectionReplacementRunes(t *testing.T) {
	document := sourceDocumentForCompile("doc-clean", "接口�配置",
		"# 目录\n\n# API�服务\n\n系统使用�API服务来处理认证请求。", 1, 1)
	compilation, err := Compile("base-clean", 1, []SourceDocument{document}, 1_000)
	if err != nil {
		t.Fatal(err)
	}
	seenAPIConcept := false
	for _, unit := range compilation.Units {
		if strings.Contains(unit.Title, "\\ufffd") || strings.Contains(unit.Content, "\\ufffd") {
			t.Fatalf("replacement rune leaked into projection: %+v", unit)
		}
		if unit.Type == UnitConcept && unit.Title == "API 服务" {
			seenAPIConcept = true
		}
		if unit.Type == UnitConcept && (unit.Title == "目录" || unit.Title == "问题现象" || unit.Title == "..." || strings.HasPrefix(unit.Title, "1.")) {
			t.Fatalf("boilerplate/procedural concept escaped filter: %+v", unit)
		}
	}
	if !seenAPIConcept {
		t.Fatalf("cleaned API concept missing: %+v", compilation.Units)
	}
	foundFact := false
	for _, unit := range compilation.Units {
		if unit.Type == UnitFact && unit.Content == "系统使用 API服务来处理认证请求" {
			foundFact = true
		}
	}
	if !foundFact {
		t.Fatalf("cleaned fact missing: %+v", compilation.Units)
	}
}

func TestSummaryEvidenceStaysDocumentScoped(t *testing.T) {
	documents := []SourceDocument{
		sourceDocumentForCompile("doc-alpha", "Alpha Release", "# Shared Service\n\nThe alpha service uses alpha telemetry.", 1, 1),
		sourceDocumentForCompile("doc-beta", "Beta Release", "# Shared Service\n\nThe beta service uses beta telemetry.", 1, 1),
	}
	compilation, err := Compile("base-scoped-summary", 1, documents, 1_000)
	if err != nil {
		t.Fatal(err)
	}
	var alphaSummary, betaSummary Unit
	for _, unit := range compilation.Units {
		if unit.Type != UnitSummary {
			continue
		}
		if unit.Title == "Alpha Release" {
			alphaSummary = unit
		}
		if unit.Title == "Beta Release" {
			betaSummary = unit
		}
	}
	if alphaSummary.ID == "" || betaSummary.ID == "" {
		t.Fatalf("summaries missing: %+v", compilation.Units)
	}
	for _, testCase := range []struct {
		summary    Unit
		documentID string
	}{
		{alphaSummary, "doc-alpha"},
		{betaSummary, "doc-beta"},
	} {
		sources, err := ResolveEvidence(compilation.Units, testCase.summary.ID)
		if err != nil {
			t.Fatal(err)
		}
		if len(sources) == 0 {
			t.Fatalf("summary %s has no evidence", testCase.summary.Title)
		}
		for _, source := range sources {
			if source.DocumentID != testCase.documentID {
				t.Fatalf("summary %s leaked evidence from %s", testCase.summary.Title, source.DocumentID)
			}
		}
	}
}
