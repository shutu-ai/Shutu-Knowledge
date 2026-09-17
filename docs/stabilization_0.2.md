# Shutu-Knowledge 0.2 Stabilization Scope

Audit date: 2026-09-17

This document freezes the 0.2 scope around the Windows Tier-1 personal
knowledge-base path. It does not reopen the architecture plan or add new
retrieval/runtime subsystems.

## Release blockers (P0)

| Item | Current state | Required closure evidence |
|---|---|---|
| Managed runtime smoke | In progress in the current worktree; the smoke reached real embedding and rerank inference, then entered the intentional model lifecycle reinstall path. | Complete current-source embedding, rerank, OCR, PDF, corruption recovery, lifecycle, and offline restart smoke. |
| Formal release package | Existing package builder and static audit are present, but must be run after the final candidate is clean. | ZIP extraction, checksums, required notices, secret audit, packaged startup/import/search/offline restart. |
| Agent integration | Existing protocol, catalog, removal, and real-process tests are present. | Re-run the current candidate's Agent integration/removal gate or record the external Agent binary as unavailable. |

No new P0 correctness defect remains open after the storage shutdown fix: the
full serial race suite passed after the fix. The three rows above remain gates
until their current-candidate evidence is recorded.

## Must fix (P1)

- Keep the Web E2E selector tied to stable form semantics (`name="path"`),
  not translated heading text.
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

## Deferred from 0.2 Release Gate

The unfinished architecture-plan scale and release-host experiments are
explicitly deferred. They must not be used to keep the Windows personal
knowledge-base release open after the hard gates in
`docs/release_0.2_acceptance.md` pass.
