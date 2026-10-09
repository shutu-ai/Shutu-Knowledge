package eval

import (
	"fmt"
	"sort"
)

// ArmComparison is one pairwise arm delta for one metric.
type ArmComparison struct {
	Base    string  `json:"base"`
	Arm     string  `json:"arm"`
	Metric  string  `json:"metric"`
	BaseVal float64 `json:"base_value"`
	ArmVal  float64 `json:"arm_value"`
	Delta   float64 `json:"delta"`
	// Relative is Delta/BaseVal; it is 0 when BaseVal is 0 (absolute delta
	// only, never a fake percentage of nothing).
	Relative float64 `json:"relative,omitempty"`
}

// Compare produces pairwise metric deltas between a baseline arm and one
// comparison arm. Small samples must be labeled insignificant by the caller;
// the comparison itself never hides the sample size.
func Compare(base, arm RunReport) []ArmComparison {
	perCategoryBase := categoryIndex(base)
	perCategoryArm := categoryIndex(arm)
	comparisons := []ArmComparison{
		metricDelta(base, arm, "evidence_recall", base.Summary.EvidenceRecall, arm.Summary.EvidenceRecall),
		metricDelta(base, arm, "full_coverage_rate", base.Summary.FullCoverageRate, arm.Summary.FullCoverageRate),
		metricDelta(base, arm, "abstain_accuracy", base.Summary.AbstainAccuracy, arm.Summary.AbstainAccuracy),
		metricDelta(base, arm, "answer_accuracy", base.Summary.AnswerAccuracy, arm.Summary.AnswerAccuracy),
		metricDelta(base, arm, "p95_latency_ms", float64(base.Summary.P95LatencyMS), float64(arm.Summary.P95LatencyMS)),
	}
	categories := make([]string, 0, len(perCategoryBase))
	for category := range perCategoryBase {
		categories = append(categories, category)
	}
	sort.Strings(categories)
	for _, category := range categories {
		baseRow := perCategoryBase[category]
		armRow, ok := perCategoryArm[category]
		if !ok {
			comparisons = append(comparisons, ArmComparison{
				Base: base.Label, Arm: arm.Label,
				Metric:  fmt.Sprintf("recall[%s]", category),
				BaseVal: baseRow.EvidenceRecall,
			})
			continue
		}
		comparisons = append(comparisons, metricDelta(base, arm,
			fmt.Sprintf("recall[%s]", category),
			baseRow.EvidenceRecall, armRow.EvidenceRecall))
	}
	return comparisons
}

func metricDelta(base, arm RunReport, metric string, baseVal, armVal float64) ArmComparison {
	delta := armVal - baseVal
	relative := 0.0
	if baseVal != 0 {
		relative = delta / baseVal
	}
	return ArmComparison{
		Base: base.Label, Arm: arm.Label, Metric: metric,
		BaseVal: baseVal, ArmVal: armVal, Delta: delta, Relative: relative,
	}
}

func categoryIndex(report RunReport) map[string]CategoryRow {
	out := map[string]CategoryRow{}
	for _, row := range report.Summary.ByCategory {
		out[row.Category] = row
	}
	return out
}