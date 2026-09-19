# Shutu-Knowledge 0.5.1 Release Report

This is the formal maintenance closure of the validated temporal-selection fix.
It contains no 0.6 range/phase features and no architecture expansion.

## Release source

* Release branch: `release/0.5.1-prep`
* Intended release source: this commit
* Parent release: `v0.5.0`
* PR #1 merge commit: `c52c59b3ca6e4435a7527a138c8150a39ff9e77e`
* Post-merge master CI: [35468004770](https://github.com/shutu-ai/Shutu-Knowledge/actions/runs/35468004770) — PASS

## Scope audit

PR #1 contained exactly the expected four commits: validation harness/fixtures,
targeted temporal correctness fixes, validation documentation, and CI-evidence
documentation. The business fix `5537771` only touches semantic context
selection, temporal identity handling, Knowledge context evidence projection,
and regression tests; it does not introduce TemporalRange, HistoricalPhase,
storage migration, a new parser, or an API break.

Audit results:

* Private `.local` corpus committed: NO
* Generated secrets found: NO
* Unrelated binary assets found: NO
* Scope expansion: NO

## Validation result

Source: `benchmarks/real_world_internalization/temporal_results/latest.json`

* Queries: 248 (148 temporal/version, 100 regression)
* Strict temporal accuracy: 90.5% (134/148)
* Temporal support proxy: 58.4% → 96.1%
* Conflict accuracy: 100%
* Unknown-version correctness: 100%
* Audited temporal provenance: 170/170 valid
* Unsupported claims: 0
* False supersession risk: 0

The remaining 0.6 gap is ambiguous temporal range/history reasoning; it is
intentionally excluded from this maintenance release.

## Release gate

| Gate | Result | Evidence |
|---|---|---|
| Build | PASS | post-merge master CI run 35468004770 |
| Go Test | PASS | post-merge master CI run 35468004770 |
| Race | PASS | post-merge master CI run 35468004770 |
| Web | PASS | post-merge master CI run 35468004770 |
| Benchmark Smoke | PASS | post-merge master CI run 35468004770 |
| Browser E2E | PASS | post-merge master CI run 35468004770 |
| Temporal regression | PASS | PR CI and committed 248-query result |
| Conflict regression | PASS | 20/20 conflict cases |
| Unknown-version regression | PASS | 8/8 unknown-version cases |
| Temporal provenance | PASS | 170/170 audited units valid |
| Package Smoke | PENDING | formal package smoke before tag |
| Push CI | PENDING | release-source push CI |
| Tag CI | PENDING | immutable tag CI |
| GitHub Release | PENDING | post-Tag CI release |
| No P0 | PASS | no forbidden selection, fabricated metadata, false supersession, or invalid audited provenance |
