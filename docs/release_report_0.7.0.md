# Shutu Knowledge v0.7.0 Release Report

Status: **RELEASED**. v0.7.0 enters **0.7.x MAINTENANCE MODE**. Work does not
automatically proceed to 0.8; future scope must be driven by real-world usage
and the measured gaps below.

## Release provenance

| Field | Value |
|---|---|
| Release source / tag target | `8eb6a34a770addbca85aa67ca9d3db15fbe2c142` |
| Release branch | `feature/0.7-structured-data-model` |
| PR | [#5](https://github.com/shutu-ai/Shutu-Knowledge/pull/5) |
| Merge strategy | Normal repository merge commit |
| Merged master SHA | `99add9046ee1151289605144dce0bd33de048690` |
| Merge parents | `01eca25f9318ef9b7f9a55cb421627eba1da0bcb`, `8eb6a34a770addbca85aa67ca9d3db15fbe2c142` |
| Tag | `v0.7.0` |
| Tag object | `aed7a73f6c7ce8e9a14534826bde52ef7f1b88a9` |
| Tag target | `8eb6a34a770addbca85aa67ca9d3db15fbe2c142` |

The merge commit changed ancestry, not source content. `git diff` between the
release source and merged master is empty for the release tree, so the
pre-verified formal artifact remains valid and was not rebuilt or retargeted.
The tag points to the exact release source, not the merge commit.

## CI and gates

| Gate / run | Result |
|---|---|
| Candidate push CI `35844142371` | PASS |
| Candidate full dispatch CI `35847088210` | PASS |
| Immutable Tag CI `35856909882` | PASS |
| Build | PASS |
| Race | PASS |
| Benchmark smoke | PASS |
| Browser E2E | PASS |
| Runtime release | PASS |
| Release package | PASS |
| Windows / macOS / Ubuntu release host | PASS |

Tag CI head SHA was exactly
`8eb6a34a770addbca85aa67ca9d3db15fbe2c142`.

## GitHub Release and artifact

| Field | Value |
|---|---|
| GitHub Release | **PUBLISHED** |
| Title | `Shutu Knowledge v0.7.0` |
| URL | `https://github.com/shutu-ai/Shutu-Knowledge/releases/tag/v0.7.0` |
| Asset | `shutu-knowledge-0.7.0-windows-amd64.zip` |
| Asset size | 13,005,775 bytes |
| ZIP SHA-256 | `1b4c77387516b056a6876f7ec9f831859530dfd00197a5b4f484a4eb7d617487` |
| Binary SHA-256 | `89a938baf0205d7e9535a7a70dcfa11286950ab0dc25679d2be586a1ae650988` |

The published asset was re-downloaded, extracted, and verified byte-for-byte by
SHA-256. Extracted `BUILD-METADATA.json` reports version `0.7.0`, git SHA
`8eb6a34a770addbca85aa67ca9d3db15fbe2c142`, and the expected embedded binary
hash.

## Published package smoke

`scripts/package_runtime_smoke.ps1` was run against the re-downloaded published
ZIP:

- Result: **PASS**
- Packaged startup: PASS
- Text import: PASS
- OCR import: PASS
- Embedding/vector retrieval: PASS
- Runtime status: PASS
- Offline restart retrieval: PASS
- Package SHA used by smoke: `1b4c77387516b056a6876f7ec9f831859530dfd00197a5b4f484a4eb7d617487`

## Real-world validation classification

Final classification: **ACCEPTABLE**.

Compiled fixed-corpus snapshot:

| Object | Count |
|---|---:|
| Logical tables | 4,194 |
| Fields | 79,685 |
| Enums | 2,574 |
| Business concepts | 323 |
| Key candidates | 21,295 |

Fixed benchmark result:

| Category | v0.6.4 | v0.7.0 |
|---|---:|---:|
| Exact | 50/50 | 50/50 |
| Semantic reverse | 7/30 | 23/30 |
| Cross-workbook | 0/30 | 18/30 |
| Analysis | 2–3/20 | 12/20 |
| Join/data-model | 3/20 | 18/20 |
| Unsupported join claims | — | 0 |
| Same-name wrong merges | — | 0 |

These are real residual gaps and remain explicit acceptance criteria for
maintenance work. The release does not claim perfect semantic reasoning, SQL
generation/execution, GraphRAG, ontology support, or autonomous recursive
research.

## Closure checks

- Local `master` equals `origin/master` after the documentation-only closure.
- Working tree is clean except intentionally untracked local task notes.
- `v0.7.0` is annotated and immutable; it was not moved or recreated.
- `v0.6.4` and earlier tags were preserved.
- No private `.tmp`, `.local`, corpus, workbook, raw QA, field list, or
  proprietary data was committed.
