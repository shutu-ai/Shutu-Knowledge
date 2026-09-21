# Local model runtime at real corpus scale (v0.6.3 candidate)

Date: 2026-09-22
Scope: local embedding and reranker runtime on the 50-workbook SmartCare data-dictionary corpus. This document contains aggregate evidence only; raw workbooks and query answers remain private.

## Executive result

**Runtime gate: substantially improved and able to complete full-corpus indexing and reranking.
Overall validation classification: WEAK.**

The prior Qwen3-0.6B path could not complete real-scale work without extreme CPU/memory pressure. A much smaller pinned embedding model, bounded ONNX threading, primary-key vector persistence, top-K vector reads, a quantized reranker, and a top-K rerank window allow the full corpus to complete and search to run. However, semantic reverse lookup, cross-workbook discovery, analysis, and join reasoning remain weak. The release is therefore not classified ACCEPTABLE or STRONG and must not be tagged.

## Final model configuration

| Component | Setting |
|---|---|
| Embedding model | `Xenova/all-MiniLM-L6-v2@751bff37182d3f1213fa05d7196b954e230abad9` |
| Embedding batch | 16 |
| Embedding token cap | 512 |
| ONNX intra-op threads | 4 |
| ONNX inter-op threads | 1 |
| ONNX execution | sequential |
| Managed process retirement | after 64 successful requests |
| Reranker | `Xenova/bge-reranker-base`, q4 ONNX |
| Rerank window | top 20 final-sized candidates, micro-batched by 8 |
| Search deadline | 25 s |
| Scheduler | model=1, maxPerBase=1 |

## Root causes and resolution

| Layer | Evidence | Change |
|---|---|---|
| Model scale | Qwen3-0.6B reached ~1.84 GB RSS and ~1216% CPU with default ONNX threads; single-thread was stable but ~1 item/s. | Use a much smaller pinned MiniLM model; do not mask the issue with a larger model. |
| ONNX oversubscription | Default threading oversubscribed the host. | Pin 4 intra-op / 1 inter-op / sequential execution. |
| Tensor lifetime | Output tensors could persist until GC. | Explicitly dispose embedding and rerank outputs. |
| Process lifetime | Long-lived managed workers accumulated native pressure. | Retire after bounded successful requests. |
| Vector write | Updating 19,749 reused rows by `(doc, generation, hash)` exceeded the writer budget; the old degraded path preserved the old active index. | Skip rows whose vectors were carried into the staged generation; write remaining vectors by chunk primary key in bounded batches; retry one idempotent batch after `ErrWriteUnknown` when the caller remains active. |
| Vector read | Scoring returned every row's text/context and allocated a query vector per row. | Two-pass top-K scan: score embeddings first, then load only the winners. |
| Reranker batch | A fp32 60-candidate cross-encoder request repeatedly exceeded 8-20 s deadlines. | q4 model, 8-item micro-batches, and rerank only the final-sized top-K after broad recall. |
| Short-token lexical lane | LIKE-only queries selected/ordered an FTS-only BM25 expression and failed. | Use BM25/FTS join only for MATCH queries; short-token scans use a plain chunks scan. |

## Full-corpus embedding reconciliation

| Metric | Value |
|---|---:|
| Active real-corpus chunks | 154,475 |
| Embedded active chunks | 154,475 |
| Missing vectors | 0 |
| Largest workbook chunks | 64,268 |
| Largest workbook embedded | 64,268 |

The final successful largest-document generation completed in **1,446.659 s** and atomically switched to `ready`. During a deliberate cancellation of an earlier generation, the active generation remained the previous 19,749-vector authoritative index while the staged generation retained safe completed progress; restart recovered cleanly and a later run completed. This is real recovery evidence, but it is not three complete clean reruns from scratch.

Observed application peak RSS during the successful indexing path was **~2.35 GB**, down from the ~3.2 GB Qwen3 attempts. The benchmark did not yet provide a complete native/Go heap/model tensor memory breakdown or sustained CPU flamegraph, so memory and CPU acceptance are reported as bounded but not exhaustively proven.

## Search latency (100 representative queries per mode)

Queries are generic dictionary probes; no private response text is published. Errors and timeouts are HTTP/API failures; zero hit is a retrieval outcome, not an error.

| Mode | Success | Errors | Timeouts | Zero hit | p50 | p95 | p99 |
|---|---:|---:|---:|---:|---:|---:|---:|
| BM25 | 100/100 | 0 | 0 | 4 | 1,118 ms | 5,030 ms | 5,244 ms |
| Vector | 100/100 | 0 | 0 | 35 | 2,257 ms | 4,759 ms | 5,606 ms |
| Hybrid, reranker off | 100/100 | 0 | 0 | 4 | 3,061 ms | 5,789 ms | 6,679 ms |
| Hybrid + reranker | 100/100 | 0 | 0 | 4 | 10,234 ms | 12,536 ms | 12,967 ms |

The reranker applied to **100/100** eligible searches. Candidate-level rerank latency was p50 **7,468 ms** and p95 **7,815 ms**.

## Excel QA (150 questions)

Mix: 50 exact lookup, 30 semantic reverse, 30 cross-workbook, 20 analysis, 20 join/data-model. Evaluation was retrieval/evidence based; it did not ask a model to invent SQL or assert unobserved joins. Thus generated hallucination counts are zero by construction, not evidence of LLM reasoning quality.

### All-question result

| Mode | Success | Errors | Zero hit | Query evidence found | Citation present | p50 | p95 |
|---|---:|---:|---:|---:|---:|---:|---:|
| BM25 | 150/150 | 0 | 14 | 59 | 136 | 886 ms | 5,229 ms |
| Hybrid | 150/150 | 0 | 8 | 56 | 142 | 2,772 ms | 6,084 ms |
| Hybrid + reranker | 150/150 | 0 | 8 | 56 | 142 | 10,060 ms | 12,236 ms |

### Category highlights

| Category | BM25 found | Hybrid found | Hybrid + reranker found | At least 2 workbooks: BM25 / hybrid / rerank |
|---|---:|---:|---:|---:|
| Exact lookup | 50 / 50 | 46 / 50 | 46 / 50 | 37 / 42 / 36 |
| Semantic reverse | 7 / 30 | 7 / 30 | 7 / 30 | 16 / 20 / 20 |
| Cross-workbook | 0 / 30 | 0 / 30 | 0 / 30 | 15 / 19 / 16 |
| Analysis | 2 / 20 | 3 / 20 | 3 / 20 | 10 / 19 / 14 |
| Join/data-model | 0 / 20 | 0 / 20 | 0 / 20 | 11 / 14 / 11 |

The evaluator recorded candidate join evidence for only **3 / 20** join queries in every mode. It found no unsupported join claims because the QA harness did not generate join assertions.

## What passed

- All 154,475 active chunks have the active model-space vector.
- Full largest-document reindex completed and atomically switched.
- 100/100 BM25, vector, hybrid, and hybrid+reranker latency runs returned without HTTP errors or deadline failures.
- 100/100 eligible hybrid searches applied the local reranker.
- The 150-question QA completed without search errors.
- Cancellation kept the old active index usable and restart resumed recovery.

## What remains weak

1. Embeddings did not materially improve semantic reverse lookup: only 7/30 in all modes.
2. Cross-workbook queries produced no reliable cross-workbook concept answer evidence in 30/30 cases.
3. Join/data-model evidence remains sparse: 3/20.
4. Analysis evidence is weak: 2-3/20.
5. Hybrid ranking reduced exact-query evidence from 50/50 lexical to 46/50; reranking did not restore it.
6. The strict runtime suite still lacks three complete clean embedding runs, a formal background-embedding/interactive-search contention run, and full native-memory breakdown.

## Conclusion

The original runtime blocker no longer prevents full-corpus completion: local embedding and reranking are now bounded enough to complete and benchmark at the real scale. But the completed QA demonstrates that structured Excel retrieval is still not robust data-model knowledge. Final classification remains **WEAK**; do not tag or release v0.6.3.
