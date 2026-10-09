# Evaluation Protocol and 200Q Gold Set

Status: **Phase 6 complete — harness, tiny fixture, and annotation spec shipped.**

## Harness

`internal/eval` + `shutu-knowledge eval` replay a frozen gold set through the
shared `agentquery` layer (never a forked retrieval path) and write the plan
§12.6 report layout:

```text
manifest.json  questions.jsonl  traces.jsonl
retrieval_metrics.json  report.json  report.md
```

The harness is deterministic and LLM-free: identical fixture + code produce
identical metrics, so any change is a real retrieval change. The tiny fixture
(`evals/tiny-fixture`, fully synthetic FlowPump X200 corpus) reproduces from a
clean checkout:

```bash
shutu-knowledge eval --fixture evals/tiny-fixture --mode evidence --out .tmp/eval/evidence
shutu-knowledge eval --fixture evals/tiny-fixture --mode hybrid  --out .tmp/eval/hybrid
```

`go test ./internal/eval/` is the CI gate: exact-fact and multi-hop questions
must fully cover gold evidence, and no-answer questions must expose their scores.

## Finding already shipped with the harness

The first harness run exposed a real lexical-lane defect: single/double-character
words (for example "do") applied an AND LIKE filter that silently zeroed whole
queries. Fixing it restored full recall for cross-document and temporal queries
and required an explicit, reason-recorded rebaseline of the 0.2 token baseline
(`docs/0.4_benchmark_0.2_baseline.json`, `rebaseline.reason`; the benchmark test
fails without a recorded reason).

## Retrieval-layer metrics (implemented)

| Metric | Definition |
|---|---|
| Evidence Recall@K | matched gold locators / total gold locators, K = top-k |
| Full coverage rate | questions whose entire required-evidence set was retrieved |
| Abstain accuracy | questions where the harness observed zero-score (no-evidence) retrieval matching `expected_abstain` |
| Answer accuracy (retrieval-level) | full-coverage or correct abstention |
| Latency | per-question mean and p95 |
| Degraded runs | runs where a lane reported degraded diagnostics |

## Agent-layer metrics (Phase 7)

Answer correctness, citation faithfulness, citation resolution, multi-hop
completion, false relation activation, temporal correctness, abstention quality,
tool-use efficiency, and cost require a real agent answering with the tools; the
harness intentionally does not synthesize them.

## 200Q gold set annotation spec

Category quotas (plan §12.3): exact parameter/error 30 · cross-chapter/document 35 ·
multi-hop/conditional 35 · temporal/version 30 · structured tables/fields 25 ·
conflict/negation/exception 25 · no-answer/adversarial 20.

Each entry carries: `question_id`, `question`, `language`, `base_ids`,
`version_policy`, `gold_answer`, `required_claims`, `gold_evidence_locators`
(document + distinctive needle), `forbidden_claims`, `ambiguity`,
`expected_abstain`, `category`, `difficulty`, `reviewer`, `gold_version`.
Validation (`eval.LoadGoldSet`) enforces: answerable questions carry evidence and
a gold answer; abstain questions carry no gold evidence; IDs are unique.

Rules:

1. Corpus is licensed/user-owned technical documentation only; no private or
   production data is committed. Private benchmarks live in local eval profiles
   outside Git (`.gitignore` + hashed manifests).
2. Gold evidence needs a human reviewer per entry; disagreements go to blinded
   second review.
3. Every run freezes manifest (commit, versions, dataset hash), config, traces,
   metrics, comparisons, `report.md`, and `failures.md` under
   `reports/evals/<run-id>/`.
4. Statistical reporting must include pairwise deltas with sample sizes and
   bootstrap confidence intervals; small-sample results are labeled not
   significant rather than celebrated.
5. Thresholds are pre-registered (§12.5) before a run and never back-filled.
6. A frozen judge configuration is required for any model-assisted scoring;
   deterministic locator checks always run first.