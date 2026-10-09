# Retrieval Eval Report — hybrid

- Mode: **hybrid** · Top-K: 10 · Questions: 12
- Version: 0.7.0 · Commit:  · Timestamp: 2026-10-09T23:21:27Z

| Metric | Value |
|---|---|
| Evidence recall | 0.833 |
| Full required-evidence coverage | 0.833 |
| Abstain accuracy | 0.833 |
| Answer accuracy (retrieval-level) | 0.833 |
| Mean latency (ms) | 0.0 |
| p95 latency (ms) | 0 |
| Degraded runs | 0 |

## By category

| Category | Questions | Recall | Correct |
|---|---|---|---|
| conflict_exception | 1 | 1.000 | 1.000 |
| cross_document | 2 | 1.000 | 1.000 |
| exact_fact | 3 | 1.000 | 1.000 |
| multi_hop | 2 | 1.000 | 1.000 |
| no_answer | 2 | 0.000 | 0.000 |
| temporal | 2 | 1.000 | 1.000 |

## Failures

- **tiny-011** (no_answer): matched 0/0 locators, abstained=false, expected_abstain=true, degraded=false
- **tiny-012** (no_answer): matched 0/0 locators, abstained=false, expected_abstain=true, degraded=false
