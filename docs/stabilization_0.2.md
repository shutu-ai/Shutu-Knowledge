# Shutu-Knowledge 0.2 Stabilization Scope

Audit date: 2026-09-18

This document freezes the 0.2 scope around the Windows Tier-1 personal
knowledge-base path. It does not reopen the architecture plan or add new
retrieval/runtime subsystems.

## Release blockers (P0)

| Item | Current state | Required closure evidence |
|---|---|---|
| Managed runtime smoke | PASS | Current-source embedding, rerank, OCR, PDF, corruption recovery, lifecycle, and offline restart smoke completed. |
| Formal release package | PASS | Clean formal ZIP passed checksums, required notices, static secret audit, packaged startup/import/search, and offline restart. |
| Agent integration | PASS | Current candidate removal gate passed catalog validation, real Agent health, and zero removed/unexpected tools or routes. |
| Release gate semantics | PASS | Normal builds enforce production p95 budgets; race builds enforce concurrency correctness and bounded completion without reusing production latency thresholds. |

No new P0 correctness or production-performance defect remains open. The
immutable `v0.2.0` tag is retained as a historical candidate; `v0.2.1` is the
released source after separating normal production performance from race
correctness validation.

## Must fix (P1)

- Keep the Web E2E selector tied to stable form semantics (`name="path"`),
  not translated heading text.
- Keep package smoke paths absolute and poll durable operation receipts before
  asserting document readiness; the Web API is asynchronous by contract.
- Keep the release commands serial where Go package initialization consumes
  the generated `internal/web/dist/build-info.json`; concurrent Web build and
  Go test can delete that shared embedded output during test startup.
- Preserve actionable health and recovery states for missing/corrupt models,
  OCR/PDF/Office helpers, and failed durable operations.

## Backlog

- Linux/macOS release-host parity beyond build/test support.
- SmartCare-scale corpus and extreme-scale resource validation.
- Multi-user, distributed, PostgreSQL, vector-database, Knowledge Graph, and
  richer document-understanding work.
- A full installer/updater UI if the formal portable ZIP remains insufficient;
  the 0.2 gate uses the existing verified portable Windows package.
- Document IR, LLM Wiki, Knowledge Graph, new retrieval/storage architecture,
  and large-scale platform expansion are 0.3 backlog items.

## Deferred from 0.2 Release Gate

The unfinished architecture-plan scale and release-host experiments are
explicitly deferred. They must not be used to keep the Windows personal
knowledge-base release open after the hard gates in
`docs/release_0.2_acceptance.md` pass.

## Final status

```text
Version: 0.2.1
Status: Released
Primary Platform: Windows x64
Maintenance: Active
```
