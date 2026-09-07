# Runtime Dependencies

This document states what a fresh Shutu-Knowledge installation actually ships.
It is intentionally separate from the V1 release license report. Knowledge
automatically prepares a private Node.js runtime under its own data home and
installs the pinned package lock there; users do not write or configure a
helper process.

| Capability | Built into main binary | Bundled runtime | Auto-managed runtime | Current requirement | License audit status |
|---|---:|---:|---:|---|---|
| Lexical retrieval and supported PDF/text parsing | Yes | N/A | N/A | None | PASS: project Apache-2.0 and Go notices apply |
| Local embedding | No | No | Yes: Node.js + transformers.js + ONNX Runtime | Pinned Qwen3 ONNX model downloaded on first inference | AUTO-MANAGED; real smoke passed |
| Local reranker | No | No | Yes: Node.js + transformers.js + ONNX Runtime | Pinned BGE cross-encoder downloaded on first load | AUTO-MANAGED; real smoke passed |
| OCR recognition | No | No | Yes: Tesseract.js worker and language data | `eng+chi_sim` data downloaded on first OCR | AUTO-MANAGED; real OCR passed |
| Full-page PDF rendering | No | No | Yes: PDF.js + napi-rs canvas | None beyond pinned npm runtime | AUTO-MANAGED; real page render passed |
| JBIG2 / JPX | Via managed PDF.js renderer | No | Yes: PDF.js decoder path | Renderer capability | AUTO-MANAGED; real fixtures passed |
| `.doc` / `.ppt` / `.xls` | No | No | Yes: Anydoc native package | None; LibreOffice is optional fallback | AUTO-MANAGED; real fixtures passed |
| Optional MinerU processing | No | No | No | Operator-selected remote service and credentials | Separate service terms apply |

## Model lifecycle contract

The product distinguishes:

`NOT_INSTALLED → DOWNLOADING → VERIFYING → INSTALLED → LOADING → READY`,
with `RUNTIME_MISSING` and `FAILED` as diagnostic terminal states. `READY`
requires a valid artifact set, a live runtime, successful model loading, and a
minimum inference smoke test. The managed runtime persists its state below
`<data-home>/runtime/runtime-state.json` and keeps model files in its private
cache, so a restart can reload without downloading again.

## Packaging decision

The Node.js runtime package and its lockfile are embedded in the Knowledge
binary. Platform Node archives use fixed official URLs and SHA-256 checksums;
model files use fixed upstream revisions and SHA-256 checksums before the
runtime records `READY`. The runtime is therefore `AUTO_MANAGED_EXTERNAL_RUNTIME`
for supported Windows amd64 and Linux amd64 installations. LibreOffice remains
an optional detected fallback; the default legacy path uses the managed Anydoc
package.

The Agent is not a runtime dependency owner. All future runtime management must
remain in the Knowledge data and process domain and must not modify
`shutu-agent` or import `shutu-agent/internal/...`.
