# dsh-knowledge Capability Inventory

Source of truth: `dsh-knowledge` v0.3.9 (commit `95e4a135cca3282b345c6d12a8e09cc4a314402f`), audited from source under `C:\dev-projects\dsh\dsh-knowledge` (read-only reference). Status vocabulary: `AUDITED` (confirmed in source), values for "shutu-knowledge target" refer to the planned implementation and start at `TODO`.

Reference repo layout: `src/knowledge/*` (service, storage, retrieval, models), `src/tool-knowledge/index.ts` (14 model tools + auto-retrieve), `src/ui/client/*` (browser panel), `tests/*` (25 spec files), `benchmarks/*` (fixed zh/en corpus + retrieval/RAG eval), `scripts/*` (build/release/benchmark), `cordis.patch.yml` (deployment defaults).

## 1. Knowledge Base Management

| Capability ID | dsh-knowledge implementation | Evidence |
|---|---|---|
| KBM-01 Create base | `KnowledgeService.createBase` (name, description, group, per-base config) | `src/knowledge/index.ts` |
| KBM-02 Rename/update base | `renameBase` PATCH: name, description, group, config | `src/knowledge/index.ts`, `src/knowledge/http.ts` |
| KBM-03 Delete base | `deleteBase`: deletes chunks by base, raw files by base, domain record | `src/knowledge/index.ts`, `src/knowledge/store.ts` |
| KBM-04 List bases | `listBases` returns `BaseSummary` (documentCount, storedDocCount, chunkCount, charCount, tokenCount, sourceInfo) | `src/knowledge/types.ts` |
| KBM-05 Groups | create/rename/delete group; bases carry `group`; sidebar folding | `src/knowledge/index.ts`, `http.ts` |
| KBM-06 Stats | `stats(baseId?)`: document/chunk counts, char/token totals, embedded flag, dimensions, staleEmbeddings + staleChunkCount (model drift detection) | `src/knowledge/index.ts` |
| KBM-07 Restore base | `restoreBase`: create a new base from an existing base's config snapshot | `src/knowledge/index.ts` |
| KBM-08 Invocation switch | global `enabled` + pinned `enabledBaseIds`; empty/invalid scope matches zero bases (fail-closed) | `src/knowledge/domain.ts`, `tool-knowledge/index.ts` |

User-visible behavior: bases are grouped namespaces with per-base config overrides (empty fields inherit global); deletion is destructive and host-approved.

## 2. Document Lifecycle

| Capability ID | dsh-knowledge implementation | Evidence |
|---|---|---|
| DOC-01 Import text | `addTextDocument` (title, content, parentDirectoryId) | `index.ts` |
| DOC-02 Import file | `addFileDocument` (base64 bytes, conflict strategy keep/replace/rename/detect) | `index.ts` |
| DOC-03 Batch files | `addFiles`: server-side conflict detection, up to 20 files x 22 MB, 5-way import pool | `index.ts`, README |
| DOC-04 Import URL | `addUrlDocument` + `refreshUrlDocument` (changed detection, title/chunk update) | `index.ts` |
| DOC-05 Directory import | `importDirectory` (flat job) and `importDirectoryTree` (stable tree with `parentDirectoryId`); recursive scan of supported extensions | `index.ts`, `parse.ts` |
| DOC-06 Tracked path | file/directory sources persist `sourcePath`; `importFromPath` and `setBaseSourcePath` allow rescan/repoint | `index.ts` |
| DOC-07 Reindex document | `reindexDocument`: re-read raw source (raw file or rawText), re-parse, re-chunk, re-embed with vector-hash reuse | `index.ts` |
| DOC-08 Reindex base | `startReindexBase` background job with status/cancel; `reindexDocuments` bulk | `index.ts` |
| DOC-09 Delete documents | single + bulk delete incl. chunks and raw files | `index.ts` |
| DOC-10 Rename document | `renameDocument` PATCH | `index.ts` |
| DOC-11 Status model | document summary status: pending / processing (parsing, embedding) / completed / failed; `indexingProgress` 0-100; `incomplete` marker for crash recovery | `types.ts` |
| DOC-12 Error taxonomy | `errorCode`: interrupted, dimension_mismatch, parse_failed, embedding_provider; localized in UI | `types.ts` |
| DOC-13 Raw store | "import means copy": original bytes persisted at `<chunkStoreDir>/knowledge-raw/<baseId>/<docId><ext>` (or directory-relative tree), path-escape guarded | `store.ts` |
| DOC-14 Duplicate handling | SHA-256 `contentHash` dedup; same-name conflict strategies (server-authoritative detect round) | `index.ts`, `types.ts` |
| DOC-15 Startup recovery | `recoverInterruptedImports`: remove unrecoverable placeholders, resume docs with rawText/rawFile (hash reuse re-embeds only missing batches); `resumeInterruptedOnStartup` off = mark failed | `store.ts`, `config.ts` |
| DOC-16 Orphan reconciliation | `reconcileOrphanRaws` deletes raw files no document references; `reconcileChunkCounts` fixes drifted metadata | `store.ts` |
| DOC-17 URL auto-refresh | `urlRefreshHours` (0 = off) | `config.ts` |
| DOC-18 Previews | document raw text (bounded `rawTextLimit`, `rawTextTruncated`), chunk list paging, raw file download/inline | `http.ts` |

## 3. Input Sources

| Capability ID | dsh-knowledge implementation | Evidence |
|---|---|---|
| SRC-01 Local file | base64 upload or absolute path | `index.ts` |
| SRC-02 Directory | recursive scan, stable folder tree, rescan picks up new/changed/removed files, per-file errors preserved | README, `index.ts` |
| SRC-03 Text note | direct text entry | `index.ts` |
| SRC-04 URL | fetch (proxy-aware), refresh, auto-refresh interval | `net.ts`, `index.ts` |

## 4. Document Parsing

| Capability ID | dsh-knowledge implementation | Evidence |
|---|---|---|
| PRS-01 Format set | `SUPPORTED_DOCUMENT_EXTENSIONS`: txt, md, markdown, mdx, csv, html, htm, json, log, pdf, docx, doc, pptx, ppt, xlsx, xls, epub | `parse.ts` |
| PRS-02 Text decode | UTF-8 with GB18030 fallback (replacement-char heuristic), BOM handling | `parse.ts` |
| PRS-03 HTML | turndown -> structure-preserving Markdown (headings/lists/links/code/tables); nav/footer/aside stripped; regex fallback | `parse.ts` |
| PRS-04 PDF primary | pdf-parse; empty/corrupt -> `@firecrawl/anydoc` content-signature fallback -> layout reassembly (pdfjs glyph y-band clustering for per-glyph math PDFs, CID cmaps) -> OCR | `parse.ts` |
| PRS-05 DOCX | mammoth extractRawText | `parse.ts` |
| PRS-06 DOC (legacy) | word-extractor | `parse.ts` |
| PRS-07 PPTX / PPT | PPTX: jszip slide XML text; PPT: anydoc -> Markdown | `parse.ts` |
| PRS-08 XLSX / XLS | jszip sheet XML (shared strings, inline strings, bool/number), tab-joined rows; XLS via anydoc | `parse.ts` |
| PRS-09 EPUB | jszip xhtml -> turndown Markdown per page | `parse.ts` |
| PRS-10 Zip safety | 256 MB uncompressed zip-bomb guard | `parse.ts` |
| PRS-11 Text-layer health | `averageLineLength` heuristic decides keep / reassemble / OCR | `parse.ts` |
| PRS-12 MinerU | optional remote processor (batch create -> signed PUT upload -> poll -> download zip -> Markdown); failure falls back to local pipeline; per-base `documentProcessorProvider` | `mineru.ts` |

## 5. OCR

| Capability ID | dsh-knowledge implementation | Evidence |
|---|---|---|
| OCR-01 Engine | PaddleOCR PP-OCRv5 mobile (det 4.8 MB + rec 16.5 MB + 18383-entry CJK dict, ~21 MB), downloaded from HF endpoint (default mirror hf-mirror.com) | `ocr.ts` |
| OCR-02 Isolation | OCR inference in worker thread; native/WASM crash cannot take down host; client respawns on error | `ocr-worker.ts` |
| OCR-03 Rendering | mupdf WASM full-page render ~216 dpi (scanned + vector-only pages); fallback: pdfjs embedded-raster extraction | `ocr.ts` |
| OCR-04 Preprocessing | grayscale, min-max contrast stretch, 3x3 unsharp, 2x upscale for small rasters | `ocr.ts` |
| OCR-05 Tesseract fallback | tesseract.js when PaddleOCR fails; CJK inter-character space folding postprocess | `ocr.ts` |
| OCR-06 Modes | native text extraction first; OCR fallback for empty/corrupt layers; full-page OCR for scans; no separate "forced OCR" flag (OCR triggers automatically) | `parse.ts`, `ocr.ts` |
| OCR-07 Management | download/status/remove HTTP endpoints + UI page; readiness gate `isOcrReady` | `http.ts`, `ocr.ts` |

## 6. Chunking

| Capability ID | dsh-knowledge implementation | Evidence |
|---|---|---|
| CHK-01 Token budgets | `chunkSize`/`chunkOverlap` are token budgets converted via document chars-per-token ratio (CJK ~1.5 chars/token, Latin ~4) | `chunk.ts` |
| CHK-02 Smart chunking | heading-aware splitter: markdown heading stack path, code-fence protection (same-char length rule), blank-line blocks, scored break points (h1 100 ... h6 50, code fence 80, hr 60, paragraph 20, CJK sentence 8, list 5, newline 1) with distance decay 0.7 in a 22% window | `chunk.ts` |
| CHK-03 Delimiter mode | `smartChunk=false`: split on configured separator only | `chunk.ts` |
| CHK-04 Semantic chunking | `splitSemanticSegments` (paragraph-level, never windowed) + `mergeSemanticSegments` (greedy adjacent merge while cosine >= threshold; merged vector = length-weighted renormalized mean, no extra embedding pass); `semanticChunkThreshold` default 0.75 | `chunk.ts` |
| CHK-05 Token limit refinement | `refineChunksByTokenLimit`: recursive split at preferred boundaries (blank line, CJK period/excl/question, comma, comma-space, space), keeps unsplittable pieces whole | `chunk.ts` |
| CHK-06 Chunk payload | id, docId, baseId, index, text, heading path, retrieval context (title + heading), optional embedding + embeddingModel tag | `types.ts` |

## 7. Metadata

| Capability ID | dsh-knowledge implementation | Evidence |
|---|---|---|
| META-01 Document level | title, fileName, mimeType, url, sourcePath, parentDirectoryId, contentHash, rawFilePath, charCount, tokenCount, chunkCount, timestamps | `types.ts` |
| META-02 Chunk level | heading path, chunk order index, retrieval context string | `types.ts` |
| META-03 Not preserved | page / sheet / slide numbers are not kept as chunk metadata (slides/sheets are flattened into text); custom user metadata per chunk is absent | `parse.ts` |
| META-04 Citation support | search hits carry documentTitle, heading, chunkId, chunkIndex, contextWindow for attribution | `types.ts` |

## 8. Storage and Indexing

| Capability ID | dsh-knowledge implementation | Evidence |
|---|---|---|
| STO-01 Business state | bases/documents/config-overrides/groups/enabled scope in DSH `storageDomain` (zod-validated records) | `domain.ts` |
| STO-02 Chunk store | plugin-owned SQLite (`node:sqlite`): `chunk` table with float32 LE embedding BLOB, doc/base indexes, embedding-hash index | `chunkdb.ts` |
| STO-03 FTS | external-content FTS5 trigram table over search text (context + body) kept in sync by triggers | `chunkdb.ts` |
| STO-04 Vector cache | per-base lazy Float32Array caches for the brute-force lane; precise invalidation | `chunkdb.ts` |
| STO-05 Vector-hash reuse | `hashEmbeddingText` (sha256 of context+text) x embedding model lookup reuses stored vectors across re-index/re-embed (A4) | `chunkdb.ts` |
| STO-06 Legacy migration | one-time idempotent migration from legacy JSON chunk file to SQLite | `chunkdb.ts` |
| STO-07 Memory fallback | `MemoryStore` when the storage backend is absent (tests/headless) | `store.ts` |
| STO-08 Space reclaim | WAL checkpoint + threshold-gated VACUUM (freelist >= 20% and >= 8 MB) + FTS optimize | `chunkdb.ts` |
| STO-09 Delete batching | chunk deletes in 2000-row batches with 50 ms event-loop yields | `chunkdb.ts` |
| STO-10 Raw files | see DOC-13 | `store.ts` |

## 9. Full-Text Retrieval

| Capability ID | dsh-knowledge implementation | Evidence |
|---|---|---|
| FTS-01 Tokenization | query side: whole words >= 3 chars + CJK trigram windows (max 64 MATCH terms); 1-2 char terms applied as escaped LIKE filters | `chunkdb.ts` |
| FTS-02 BM25 | SQLite FTS5 `bm25()` normalized via `raw/(raw+1)`; index side trigram tokenizer | `chunkdb.ts`, `retrieval.ts` |
| FTS-03 Guards | empty/symbol-only query routes to safe LIKE scan (never `MATCH ''` or `LIKE '%%'` full scan); deadline-aware | `chunkdb.ts` |
| FTS-04 Filters | docIds / titleIncludes / sourceTypes / updatedBefore/After; empty filter = match nothing (fail-closed); > 500 ids refused loudly | `types.ts`, `chunkdb.ts` |
| FTS-05 Fallback | in-memory corpus BM25 (k1=1.5, b=0.75) for MemoryStore / unit tests | `retrieval.ts` |

## 10. Vector Retrieval

| Capability ID | dsh-knowledge implementation | Evidence |
|---|---|---|
| VEC-01 Providers | openai-compatible `/embeddings`, Ollama (modern `/api/embed` + legacy per-prompt), local transformers.js worker, none | `embed.ts` |
| VEC-02 Local model | default `onnx-community/Qwen3-Embedding-0.6B-ONNX` (1024-d), dedicated worker thread, family-based pooling (qwen3 last_token, bge/bce cls, e5/gte mean), idle model release with worker kept alive | `embed.ts`, `embed-worker.ts` |
| VEC-03 Normalization | all vectors L2-normalized; cosine = dot product | `embed.ts`, `retrieval.ts` |
| VEC-04 Search | brute-force scan of scope BLOBs (no ANN), dimension validation, deadline checks | `chunkdb.ts` |
| VEC-05 Health | probe-embedding-dimensions endpoint before config save; `dimension_mismatch` error class; local readiness marker with file fingerprints | `http.ts`, `types.ts` |

## 11. Hybrid Retrieval, RRF, MMR, Rerank

| Capability ID | dsh-knowledge implementation | Evidence |
|---|---|---|
| HRT-01 Modes | auto (hybrid when vectors exist, else lexical), hybrid, vector (degrades to lexical without vectors), lexical | `retrieval.ts`, `index.ts` |
| HRT-02 RRF | `score(d) = sum w_i / (60 + rank_i(d))`; hybrid weights `[rrfVectorWeight, 1]` (0.1-5), multi-query fuses per-variant orders; normalized by `2/(K+1)`; ties keep recall order | `retrieval.ts`, `index.ts` |
| HRT-03 Multi-query | `queries[]` variants (plus tool `extraQueries` up to 3): normalized, deduplicated, retrieved independently, rank-fused, reranked at most once | `index.ts`, `tool-knowledge/index.ts` |
| HRT-04 MMR | optional `mmrDiversity` (lambda) over topK*3 (min 12) pool; hits without embeddings appended unmodified | `retrieval.ts` |
| HRT-05 Rerank remote | POST `{baseUrl}/rerank` (Jina/SiliconFlow/Cohere-v2 style); strict validation: count match, finite [0,1]; pair budgets 128 query + 352 evidence tokens | `rerank.ts` |
| HRT-06 Rerank local | `local:` prefix models run in a dedicated child process (`rerank-process.mjs`); hard timeout, on-demand rebuild, isolated from embedding worker | `rerank-adapter.ts`, `rerank-process.ts` |
| HRT-07 Circuit breaker | queue cap 16; 3 consecutive timeout/crash/runtime/invalid-response failures -> 5 min open with half-open probes | README, `rerank-adapter.ts` |
| HRT-08 Status reporting | `RerankStatus`: configured/provider/model/status applied|not_needed|degraded/attempted/candidateCount/elapsedMs/error{code, retryable, action} | `types.ts` |
| HRT-09 Thresholds | similarityThreshold applied to comparable vector/rerank relevance scores only; BM25/RRF rank scores not thresholded by the same knob | README, `retrieval.ts` |

## 12. Context Composition

| Capability ID | dsh-knowledge implementation | Evidence |
|---|---|---|
| CTX-01 Window | ordered `before -> anchor -> after` from contiguous neighbors, heading-path boundary by default (`crossHeading` opt-in) | `context.ts` |
| CTX-02 Budgets | explicit search: 768 tokens/hit, 8192 total output; auto background: 180/hit, 640 total; anchor continuation default 1600 (128-4096 configurable) | `context.ts`, README, `tool-knowledge/index.ts` |
| CTX-03 Anchor priority | anchor always included; oversized anchor cropped around query focus (identifier/word/CJK bigram matching, generic words ignored) at sentence boundaries | `context.ts` |
| CTX-04 Overlap dedup | adjacent excerpts with >= 24-char exact suffix/prefix overlap are trimmed | `context.ts` |
| CTX-05 Side balancing | post-anchor budget split between sides; unused allowance donated; serialized-budget enforcement shrinks farthest excerpt first | `context.ts` |
| CTX-06 Serialization | `serializeContextWindow` document order, `>>>` marks the anchor, `[heading]` prefixes; `hasMoreBefore/After` flags | `context.ts` |
| CTX-07 Continuation | `knowledge_get_document` anchor mode via `anchorChunkId`/`anchorIndex` with before/after/maxTokens/focus/crossHeading; legacy `siblingContext` retained through 0.3.x | `tool-knowledge/index.ts` |

## 13. Embedding / Rerank / OCR Model Management

| Capability ID | dsh-knowledge implementation | Evidence |
|---|---|---|
| MOD-01 Local model lifecycle | list / download (progress) / cancel (ack-based) / remove (release-ack before unlink) / status per model | `embed.ts`, `http.ts` |
| MOD-02 Readiness | config + tokenizer + non-empty ONNX check; readiness marker with file fingerprints + runtime version; self-test for custom rerankers (single-logit validation + pos/neg probes) | README, `localModels.ts` |
| MOD-03 Custom rerankers | register custom HF ONNX reranker (experimental) via API | `http.ts` |
| MOD-04 Cache management | `localModelCacheDir` config, native folder picker UI, safe migration (guard against active downloads) | `index.ts`, UI |
| MOD-05 HF endpoint | `hfEndpoint` config / `HF_ENDPOINT` env override (mirror support) | `embed.ts` |
| MOD-06 Ollama | list tags / pull (progress + cancel) / delete; browsing/pulling never changes embedding config | `ollama.ts`, `http.ts` |
| MOD-07 Model suggestions | curated suggestion list for embedding/rerank model ids | `index.ts` |

## 14. Knowledge Tools (model-facing)

| Capability ID | dsh-knowledge implementation | Evidence |
|---|---|---|
| TOOL-01 inventory | 14 tools: knowledge_search, knowledge_list_bases, knowledge_create_base, knowledge_delete_base, knowledge_add_document, knowledge_list_documents, knowledge_delete_document, knowledge_import_url, knowledge_refresh_url, knowledge_stats, knowledge_get_document, knowledge_read_document, knowledge_reindex_document, knowledge_reindex_base | `tool-knowledge/index.ts` |
| TOOL-02 search tool | query/baseId/topK/mode/docIds/titleIncludes/sourceTypes/updatedAfter/updatedBefore/extraQueries; returns citations, chunkIndex, ordered contextWindow | `tool-knowledge/index.ts` |
| TOOL-03 read tool | `knowledge_read_document`: character-range reads + regex locate; `knowledge_get_document`: chunk paging + anchor continuation mode | `tool-knowledge/index.ts` |
| TOOL-04 scope guard | all tools respect enabled scope; disabled invocation denied via tools guard | `tool-knowledge/index.ts` |
| TOOL-05 destructive approval | delete_base / delete_document require one-shot host approval; runtime fails closed without an approval channel | `tool-knowledge/index.ts` |
| TOOL-06 proactive guidance | system-prompt section injected while enabled and bases exist: proactively call knowledge_search before answering; quote excerpts with citations | `tool-knowledge/index.ts` |

## 15. Automatic Retrieval (Auto RAG)

| Capability ID | dsh-knowledge implementation | Evidence |
|---|---|---|
| AUTO-01 Trigger | `agent/pre-step` event on user turns; folds a user-role background message (plugin-sourced) immediately after the triggering message so background stays with its turn | `tool-knowledge/index.ts` |
| AUTO-02 Query planner | current-turn-first: current message capped 200 chars; history-augmented second query only when message <= 40 chars and deictic/short; uses last 2 user messages; both queries lexically recalled then RRF-fused (history never replaces current question) | `tool-knowledge/index.ts`, README |
| AUTO-03 Budgets | top 3 hits, 180 tokens/hit, 640 total, shared 4 s wall-clock deadline before first token | `tool-knowledge/index.ts` |
| AUTO-04 Gates | abs min score 0.12; rerank floor 0.3; lead ratio 1.2 (strong mult 2 bypass); relevance group ratio 0.6; candidate pool 12 | `tool-knowledge/index.ts` |
| AUTO-05 Seats | per-base `autoRetrieveWeight` (0-5, default 3) caps chunks per base per injection; 0 excludes | `tool-knowledge/index.ts` |
| AUTO-06 Throttle | same topic: max 1 new evidence per 5 min; new topic: up to 3; injected-chunk dedup memory cap 50 | `tool-knowledge/index.ts` |
| AUTO-07 Identifier channel | pure numbers/model versions/error codes follow a strict identifier path; injected text must contain the complete, boundary-correct identifier | README |
| AUTO-08 Safety | evidence framed as untrusted reference material; state committed only after successful fold; best-effort, never breaks the step; per-agent state cleared on dispose; provider-outage logs deduplicated | `tool-knowledge/index.ts` |

## 16. Web UI

| Capability ID | dsh-knowledge implementation | Evidence |
|---|---|---|
| WEB-01 Entry | sidebar "Knowledge" entry next to settings; client bundle injected via DSH client runtime | `src/ui/client/*` |
| WEB-02 Bases view | grouped base list, create/rename/delete dialogs, group management, source info lines | `KnowledgeSection.tsx` |
| WEB-03 Documents view | table with status/progress/error codes, folder tree drill-down, previews (raw/text/chunks), rename/delete, batch rebuild | `KnowledgeSection.tsx` |
| WEB-04 Import | file upload (20 files x 22 MB), directory path import, URL import, text notes; conflict strategy dialogs (rename/replace/keep) | UI + `types.ts` |
| WEB-05 Recall test | query + base selection, results with source, relevance, keyword/vector scores, latency, rerank status, copy citations, query history replay | README, UI |
| WEB-06 Local models | embedding/rerank/OCR model pages: download, retry, cancel, delete, progress, health, cache dir migration, HF mirror setting | `LocalModelsSection.tsx` |
| WEB-07 Ollama | model list, pull with progress, cancel, delete | UI |
| WEB-08 Settings | global + per-base config editors (embedding, rerank, chunking, retrieval, auto-retrieve, conflict, processor, captioning, storage paths) | `rag-config.tsx` |
| WEB-09 UX polish | zh/en locales, theme tokens, popover viewport placement, toasts | `locales.ts`, `theme.ts`, `popover.tsx` |
| WEB-10 API client | same-origin fetch client mirroring host vocabulary (no host imports) | `api.ts` |

## 17. HTTP API

| Capability ID | dsh-knowledge implementation | Evidence |
|---|---|---|
| API-01 surface | `/knowledge/*`: config GET/PUT, knowledge-toggle GET/PUT, groups CRUD, stats, local-model-status, probe-embedding-dimensions, local-models (list/custom/self-test/download/cancel/remove/migrate), local-ocr (status/download/remove), local-ollama (tags/pull/status/pulls/delete), model-suggestions, indexing-status, import-directory job status/cancel, reindex job status/cancel, bases CRUD + stats/reindex/files-batch/restore/import-directory(-tree)/import-path/source-path/directories/documents, documents (get/patch/delete/chunks/reindex/refresh/raw, bulk delete/reindex), search POST | `http.ts` |
| API-02 envelope | `{ok:true,value}` / `{ok:false,error{code,message}}`; conflicts as 409; raw download with content-disposition | `http.ts` |
| API-03 limits | JSON body cap 32 MB | `http.ts` |

## 18. Background Jobs

| Capability ID | dsh-knowledge implementation | Evidence |
|---|---|---|
| JOB-01 directory import | job id, total, status, cancel | `index.ts`, `http.ts` |
| JOB-02 reindex base | background job with status/cancel | `index.ts` |
| JOB-03 batch files | 5-way pool | `index.ts` |
| JOB-04 indexing status | per-document phase (parsing/embedding) + progress | `index.ts` |
| JOB-05 crash recovery | see DOC-15/DOC-16 | `store.ts` |

## 19. Configuration

| Capability ID | dsh-knowledge implementation | Evidence |
|---|---|---|
| CFG-01 layers | deployment defaults (cordis.patch.yml) -> runtime overrides (persisted) -> per-base overrides; numeric clamping on resolve | `config.ts` |
| CFG-02 fields | 38+ fields: embedding (provider/baseUrl/model/apiKey/batchSize), rerank (model/baseUrl/apiKey/timeout), chunking (smartChunk/separator/size/overlap/semantic/threshold/tokenLimit), retrieval (topK/mode/threshold/mmr/rrfVectorWeight/siblingChunks), storage (chunkStorePath/localModelCacheDir/hfEndpoint/localWorkerIdleTimeoutMs), processing (documentProcessorProvider/mineru*/imageCaption*), workflow (conflictStrategy/urlRefreshHours/resumeInterruptedOnStartup), auto-retrieve (autoRetrieve/autoRetrieveWeight) | `config.ts` |
| CFG-03 env | KNOWLEDGE_API_KEY, KNOWLEDGE_RERANK_API_KEY, HF_ENDPOINT, DSH_HOME, HTTP(S)_PROXY | `config.ts`, `embed.ts`, `net.ts` |

## 20. Observability, Error Handling, Security, Testing

| Capability ID | dsh-knowledge implementation | Evidence |
|---|---|---|
| OBS-01 status surfaces | indexingStatus, rerank status, embedding errors, background job status, console warnings; no formal metrics endpoint | various |
| ERR-01 fail-closed | empty filters match nothing; invalid rerank responses degrade (never misapplied); conflicts 409 | `chunkdb.ts`, `rerank.ts`, `http.ts` |
| SEC-01 path safety | raw store path-escape guard; zip-bomb guard; body cap | `store.ts`, `parse.ts`, `http.ts` |
| SEC-02 isolation | embedding worker thread, OCR worker thread, rerank child process: local inference failures cannot take down host | `embed.ts`, `ocr-worker.ts`, `rerank-adapter.ts` |
| SEC-03 network | proxy env support for all fetches (including model downloads), timeout + retry + deadline, network error hints | `net.ts` |
| TST-01 test layers | 25 spec files: chunk, chunkdb, context, config, retrieval, embed, embedding-reuse, store, domain-store, service, auto-retrieve (+sqlite), tool-contract, ocr, parse, rerank(+adapter), local-rerank readiness/runtime, worker lifecycle, net, ui-policy, popover, chunk-expansion, packed smoke | `tests/` |
| TST-02 benchmarks | fixed zh/en corpus (24 docs), questions.json, `eval-retrieval` (Hit@k/Recall@k/MRR), `eval-rag` (sentence-level context recall, RAGAS-style), `benchmark-retrieval` latency baseline | `benchmarks/`, `scripts/` |

## 21. dsh-knowledge known limitations (documented upstream)

- Model selectors are editable combo boxes, not live provider model lists.
- Embedding runs inside the import flow; first local-model download blocks that import (progress visible).
- MinerU needs an API key; otherwise local parse + OCR.
- Text notes have no rich-text editor.
- Intel Mac cannot run onnxruntime-based local embedding/OCR (remote providers only).
