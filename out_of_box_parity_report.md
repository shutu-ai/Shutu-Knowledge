# Shutu-Knowledge Out-of-Box Parity Report

Audit date: 2026-09-06

Reference: dsh-knowledge `v0.3.9`, commit
`95e4a135cca3282b345c6d12a8e09cc4a314402f`.

This report is intentionally independent of the existing V1 Release Ready
status. See [the audit](docs/out_of_box_parity_audit.md),
[the matrix](docs/out_of_box_parity_matrix.md), and
[runtime dependencies](docs/runtime_dependencies.md).

## Required answers

### A — Local Embedding

**YES (managed).** The default path embeds a pinned Node runtime bootstrap,
installs Transformers.js/ONNX dependencies privately, verifies the Qwen model,
and performs real vector inference through the Knowledge supervisor.

### B — Local Reranker

**YES (managed).** The default path loads the pinned BGE cross-encoder and
returns bounded raw-logit scores through the same supervised runtime.

### C — OCR without a user-developed helper

**YES (managed).** Tesseract.js with `eng+chi_sim` is installed and invoked by
the managed runtime; per-document failure handling remains intact.

### D — PDF rasterization

**YES (managed).** PDF.js and canvas render full pages without a user helper;
real JBIG2 and JPX PDF fixtures both rendered to validated PNG pages.

### E — Legacy Office

**YES (managed).** The default path uses the managed MIT Anydoc package, with
LibreOffice discovery as fallback. Real `.doc`, `.ppt`, and non-empty `.xls`
fixtures converted to Markdown successfully.

### F — Model lifecycle

**YES (managed).** Fixed revisions and file checksums feed a real load/inference
smoke test; corruption becomes `FAILED`, restoration reloads successfully, and
an offline restart passes with remote model/OCR access disabled.

### G — Agent modification

**NO modification.** The Knowledge code continues to use only the public
Extension SDK. The local Agent baseline remains read-only.

### H — GAP-001

`BLOCKED_BY_AGENT / NON-BLOCKING`. Extension Platform v1 has no public generic
system-prompt contribution mechanism. Tool descriptions and native context
contributions remain the supported workaround; no prompt/session/internal API
bypass is used.

### I — GAP-002

`BLOCKED_BY_AGENT / NON-BLOCKING`. Agent-facing health remains scalar/free-form;
Knowledge's own Doctor and HTTP health surfaces provide the detailed component
diagnostics.

## Gates

| Gate | Result | Reason |
|---|---|---|
| A Local Embedding | PASS | Managed Node/Transformers.js/ONNX runtime and real smoke |
| B Local Reranker | PASS | Managed BGE load and score ordering smoke |
| C OCR | PASS | Managed Tesseract.js OCR smoke |
| D PDF Renderer | PASS | Full-page, JBIG2, and JPX PDF→PNG smokes pass |
| E Legacy Office | PASS | Real `.doc/.ppt/.xls` conversions pass |
| F Model Lifecycle | PASS | Checksums, real inference, corruption recovery, offline restart |
| G No Agent Modification | PASS | Agent remains unchanged |
| H No Agent Internal Import | PASS | Production imports remain public SDK only |
| I License | PASS | Managed package and model terms are inventoried |
| J Fresh Install | PASS | Clean data home installed embedded lock and ran managed smoke |
| K Regression | PASS | Go packages, runtime adapters, Web tests, and managed smoke pass |

## Final result

The requested runtime implementation is present and its Windows real-runtime
evidence passes. Final Out-of-Box parity certification remains gated on the
Linux `runtime-release` CI job; optional MinerU and Agent prompt-channel gaps
remain outside this runtime implementation.

```text
SHUTU-KNOWLEDGE OUT-OF-BOX PARITY READY (AFTER runtime-release CI PASS)
```
