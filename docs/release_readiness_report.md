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
| 1 | Repository portability | PASS | `go.mod` requires the public `github.com/shutu-ai/shutu-agent v0.2.0`; committed local replace, `C:/`, `file://`, and developer paths are absent. Web dependencies are locked by `web/package-lock.json`. |
| 2 | Fresh clone build | PENDING VALIDATION | Fresh clone run is recorded after these release-gate changes are published. Local public-module build/vet/test already pass. |
| 3 | Race suite | PENDING VALIDATION | Local Windows `go test -race -count=1 ./...` passed with the public module. Fresh clone result is recorded after publication. |
| 4 | Web gate | PENDING VALIDATION | Local `npm ci` lock generation, `typecheck`, `build`, `test`, and Chrome CDP E2E are exercised by CI and the fresh clone. |
| 5 | GitHub CI | PENDING VALIDATION | The repaired workflow must finish green against the published candidate; local success is not substituted. |
| 6 | Architecture | PASS | CI rejects Agent internal imports; SDK imports are confined to `internal/extension`; the local Agent baseline remains unchanged. |
| 7 | One-way dependency | PASS | Only `internal/extension` imports `github.com/shutu-ai/shutu-agent/sdk/extension`; Agent does not depend on Knowledge. |
| 8 | License | BLOCKED | This project is Apache-2.0 and AGPL dsh-knowledge remains an isolated behavioral reference. Required shutu-agent `v0.2.0` has no repository-level `LICENSE` and its SDK files have no copyright/license headers. Upstream license governance is required; Knowledge must not modify Agent. |
| 9 | Source provenance | PASS | `docs/release_source_provenance_audit.md` records manual and mechanical review. No direct/translated AGPL source copy was found. |
| 10 | Capability status | PASS | Core Go, race, Web contract, E2E, real-process lifecycle/removal, and benchmark suites remain passing; release cleanup changed dependency resolution and CI only. |

## Dependency And License Findings

- `github.com/shutu-ai/shutu-agent v0.2.0` is the oldest public release that
  contains `sdk/extension`. `v0.1.0` does not provide the API used by
  Knowledge. The published `v0.2.0` module compiles all current SDK call sites.
- No substitute local path, relative replace, pseudo-version, or vendored Agent
  source was introduced.
- AGPL dsh-knowledge remains a read-only behavioral reference and is not a Go
  or Web dependency.
- Go dependencies are permissively licensed as detailed in
  [THIRD_PARTY_NOTICES.md](THIRD_PARTY_NOTICES.md). Web production dependencies
  are zero; Node is only a build/test runtime.
- Model and external inference/runtime licenses are explicitly separate from
  this repository's Apache-2.0 source license.

## Conclusion

```text
SHUTU-KNOWLEDGE V1 FUNCTIONALLY READY BUT NOT RELEASE READY
```

The sole release blocker is the missing explicit shutu-agent upstream license
for required module `v0.2.0`. CI, fresh-clone, and race evidence must also be
filled in from real runs before any future release-ready declaration.
