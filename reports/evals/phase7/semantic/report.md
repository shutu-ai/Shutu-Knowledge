# Retrieval Eval Report — semantic

- Mode: **semantic** · Top-K: 10 · Questions: 12
- Version: 0.7.0 · Commit:  · Timestamp: 2026-10-09T23:21:27Z

| Metric | Value |
|---|---|
| Evidence recall | 0.000 |
| Full required-evidence coverage | 0.000 |
| Abstain accuracy | 0.833 |
| Answer accuracy (retrieval-level) | 0.000 |
| Mean latency (ms) | 0.0 |
| p95 latency (ms) | 0 |
| Degraded runs | 0 |

## By category

| Category | Questions | Recall | Correct |
|---|---|---|---|
| conflict_exception | 1 | 0.000 | 0.000 |
| cross_document | 2 | 0.000 | 0.000 |
| exact_fact | 3 | 0.000 | 0.000 |
| multi_hop | 2 | 0.000 | 0.000 |
| no_answer | 2 | 0.000 | 0.000 |
| temporal | 2 | 0.000 | 0.000 |

## Failures

- **tiny-001** (exact_fact): matched 0/1 locators, abstained=false, expected_abstain=false, degraded=false
- **tiny-002** (exact_fact): matched 0/1 locators, abstained=false, expected_abstain=false, degraded=false
- **tiny-003** (exact_fact): matched 0/1 locators, abstained=false, expected_abstain=false, degraded=false
- **tiny-004** (cross_document): matched 0/2 locators, abstained=false, expected_abstain=false, degraded=false
- **tiny-005** (cross_document): matched 0/2 locators, abstained=false, expected_abstain=false, degraded=false
- **tiny-006** (multi_hop): matched 0/2 locators, abstained=false, expected_abstain=false, degraded=false
- **tiny-007** (multi_hop): matched 0/1 locators, abstained=false, expected_abstain=false, degraded=false
- **tiny-008** (temporal): matched 0/3 locators, abstained=false, expected_abstain=false, degraded=false
- **tiny-009** (temporal): matched 0/1 locators, abstained=false, expected_abstain=false, degraded=false
- **tiny-010** (conflict_exception): matched 0/1 locators, abstained=false, expected_abstain=false, degraded=false
- **tiny-011** (no_answer): matched 0/0 locators, abstained=false, expected_abstain=true, degraded=false
- **tiny-012** (no_answer): matched 0/0 locators, abstained=false, expected_abstain=true, degraded=false
