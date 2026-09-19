# Shutu-Knowledge 0.5.1 Release Report

This is the formal maintenance closure of the validated temporal-selection fix.
It contains no 0.6 range/phase features and no architecture expansion.

## Release source

* Release branch: `release/0.5.1-prep`
* Release source: `580703635740fff53078e622d6e7e94d40d4bb5f`
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
| Package Smoke | PASS | formal smoke passed extract/start/runtime/import/embedding/retrieval/OCR/offline restart; separate packaged temporal API smoke passed current, explicit-version, and historical queries |
| Push CI | PASS | [35469732778](https://github.com/shutu-ai/Shutu-Knowledge/actions/runs/35469732778) at `580703635740fff53078e622d6e7e94d40d4bb5f` |
| Tag CI | PASS | [35471180304](https://github.com/shutu-ai/Shutu-Knowledge/actions/runs/35471180304) at `580703635740fff53078e622d6e7e94d40d4bb5f` |
| GitHub Release | PASS | [Shutu Knowledge v0.5.1](https://github.com/shutu-ai/Shutu-Knowledge/releases/tag/v0.5.1) with hash-verified asset |
| No P0 | PASS | no forbidden selection, fabricated metadata, false supersession, or invalid audited provenance |

## Formal package and smoke evidence


A separate packaged-binary API smoke imported release `1.0.0`, `1.1.0`, and `1.2.0`
documents. After semantic compilation, current selected `release-1.2.0.md`;
explicit release `1.0.0` and historical version `1.0.0` both selected
`release-1.0.0.md` with resolved version `1.0.0`. The raw result is retained
in the local release-work directory and summarized here.

The formal Windows package was built locally from clean release source
`580703635740fff53078e622d6e7e94d40d4bb5f`:

```text
filename: shutu-knowledge-0.5.1-windows-amd64.zip
size: 12627976 bytes
SHA-256: d56b946e7bdf8448aceaa3069bd5f11f7546e7bb7c62e4bbce16dc3c8a870195
binary SHA-256: 6beab787f60e9dacb67ecc678492e4e05361da22e2d0528563fd1bf777497970
```

`scripts/package_runtime_smoke.ps1` passed online retrieval, runtime status,
OCR import, embedding, offline restart, and post-restart retrieval. Its recorded
package hash matched the formal package exactly.

## Tag CI evidence

The annotated `v0.5.1` tag object is
`918e057ec22f84f8d832680daaa6827d35d74724`; it points exactly to
`580703635740fff53078e622d6e7e94d40d4bb5f`.

[Tag CI run 35471180304](https://github.com/shutu-ai/Shutu-Knowledge/actions/runs/35471180304)
completed successfully:

| Job | Result |
|---|---|
| build | PASS |
| release-package | PASS |
| runtime-release | PASS |
| release-host (windows-latest) | PASS |
| release-host (ubuntu-latest) | PASS |
| release-host (macos-latest) | PASS |

Tag CI rebuilt the tagged source successfully. Its `BUILD-METADATA.json`
matched version `0.5.1`, platform `windows-amd64`, storage contract
`2/8/8`, and release source `580703635740fff53078e622d6e7e94d40d4bb5f`.

## GitHub Release evidence

* Release: [Shutu Knowledge v0.5.1](https://github.com/shutu-ai/Shutu-Knowledge/releases/tag/v0.5.1)
* Tag: `v0.5.1`
* Draft: false
* Prerelease: false
* Asset: `shutu-knowledge-0.5.1-windows-amd64.zip`
* Asset size: `12627976` bytes
* Asset state: uploaded
* GitHub-reported digest: `sha256:d56b946e7bdf8448aceaa3069bd5f11f7546e7bb7c62e4bbce16dc3c8a870195`
* Post-upload downloaded SHA-256: `d56b946e7bdf8448aceaa3069bd5f11f7546e7bb7c62e4bbce16dc3c8a870195`

## Release status

```text
READY
```

All product, master, artifact, Tag CI, GitHub Release, and asset-integrity
gates pass. Stage A is closed; Stage B may start on a separate 0.6 branch.

