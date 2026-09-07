# dsh-knowledge Equivalence Matrix

Companion to `dsh_knowledge_capability_inventory.md`. Status values: `PASS` / `PARTIAL` / `BLOCKED` / `NOT APPLICABLE`. Every non-PASS value must carry a reason and remediation path. Current snapshot: fresh Phase 7 source comparison; every row now has an evidence-backed status.

| Capability | dsh-knowledge | shutu-knowledge | Status | Evidence / Reason |
|---|---|---|---|---|
| Knowledge base CRUD | Yes | implemented | PASS | KBM-01..05; `TestBaseLifecycle`, `TestKnowledgeAPIRoundTrip` |
| Base groups + invocation switch | Yes | implemented | PASS | KBM-05..08; `TestGroupsAndScope`, `TestSearchFailClosed` |
| Base stats (incl. stale embedding detection) | Yes | implemented | PASS | KBM-06; `TestStatsDetectStaleEmbeddings`, stats API |
| File import (single + batch + conflict strategies) | Yes | implemented | PASS | DOC-02, DOC-03, DOC-14; `TestAddFilesConflictStrategiesAndDedup` |
| Text import | Yes | implemented | PASS | DOC-01; `TestTextImportLifecycleAndChunks` |
| URL import + refresh | Yes | implemented | PASS | DOC-04, DOC-17; `TestURLImportAndRefresh` |
| Directory import (tree + rescan + repoint) | Yes | nested tracked containers, incremental rescan, explicit repoint, recursive delete, per-entry errors | PASS | DOC-05..06; nested/rescan/repoint/failure/recursive-delete tests pass and failed rows remain visible. |
| Document status model + progress | Yes | implemented | PASS | DOC-11; lifecycle and processing phase tests |
| Error taxonomy (interrupted/dimension/parse/provider) | Yes | implemented | PASS | DOC-12; embedding failure/dimension tests |
| Raw source store ("import means copy") | Yes | implemented | PASS | DOC-13; raw-store guards and file reindex tests |
| Reindex document/base + jobs | Yes | implemented | PASS | DOC-07..08, JOB-01..04; `TestReindexBaseJob`, job API |
| Startup recovery + orphan reconciliation | Yes | implemented | PASS | DOC-15..16; recovery and storage reconciliation tests |
| Parsers: txt/md/mdx/csv/json/log | Yes | implemented | PASS | PRS-01..02; registry, UTF-8/GB18030 tests |
| Parsers: HTML -> Markdown | Yes | implemented | PASS | PRS-03; HTML chrome-stripping and BOM tests |
| Parsers: PDF multi-fallback chain | Yes | implemented text layer, coordinate layout reassembly, MinerU, OCR, and content-signature helper fallback | PASS | PRS-04, PRS-11; average-line health, y-band reassembly, OCR-request preservation, OCR-failure fallback, and content-converter ordering are tested. The converter command is deployment-supplied by design; an unavailable helper preserves native text/error behavior. |
| Parsers: DOCX/DOC/PPTX/PPT/XLSX/XLS/EPUB | Yes | modern formats built in; managed Anydoc runtime covers legacy formats, with explicit converter override | PASS | PRS-05..09; modern formats parse in-process and real `.doc/.ppt/.xls` fixtures pass through the managed native package. Explicit converter contracts retain quoted executable support, health/readiness checks, fail-closed behavior, and Settings hot reload. |
| OCR (PaddleOCR + Tesseract fallback + rendering) | Yes | managed Tesseract.js/PDF.js runtime by default, optional external overrides, embedded-raster fallback/preprocessing, and PP-OCRv5 mobile artifact lifecycle | PASS | OCR-01..07; mode/failure fallback, managed runtime readiness/lifecycle, PaddleOCR artifact validation, dedicated Web lifecycle, real managed OCR and PDF-page probes, optional helper timeout/health tests, and removal are implemented. When PDF-envelope OCR fails, bounded embedded rasters receive upstream-equivalent preprocessing, page grouping, and CJK-space folding. |
| MinerU remote processing | Yes | implemented | PASS | PRS-12; `TestMineruClientFlow` covers upload/poll/download. |
| Smart (heading-aware) chunking | Yes | implemented | PASS | CHK-02; heading/fence/CJK chunk tests |
| Delimiter chunking | Yes | implemented | PASS | CHK-03; delimiter behavior exercised by chunk tests |
| Semantic chunking | Yes | implemented | PASS | CHK-04; merge/coherence/budget/fallback tests |
| Token-limit refinement | Yes | implemented | PASS | CHK-05; `TestRefineByTokenLimit` |
| Metadata preservation (doc-level) | Yes | implemented | PASS | META-01; lifecycle tests and persisted document fields |
| Metadata preservation (chunk-level) | Partial in dsh | matches upstream fields | PASS | META-02..04; headings/context/order/citations retained; page/sheet/slide are intentionally absent upstream too |
| SQLite chunk store + FTS5 trigram | Yes | implemented | PASS | STO-02..03; lexical retrieval and migration tests |
| Vector-hash reuse | Yes | implemented | PASS | STO-05; `TestVectorHashReuseOnReindex` |
| Legacy migration / memory fallback / VACUUM / delete batching | Yes | versioned migrations, FTS optimize, threshold VACUUM, bounded deletes | NOT APPLICABLE (memory fallback) | STO-06..09; storage migrations, startup FTS optimize, threshold VACUUM, and bounded deletes pass. Upstream JSON migration is N/A. Upstream `MemoryStore` compensates for an absent host storage domain and intentionally lacks SQL retrieval/raw files; shutu-knowledge owns its data domain and always opens its own SQLite/raw store, so that fallback path has no equivalent deployment state. |
| BM25 full-text retrieval | Yes | implemented | PASS | FTS-01..04; tokenization/ranking/fail-closed tests |
| Embedding providers (openai/ollama/local/none) | Yes | openai/ollama/none plus managed local Transformers.js/ONNX provider and explicit helper override | PASS | VEC-01..02; provider contract, vector validation, normalization, real Qwen inference, fixed model checksums, and runtime wiring are tested. |
| Local embedding worker isolation | Yes | supervised Knowledge-managed or explicitly configured helper process | PASS | VEC-02, SEC-02; `internal/runtime` covers handshake/readiness/crash restart/idle timeout/request deadline, managed package bootstrap, offline cache reload, and adapters are race-tested. |
| Vector search + dimension validation | Yes | implemented | PASS | VEC-03..05; cosine, threshold, dimension-mismatch tests |
| Hybrid + RRF (weighted, multi-query) | Yes | implemented | PASS | HRT-01..03; RRF/multi-query/hybrid tests |
| MMR | Yes | implemented | PASS | HRT-04; diversity tests |
| Rerank remote + local (child process, circuit breaker) | Yes | remote + managed local BGE runtime + isolated helper override + circuit breaker | PASS | HRT-05..07; helper contract, score validation, breaker, lifecycle tests, real BGE score ordering, and managed model self-test pass. |
| Rerank status reporting | Yes | implemented | PASS | HRT-08; applied/degraded/candidate/elapsed fields tested |
| Context composer (window, anchor, dedup, budgets) | Yes | implemented | PASS | CTX-01..06; evidence module has focused tests for each rule |
| Anchor continuation reading | Yes | implemented | PASS | CTX-07; API and extension tests cover anchor continuation |
| Local model management (download/cancel/remove/status/self-test) | Yes | implemented artifact cache, custom reranker registry, HF download/cancel/readiness/remove, and self-test | PASS | MOD-01..02 lifecycle and registration/self-test API pass; self-test state is invalidated when the artifact envelope changes. |
| Model cache dir migration + HF mirror | Yes | implemented cache dir + endpoint config and verified migration | PASS | MOD-04..05; plan/execute API and Models UI copy to an empty target, verify files, optionally remove source, then activate the target. |
| Ollama management | Yes | implemented list/pull/progress/cancel/delete | PASS | MOD-06; `internal/models` tests and Models UI |
| Knowledge tools (14) | Yes | implemented via Extension Tool Contribution | PASS | TOOL-01..03; external Agent catalog and `internal/extension` tests |
| Tool scope guard (enabled bases) | Yes | implemented in tool handler | PASS | TOOL-04; explicit-empty scope fail-closed tests |
| Destructive approval | Yes (host channel) | declared via ToolRisk + RequiresApproval | PASS | TOOL-05; Agent owns approval, external catalog shows approval-required deletes |
| Proactive-use system prompt guidance | Yes (systemPrompt.section) | BLOCKED (non-blocking) | BLOCKED | Extension v1 has no system-prompt contribution; workaround GAP-001: proactive instruction embedded in tool descriptions |
| Auto RAG (current-turn-first, budgeted, throttled) | Yes | history-aware planning, language/identifier gates, relevance gates, topic throttle, injection dedup | PASS | AUTO-01..05; focused policy tests and context lifecycle test pass. History is a bounded Knowledge-owned cache plus explicit metadata override, never Agent session mutation. |
| Retrieval test UI | Yes | query/base/mode/score/context, citation copy, and replayable history implemented | PASS | WEB-05; `TestRecallSearchHistoryAPI` and browser exercise cover invocation replay, persisted history, per-hit/all-citation clipboard copy, and deletion. |
| Web management panel | Yes | grouped bases, group management, folder drilldown/previews, document errors/progress, OCR model lifecycle, zh/en localization, and batch rebuild implemented | PASS | WEB-01..08; dedicated OCR artifact/runtime status, persistent zh/en language selection across every management route, processor/workflow/auto-retrieve settings, and browser coverage pass. Multi-file selection enforces the upstream 20-file cap and supported-format filter client-side before upload. |
| HTTP API surface | Yes | implemented core REST, model lifecycle, custom reranker registration/self-test, cache migration, restore/raw/probe/status/base-name | PASS | API-01..03; focused Go/API tests cover the complete documented v1 endpoint set. |
| Config layering (deployment/runtime/per-base) | Yes | implemented defaults/file/runtime/per-base layers with global processing, workflow, auto-retrieve, helper, image-caption, and optional image-decoder settings | PASS | CFG-01..03; global/base layering, clamping, persistence, URL inheritance, Agent gating, OpenAI/Ollama caption requests, and best-effort failures are tested. Caption raster decoding covers bounded Device/ICC and Indexed rasters, JPEG/DCTDecode, Flate predictors, LZW, ASCIIHex/ASCII85, RunLength, filter chains, CCITT Group4/Group3 1D, 1/2/4/8-bit samples, Separation/DeviceN tint transforms, and soft-mask alpha composition. JBIG2 and JPX use a bounded optional deployment-supplied decoder contract with globals, real-process/hot-reload tests, health, and fail-closed behavior. PDF Pattern is a painting color space, not an image XObject sample codec. |
| Crash-safe background jobs | Yes | directory/reindex jobs, bounded five-way batch ingestion, status/cancel, recovery | PASS | JOB-01..05; race-tested bounded parallel batch test, directory records/rescan, cancellation/recovery, and job APIs pass. |
| Observability | Partial in dsh (status surfaces, no metrics) | structured metrics + status/rerank/job surfaces | PASS | OBS-01; `/api/metrics` aggregates import/parse/chunk/embedding/search/rerank durations, candidate/context counts, model errors, and job failures with focused/API tests. |
| Fail-closed error semantics | Yes | implemented | PASS | ERR-01; empty scope/filter and provider failure tests |
| Security guards (paths, zip, body, isolation) | Yes | path/zip/body/process isolation, private persistence, write-only credentials, and proxy-aware outbound clients implemented | PASS | SEC-01..04; `docs/security_review.md` records source/test review and fixes for per-base key redaction, storage permissions, and upstream error leakage. |
| Retrieval benchmark suite (zh/en, Hit@k, MRR, context recall) | Yes | repeatable 119-document zh/en benchmark and CI smoke | PASS | TST-02; correctness gate covers Hit@1/Hit@3/Recall@3/MRR/context recall and benchmarks cover ingest/chunk/embed/lexical/vector/hybrid/rerank/RAG. |
| Test suite parity (25 spec areas) | Yes | broad Go/API/extension/web/runtime/benchmark tests plus Chrome CDP lifecycle and policy E2E | PASS | TST-01; `docs/test_parity.md` maps all 25 upstream spec areas to current Go, web-contract, browser E2E, runtime, and real-process gates. 24 areas have PASS evidence and popover placement is not applicable because the independent UI has no floating popover/submenu component. The audit covers equivalent architecture boundaries; it does not change the separate OCR and image-codec capability statuses. |

## Acceptance rule

The matrix may only be declared done when every target capability is `PASS` with test evidence, or `BLOCKED` with a recorded Agent Contract gap / `NOT APPLICABLE` with justification. `PARTIAL` requires a documented remediation path and user-visible scope.
