package health

import (
	"context"
	"errors"
	"testing"
)

func TestSnapshotAggregatesSeverities(t *testing.T) {
	reg := NewRegistry()
	reg.Register(CheckerFunc{CheckName: "database", Level: Critical, Fn: func(context.Context) error { return nil }})
	reg.Register(CheckerFunc{CheckName: "reranker", Level: Optional, Fn: func(context.Context) error { return errors.New("model missing") }})
	report := reg.Snapshot(context.Background())
	if !report.Ready || report.Status != "ready" {
		t.Fatalf("optional failure must not flip readiness: %+v", report)
	}
	if len(report.Components) != 2 {
		t.Fatalf("components: %+v", report.Components)
	}

	reg.Register(CheckerFunc{CheckName: "schema", Level: Critical, Fn: func(context.Context) error { return errors.New("boom") }})
	report = reg.Snapshot(context.Background())
	if report.Ready {
		t.Fatal("critical failure must flip readiness")
	}
	if report.Status == "ready" || !contains(report.Status, "schema") {
		t.Fatalf("status should name the failure: %q", report.Status)
	}
}

func contains(haystack, needle string) bool {
	for i := 0; i+len(needle) <= len(haystack); i++ {
		if haystack[i:i+len(needle)] == needle {
			return true
		}
	}
	return false
}
