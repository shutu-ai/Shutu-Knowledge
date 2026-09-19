# Real-World Knowledge Internalization Validation Harness

This directory is a fixed, reproducible harness for validating 0.4 on real
documents. It does **not** commit corpus content.

## What is committed

- `corpus_manifest.json`: corpus identities, public sources, and local paths
- `queries.jsonl`: fixed query definitions, expected documents/terms
- `schema.json`: payload schema
- `results/`: machine-readable aggregate and per-query results
- `main.go`: the harness itself

## What is not committed

Corpus files are stored under the ignored `.local/validation-corpus` tree. No
private document text, snippet, or raw knowledge unit is committed.

## Run

```powershell
$env:GOCACHE = "C:\dev-projects\shutu-knowledge\.gocache"
go run ./benchmarks/real_world_internalization
```

The harness creates a fresh database per run inside `.tmp` and writes:

- `benchmarks/real_world_internalization/results/latest.json`
- `benchmarks/real_world_internalization/results/aggregate.json`

## Evaluation model

The current run is a deterministic context-support proxy, not a human or LLM
answer judge. Each query is scored from:

- expected answer-term coverage,
- expected document/title coverage,
- citation coverage,
- context tokens,
- latency.

0.3 Baseline is evidence-only hybrid retrieval. 0.4 is `CompileKnowledgeContext`
(AUTO routing + semantic memory + evidence). Both paths use the same query,
top-k, token cap, and corpus.

## Validation result

The current result is **ACCEPTABLE**. See
`docs/0.4_real_world_validation.md`.
