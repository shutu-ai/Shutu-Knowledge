# Shutu Knowledge v0.6.3 Release Notes

v0.6.3 is a **maintenance release focused on local embedding and reranker
runtime stability at real corpus scale**. It contains no new semantic or
data-model capabilities.

This release stabilizes local embedding and reranker runtime at real corpus
scale.

Structured Excel data-model semantic reasoning remains under validation and is
not claimed production-ready in v0.6.3.

## Local model runtime stability

- Use a pinned, smaller local embedding model so a real 154,475-chunk corpus
  can complete embedding without the prior runaway CPU/memory behavior.
- Bound ONNX execution to four intra-op threads and one inter-op thread.
- Explicitly dispose embedding and reranker output tensors so native memory is
  not retained until an unpredictable garbage-collection cycle.
- Retire managed model worker processes after 64 successful requests to bound
  native allocator growth while amortizing model startup.

## Bounded reranking and search

- Broad retrieval still builds recall candidates, but the cross-encoder now
  reranks only the final-sized top-K head.
- Local reranker requests use a quantized model and eight-item micro-batches.
- The default search deadline is 25 seconds, with bounded model-slot
  concurrency and queueing.

## Vector persistence at scale

- Reused vectors carried into a staged generation no longer trigger redundant
  large updates.
- Remaining vectors are written by chunk primary key in bounded batches.
- A single idempotent batch retry handles an uncertain write outcome only when
  the caller remains active.
- Vector scoring uses a two-pass top-K scan: score stored vectors first, then
  load text/context only for the winning candidates.

## Cancellation, restart, and coexistence

- Cancellation keeps the previously active generation authoritative and
  searchable while preserving safe staged progress.
- Startup recovery explicitly resolves interrupted generations.
- Background embedding and interactive search were exercised together:
  20/20 foreground searches passed while a large reindex ran.
- A synthetic mixed-token-length runtime load test covers cancellation and
  retry, including atomic publication of the retried generation.

## Real-scale runtime evidence

On the 50-workbook, 2,109-sheet corpus:

- 154,475 / 154,475 active chunks embedded; missing vectors: 0.
- Largest workbook: 64,268 / 64,268 chunks embedded.
- BM25: 100/100; Vector: 100/100; Hybrid: 100/100; Hybrid + reranker: 100/100.
- Search HTTP timeout/error count: 0.
- Reranker applied: 100/100 eligible searches.
- Three repeated representative runs passed with no monotonic memory growth.
- Offline restart and recovery passed.

## Limitation

The same corpus still shows weak Excel semantic reverse lookup, cross-workbook
discovery, analysis, and join/data-model evidence. Those results motivate the
separate structured data-model work; they do not change this runtime-only
maintenance classification.
