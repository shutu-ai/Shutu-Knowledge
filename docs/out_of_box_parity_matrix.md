# Out-of-Box Parity Matrix

Reference: dsh-knowledge `v0.3.9`, commit
`95e4a135cca3282b345c6d12a8e09cc4a314402f`.

| Capability | dsh-knowledge | Current Shutu | Final Mode | Test / evidence | Status |
|---|---|---|---|---|---|
| Local Embedding | transformers.js + onnxruntime-node, Qwen3 Embedding | Provider plus supervised JSON helper contract; no shipped inference runtime | MANUAL_EXTERNAL_RUNTIME | `internal/embedding/local_test.go` is contract-only; no real runtime/model smoke | FAIL |
| Local Reranker | BGE cross-encoder in isolated worker/process | Provider plus supervised JSON helper contract; self-test requires configured helper | MANUAL_EXTERNAL_RUNTIME | `internal/rerank/local_test.go`, self-test API, no real model in release | FAIL |
| OCR | PaddleOCR with Tesseract fallback | Artifact download plus configured OCR helper/fallback | MANUAL_EXTERNAL_RUNTIME | `internal/models/ocr.go`, `internal/parser/helper_runtime.go` | FAIL |
| PDF Renderer | MuPDF full-page rasterization | Built-in PDF parsing plus optional configured render helper | MANUAL_EXTERNAL_RUNTIME | `internal/parser/pdf_render.go`, renderer fallback tests | FAIL |
| JBIG2 | Reference decoder path | Optional configured image decoder only | MANUAL_EXTERNAL_RUNTIME | `internal/parser/pdf_external_decoder.go` | FAIL |
| JPX | Reference decoder path | Optional configured image decoder only | MANUAL_EXTERNAL_RUNTIME | `internal/parser/pdf_external_decoder.go` | FAIL |
| `.doc` | Anydoc/legacy document conversion | Configured legacy-office converter only | MANUAL_EXTERNAL_RUNTIME | `internal/parser/office.go`, helper process tests | FAIL |
| `.ppt` | Anydoc/legacy presentation conversion | Configured legacy-office converter only | MANUAL_EXTERNAL_RUNTIME | `internal/parser/office.go`, helper process tests | FAIL |
| `.xls` | Anydoc/legacy spreadsheet conversion | Configured legacy-office converter only | MANUAL_EXTERNAL_RUNTIME | `internal/parser/office.go`, helper process tests | FAIL |
| Model Download | Downloaded local artifacts | Built-in Hugging Face artifact downloader | BUILT_IN | `internal/models/manager.go` download tests | PASS (artifact only) |
| Model Validation | Runtime-compatible model load/readiness marker | Non-empty artifact checks; no checksum/load/inference validation | MANUAL_EXTERNAL_RUNTIME | Model manifest inspection | FAIL |
| Model Inference | Real vector/score inference | Requires manually configured helper | MANUAL_EXTERNAL_RUNTIME | No real inference implementation in repository | FAIL |
| Offline Restart | Loaded local runtime survives Knowledge restart | No managed runtime/model load to restore | MANUAL_EXTERNAL_RUNTIME | No fresh-install offline runtime scenario | FAIL |
| Doctor | Runtime/model/component diagnosis | Built-in storage/health diagnosis plus explicit degraded runtime/model state | BUILT_IN | `shutu-knowledge doctor`; cannot install missing runtimes | FAIL |
| Packaging | Runtime dependencies included by npm package | Go binary and Web assets only; no ML/OCR/Office runtime package | BUNDLED_RUNTIME | `go.mod`, `web/package.json`, release artifacts | FAIL |
| System Prompt Guidance | `systemPrompt.section` | No public Extension v1 prompt-guidance channel | BLOCKED_BY_AGENT | `docs/agent_extension_gap_report.md` GAP-001 | BLOCKED_BY_AGENT / NON-BLOCKING |

`PASS (artifact only)` is deliberately not an Out-of-Box parity pass. The
strict final status is `FAIL` whenever the requested behavior still needs a
user-installed or user-authored runtime.
