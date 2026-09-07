# Out-of-Box Parity Matrix

Reference: dsh-knowledge `v0.3.9`, commit
`95e4a135cca3282b345c6d12a8e09cc4a314402f`.

| Capability | dsh-knowledge | Current Shutu | Final Mode | Test / evidence | Status |
|---|---|---|---|---|---|
| Local Embedding | transformers.js + onnxruntime-node, Qwen3 Embedding | Knowledge-managed Node runtime; pinned model revision and checksum | AUTO_MANAGED_EXTERNAL_RUNTIME | Real Go→Node smoke: Chinese/English/mixed batch, 1024 dimensions, Qwen model | PASS |
| Local Reranker | BGE cross-encoder in isolated worker/process | Knowledge-managed Node runtime; raw logits converted to bounded scores | AUTO_MANAGED_EXTERNAL_RUNTIME | Real three-candidate BGE ordering: relevant `0.99996` above two unrelated scores near `0.000037` | PASS |
| OCR | PaddleOCR with Tesseract fallback | Knowledge-managed Tesseract.js `eng+chi_sim` runtime | AUTO_MANAGED_EXTERNAL_RUNTIME | Real PNG, rotated, low-quality, and two-page scanned-PDF OCR/index/retrieval: `Knowledge Runtime OCR 7788`; failure remains per-document | PASS |
| PDF Renderer | MuPDF full-page rasterization | Knowledge-managed PDF.js + canvas full-page renderer | AUTO_MANAGED_EXTERNAL_RUNTIME | Real PDF→PNG envelope: one rendered page | PASS |
| JBIG2 | Reference decoder path | Managed PDF.js decoder path; unsupported input fails visibly | AUTO_MANAGED_EXTERNAL_RUNTIME | Real `JBIG2Globals.pdf` PDF-to-PNG smoke | PASS |
| JPX | Reference decoder path | Managed PDF.js decoder path; unsupported input fails visibly | AUTO_MANAGED_EXTERNAL_RUNTIME | Real `bug_jpx.pdf` PDF-to-PNG smoke | PASS |
| `.doc` | Anydoc/legacy document conversion | Knowledge-managed MIT `@firecrawl/anydoc` native package | BUNDLED_RUNTIME | Real Apache POI `SampleDoc.doc` Markdown conversion | PASS |
| `.ppt` | Anydoc/legacy presentation conversion | Knowledge-managed MIT `@firecrawl/anydoc` native package | BUNDLED_RUNTIME | Real Apache POI `37625.ppt` Markdown conversion | PASS |
| `.xls` | Anydoc/legacy spreadsheet conversion | Knowledge-managed MIT `@firecrawl/anydoc` native package | BUNDLED_RUNTIME | Real Apache POI `finance.xls` Markdown conversion | PASS |
| Model Download | Downloaded local artifacts | Built-in Hugging Face artifact downloader | BUILT_IN | `internal/models/manager.go` download tests | PASS (artifact only) |
| Model Validation | Runtime-compatible model load/readiness marker | Fixed revisions, file SHA-256, real smoke, persisted runtime state | AUTO_MANAGED_EXTERNAL_RUNTIME | Real Qwen/BGE load and inference; corruption marked FAILED and restored | PASS |
| Model Inference | Real vector/score inference | Managed Node/ONNX runtime calls existing providers | AUTO_MANAGED_EXTERNAL_RUNTIME | Real Qwen/BGE calls through Go supervisor | PASS |
| Offline Restart | Loaded local runtime survives Knowledge restart | Private cache and state reload without download | AUTO_MANAGED_EXTERNAL_RUNTIME | Real offline restart smoke on Qwen cache | PASS |
| Doctor | Runtime/model/component diagnosis | Core health plus managed runtime status/version/lifecycle output | BUILT_IN | `shutu-knowledge doctor`; first probe installs package lock | PASS |
| Packaging | Runtime dependencies included by npm package | Go binary embeds JS/lock/manifest; Node is fixed-download fallback | AUTO_MANAGED_EXTERNAL_RUNTIME | Clean data home installed embedded lock; runtime smoke passed | PASS |
| System Prompt Guidance | `systemPrompt.section` | No public Extension v1 prompt-guidance channel | BLOCKED_BY_AGENT | `docs/agent_extension_gap_report.md` GAP-001 | BLOCKED_BY_AGENT / NON-BLOCKING |

The managed ML, OCR, PDF codec, and Office paths no longer require a
user-authored helper. Windows implementation and real smoke are passing; the
strict final status becomes `PASS` only after the Linux `runtime-release` CI
job also passes. Explicit external services such as MinerU remain optional.
