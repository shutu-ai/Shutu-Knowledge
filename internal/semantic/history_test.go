package semantic

import "testing"

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
