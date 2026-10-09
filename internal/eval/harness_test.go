package eval

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
)

func formatFloat(v float64) string {
	return strconv.FormatFloat(v, 'f', 3, 64)
}

func repoDir(t *testing.T) string {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("cannot resolve repo root")
	}
	return filepath.Dir(filepath.Dir(filepath.Dir(file)))
}

func fixtureDir(t *testing.T) string {
	t.Helper()
	return filepath.Join(repoDir(t), "evals", "tiny-fixture")
}

func TestGoldSetValidationRejectsBadEntries(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "gold.jsonl")
	bad := strings.Join([]string{
		`{"question_id":"q1","question":"Q","category":"exact_fact"}`,
		`{"question_id":"q2","question":"Q","category":"exact_fact","gold_answer":"a","gold_evidence_locators":[]}`,
		`{"question_id":"q2","question":"Q","category":"exact_fact","gold_answer":"a","gold_evidence_locators":[{"document":"d","needle":"n"}]}`,
		`{"question_id":"q3","question":"Q","category":"no_answer","expected_abstain":true,"gold_evidence_locators":[{"document":"d","needle":"n"}]}`,
	}, "\n")
	if err := os.WriteFile(path, []byte(bad), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadGoldSet(path); err == nil {
		t.Fatal("expected validation failure")
	}
}

func TestTinyFixtureEvidenceEndToEnd(t *testing.T) {
	ctx := context.Background()
	fixture, err := LoadFixture(ctx, fixtureDir(t))
	if err != nil {
		t.Fatalf("load fixture: %v", err)
	}
	questions, err := LoadGoldSet(filepath.Join(fixtureDir(t), "gold.jsonl"))
	if err != nil {
		t.Fatalf("load gold set: %v", err)
	}
	if len(questions) != 12 {
		t.Fatalf("gold questions = %d, want 12", len(questions))
	}

	report, err := Run(ctx, fixture, questions, Options{
		Mode: "evidence", TopK: 10, Label: "evidence-a",
	})
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	byID := map[string]QuestionResult{}
	for _, result := range report.PerQuestion {
		byID[result.QuestionID] = result
		t.Logf("top scores: %s=%.3f", result.QuestionID, result.TopScore)
	}
	// Exact-fact retrieval must fully cover gold evidence.
	for _, id := range []string{"tiny-001", "tiny-002", "tiny-003"} {
		if !byID[id].Correct {
			t.Fatalf("%s: correct=false, recall=%.2f (%d/%d)", id, byID[id].Recall, byID[id].MatchedLocators, byID[id].TotalLocators)
		}
	}
	// Maintenance schedule questions need full coverage.
	if !byID["tiny-004"].FullCoverage {
		t.Fatalf("tiny-004: full coverage expected, recall=%.2f", byID["tiny-004"].Recall)
	}
	// No-answer questions are recorded with their scores. The retrieval layer
	// cannot decide abstention when the vector lane scores unrelated content
	// highly; that decision belongs to the agent layer (see
	// 05-evaluation-protocol.md). The harness must simply expose the signal.
	if byID["tiny-011"].TopScore <= 0 || byID["tiny-012"].TopScore <= 0 {
		t.Fatalf("no-answer questions must record their top scores: %v %v", byID["tiny-011"].TopScore, byID["tiny-012"].TopScore)
	}

	outDir := t.TempDir()
	if err := WriteReport(report, questions, outDir); err != nil {
		t.Fatalf("write report: %v", err)
	}
	for _, name := range []string{"manifest.json", "questions.jsonl", "traces.jsonl", "retrieval_metrics.json", "report.json", "report.md"} {
		if _, err := os.Stat(filepath.Join(outDir, name)); err != nil {
			t.Fatalf("missing report file %s", name)
		}
	}
	if report.Summary.EvidenceRecall < 0.5 {
		t.Fatalf("overall recall %.2f is below the toy gate", report.Summary.EvidenceRecall)
	}
	// Multi-hop evidence needs the hybrid lane; the evidence arm is expected to
	// miss the cross-document hop (that gap is what Phase 7 measures).
	if byID["tiny-006"].FullCoverage {
		t.Logf("tiny-006 multi-hop fully covered even in evidence mode")
	}
}

func TestTinyFixtureHybridRuns(t *testing.T) {
	ctx := context.Background()
	fixture, err := LoadFixture(ctx, fixtureDir(t))
	if err != nil {
		t.Fatalf("load fixture: %v", err)
	}
	questions, err := LoadGoldSet(filepath.Join(fixtureDir(t), "gold.jsonl"))
	if err != nil {
		t.Fatalf("load gold set: %v", err)
	}
	report, err := Run(ctx, fixture, questions, Options{
		Mode: "hybrid", TopK: 10, Label: "hybrid-b",
	})
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if report.Summary.EvidenceRecall < 0.5 {
		t.Fatalf("hybrid overall recall %.2f is below the toy gate", report.Summary.EvidenceRecall)
	}
	byID := map[string]QuestionResult{}
	for _, result := range report.PerQuestion {
		byID[result.QuestionID] = result
	}
	if !byID["tiny-006"].FullCoverage {
		t.Fatalf("hybrid tiny-006 multi-hop: full coverage expected, recall=%.2f", byID["tiny-006"].Recall)
	}
	if !byID["tiny-008"].FullCoverage {
		t.Fatalf("hybrid tiny-008 temporal: full coverage expected, recall=%.2f", byID["tiny-008"].Recall)
	}
}
