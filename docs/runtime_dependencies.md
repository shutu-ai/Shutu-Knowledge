# Runtime Dependencies

This document states what a fresh Shutu-Knowledge installation actually ships.
It is intentionally separate from the V1 release license report.

| Capability | Built into main binary | Bundled runtime | Auto-managed runtime | Current requirement | License audit status |
|---|---:|---:|---:|---|---|
| Lexical retrieval and supported PDF/text parsing | Yes | N/A | N/A | None | PASS: project Apache-2.0 and Go notices apply |
| Local embedding | No | No | No | User-configured line-delimited JSON inference helper and model | NOT READY; no additional runtime bundled |
| Local reranker | No | No | No | User-configured line-delimited JSON inference helper and model | NOT READY; no additional runtime bundled |
| OCR recognition | No | No | No | User-configured OCR helper; downloaded PaddleOCR artifacts alone are insufficient | NOT READY; no additional runtime bundled |
| Full-page PDF rendering | No | No | No | User-configured renderer helper | NOT READY; no renderer redistributed |
| JBIG2 / JPX | Partial built-in PDF image handling | No | No | User-configured image decoder for unsupported codecs | NOT READY; no decoder redistributed |
| `.doc` / `.ppt` / `.xls` | No | No | No | User-configured legacy-office converter | NOT READY; no converter redistributed |
| Optional MinerU processing | No | No | No | Operator-selected remote service and credentials | Separate service terms apply |

## Model lifecycle contract

The product distinguishes:

`NOT_INSTALLED → DOWNLOADING → VERIFYING → INSTALLED → LOADING → READY`,
with `RUNTIME_MISSING` and `FAILED` as diagnostic terminal states. `READY`
requires a valid artifact set, a live runtime, successful model loading, and a
minimum inference smoke test. The model API now reports downloaded artifacts as
`INSTALLED` and `ready: false`; it does not claim that a directory of ONNX
files can infer.

## Packaging decision

No ONNX runtime, OCR engine, PDF renderer, Office converter, model weights, or
tokenizer is currently bundled or auto-installed by this repository. Therefore
the local-runtime parity result is `NOT READY`. A future runtime implementation
must add an installer/manifest, checksum and resume handling, platform assets,
doctor integration, and a license/weight-terms audit before changing that
classification.

The Agent is not a runtime dependency owner. All future runtime management must
remain in the Knowledge data and process domain and must not modify
`shutu-agent` or import `shutu-agent/internal/...`.
