# Phase 7 Equivalence Audit

## Method

This audit re-read the pinned dsh-knowledge source rather than reusing the
Phase 0 conclusion. The comparison focused on `src/knowledge/http.ts`,
`src/knowledge/config.ts`, `src/knowledge/index.ts`, workflow code, tests, and
benchmarks. The reference baseline remained
`95e4a135cca3282b345c6d12a8e09cc4a314402f` and its worktree stayed clean.

The current implementation was checked against its live source in
`internal/{knowledge,web,extension,models,parser,chunk,embedding,retrieval}`
and `web/src`. Statuses were assigned from named Go/API tests and the documented
real Agent-process evidence, not from intention or unchecked plan items.

## Corrections made during this audit

- Added base restore from stored raw text/source bytes and documents.
- Added original raw-file download and `?inline=1` preview.
- Added embedding dimension probe, including local catalog dimensions without
  pretending that an unavailable runtime is ready.
- Added global indexing status as a JSON array.
- Added the upstream 32 MiB request-body bound.
- Added startup reconciliation for orphan raw files and drifted chunk counts.
- Added coordinate reassembly and text-layer health checks for fragmented PDF
  glyph streams, with native-text preservation when OCR is unavailable.
- Added the optional deployment-supplied content-signature converter to the
  PDF fallback chain, with OCR-first ordering and fail-safe native behavior.
- Added image-caption configuration, OpenAI-compatible/Ollama requests, PDF
  raster extraction for bounded Device/ICC and Indexed images, common
  filter chains and predictors, JPEG/DCTDecode, and low-bit samples, with
  best-effort failure preservation.
- Added the PaddleOCR PP-OCRv5 mobile artifact lifecycle, including validated
  dictionary conversion, Web status/download/removal, artifact-gated runtime
  calls, and browser coverage.
- Added legacy-office command hot reload, quoted executable support, and
  real external-process/fail-closed regression coverage.
- Updated the equivalence matrix from stale planned/TODO rows to current
  evidence-backed PASS/PARTIAL/BLOCKED statuses.

## Remaining equivalence work

The largest functional gaps are isolated and have explicit remediation paths:

1. **Local ML runtimes**: the isolated helper-process boundary is now
   implemented and race-tested for embedding/rerank/OCR, including health,
   request deadlines, crash restart, idle lifecycle, and config hot reload.
   Deployment still needs a real inference helper; artifact presence is never
   interpreted as readiness.
2. **Directory ownership/rescan**: directories remain tracked documents,
   with nested containers, explicit repointing, source-path rescan, recursive
   deletion, isolated failure rows, and SPA drilldown/preview.
3. **Auto-RAG policy**: current-turn-first planning now has a bounded
   history-enhanced variant, language/identifier gates, comparable-lane
   relevance gating, same-topic throttling, and context-window injection
   dedup. This is covered by focused policy tests.
4. **Document workflow UX**: grouped base management, base/document rename,
   folder drilldown, text/chunk previews, error/progress rows, and batch
   rebuild, supported-format filtering, 20-file preflight,
   and persistent zh/en localization are implemented.
5. **Storage/network hardening**: the upstream memory-store fallback is not
   applicable to this Knowledge-owned data-domain architecture. Proxy-aware
   outbound HTTP, verified model-cache migration, startup FTS
   optimize, configurable threshold VACUUM, and structured metrics are
   implemented.
6. **Benchmarks**: a repeatable 119-document zh/en corpus now has a
   correctness gate for Hit@k/Recall@k/MRR/context recall and benchmark
   coverage for ingestion, chunking, embedding, lexical, vector, hybrid,
   rerank, and end-to-end RAG.
7. **Configuration**: global processor/MinerU, conflict/URL-refresh,
   auto-retrieve, image-caption, and document-helper defaults now layer
   beneath explicit base overrides with secret-safe API handling. Caption
   raster decoding covers bounded Device/ICC and Indexed images, common filter
   chains and predictors, JPEG/DCTDecode, CCITT Group4/Group3 1D, low-bit
   samples, Separation/DeviceN tint transforms, and soft-mask composition;
   JBIG2 and JPX use an optional deployment-supplied decoder contract with
   JBIG2 globals, bounds, health, hot reload, and fail-closed behavior; PDF
   Pattern is not an image XObject sample codec.

## Hardening gate delta

After this audit, Gates H, I, and J received direct regression or repeatable
external-process evidence. The new OCR-failure test preserves a native PDF text
layer; extension tests cover disabled scope, adapter failures, compatible Agent
versions, and replacement of internal chunking/provider/reranker components;
and `scripts/removal_gate.ps1` compares the real Agent tool catalog and
extension route inventory with Knowledge installed and removed. Evidence and
remaining hardening scope are consolidated in `docs/gates.md`.

## License and reuse review

dsh-knowledge remains AGPL-3.0 at the pinned commit. This repository continues
the behavior-only reimplementation policy: no dsh source is copied or
translated. `THIRD_PARTY_NOTICES.md` records the reference-license boundary and
the third-party dependencies adopted by this implementation. The shutu-agent
baseline also remained unchanged; only its pre-existing, user-owned untracked
assets were present.

The one contract block remains GAP-001 (model-visible proactive guidance).
It is non-blocking and has a documented generic improvement request plus the
legitimate tool-description workaround. GAP-002 remains a watch-level health
payload limitation with a non-breaking own-HTTP workaround.
