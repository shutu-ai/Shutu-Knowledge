# Shutu Knowledge Implementation Report

Date: 2026-09-06

## Capability Result

The evidence-backed equivalence matrix currently records:

- `PASS`: 53 target capabilities,
- `BLOCKED`: 1 capability,
- `NOT APPLICABLE`: 1 target capability.

The matrix is the authoritative capability-level detail. It contains no
`PARTIAL` capability rows.

## Required Answers

1. **Does this implement all dsh-knowledge target capabilities?** At the
   capability-contract level, yes: 53 matrix rows pass with current-state test
   evidence, one is blocked by a documented Extension v1 gap, and one is not
   applicable. OCR fallback orchestration, bounded embedded-raster retries,
   preprocessing, CJK output cleanup, and the optional deployment-supplied
   full-page renderer contract are implemented and tested. Actual rendering and
   OCR runtimes remain deployment-supplied and are never reported ready merely
   because their contracts exist. JBIG2 and JPX
   The 25-area test parity audit is complete and recorded in
   `docs/test_parity.md`. Local embedding and rerank inference runtimes are
   intentionally deployment-supplied through isolated helpers. The blocked row
   is model-visible proactive guidance because Extension v1 has no
   prompt-guidance channel; GAP-001 documents the non-breaking
   tool-description workaround.
2. **Was shutu-agent modified?** No. The pinned Agent repositories remain
   read-only. Evidence and baselines are recorded in
   [docs/source_reuse_inventory.md](docs/source_reuse_inventory.md).
3. **Does production import `shutu-agent/internal/...`?** No. Only
   `internal/extension` imports the public
   `github.com/shutu-ai/shutu-agent/sdk/extension` package, and CI rejects any
   Agent-internal import.
4. **Do Agent Contract Gaps exist?** Yes: GAP-001 (model-visible extension
   guidance) and GAP-002 (structured extension health payload). Both are
   non-blocking, documented generically, and have no bypass or Agent
   modification.
5. **Are automatic RAG, tools, approval, Web, events, and lifecycle
   implemented?** Yes, through the documented Extension v1 mechanisms. The
   real-process evidence includes context injection, 14 catalog tools,
   destructive approval metadata, native navigation/reverse proxy, explicit
   empty event subscription, lifecycle restart, and clean shutdown.
6. **Was real two-process Agent integration run?** Yes.
   [docs/agent_integration.md](docs/agent_integration.md) records discovery,
   Protocol v1 negotiation, health ready, tools, context injection, Web proxy
   import, Knowledge kill/restart, and Agent shutdown/orphan checks.

## Hardening And Gate Status

Current Gate A-J evidence is consolidated in
[docs/gates.md](docs/gates.md). All gates A through J pass. Security controls and residual
deployment risks are recorded in
[docs/security_review.md](docs/security_review.md). The repeatable removal run
reported 14 installed/0 removed tools, 1 installed/0 removed native routes, and
a healthy Agent in removed mode.

The renderer increment was verified successfully with:

```text
go build ./...
go vet ./...
go test -race ./...
go test ./internal/knowledge -run '^$' -bench Benchmark -benchtime=1x
web: npm run build
web: npm test
web: npm run test:e2e
scripts/removal_gate.ps1
```

The preceding full-suite verification completed successfully for:

```text
go build ./...
go vet ./...
go test ./...
go test -race ./...
go test ./internal/knowledge -run '^$' -bench Benchmark -benchtime=1x
web: npm run test:e2e
scripts/removal_gate.ps1
web: npm run build
web: npm test
```

No current automated test failure is known.

## Remaining Blockers To Ready

None. The project is released under Apache-2.0 in [LICENSE](LICENSE).
Agent Contract GAP-001 and GAP-002 remain documented limitations rather than
readiness blockers; their workarounds and generic improvement requirements are
recorded in `docs/agent_extension_gap_report.md`.

SHUTU-KNOWLEDGE V1 READY
