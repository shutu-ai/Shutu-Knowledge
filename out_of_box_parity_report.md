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

**NO.** Knowledge validates and supervises a configured helper, but the release
does not contain or automatically install a real embedding inference runtime.

### B — Local Reranker

**NO.** The provider and self-test are real integration surfaces, but inference
still requires a manually configured helper and model.

### C — OCR without a user-developed helper

**NO.** The PaddleOCR artifact workflow does not include the recognizer runtime;
OCR remains dependent on a deployment-supplied helper or fallback command.

### D — PDF rasterization

**NO.** Supported embedded image codecs are built in, but full-page rendering
for vector-only/scanned workflows requires a manually configured renderer.
JBIG2 and JPX require a manually configured decoder.

### E — Legacy Office

**NO.** `.doc`, `.ppt`, and `.xls` dispatch to a configured converter; no
automatic discovery, installation, or bundled converter is shipped.

### F — Model lifecycle

**NO.** Artifact download works, but download → validated load → inference →
restart is not a complete product-managed loop. The API now reports a complete
artifact set as `INSTALLED`, not `READY`.

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
| A Local Embedding | FAIL | Manual external inference runtime required |
| B Local Reranker | FAIL | Manual external inference runtime required |
| C OCR | FAIL | Manual external OCR runtime required |
| D PDF Renderer | FAIL | Manual external renderer required |
| E Legacy Office | FAIL | Manual external converter required |
| F Model Lifecycle | FAIL | No runtime load/inference readiness loop |
| G No Agent Modification | PASS | Agent remains unchanged |
| H No Agent Internal Import | PASS | Production imports remain public SDK only |
| I License | PASS for current package; parity runtimes not selected | No new runtime is redistributed |
| J Fresh Install | FAIL | Fresh install cannot complete local Embedding/OCR/Office/Reranker paths |
| K Regression | PASS | Current Go build/vet/test/race, Web typecheck/contract/build/Chrome E2E, and Extension integration suites pass after the lifecycle-state correction |

## Final result

The repository is V1 Release Ready, but the requested Out-of-Box runtime parity
is not complete because the local ML, OCR, PDF-rendering, and legacy Office
paths still require manual external runtime configuration.

```text
SHUTU-KNOWLEDGE OUT-OF-BOX PARITY NOT READY
```
