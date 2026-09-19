package semantic

import (
	"fmt"
	"strings"
	"testing"
)

func TestVersionIdentitySupportsRequiredFamiliesAndUnknownOrder(t *testing.T) {
	cases := []struct {
		name        string
		left, right string
		expected    int
		comparable  bool
	}{
		{name: "semantic patch", left: "v26.2.0", right: "26.10.0", expected: -1, comparable: true},
		{name: "release", left: "Rel-17", right: "Release 18", expected: -1, comparable: true},
		{name: "3gpp document", left: "v170700p", right: "18.1.0", expected: -1, comparable: true},
		{name: "cross family", left: "26.2", right: "Rel-26", expected: 0, comparable: false},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			order, ok := CompareVersionIdentity(test.left, test.right)
			if order != test.expected || ok != test.comparable {
				t.Fatalf("CompareVersionIdentity(%q,%q)=%d,%v want %d,%v", test.left, test.right, order, ok, test.expected, test.comparable)
			}
		})
	}
}

func TestParseTemporalQueryCategories(t *testing.T) {
	cases := []struct {
		query    string
		expected TemporalIntent
		versions []string
	}{
		{query: "当前版本支持哪些功能？", expected: TemporalCurrent},
		{query: "What was true in version 26.2?", expected: TemporalExplicitVersion, versions: []string{"26.2"}},
		{query: "旧版本的行为是什么", expected: TemporalHistorical},
		{query: "What changed from 26.2 to 26.3?", expected: TemporalEvolution, versions: []string{"26.2", "26.3"}},
		{query: "这个配置现在还有效吗？", expected: TemporalValidity},
		{query: "How does retrieval work?", expected: TemporalNone},
	}
	for _, test := range cases {
		parsed := ParseTemporalQuery(test.query)
		if parsed.Intent != test.expected {
			t.Fatalf("ParseTemporalQuery(%q)=%+v want intent %v", test.query, parsed, test.expected)
		}
		if len(parsed.Versions) != len(test.versions) {
			t.Fatalf("versions(%q)=%v want %v", test.query, parsed.Versions, test.versions)
		}
		for i, version := range test.versions {
			if parsed.Versions[i] != version {
				t.Fatalf("version %d=%q want %q", i, parsed.Versions[i], version)
			}
		}
	}
}

func TestComparisonPreservesCrossDocumentRouteWithExplicitScope(t *testing.T) {
	plan := RouteQuery("How do release 0.3 and vector retrieval work across documents?")
	if plan.Intent != IntentCrossDocument {
		t.Fatalf("intent=%v want %v temporal=%+v", plan.Intent, IntentCrossDocument, plan.Temporal)
	}
	if plan.Temporal.Intent != TemporalExplicitVersion || len(plan.Temporal.Versions) != 1 {
		t.Fatalf("temporal scope=%+v", plan.Temporal)
	}
}

func TestExtractTemporalSourceDoesNotTreatPublicationDateAsVersion(t *testing.T) {
	version, published, _, _, ok := ExtractTemporalSource("2026-06-20-release-v2.8.0.md", nil)
	if !ok || version != "2.8.0" || published == 0 {
		t.Fatalf("extract = %q,%d,%v want 2.8.0,nonzero,true", version, published, ok)
	}
}

func TestTemporalCompilationSupersedesAndKeepsProvenance(t *testing.T) {
	old := sourceDocumentForCompile("release-old", "Release v26.2", "# Release v26.2\n\nVersion 26.2 supports the 128k context window.", 1, 1)
	new := sourceDocumentForCompile("release-new", "Release v26.3", "# Release v26.3\n\nVersion 26.3 supports the 1M context window.", 2, 2)
	compilation, err := Compile("temporal-base", 1, []SourceDocument{old, new}, 1000)
	if err != nil {
		t.Fatal(err)
	}
	var oldUnit, newUnit *Unit
	for i := range compilation.Units {
		unit := &compilation.Units[i]
		if unit.Type != UnitFact {
			continue
		}
		switch {
		case strings.Contains(unit.Content, "128k"):
			oldUnit = unit
		case strings.Contains(unit.Content, "1M"):
			newUnit = unit
		}
	}
	if oldUnit == nil || newUnit == nil {
		t.Fatalf("versioned facts missing")
	}
	if oldUnit.Status != UnitSuperseded || oldUnit.SupersededBy != newUnit.ID || newUnit.Status != UnitActive {
		t.Fatalf("lifecycle old=%+v new=%+v", *oldUnit, *newUnit)
	}
	if oldUnit.Version != "26.2" || newUnit.Version != "26.3" {
		t.Fatalf("source-derived versions old=%q new=%q", oldUnit.Version, newUnit.Version)
	}
	supersedes := 0
	for _, relation := range compilation.Relations {
		if relation.Type == RelationSupersedes && relation.SubjectUnitID == oldUnit.ID && relation.ObjectUnitID == newUnit.ID {
			supersedes++
			if len(relation.Sources) == 0 {
				t.Fatal("supersession relation lacks provenance")
			}
		}
	}
	if supersedes != 1 {
		t.Fatalf("supersedes relations=%d want 1", supersedes)
	}
}

func TestVersionAwareSearchSelectsRequestedScope(t *testing.T) {
	compilation, err := Compile("search-temporal", 1, []SourceDocument{
		sourceDocumentForCompile("old", "Release v26.2", "# Release v26.2\n\nVersion 26.2 supports the 128k context window.", 1, 1),
		sourceDocumentForCompile("new", "Release v26.3", "# Release v26.3\n\nVersion 26.3 supports the 1M context window.", 2, 2),
	}, 1000)
	if err != nil {
		t.Fatal(err)
	}

	current, err := SearchCompilation(compilation, SearchOptions{Query: "当前 context window", Kinds: []UnitKind{UnitFact}})
	if err != nil {
		t.Fatal(err)
	}
	if len(current.Hits) != 1 || !strings.Contains(current.Hits[0].Unit.Content, "1M") {
		t.Fatalf("current hits=%+v", current.Hits)
	}
	historical, err := SearchCompilation(compilation, SearchOptions{Query: "26.2 context window", Kinds: []UnitKind{UnitFact}})
	if err != nil {
		t.Fatal(err)
	}
	if len(historical.Hits) != 1 || !strings.Contains(historical.Hits[0].Unit.Content, "128k") {
		t.Fatalf("historical hits=%+v", historical.Hits)
	}
	if historical.Routing == nil || historical.Routing.Temporal.Intent != TemporalExplicitVersion || historical.Routing.Temporal.FromVersion != "26.2" {
		t.Fatalf("historical routing=%+v", historical.Routing)
	}
}

func TestTemporalSupersessionAuditExhaustivelyChecksThirtyPairs(t *testing.T) {
	for i := 0; i < 30; i++ {
		oldVersion, newVersion := fmt.Sprintf("1.%d", i), fmt.Sprintf("1.%d", i+1)
		compilation, err := Compile(fmt.Sprintf("audit-%02d", i), 1, []SourceDocument{
			sourceDocumentForCompile("old", "Release "+oldVersion, "# Release\n\nVersion "+oldVersion+" supports bounded context.", 1, 1),
			sourceDocumentForCompile("new", "Release "+newVersion, "# Release\n\nVersion "+newVersion+" supports bounded context.", 2, 2),
		}, 1000)
		if err != nil {
			t.Fatal(err)
		}
		relations := make([]Relation, 0, 1)
		for _, relation := range compilation.Relations {
			if relation.Type == RelationSupersedes {
				relations = append(relations, relation)
			}
		}
		if len(relations) != 1 {
			t.Fatalf("pair %d relations=%d want 1", i, len(relations))
		}
		subject, object := findTemporalAuditUnit(compilation.Units, relations[0].SubjectUnitID), findTemporalAuditUnit(compilation.Units, relations[0].ObjectUnitID)
		order, comparable := CompareVersionIdentity(subject.Version, object.Version)
		if !comparable || order >= 0 || len(relations[0].Sources) == 0 {
			t.Fatalf("pair %d invalid supersession %+v subject=%+v object=%+v", i, relations[0], subject, object)
		}
	}
}

func findTemporalAuditUnit(units []Unit, id string) Unit {
	for _, unit := range units {
		if unit.ID == id {
			return unit
		}
	}
	return Unit{}
}

func TestExtractTemporalSourceHandlesCompact3GPPFilename(t *testing.T) {
	version, _, _, _, ok := ExtractTemporalSource("3gpp/ts_123501v181200p_excerpt.txt", nil)
	if !ok || version != "18.12.0" {
		t.Fatalf("version=%q ok=%v want 18.12.0,true", version, ok)
	}
}

func TestLatestComparableVersionSkipsUnknownSuffixButKeepsKnownOrder(t *testing.T) {
	version, ok := LatestComparableVersion([]string{"2.7.7", "2.7.10"})
	if !ok || version != "2.7.10" {
		t.Fatalf("latest = %q,%v want 2.7.10,true", version, ok)
	}
}

func TestResolveCurrentVersionRefusesUnknownOrder(t *testing.T) {
	compilation := Compilation{Units: []Unit{
		{ID: "a", Version: "26.2", Status: UnitActive},
		{ID: "b", Version: "Rel-26", Status: UnitActive},
	}}
	if version, ok := ResolveCurrentVersion(compilation); ok || version != "" {
		t.Fatalf("unknown order resolved version=%q,%v", version, ok)
	}
}
