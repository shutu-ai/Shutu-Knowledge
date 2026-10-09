package eval

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

// gitCommit is injected at build time when available; tests and local runs
// fall back to the environment or leave it empty rather than guessing.
var gitCommit = func() (string, error) {
	return os.Getenv("SHUTU_EVAL_GIT_COMMIT"), nil
}

// WriteReport freezes one run into the plan §12.6 layout under outDir.
func WriteReport(report *RunReport, questions []GoldQuestion, outDir string) error {
	if err := os.MkdirAll(outDir, 0o755); err != nil {
		return err
	}
	if err := writeJSON(filepath.Join(outDir, "manifest.json"), map[string]any{
		"label": report.Label, "mode": report.Mode, "top_k": report.TopK,
		"version": report.Version, "git_commit": report.GitCommit,
		"timestamp":      report.Timestamp,
		"gold_questions": report.GoldQuestions,
	}); err != nil {
		return err
	}
	if err := writeJSON(filepath.Join(outDir, "retrieval_metrics.json"), report.Summary); err != nil {
		return err
	}
	if err := writeJSON(filepath.Join(outDir, "report.json"), report); err != nil {
		return err
	}
	if err := writeJSONL(filepath.Join(outDir, "questions.jsonl"), questions); err != nil {
		return err
	}
	if err := writeJSONL(filepath.Join(outDir, "traces.jsonl"), report.PerQuestion); err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(outDir, "report.md"), []byte(renderMarkdown(report)), 0o644)
}

func renderMarkdown(report *RunReport) string {
	summary := report.Summary
	out := fmt.Sprintf(`# Retrieval Eval Report — %s

- Mode: **%s** · Top-K: %d · Questions: %d
- Version: %s · Commit: %s · Timestamp: %s

| Metric | Value |
|---|---|
| Evidence recall | %.3f |
| Full required-evidence coverage | %.3f |
| Abstain accuracy | %.3f |
| Answer accuracy (retrieval-level) | %.3f |
| Mean latency (ms) | %.1f |
| p95 latency (ms) | %d |
| Degraded runs | %d |

## By category

| Category | Questions | Recall | Correct |
|---|---|---|---|
`,
		report.Label, report.Mode, report.TopK, report.GoldQuestions,
		report.Version, report.GitCommit, report.Timestamp,
		summary.EvidenceRecall, summary.FullCoverageRate, summary.AbstainAccuracy,
		summary.AnswerAccuracy, summary.MeanLatencyMS, summary.P95LatencyMS,
		summary.DegradedRuns)
	for _, row := range summary.ByCategory {
		out += fmt.Sprintf("| %s | %d | %.3f | %.3f |\n",
			row.Category, row.Questions, row.EvidenceRecall, row.CorrectRate)
	}
	out += "\n## Failures\n\n"
	failures := 0
	for _, result := range report.PerQuestion {
		if result.Correct {
			continue
		}
		failures++
		out += fmt.Sprintf("- **%s** (%s): matched %d/%d locators, abstained=%t, expected_abstain=%t, degraded=%t",
			result.QuestionID, result.Category, result.MatchedLocators, result.TotalLocators,
			result.Abstained, result.ExpectedAbstain, result.Degraded)
		if result.Error != "" {
			out += ", error=" + result.Error
		}
		out += "\n"
	}
	if failures == 0 {
		out += "- none\n"
	}
	return out
}

func writeJSON(path string, value any) error {
	encoded, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, encoded, 0o644)
}

func writeJSONL(path string, values any) error {
	file, err := os.Create(path)
	if err != nil {
		return err
	}
	defer file.Close()
	encoder := json.NewEncoder(file)
	return encoder.Encode(values)
}
