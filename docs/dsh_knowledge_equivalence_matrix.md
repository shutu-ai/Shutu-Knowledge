# dsh-knowledge Equivalence Matrix

Companion to `dsh_knowledge_capability_inventory.md`. Status values: `PASS` / `PARTIAL` / `BLOCKED` / `NOT APPLICABLE`. Every non-PASS value must carry a reason. Current snapshot: start of Phase 1 (audit complete, implementation not started).

| Capability | dsh-knowledge | shutu-knowledge | Status | Evidence / Reason |
|---|---|---|---|---|
| Knowledge base CRUD | Yes | planned `internal/knowledge` service | TODO | inventory KBM-01..05 |
| Base groups + invocation switch | Yes | planned | TODO | KBM-05..08 |
| Base stats (incl. stale embedding detection) | Yes | planned | TODO | KBM-06 |
| File import (single + batch + conflict strategies) | Yes | planned | TODO | DOC-02, DOC-03, DOC-14 |
| Text import | Yes | planned | TODO | DOC-01 |
| URL import + refresh | Yes | planned | TODO | DOC-04, DOC-17 |
| Directory import (tree + rescan + repoint) | Yes | planned | TODO | DOC-05, DOC-06 |
| Document status model + progress | Yes | planned | TODO | DOC-11 |
| Error taxonomy (interrupted/dimension/parse/provider) | Yes | planned | TODO | DOC-12 |
| Raw source store ("import means copy") | Yes | planned | TODO | DOC-13 |
| Reindex document/base + jobs | Yes | planned | TODO | DOC-07, DOC-08, JOB-01..04 |
| Startup recovery + orphan reconciliation | Yes | planned | TODO | DOC-15, DOC-16 |
| Parsers: txt/md/mdx/csv/json/log | Yes | planned | TODO | PRS-01, PRS-02 |
| Parsers: HTML -> Markdown | Yes | planned | TODO | PRS-03 |
| Parsers: PDF multi-fallback chain | Yes | planned | TODO | PRS-04, PRS-11 |
| Parsers: DOCX/DOC/PPTX/PPT/XLSX/XLS/EPUB | Yes | planned | TODO | PRS-05..08 |
| OCR (PaddleOCR + Tesseract fallback + rendering) | Yes | planned | TODO | OCR-01..07 |
| MinerU remote processing | Yes | planned | TODO | PRS-12 |
| Smart (heading-aware) chunking | Yes | planned | TODO | CHK-02 |
| Delimiter chunking | Yes | planned | TODO | CHK-03 |
| Semantic chunking | Yes | planned | TODO | CHK-04 |
| Token-limit refinement | Yes | planned | TODO | CHK-05 |
| Metadata preservation (doc-level) | Yes | planned | TODO | META-01 |
| Metadata preservation (chunk-level) | Partial in dsh | target: match dsh + document gaps | TODO | META-02, META-03: page/sheet/slide numbers not preserved upstream |
| SQLite chunk store + FTS5 trigram | Yes | planned | TODO | STO-02, STO-03 |
| Vector-hash reuse | Yes | planned | TODO | STO-05 |
| Legacy migration / memory fallback / VACUUM / delete batching | Yes | planned (migration applies to our own schema versions) | TODO | STO-06..09 |
| BM25 full-text retrieval | Yes | planned | TODO | FTS-01..04 |
| Embedding providers (openai/ollama/local/none) | Yes | planned | TODO | VEC-01 |
| Local embedding worker isolation | Yes | planned | TODO | VEC-02, SEC-02 |
| Vector search + dimension validation | Yes | planned | TODO | VEC-03, VEC-04 |
| Hybrid + RRF (weighted, multi-query) | Yes | planned | TODO | HRT-01..03 |
| MMR | Yes | planned | TODO | HRT-04 |
| Rerank remote + local (child process, circuit breaker) | Yes | planned | TODO | HRT-05..07 |
| Rerank status reporting | Yes | planned | TODO | HRT-08 |
| Context composer (window, anchor, dedup, budgets) | Yes | planned | TODO | CTX-01..06 |
| Anchor continuation reading | Yes | planned | TODO | CTX-07 |
| Local model management (download/cancel/remove/status/self-test) | Yes | planned | TODO | MOD-01..03 |
| Model cache dir migration + HF mirror | Yes | planned | TODO | MOD-04, MOD-05 |
| Ollama management | Yes | planned | TODO | MOD-06 |
| Knowledge tools (14) | Yes | planned via Extension Tool Contribution | TODO | TOOL-01..03 |
| Tool scope guard (enabled bases) | Yes | planned in tool handler | TODO | TOOL-04 |
| Destructive approval | Yes (host channel) | planned via ToolRisk + RequiresApproval | TODO | TOOL-05 |
| Proactive-use system prompt guidance | Yes (systemPrompt.section) | BLOCKED (non-blocking) | BLOCKED | Extension v1 has no system-prompt contribution; workaround GAP-001: proactive instruction embedded in tool descriptions |
| Auto RAG (current-turn-first, budgeted, throttled) | Yes | planned via Native Context Provider (`on_user_input_change`) | TODO | AUTO-01..08; mapping in agent_contract_mapping.md |
| Retrieval test UI | Yes | planned | TODO | WEB-05 |
| Web management panel | Yes | planned (own build, Agent navigation contribution) | TODO | WEB-01..10 |
| HTTP API surface | Yes | planned equivalent REST API on extension-owned server | TODO | API-01..03 |
| Config layering (deployment/runtime/per-base) | Yes | planned (extension-owned config + per-base overrides) | TODO | CFG-01..03 |
| Crash-safe background jobs | Yes | planned | TODO | JOB-01..05 |
| Observability | Partial in dsh (status surfaces, no metrics) | target: match + structured logs | TODO | OBS-01 |
| Fail-closed error semantics | Yes | planned | TODO | ERR-01 |
| Security guards (paths, zip, body, isolation) | Yes | planned | TODO | SEC-01..03 |
| Retrieval benchmark suite (zh/en, Hit@k, MRR, context recall) | Yes | planned equivalent dataset + metrics | TODO | TST-02 |
| Test suite parity (25 spec areas) | Yes | planned Go equivalents incl. real two-process extension tests | TODO | TST-01 |

## Acceptance rule

The matrix may only be declared done when every target capability is `PASS` with test evidence, or `BLOCKED` with a recorded Agent Contract gap / `NOT APPLICABLE` with justification. `PARTIAL` requires a documented remediation path and user-visible scope.
