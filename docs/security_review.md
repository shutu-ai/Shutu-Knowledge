# Security Review

This review covers the current Phase 8 source and its executable checks. It is
a release-review record, not a claim that the remaining Gate C/Web/E2E work is
finished.

## Verified Controls

| Area | Control | Evidence |
|---|---|---|
| Logging | Application logs record recovery/maintenance counts and component errors. They do not log document bodies, raw retrieval evidence, prompts, URLs, credentials, or model vectors. | Source review of all production logging calls in `internal`; log levels default to `info`. |
| Global credentials | Embedding/rerank API keys are accepted from file/env, excluded from configuration JSON, persisted only in Knowledge's private config file, and reported by doctor as `<set>`. | `internal/config/config.go`; `config_test.go` and Web global-config tests. |
| Per-base credentials | API responses are write-only for embedding, rerank, and MinerU keys. Responses expose `*ApiKeySet` only; PATCH preserves an omitted key and explicit clear flags remove it. | `BaseConfig.Redacted`, Web base handlers, and `TestBaseAPIKeysAreWriteOnly`. |
| Outbound credentials | Provider keys are placed only in the outbound `Authorization` header through the shared proxy-aware HTTP client. Provider error surfaces include status codes, not request credentials or upstream response bodies. | `embedding`, `rerank`, `ollama`, and `httpx` tests. |
| File permissions | Knowledge config and model data use `0600/0700`. SQLite and raw source files/directories are constrained to `0600/0700` on POSIX filesystems. | `storage` permission regressions and `config`/models implementation. |
| Path safety | Raw-store relative paths reject traversal, absolute paths, and Windows drive prefixes before joining under the root. Model IDs/artifacts are constrained beneath the model cache. | `TestRawStoreRoundTripAndGuards` and `TestManagerRejectsUnsafeIDsAndIncompleteModels`. |
| Archive/input limits | The request body is capped at 32 MiB. Office/EPUB archives enforce a 256 MiB uncompressed aggregate cap, and malformed/unsupported inputs fail as isolated documents rather than panics. | `internal/web/server.go`, `internal/parser/office.go`, parser/storage/failure tests. |
| PDF raster decoding | Embedded image streams are capped at 64 MiB and JPEG rasters at 4M pixels; unsupported or malformed codecs are skipped instead of failing ingestion or growing without bound. | `internal/parser/pdf_images.go`, `pdf_filters.go`, and focused filter/corrupt-stream tests. |
| Configuration validation | Unknown YAML fields fail closed and numeric/enum fields are normalized to bounded ranges. | `internal/config/config_test.go`. |
| Failure isolation | Optional embedding/rerank/OCR failures degrade or become tool errors without losing process readiness; malformed input cannot crash the extension adapter. | Gate H tests in `docs/gates.md`. |

## Fixes Made By This Review

1. Per-base API keys were previously serialized with their containing base
   configuration. They are now redacted from every base API response and can
   only be written or explicitly cleared.
2. Raw source and SQLite files were using permissive default modes. Knowledge
   now requests private `0700/0600` modes (POSIX filesystems).
3. Embedding, rerank, and Ollama errors forwarded bounded upstream response
   bodies. They now expose stable operation errors and HTTP status only.

## Residual Risks

- The standalone server is intended for loopback development and performs no
  user authentication. In Agent-managed mode, Knowledge also binds to an
  ephemeral loopback port; the Agent shell owns user authentication and
  reverse-proxy access. Multi-tenant network exposure is out of scope for V1
  and requires a deployment boundary before changing the listen address.
- Windows does not implement POSIX mode bits identically. The permission tests
  assert the requested modes on POSIX; Windows deployment should use a private
  user profile/service account directory.
- ZIP expansion is bounded, but PDF/helper processors still depend on the
  configured external runtimes. Those runtimes remain optional and isolated and
  are never bundled or promoted to readiness merely because artifacts exist.
