# Shutu-Knowledge 0.5.0 Release Report

Release objective: select the applicable version/current truth instead of
returning every chronologically relevant version to the model.

## Scope shipped

* Optional VersionIdentity, release, published/effective, and source authority
  metadata on Knowledge Units.
* Conservative supersession with evidence-backed relations; conflict remains a
  separate state.
* Temporal query understanding for CURRENT, explicit version, HISTORICAL,
  EVOLUTION, COMPARE, VALIDITY, introduced, and removed intents.
* Scope-aware semantic retrieval and exact-evidence context selection.
* Temporal diagnostics in routing/context APIs and Knowledge Explorer.
* Compatible metadata-envelope migration: absent 0.4 temporal fields remain
  UNKNOWN/null without a required recompile.

## Validation

See:

* `docs/0.5_temporal_validation.md`
* `docs/0.5_temporal_gap_analysis.md`
* `docs/0.5_temporal_error_taxonomy.md`
* `benchmarks/real_world_internalization/results/latest.json`
* `benchmarks/real_world_internalization/results/baseline_0.4.json`

Headline result: the original temporal subset improves 0.821 → 1.000, current
release selection improves 0.333 → 0.881, and semantic context falls
1520.1 → 1498.0 tokens on the 120-query like-for-like comparison. Expanded
temporal term criteria is 0.994.

## Release gate

| Gate | Result | Evidence |
|---|---|---|
| Build | PASS | `go build ./...`; formal package build |
| Go Test | PASS | `go test ./...` |
| Race | PASS | `go test -race ./... -p 1 -timeout=30m` |
| Web | PASS | build, typecheck, contract/API tests |
| 0.4 Migration | PASS | 0019 additive schema + metadata envelope compatibility tests |
| 0.4 Regression | PASS | 120-query common comparison; citations unchanged at 1.000 |
| Temporal Metadata | PASS | semantic/temporal tests and 91-unit provenance audit |
| Version Identity | PASS | semantic/release/3GPP comparison tests; UNKNOWN when incomparable |
| Supersession | PASS | 30-pair exhaustive fixture; zero false real-corpus supersessions |
| Conflict Model | PASS | same-version contradiction fixtures remain queryable/conflicted |
| Temporal Compilation | PASS | real corpus compilation and audit |
| Temporal Query Routing | PASS | deterministic router tests and expanded benchmark |
| Version-aware Retrieval | PASS | current/historical/explicit scope tests |
| Version-aware Context | PASS | exact-evidence scope narrowing and rendered temporal section |
| Current Query | PASS | current criteria 1.000; no forbidden selection |
| Historical Query | PASS | historical criteria 1.000 |
| Explicit Version | PASS | explicit criteria 0.975 |
| Evolution Query | PASS | criteria 1.000; support proxy 0.611 |
| Comparison Query | PASS | support proxy 0.889 |
| Conflict Query | PASS | support proxy 0.889; both conflicting sources surfaced |
| Temporal Provenance | PASS | 91/91 audited units valid |
| Citation Accuracy | PASS | 1.000 |
| Unsupported Claims | PASS | 0 |
| Incremental Update | PASS | benchmark update path |
| Delete | PASS | benchmark delete path |
| Rollback | PASS | generation rollback tests |
| Restart | PASS | semantic restart/recovery test |
| Context Efficiency | PASS | common context tokens 1520.1 → 1498.0 |
| Performance Regression | PASS | support criteria improves; temporal context/latency remains bounded |
| Agent Integration | PASS | context/evidence contract tests; backward-compatible JSON |
| Windows Package Smoke | PASS | formal package online + offline restart smoke |
| Push CI | PASS | [GitHub Actions run 35450225989](https://github.com/shutu-ai/Shutu-Knowledge/actions/runs/35450225989), commit `9bdf28993dd15effceaf04b3eed73d46df93e695` |
| Tag CI | NOT RUN | tag promotion requires explicit owner approval |
| No P0 | PASS | no forbidden selection or false supersession observed |

## Candidate artifact

Verified Windows package produced from release-branch commit
`9bdf28993dd15effceaf04b3eed73d46df93e695`:

```text
filename: shutu-knowledge-0.5.0-windows-amd64.zip
size: 12621004 bytes
SHA-256: 813e8c88aa3bff125d07adfa0a6c9c69fddc86491c84dc418c2a27d9c563b90c
```

The same artifact passed online retrieval, runtime status, OCR ingestion, and
offline restart smoke.

## Release status

```text
NOT READY
```

Product gates and push CI pass. Release promotion remains intentionally blocked
pending explicit owner approval to create the versioned `v0.5.0` tag and run
tag CI.
