package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/shutu-ai/shutu-knowledge/internal/eval"
)

// cmdEval replays a frozen gold set through the shared query layer for one or
// more retrieval arms and writes the report layout from plan §12.6 plus a
// pairwise comparison. The harness builds its own isolated fixture knowledge
// base, so this command never touches the default data home.
func cmdEval(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("eval", flag.ContinueOnError)
	fixtureDir := fs.String("fixture", "evals/tiny-fixture", "directory with corpus/ and gold.jsonl")
	arms := fs.String("arms", "evidence", "comma-separated retrieval arms, for example evidence,semantic,hybrid")
	topK := fs.Int("top-k", 10, "retrieval depth for recall computation")
	outDir := fs.String("out", "", "report output root (default .tmp/eval)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	armList := strings.Split(*arms, ",")
	for _, arm := range armList {
		switch strings.TrimSpace(arm) {
		case "evidence", "semantic", "hybrid", "auto":
		default:
			return fmt.Errorf("unsupported arm %q", arm)
		}
	}
	if *outDir == "" {
		*outDir = ".tmp/eval"
	}
	questions, err := eval.LoadGoldSet(filepath.Join(*fixtureDir, "gold.jsonl"))
	if err != nil {
		return err
	}
	fixture, err := eval.LoadFixture(ctx, *fixtureDir)
	if err != nil {
		return err
	}
	reports := make([]eval.RunReport, 0, len(armList))
	for _, arm := range armList {
		arm = strings.TrimSpace(arm)
		report, err := eval.Run(ctx, fixture, questions, eval.Options{
			Mode: arm, TopK: *topK, Label: arm,
		})
		if err != nil {
			return err
		}
		reportDir := filepath.Join(*outDir, arm)
		if err := eval.WriteReport(report, questions, reportDir); err != nil {
			return err
		}
		fmt.Printf("arm %s: questions=%d evidence_recall=%.3f full_coverage=%.3f abstain_accuracy=%.3f answer_accuracy=%.3f\n",
			arm, report.GoldQuestions, report.Summary.EvidenceRecall,
			report.Summary.FullCoverageRate, report.Summary.AbstainAccuracy,
			report.Summary.AnswerAccuracy)
		reports = append(reports, *report)
	}
	if len(reports) >= 2 {
		base := reports[0]
		allComparisons := make([]eval.ArmComparison, 0)
		for _, arm := range reports[1:] {
			allComparisons = append(allComparisons, eval.Compare(base, arm)...)
		}
		encoded, err := json.MarshalIndent(allComparisons, "", "  ")
		if err != nil {
			return err
		}
		comparisonPath := filepath.Join(*outDir, "comparisons.json")
		if err := os.WriteFile(comparisonPath, encoded, 0o644); err != nil {
			return err
		}
		fmt.Printf("comparisons: %s (%d deltas)\n", comparisonPath, len(allComparisons))
	}
	return nil
}