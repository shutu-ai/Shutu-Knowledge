# Release Readiness Report

Audit date: 2026-09-06

## Environment

| Item | Value |
|---|---|
| OS | Windows 11 Home (Chinese), x86-64, build 10.0.26200 |
| Go | 1.26.7 windows/amd64 |
| Node | 24.19.0 / npm 12.0.2 |
| Candidate branch | `master` |

## Release Gates

| Gate | Criterion | Status | Evidence |
|---|---|---|---|
| 1 | Repository portability | PASS | `go.mod` requires the public `github.com/shutu-ai/shutu-agent v0.2.1`; no committed local replace, `C:/`, `file://`, developer path, or `go.work` is present. Web dependencies are locked by `web/package-lock.json`. |
| 2 | Fresh clone build | PASS | Fresh GitHub clone of release commit `0c4256b1dc447a08a770da9afa56f5281283cd33` completed `go mod download`, `go build ./...`, `go vet ./...`, and `go test -count=1 ./...`. No local Agent checkout or workspace file was used. |
| 3 | Race suite | PASS | The same isolated Windows/x86-64 clean clone completed `go test -race -count=1 ./...`; the local development tree also passed the identical suite. |
| 4 | Web gate | PASS | In the same clean clone, `npm ci`, `npm run typecheck`, `npm test`, `npm run build`, and `npm run test:e2e` passed. E2E completed with `Chrome/CDP lifecycle passed`; this package has no `verify` script. |
| 5 | GitHub CI | PASS | Release commit `0c4256b1dc447a08a770da9afa56f5281283cd33`: CI run [34036250409](https://github.com/shutu-ai/Shutu-Knowledge/actions/runs/34036250409) completed with `success`. |
| 6 | Architecture | PASS | CI rejects Agent internal imports; SDK imports are confined to `internal/extension`; the local Agent baseline remains unchanged. |
| 7 | One-way dependency | PASS | Only `internal/extension` imports `github.com/shutu-ai/shutu-agent/sdk/extension`; Agent does not depend on Knowledge. |
| 8 | License | PASS | Shutu-Knowledge is Apache-2.0; dsh-knowledge remains an AGPL-3.0 behavioral reference only; `shutu-agent v0.2.1` is explicitly Apache-2.0 licensed and includes `LICENSE`. |
| 9 | Source provenance | PASS | `docs/release_source_provenance_audit.md` records manual and mechanical review. The independently authored managed runtime adds only inventoried third-party packages and model artifacts; no dsh source is reused. |
| 10 | Capability status | PASS | Core Go, race, Web contract, E2E, real-process lifecycle/removal, and benchmark suites remain passing. GAP-001 and GAP-002 remain documented non-blocking upstream contract limitations. |

## Dependency And License Findings

- `github.com/shutu-ai/shutu-agent v0.2.1` is the public release used by this
  project and compiles all current `sdk/extension` call sites. The module
  distribution includes the upstream Apache-2.0 `LICENSE`.
- No substitute local path, relative replace, pseudo-version, or vendored Agent
  source was introduced.
- AGPL dsh-knowledge remains a read-only behavioral reference and is not a Go
  or Web dependency.
- Go dependencies are permissively licensed as detailed in
  [THIRD_PARTY_NOTICES.md](THIRD_PARTY_NOTICES.md). Web production dependencies
  are zero; Node is only a build/test runtime.
- Model and external inference/runtime licenses are explicitly separate from
  this repository's Apache-2.0 source license.

## Final Clean-Room Revalidation

The release candidate was pushed before validation. A new isolated GitHub clone
at `0c4256b1dc447a08a770da9afa56f5281283cd33` passed the complete Go and Web
gates above. `go list -m github.com/shutu-ai/shutu-agent` returned
`github.com/shutu-ai/shutu-agent v0.2.1`; the JSON module record pointed to the
public module cache entry and no local replacement was present.

The local `C:\dev-projects\Agent\shutu-agent` baseline was checked before and
after the release work and remained at
`60730c671d30e30eb910b92a69c621ce9fecfdf0`; the only worktree entries are
pre-existing untracked user-owned files.

```text
SHUTU-KNOWLEDGE V1 RELEASE READY
```
