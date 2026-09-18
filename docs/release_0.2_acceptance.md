# Shutu-Knowledge 0.2.1 Windows Release Acceptance

Audit date: 2026-09-18

This is the unique 0.2 release gate. Results refer to the current worktree
and must not be inferred from the older release-readiness report.

## Candidate

| Field | Value |
|---|---|
| Branch | `master` |
| Source baseline | Final release commit tagged `v0.2.1`; the prior immutable `v0.2.0` remains a historical candidate |
| Product version | `0.2.1` |
| Target | Windows x64 / Tier 1 |
| Go | 1.26.7 windows/amd64 |
| Node/npm | Node 24.19.0 / npm 12.0.2 |

## Hard gates

| Gate | Result | Current evidence |
|---|---|---|
| `go build ./...` | PASS | Current source, serial run on 2026-09-17. |
| `go vet ./...` | PASS | Current source, serial run on 2026-09-17. |
| `go test ./... -count=1` | PASS | Current source, serial run after Web build. |
| `go test -race ./... -p 1 -count=1` | PASS | Serial race suite; race correctness is checked separately from production latency budgets. |
| Busy HTTP production performance | PASS | Ten normal-build target runs kept `statusP95` at 13.2–16.8ms against the 100ms production budget. |
| Busy HTTP race correctness/boundedness | PASS | Ten race-build target runs had no race, deadlock, timeout, or correctness failure; diagnostic max stayed below the 360ms bounded-completion budget. |
| `npm ci` | PASS | Current `web/package-lock.json`; 1 package audited, 0 vulnerabilities. |
| Web typecheck / contract / API / build | PASS | `npm.cmd run typecheck`, `npm.cmd test`, and `npm.cmd run build`. |
| Chrome/CDP Web E2E | PASS | Current-source `npm.cmd run test:e2e`; lifecycle, import failure/retry, documents/chunks, reindex/delete, and restart cache checks passed. |
| Windows native host lifecycle | PASS | `go run ./cmd/release-acceptance -profile host -allow-dirty`; result under `.tmp/release-acceptance-current/20260917-170020`. |
| Runtime smoke | PASS | `runtime_smoke.go` passed lifecycle, embedding, rerank, PDF/codec failure, OCR, corruption recovery, and offline restart probes. |
| Document lifecycle | PASS | Current Go lifecycle, generation, delete, reindex, restart, cancellation, and recovery tests; Web E2E also covers import/retry/reindex/delete. |
| Search golden/regression | PASS | `internal/knowledge/retrieval_quality_test.go` and retrieval regression command/tests are included in the current Go suites. |
| Agent integration | PASS | `scripts/removal_gate.ps1 -OutputRoot .gocache\removal-gate-current3`: catalog PASS, removed Agent healthy, 18 tools installed and 0 removed/ unexpected. |
| Fresh package / install / upgrade | PASS | Formal ZIP passed static secret audit, extraction/checksum/notice checks, packaged text/OCR import, online vector retrieval, and offline restart retrieval. Package smoke used the prevalidated local model cache; first-download behavior is covered by the direct runtime gate. |
| No P0 defects | PASS | All Windows Tier-1 hard gates above passed; no open P0 remains. |

## Release decision

```text
RELEASED
```

The Windows Tier-1 0.2.1 release is sourced from the immutable `v0.2.1` tag.
The formal package builder reported a clean worktree, required-file/checksum
validation, and a static secret audit with zero findings; packaged runtime
smoke completed the online and offline data path.

## Known limitations and deferrals

- Linux full release-host parity is deferred; basic build/test support remains.
- macOS full release validation and extreme-scale validation are deferred.
- The managed runtime downloads model/OCR data into the user data home; model
  and external runtime terms remain separate from the Apache-2.0 source license
  and are inventoried in `docs/runtime_license_inventory.md`.
- The packaged smoke reuses a separately prevalidated model cache to avoid
  making release acceptance depend on a live model download. A first-download
  path remains covered by the direct managed-runtime smoke and the package
  itself remains cache-free.
- The generated Web build metadata is a shared embedded artifact. Run Web
  build before Go tests; do not run the destructive Web clean/build concurrently
  with Go package initialization.

## Maintenance mode

`0.2.x` is in maintenance mode. P0/P1 bugfixes, security fixes, data
corruption fixes, compatibility fixes, and packaging fixes remain allowed.
Document IR, LLM Wiki, Knowledge Graph, new retrieval/storage architecture,
and large-scale platform expansion are deferred to 0.3.
