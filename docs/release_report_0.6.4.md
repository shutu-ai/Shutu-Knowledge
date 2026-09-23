# Shutu Knowledge v0.6.4 Release Report

Status: **RELEASED**. Stage A is complete. Stage B / v0.7 remains a separate
future work stream.

Type: MAINTENANCE RELEASE (generation GC / cross-process historical reader
correctness and release-validation closure).
Baseline: v0.6.3 (`4699a697674020ce5e90b69523a764917cb8bf55`).
Release source / tag target: `39926aa84ed6321871eddbe57f4943b8fa851a7a`.

## Scope audit

The packaged release source includes investigation commit
`becdf9a1a113b79dbf8e2980e7ddfe06f1248d1f` and documentation commit
`39926aa84ed6321871eddbe57f4943b8fa851a7a`.

Production changes are limited to ordering of two existing retired-generation
GC deletion phases. No schema change, API change, retrieval redesign, feature
expansion, or v0.7 implementation is included. The remaining change is release
validation synchronization and evidence.

Detailed investigation: `docs/v0.6.3_tag_race_failure_analysis.md`.

## Classification

- v0.6.3 Tag CI failure: test synchronization bug (zero-byte marker read).
- TOCTOU audit finding: production retired-generation GC could expose a
  partial generation to a newly started reader between chunk and mapping
  deletion. v0.6.4 fixes this with mapping-first fail-closed deletion.

## Validation gates

Pre-tag exact-source evidence on `39926aa84ed6321871eddbe57f4943b8fa851a7a`:

- Workflow-dispatch run `35737133516`: **PASS**.
- Gates: build, race, benchmark smoke, browser E2E, runtime-release,
  release-package, and Windows/macOS/Ubuntu release-host.
- Local correctness evidence: cross-process race soak 20/20 PASS; targeted
  normal 20/20 PASS; package normal PASS; package race PASS; full
  `go test ./...` PASS; `go vet ./...` PASS; web build/typecheck/tests PASS.

Immutable tag CI:

- Tag CI run: `35822441526`.
- Source SHA: `39926aa84ed6321871eddbe57f4943b8fa851a7a`.
- Result: **PASS**.
- Gates: build/race/benchmark/E2E, runtime-release, release-package, and
  Windows/macOS/Ubuntu release-host all passed.

## Immutable release tag

| Field | Value |
| --- | --- |
| Tag | `v0.6.4` |
| Tag object | `2d9e9f59a456d8a1c4fc2b9b8a82a401fc50f488` |
| Tag target | `39926aa84ed6321871eddbe57f4943b8fa851a7a` |

The tag is annotated and immutable. It was not moved, recreated, overwritten,
or retargeted after push.

## GitHub Release and artifact

| Field | Value |
| --- | --- |
| GitHub Release | **PUBLISHED** |
| Title | `Shutu Knowledge v0.6.4` |
| Published at | `2026-09-23T06:00:53Z` |
| Release URL | `https://github.com/shutu-ai/Shutu-Knowledge/releases/tag/v0.6.4` |
| Asset | `shutu-knowledge-0.6.4-windows-amd64.zip` |
| Asset size | 12,658,426 bytes |
| ZIP SHA-256 | `23a4d185d2e86a091c5458d48c206eebc6a64a07be4bb7c44a6ca99e4c4521b9` |
| Embedded binary SHA-256 | `6b153e83781dc8f260e45fa78d8a1b2f6be19f459d9eb2f26bd6debbba54fc7e` |

The asset was built from the exact release source in CI run
`35737133516`. Post-download verification found the expected size and both
exact hashes. Extracted `BUILD-METADATA.json` reports version `0.6.4`, git SHA
`39926aa84ed6321871eddbe57f4943b8fa851a7a`, and the expected binary hash.

`scripts/package_runtime_smoke.ps1` was run against the re-downloaded published
ZIP. Result: **PASS**, including packaged startup, text and OCR import,
embedding/vector retrieval, runtime status, and offline restart retrieval.
