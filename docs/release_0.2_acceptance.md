# Shutu-Knowledge 0.2 Windows Release Acceptance

Audit date: 2026-09-17

This is the unique 0.2 release gate. Results refer to the current worktree
and must not be inferred from the older release-readiness report.

## Candidate

| Field | Value |
|---|---|
| Branch | `master` |
| Source HEAD | `0dd6cd54a36922ab4a230aabb8f57241430bc2ee` before this stabilization work |
| Product version | `0.2.0-rc.1+storage.v2` |
| Target | Windows x64 / Tier 1 |
| Go | 1.26.7 windows/amd64 |
| Node/npm | Node 24.19.0 / npm 12.0.2 |

## Hard gates

| Gate | Result | Current evidence |
|---|---|---|
| `go build ./...` | PASS | Current source, serial run on 2026-09-17. |
| `go vet ./...` | PASS | Current source, serial run on 2026-09-17. |
| `go test ./... -count=1` | PASS | Current source, serial run after Web build. |
| `go test -race ./... -p 1 -count=1` | PASS | Second current-source serial run; all packages passed after the `Writer.Stop` fix. |
| `npm ci` | PASS | Current `web/package-lock.json`; 1 package audited, 0 vulnerabilities. |
| Web typecheck / contract / API / build | PASS | `npm.cmd run typecheck`, `npm.cmd test`, and `npm.cmd run build`. |
| Chrome/CDP Web E2E | PASS | Current-source `npm.cmd run test:e2e`; lifecycle, import failure/retry, documents/chunks, reindex/delete, and restart cache checks passed. |
| Windows native host lifecycle | PASS | `go run ./cmd/release-acceptance -profile host -allow-dirty`; result under `.tmp/release-acceptance-current/20260917-170020`. |
| Runtime smoke | NOT PASSED YET | Current run reached real embedding/rerank and is still in the intentional lifecycle reinstall path; do not claim the gate until terminal output is recorded. |
| Document lifecycle | PASS | Current Go lifecycle, generation, delete, reindex, restart, cancellation, and recovery tests; Web E2E also covers import/retry/reindex/delete. |
| Search golden/regression | PASS | `internal/knowledge/retrieval_quality_test.go` and retrieval regression command/tests are included in the current Go suites. |
| Agent integration | NOT RE-RUN IN THIS AUDIT | Existing protocol/catalog/removal evidence is retained in `docs/gates.md`; current-candidate rerun remains required. |
| Fresh package / install / upgrade | NOT RE-RUN IN THIS AUDIT | `scripts/package_release.ps1` and `scripts/package_runtime_smoke.ps1` exist; final clean-candidate package smoke remains required. |
| No P0 defects | NOT READY | Runtime, Agent, and package gates above remain open. |

## Release decision

```text
NOT READY
```

This status is intentionally explicit while current-candidate runtime,
package, and Agent evidence is incomplete. It must be changed to `READY` only
after every hard gate is independently rerun and recorded here.

## Known limitations and deferrals

- Linux is build/test supported; it is not a Windows release blocker.
- macOS and extreme-scale validation are deferred from the 0.2 gate.
- The managed runtime downloads model/OCR data into the user data home; model
  and external runtime terms remain separate from the Apache-2.0 source license
  and are inventoried in `docs/runtime_license_inventory.md`.
- The generated Web build metadata is a shared embedded artifact. Run Web
  build before Go tests; do not run the destructive Web clean/build concurrently
  with Go package initialization.
