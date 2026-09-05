# shutu-knowledge Architecture

Status: Phase 0 design. Derived from the audited dsh-knowledge behavior (`dsh_knowledge_capability_inventory.md`) and the shutu-agent Extension Platform v1 contract (`agent_contract_mapping.md`).

## 1. Goals

1. Capability and behavior equivalence with dsh-knowledge (see equivalence matrix), not source translation.
2. Single-direction dependency: this project depends only on `github.com/shutu-ai/shutu-agent/sdk/extension` (public v1). Never `internal/...`.
3. Knowledge Core is agent-agnostic; the Extension Adapter is the only Agent-aware layer.
4. Deleting this project leaves shutu-agent unaffected.

## 2. Tech stack

| Concern | Choice | Rationale |
|---|---|---|
| Language | Go 1.23+ | matches shutu-agent deployment model, single binary, cross-platform |
| Database | SQLite via `modernc.org/sqlite` (pure Go) with FTS5 | parity with dsh `node:sqlite` + FTS5 trigram; no cgo, portable builds |
| Schema management | versioned embedded migrations (own runner) | Gate: no scattered `CREATE TABLE IF NOT EXISTS` in business code |
| Web UI | React + TypeScript + Vite, embedded into the Go binary (`go:embed`) | independent build pipeline, native navigation via web contribution |
| Local ML (embedding/rerank) | optional helper process (Node + transformers.js, same model families as dsh), health-checked and auto-discovered; remote OpenAI-compatible/Ollama providers work without it | avoids cgo/onnxruntime build burden; optional runtime is explicitly allowed by the requirements |
| OCR | optional helper (PaddleOCR ONNX / Tesseract) with availability detection and fallback; native text extraction first | OCR failure must not break parseable text |
| MinerU | remote HTTP processor (batch/upload/poll/download), fallback to local chain | parity with PRS-12 |

## 3. Process model

```text
shutu-agent
  │  manages (stdio, JSON-RPC shutu-extension/1)
  ▼
shutu-knowledge (extension process)
  ├── extension server (initialize/health/context/tool/event/shutdown)
  ├── local HTTP server (web UI + REST API)  ── webBaseUrl reported at initialize
  ├── job manager (ingest/parse/embed/reindex/refresh worker pool)
  └── optional helper processes (model worker, OCR worker)
```

Standalone mode: `shutu-knowledge serve` runs the HTTP server without the Agent; `shutu-knowledge doctor` and `shutu-knowledge version` work offline.

## 4. Module layout

```text
shutu-knowledge/
├── cmd/shutu-knowledge/        # CLI entrypoints (serve, doctor, version)
├── internal/
│   ├── extension/              # ONLY package importing sdk/extension; DTO mapping
│   ├── knowledge/              # KB/document service, lifecycle, scope guard
│   ├── ingest/                 # source handlers (file/dir/url/text), jobs, recovery
│   ├── parser/                 # parser registry + format parsers
│   ├── chunk/                  # chunking strategies (smart/delimiter/semantic/token-limit)
│   ├── embedding/              # EmbeddingProvider implementations + hashing/reuse
│   ├── rerank/                 # RerankerProvider (remote/local) + circuit breaker
│   ├── retrieval/              # BM25/vector lanes, RRF, MMR, orchestration
│   ├── index/                  # SQLite schema access, FTS, vector store
│   ├── context/                # context window composer (before/anchor/after)
│   ├── storage/                # store interface, migrations, raw store
│   ├── models/                 # local model manager (download/status/self-test/migrate)
│   ├── jobs/                   # job manager, worker pool, recovery
│   ├── web/                    # REST API + static embedding of web build
│   └── config/                 # layered config (defaults/file/env), clamping
├── web/                        # React SPA source (independent build)
├── migrations/                 # versioned SQL migrations
├── examples/                   # sample configs, extension.yaml
├── docs/
├── tests/                      # integration + extension two-process tests
└── go.mod
```

Dependency rule: `extension → knowledge/ingest/retrieval/...`; nothing below `knowledge` imports `extension` or any Agent DTO.

## 5. Data domain

Default root `~/.shutu/knowledge/` (override via `SHUTU_KNOWLEDGE_HOME`):

```text
knowledge.db      # all business state + chunk index + FTS + vectors (versioned schema)
raw/              # original source bytes: <baseId>/<docId><ext> or directory-relative trees
models/           # downloaded local model weights (helper-owned cache)
cache/ tmp/ logs/
```

`knowledge.db` owns: bases, documents, chunks (+ float32 embedding BLOB + embedding-text hash + model tag), config overrides, groups, enabled scope, jobs, retrieval history. Agent state is never touched.

Core tables (initial plan, evolved only via migrations): `bases`, `documents`, `chunks`, `chunk_fts` (external-content FTS5 trigram + triggers), `jobs`, `schema_migrations`, `kv` (config/groups/scope).

## 6. Retrieval pipeline

```text
query (+ optional variants)
  → query planner (current-turn-first for auto RAG)
  → lexical lane: FTS5 trigram BM25 (word terms ≥3 chars + CJK trigrams ≤64, LIKE for 1-2 char terms)
  → vector lane: EmbeddingProvider → brute-force cosine over scoped BLOBs (dimension-validated)
  → weighted RRF (k=60, vector weight rrfVectorWeight) → MMR (optional) → reranker (optional, strict validation, circuit breaker)
  → context composer: before → anchor → after, heading-bounded, token-budgeted, ≥24-char overlap dedup, focus-centered cropping
  → SearchHit{text, scores, contextWindow, anchorChunkId, chunkIndex}
```

Behavior parity targets are pinned by tests: RRF math, tie stability, fail-closed filters, rerank degradation, window budgets (768/hit explicit, 180/640 auto, 1600 default continuation), threshold applicability.

## 7. Document lifecycle

States: `pending → processing{parsing|embedding} → ready | failed`, plus `stale` (config/model drift) and `incomplete` (crash marker, recoverable when rawText/rawFile exists). Startup: remove unrecoverable placeholders, resume or fail incomplete imports per config, reconcile chunk counts and orphan raws.

Jobs: directory import, reindex (doc/base/bulk), URL refresh, batch files (bounded pool). All jobs: persisted status, cancel, progress, crash-safe resume, non-blocking to web/extension.

## 8. Extension integration (v1)

Per `agent_contract_mapping.md`: tools capability with 14 tools (read/write/destructive risks; approval on deletes), context provider `on_user_input_change` (configurable, incl. `after_tool_result`), web contribution with route + own HTTP server, health capability (DB/migrations gate readiness), lifecycle with on-failure restart, minimal permissions (`session.id`, `user.input`, `session.turn`, `session.step`, `workspace.path`), no event subscriptions initially.

Auto-RAG behavior parity (current-turn-first planner, score gates, per-base seats, throttle/dedup, 4 s budget, untrusted framing) is implemented inside the provider path; it must degrade to no-injection on any failure and never block the Agent.

## 9. Configuration

Layers: built-in defaults → `config.yaml` (or `config.json`) in the data dir → env (`SHUTU_KNOWLEDGE_*`, `KNOWLEDGE_API_KEY`, `KNOWLEDGE_RERANK_API_KEY`, `HF_ENDPOINT`, `HTTP(S)_PROXY`) → per-base overrides. Numeric fields clamped on resolve (parity with dsh ranges). Manifest `configurationSchema` mirrors the host-presentable subset; business config storage stays internal.

## 10. Observability

Structured logs (ingest/parse/embed/retrieval/rerank durations, candidate/context counts, job failures, model errors) with secret/document-content redaction by default; `/health` + `/api/status` structured subsystems; `doctor` CLI (database, migrations, storage permissions, parser/OCR/model availability, extension config). Optional metrics counters exposed via the status API.

## 11. Testing strategy

1. Unit: chunker (incl. scored breaks, semantic merge, token refinement), RRF/MMR/BM25 normalization, context composer (budgets/dedup/focus), config clamping, hash reuse, path guards.
2. Integration: store + migrations, parser matrix (incl. GBK decode, zip guard), job recovery (kill points), URL refresh.
3. Extension two-process tests: real shutu-agent + shutu-knowledge — discovery, handshake, health, tools in registry (incl. approval), context injection evidence, web navigation, restart, clean shutdown, removal safety.
4. Web: API contract tests + UI build tests; navigation appears via `/api/extensions`.
5. Benchmarks: fixed zh/en corpus (parity with dsh benchmark shape), Hit@k / Recall@k / MRR / sentence-level context recall, latency baselines for each stage.

## 12. Risk register (phase 0 view)

| Risk | Mitigation |
|---|---|
| FTS5 parity differences between node:sqlite and modernc | pin tokenizer behavior with golden tests; document query grammar parity |
| Local ML helper not installed | remote providers + lexical fallback; doctor guidance; never blocks import completion |
| Context provider latency | internal deadline (4 s auto path), lexical-only degradation, no retry storm |
| Large imports blocking | worker pool + persisted jobs + cancel/resume |
| License contamination from dsh (AGPL-3.0) | clean-room behavior reimplementation; no source copying; `source_reuse_inventory.md` tracks references |
