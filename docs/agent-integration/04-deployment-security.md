# Deployment, Security, and Operations

Status: **Phase 5 hardening implemented; package gates re-runnable per release.**


## Release verification record (this cycle)
- `scripts/package_release.ps1 -Platform windows-amd64` from the clean Phase 5
  checkout produced `shutu-knowledge-0.7.0-windows-amd64.zip` (14,270,435 bytes,
  SHA256 `311fa9…d0929`, Git `2cbb742`); the built-in static secret audit passed.
- The packaged binary was extracted to an empty directory (no dev tree) and
  verified: `version` reports the matching commit and storage contract;
  `doctor` reports `status=ready` on a fresh data home; the packaged `mcp`
  stdio mode answered a real `initialize` JSON-RPC line with the server
  capabilities and no banner or progress output.
- The full OCR runtime smoke (`package_runtime_smoke.ps1`) requires the external
  OCR container image and sample office documents; it remains a release-host
  gate to run where those artifacts exist.
- `go test ./... -count=1` passed on the same commit, including the new
  security tests in `internal/mcp/security_test.go`.
## Security baseline (implemented)

| Threat (plan §10.1) | Control | Where |
|---|---|---|
| Unauthorized cross-base reads | `base_ids` intersect the enabled scope; unknown IDs fail `PERMISSION_DENIED`/`BASE_NOT_FOUND`; evidence/document reads verify `document.BaseID` inside the scope and fail closed with `EVIDENCE_UNRESOLVED` | `agentquery.resolveScope`, `internal/mcp` evidence resolution |
| IDOR via cross-referenced IDs | unit/document/chunk pointers must resolve inside one authorized base+generation; out-of-scope content never enters an envelope (negative test `TestMCPCrossBaseEvidenceIsolation`) | `internal/mcp/security_test.go` |
| Public exposure of the local MCP | HTTP binds loopback only; non-loopback refuses to start without `--allow-remote` (AT-17); Host header must be loopback (DNS rebinding); browser `Origin` must be loopback or the request is 403 (`TestHTTPOptionsValidateRejectsNonLoopback`, `TestMCPHTTPMiddlewareRejectsForeignHostAndOrigin`) | `internal/mcp/http.go` |
| Path traversal / SSRF via resource URIs | URIs are logical identifiers parsed into segments; no filesystem access, no outbound fetch; unknown segments return resource-not-found | `internal/mcp/resources.go` |
| Prompt injection via documents | document text is returned verbatim as inert data; the Skill forbids treating it as instructions; fixture proves injected text passes through with contract intact (`TestMCPPromptInjectionFixture`) | `internal/mcp/security_test.go`, skill citation policy |
| DoS by oversized or excessive queries | query byte ceiling (8 KiB → `RESOURCE_LIMIT`), top_k/hops/nodes/fan-out ceilings in the shared layer, per-call deadline (120 s → `TIMEOUT`), concurrency cap (8; overflow → `RESOURCE_LIMIT`) | `internal/mcp/limits.go`, `agentquery` traversal bounds |
| Trace leakage | trace records store tool name, timing, scope, degraded/warning codes only — never query text or document bodies (asserted in wire tests); stdio runs inside the OS user; no privilege escalation | `internal/mcp/contract.go` |
| Secret exposure | the server keeps no credentials; HTTP token handling is delegated to the reverse proxy when `--allow-remote` is used | this document |

Existing REST behavior keeps its own surface unchanged; scope enforcement for REST
search is shared through the same `agentquery` layer.

## Deployment modes

### Local single-user (default)

```bash
shutu-knowledge mcp                      # stdio, OS-user permissions
shutu-knowledge mcp --http 127.0.0.1:7801
```

Personal/local deployments are single-user. Do not advertise them as multi-tenant.

### Remote / multi-user

Requires deliberate work beyond this binary: TLS, authentication, and a reverse
proxy. Start with `--allow-remote` only after that stack exists, and keep the
knowledge store on a filesystem the service user can read. Multi-tenant isolation
of the SQLite layout has not been audited for hostile tenants.

## Limits reference

| Limit | Value | On breach |
|---|---|---|
| Query bytes | 8192 | `RESOURCE_LIMIT` |
| Concurrent tool calls | 8 per process | `RESOURCE_LIMIT` after 10 s wait |
| Tool call deadline | 120 s | `TIMEOUT` |
| Explore hops / nodes / fan-out | 4 / 256 / 64 (defaults 1 / 64 / 16) | silent clamp + `truncated` flag |
| Expand roots | 8 per call | `INVALID_ARGUMENT` |

## Packaging and upgrade/rollback

- `scripts/package_release.ps1` builds the formal Windows/Linux package from a
  clean candidate checkout; `cmd/release-acceptance` verifies the archive
  identity and runs the test groups; `scripts/package_runtime_smoke.ps1` and
  `scripts/audit_release_package.ps1` cover runtime and audit gates.
- Upgrades are drop-in: the SQLite storage enforces reader/writer contract
  versions, and an older binary refuses a newer database instead of corrupting
  it. Roll back by replacing the binary with the previous release; data written
  by a newer writer cannot be read by an older reader — restore the backup taken
  before upgrade if a downgrade past a writer bump is required.
- Keep one backup of the data home before upgrading; the instance lock prevents
  two binaries sharing one data home concurrently.
