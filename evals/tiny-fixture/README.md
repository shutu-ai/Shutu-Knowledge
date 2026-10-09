# Tiny Eval Fixture

A small, fully synthetic corpus (four markdown documents about a fictional
FlowPump X200) plus a 12-question gold set. No private or production data.

## Reproduce from a clean checkout

```bash
go build ./...
shutu-knowledge eval --fixture evals/tiny-fixture --mode evidence --out .tmp/eval-evidence
shutu-knowledge eval --fixture evals/tiny-fixture --mode hybrid  --out .tmp/eval-hybrid
```

Each run writes the frozen report layout (`manifest.json`, `questions.jsonl`,
`traces.jsonl`, `retrieval_metrics.json`, `report.json`, `report.md`).

## Gate

The harness test (`go test ./internal/eval/`) asserts: exact-fact and multi-hop
questions reach full required-evidence coverage, no-answer questions abstain,
and the report files exist. Retrieval is deterministic, so failures indicate a
real retrieval regression, not flakiness.