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
| Push CI | PENDING | required before release promotion |
| Tag CI | PENDING | required before release promotion |
| No P0 | PASS | no forbidden selection or false supersession observed |

## Candidate artifact

Initial verified Windows package produced from temporal implementation commit
`6be7affeb86ebfa44f31e899bf8a14b6c6331c23`:

```text
filename: shutu-knowledge-0.5.0-windows-amd64.zip
size: 12620990 bytes
SHA-256: 791f11242da870b5126fd35824b6ce12f2f52a91a3f2b4d7edc6c96c01eda2fc
```

A final artifact is rebuilt from the promoted documentation-only commit after
CI/tag evidence; product code is unchanged. This section records the actual
verified package used for Windows smoke.

## Release status

```text
NOT READY
```

The product gates pass, but release promotion remains blocked on observed push
and tag CI.
