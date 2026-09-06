# Upstream Test Parity Audit

This audit maps the 25 upstream Vitest spec areas to the current Knowledge
test layers. It compares the user-visible or integration contract under test,
not the test framework or implementation internals. A single upstream test file
may be covered by several focused Go, web-contract, browser E2E, and real-process
gates here; conversely, an implementation-specific upstream test is satisfied by
the test for the equivalent architecture boundary.

Commands used by this audit are documented in `docs/testing.md`. The browser
policy and chunk-expansion checks run through `web/scripts/contract-test.mjs`
and `web/scripts/e2e.mjs`; process, removal, and protocol-upgrade behavior is
covered by `internal/extension` and `scripts/removal_gate.ps1`.

| # | Upstream spec area | Contract under test | Current evidence | Status |
|---|---|---|---|---|
| 1 | `auto-retrieve-sqlite.spec.ts` | Auto-retrieval scope, base layering, injection dedup/throttle, abort safety, strict identifiers | `TestAutoRetrieveGlobalAndBaseLayering`, `TestAutoQueryPlanningAndSignals`, `TestInjectedEvidenceDedupPreservesFreshAnchor`, `TestAutoRelevanceGateDistinguishesLaneScores`, and `TestDisabledScopeIsolation` | PASS |
| 2 | `auto-retrieve.spec.ts` | Current-turn planning, history enhancement, relevance/keyword/identifier gates, budgets, base seats, rerank degradation, delivery commit, cancellation | `internal/extension/auto_policy_test.go`, `internal/extension/context.go`, `TestAutoRetrieveGlobalAndBaseLayering`, `TestFailureIsolationAdapters`, and `TestRerankerTimeoutDegradesQuickly` | PASS |
| 3 | `chunk-expansion.spec.ts` | Chunk previews expand and collapse individually and as a set; expansion does not disturb sibling state; selector-safe IDs and localized labels | `TestKnowledgeAPIRoundTrip`, `web/scripts/contract-test.mjs` (`stableID`, `chunkBodyID`, `chunkIsExpanded`), and Chrome E2E expansion/collapse assertions | PASS |
| 4 | `chunk.spec.ts` | Heading-aware budgets, overlap, CJK sizing, semantic merge/fallback, fence integrity, and token refinement | `internal/chunk/chunk_test.go` and `internal/chunk/semantic_test.go` | PASS |
| 5 | `chunkdb.spec.ts` | SQLite FTS5 lexical search, CJK/Latin behavior, symbol fail-closed, short-token fallback, scope filters, vector ranking | `internal/retrieval/retrieval_test.go`, `internal/storage/storage_test.go`, and `internal/knowledge/search_test.go` | PASS |
| 6 | `config.spec.ts` | Defaults, deployment/runtime/base layering, clamping, secrets, model configuration, and invalid input rejection | `internal/config/config_test.go`, `TestResolveBaseConfigLayering`, `TestGlobalConfigRoundTripProtectsSecrets`, and `TestBaseAPIKeysAreWriteOnly` | PASS |
| 7 | `context.spec.ts` | Ordered context windows, heading boundaries, anchor focus, CJK matching, overlap dedup, budgets, and serialization | `internal/evidence/evidence_test.go`, `TestHybridSearchRanksAndExplains`, and anchor API/extension coverage | PASS |
| 8 | `domain-store.spec.ts` | Store/business-state integration, raw bytes, recovery, vector reuse, source repointing, reconciliation, and SQL retrieval | `internal/storage/storage_test.go`, `TestReconcileStorageRemovesOrphansAndFixesCounts`, `TestRecoverInterrupted`, `TestFileImportReindexAndDeleteCascade`, and directory/API tests | PASS |
| 9 | `embed.spec.ts` | Provider URL defaults, fail-closed validation, normalization, indexes, and provider errors | `internal/embedding/provider_test.go`, `internal/embedding/local_test.go`, and `TestRuntimeProvidersAndOCRAreWired` | PASS |
| 10 | `embedding-reuse.spec.ts` | Embedding-text hashes, unchanged reindex reuse, model identity, cross-document/base reuse, and interrupted recovery | `TestVectorHashReuseOnReindex`, `TestStatsDetectStaleEmbeddings`, and startup recovery tests | PASS |
| 11 | `local-rerank-readiness.spec.ts` | Artifact readiness, custom registration, stale envelopes, and fail-closed unavailable models | `TestCustomRerankerRegistrationAndSelfTest`, `TestManagerRejectsUnsafeIDsAndIncompleteModels`, and reranker self-test API | PASS |
| 12 | `local-rerank-runtime.spec.ts` | Isolated helper scoring, validation, timeouts, crash rebuild, deadlines, and degradation | `internal/rerank/local_test.go`, `internal/runtime/manager_test.go`, and `TestRerankerTimeoutDegradesQuickly` | PASS |
| 13 | `local-worker-lifecycle.spec.ts` | Idle unload/reuse and configurable lifecycle deadlines | `TestManagerIdleLifecycle`, `TestManagerHonorsCallDeadlineAndClose`, and config timeout normalization | PASS |
| 14 | `net.spec.ts` | Shared outbound HTTP policy, proxy handling, owner cancellation, deadlines, and bounded provider failures | `internal/httpx/client_test.go`, `TestOpenAIProviderErrors`, `TestRemoteRerankValidation`, and `TestRerankerTimeoutDegradesQuickly` | PASS |
| 15 | `ocr.spec.ts` | OCR modes, artifact state, dictionary validation, fail-safe native text, isolated runtime request contract, optional full-page renderer contract, embedded-raster OCR fallback/preprocessing, fallback command selection, and image extraction/tint/soft-mask/optional-codec composition used by captioning | `TestOCRModeResolution`, `TestOCRFailureKeepsNativeText`, `TestFragmentedPDFRequestsOCRButKeepsNativeText`, `TestEmbeddedRasterFallbackRunsWhenPDFEnvelopeOCRFails`, `TestRenderedPDFPagesPreferredOverPDFEnvelope`, `TestOCRRendererFailureFallsBackToEmbeddedRasters`, `TestSetGlobalConfigHotReloadsOCRRenderer`, `TestParseRenderedPDFPages*`, `TestPrepareOCRImageUpscalesAndGrayscales`, `TestPostprocessOCRTextFoldsCJKSpacesOnly`, `TestTesseractFallbackRunsAfterPrimaryRuntimeFailure`, `TestRuntimeOCRHelper*`, `TestOCRBundleLifecycle`, `TestExtractPDFJPXWithOptionalDecoder`, `TestExtractPDFJBIG2ForwardsGlobalsToOptionalDecoder`, `TestSetGlobalConfigHotReloadsImageDecoder`, and parser image tests | PASS |
| 16 | `parse.spec.ts` | HTML cleanup/Markdown, encodings, PDF text health, modern formats, unsupported-input failure, and parser registry dispatch | `internal/parser/parser_test.go`, PDF layout/OCR tests, and document failure tests | PASS |
| 17 | `popover-placement.spec.ts` | Floating popover and submenu viewport placement | No floating popover or submenu component exists in the independent Web UI; navigation and responsive shell behavior are exercised by Chrome E2E. Host-owned menus remain outside Knowledge. | NOT APPLICABLE |
| 18 | `rerank-adapter.spec.ts` | Local score mapping, batching, finite-score validation, OOM handling, and non-OOM failure propagation | `internal/rerank/local_test.go` and circuit-breaker failure tests | PASS |
| 19 | `rerank.spec.ts` | Local and remote rerank ordering, score range, deadline, retries, malformed response, timeout, and degradation | `internal/rerank/rerank_test.go`, `TestRerankerAppliedAndDegraded`, and `TestRerankerTimeoutDegradesQuickly` | PASS |
| 20 | `retrieval.spec.ts` | Tokenization, cosine, BM25, vector and lexical modes, RRF, weights, thresholds, and MMR diversity | `internal/retrieval/retrieval_test.go` and `TestHybridSearchRanksAndExplains` | PASS |
| 21 | `service.spec.ts` | Base/document lifecycle, imports, conflicts, directories, URL refresh, metadata filters, jobs, groups, restore, search, multi-query fusion, rerank, and migration | `internal/knowledge/service_test.go`, `service_phase4_test.go`, `service_dir.go` tests, and `internal/web/api_test.go` | PASS |
| 22 | `smoke-packed-lifecycle.spec.mjs` | Packed process lifecycle, clean removal, leftover dependencies, and independent upgrade | `TestProtocolUpgradeIndependence`, `TestDisabledScopeIsolation`, `TestFailureIsolationAdapters`, and `scripts/removal_gate.ps1` | PASS |
| 23 | `store.spec.ts` | Persistent bases, documents, chunks, config, groups, migrations, and delete semantics | `internal/storage/storage_test.go`, `internal/knowledge/store.go` tests, and service lifecycle tests | PASS |
| 24 | `tool-contract.spec.ts` | 14-tool contract, bounded pagination, anchors, stale anchors, scope fail-closed, filters, rendering budgets, and continuation | `internal/extension/extension_test.go`, `TestToolLifecycleAndContext`, and `TestDisabledScopeIsolation` | PASS |
| 25 | `ui-policy.spec.ts` | Ollama browsing/pulling isolated from provider persistence; manually dismissable notifications | Web contract isolates `ollamaPanel` from `updateBase`/`updateConfig` and embedding fields; Chrome E2E triggers an error toast, dismisses it, and verifies the hidden state | PASS |

## Result

24 of 25 upstream spec areas have current PASS evidence. The remaining area is
intentionally not applicable because this independent Web UI has no floating
popover/submenu component. This is a test-parity result only: passing contract
tests do not claim that a deployment has installed healthy OCR or renderer
binaries. The capability matrix records those optional-runtime boundaries and
their fail-closed behavior.
