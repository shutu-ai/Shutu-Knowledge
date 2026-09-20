package semantic

import (
	"fmt"
	"strings"
	"testing"
)

func TestParseTemporalRangeExplicitVersionRange(t *testing.T) {
	tests := []struct{ query, from, to string }{
		{query: "What changed from 0.2 to 0.5?", from: "0.2", to: "0.5"},
		{query: "Compare v2.7.10 through v2.9", from: "2.7.10", to: "2.9"},
	}
	for _, test := range tests {
		r, ok := ParseTemporalRange(test.query)
		if !ok || r.Kind != RangeVersion || r.Start == nil || r.End == nil {
			t.Fatalf("ParseTemporalRange(%q) = %+v,%v", test.query, r, ok)
		}
		if r.Start.Value != test.from || !r.Start.Inclusive || r.End.Value != test.to || !r.End.Inclusive {
			t.Fatalf("range = %+v, want %s..%s", r, test.from, test.to)
		}
		if !r.ContainsVersion(test.from) || !r.ContainsVersion(test.to) {
			t.Fatalf("range excludes endpoint: %+v", r)
		}
	}
}

func TestParseTemporalRangeReversesReversedPair(t *testing.T) {
	r, ok := ParseTemporalRange("changes from 0.5 to 0.2")
	if !ok || r.Start == nil || r.End == nil || r.Start.Value != "0.2" || r.End.Value != "0.5" {
		t.Fatalf("range=%+v ok=%v", r, ok)
	}
}

func TestParseTemporalRangeRelativeVersionBoundaries(t *testing.T) {
	before, ok := ParseTemporalRange("How did it work before 0.4?")
	if !ok || before.Kind != RangeVersion || before.Start != nil || before.End == nil || before.End.Value != "0.4" || before.End.Inclusive {
		t.Fatalf("before=%+v ok=%v", before, ok)
	}
	since, ok := ParseTemporalRange("What changed since 0.4?")
	if !ok || since.Kind != RangeVersion || since.Start == nil || since.Start.Value != "0.4" || !since.Start.Inclusive || since.End != nil {
		t.Fatalf("since=%+v ok=%v", since, ok)
	}
}

func TestParseTemporalRangeKeepsVersionAndTimeSeparate(t *testing.T) {
	timer, ok := ParseTemporalRange("What changed from 2024-01-01 to 2026-01-01?")
	if !ok || timer.Kind != RangeTime || timer.Start == nil || timer.End == nil || timer.Start.Time == nil || timer.End.Time == nil {
		t.Fatalf("time range=%+v ok=%v", timer, ok)
	}
	if timer.Start.Time.Format("2006-01-02") != "2024-01-01" || timer.End.Time.Format("2006-01-02") != "2026-01-01" {
		t.Fatalf("time endpoints=%+v", timer)
	}
}

func TestParseTemporalRangePreservesAmbiguity(t *testing.T) {
	r, ok := ParseTemporalRange("What happened in earlier release stages?")
	if !ok || r.Kind != RangeAmbiguous || !r.Ambiguous || r.Start != nil || r.End != nil {
		t.Fatalf("ambiguous range=%+v ok=%v", r, ok)
	}
	if got := r.Describe(); got == "" {
		t.Fatal("Describe returned empty diagnostics")
	}
}

func TestTemporalRangeRejectsUnknownOrder(t *testing.T) {
	r, ok := ParseTemporalRange("What changed from 26.2.0-beta to 26.3?")
	if ok {
		t.Fatalf("unknown suffix accepted as ordered range: %+v", r)
	}
}

func TestResolveTemporalRangeSelectsOrderedVersions(t *testing.T) {
	compilation := Compilation{Units: []Unit{
		{ID: "old", Version: "0.2", Status: UnitActive},
		{ID: "middle", Version: "0.3", Status: UnitActive},
		{ID: "new", Version: "0.4", Status: UnitActive},
		{ID: "unknown", Version: "26.2.0-beta", Status: UnitActive},
		{ID: "deleted", Version: "0.3", Status: UnitDeleted},
	}}
	r, ok := ParseTemporalRange("What changed from 0.2 to 0.4?")
	if !ok {
		t.Fatal("range parse failed")
	}
	resolved := ResolveTemporalRange(compilation, r)
	if len(resolved.Versions) != 3 || resolved.Versions[0] != "0.2" || resolved.Versions[2] != "0.4" {
		t.Fatalf("versions=%+v", resolved.Versions)
	}
	if resolved.UnitCount != 3 || len(resolved.Units) != 3 {
		t.Fatalf("resolved=%+v", resolved)
	}
}

func TestResolveRelativeEventBoundary(t *testing.T) {
	compilation := Compilation{Units: []Unit{
		{ID: "pre", Version: "0.2", Title: "RAG", Content: "before semantic memory"},
		{ID: "intro", Version: "0.3", Title: "Document Intelligence", Content: "introduced semantic memory"},
		{ID: "post", Version: "0.4", Title: "Semantic Memory", Content: "semantic memory expanded"},
	}}
	r, ok := ParseTemporalRange("How did knowledge work before semantic memory?")
	if !ok || r.Kind != RangeEvent || r.End == nil || r.End.ResolvedVersion != "" {
		t.Fatalf("event range=%+v ok=%v", r, ok)
	}
	resolved := ResolveTemporalRange(compilation, r)
	if r.End.ResolvedVersion != "0.3" {
		t.Fatalf("end=%+v", r.End)
	}
	if len(resolved.Versions) != 1 || resolved.Versions[0] != "0.2" || resolved.UnitCount != 1 || resolved.Units[0].ID != "pre" {
		t.Fatalf("resolved=%+v", resolved)
	}
}

func TestResolveEventBoundaryRequiresEvidence(t *testing.T) {
	r, _ := ParseTemporalRange("How did it work before unicorns?")
	resolved := ResolveTemporalRange(Compilation{}, r)
	if resolved.Confidence != 0 || resolved.UnitCount != 0 || len(resolved.Versions) != 0 {
		t.Fatalf("unevidenced event resolved=%+v", resolved)
	}
}

func TestResolveTemporalRangeKeepsAmbiguousUnmaterialized(t *testing.T) {
	r, _ := ParseTemporalRange("What happened in earlier release stages?")
	resolved := ResolveTemporalRange(Compilation{Units: []Unit{{ID: "unit", Version: "0.1", Status: UnitActive}}}, r)
	if resolved.UnitCount != 0 || len(resolved.Versions) != 0 || !resolved.Range.Ambiguous {
		t.Fatalf("ambiguous materialized=%+v", resolved)
	}
}

func historyTestCompilation() Compilation {
	return Compilation{Units: []Unit{
		{ID: "v02", Version: "0.2", Type: UnitFact, Content: "RAG foundation indexed documents", Status: UnitActive, Sources: []EvidenceSource{{DocumentID: "doc-0.2", ChunkID: "chunk-0.2", IndexGeneration: 1, SourceVersion: 1}}},
		{ID: "v03", Version: "0.3", Type: UnitFact, Content: "Document IR introduced structured parsing", Status: UnitActive, Sources: []EvidenceSource{{DocumentID: "doc-0.3", ChunkID: "chunk-0.3", IndexGeneration: 1, SourceVersion: 1}}},
		{ID: "v04", Version: "0.4", Type: UnitFact, Content: "Semantic memory added activated units", Status: UnitActive, Sources: []EvidenceSource{{DocumentID: "doc-0.4", ChunkID: "chunk-0.4", IndexGeneration: 1, SourceVersion: 1}}},
		{ID: "v05", Version: "0.5", Type: UnitFact, Content: "Temporal knowledge preserved version scope", Status: UnitActive, Sources: []EvidenceSource{{DocumentID: "doc-0.5", ChunkID: "chunk-0.5", IndexGeneration: 1, SourceVersion: 1}}},
	}}
}

func TestDeriveHistoricalTimelineUsesMinorVersionPhases(t *testing.T) {
	timeline := DeriveHistoricalTimeline(historyTestCompilation(), 4)
	if len(timeline.Phases) != 4 {
		t.Fatalf("phases=%d want 4: %+v", len(timeline.Phases), timeline)
	}
	if timeline.Phases[0].StartVersion != "0.2" || timeline.Phases[3].EndVersion != "0.5" {
		t.Fatalf("boundaries=%+v", timeline.Phases)
	}
	for index, phase := range timeline.Phases {
		if len(phase.SourceUnitIDs) == 0 || len(phase.Evidence) == 0 || len(phase.KeyChanges) == 0 {
			t.Fatalf("phase %d lacks provenance/evidence: %+v", index, phase)
		}
		if !strings.HasPrefix(phase.ID, "phase-") || phase.Metadata["basis"] != "ordered-source-version" {
			t.Fatalf("phase %d metadata=%+v", index, phase)
		}
	}
	if len(timeline.Transitions) != 3 {
		t.Fatalf("transitions=%d want 3", len(timeline.Transitions))
	}
	for _, transition := range timeline.Transitions {
		if transition.Statement == "" || len(transition.SourceUnitIDs) == 0 || len(transition.Evidence) == 0 {
			t.Fatalf("unevidenced transition=%+v", transition)
		}
	}
}

func TestDeriveHistoricalTimelineUsesMajorPhases(t *testing.T) {
	compilation := Compilation{Units: []Unit{
		{ID: "a", Version: "0.1", Type: UnitFact, Content: "early foundation", Status: UnitActive},
		{ID: "b", Version: "0.9", Type: UnitFact, Content: "late foundation", Status: UnitActive},
		{ID: "c", Version: "1.0", Type: UnitFact, Content: "first stable release", Status: UnitActive},
		{ID: "d", Version: "2.0", Type: UnitFact, Content: "second architecture era", Status: UnitActive},
	}}
	timeline := DeriveHistoricalTimeline(compilation, 4)
	if len(timeline.Phases) != 3 {
		t.Fatalf("phases=%d want 3: %+v", len(timeline.Phases), timeline)
	}
	if timeline.Phases[0].StartVersion != "0.1" || timeline.Phases[0].EndVersion != "0.9" {
		t.Fatalf("foundation phase=%+v", timeline.Phases[0])
	}
	if timeline.Phases[1].StartVersion != "1.0" || timeline.Phases[2].StartVersion != "2.0" {
		t.Fatalf("major phases=%+v", timeline.Phases)
	}
}

func TestDeriveHistoricalTimelineRequiresEvidence(t *testing.T) {
	timeline := DeriveHistoricalTimeline(Compilation{}, 4)
	if len(timeline.Phases) != 0 || timeline.Confidence != 0 {
		t.Fatalf("empty timeline=%+v", timeline)
	}
}

func TestRangeHistoryIntentPreservesAmbiguity(t *testing.T) {
	for _, query := range []string{
		"What happened in earlier release stages?",
		"Describe the early history of the project.",
		"以前是什么样？",
	} {
		plan := RouteQuery(query)
		if plan.Temporal.Intent != TemporalRangeHistory {
			t.Fatalf("query %q intent=%s", query, plan.Temporal.Intent)
		}
		if plan.Temporal.Range == nil || plan.Temporal.Range.Kind != RangeAmbiguous || !plan.Temporal.Range.Ambiguous {
			t.Fatalf("query %q range=%+v", query, plan.Temporal.Range)
		}
		if plan.Intent != IntentTemporal || !plan.UseSemanticMemory {
			t.Fatalf("query %q plan=%+v", query, plan)
		}
	}
}

func TestRangeHistoryIntentPreservesExistingExplicitVersion(t *testing.T) {
	plan := RouteQuery("historical Version 0.2 supports the 128k context window")
	if plan.Temporal.Intent != TemporalExplicitVersion || plan.Temporal.FromVersion != "0.2" {
		t.Fatalf("plan=%+v", plan.Temporal)
	}
	if plan.Temporal.Range != nil && plan.Temporal.Range.Ambiguous {
		t.Fatalf("ambiguous range attached to explicit version: %+v", plan.Temporal.Range)
	}
}

func TestRangeHistoryIntentUsesRelativeEvent(t *testing.T) {
	plan := RouteQuery("How did knowledge work before semantic memory?")
	if plan.Temporal.Intent != TemporalRangeHistory || plan.Temporal.Range == nil || plan.Temporal.Range.Kind != RangeEvent {
		t.Fatalf("plan=%+v", plan.Temporal)
	}
	if plan.Temporal.Range.End == nil || plan.Temporal.Range.End.Value != "semantic memory" {
		t.Fatalf("range=%+v", plan.Temporal.Range)
	}
}
func TestSelectHistoricalRepresentativesSamplesEarlierHalf(t *testing.T) {
	compilation := Compilation{Units: make([]Unit, 0, 20)}
	for index := 0; index < 20; index++ {
		version := "0." + fmt.Sprint(index)
		compilation.Units = append(compilation.Units, Unit{
			ID: "unit-" + version, Type: UnitFact, Title: "release-" + version,
			Content: "release Open5GS stage " + version, Version: version, Status: UnitActive,
		})
	}
	rangeResult, ok := ParseTemporalRange("What happened in earlier release stages?")
	if !ok || rangeResult.Kind != RangeAmbiguous {
		t.Fatalf("range=%+v ok=%v", rangeResult, ok)
	}
	selected := SelectHistoricalRepresentatives(compilation, rangeResult, 8, "What happened in earlier release stages?", []string{"release", "Open5GS"})
	if len(selected) != 8 {
		t.Fatalf("selected=%d want bounded sample of older half", len(selected))
	}
	if selected[0].Version != "0.0" || selected[len(selected)-1].Version != "0.7" {
		t.Fatalf("range endpoints=%+v", selected)
	}
	seen := map[string]bool{}
	for _, unit := range selected {
		if seen[unit.Version] {
			t.Fatalf("duplicate representative version %s", unit.Version)
		}
		seen[unit.Version] = true
	}
}
func TestGlobalHistoryPreservesGlobalRouting(t *testing.T) {
	plan := RouteQuery("Summarize the current Open5GS corpus, release history, and main capabilities.")
	if plan.Intent != IntentGlobal {
		t.Fatalf("intent=%s want global", plan.Intent)
	}
	if plan.Temporal.Intent != TemporalHistorical || plan.Temporal.Range != nil {
		t.Fatalf("temporal=%+v", plan.Temporal)
	}
}
