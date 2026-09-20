# Shutu Knowledge 0.6.0 Release Report

This is the formal Stage B closure for Temporal Range & Historical Reasoning.
It records the merged release source, required CI, benchmark and regression
evidence, package integrity, Tag CI evidence, and the published GitHub asset.

## Release source and scope

* Development branch: `0.6-temporal-range-history`
* Release PR: [#2](https://github.com/shutu-ai/Shutu-Knowledge/pull/2)
* Merge / release source: `a700b4620966be960cdc9ed20550c48f7171fc50`
* Base maintenance release: `v0.5.1`
* Version metadata: `0.6.0`
* Storage contract: format `2`, reader `8`, writer `8`
* Storage migration: none required
* Private `.local` corpus committed: NO

The scope is limited to temporal ranges, relative boundaries, historical phases,
range/history routing, history context compilation, tests, benchmarks, and
release documentation. It does not introduce GraphRAG, a new graph/vector
database, storage migration, parser rewrite, ontology, memory consolidation, or
distributed infrastructure.

## CI evidence

| Gate | Result | Evidence |
|---|---|---|
| Branch push CI | PASS | [35480379577](https://github.com/shutu-ai/Shutu-Knowledge/actions/runs/35480379577) at `d06460ec7d6f2e82eeafa0f17aed1ded327a3055` |
| Pull request CI | PASS | [35480389440](https://github.com/shutu-ai/Shutu-Knowledge/actions/runs/35480389440) at `d06460ec7d6f2e82eeafa0f17aed1ded327a3055` |
| Post-merge master CI | PASS | [35481639799](https://github.com/shutu-ai/Shutu-Knowledge/actions/runs/35481639799) at `a700b4620966be960cdc9ed20550c48f7171fc50` |
| Build / Go test / race | PASS | master and Tag CI |
| Web build/typecheck/tests | PASS | master and Tag CI |
| Browser E2E | PASS | master and final Tag CI attempt |
| Benchmark smoke | PASS | master and Tag CI |
| Storage-writer guard | PASS | master and Tag CI |
| Architecture/portability gates | PASS | master and Tag CI |
| Windows package smoke | PASS | exact-source formal package |
| Tag CI | PASS | [35482888897](https://github.com/shutu-ai/Shutu-Knowledge/actions/runs/35482888897), final attempt |
| GitHub Release | PASS | [Shutu Knowledge v0.6.0](https://github.com/shutu-ai/Shutu-Knowledge/releases/tag/v0.6.0) |

The exact source tree also passed Browser E2E in branch push, pull-request, and
post-merge master CI before tagging.

## Range/history benchmark

The dedicated 84-query benchmark completed at 100% temporal accuracy. Support
criteria and citation rate were 100%; unsupported claims were 0.

| Category | 0.5 | 0.6 | Delta |
|---|---:|---:|---:|
| Before | not exposed as a range gate | 100% (10/10) | new range/history coverage |
| After | not exposed as a range gate | 100% (10/10) | new range/history coverage |
| Range | not exposed as a range gate | 100% (10/10) | new range/history coverage |
| Early History | not exposed as a range gate | 100% (10/10) | new range/history coverage |
| Full History | not exposed as a range gate | 100% (8/8) | new range/history coverage |
| Phase | not exposed as a range gate | 100% (10/10) | new range/history coverage |
| Relative Event | not exposed as a range gate | 100% (8/8) | new range/history coverage |
| Ambiguous | 0% | 100% (8/8) | +100 pp |

Committed artifact:
`benchmarks/real_world_internalization/range_history_results/latest.json`.

## 0.5 temporal regression

The 248-query regression improved from the 0.5 temporal-validation baseline of
90.5% strict accuracy to 96.8% (240/248). Unsupported claims remained 0.

Key categories:

| Category | 0.5 | 0.6 |
|---|---:|---:|
| Current | 66.7% | 58.3% |
| Explicit Version | 96.7% | 97.0% |
| Historical Exact | 100% | 100% |
| Conflict | 100% | 100% |
| Unknown Version | 100% | 100% |
| Global | 100% | 100% |
| Cross-document | 100% | 100% |
| Multi-hop | 100% | 100% |
| Ambiguous Temporal | 0% | 87.5% |

The remaining current-resolution uncertainty is explicitly retained as a
follow-up measurement gap. It was not hidden by weakening the 0.5 scope gates.

## Quality, efficiency, and lifecycle

* Invalid provenance: 0
* Unsupported claims: 0
* Temporal over-interpretation: 0 in controlled ambiguity tests
* Historical phase audit: PASS
* Historical transition audit: PASS
* Average context tokens: 1,272.8
* P95 context tokens: 1,791
* P50 latency: 211 ms
* P95 latency: 373 ms
* Incremental update: PASS in 701 ms; 1,075/1,075 unchanged units reused
* Delete propagation: PASS in 699 ms with no forbidden residue
* Offline restart: PASS

## Formal package

The formal Windows package was rebuilt from clean merged release source
`a700b4620966be960cdc9ed20550c48f7171fc50`, not from the earlier feature-head
candidate:

```text
filename: shutu-knowledge-0.6.0-windows-amd64.zip
size: 12660331 bytes
SHA-256: d0178c173ef0af203af979267716521d9b0830bcb29b1d5a3e424ab716385e1a
binary SHA-256: 6f6b93c1eba4e9bd3776a576ed8e553e57b12bfbbd133f3afac635366da1866b
```

`BUILD-METADATA.json` recorded:

```text
version: 0.6.0
platform: windows-amd64
git_sha: a700b4620966be960cdc9ed20550c48f7171fc50
storage contract: 2/8/8
```

Formal package smoke passed extract, start, runtime status, document import,
embedding, retrieval, OCR, offline restart, and post-restart retrieval. A
separate packaged temporal context smoke passed `CURRENT`,
`AS_OF_VERSION`, and `RANGE_HISTORY` against the exact package.

## Tag evidence

`v0.6.0` is an annotated tag and was pushed once:

* Tag object: `f47b0594678937ed29e107daf26728e5d9ff5faf`
* Target commit: `a700b4620966be960cdc9ed20550c48f7171fc50`
* Tag moved or recreated after push: NO

## Tag CI evidence

[Tag CI run 35482888897](https://github.com/shutu-ai/Shutu-Knowledge/actions/runs/35482888897)
completed with every required job PASS on the immutable tag:

| Job | Result |
|---|---|
| build | PASS |
| release-package | PASS |
| runtime-release | PASS |
| release-host (windows-latest) | PASS |
| release-host (ubuntu-latest) | PASS |
| release-host (macos-latest) | PASS |

The first Browser E2E attempt failed with
`stress produced an error toast`. The failed job was retried without changing
the source, tag object, target commit, workflow, or assertions; it then passed.
The same source had already passed Browser E2E in branch push, pull-request, and
post-merge master CI.

Tag CI rebuilt the tagged source successfully. Its `BUILD-METADATA.json`
matched version `0.6.0`, platform `windows-amd64`, release source
`a700b4620966be960cdc9ed20550c48f7171fc50`, and storage contract `2/8/8`.
Go build artifacts are not assumed to be byte-for-byte reproducible; the local
smoke-tested archive above is the published GitHub asset.

## GitHub Release evidence

* Release: [Shutu Knowledge v0.6.0](https://github.com/shutu-ai/Shutu-Knowledge/releases/tag/v0.6.0)
* Title: `Shutu Knowledge v0.6.0`
* Draft: false
* Prerelease: false
* Published at: `2026-09-20T02:49:17Z`
* Asset: `shutu-knowledge-0.6.0-windows-amd64.zip`
* Asset size: `12660331` bytes
* Asset state: uploaded
* GitHub-reported digest: `sha256:d0178c173ef0af203af979267716521d9b0830bcb29b1d5a3e424ab716385e1a`
* Post-download SHA-256: `d0178c173ef0af203af979267716521d9b0830bcb29b1d5a3e424ab716385e1a`

## Release status

```text
READY
```

All product, CI, package-integrity, Tag CI, GitHub Release, and post-download
asset-verification gates pass.
