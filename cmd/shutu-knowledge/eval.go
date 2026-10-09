package main

import (
	"context"
	"flag"
	"fmt"

	"github.com/shutu-ai/shutu-knowledge/internal/eval"
)

// cmdEval replays a frozen gold set through the shared query layer and writes
// the report layout from plan §12.6. The harness builds its own isolated
// fixture knowledge base, so this command never touches the default data home.
func cmdEval(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("eval", flag.ContinueOnError)
	fixtureDir := fs.String("fixture", "evals/tiny-fixture", "directory with corpus/ and gold.jsonl")
	mode := fs.String("mode", "evidence", "retrieval lane for the whole run: evidence, semantic, hybrid, or auto")
	topK := fs.Int("top-k", 10, "retrieval depth for recall computation")
	label := fs.String("label", "", "arm label recorded in the report (defaults to the mode)")
	outDir := fs.String("out", "", "report output directory (default .tmp/eval/<mode>)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *label == "" {
		*label = *mode
	}
	if *outDir == "" {
		*outDir = ".tmp/eval/" + *mode
	}
	questions, err := eval.LoadGoldSet(fmt.Sprintf("%s/gold.jsonl", *fixtureDir))
	if err != nil {
		return err
	}
	fixture, err := eval.LoadFixture(ctx, *fixtureDir)
	if err != nil {
		return err
	}
	report, err := eval.Run(ctx, fixture, questions, eval.Options{
		Mode: *mode, TopK: *topK, Label: *label, OutDir: *outDir,
	})
	if err != nil {
		return err
	}
	if err := eval.WriteReport(report, questions, *outDir); err != nil {
		return err
	}
	fmt.Printf("eval %s: questions=%d evidence_recall=%.3f full_coverage=%.3f abstain_accuracy=%.3f answer_accuracy=%.3f\n",
		*label, report.GoldQuestions, report.Summary.EvidenceRecall,
		report.Summary.FullCoverageRate, report.Summary.AbstainAccuracy,
		report.Summary.AnswerAccuracy)
	fmt.Printf("report: %s\n", *outDir)
	return nil
}