# Shutu Knowledge v0.6.4 Release Report

Type: MAINTENANCE RELEASE (generation GC / cross-process historical reader
correctness and release-validation closure).
Baseline: v0.6.3 (`4699a697674020ce5e90b69523a764917cb8bf55`).

## Scope audit

Investigation commit: `becdf9a1a113b79dbf8e2980e7ddfe06f1248d1f`. A documentation-only closure commit follows; the final packaged commit is identified authoritatively by `BUILD-METADATA.json` and must match the future tag target.

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

## Software gates

Investigation commit CI: push run `35730789855` PASS (build, race, benchmark smoke, browser E2E); workflow-dispatch run `35733722833` PASS (runtime-release, Windows/macOS/Ubuntu release-host, release-package, build). Local normal/race matrices and web gates are recorded in the analysis report. The documentation-only closure commit requires one new exact-source dispatch before any tag.

## Release provenance

To be completed after formal artifact, immutable tag CI, and explicit
publication approval. This branch intentionally does not create or move a tag.
