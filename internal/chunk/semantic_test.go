package chunk

import (
	"strings"
	"testing"
)

func TestMergeSemanticSegmentsCoherentAndBoundary(t *testing.T) {
	segments := []Piece{
		{Text: "alpha topic sentence"},
		{Text: "alpha continues"},
		{Text: "completely unrelated"},
	}
	vectors := [][]float64{
		{1, 0, 0},
		{0.99, 0.1, 0},
		{0, 1, 0},
	}
	merged := MergeSemanticSegments(segments, vectors, 800, 0.75)
	if len(merged) != 2 {
		t.Fatalf("expected 2 merged chunks, got %d: %+v", len(merged), merged)
	}
	if !strings.Contains(merged[0].Text, "alpha continues") {
		t.Fatalf("first merge: %q", merged[0].Text)
	}
	if merged[0].Vector == nil || merged[0].Vector[0] < 0.99 {
		t.Fatalf("mean vector: %v", merged[0].Vector)
	}
	// Threshold 0 merges everything coherent.
	merged = MergeSemanticSegments(segments, vectors, 800, 0)
	if len(merged) != 1 {
		t.Fatalf("threshold 0 should merge all: %d", len(merged))
	}
}

func TestMergeSemanticSegmentsBudgetAndFallback(t *testing.T) {
	long := strings.Repeat("内容。", 400)
	segments := []Piece{{Text: long}, {Text: long}}
	vectors := [][]float64{{1, 0}, {1, 0}}
	// Tiny budget forces a split despite high similarity.
	merged := MergeSemanticSegments(segments, vectors, 64, 0.1)
	if len(merged) < 2 {
		t.Fatalf("budget should split: %d", len(merged))
	}
	// No vectors: budget-only merging still works (fallback path).
	merged = MergeSemanticSegments([]Piece{{Text: "a"}, {Text: "b"}}, nil, 800, 0.75)
	if len(merged) != 1 {
		t.Fatalf("vector-less merge: %d", len(merged))
	}
}
