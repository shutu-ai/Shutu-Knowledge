# Definition of Done Gates

Status values are `PASS` and `NOT PASSED`. A gate passes only when the cited
current-state evidence covers its acceptance criterion. The project conclusion
is recorded by
[shutu_knowledge_implementation_report.md](../shutu_knowledge_implementation_report.md).

| Gate | Criterion | Status | Evidence |
|---|---|---|---|
| A | One-way dependency on the public Agent Extension API | PASS | `internal/extension` is the sole production importer of `github.com/shutu-ai/shutu-agent/sdk/extension`; Knowledge Core packages do not import Agent SDK types. The pinned Agent reference remained read-only (see `docs/source_reuse_inventory.md`). |
| B | No Agent internal imports | PASS | `.github/workflows/ci.yml` fails on any `shutu-agent/internal` Go import. The current repository search returns no matches; only the public `sdk/extension` import is present. |
| C | Capability equivalence | PASS | `docs/dsh_knowledge_equivalence_matrix.md` has evidence-backed rows and a completed 25-area test parity audit. Target capability rows are `PASS`, one Agent Contract gap is `BLOCKED`, and one upstream-only fallback is `NOT APPLICABLE`; there are no `PARTIAL` rows. |
| D | Native context injection | PASS | `docs/agent_integration.md` records a real Agent + Knowledge process turn where user input triggered retrieval and durable Agent events contain the Knowledge evidence before the intentionally failing model request. |
| E | Tools and approval | PASS | The same real-process evidence shows all 14 tools in the Agent catalog and destructive deletes declared approval-required. Adapter-level lifecycle and scope tests are in `internal/extension`. |
| F | Native Web contribution | PASS | The real-process evidence records the Agent-discovered `/extensions/shutu-knowledge/` route with navigation enabled, a ready extension inventory entry, and an authenticated reverse-proxy import through the Knowledge API. |
| G | Lifecycle | PASS | The real-process evidence covers discovery, Protocol v1 initialize, health ready, Knowledge process restart, Agent shutdown, and absence of an orphaned Knowledge child. |
| H | Failure isolation | PASS | Knowledge crash/restart and Web unavailability are covered by the real-process kill/restart evidence. `TestFailureIsolationAdapters` covers unavailable embeddings and bad documents while health remains ready; `TestRerankerTimeoutDegradesQuickly` bounds rerank timeout; `TestOCRFailureKeepsNativeText` preserves the native PDF text when OCR is unavailable; `TestOCRRendererFailureFallsBackToEmbeddedRasters` isolates real renderer failures; `TestEmbeddedRasterFallbackRunsWhenPDFEnvelopeOCRFails` bounds the embedded-raster retry path. |
| I | Removal/disable isolation | PASS | `scripts/removal_gate.ps1` starts the pinned Agent web-only process with a temporary absolute Knowledge command. The latest run reports `InstalledTools=14`, `RemovedTools=0`, `InstalledRoutes=1`, `RemovedRoutes=0`, and `RemovedAgentHealthy=True`. `TestDisabledScopeIsolation` additionally proves that a disabled Knowledge scope returns no context, fails tools closed, and keeps process health ready. |
| J | Independent upgrade | PASS | `TestProtocolUpgradeIndependence` completes Protocol v1 sessions with Agent versions `0.9.0` and `1.1.0`, then changes Knowledge's chunking, embedding, and reranker internals and uses the unchanged tool contract; the replacement reranker is observed applied. Parser registry and MinerU flow tests exercise alternate parsing through the same Knowledge service API. |

## Repeatability

From the repository root:

```powershell
go test ./internal/extension -run '^Test(DisabledScopeIsolation|FailureIsolationAdapters|ProtocolUpgradeIndependence)$' -count=1
go test ./internal/knowledge -run '^Test(RerankerTimeoutDegradesQuickly|OCRFailureKeepsNativeText|EmbeddedRasterFallbackRunsWhenPDFEnvelopeOCRFails)$' -count=1
go test ./internal/knowledge -run '^Test(RenderedPDFPagesPreferredOverPDFEnvelope|OCRRendererFailureFallsBackToEmbeddedRasters|SetGlobalConfigHotReloadsOCRRenderer)$' -count=1
go test ./internal/parser -run '^TestParseRenderedPDFPages' -count=1
cd web; npm run test:e2e
powershell -NoProfile -ExecutionPolicy Bypass -File scripts/removal_gate.ps1
```

The removal gate requires the pinned Agent binary and its built web assets. It
uses a temporary data directory and ephemeral loopback port and does not modify
either reference repository.
