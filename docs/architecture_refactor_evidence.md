# Architecture Refactor Evidence

> Status: implementation in progress
>
> Baseline: `docs/architecture_refactor_plan.md` v1.3
>
> Updated: 2026-09-15

This record separates implemented behavior from gates that remain open. A
passing unit or browser test is not treated as SmartCare-scale acceptance.

## Implemented Evidence

### P0/P1a initial implementation - metrics and Storage Writer

- `cmd/architecture-baseline` is now a read-only P0 inventory tool. Repeated
  `-dataset name=path` inputs record sorted relative file paths, sizes and
  SHA-256 digests, directory depth, text-byte/character/line distribution,
  model manifest identity, host CPU/memory/disk capacity, and optional
  SQLite document/chunk/embedding-model/storage-format metadata. `-config`
  records the config digest plus a recursively redacted view; source bytes,
  API keys, tokens, passwords, cookies, and credentials are never written to
  the report. The stable `reportFingerprint` excludes collection time, host
  capacity volatility, and absolute input paths while retaining input digests.
  `go test ./cmd/architecture-baseline -count=1`, a Windows cross-compile,
  and local dataset/config plus SQLite smoke reports passed. This closes the
  collection tooling portion of P0-02, but not the required SmartCare input
  copies or P0-03/P0-04 scenario and budget evidence.

- `cmd/architecture-scenarios` now provides a bounded API workload collector
  for status, single-base import/reindex, dual-base reindex, cross-base import/delete,
  continuous search, and paged document switching. It polls Durable Operations,
  records request outcomes and p50/p95/p99 latency, and samples sanitized
  status/metrics/base-stat snapshots. Search hits, document content, complete
  responses, and secret-bearing fields are excluded. Restart, file-lock, and
  OS-level disk-fault scenarios are deliberately recorded as `unrun` and still
  require a release-host controller; the runner does not close those gates.

- Scenario collector validation: `go test ./cmd/architecture-scenarios ./scripts
  -count=1` and `go vet ./...` passed. The command's report aggregates request
  outcomes and latency percentiles, Durable Operation stage timings, and peak
  RSS/WAL/temp/upload/disk values from sampled status data. No live SmartCare
  scenario was claimed in this local validation because the required isolated
  two-base instance and input IDs are not present in the workspace.

- `cmd/retrieval-regression` now provides the P0-05 fixed-corpus runner. Each
  input case fixes its query, base, mode, TopK/MMR, expected and negative IDs,
  and optional context/raw-citation checks. The report retains only the query
  SHA-256, hit/chunk/base IDs, rank/scores, generation/source version, reranker
  provider/model/status, diagnostic stage counts/candidates, and dependent
  endpoint metadata. Query text, hit/context/raw bytes, and reranker error
  messages are excluded. Missing expected hits, negative hits, HTTP failures,
  and dependent-check failures fail the report; missing anchors are `unrun`.
  Unit coverage passed for Hit@1/Hit@3/MRR, version and diagnostic extraction,
  negative-hit rejection, context/raw headers, and content non-leakage. This
  closes the P0-05 runner/tooling portion only; SmartCare case IDs and repeated
  product-dictionary/Suite evidence remain required.

- P0-04 now defines the configuration-to-budget-to-observation reconciliation
  table in `docs/architecture_refactor_plan.md`: queue/per-base fairness,
  memory/RSS, disk/WAL/free-water, temp/upload release, model wait, retrieval
  deadlines, and Writer transaction spans each have an explicit observed
  field and acceptance rule. This is the reviewable accounting contract, not a
  claim of live capacity acceptance; SmartCare dual-base mixed success/reject
  runs are still required before freezing thresholds.

- P1B-04 upload ownership follow-up: `import_file` now binds the measured size
  of a completed upload into `Request.TotalBytes` before resolving the durable
  operation's memory/disk/temp budget. This keeps admission accounting tied to
  the immutable staged input rather than a generic default. The service
  regression `TestUploadBindsAtomicallyAndReleasesOnSuccess` and the Web
  regression `TestOperationFileUploadImportIsDurable` both assert the bound
  byte count; normal and race targeted tests passed. This is local contract
  evidence only and does not close aggregate scale, persistent-volume, or
  release-host gates.

- Latest repository regression after the P1B-04 binding fix: `go test ./...
  -count=1` passed across all packages (`internal/app` 101.975s,
  `internal/knowledge` 154.845s, `internal/web` 338.741s); `go vet ./...`
  and `git diff --check` also passed. The result is current-source local
  evidence and is not a CI, SmartCare-scale, or release-host result.

- P1B-03/P1B-06 model-cache migration hardening: the Durable Operation now
  passes its cancellation context into cache copy/verification, closes each
  artifact handle before walking the next file, and persists the new cache
  configuration only after the verified target is ready and before explicit
  source removal. A failed pre-commit callback removes the target and retains
  the source; replay against an already activated target persists the config
  and returns an idempotent result. Model migration and Web durable-operation
  tests passed, including commit-failure/source-retention and canceled-context
  cases. Release-host crash-window and persistent-volume evidence remain open.

- `maintenance_storage` now passes its operation context through
  `storage.MaintainSQLiteContext` to FTS optimization and VACUUM; the legacy
  no-context method remains a compatibility wrapper. A canceled maintenance
  regression passed alongside the existing threshold/forced-vacuum test. This
  proves cancellation propagation at the repository boundary, not interruption
  behavior on every release-host SQLite/filesystem implementation.

- Final repository regression after the migration and maintenance changes:
  `go test ./... -count=1` passed, including `internal/app` (97.691s),
  `internal/knowledge` (169.611s), `internal/models` (4.431s),
  `internal/operations` (5.005s), `internal/storage` (7.607s), and
  `internal/web` (341.426s). This confirms current-source integration only;
  SmartCare-scale, persistent-volume, cross-platform runtime, and rollback
  gates remain open.

- Configuration durability follow-up: `config.Save` now writes to a private
  same-directory temporary file, flushes and closes it, then replaces the
  configured path; failed writes clean up the temporary file. The config
  regression verifies replacement, reload, and no leftover temp artifact.
  This strengthens local persistence boundaries but is not a substitute for
  release-host power-loss or filesystem-fault testing.

- Model deletion follow-up: local model and OCR removal now use a
  cancellation-aware recursive tree delete. Cancellation is checked between
  entries, and an interrupted partial tree remains retryable; an already
  absent tree remains a successful idempotent outcome at the operation layer.
  `TestManagerRemoveHonorsCanceledContext` covers cancel-before-delete and
  retry-to-convergence. Locked-file behavior and persistent-volume fault
  evidence remain release-host gates.

- Error-boundary hardening: operation terminal errors and Web error envelopes
  now pass through a bounded sanitizer that redacts credential-like key/value
  fields, Bearer tokens, secret-shaped tokens, file URLs, and local paths while
  retaining useful causes such as `document not found`. The sanitizer also
  converts control newlines and caps the message at 1,024 runes. Regression
  coverage verifies secret/path non-leakage and useful-cause preservation;
  release-host logs and helper-specific failure variants still require review.

- Runtime error-boundary hardening: managed/helper `runtime.Error`, captured
  stderr, and wrapped process failures now use the same shared sanitizer while
  preserving `errors.Is` cause matching. Runtime tests cover wire-error and
  stderr-wrapper non-leakage. This closes the repository-side redaction path;
  release-host process logs still need an independent audit.

- Latest full regression after shared runtime/operation error sanitization:
  `go test ./... -count=1` passed, including `internal/app` (95.019s),
  `internal/extension` (95.025s), `internal/knowledge` (146.514s),
  `internal/models` (3.471s), `internal/operations` (4.772s),
  `internal/runtime` (23.603s), `internal/storage` (8.098s), and
  `internal/web` (304.532s). `go vet ./...` and `git diff --check` passed;
  this is current-source repository evidence only.

- Latest full regression after cancellation-aware model deletion:
  `go test ./... -count=1` passed, including `internal/app` (96.860s),
  `internal/extension` (147.251s), `internal/knowledge` (163.798s),
  `internal/models` (3.932s), `internal/operations` (4.983s),
  `internal/storage` (10.919s), and `internal/web` (330.850s). This is
  current-source repository evidence only; release-host fault gates remain
  open.

- Latest full regression after configuration durability changes:
  `go test ./... -count=1` passed, including `internal/app` (123.223s),
  `internal/extension` (114.647s), `internal/knowledge` (159.287s),
  `internal/models` (3.589s), `internal/operations` (6.264s),
  `internal/storage` (8.574s), and `internal/web` (360.552s). `go vet ./...`
  and `git diff --check` also passed. This remains repository-local evidence;
  no SmartCare, persistent-volume, or release-host gate is claimed.

- Final validation after the P0-05 runner safety checks and P0-04
  reconciliation update: `go test ./... -count=1` passed every package,
  including `cmd/architecture-baseline` (2.475s),
  `cmd/architecture-scenarios` (3.781s), `cmd/retrieval-regression` (2.579s),
  App (105.187s), Extension (133.479s), Knowledge (155.587s), Operations
  (8.413s), Runtime (22.143s), Storage (8.787s), Web (345.228s), and Scripts
  (1.690s). The new runner also passed `go test -race
  ./cmd/retrieval-regression -count=1` (2.293s), Windows
  `GOOS=windows GOARCH=amd64 go test -c`, `go vet ./...`, and
  `git diff --check`. These are repository/current-source checks, not
  SmartCare-scale or release-host acceptance.

- Full regression after adding the P0-03 runner: `go test ./... -count=1`
  passed for every package (App 102.458s, Knowledge 156.864s, Operations
  6.110s, Runtime 29.257s, Storage 9.989s, Web 341.698s); the runner and
  scripts also passed `go test -race`, and `go vet ./...` plus
  `git diff --check` passed. This verifies repository integration only; it is
  not a live SmartCare or release-host acceptance.

- Live local P0-03 smoke on a freshly rebuilt current-source Windows binary:
  an isolated `provider=none` instance created two temporary bases and ran
  `status,single-import,single-reindex,dual-reindex,continuous-search,page-switch`
  for 6s. Report `.tmp/architecture-scenarios-current-20260914-205643.json`
  finished `passed`: four Durable Operations succeeded, continuous search
  recorded 2,855 successful requests plus 2 expected `cancelled` shutdown
  requests, and sampling captured peak RSS 35,561,472 bytes, WAL 4,136,512
  bytes, temp/upload 0, and disk footprint 4,390,464 bytes. This is an
  isolated local-loop proof only; it does not satisfy SmartCare scale,
  persistent-volume, or release-host fault gates.

- Repeated live local smoke after adding event attribution:
  `.tmp/architecture-scenarios-current-20260914-205911.json` passed on the
  current-source Windows binary with two isolated bases. Four operations
  (`import_text` and three `reindex_base`) reached `succeeded` and each exposed
  `submitted`, `claimed`, `phase`, and `finished` event kinds. Continuous search
  recorded 2,242 successful requests and 2 expected `cancelled` shutdown
  requests; observed peaks were RSS 34,643,968 bytes, WAL 4,132,392 bytes,
  temp/upload 0, and disk footprint 4,386,344 bytes. This remains local
  provider-none evidence, not SmartCare-scale or release-host acceptance.

- Cross-base local smoke: `.tmp/architecture-scenarios-cross-20260914-210027.json`
  passed on a freshly rebuilt current-source Windows binary. An explicit seed
  document in base B was deleted while a new text import ran in base A; both
  `import_text` and `delete_document` reached `succeeded`, each with
  `submitted/claimed/phase/finished` events. The report contained no `unrun`
  item for this scenario. This proves the local API/operation loop and explicit
  target boundary only; it does not close SmartCare two-base pressure or
  release-host fault gates.

- Final full race after the event-attribution, resource-peak, and cancellation
  accounting changes: `go test -race ./... -count=1 -timeout=20m` passed every
  package, including `cmd/architecture-baseline` (2.439s),
  `cmd/architecture-scenarios` (1.746s), App (174.220s), Extension (165.220s),
  Knowledge (552.209s), Operations (19.769s), Runtime (50.337s), Storage
  (123.139s), Web (628.611s), and Scripts (3.119s). No race report or timeout
  remained.

- `internal/knowledge/metrics.go` now exposes the stage fields required by the
  v1.2 plan: queue wait, run time, parse/model time, raw disk-read time, read
  connection wait, DB transaction time, FTS time, and vector time. Import,
  search, raw citation, generation staging, FTS, and vector paths record the
  corresponding bounded durations without recording document content,
  credentials, or provider secrets.
- `internal/storage/writer.go` provides a bounded priority-aware writer with
  separate control, normal, and maintenance queues, context cancellation,
  bounded default write deadlines, transaction callbacks, and explicit
  unknown-outcome handling. `storage.DB` starts it at open, routes standalone
  mutations through it, and stops it before closing SQLite.
- Durable Operation state transitions (recovery, submission, claim, finish,
  cancellation, retry, and signing-key rotation) and the main Knowledge
  transaction paths now execute through `DB.WriteTx`. `go test
  ./internal/knowledge ./internal/operations ./internal/storage -count=1`
  passed after this change; `writer_test.go` covers commit/rollback, control
  priority, and bounded admission cancellation.
- This is an initial P0/P1a slice, not a closed gate. The remaining P1a work
  still includes a complete production write-path static audit, centralized
  transaction-duration metrics, splitting `putChunksReplace` and other large
  transactions into bounded batches, and SmartCare/release-host contention
  evidence.

- Startup SQLite maintenance now submits a durable `maintenance_storage`
  operation with a `sqliteOnly` payload. It therefore uses the maintenance
  scheduler lane, is visible through the existing operation/job status view,
  and does not turn deferred startup into a full raw-file reconciliation.
  The legacy in-process `jobs.Manager` is no longer constructed or stopped by
  `internal/app`; production HTTP paths use the durable operation service,
  while the old Knowledge helper signatures remain only as test/compatibility
  seams. A full Web legacy-operation compatibility run passed after this
  migration.

- Writer admission is fail-fast when a priority queue is full and returns
  `ErrWriterQueueFull`; it no longer waits for caller deadlines before
  reporting overload. `go test ./internal/storage -count=1` passed after the
  change.

- A cancellation boundary exposed by the Writer migration is now handled in
  the durable state machine: an admitted write that returns
  `storage.ErrWriteUnknown` is classified from the persisted cancel intent as
  `cancelled`, or as `interrupted` when no cancel was requested. The Agent
  running-import cancellation test and the App/Extension/Operations/Storage
  combination regression both pass after this fix.

- `restore_base` is now a Durable Operation. The HTTP handler allocates the
  target ID, persists a replayable command, and returns an operation receipt;
  restore execution uses deterministic target-document IDs so a worker retry
  does not create duplicate business documents. The historical raw-restore
  Web test was updated to assert receipt-then-terminal-result behavior.

- Scheduler status now includes data-free process/storage measurements:
  peak/sample RSS and heap, database/WAL bytes, raw/staging/upload bytes, and
  temporary-file bytes and free bytes on the database volume. Aggregate upload retention is bounded by the new
  `scheduler.uploadBytes` budget and completion checks the budget atomically
  in the Writer transaction. `scheduler.tempBytes` is now wired from config
  into the Operation Service; temporary staging has an atomic in-process
  reservation budget and HTTP quota failures return `429 quota_exceeded`.
  Resource/aggregate-upload quota regressions passed.
- The resource sampler reports free bytes on the database volume. A configured
  `diskLowWaterBytes` boundary rejects new commands with `disk_low_water`
  before persistence, while a retry succeeds after the probe reports
  sufficient space. The policy is covered by
  `TestDiskLowWaterRejectsNewCommandsAndAllowsRetryAfterRecovery`; real
  persistent-volume ENOSPC behavior remains a release-host gate.
- Migration `0015_operation_resource_budgets.sql` persists each operation's
  effective memory/disk/temp peak budget. Scheduler admission reserves those
  bytes atomically when claiming work, releases them at terminal completion,
  and exposes reserved totals alongside configured limits. A request that
  exceeds the configured aggregate budget is rejected before persistence;
  scheduler and durable-operation tests cover reservation, release, and
  persisted status fields. The storage compatibility envelope advances to
  reader/writer version 7 so pre-budget binaries cannot reopen the database.
- `scripts/writer_guard_test.go` is now a cross-platform CI check. It scans all
  production Go packages under `internal` (not only app/knowledge/operations)
  and rejects mutation/transaction calls against `ReadDB()` while allowing the
  single fixed read snapshot in `internal/knowledge/search.go`. It also rejects
  embedded raw `*sql.DB` access outside the storage wrapper implementation; the
  guard passed locally and is wired into `.github/workflows/ci.yml`.
- Scheduler claim ordering now applies bounded priority aging (one point per
  second waited) on top of the persisted priority, while preserving FIFO order
  for ties. `TestPriorityAgingPreventsStarvation` and the existing delete/
  independent-lane tests pass; sustained two-base fairness remains a scale
  acceptance gate.
- Reindex submissions now derive `reindex.auto.*` idempotency keys from the
  target document/source identity, active generation, and resolved base
  model/config snapshot.
  Single-document, batch, base-wide, Web, generic Operation, and Agent paths
  use the same digest contract; batch IDs are canonicalized for order-
  independent equivalence. Base-wide keys use the bounded base mutation epoch,
  which is advanced atomically with source-identity changes and subtree-delete
  fences instead of scanning all documents at receipt time.
  `TestReindexIdempotencyKeyTracksTargetAndConfigSnapshot` proves repeat/
  batch-order equivalence, config-change separation, and whole-base key change
  after a new document. This is an initial cross-entry contract; concurrent
  state-change and release-host failure evidence remains open.
- Durable Operation responses now expose `queueWaitMs` and `runTimeMs`,
  derived from the persisted request/claim/terminal timestamps for running,
  succeeded, failed, cancelled, interrupted, and retried commands. The timing
  contract is covered by `TestOperationExposesDerivedQueueAndRunTiming`; the
  per-type outcome aggregate is exposed by `OperationMetrics`, and the scenario
  runner now emits a reproducible metadata-only per-operation event trace.
  Stable stage-metric association under scale remains a P0 evidence task.
- `WriterStats` now retains the existing aggregate JSON fields and adds a
  `byPriority` snapshot for `control`, `normal`, and `maintenance`. Each class
  reports admitted, rejected, dropped, succeeded, failed, queue-wait, and
  run-time counters; it also reports transaction count and transaction runtime.
  Unknown priorities normalize to `normal` so accounting cannot disappear into
  an unreported queue. `writer_test.go` verifies class attribution for success,
  failure, rejection, transaction failure, and all three queues. This closes
  the Writer class/transaction-runtime implementation slice. For `Tx`, the
  Writer also records `dbWaitMs` around `BeginTx`, `dbTransactionMs` through
  commit/rollback, and separate SQL callback and commit spans; full write-path
  timing evidence remains open.
- `OperationMetrics` now aggregates durable operation rows by command type and
  state, including queued/running/cancelling/succeeded/failed/cancelled/
  interrupted counts, timeout classifications, and queue/run duration sums.
  Process-lifetime submission rejections are tracked separately because a
  rejected command deliberately has no durable row. The aggregate is exposed
  inside the existing `/api/status` scheduler snapshot; SQL groups rows before
  scanning them, and tests cover both the service result and HTTP shape.
  `context.DeadlineExceeded` without a cancel intent is now persisted as
  `failed/timeout`, while restart/cancel boundaries retain their
  `interrupted`/`cancelled` classifications.

### 2026-09-14 local validation snapshot

- After migration 0015, the first full run exposed one Windows-only transient
  failure in `TestImportProcessKillReplaysExactBusinessEffect` (`parse_failed:
  conflict`) while the same process-kill test was being run inside the full
  parallel package set. The test passed in isolation and in five consecutive
  repetitions (18.38-24.87s).
- The immediate second `go test ./... -count=1` run passed across all packages,
  including the long-running App, Extension, Knowledge, Operations, Runtime,
  Storage, and Web integration suites. That run completed with App in
  106.615s, Knowledge in 185.098s, and Web in 401.716s; no package reported a
  failure.
- The targeted resource/config/restore/lifecycle regression also passed, and
  `git diff --check` is clean apart from Git's normal LF/CRLF conversion
  warnings.
- After adding Writer aggregate/class statistics and the CI guard, the final
  `go test ./... -count=1` run passed again. Current package timings include
  App 142.286s, Extension 181.699s, Knowledge 211.070s, Storage 19.232s,
  Web 516.673s, and Scripts 1.655s. The race regression also passed for
  `./internal/storage ./internal/operations` (Storage 152.710s, Operations
  22.558s).
- Post-vet cleanup validation passed: `go vet ./...`, the writer guard and Web
  smoke tests, and `go test ./internal/knowledge ./internal/app -count=1`
  (Knowledge 170.186s, App 73.104s). The cleanup removed only a self-
  assignment flagged by Vet; it did not alter runtime behavior.
- After the P4 bounded reindex/tree-count changes, the current full
  `go test ./... -count=1` run passed across every package. Recorded timings
  include App 132.774s, Extension 115.644s, Knowledge 183.140s, Operations
  6.001s, Storage 11.156s, Web 362.289s, Scripts 1.302s, and cmd 2.643s.
  This run includes the new stable reindex-window regression and the Web
  directory-operation paths.
- Before the final legacy-list pagination change, the full
  `go test ./... -count=1` run passed across every package. Recorded timings
  include App 95.646s, Extension 135.227s, Knowledge 177.144s, Operations
  5.582s, Storage 9.720s, Web 388.280s, Scripts 1.273s, and cmd 2.291s.
- After the bounded flat-list compatibility change, targeted Knowledge and
  Web pagination/deprecation tests passed, followed by `go vet ./...` and
  `git diff --check`.
- The current full `go test ./... -count=1` run passed after the Operation
  timing fields and bounded-list protocol were finalized. Recorded timings
  include App 136.193s, Extension 150.073s, Knowledge 239.392s, Operations
  8.047s, Storage 17.461s, Web 519.483s, Scripts 1.687s, and cmd 2.555s.
- The latest full `go test ./... -count=1` run passed after the Writer class
  breakdown was added. Recorded timings include App 123.813s, Extension
  139.156s, Knowledge 238.887s, Operations 9.049s, Runtime 34.529s,
  Storage 19.004s, Web 522.020s, Scripts 1.547s, and cmd 2.592s.
- After adding bounded local-model paging and restricting managed/configured/
  custom compatibility entries to the first page, the latest full
  `go test ./... -count=1` run passed across all packages. Recorded timings:
  App 91.731s, Extension 95.974s, Knowledge 145.925s, Models 2.333s,
  Operations 5.081s, Runtime 27.203s, Storage 7.764s, Web 309.538s,
  Scripts 0.892s; all cmd packages also passed. The targeted paging/API regression and
  `go vet ./...` also passed; Windows `internal/models` cross-compilation
  passed, and targeted `go test -race` model and Web regressions passed. A
  final `git diff --check` showed only normal LF/CRLF warnings.
- After moving the model-cache migration preflight into the cancellable
  `plan_model_cache_migration` Durable Operation, the latest full
  `go test ./... -count=1` run passed across all packages. Recorded timings:
  App 101.617s, Extension 98.118s, Knowledge 152.160s, Models 3.109s,
  Operations 5.175s, Runtime 25.442s, Storage 9.857s, Web 303.085s,
  Scripts 1.248s; targeted migration-plan/API tests also passed.
- The migration-plan operation and context-cancellation regressions also passed
  under targeted `go test -race` for `internal/web` and `internal/models`.
- After bounding whole-base `reindex.auto.*` equivalence-key generation with
  `bases.mutation_epoch`, source-identity changes and subtree-delete fences
  advance the epoch atomically, so HTTP receipt generation no longer scans
  every document. Explicit document batches retain sorted per-document
  source/content/raw identity hashing. `TestReindexIdempotencyKeyTracksTargetAndConfigSnapshot`
  now proves that adding a document changes the whole-base key. The latest
  full `go test ./... -count=1` run passed with App 89.007s, Extension
  112.029s, Knowledge 151.917s, Models 3.713s, Operations 6.769s, Runtime
  24.205s, Storage 9.564s, Web 305.473s, Scripts 1.420s; targeted reindex
  tests also passed. This remains local evidence; dual-base concurrency,
  failure/restart equivalence, and release-host replay are still open.
- The architecture scenario runner now records an ordered, metadata-only
  `eventTrace` per terminal Operation: event ID, revision, creation time, kind,
  adjacent-event elapsed milliseconds, and only phase/progress counters from phase payloads. It intentionally drops
  command payloads, error text, and unknown fields. The durable-operation poll
  test and a focused redaction/order test passed in
  `cmd/architecture-scenarios` (1.293s). This strengthens local traceability;
  stable association of stage metrics with operation types under scale remains
  a P0 gate.
- The latest full `go test ./... -count=1` run passed after the bounded
  Extension document lists and ordered operation traces were added. Recorded
  timings: architecture-baseline 2.641s, architecture-scenarios 3.166s,
  retrieval-regression 3.146s, main 2.637s, App 85.047s, Extension 102.338s,
  Knowledge 150.239s, Models 3.330s, Operations 5.216s, Runtime 20.074s,
  Storage 7.602s, Web 300.761s, Scripts 1.138s; all remaining tested packages
  also passed. This is local regression evidence and does not close the
  SmartCare/release-host gates.
- The filesystem resource sampler now caches recursive raw/upload footprint
  walks for two seconds while refreshing cheap DB/WAL, RSS/heap, and free-space
  fields on every status sample. This prevents repeated `/api/status` calls
  from turning a large raw corpus into a control-plane scan; the sampler
  regression proves cached directory bytes and refreshed database bytes.
  Targeted Operations tests passed. The cache interval and footprint thresholds
  still require SmartCare-scale calibration.
- Durable Operation results now share the 1 MiB persistence boundary used for
  command payloads. A result that exceeds the bound becomes a sanitized,
  non-retryable `result_too_large` failure and no result blob is written;
  `TestOversizedResultIsBoundedAndNonRetryable` covers the state, storage, and
  retry behavior. This closes a local result-retention boundary, while
  scale-level result ownership and retention accounting remain open.
- Full repository regression after the result boundary and cancellation
  preservation fix: `go test ./... -count=1` passed. Recorded timings include
  App 104.623s, Extension 94.517s, Knowledge 148.049s, Operations 4.861s,
  Runtime 26.352s, Storage 7.807s, Web 298.653s, Scripts 1.010s, and all cmd
  packages. `go vet ./...` and `git diff --check` also passed; this remains
  local current-source evidence rather than SmartCare or release-host proof.
- P1B command-envelope follow-up: migration `0016` adds durable principal/scope,
  input reference/hash, source/config/model snapshot references, target/ancestor
  epochs, allocated document/generation, retry schedule, recovery/result
  retention fields, and the `operation_items` per-item commit-marker table.
  Idempotency fingerprints now cover the immutable envelope semantics, while
  legacy document IDs remain checked separately. `TestSubmitPersistsReplayEnvelope`
  verifies round-trip metadata and snapshot conflicts; storage and Operations
  targeted tests passed. Producers still need to populate real snapshots/epochs
  and make business publication plus marker update one commit boundary.
- Batch commit-marker follow-up: `operation_items` is now used by batch delete,
  document reindex, and base reindex executors to persist committed, skipped,
  and failed item outcomes. A committed item cannot be overwritten by a stale
  attempt; retry admission records and honors `next_attempt_at`. Full current-
  source regression after the migration and marker wiring passed with App
  84.138s, Extension 124.015s, Knowledge 145.629s, Operations 6.328s, Runtime
  24.487s, Storage 7.864s, Web 295.608s, Scripts 1.133s, and all cmd packages.
  This is local evidence; atomic business-publication/marker coupling and
  release-host replay remain open.
- App admission now enriches new Durable Operations with a local-process
  principal, base/document target and ancestor epochs, source-version reference,
  and hashed non-secret config/model snapshot references. Existing idempotency
  keys are checked before enrichment so a deleted target or changed configuration
  cannot prevent recovery of the original operation. The App enrichment and
  non-secret round-trip regression passed; this records the replay envelope but
  does not make business publication and marker writes one SQLite transaction.
- Latest full repository regression after App-side envelope enrichment and
  target-deletion replay coverage: `go test ./... -count=1` passed. Timings
  include App 109.229s, Extension 92.329s, Knowledge 150.162s, Operations
  5.345s, Runtime 21.424s, Storage 7.131s, Web 413.637s, Scripts 0.782s,
  and all cmd packages. `go vet ./...` and `git diff --check` also passed.
- Business-publication/marker coupling follow-up: Knowledge now accepts a
  bounded `CommitHook` on the context. Document generation activation and the
  logical delete fence invoke the hook inside their own short writer
  transaction; App import, single/batch delete, and single/batch reindex pass
  an `operation_items` committed marker through that hook. A failing hook is
  covered by `TestCommitHookRollbackProtectsDocumentFence`, which proves the
  delete fence and reindex generation do not escape the rollback. Later raw
  cleanup remains outside the transaction and is therefore recoverable rather
  than incorrectly treated as a second business commit.
- Latest full current-source regression after the commit-hook coupling:
  `go test ./... -count=1` passed. Timings include App 105.148s, Extension
  102.437s, Knowledge 145.035s, Operations 5.610s, Runtime 22.768s, Storage
  7.383s, Web 303.181s, Scripts 0.821s, and all command packages. `go vet ./...`
  also passed; this remains local evidence and does not close SmartCare,
  release-host, or external fault-injection gates.
- P4-01 control-path follow-up: the no-op model-cache migration branch now
  uses `models.Manager.CountContext` instead of the legacy full `List` call.
  The count walks only manifest paths, does not decode or retain model
  metadata, and honors cancellation; `TestManagerCountContextDoesNotDecodeCatalog`
  covers both properties. This removes the remaining production caller of the
  unbounded model catalog read; model-picker paging remains separately bounded.
- P4 Web compatibility follow-up: the no-parameter legacy document endpoint
  now preserves its array response shape while returning only the default 50
  row page; callers with more data must use the successor `limit/offset`
  contract. `TestLegacyDocumentListProvidesBoundedCompatibilityMode` now
  creates 54 documents and verifies the legacy response stops at 50.
- P1B batch-import follow-up: `import_files` now persists a bounded pending
  allocation (`operation_items.result`) for every input item, passes the stable
  document ID into `AddFileDocumentWithID`, and uses the per-item commit-hook
  factory for successful publication markers. A retry reconstructs the same
  IDs from the durable item rows; `TestImportFilesOperationPersistsStableItemAllocations`
  verifies committed allocations and replay reuse. Skipped/error outcomes and
  the remaining aggregate operations still require the broader fault matrix.
- Base-delete and restore-boundary follow-up: `delete_base` now writes its
  committed item marker inside the base lifecycle-fence transaction and can
  resume cleanup from `lifecycle_state=deleting`; a post-cleanup marker call
  covers legacy already-fenced rows. `restore_base` now counts non-directory
  source rows and walks full source documents in cancellable 50-row keyset
  pages instead of materializing the source base. Fence rollback coverage is
  provided by `TestBaseDeleteCommitHookRollbackProtectsFence`.
- Restore marker follow-up: `restore_base` now passes a per-source-document
  commit-hook factory through `RestoreBaseWithID`; each derived document's
  generation publication atomically records its source item marker, while the
  deterministic target ID makes already-ready items safe to skip on retry.
  `TestRawRestoreProbeAndIndexingAPI` verifies the committed item marker.
- Directory and batch-planning read-boundary follow-up: directory import and
  rescan now load only the requested source subtree metadata, and top-level
  source lookup uses a bounded `base_id + source_path` query. `import_files`
  duplicate planning now queries only the at-most-20 input hashes instead of
  materializing every document in the base. Existing directory, batch-import,
  and full-suite regressions remain green.
- Latest full current-source regression after base-delete recovery and
  paginated restore: `go test ./... -count=1` passed. Timings include App
  110.656s, Extension 119.677s, Knowledge 145.353s, Operations 5.241s,
  Runtime 24.974s, Storage 8.717s, Web 299.074s, Scripts 1.186s, and all
  command packages; `go vet ./...` and `git diff --check` also passed.
- Latest full current-source regression after stable batch-item allocation and
  bounded legacy document-list changes: `go test ./... -count=1` passed. Timings
  include App 134.713s, Extension 95.053s, Knowledge 153.234s, Operations
  6.706s, Runtime 26.142s, Storage 8.070s, Web 306.939s, Scripts 1.156s,
  and all command packages; `go vet ./...` also passed.
- Batch import race and read-path audit follow-up: `go test -race ./internal/app
  -run TestImportFilesOperationPersistsStableItemAllocations -count=1` passed
  in 13.593s. The production search found no remaining caller of the
  unbounded `ListDocumentsContext` or `Models.List`; only the compatibility
  methods/tests remain.
- Latest full current-source regression after target-subtree directory reads,
  bounded content-hash planning, and scalar duplicate checks: `go test ./...
  -count=1` passed. Timings include App 122.919s, Extension 96.997s,
  Knowledge 151.966s, Models 3.786s, Operations 6.173s, Runtime 24.067s,
  Storage 7.547s, Web 296.322s, Scripts 1.095s, and all command packages;
  `go vet ./...` also passed. This remains local evidence and does not close
  SmartCare-scale, release-host, cross-platform, or fault-injection gates.
- Retry-boundary and batch-concurrency follow-up: `operations.GetItem` now
  provides a scalar marker lookup so single and aggregate re-index executors
  skip already committed/skipped items on replay; `AddFiles` protects its
  shared result arrays across workers, and skipped batch outcomes preserve the
  preallocated item result. Targeted package tests and the multi-file race
  test passed.
- Delete-recovery follow-up: a batch delete retry now distinguishes a truly
  absent document from a committed `lifecycle_state=deleting` tombstone,
  validates the tombstone's base, and resumes physical cleanup before marking
  the item complete. Existing durable-delete cancellation tests remain green.
- Directory aggregate follow-up: durable import/rescan now preserves a
  bounded root-document result on partial failure and records a root item
  marker; durable recursive delete passes its root marker through the logical
  delete fence and records a bounded failure result, while an already-fenced
  root remains eligible for cleanup retry. Directory, delete, and Web tests
  passed.
- Latest full current-source regression after retry marker handling and the
  batch result race fix: `go test ./... -count=1` passed. Timings include App
  114.568s, Extension 101.698s, Knowledge 142.915s, Models 2.419s,
  Operations 5.488s, Runtime 20.229s, Storage 7.036s, Web 317.345s,
  Scripts 0.912s, and all command packages; `go vet ./...` also passed.
- Latest full current-source regression after aggregate directory markers and
  delete-tombstone cleanup recovery: `go test ./... -count=1` passed. Timings
  include App 112.326s, Extension 105.494s, Knowledge 157.719s, Models
  3.200s, Operations 6.989s, Runtime 22.957s, Storage 8.523s, Web 302.197s,
  Scripts 1.143s, and all command packages; `go vet ./...` also passed.
- Startup race follow-up: `NewWithOptions` now starts Durable Operation
  workers only after Knowledge provider/runtime wiring and optional health
  registration finish. This removes recovered-operation access to mutable
  provider state during initialization. The previously failing
  `TestImportOperationPreDispatchRecoveryCreatesBusinessEffect` and the full
  `go test -race ./internal/app -count=1` both passed; the latter completed in
  137.455s.
- Final current-source normal regression after startup ordering and aggregate
  cleanup changes: `go test ./... -count=1` passed. Timings include App
  110.813s, Extension 93.152s, Knowledge 151.763s, Models 3.933s,
  Operations 7.300s, Runtime 22.785s, Storage 8.354s, Web 310.235s,
  Scripts 1.230s, and all command packages; `go vet ./...` also passed.
- Full current-source race regression after the startup-order fix:
  `go test -race ./... -count=1` passed for every package. Timings include
  App 170.815s, Extension 150.730s, Knowledge 566.131s, Models 3.591s,
  Operations 31.861s, Runtime 29.424s, Storage 127.345s, Web 566.700s,
  Scripts 2.482s, and all command packages. The earlier App race was therefore
  reproduced, fixed, and revalidated rather than treated as an environmental
  flake.
- Post-regression quality gates also passed: `go vet ./...`, `git diff --check`,
  and the targeted race run for envelope, marker monotonicity, and bounded
  results (`go test -race ./internal/operations -run
  'TestSubmitPersistsReplayEnvelope|TestOperationItemCommitMarkerIsMonotonic|TestOversizedResultIsBoundedAndNonRetryable'`).
- Final affected-package verification after cancellation markers and schema
  assertions: `go test ./internal/app ./internal/operations ./internal/storage
  -count=1` passed (App 57.482s, Operations 6.998s, Storage 7.125s), and
  `go test -race ./internal/app -run
  TestCancelBatchReindexKeepsCommittedPartialResult -count=1` passed in
  14.436s. This supplements, but does not replace, the broader local full run.
- This local snapshot does not close the plan's SmartCare/release-host
  contention, persistent-volume low-water/ENOSPC calibration, task-equivalence/fairness
  gates, final legacy-list retirement, scale-level stage-metric association, or
  full write-path timing breakdown gates.
- After adding the Writer class breakdown, `go test ./internal/storage
  ./internal/operations -count=1`, `go vet ./...`, and `git diff --check`
  passed. The targeted Writer run completed with Storage 15.462s and
  Operations 7.710s; the full-suite timings above remain the latest broad
  integration evidence.
- After adding Writer transaction and DB timing fields, `go test
  ./internal/storage ./internal/operations -count=1`, `go vet ./...`, and
  `git diff --check` passed. The targeted run completed with Storage 16.197s
  and Operations 8.417s.
- After adding Operation type/outcome aggregation, targeted
  `go test ./internal/operations ./internal/web -run
  'TestOperationMetricsAggregatesOutcomesByType|TestStatusExposesSchedulerCapacity'
  -count=1` passed (Operations 0.572s, Web 13.664s), followed by
  `go vet ./...` and `git diff --check`.
- After expanding Writer timing and static guard coverage, `go test
  ./internal/storage ./internal/operations -count=1`, `go test ./scripts -run
  TestWriterGuard -count=1`, `go vet ./...`, and `git diff --check` passed.
  The package runs completed with Storage 14.990s, Operations 8.189s, and
  Scripts 1.535s.
- After adding the explicit timeout classification, full Operations tests and
  the targeted Web scheduler-status test passed; the combined run completed
  with Web 14.200s and Operations 10.884s, followed by `go vet ./...` and
  `git diff --check`.
- The latest full `go test ./... -count=1` run passed after timeout
  classification, Operation aggregates, Writer DB spans, and the all-
  `internal` guard were finalized. Recorded timings include App 127.485s,
  Extension 143.085s, Knowledge 241.923s, Operations 7.658s, Runtime
  28.893s, Storage 16.025s, Web 490.586s, Scripts 1.925s, and cmd 1.721s.

### P1a - generation and delete fences

- Migration `0005_generation_lifecycle.sql` adds lifecycle and mutation fences
  plus document/chunk index generations.
- Chunk replacement stages a new generation before activation. Active and
  retired rows are selected by generation.
- Generation staging is model-space aware. A rebuild carries a vector forward
  only when its embedding-text hash and stored model key match the target
  provider; an absent/disabled target produces lexical chunks and storage
  enforces null embeddings before commit. Retry also removes abandoned
  generations newer than active, so a failed model migration cannot block the
  next attempt with duplicate unpublished chunk IDs.
  A synthetic 400-document/1,600-chunk drill migrates every document from
  model A to B, attempts model C on every document with a provider failure,
  then creates explicit disabled-provider lexical generations. It proves the
  active vector identity changes only for successful B, C leaves no vectors or
  published mutation, and disabled embedding leaves zero active vectors.
- Document/base deletes commit a `deleting` fence before bounded physical
  cleanup; normal reads exclude fenced rows.
- Document upserts check the active base and complete parent chain in the same
  write transaction, closing the stale-scan/new-child-ID injection race.
  A real two-process drill now separates the race phases: one process reads a
  root/nested scan snapshot, another commits the ancestor tombstone, and the
  first replays both stale moves plus a new-child injection from that old
  snapshot. Every conditional write returns conflict; tombstones and the
  active-tree view remain unchanged.
- Migration `0013` pins each generation's raw path and SHA-256 content hash and
  advances the compatibility envelope to format 2, reader/writer 6. New file
  publications use immutable `.generations/<doc>/v<source-version>` paths;
  legacy shared paths are copied and document/mapping rows are updated together
  before a rebuild may replace them.
- Raw citations carry generation/source identity. A pinned raw download resolves
  its own immutable bytes, checks the current tombstone fence, and verifies the
  mapping's SHA-256 hash; stale source versions, deleted objects, corruption, or
  legacy shared-path replacement returns `historical_evidence_expired` instead
  of mixing evidence. Generation retention removes retired chunks, mappings, and
  raw paths; maintenance reconciliation honors retained generation paths until
  the mapping expires or a delete fence removes it.
  A synthetic 600-document corpus with 12,000 active and 12,000 retired chunks
  exercises the WAL boundary at scale: a pinned reader completes across
  concurrent retirement/GC without mixing or losing its generation view, while
  post-commit historical access and raw retrieval fail closed.
  A second real cross-process drill repeats the boundary: a child process
  pins a historical generation, the parent prunes every retired row while the
  child remains open, and the child's post-GC reads and mapping remain pinned
  until commit.
- Tests: `internal/knowledge/service_generation_test.go`,
  `internal/knowledge/synthetic_model_switch_test.go`,
  `internal/knowledge/synthetic_historical_corpus_test.go`,
  `internal/knowledge/cross_process_historical_reader_test.go`,
  `internal/knowledge/cross_delete_race_test.go`,
  `internal/knowledge/service_test.go`, and generation-aware store/search
  tests.

### P1b - durable operations

- Migrations `0006_operations.sql` through `0008_url_captures.sql` provide
  durable commands, events, upload sessions, and URL capture ownership.
- `internal/operations` implements persisted submission, request fingerprints,
  idempotency conflict detection, claim/finish/cancel/retry events, bounded
  stop, and restart recovery. `jobId` compatibility remains available.
- Operation progress now records a durable, de-duplicated `phase` event when
  the executor changes stage, alongside the existing submitted/claimed/finished
  lifecycle events. The event payload contains only phase/progress counters,
  never source content or credentials; stale-attempt progress remains ignored.
- Migration `0014_operation_signing_keys.sql` adds persisted HMAC validation
  secrets for server-issued operation keys. The API can issue, rotate, and
  revoke scoped credentials; submission verifies the signed type/scope/expiry
  and persists the stable credential ID rather than the secret-bearing token.
  Restart preserves the active and retired signing secrets, rotation preserves
  old credentials, revocation fails closed immediately, and issuance is rate
  limited.
- Cross-process C16 recovery is executable: a child sole-owner process resumes
  a stale running command, accepts and claims a second command over HTTP, is
  forcibly killed, and a new owner recovers both. The test proves attempts
  advance 1→2→3 and 0→1→2, each operation has exactly one terminal event and
  one replayed business effect, and no operation row is duplicated. It exposed
  and fixed a real recovery defect: `wakeupAll` emitted only one lane token, so
  a second recovered command could remain queued while the first worker was
  occupied.
- Cancellation now has explicit C11 coverage: queued work never executes after
  cancel, a running executor converges from `cancelling` to `cancelled`, a
  terminal cancel rejects retry, and a restart with a persisted cancel intent
  emits a terminal event without replaying the command. Batch cancellation now
  preserves committed units, marks an incomplete result partial, and stops
  uncommitted units. A committed cancel intent followed by an authoritative
  publish result resolves to succeeded without rollback; a queued durable
  delete batch cancelled under a same-base quota preserves every document and
  rejects retry. Once a document-tree delete fence commits, cleanup is
  mandatory: a cancel intent recorded at the tombstone/cleanup boundary cannot
  leave the committed tree half-cleaned.
  A real cross-process drill now exercises the ownership boundary: a child
  owns a running command, another process commits `cancel_requested`, the
  child observes the intent and is force-killed before its terminal write, and
  a new owner resolves the row to cancelled without replay.
- Web, Agent tools, and staged uploads use the durable operation contract.
- Terminal operation payloads and results are released after their durable
  idempotency window during startup recovery, while the compact operation row
  and expiry key remain for explicit `operation_expired` responses; retryable
  failures retain command input until retry is no longer allowed.
  `TestExpiredTerminalOperationReleasesPayloadAndResult` covers the local
  retention boundary.
- Legacy Web compatibility routes for text/base64-file/URL import, refresh,
  document/base batch deletion, directory import/rescan/delete, and re-index
  now submit durable operations while preserving `jobId` responses. They no
  longer execute parsing, model work, or physical cleanup inside the HTTP
  request.
- OCR deletion, local model deletion (including managed runtime models and
  custom reranker registration cleanup), and Ollama deletion now submit
  `remove_ocr_model`, `remove_model`, and `ollama_delete` operations. Model
  cache migration POST now only submits `migrate_model_cache`; its filesystem
  plan/activation runs in the executor. The former synchronous GET preflight
  now submits the cancellable `plan_model_cache_migration` operation, and the
  generic `/api/operations` adapter accepts the same command. The Web model
  screen tracks the returned `jobId` for these removals, so an HTTP 202 is not
  rendered as completed deletion.
- The generic `/api/operations` adapter now normalizes directory/batch
  deletion, restore, URL refresh, model removal, and Ollama removal inputs and
  assigns their resource lanes; managed model download is admitted through the
  shared `model` lane. `TestOperationHandlerGuard` statically rejects direct
  model, physical-delete, migration, or long-document executor calls from the
  receipt-producing Web handlers.
- Runtime model catalog reads now prefer `StatusSnapshotter.CachedStatus`, a
  copy-on-read cache that never contacts the helper; compatibility controllers
  without the optional interface retain the old fallback. Model removal
  classification uses configuration/persisted catalog data and no longer calls
  `Runtime.Status` from the removal handler.
- Tests: `internal/operations/*_test.go`,
  `internal/web/operations_*_test.go`, and extension operation tests.

### P2 - resource scheduling and bounded retrieval

- Migration-free scheduler lanes cover `io`, `db_write`, `disk`, `network`,
  `model`, and `maintenance`, with per-base active quotas and delete priority.
- `/api/status` exposes lane limits, active work, queued work, active bases,
  total queue capacity, and per-base quota.
- Interactive retrieval has an end-to-end timeout and a bounded model-admission
  wait. Query variants and `TopK` have explicit caps. Scheduler waits map to
  HTTP 429; search timeouts map to HTTP 408. Metrics expose scheduler waits
  and search timeouts.
- A process-wide model semaphore is shared by interactive search and model
  operations, so a background operation cannot bypass an interactive slot.
- Tests: `internal/operations/scheduler_test.go`,
  `internal/knowledge/search_test.go`, and scheduler capacity API tests.

### P3 - maintenance and recovery foundation

- Orphan raw-file reconciliation can run as durable `maintenance_storage`.
- The safe maintenance contract supports dry-run, quarantine, explicit
  quarantine purge, retention-based expiry, and chunk-count repair. Results are
  counts and bytes, not source text. Quarantine is excluded from the active
  scan and collision handling is bounded.
- Forced process-kill drills now cover raw generation publication, upload
  staging publication, and raw quarantine moves at pre-rename and post-rename
  breakpoints. Upload startup ownership recovery removes orphan partial
  temporaries; an uploading session can retry the same bytes after either
  breakpoint. Raw pre-rename death leaves the old publication authoritative
  and a complete synced temp for bounded quarantine cleanup; post-rename
  death exposes the complete new generation without changing the old version.
  A quarantine pre-move death preserves the active source and empty reserved
  area, while post-move death leaves complete bytes only in quarantine for
  bounded retention or explicit purge.
- A synthetic corpus-scale maintenance drill builds 1,200 published file
  documents plus 1,200 orphan raw files, then drives dry-run, quarantine,
  retention expiry, and explicit purge through the same safe reconcile path
  used by `maintenance_storage`.
- A synthetic Windows corpus-scale lock drill holds a no-share OS handle on
  one orphan among 1,200, proves quarantine makes bounded progress, fails
  closed while the lock remains, and moves the exact remaining bytes after
  release without changing referenced raw.
- A synthetic POSIX corpus-scale advisory-lock drill holds `LOCK_EX` on one
  orphan among 1,200 and records that POSIX locks do not invalidate rename:
  quarantine preserves every byte, referenced raw stays unchanged, and purge
  remains explicit.
- Maintenance commands use the `maintenance` scheduler lane, persist progress,
  and are callable from Web and Agent.
- Tests: `internal/storage/storage_test.go`,
  `internal/knowledge/service_maintenance_test.go`,
  `internal/knowledge/service_maintenance_corpus_test.go`, and
  `internal/knowledge/service_maintenance_filelock_windows_test.go`, plus
  `internal/knowledge/service_maintenance_filelock_unix_test.go` and
  `internal/web/operations_maintenance_test.go`.

### P4 - read-path performance

- The indexing-status control endpoint is bounded independently of document-list
  pagination: SQL filters active `pending/processing` rows before scanning,
  materializes at most 200 metadata rows, and honors the HTTP request context.
  The old unbounded `listAllDocumentMetadata` production path was removed;
  migration 0017 adds the supporting `(lifecycle_state, status, updated_at, id)`
  index. `TestIndexingStatusIsBoundedAndCancellable` seeds 201 active rows,
  verifies the 200-row cap, and verifies cancellation. The affected package
  run `go test ./internal/knowledge ./internal/web ./internal/storage -count=1
  -timeout=20m` passed (Knowledge 131.833s, Web 262.242s, Storage 7.012s).
  The subsequent full current-source `go test ./... -count=1 -timeout=30m`
  also passed, including App 136.676s, Extension 95.728s, Knowledge 157.059s,
  Operations 9.025s, Runtime 47.886s, Storage 9.993s, Web 305.647s, and
  Scripts 1.053s.
- Restore source pagination now selects metadata only and fetches one full
  source document at a time with the operation context; the target existence
  check uses the same cancellation boundary. This bounds the page
  working set even when `raw_text` is large; `TestRestoreSourcePagesUseMetadataOnly`
  verifies that the page projection omits raw text while the explicit source
  lookup still returns it. Its normal and race runs passed. The subsequent
  full Knowledge package run passed in 110.025s, followed by a passing
  `go vet ./...`.
- URL capture recovery now reconciles leftover ready captures when an
  `import_url` or `refresh_url` item marker already proves the business effect.
  `TestResolvedURLOperationConsumesLeftoverCapture` verifies that replay keeps
  the existing document result and transitions the capture to `consumed` with
  an empty body; normal and race runs passed.
  The subsequent full App package run passed in 95.583s.
- `/api/bases/{id}/documents/children` provides bounded parent-scoped paging
  and breadcrumbs; migration `0009_document_children.sql` adds the index.
- The documents page no longer loads an entire base to construct a tree.
- Durable `reindex_base` no longer materializes the whole base: the Web
  receipt uses a scalar active-document count, while the Worker captures a
  `(created_at,id)` upper cursor and processes keyset pages of 50 metadata
  rows. A regression test creates a document during the first page and proves
  it is excluded from the fixed target window.
- Both directory-delete Web routes use a recursive SQL scalar count for
  progress instead of loading all sibling metadata into an in-memory tree;
  the durable delete Worker remains responsible for the fenced cleanup.
- The legacy flat document-list endpoint now supports bounded `limit/offset`
  pages with `total`/`hasMore`; its no-parameter compatibility response keeps
  the legacy array shape but returns only the default first page, emits
  `Deprecation: true`, and advertises the successor-version Link. This removes
  the last Web caller of the unbounded document-list read. `TestListDocumentsPageIsBounded`
  and `TestLegacyDocumentListProvidesBoundedCompatibilityMode` cover the
  service and HTTP contract.
- The local-model picker now uses `models.Manager.ListPage` and
  `App.ListLocalModelsPage`; `/api/local-models` accepts bounded `limit/offset`
  parameters and returns `nextOffset/hasMore` while preserving `models` and
  `cacheDir`. The no-parameter compatibility response uses the default bounded
  page and is marked `Deprecation: true`; managed-runtime/configured entries
  remain classified without an unbounded cache walk. `TestManagerListPageBoundsAndAdvances` and
  `TestManagedRuntimeModelAppearsInLocalModelList` cover paging and HTTP shape.
- Extension document listing now uses the same bounded page contract for both
  `knowledge_list_documents` and the `knowledge_list_bases(baseId)` outline
  branch. The legacy `documents` field remains, with `total`, `limit`, `offset`,
  and `hasMore` added; default limit is 50 and the service cap is 200.
  Extension integration tests cover explicit one-row pages for both routes and
  the full suite passed in 61.566s; the final lifecycle/outline regression also
  passed in 8.359s. This closes the local control-path implementation slice; large-
  directory response-size/p95 and compatibility retirement evidence remain
  P4 gates.
- Lightweight statistics use a short TTL cache with invalidation on relevant
  mutations. Vector diagnostics remain live.
- Tests: `internal/web/documents_children_test.go`,
  `internal/knowledge/stats_cache_test.go`, and
  `internal/knowledge/service_test.go` (`TestForEachReindexDocumentBatchUsesStableBoundedWindow`).

### P5 - frontend task state

- The frontend keeps an operation store, restores queued/running work after a
  refresh, polls durable status independently of route reads, and retains
  failed/cancelled cards with retry where the backend permits retry.
- Terminal document task updates replace the card action in place, so a failed
  import switches Cancel to Retry without a full-page render; the active-task
  summary excludes failed/cancelled cards while those cards remain visible.
- Route changes abort ordinary route GETs without aborting operation polling.
- Document task completion now refreshes only the current children page,
  preserving the surrounding document view, task cards, and checked rows. The
  affected preview is refreshed in place; import task completion updates task
  cards without rebuilding the route.
- Browser lifecycle behavior is exercised by `web/scripts/e2e.mjs`, including
  an explicit wait that was previously exposed to a transient full-render race.
- E2E submits a worker-invalid directory through the real UI and verifies its
  failed card and Retry action without stubbing the operation store.
- Every page-level rerender now takes a fresh generation, and document requests
  are bound to the selected base as well as the route generation. Durable task
  polling refreshes its store entry and card with the latest revision; successful
  cards are removed in place while failed/cancelled cards remain retryable.
- Web builds now record a deterministic SHA-256 `buildId` over the deployed
  assets. `/api/version` exposes that identity, and the shell/assets send
  `Cache-Control: no-cache` with a build-wide ETag, so an unchanged deployment
  revalidates as 304 while a new build invalidates stale browser copies.
  Header handling is applied before the Agent prefix is stripped. E2E performs
  a real backend process termination/restart on the same data home and port,
  proves both `webBuild` and asset ETag survive that restart, and repeats the
  304 revalidation. A Go HTTP matrix separately covers standalone assets, a
  new server instance, and proxied `/extensions/knowledge` assets.

### P6 - startup and process ownership

- Deferred startup recovery and background maintenance leave `/api/status` and
  `/healthz` available while reporting starting/degraded state.
- A crash-safe SQLite lock file provides data-directory instance ownership on
  Windows and POSIX without stale PID heuristics.
- Runtime helper close terminates the configured process tree and waits for
  process reaping before application close completes.
- On POSIX, a process-group drill creates a real shell-to-sleep descendant,
  calls the production terminator, and proves the descendant is gone by
  signal zero rather than inferring cleanup from the direct child's exit.
- Tests: `internal/storage/instance_lock_test.go`,
  `internal/runtime/manager_test.go`, and
  `internal/runtime/process_unix_test.go`.

## C01-C16 Coverage Snapshot

This is an evidence map, not a claim that the fault matrix is complete.

| Gate | Current evidence | Missing for closure |
|---|---|---|
| C01 | Generic failed operations preserve terminal state and retry events. `TestOperationTextImportFailureIsReceivableRetryableAndVisible` gates the first worker behind the same-base quota, asserts a 202 receiver with a stable document ID and no fabricated result, deletes the real target, then proves the replayable worker fails. HTTP exposes `failed`/`operation_failed`, the legacy job endpoint exposes the failure for UI polling, and retry advances to attempt 2 before failing again. Browser E2E now submits a worker-invalid directory through the real UI and asserts the card never reports success, reaches `failed` with Retry, and decrements active count. `TestLegacyParserProcessKillRecoversRawVersion` force-kills a real child App during its first external legacy parser call after the raw version is staged; a new owner replays attempt 2 and publishes exactly one vector-free generation. | SmartCare-scale production UI task pressure remains under the external two-base gate. |
| C02 | Restart replay test simulates command claim then worker loss. `TestStartRecoveryKeepsNewSubmissionQueued` exercises the explicit pre-dispatch breakpoint and proves recovery does not interrupt a new queued command. `TestImportOperationPreDispatchRecoveryCreatesBusinessEffect` runs the registered `import_text` adapter at the same post-claim/pre-dispatch breakpoint, restarts the App, and proves the persisted command and stable document target are unchanged while recovery creates exactly one ready publication with its chunks at attempt 2. `TestImportProcessKillReplaysExactBusinessEffect` force-kills a real child App after claim/admission and before executor dispatch; a new owner replays at attempt 3 with one ready document, source version, generation, and chunk set. | Release-host replay. |
| C03 | Import executors reuse preallocated/ready documents. `TestPublishedDocumentReusePreventsDuplicateBusinessEffect` proves a ready preallocated document is reused without generation/source/chunk duplication. `TestImportOperationRestartReusesPublishedDocument` runs the registered `import_text` adapter, resets the durable row to the exact post-publish/terminal-loss breakpoint, restarts the App, and proves recovery reports attempt 2 while reusing the same document, source version, generation, chunks, and one-document list. `TestImportProcessKillReplaysExactBusinessEffect` force-kills a real child App after the ready document/chunks commit and before terminal publication; a new owner replays at attempt 3 and reuses the same document, source version, generation, chunks, and one-document list. | Release-host replay. |
| C04 | Same/different fingerprints return the original operation or conflict; a bound key remains retrievable after the queue fills; uploads and URL captures are immutable and idempotent. Sixteen concurrent same-key submissions return one operation and execute one command. Terminal bindings now have a configurable retention window; expired retries return `ErrOperationExpired` and HTTP `410 operation_expired`. Server-issued credentials are signed with persisted secrets and scoped by type/target; API admission verifies signature, expiry, and scope before mapping retries to one stable operation key. Tests cover restart, rotation retention, immediate revocation, 12 concurrent clients, and expired operation bindings without replay. | Hours-scale production multi-client expiry drill and release-host repeat. |
| C05 | Submission and terminal writes use SQLite. A real `BEGIN IMMEDIATE` busy-writer injection proves a 100ms control-path deadline fails without committing, releases cleanly, and retries the original idempotency key into one submitted event and one execution. `TestSustainedBusyWriterKeepsHTTPControlPathsBounded` drives a real HTTP server while SQLite's application-pool writer lock is held: 30 uncertain submits and 10 cancellation attempts fail bounded without durable commands or partial cancel rows, while 50 operation status reads remain successful. After release, the original key creates exactly one command with one submitted event and cancellation converges without duplicate intent. | Release-host and production-load latency distribution repeat. |
| C06 | Delete fences block stale ingest and hide tombstones from search. `TestAncestorDeleteRejectsNewChildIDInjection` takes a scan snapshot, commits an ancestor tombstone, then proves a brand-new child ID is rejected and never visible. The upsert now evaluates the active base and full parent-chain fence in one write transaction. `TestCrossProcessDeleteFenceRejectsStaleSnapshot` performs the snapshot in one process, commits the ancestor delete in another, and proves the injected child fails closed and never becomes visible. | Release-host repeat. |
| C07 | Conditional mutation/activation fences exist. `TestStaleAttemptProgressAndFinishAreIgnored` proves stale-attempt progress and terminal callbacks cannot mutate the current attempt. `TestDeleteFenceRejectsStaleMoveSnapshot` captures an active parent/child snapshot, commits an ancestor tombstone, then injects stale move writes that detach the child and repoint the root. A real bug was found and fixed: the conditional document UPSERT reported success even when its active-scope predicate updated zero rows. `putDocument` now checks `RowsAffected` and returns `ErrConflict`; the test proves both stale writes are rejected, tombstones remain deleting, and the visible tree stays empty. `TestCrossProcessDeleteFenceRejectsStaleSnapshot` repeats the move timing across real processes and rejects every stale write. | Release-host drill. |
| C08 | A WAL read snapshot pins lexical recall, vector recall, context, titles, and diagnostics to one generation; a concurrent activation injection test proves no lane mixing, and hits expose `sourceVersion`/`indexGeneration`. Migration `0012` durably records retired generation/source identities and migration `0013` pins each generation's immutable raw path/hash. `TestHistoricalContextSurvivesGenerationSwitchUntilRetention` reads old evidence after activation, rejects a mismatched source version, and proves retention GC removes retired chunks, mappings, and raw paths before the historical request returns `historical_evidence_expired`. `TestHistoricalRawSurvivesUnpublishedCurrentPathReplacement` proves an old citation survives a legacy shared-path replacement, while the current citation fails closed on changed bytes; the HTTP raw API test covers current identity headers and retained old bytes after re-index. `TestSyntheticHistoricalCorpusLongReaderGCDrill` pins 20 historical chunks in a read transaction while GC removes all 12,000 retired chunks/mappings across 600 documents; the pinned view stays unchanged and later historical access fails closed. | SmartCare-scale long-reader/GC timing. |
| C09 | Active reads exclude deleting objects, and separately issued historical chunk/raw citations are checked against the current document/base tombstone before bytes or text are returned. A gated search test commits an ancestor delete after lexical recall, then proves hits, generations, and diagnostics disappear before response. `TestHistoricalEvidenceFailsClosedAfterDeleteFence` commits a tombstone after publishing generation 2 and proves both retained raw and separately issued context return `historical_evidence_expired` rather than ordinary 404/current bytes. The synthetic corpus drill extends fail-closed historical access across a 12,000-chunk retired generation after a pinned reader commits. `TestCrossProcessHistoricalReaderSurvivesGenerationGC` proves real cross-process replay: a child pins chunks and gen1 mapping, the parent deletes all retired data, and the child sees its pinned mapping/chunks until commit before observing post-GC zero counts. | SmartCare-scale long-lived reader replay. |
| C10 | Stale/dimension diagnostics and model-key filtering exist. A same-dimension model A→B search test proves model B returns only model B evidence. `TestModelSwitchFailureKeepsVectorSpaceAndDegrades` now runs a compact failure corpus: A vectors migrate to same-dimension B only under model B; a failed C rebuild preserves the active generation/source/model, marks `embedding_provider`, leaves no C vectors, and explicit vector search degrades to lexical evidence from old generation B; disabling embedding creates a lexical generation with zero active vectors. The test also exposed and now covers cleanup of an abandoned unpublished generation that previously caused duplicate chunk IDs on retry. `TestSyntheticCorpusModelSwitchFailureAndDisable` repeats A→B, all-document failed C, and disabled generations over 400 documents/1,600 chunks, with model-count SQL and bounded vector/lexical searches. `TestEmbeddingProcessKillRecoversStagedGeneration` force-kills a real child App on its first OpenAI-compatible embedding request after staged chunks commit; a new owner cleans the abandoned stage and publishes exactly one vector-complete generation. | SmartCare-scale model-switch/provider-host drill. |
| C11 | Queued/running cancellation, repeat cancel, retry rejection, and restart intent are covered. Web and Agent transports are exercised while real imports are blocked: `TestOperationCancelTransportConvergesToCancelled` covers the HTTP endpoint and legacy UI state; `TestOperationCancelToolCancelsRunningImport` covers `knowledge_operation_cancel` through the Agent tool dispatcher and scope check. Batch variants preserve committed work: re-index returns cancelled `{succeeded:1, partial:true}`, and delete removes exactly the first committed unit before stopping. `TestPublishedResultWinsCommittedCancelIntent` proves a committed cancel intent cannot roll back an authoritative published terminal result, while `TestQueuedDeleteBatchCancellationPreservesDocuments` proves a real queued durable delete batch remains fully cancelled and non-retryable without deleting documents. `TestDurableDeleteCleanupContinuesAfterCommittedCancel` pauses a durable single-document delete after the tombstone commits, records cancel intent there, and proves cleanup still removes the document and chunks with a succeeded, non-retryable result. `TestCrossProcessCancelIntentSurvivesOwnerKill` proves real cross-process cancel ownership: a child owner sees another process's committed intent and is killed before terminal write; a new owner emits one terminal event at attempt 1 and does not replay or accept retry. | Release-host repeat. |
| C12 | Upload binding/release and URL capture ownership have tests. Raw and upload staging writes publish same-directory synced temporaries by rename; successful replacement leaves no staging residue. Repeated raw replacement plus quarantine/purge converges count and bytes. Real child-process kills now cover raw generation publication, upload staging publication, and raw quarantine moves immediately before and after rename. Publication pre-rename death preserves the old raw version or retryable uploading session; post-rename death exposes complete bytes; recovery cleans upload temps and accepts retry. Quarantine pre-move death preserves the active source and zero retained bytes; post-move death preserves complete bytes only in quarantine; explicit purge converges in both cases. A 1,200-document/1,200-orphan drill proves exact dry-run accounting, content-preserving quarantine, half-by-retention cleanup, explicit purge of the remainder, and unchanged referenced raw/search/pinned citations at corpus scale. A 600-document/1,200-orphan Windows drill uses a real no-share handle to prove bounded progress, retry fail-close, and post-release convergence under a file lock. The same 600/1,200 POSIX drill records advisory-lock rename semantics and byte-exact convergence. | Release-host/cross-platform repeat. |
| C13 | Legacy `jobId` responses and Agent operation tools exist. Proxied legacy-client tests cover status, shell/JS assets, job polling, old-ID cancellation, text/base64-file/URL import, refresh, directory import/rescan/delete, and single/bulk delete/re-index. Compatibility routes submit durable operations instead of running work in HTTP. The local cache/restart matrix is now executable: content-addressed build identity, ETag 304, new-server and proxy revalidation, and E2E termination/restart all assert the same deployed identity. Real Chrome/CDP E2E now sustains six durable reindex tasks across two isolated bases while performing 30 rapid route changes and proves a delayed stale base response cannot overwrite the selected base. Five consecutive full E2E runs passed. Linux/WSL2 package runs now cover the Agent extension and complete Web control-plane test suites. | A production Agent host drill. |
| C14 | Migration version fields and rollback notes exist. Migration `0011` persists format/reader/writer envelope and migration status; startup rejects future schemas, future writer requirements, and interrupted migrations. Health, `/api/status`, and doctor expose the envelope. Product/build identity now uses `0.2.0-rc.1+storage.v2` (distinct from published `0.1.1`) and exposes the candidate Git SHA plus storage format/reader/writer bounds through CLI version metadata, `/api/status`, `/api/version`, and formal `BUILD-METADATA.json`; Agent-facing Extension v1 manifests expose the SDK-compatible numeric version `0.2.0`. An isolated SQLite backup drill records format/hash, restores in isolation, validates FTS/data, and proves post-backup delta absence. A synthetic 41.1MB/24,000-chunk drill adds 1,200 generation mappings and trigram FTS, records build/backup/copy/verify timing, backup SHA and size, a 790,528-byte post-backup data delta, format bounds, FTS retrieval, generation-orphan absence, and delete validation. Three runs passed. | Old-compatible binary release-host drill and SmartCare-scale backup timing/size deltas. |
| C15 | Queue admission rejects a new command at the configured limit, preserves the bound idempotent result, and scheduler snapshots expose capacity. `TestSustainedOverloadRejectsBoundedAndDrains` sustains demand against a saturated durable queue: a 20-command limit holds one blocked plus 19 queued commands and rejects 85 distinct/repeated demand attempts; 40 control rounds measure state-read, idempotent-retry, and rejection latency; cancellation releases capacity while the worker remains blocked; release drains every committed command. Five consecutive runs passed with submit p95 1.564-1.653ms, other control p95 samples at or below 0.527ms, drain 40.0-48.7ms, and effective drain throughput 390-475 completions/s. `TestSlowModelAndSustainedQueueDemandStayResourceBounded` adds a blocked model lane and a second blocked IO lane behind which 128 distinct 64KiB commands demand a 64-command durable queue: admission holds those two active plus 62 queued and rejects 66 (51.6%), four concurrent readers make 80 snapshots/status reads while saturated, cancelling one queued command releases admission before blockers return, and release drains with exactly 63 successful business effects. Five Windows runs held the SQLite/WAL delta at 5,992,728 bytes, peak RSS at 40.98-84.39MB, reader p95 at 4.004-4.747ms, submit/rejection p95 at or below 2.546ms/512.1microseconds, and drain at 223.219-385.080ms. `TestSQLiteDiskFullAtGenerationCommitPreservesActiveAndRecovers` uses SQLite's real page ceiling to produce `database or disk is full (13)` during staged-generation commit, proves no document/chunk/generation/vector/FTS leak, preserves lexical search, and recovers the same ID to 12,000 searchable chunks after capacity returns. Five runs passed. `TestCorpusRawStoreOSDiskFullPreservesAndRecovers` places a 600-document raw corpus on a constrained Linux tmpfs, forces a real raw-temp write to return kernel `ENOSPC`, proves no staged residue or published-content change, and recovers the same stable document ID after capacity returns. The same drill has an opt-in dedicated-volume mode for persistent filesystems; a local 12MiB ext4 loopback drill repeated the fail-closed/recovery contract five times. | Release-host persistent-volume RSS/disk/slow-reader/slow-model runs and SmartCare two-base acceptance remain external. |
| C16 | Startup recovery, operation stop timeout, instance ownership, duplicate `Start` rejection, and recovery/new-submission ordering have tests. A real child process owns recovery, accepts a new command while replaying an old one, and is force-killed at the active/claimed breakpoint; a new owner replays both exactly once. The exercise found and fixed a recover wake-up fanout bug that left a second recovered command queued. Local Ubuntu WSL2 execution covers instance ownership, Linux process-group descendant termination, and the full runtime/storage/operations/App/extension/Web packages, including durable recovery/cancel and parser/embedding process-kill drills. A real `CGO_ENABLED=0` Linux delivery binary also passes isolated version/doctor, healthz, instance-lock rejection, and SIGTERM smoke checks. Runtime, operations, App, storage, and knowledge test binaries also compile for darwin/arm64. | Linux/macOS release-host repeat and a production bounded-exit/process-tree drill. |

## Verification Snapshot

Commands executed against this work tree include:

```text
go test ./...
node web/scripts/contract-test.mjs
node web/scripts/e2e.mjs
git diff --check
```

Latest full validation after cancellation convergence, recovery-event, and
queue-capacity tests:

- `go test ./...`: passed. Notable wall times were `extension` 100.576s,
  `operations` 2.168s, and `web` 317.239s; cached packages remained green.
- `node web/scripts/contract-test.mjs`: passed (`web contract: ok`).
- `node web/scripts/e2e.mjs`: passed; the build produced 6 web files and the
  Chrome/CDP lifecycle check succeeded. One intermittent assertion exposed the
  background full-render race; the E2E now explicitly waits for the document
  row, while data-level refresh remains an open P5 pressure gate.
- `git diff --check`: no whitespace errors; the repository emits existing
  CRLF conversion warnings.

Follow-up validation after the targeted document-list refresh:

- `web/src/app.js` bundled successfully to `internal/web/dist/app.js`.
- `node web/scripts/contract-test.mjs`: passed.
- `node web/scripts/e2e.mjs`: passed with the data-level document refresh and
  route-abort suppression enabled.

Validation after the fixed-generation search snapshot:

- `go test ./...`: passed. The full run completed after the snapshot change;
  notable wall times were `extension` 168.787s and `web` 321.008s.
- `go test ./internal/operations`: passed with concurrent idempotent
  acceptance, duplicate-start rejection, pre-dispatch recovery/new-submission,
  and existing durable-queue tests at `-count=1`.
- `go test ./internal/knowledge`: targeted C03 reuse test passed at
  `-count=1`.
- `go test ./internal/knowledge ./internal/operations -count=1`: passed
  after the final-visibility delete fence; knowledge ran in 17.722s.
- `go test ./internal/knowledge -run
  TestVectorSearchDoesNotMixSameDimensionModels -count=1`: passed.
- Final regression after the delete-visibility and model-isolation changes:
  `go test ./...` passed (notably `extension` 103.077s, `knowledge`
  15.342s, and `web` 281.395s).
- `node web/scripts/contract-test.mjs`: passed.
- `node web/scripts/e2e.mjs`: passed; it built 6 web files and completed the
  Chrome/CDP lifecycle check.
- C04 retention follow-up: migration `0010_operation_idempotency_expiry.sql`
  adds a durable terminal expiry, `scheduler.idempotencyRetentionHours`
  configures it, and targeted Operation/HTTP tests prove a terminal retry
  stays idempotent before expiry and returns `410 operation_expired` after it.
- `go test ./internal/config ./internal/app ./internal/operations -count=1`:
  passed after the retention migration and configuration.
- `go test ./internal/web -count=1`: passed in 259.612s, covering the full
  Web API against the new migration-backed field.
- C13/P1b compatibility follow-up: `TestLegacyJobClientThroughExtensionProxyPrefix`
  passed, and the old single-document delete, directory delete, and document
  reindex routes now submit durable operations while preserving `jobId`
  responses. A full Web rerun passed in 262.646s.
- Model-operation follow-up: targeted Web tests for managed model removal,
  downloaded/custom reranker removal, OCR removal, Ollama removal, and
  deferred cache migration passed after being updated to wait for durable
  terminal state.
  `go test ./internal/app ./internal/web -run
  'TestManagedRuntimeModelAppearsInLocalModelList|TestLocalModel|TestModelCacheMigrationAPI|TestOCRModelAPIStatusAndRemove|TestOllamaModelRemoveUsesDurableOperation'
  -count=1` passed; the Web package completed in 42.217s.
- Complete legacy-route migration follow-up: text, base64-file, URL, refresh,
  directory, single/bulk delete, and re-index compatibility routes submit
  durable operations. Their async HTTP contracts and proxied legacy clients are
  covered by targeted Web tests; all six initially stale compatibility tests
  were migrated to wait for durable terminal state and passed.
- Latest model-operation regression: the targeted Web removal/migration tests,
  `go test ./internal/operations ./internal/app -count=1`, and a complete
  `go test ./internal/app -count=1` passed. A concurrent full `go test ./...
  -count=1` run passed every package except one transient
  `TestImportProcessKillReplaysExactBusinessEffect` attempt that ended with
  `parse_failed: conflict`; the same test then passed alone in 17.17s and the
  full App package passed in 71.859s. Therefore the repository-wide result is
  recorded as a flaky timing signal, not as an unconditional full-regression
  pass.
- Frontend/runtime final checks: `node web/scripts/contract-test.mjs`,
  `node web/scripts/e2e.mjs`, `go test ./scripts -run TestWriterGuard
  -count=1`, `go vet ./...`, and `git diff --check` passed. The remaining
  warnings are Git's existing LF-to-CRLF working-copy notices.
- Final full regression after the legacy-route migration: `go test ./...`
  passed (notably `extension` 91.322s and `web` 311.173s).
- `node web/scripts/contract-test.mjs`: passed.
- `node web/scripts/e2e.mjs`: passed; it built 6 web files and completed the
  Chrome/CDP lifecycle check after the route migration.
- `git diff --check`: no whitespace errors; existing CRLF warnings remain.
- Latest focused regression after generic Operation normalization, durable
  restore binding, phase-event tracing, and cached runtime status changes:
  `go test ./internal/web -count=1` passed in 308.427s;
  `go test ./internal/runtime ./internal/operations ./scripts -count=1`
  passed (`15.368s`, `4.584s`, and `1.195s` respectively); and the focused
  restore/cache-migration set passed in `23.477s`. These runs cover the
  no-handler-preflight cache migration path, managed/Ollama model removal,
  generic restore input, and nonblocking runtime model classification.
- Final full regression after those changes: `go test ./... -count=1`
  passed for every package; notable wall times were `app` 106.371s,
  `extension` 100.779s, `knowledge` 165.331s, `runtime` 22.935s,
  `storage` 7.344s, and `web` 351.099s. The previously intermittent process
  kill/replay test passed in this run.
- Final static/browser checks: `go vet ./...`, all three Writer/Operation
  handler guards, `node web/scripts/contract-test.mjs`, and
  `node web/scripts/e2e.mjs` passed; E2E built 6 web files and the Chrome/CDP
  lifecycle check passed. `git diff --check` reported no whitespace errors;
  only existing LF-to-CRLF working-copy warnings remain.
- Concurrency follow-up: the first full race run found two test-harness races
  (mutable gated-embedder coordination and concurrent child-process log reads)
  plus a 10-second historical-reader marker timeout under race overhead. The
  harness now uses immutable phase channels/atomic call sequencing, a
  synchronized log buffer, and a 90-second bounded marker wait. Targeted
  reproductions passed, followed by `go test -race ./... -count=1
  -timeout=20m` passing every package (`app` 164.088s, `knowledge`
  554.459s, `web` 626.484s, `storage` 125.407s); no race report remains.
- P2 fairness follow-up: `TestPerBaseQuotaDoesNotStarveAnotherBase` and its
  race run passed. With base-a's per-base slot occupied and another base's
  operation queued behind it, base-b was admitted and completed before the
  second base-a operation; this is a local fairness proof, not the required
  SmartCare two-base sustained-pressure report.
- Latest post-fairness validation: `go test ./internal/app ./internal/knowledge
  ./internal/operations ./cmd/architecture-baseline -count=1` passed (App
  72.532s, Knowledge 122.478s, Operations 4.476s, baseline 0.904s);
  `go test -race ./internal/operations -count=1` passed in 16.584s, and
  `go vet ./...` plus `git diff --check` also passed. The prior full race run
  remains green for the repository; this Operations-package race rerun includes
  the newly added fairness test. A subsequent full repository race rerun also
  passed; its exact package timings are recorded in the following bullet.
- Current full race after adding the scenario runner and its integration tests:
  `go test -race ./... -count=1 -timeout=20m` passed every package, including
  `cmd/architecture-scenarios` (2.955s), App (153.415s), Knowledge (553.852s),
  Operations (23.404s), Runtime (48.514s), Storage (126.929s), Web (599.322s),
  and Scripts (2.410s); no race report or timeout remained.
- C08 historical-evidence follow-up: targeted
  `TestHistoricalContextSurvivesGenerationSwitchUntilRetention` passed in
  0.10s. `go test ./internal/storage ./internal/knowledge -count=1` passed
  (storage 1.952s, knowledge 13.428s). `go test ./internal/web
  ./internal/extension -count=1` passed (web 324.000s, extension 129.933s).
  Final `go test ./... -count=1` passed (notably extension 97.889s, knowledge
  13.470s, operations 3.434s, storage 2.064s, and web 339.852s).
  `node web/scripts/contract-test.mjs` passed (`web contract: ok`). E2E built
  6 web files and passed the Chrome/CDP lifecycle check.
- C01 Web-import-failure follow-up: targeted
  `TestOperationTextImportFailureIsReceivableRetryableAndVisible` passed in
  9.93s. `go test ./internal/web -count=1` passed in 325.905s and
  `go test ./internal/operations -count=1` passed in 2.901s.
- C11 partial-cancellation follow-up: targeted
  `TestCancelBatchReindexKeepsCommittedPartialResult` passed in 9.27s.
  `go test ./internal/app ./internal/operations -count=1` passed (app
  11.556s, operations 2.508s). Targeted Web batch/import/cancel tests passed in
  40.737s, and `TestOperationCancelTransportConvergesToCancelled` passed in
  10.86s.
- Final regression after the C11 cancellation fixes: `go test ./... -count=1`
  passed (notably app 37.709s, extension 142.320s, knowledge 13.826s,
  operations 3.643s, runtime 24.711s, and web 365.274s). Contract test passed
  (`web contract: ok`), and E2E built 6 web files and passed the Chrome/CDP
  lifecycle check.
- C11 transport and browser follow-up:
  `TestOperationCancelToolCancelsRunningImport` passed in 10.81s, and targeted
  re-index/delete batch cancellation tests passed in 21.267s (individual runs
  11.77s and 9.32s). A browser failure-card run exposed a stale Cancel action
  and active-task count after the backend chip reached failed. The document
  card now updates actions and summary by data-level replacement. After the fix,
  E2E asserted failed + Retry, active count 1, and passed in 16.966s; contract
  test also passed.
- C11 publish/delete follow-up:
  `TestPublishedResultWinsCommittedCancelIntent` passed in 0.13s;
  `go test ./internal/operations -count=1` passed in 2.727s. Targeted
  re-index/delete cancellation tests passed, including the real queued durable
  delete batch in 11.78s on a repeat isolated run (a combined run on the same
  host later slowed to 48.56s without failure, so timing is not an acceptance
  claim here).
- C11 tombstone-boundary follow-up:
  `TestDurableDeleteCleanupContinuesAfterCommittedCancel` passed in 10.47s.
  `go test ./internal/knowledge ./internal/app -count=1` passed (knowledge
  13.574s, app 39.941s). Final post-boundary `go test ./... -count=1` passed
  (notably app 79.842s, extension 183.926s, knowledge 14.601s, operations
  3.511s, runtime 44.347s, and web 407.883s).
- Final C11 package regression: `go test ./internal/app
  ./internal/operations -count=1` passed (app 30.412s, operations 2.630s).
- Post-boundary browser validation: contract test passed (`web contract: ok`);
  E2E built 6 web files and passed the Chrome/CDP lifecycle check.
- C03 restart-reuse follow-up:
  `TestImportOperationRestartReusesPublishedDocument` passed in 10.50s.
  `go test ./internal/app -count=1` passed in 50.263s.
- C02 pre-dispatch follow-up:
  `TestImportOperationPreDispatchRecoveryCreatesBusinessEffect` passed in
  10.50s. A repeat `go test ./internal/app -count=1` passed in 60.608s.
- C07 stale-move bug-fix follow-up: targeted
  `TestDeleteFenceRejectsStaleMoveSnapshot` passed in 0.08s. Before the fix,
  the same test exposed that a conditional document UPSERT could affect zero
  rows yet report success against a committed tombstone. After checking
  `RowsAffected` and returning `ErrConflict`, `go test ./internal/knowledge
  ./internal/app -count=1` passed (knowledge 12.938s, app 60.400s). Final
  `go test ./... -count=1` passed (notably app 147.269s, extension 171.527s,
  knowledge 15.020s, operations 3.055s, runtime 42.938s, and web 359.113s).
  Contract test passed (`web contract: ok`), and E2E built 6 web files and
  passed the Chrome/CDP lifecycle check.
- C08 raw-citation follow-up: pinned raw identity, SHA-256 verification, and
  HTTP identity headers were added. The pinned citation test passed in 0.15s,
  proving that replacement bytes in the shared raw path fail closed before the
  new generation is activated. The raw API test passes for a current citation
  and returns `410 historical_evidence_expired` without raw bytes after
  re-index. Knowledge and web package regression passed (13.451s and
  321.444s); contract passed and E2E built 6 web files and passed
  Chrome/CDP lifecycle.
- C05 follow-up: `TestBusyWriterBoundedFailureAndIdempotentRetry` injects an
  external `BEGIN IMMEDIATE` writer lock and verifies bounded failure without
  durable leakage, followed by safe convergence on one operation after
  rollback. `go test ./internal/operations -count=1` passed in 2.653s.
- C14 compatibility follow-up: storage migration `0011` persists the current
  format envelope and inherits the P1a minimum reader/writer baseline (5).
  Targeted storage compatibility and isolated backup/restore drills passed;
  `go test ./internal/storage -count=1` passed in 2.040s.
- Final C14 regression after compatibility metadata and status/doctor/health
  exposure: `go test ./...` passed (notably `extension` 97.710s,
  `knowledge` 14.372s, `operations` 2.346s, `storage` 1.858s, and `web`
  341.777s).
- Post-C14 browser/contract validation: contract test passed; E2E built 6 web
  files and passed Chrome/CDP lifecycle.
- C06/C07 follow-up: the document upsert now checks the base and complete
  ancestor chain in the same write transaction. Targeted
  `TestAncestorDeleteRejectsNewChildIDInjection` and
  `TestStaleAttemptProgressAndFinishAreIgnored` passed; knowledge and
  operations full packages passed at `-count=1` (13.701s and 2.555s).
- Final regression after the ancestor-chain fence: `go test ./...` passed
  (notably `extension` 101.931s, `knowledge` 15.527s, `operations` 3.238s,
  and `web` 377.482s).
- C12 crash-safety follow-up: raw and upload staging writes now publish a
  synced temporary with atomic rename. `TestRawStoreWritesReplaceAtomicallyAndConverge`
  and the upload binding test verify final bytes, no temp residue, bounded
  active files, and quarantine/purge byte convergence. Storage, operations, and
  knowledge full packages passed at `-count=1`.
- Post-fence browser/contract validation: `node web/scripts/contract-test.mjs`
  passed; `node web/scripts/e2e.mjs` built 6 web files and passed the
  Chrome/CDP lifecycle check.
- Final C12 validation after atomic raw/staging publication:
  `go test ./...` passed (notably `extension` 79.503s, `knowledge` 12.140s,
  with `operations`, `storage`, and `web` cached from this work tree).
  A first full run exposed only a wall-clock-sensitive reranker timeout test;
  it now exercises provider cancellation deterministically and passed 10
  consecutive runs, after which the full suite passed.
- Post-raw-change browser/contract validation: contract test passed and E2E
  built 6 web files and passed Chrome/CDP lifecycle.
- C09 immutable raw-generation follow-up: targeted generation/raw/delete tests
  passed, then `go test ./internal/knowledge ./internal/storage ./internal/app
  -count=1` passed (knowledge 15.522s, storage 1.874s, app 60.274s). The first
  post-change full run passed every production package; its Web test exposed
  only the stale format-version assertion. After that test-only update,
  `go test ./internal/web -count=1` passed in 326.569s. Contract passed, E2E
  built 6 web files and passed Chrome/CDP, and the migration envelope records
  format 2/reader 6/writer 6/schema 13.
- C10 model-switch follow-up: targeted model-isolation/failure tests passed.
  The new corpus exposed two real defects—same-hash vectors could cross model
  generations, and an abandoned unpublished generation blocked retry—both now
  fixed. `go test ./internal/knowledge -count=1` passed in 13.498s; storage,
  operations, and app passed at `-count=1` (1.994s, 3.093s, 65.426s).
- C13 cache/restart follow-up: the targeted Go cache/proxy matrix passed in
  11.405s, and the full Web package passed at `-count=1` in 359.831s. Contract
  passed (`web contract: ok`). E2E built the content-hashed web bundle,
  terminated/restarted the real backend process on the same home and port,
  proved stable `webBuild`/ETag identity and 304 revalidation, then passed the
  Chrome/CDP lifecycle check.
- C15 sustained-overload follow-up: targeted `TestSustainedOverloadRejectsBoundedAndDrains`
  passed with the corrected report: 19 accepted queued commands plus one
  blocked active command, and 85 rejected attempts. Five consecutive runs
  passed with submit p95 1.564-1.653ms, control-path p95 no greater than
  0.527ms, drain 39.969-48.673ms, and 390.4-475.4 successful operations/s.
  The full operations package passed at `-count=1` in 3.617s.
- C16 cross-process follow-up:
  `TestCrossProcessKillRecoversRecoveredAndNewCommands` exposed and fixed the
  recover wake-up fanout defect. One targeted run and five consecutive runs
  passed; the repeated package run completed in 1.383s. Final regression after
  the fix passed with `go test ./internal/operations ./internal/app -count=1`
  (operations 2.732s, app 67.481s).
- C12 process-kill follow-up:
  `TestRawPublishProcessKillConverges` and
  `TestUploadPublishProcessKillConverges` each passed at both breakpoints and
  were repeated five times. Recovery/storage regressions passed with
  `go test ./internal/storage ./internal/operations ./internal/knowledge
  -count=1` (storage 3.321s, operations 2.733s, knowledge 14.807s), followed
  by the app package in 61.776s.
- C12 raw-quarantine process-kill follow-up:
  `TestQuarantineMoveProcessKillConverges` force-killed a real child process
  immediately before and after `RawFileStore.Quarantine` renamed the active
  source. At each breakpoint the active tree, quarantine bytes, and quarantine
  statistics converged as declared; pre-move retry plus purge left the active
  source recovered, and post-move purge removed the retained copy. The target
  passed five consecutive runs, followed by `go test ./internal/storage
  -count=1` in 3.271s.
- C02/C03 real-owner process-kill follow-up:
  `TestImportProcessKillReplaysExactBusinessEffect` force-killed real child
  Apps at the pre-dispatch and post-publish/terminal-loss boundaries. A new
  owner replayed each command at attempt 3 without duplicating the document,
  source version, active generation, or chunks; finish-event totals stayed at
  the expected historical count (1 for pre-dispatch and 2 after an earlier
  successful publication). Five consecutive targeted runs passed in 71.659s.
  Affected-package regression passed with `go test ./internal/operations
  ./internal/app -count=1` (operations 3.419s, app 55.387s).
- C05 sustained busy-writer HTTP follow-up:
  `TestSustainedBusyWriterKeepsHTTPControlPathsBounded` ran 30 concurrent
  submits, 50 reads, and 10 concurrent cancels against a real HTTP server
  while a write transaction owned the SQLite writer lock. Five consecutive
  runs passed with submit p95 182.014-185.316ms, status p95
  9.149-11.098ms, cancellation p95 181.670-182.612ms, and post-release
  cancellation drain 22.473-30.243ms. No uncertain response persisted a
  command or partial cancel; recovery preserved exactly one submitted event.
  The full Web package passed at `-count=1` in 226.367s.
- C04 server-credential follow-up:
  `TestIdempotencyCredentialSurvivesRestartAndRotation` proved restart
  validation, rotation retention, revocation fail-close, stable operation-key
  mapping, tamper rejection, and scope checks.
  `TestConcurrentCredentialClientsKeepDistinctBindingsUntilExpiry` ran 12
  clients to distinct successful operations and proved repeats did not
  execute; making operation bindings expire while credentials remained valid
  returned `ErrOperationExpired` without additional replay.
  A signed credential with a past expiry also failed closed with
  `ErrIdempotencyCredentialExpired`.
  `TestOperationCredentialAPIAdmitsAndRevokesSignedScope` exercised issuance,
  scoped HTTP submission, retry, rotation, revocation, tamper rejection, and
  the issuance limiter. Each targeted combination passed five consecutive
  runs (operations 1.326s, Web API 32.701s). Affected-package regression
  passed with `go test ./internal/storage ./internal/operations
  ./internal/web -count=1` (3.606s, 3.590s, 246.424s).
  After tightening the wire format to an `opcred.` token prefix, final
  operations/Web regression passed in 2.898s and 235.305s.
- C11 cross-process cancel-kill follow-up:
  `TestCrossProcessCancelIntentSurvivesOwnerKill` let a real child process
  claim and run a command, committed cancellation from a second process, and
  force-killed the child after it observed `cancel_requested=1` but before a
  terminal write. Afterward the row remained `cancelling` with exactly one
  cancel event and no finish event. A new owner recovered it to `cancelled` at
  attempt 1 with the expected submitted/claimed/cancel_requested/finished
  event sequence, rejected retry, and executed the command zero additional
  times. Five consecutive runs passed in 1.024s; the full operations package
  passed at `-count=1` in 2.932s.
- C14 synthetic large-backup follow-up:
  `TestSyntheticLargeDatabaseBackupRollbackDrill` built and backed up an
  isolated 41,107,456-byte database containing 1,200 documents, 24,000
  chunks/trigram FTS rows, and 1,200 generation mappings. Three consecutive
  runs passed in 11.204s. Observed ranges were build 2.798-3.025s, backup
  write 30.081-109.024ms, restore copy 44.051-147.386ms, and restore
  verification 34.414-115.247ms. Every run recorded a distinct SHA-256 and
  the exact 41,107,456-byte rollback point. A 500-chunk/790,528-byte source
  delta after backup was absent after restore; format bounds, selected FTS
  retrieval, generation-orphan absence, and post-restore delete/FTS cleanup
  were validated. The full storage package passed at `-count=1` in 6.872s.
- C15 resource-bounds follow-up:
  `TestSlowModelAndSustainedQueueDemandStayResourceBounded` held a one-slot
  model lane with a blocked operation, then held the one-slot IO lane with a
  second blocked operation while submitting 128 distinct bounded commands with
  64KiB encoded payloads. The 64-command durable limit accepted two active and
  62 queued operations, rejected 66 of 128 queued demand (51.6%), and the
  scheduler snapshot stayed at that
  saturated boundary. While both lanes remained blocked, four readers performed
  20 rounds of scheduler snapshot plus model/IO status reads. Cancelling the
  first queued operation reached `cancelled` and admitted exactly one replacement
  before either blocker returned. Releasing both blockers drained all durable
  work in 223.219-385.080ms with one slow-model effect and 62 bounded-IO effects
  (the 63rd queued command was intentionally cancelled). Five consecutive
  Windows runs passed in 0.48-0.69s each. Baseline disk was 609,576 bytes and
  peak SQLite/WAL footprint was 6,602,304 bytes in every run, for a fixed
  5,992,728-byte delta below the 8MiB assertion. Baseline RSS was
  16.364-63.029MB, observed peak RSS was 40.985-84.394MB below the 256MiB
  assertion, and observed Go heap was 0.994-3.408MB. Submit p95 was
  2.051-2.546ms, rejection p95 was no more than 512.1 microseconds, reader p95
  was 4.004-4.747ms, and full `go test ./internal/operations -count=1`
  regression passed in 4.161s. The resulting 63 effective operations drained at
  163.6-282.2/s; the test now reports this throughput and the initial rejection
  rate directly.
- C15 SQLite disk-full commit follow-up:
  `TestSQLiteDiskFullAtGenerationCommitPreservesActiveAndRecovers` imports a
  501-chunk baseline, preallocates a second document, reads SQLite's actual
  `page_count` (316 pages at 4,096 bytes), and sets `max_page_count` eight pages
  higher. A 12,000-chunk staged generation then crosses the ceiling and the real
  ingest path returns `database or disk is full (13)`. The fault snapshot kept
  exactly two active documents, the authoritative 501 baseline chunks, one
  generation mapping, zero embedded vectors, and an unchanged `chunk_fts_data`
  shadow-table size. The baseline remained ready at generation 1/source version 1,
  lexical search still returned only baseline generation-1 evidence, and the
  independent fault marker returned zero hits. After raising the ceiling, retrying
  the same preallocated ID produced a ready generation-1 document with 12,000
  chunks; lexical search returned that document, and `PRAGMA integrity_check`
  stayed `ok`. Five consecutive runs passed in 1.680-2.150s each (9.505s total);
  the full knowledge package passed at `-count=1` in 37.235s. This is a real
  SQLite storage-capacity fault, not an OS-volume/corpus-scale disk-exhaustion claim.
- C15 OS-level raw-store ENOSPC follow-up:
  `TestCorpusRawStoreOSDiskFullPreservesAndRecovers` imports 600 markdown
  documents through the real file-ingest path with their raw store on a
  4MiB Linux tmpfs and SQLite on the normal filesystem. The 600 corpus files
  contain exactly 1,228,800 raw bytes. The test leaves only 4,096 bytes free,
  then submits a real 17,464-byte markdown import; its raw staging temp write
  returns kernel `no space left on device` while `AddFileDocumentWithID` marks
  only that preallocated document `failed/parse_failed` with no raw path.
  All 600 published raw files retain their SHA-256 hashes, no `.tmp-` staging
  residue remains, lexical searches find first/middle/last documents, the
  fault marker returns zero hits, and SQLite integrity remains `ok`. After
  deleting filler, retrying the same ID publishes a ready generation with
  exact raw bytes and a lexical hit. Five Ubuntu 22.04/WSL2 runs passed in
  4.87-6.56s: build 3.541-5.096s, fault return 3.576-4.806ms, and recovery
  1.233-1.466s. The Linux knowledge package including this test passed at
  `-count=1` in 1:20.51 with observed peak RSS 75,004KB. This is real
  kernel-level capacity exhaustion on a local constrained tmpfs; it does not
  replace release-host persistent-volume or SmartCare-scale disk evidence.
- C15 ext4 persistent-filesystem raw-store ENOSPC follow-up:
  `TestCorpusRawStoreOSDiskFullPreservesAndRecovers` now accepts
  `SHUTU_TEST_RAW_VOLUME` for a dedicated mounted raw filesystem. The mode is
  disabled unless `SHUTU_TEST_RAW_VOLUME_ACK=dedicated-destructive-volume` is
  also set; it requires an absolute directory, at most 16MiB capacity, and an
  absent `raw/` path, and checks reserved filler names before starting. A
  one-time 12MiB ext4 loopback volume used 1KiB blocks, a journal, and zero
  reserved-block percentage. After the 600-document/1,228,800-byte corpus,
  the drill left 3,072 bytes available; a 17,464-byte real import returned
  kernel ENOSPC. All published hashes, searches, no-staging-residue checks,
  SQLite integrity, and stable-ID recovery assertions passed.
  Five Ubuntu/WSL2 runs passed in 9.31-10.64s: fault return
  54.880-276.403ms and recovery 1.201-1.802s. The default tmpfs mode also
  remained green for five runs in 5.68-7.36s. The ext4 run exposed its
  reserved-block accounting behavior and used `mkfs.ext4 -m 0` so the
  dedicated harness had an unambiguous allocation boundary. This is local
  loopback ext4 evidence, not a release-host persistent-volume acceptance;
  the guarded volume mode is the release-host entry point for that follow-up.
- C10 embedding process-kill follow-up:
  `TestEmbeddingProcessKillRecoversStagedGeneration` configured a real child
  App against an OpenAI-compatible HTTP embedding endpoint. The child replayed
  a durable text import and reached the endpoint only after
  `putChunksReplace` committed its staged chunks; the parent then force-killed
  the child process tree. Direct SQLite inspection at the breakpoint found the
  operation `running` at attempt 1, the document `processing` and incomplete,
  active generation 0/desired generation 1, staged chunks present, zero
  generation mappings, and zero vectors. On restart, startup recovery resumed
  the document, the new owner discarded the abandoned stage, replayed the same
  operation at attempt 2, and completed embedding against the same endpoint.
  The authoritative publication became generation 1/source version 2 with
  `embedding_model=openai:kill-model`, exactly one generation mapping, vectors
  for every chunk, and a generation-1 lexical hit. Five consecutive runs passed
  in 0.84-1.14s each (5.060s total); the full app package passed at
  `-count=1` in 59.753s.
- C01 parser process-kill follow-up:
  `TestLegacyParserProcessKillRecoversRawVersion` configured a real test-binary
  helper as the App's `.doc` legacy parser and submitted a durable `import_file`
  command with a completed upload session. The child App claimed the operation,
  staged the immutable raw version, and entered the external parser; the helper
  wrote its marker and blocked, after which the parent force-killed the child
  App process tree. At the breakpoint SQLite showed the operation `running` at
  attempt 1, the document `processing`/incomplete at active generation 0, zero
  chunks/mappings/vectors, and no published document raw path; the raw store held
  exactly one staged `.generations` version with the exact uploaded bytes. On
  restart, the new owner replayed at attempt 2, invoked the parser again, and
  published one generation mapping with the converted text as generation
  1/source version 2; lexical search returned that document and generation.
  Five consecutive runs passed in 0.76-0.90s each (4.240s total); the full app
  package passed at `-count=1` in 55.279s.
- P5 frontend two-base stress follow-up:
  `web/scripts/e2e.mjs` now builds two isolated API-created bases with three
  ready text documents each, triggers six durable document reindexes from the
  real UI, then performs 30 rapid hash-route changes across overview, bases,
  import, and documents while operation polling continues. A marker outside the
  SPA screen proves no full-page replacement; the task summary reaches zero
  active cards after all six backend operations reach `succeeded`; each stress
  base retains three documents with one chunk each. The test then patches browser
  fetch so an Alpha document-children response resolves 250ms after the current
  Beta response and asserts the Beta rows remain while no Alpha row appears.
  This exposed and fixed two real defects: direct page rerenders reused a stale
  route generation, and operation polling assigned a new entry to a `const`,
  aborted polling, and left successful cards queued. Five consecutive
  Chrome/CDP runs passed in 14.493s, 14.742s, 15.163s, 14.946s, and 14.911s.
  The web contract passed, and `go test ./internal/web -count=1` passed in
  228.045s.
- C06/C07 cross-process fence follow-up:
  `TestCrossProcessDeleteFenceRejectsStaleSnapshot` used a real child process
  to read root/nested metadata from the same SQLite database. The parent then
  committed the ancestor tombstone before releasing the child to replay its
  stale scan: both moves detached/re-titled existing rows, and another process
  injected a new child ID under the deleted root. All three conditional writes
  returned `ErrConflict`; both tombstones stayed `deleting`, the injected ID
  stayed absent, and `ListDocuments` remained empty. Five consecutive runs
  passed in 1.116s; the full knowledge package passed at `-count=1` in
  14.056s.
- C08/C09 synthetic historical-corpus follow-up:
  `TestSyntheticHistoricalCorpusLongReaderGCDrill` built 600 documents with
  two generations each: 12,000 active and 12,000 retired chunks in a
  46,338,048-byte database. A lexical corpus query returned the target's 20
  active chunks in 27.739-36.871ms. A reader transaction pinned one historical
  generation while the writer aged and pruned all 12,000 retired chunks and
  mappings; the pinned read stayed unchanged across the 2.273-3.315s GC and
  completed in 2.892-3.926s. After commit, historical context failed closed,
  the retired raw path was reported and removed, and current generation
  context/raw bytes remained valid. Five consecutive runs passed in 33.886s;
  the full knowledge package passed at `-count=1` in 23.264s.
- C09 cross-process reader follow-up:
  `TestCrossProcessHistoricalReaderSurvivesGenerationGC` built 120 documents
  and 2,400 retired plus 2,400 active chunks. A real child process opened the
  database independently, fixed a WAL read transaction, and pinned 20 chunks
  plus the generation-1 mapping before the parent could mutate them. The
  parent then aged and pruned every retired generation. The child continued to
  observe all pinned chunks and the same mapping source version after GC, and
  only after commit observed zero retired chunks/mappings and retained active
  chunks. Parent-side historical context then failed closed, retired raw was
  removed, and current raw remained valid. Five consecutive runs passed in
  6.004s (GC 369.054-422.918ms; child end-to-end 495.804-608.727ms); the full
  knowledge package passed at `-count=1` in 22.730s.
- C10 synthetic model-switch follow-up:
  `TestSyntheticCorpusModelSwitchFailureAndDisable` exercised 400 documents
  and 1,600 chunks. All documents migrated from model A to B; vector search
  returned only generation/source 2 evidence. A failing model C provider was
  attempted across every document, left zero C vectors, and preserved all
  active B vectors/generations plus the `embedding_provider` error. Explicit
  disabled-provider reindexing created 1,600 lexical chunks with zero active
  vectors. Five consecutive runs passed in 63.134s: migration B
  3.107-4.568s, failed C 2.478-3.143s, disabled 1.917-2.799s, vector search
  24.693-30.376ms, lexical search 6.428-7.516ms. The full knowledge package
  passed at `-count=1` in 38.569s.
- C12 corpus-scale maintenance/raw-move follow-up:
  `TestSyntheticCorpusStorageReconcileMovesExactlyOrphans` imported 1,200
  markdown documents through the real file-ingest path and created 1,200
  orphan raw files. Each raw file was exactly 2,048 bytes, giving 2,457,600
  referenced bytes and 2,457,600 orphan bytes (4,915,200 raw payload bytes
  total); observed database size was 11,350,016-11,567,104 bytes. The drill
  drove `ReconcileStorageSafe`, the service path behind durable
  `maintenance_storage`, through dry-run, quarantine, retention expiry, and
  explicit purge. Dry-run reported exactly 1,200 orphans/2,457,600 bytes and
  moved nothing; both instrumented passes emitted `scanning`, `counting`, and
  monotonic progress through the pipeline's declared 3,600 raw+document units.
  Quarantine moved exactly the orphan set and every active and quarantined
  file matched its SHA-256. Aging exactly 600 files removed only those
  1,228,800 bytes, explicit purge removed the remaining 600/1,228,800 bytes,
  and all 1,200 referenced raw paths stayed unchanged. After cleanup, lexical
  search found the target and its generation-pinned raw citation still
  resolved the exact bytes.
  Five consecutive runs passed in 30.33-32.31s: build 11.103-12.380s,
  dry-run 101.518-112.176ms, quarantine 410.194-835.221ms, retention
  574.580-593.033ms, explicit purge 138.726-144.307ms, and search
  24.877-32.158ms. This is a synthetic Windows corpus-scale pipeline drill,
  not SmartCare-scale, production-host, OS-volume-exhaustion, or file-lock
  evidence. The full knowledge package passed at `-count=1` in 68.368s.
- C12 corpus-scale file-lock follow-up:
  `TestCorpusStorageReconcileFileLockStopsAndConverges` imported 600 markdown
  documents and created 1,200 orphan raw files. Each file was 2,048 bytes,
  giving 1,228,800 referenced bytes and 2,457,600 orphan bytes; database size
  was 5,816,320-5,836,800 bytes. A real Windows no-share handle was held on
  the 601st sorted orphan while `ReconcileStorageSafe` ran. The first pass
  quarantined the prior 600 files (1,228,800 bytes), then stopped on the
  locked file with an OS `used by another process` rename failure while
  reporting 601 scanned/1,230,848 orphan bytes. All 600 referenced raw paths
  kept their SHA-256 hashes, the locked source kept its metadata, and the
  active tree retained exactly 600 referenced plus 600 orphan files.
  A second pass while the handle remained open scanned only the locked
  2,048-byte orphan, quarantined nothing, and failed closed. After release,
  the next pass quarantined the remaining 600 files/1,228,800 bytes. The
  quarantine contained all 1,200 orphan files/2,457,600 bytes with exact
  contents, the active tree contained exactly the 600 referenced files, the
  target remained searchable, and explicit purge converged to zero.
  Five consecutive Windows runs passed in 19.52-22.04s: build 6.442-8.826s,
  first locked pass 198.665-239.274ms, retry while locked
  30.848-32.698ms, post-release recovery 208.232-256.531ms, and search
  18.149-26.424ms. This narrows corpus-scale file-lock evidence on the
  synthetic Windows host only; Linux/macOS, release-host, and SmartCare-scale
  file-lock evidence remain open. The full knowledge package passed at
  `-count=1` in 100.316s.
- C12 POSIX file-lock follow-up:
  `TestPOSIXCorpusFileLockQuarantineConverges` imported 600 markdown documents
  and created 1,200 orphan raw files. Each raw file was 2,048 bytes, giving
  1,228,800 referenced bytes and 2,457,600 orphan bytes. It held an exclusive
  advisory `flock` on the 601st sorted orphan while running
  `ReconcileStorageSafe`. As expected on POSIX, that lock did not invalidate
  rename: quarantine reported all 1,200/2,457,600 bytes, every retained file
  matched its expected bytes, all 600 active referenced raw files kept their
  SHA-256 hashes, the target stayed searchable, and explicit purge converged
  to zero. Five Ubuntu 22.04/WSL2 runs passed in 9.07-10.55s: build
  8.914-10.404s, quarantine 28.266-35.560ms, and search 1.928-5.080ms. The
  Linux knowledge package passed at `-count=1` in 1:10.10. This is local WSL2
  semantic evidence, not Linux/macOS release-host evidence.
- `git diff --check`: no whitespace errors; existing CRLF warnings remain.
- C16 Linux/WSL2 cross-platform follow-up:
  `TestTerminateProcessTreeKillsProcessGroupDescendants` was added for POSIX.
  It starts a shell in its own process group, records a real `sleep`
  descendant PID, invokes production `terminateProcessTree`, reaps the shell,
  and confirms the descendant receives `ESRCH` on signal-zero probing. The
  Linux amd64 binaries were cross-compiled from the same work tree and run on
  local Ubuntu 22.04/WSL2 kernel 6.6.87.2. The full Linux runtime package
  passed in 6.941s and the full Linux storage package passed in 6.192s,
  including instance ownership, raw/quarantine process-kill convergence,
  migrations, and the 41,107,456-byte backup/restore drill. Five Linux runs
  of the process-group test passed in about 10ms each; five instance-lock
  runs passed in 0.11-0.13s. This is local WSL2 evidence, not a Linux
  release-host or production drill.
- C16 Linux/WSL2 durable-operation follow-up:
  Linux amd64 test binaries for `internal/operations` and `internal/app`
  were cross-compiled from the same work tree and executed on local Ubuntu
  22.04/WSL2. The operations package passed at `-count=1` in 4.07s with
  observed peak RSS 38,992KB; it includes cross-process command recovery and
  cross-process cancellation owner kill. The App package passed at
  `-count=1` in 30.13s with observed peak RSS 67,584KB; its startup-recovery
  logs executed during the run, and the package covers real child-owner
  import recovery, embedding kill recovery, legacy parser kill recovery, and
  durable cancellation/partial-result behavior. These are local WSL2
  package-level proofs, not Linux release-host or production-process drills.
- C16 Linux/WSL2 Agent/Web follow-up:
  Linux amd64 test binaries for `internal/extension` and `internal/web` were
  cross-compiled from the same work tree and executed on local Ubuntu
  22.04/WSL2 from each package's normal `go test` working directory. The
  extension package passed at `-count=1` in 34.65s with observed peak RSS
  67,584KB, covering the Agent protocol and operation tools. The Web package
  passed at `-count=1` in 2:23.81 with observed peak RSS 67,584KB, covering
  durable operation APIs, legacy compatibility proxies, static assets, and
  HTTP server behavior. A first extension invocation from the repository root
  exposed only a test-relative working-directory assumption in the runner,
  not production code; rerunning from `internal/extension` matched `go test`
  semantics and passed. These remain local WSL2 package proofs, not
  production Agent-host or Linux release-host drills.
- C16 darwin/arm64 build follow-up:
  `CGO_ENABLED=0 GOOS=darwin GOARCH=arm64 go test -c` successfully built the
  runtime, operations, App, storage, and knowledge test binaries from the
  same work tree. This validates that the POSIX process-group code and these
  packages have no Windows-only build assumption, but performs no macOS
  process, filesystem, lock, recovery, or performance execution.
- C16 Linux delivery-binary smoke follow-up:
  `CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -trimpath -ldflags='-s -w'`
  produced the real single-binary deliverable from this work tree. On local
  Ubuntu 22.04/WSL2 with an isolated temporary data home, `version` reported
  0.1.1 and `doctor` reported schema 14, format 2, reader/writer 6,
  migration status ready, database/storage-format/storage-permission checks
  `ok`, and overall health `ready`. `serve` started on an isolated loopback
  port; `/healthz` returned `{"ready":true,"status":"ready"}`, a second
  process failed closed with `another Knowledge instance owns this data
  directory`, and SIGTERM terminated the first process without escaping the
  smoke timeout. This validates the real Linux artifact startup/ownership
  path in WSL2, but is not a production Linux release-host acceptance.
- Final Windows full regression:
  `go test ./... -count=1` passed after the new corpus, fault, lock, OS-exhaustion,
  and cross-platform tests were added. Notable package times were App 107.155s,
  extension 95.200s, knowledge 147.128s, operations 5.240s, runtime 23.321s,
  storage 8.968s, and Web 285.169s. `node web/scripts/contract-test.mjs`
  passed (`web contract: ok`), and `node web/scripts/e2e.mjs` built 6 web
  files and passed the real Chrome/CDP lifecycle check; the combined frontend
  validation completed in 23.03s. This validates the current work tree on
  Windows but does not replace release-host, SmartCare-scale, or old-binary
  acceptance.
- C14 release-identity governance follow-up: Linux binary smoke exposed that a
  format-2 candidate still reused the published product version `0.1.1`,
  making rollback identity ambiguous. The candidate is now
  `0.2.0-rc.1+storage.v2`; formal packaging injects the candidate Git SHA with
  `-X ...internal/version.GitCommit=<sha>`, parses the CLI JSON, and fails on a
  mismatch of version, SHA, format 2, or reader/writer 6. `BUILD-METADATA.json`
  records those storage bounds. Packaged root/example Agent manifests use
  Extension v1's numeric compatibility identity `0.2.0`, while README discloses
  both that extension version and the candidate build. `/api/status` retains
  the string `version` and adds a structured `build`; `/api/version` exposes
  the same build envelope plus `webBuild`.
  Because the working tree intentionally remains uncommitted, the packaging
  validation used a disposable detached candidate built from a temporary Git
  index containing the current source (excluding `.local`); it did not advance
  the branch. Candidate `23ad69e58d9586e1395b180d37cd6d56ba134526` packaged as
  `shutu-knowledge-0.2.0-rc.1+storage.v2-linux-amd64.zip` with SHA-256
  `19aad942f8f333ce8fc0b1a4045c55e950f59aae787660b27ec9b26709370e11` and binary
  SHA-256 `3936330db283c00ceb8248d2a66b024cacea1b9c60e2f9620a54d2e787ee9f2e`.
  The same candidate also packaged as
  `shutu-knowledge-0.2.0-rc.1+storage.v2-windows-amd64.zip` with SHA-256
  `8165d995f64fa409d8c8408bab3418d73f8af159a98a332e22f5697b87f43e10` and binary
  SHA-256 `adfe542e860fbde4f1ad307c0d61dcc55c06b84cc026e99a435ef9dfc26d49cc`;
  its deployed extension manifest reported numeric `0.2.0`.
  The package script's own validation and an independent WSL2 execution of the
  extracted ELF under `env -i HOME=/tmp/... PATH=/usr/bin:/bin` both reported
  version `0.2.0-rc.1+storage.v2`, that Git SHA, format 2, reader/writer 6,
  and minima 6/6. Targeted version, CLI, and Web endpoint tests passed;
  `git diff --check` passed. This is local packaging evidence only and does
  not replace release-host rollback or old-compatible-binary acceptance.
- Release-identity full-regression follow-up: the first post-identity
  `go test ./... -count=1` run found that the Agent SDK intentionally accepts
  only numeric `MAJOR.MINOR.PATCH` extension manifest versions and rejected
  both `Manifest()` and `extension.yaml` when given the full SemVer candidate
  build suffix. Only `TestManifestValidatesAgainstSDK` and
  `TestDeployedManifestValidates` failed; every other package in that run
  passed. The product build identity remains `0.2.0-rc.1+storage.v2`, while a
  new `version.ExtensionVersion()` derives protocol-safe `0.2.0` for generated
  Agent manifests and the packaged manifests use the same numeric identity.
  After the split, both previously failing manifest tests and the complete
  extension package passed. Frontend contract and real Chrome/CDP E2E checks
  also passed. Formal packaging was rerun after this identity split; its
  manifest/version consistency check passed, and the final observed hashes are
  recorded above.
- Release-host acceptance runner follow-up: `cmd/release-acceptance` now gives
  Linux, macOS/arm64, and Windows hosts a repeatable native entry point. Its
  `core` profile runs storage, operations, runtime, App, and Knowledge packages
  at `-count=1`; `agent` and `web` add their production transport packages, and
  `all` runs the complete set. Every run records the native host, candidate
  commit, dirty state, expected storage envelope, guarded raw-volume settings,
  per-stage logs, duration, status, and binary identity in
  `.tmp/release-acceptance/<timestamp>/result.json`. The runner rejects a
  cross-compiled environment and refuses a dirty work tree (tracked or
  untracked) unless
  `-allow-dirty` is used for local diagnostics. On Linux, setting
  `SHUTU_TEST_RAW_VOLUME` and
  `SHUTU_TEST_RAW_VOLUME_ACK=dedicated-destructive-volume` passes the dedicated
  persistent-volume acknowledgment through to the existing guarded ENOSPC
  drill. On this Windows work tree, the `identity` profile passed after
  recording candidate `11121bef014058945053dab94366c9740cb07632`, format 2 and
  reader/writer 6, and manifest version `0.2.0`; the first `agent` profile run
  exposed and fixed a Windows `exec`/ldflags injection bug, then passed the
  complete extension package in 64.937s. This tool makes release-host runs
  repeatable but is not itself release-host evidence and does not provide the
  SmartCare corpora, dedicated volumes, old-compatible binary, or external
  hosts still required by the remaining gates.
- Release-acceptance core-profile follow-up: the first core run correctly
  continued past a failed package and produced a failed summary, exposing a
  timing race in `TestUploadBindsAtomicallyAndReleasesOnSuccess`: it checked
  for `released` immediately after observing `succeeded`, while production may
  still be completing post-terminal release. The test now waits explicitly for
  terminal upload release; 20 consecutive repetitions and the full operations
  package passed. The same run showed that one grouped `go test` command hid
  per-package ownership, so core now records storage, operations, runtime, App,
  and Knowledge as separate stages and log files. The repaired local diagnostic
  run passed in 3m23.922s (storage 8.839s, operations 5.494s, runtime
  15.961s, App 1m1.888s, Knowledge 1m49.923s, manifest check 562.5microseconds)
  with candidate `11121bef014058945053dab94366c9740cb07632`. It ran on a dirty
  local Windows work tree with `-allow-dirty`; it remains Windows diagnostic
  evidence, not a clean-candidate or release-host acceptance.
- C13 route-loading follow-up: page routing created a full-screen loading
  state before route data loaded, then `render()` recreated the same loading
  view after `/api/status`. Pages that append their content left that second
  loading section above the real content, so every route could display
  "Loading page data" after it had loaded. `render()` now clears the route
  loading shell before dispatching to the page renderer, while the initial
  route shell remains available during the status request. The Chrome/CDP E2E
  now asserts after every navigation that `#screen .loading-state` is absent;
  the web contract and full browser lifecycle suite passed.

- C13 documents/import display follow-up: browser desktop and mobile screenshots
  exposed `operations is not iterable` when the active-operation list was
  empty, because the API serialized an absent Go slice as JSON `null`. The
  list endpoint now emits `[]`, and startup recovery also tolerates a null
  client payload. A new empty-list API contract test gates the JSON shape. The
  same review found that mobile documents hid source, chunks, timestamps, and
  actions behind horizontal table scrolling; at 640px the table now becomes a
  labeled card per document with every field and action visible. Chrome/CDP
  E2E adds Chinese and 390px-wide documents/import navigation checks, and the
  web contract, targeted Web API test, and full browser lifecycle suite passed.

- Upload input lease follow-up: bound staging is now released after succeeded or
  cancelled operations, after non-retryable failures, and after failures that
  have reached `DefaultMaxAttempts`. Startup recovery also compensates for the
  crash window between terminal operation persistence and best-effort file
  removal. `TestUploadReleasesAfterNonRetryableFailure` and
  `TestStartupReleasesTerminalBoundUpload` passed under both normal and race
  execution; both assert the durable `released` state and absent staging file.

- Current upload-lease follow-up: `TestUploadReleasesAfterNonRetryableFailure`
  and `TestStartupReleasesTerminalBoundUpload` passed in normal and race modes.
  The implementation releases bound staging after a successful/cancelled
  operation, a non-retryable failure, or the final allowed attempt, and startup
  recovery compensates for the terminal-write/post-cleanup crash window. Both
  tests assert the durable `released` state and absent staging file.
- Current validation after upload-lease recovery: `go test ./... -count=1`
  passed (App 149.387s, Extension 138.153s, Knowledge 194.711s, Operations
  12.959s, Runtime 28.439s, Storage 17.200s, Web 416.840s, Scripts 4.308s,
  plus all command packages); `go test -race ./internal/operations -count=1`
  passed in 23.232s and `go test -race ./internal/app -count=1` passed in
  139.266s. A concurrent `go test -race ./...` attempt was not accepted as
  evidence because Windows resource contention caused a Knowledge timeout and
  an Operations p95 assertion failure; both failing tests passed when rerun
  individually, and the affected package race suites passed in isolation.
- Current executor marker follow-up: single text/file import, single delete,
  base delete, batch delete, URL import, and URL refresh now short-circuit on a
  committed/skipped item marker before repeating their business effect. File
  replacement cleanup uses the operation context and an independent
  `replace:<documentId>` marker. The historical post-publish process-kill
  fixture explicitly removes the marker to retain coverage for old rows that
  committed business data before marker support; the isolated process-kill,
  App operation, Knowledge, and Web tests passed.
- Current replace/dedup follow-up: a same-title `replace` whose incoming bytes
  have the same content hash as the old document used to delete the old row and
  then be incorrectly marked as a duplicate, leaving the replacement title
  absent. `planFileBatch` now overrides the duplicate decision only for a title
  collision being replaced, so the new document is imported. The normal and
  race regressions `TestAddFilesConflictStrategiesAndDedup`,
  `TestAddFilesReplaceReimportsSameContentAfterTitleCollision`, and
  `TestAddFilesUsesResolvedConflictStrategy` passed; this closes a local
  P1B replacement correctness gap but does not close the scale/release-host
  gates.
- Validation after the replacement fix: `go test ./... -count=1` passed on the
  current worktree (App 111.152s, Extension 96.268s, Knowledge 151.063s,
  Operations 5.519s, Runtime 20.738s, Storage 7.844s, Web 300.510s, Scripts
  0.948s, plus all command and supporting packages); `go vet ./...` and
  `git diff --check` also passed. This is current-source local evidence only;
  it does not close the SmartCare, release-host, cross-platform, or rollback
  gates listed below.
- Current batch replay follow-up: `import_files` now carries committed/skipped
  item state from the App adapter into the bounded Knowledge planner. Resolved
  items bypass conflict resolution and worker ingestion, so a committed
  replacement cannot be deleted and imported again during replay; its stable
  allocation and title remain available for the result. The pre-dispatch
  regression `TestImportFilesExecutorSkipsResolvedItems` passed in normal and
  race modes, together with the stable-allocation regression.
- Affected-package validation after the batch replay change: `go test
  ./internal/app -count=1` passed in 74.354s and `go test
  ./internal/knowledge -count=1` passed in 111.720s; the corresponding
  targeted race suites passed in 27.561s and 2.626s. This confirms the local
  batch replay change without expanding the claim to the external scale and
  release-host gates.
- Current aggregate-directory replay follow-up: `import_directory`,
  `rescan_directory`, and `delete_directory` now check their aggregate item
  marker before filesystem scan, rescan, or deletion. Import lookup uses the
  normalized tracked source path; rescan/delete use the stable directory ID.
  The pre-dispatch regression `TestDirectoryExecutorsSkipResolvedAggregates`
  seeds a committed marker and removes the source directory, proving the
  marked operations finish without repeating business work; normal and race
  runs passed.
- Full current-source validation after the aggregate-directory change:
  `go test ./... -count=1` passed (App 127.395s, Extension 125.826s, Knowledge
  149.114s, Operations 7.522s, Runtime 18.463s, Storage 9.671s, Web 304.313s,
  Scripts 1.150s, plus all command and supporting packages). The affected App
  and Knowledge package tests also passed independently, and `go vet ./...`
  passed. At that historical point, the full concurrent race attempt remained
  intentionally unclaimed because Windows resource contention made it an
  invalid all-package race run; the later serial full-race result is recorded
  below.
- Current singleton-effect marker follow-up: OCR/local model download and
  removal, managed model loading/removal, reranker self-test, model-cache plan
  and migration, Ollama pull/delete, and SQLite/raw maintenance now use a
  durable per-operation effect marker containing a bounded result. Resolved
  markers short-circuit before the external or filesystem effect and replay
  the stored result; local model/OCR retries reconcile an already-installed
  artifact, and Ollama retries reconcile the remote tag list before repeating a
  pull/delete. `TestSingletonExecutorsSkipResolvedEffects` also checks that
  the Operation result equals the marker result, and passed in normal and race
  modes. This closes the local P1B marker/result recovery gap; real
  remote/service failure recovery remains a release-host gate.
- OCR download admission follow-up: the legacy `/api/ocr/model/download`
  handler now passes the request context into Durable Operation submission,
  matching the other long-operation routes. The cancelled-request regression
  `TestOCRModelDownloadHonorsRequestCancellation` passed in normal and race
  modes and verified that no `download_ocr_model` row is created after request
  cancellation. This covers admission cancellation only; once accepted, the
  operation remains durable and its external model download still requires
  service-failure and release-host replay evidence.
- Validation after singleton-effect markers: `go test ./internal/app -count=1`
  passed in 88.186s; the focused marker/replay/lifecycle race set passed in
  39.587s. The current full App race suite passed in 176.244s. The subsequent
  `go test ./... -count=1` passed all packages (App 178.213s, Knowledge
  144.251s, Web 337.234s); `go vet ./...` and `git diff --check` also passed.
  These remain current-source local checks and do not close SmartCare,
  release-host, cross-platform, or rollback gates.
- Full current-source race validation was then rerun serially to avoid the
  previously observed Windows package contention:
  `go test -race ./... -p 1 -count=1 -timeout=25m` exited 0 and passed every
  package, including App (174.746s), Knowledge (483.287s), Operations
  (22.903s), Runtime (20.278s), Storage (95.570s), Web (509.890s), and
  Scripts (3.215s), with no race report or timeout. `-p 1` is an execution
  isolation choice, not a substitute for SmartCare two-base pressure or
  release-host acceptance.
- Current P6-03 shutdown follow-up: `App.Close`, `UpdateConfig`, and startup
  failure cleanup now share a bounded `closeRuntimeWithContext` helper. Legacy
  controllers without `CloseWithContext` are isolated behind a deadline, while
  the production runtime path still receives the shutdown context directly.
  `TestCloseRuntimeWithContextBoundsLegacyController` passed in normal and race
  modes. This is local compatibility evidence only; real helper process-tree
  kill, SIGTERM/CTRL+C, and Linux/macOS release-host exit evidence remain open.
- Validation after the P6-03 fallback change: the focused lifecycle normal test
  passed in 0.195s and its race run passed in 1.152s; the broader current
  App/repository validation is recorded in the singleton-effect entry above.
  These are still local checks; final review requires the release-host
  process-tree and cross-platform evidence.
- P6-03 busy-runtime follow-up: `runtime.CloseWithContext` no longer waits for
  the helper request mutex before initiating shutdown. It closes the owned
  stdout pipe, triggers the process-tree stop once, waits on the process handle,
  and the serialized response reader also selects on the request deadline and
  process exit. `TestManagerCloseWithContextBoundsBusyHelper` passed in normal
  and race modes; the full `internal/runtime` race suite passed in 17.835s.
  The affected-package normal run passed App in 95.341s and Runtime in
  14.357s; the current App race suite passed in 173.967s. This remains local
  helper evidence; real release-host process trees, signals, and Linux/macOS
  acceptance are still open.
- Full normal regression after the P6-03 runtime pipe/stop change:
  `go test ./... -count=1 -timeout=25m` exited 0 for every package, including
  App (133.246s), Extension (124.294s), Knowledge (156.689s), Runtime
  (21.042s), Storage (7.378s), Web (302.574s), and Scripts (1.091s), plus all
  command and supporting packages. This is current-source local evidence and
  does not close SmartCare, release-host, cross-platform, or rollback gates.
- Final P6-03 integration rerun after guarding helper startup races:
  `go test ./internal/runtime -count=1 -timeout=8m` passed in 11.545s,
  `go test -race ./internal/runtime -count=1 -timeout=8m` passed in 17.699s,
  and `go test -race ./internal/app -count=1 -timeout=10m` passed in 176.077s.
  The helper now rejects late starts after manager shutdown and terminates a
  process that races with shutdown; this remains local Windows evidence and
  does not replace Linux/macOS release-host process-tree or signal runs.
- Full current-source race validation after the helper pipe and startup-race
  fixes: `go test -race ./... -p 1 -count=1 -timeout=30m` exited 0 and passed
  every package, including App (176.531s), Knowledge (483.739s), Operations
  (23.253s), Runtime (19.227s), Storage (96.318s), Web (509.055s), and
  Scripts (3.212s), with no race report or timeout. Serial package execution
  isolates known Windows resource contention; it remains local evidence and
  does not close SmartCare, release-host, cross-platform, or rollback gates.
- P1a/P1b production-call-graph audit: `App.NewWithOptions` constructs
  `knowledge.Service` with `jobMgr=nil`; current Web/Operation paths use the
  Durable Operation executors and do not call the legacy `Service.ReindexBase`
  or directory job submitters. Those methods remain only as an explicit
  compatibility/test seam. Production Knowledge mutations route through the
  `storage.DB` facade and its Writer/`WriteTx` APIs; the remaining direct
  `db.Exec` occurrences are facade calls or migration/test-only paths, not a
  second production writer queue. This narrows the P1a seam but does not close
  the required full static migration report or release-host evidence.

- Maintenance cleanup bounded-batch follow-up: terminal operation
  payload/result expiry, expired URL-capture cleanup, expired upload-session
  cleanup, and terminal bound-upload lease release now process at most 100 IDs
  per control-write/query batch. Each loop honors cancellation before the next
  database operation and each staging path is removed individually, so an
  abnormal backlog cannot create one unbounded ID slice or one unbounded UPDATE.
  `TestMaintenanceCleanupProcessesRowsInBatches`,
  `TestURLCaptureCleanupProcessesRowsInBatches`, and
  `TestUploadMaintenanceCleanupProcessesRowsInBatches` passed in normal and
  race modes with more than one batch, absent staging files, cleared capture
  bodies, and cancelled-context assertions. The full `internal/operations`
  normal suite passed in 7.578s. This is local boundedness evidence only;
  SmartCare-scale backlog, persistent-volume behavior, and release-host
  convergence measurements remain open.

- Delete/recovery long-path boundedness follow-up: document-tree and base
  cleanup now page cleanup references by stable document ID; generation and
  chunk removal use fixed control-writer batches with the caller's context.
  `RecoverInterrupted` selects bounded ID batches with a cursor, preventing
  unchanged pending rows from being rescanned indefinitely, and stale URL
  refresh scans use a `(created_at,id)` cursor. The normal/race regressions
  `TestDirectoryDeleteCleanupUsesBoundedPages` and
  `TestRecoverInterruptedUsesBoundedCancellableBatches` passed, covering more
  than one cleanup page, more than 256 recovery rows, and cancellation. The
  existing tombstone-resume regression also passed. This is local boundedness
  evidence; SmartCare-scale long readers, persistent-volume failures, and
  release-host recovery convergence remain open.

- Storage-reconcile visitor follow-up: `RawFileStore.WalkAll` and `CountAll`
  avoid materializing the complete active raw-file path list. The Knowledge
  reconcile path now builds referenced paths through visitors, counts
  documents/chunks with cancellable queries, and processes orphan files one at
  a time while preserving dry-run, quarantine, retention, purge, and progress
  semantics. Focused normal tests
  `TestReconcileStorageSafeQuarantinesAndPurgesOrphans` and
  `TestReconcileStorageRemovesOrphansAndFixesCounts` passed; the synthetic
  corpus regression `TestSyntheticCorpusStorageReconcileMovesExactlyOrphans`
  passed in 47.277s normal and 125.418s race mode. The corpus covers 1,200
  documents and 1,200 orphan files; these are local boundedness/semantic
  results only, and SmartCare-scale, persistent-volume, and release-host
  evidence remain open.

- P1a long-path context follow-up: document/base/raw-citation reads, legacy
  raw-generation migration, the ingest start fence, progress writes, vector
  reuse queries, and landed vector batches now use caller-owned context
  variants; compatibility wrappers retain the old background-context API.
  The Writer contract is preserved: cancellation before admission may return
  `context.Canceled`, while cancellation after admission may return
  `storage.ErrWriteUnknown`, requiring durable-state reconciliation rather than
  assuming that no write occurred. `TestReindexAndRawCitationHonorCanceledContext`
  and `TestPutChunkVectorsHonorsCanceledContext` passed in normal and race
  modes. This is local cancellation-boundary evidence; release-host fault and
  post-admission replay drills remain open.

- Directory long-path context follow-up: synchronous directory import/rescan
  preparation, stable-path lookup, container creation, directory metadata
  updates, child replacement/deletion, failure markers, and final container
  reads now use the worker-owned context. The compatibility APIs retain their
  background-context wrappers, while cancelled durable directory execution
  stops before creating a container. `TestRunDirectoryImportHonorsCanceledContext`
  plus the directory/import/rescan/repoint/delete normal and race regressions
  passed. This closes the local directory admission/cancellation seam only;
  large-tree cancellation latency and release-host replay evidence remain open.

- P1a/P1b operation-read context follow-up: executor replay/admission branches
  now use cancellable Knowledge reads for documents, bases, deleting
  tombstones, and directory stable-path lookup instead of silently falling back
  to background context. Compatibility wrappers remain available for legacy
  callers. The App package normal regression passed in 108.320s, and the
  operation-focused race regression covering aggregate, singleton, URL,
  cancellation, retry, and committed-cleanup cases passed in 149.320s. This
  is a local context-propagation result; release-host cancellation and
  post-admission replay evidence remain open.

- Operation item-marker context follow-up: executor-side committed, skipped,
  and failed marker writes now use the active worker context; the legacy helper
  wrappers retain background behavior only for non-worker compatibility calls.
  This prevents a cancelled worker from silently waiting on a marker write
  outside its cancellation boundary. A cancellation race showed that the
  final `cancelled` marker itself must still be durable after the worker
  context is cancelled; only that control write now uses
  `context.WithoutCancel` with a five-second deadline, while business work
  remains cancellable. The aggregate/singleton/cancellation/retry App normal
  regression passed in 79.767s and race regression in 133.880s. A
  release-host post-admission marker-replay drill is still required.

- Operation progress context follow-up: executor progress callbacks now pass
  the worker context through the operation service into the control write,
  including the transaction query, update, and phase event insert. This keeps
  progress persistence inside the worker cancellation boundary; terminal
  `finish` persistence remains deliberately durable after executor return.
  The delete and base-reindex aggregate paths also preserve `partial` results
  when a post-effect item-marker write fails. The focused App operation normal
  regression passed in 74.715s and the Operations/App race regression passed
  (Operations 2.419s, App 134.800s); the full normal suite below confirms the
  current-source integration baseline.

- C11 cancellation race follow-up: the worker now registers its cancellable
  context in the process-local active-run map before the pre-dispatch hook and
  re-reads the durable cancel intent after registration. This closes the
  claim-to-worker-registration window where a cancel request could be
  persisted but miss the worker and allow business execution to start with a
  live context. `TestCancelRecordedBeforeWorkerRegistrationCancelsContext`
  passed in normal (0.596s) and race (3.000s) modes; the complete Operations
  package passed in normal (6.558s) and race (27.480s) modes. This is local
  C11 evidence; cross-process kill/restart and release-host evidence remain
  required.

- P1b worker-read seam follow-up: upload-session validation now exposes a
  cancellable lookup; batch/file import conflict detection and rename
  selection now propagate the worker context and return database errors; and
  custom reranker KV cleanup now has a cancellable context variant. The new
  `TestImportConflictLookupsHonorCanceledContext`,
  `TestCustomRerankerDeleteHonorsCanceledContext`, and
  `TestUploadForOperationHonorsCanceledContext` regressions passed in normal
  and race modes, together with the related import/operation App and Knowledge
  selections. Compatibility wrappers remain for synchronous callers.

- P1b model-operation read follow-up: local model manifest lookup and shared
  OCR status inspection now expose context-aware variants and the download/
  remove replay branches use them. `TestModelReadContextHonorsCancellation`
  passed in normal and race modes, together with the related App model/import
  regression selection. Filesystem inspection itself remains bounded and the
  context is checked before and after it; release-host model-switch and
  long-reader evidence remains a separate gate.

- Current package regression after the worker-read and model-read context
  additions: `go test ./internal/app ./internal/knowledge ./internal/models
  ./internal/operations -count=1` was rerun as isolated package commands;
  App passed in 109.130s, Knowledge in 124.914s, Models in 2.759s, and
  Operations in 7.310s. The context-focused Knowledge/Models and operation
  regressions also passed in race mode. This package evidence supplements,
  but does not replace, a final serialized full-suite run and the external
  SmartCare/release-host gates.

- Latest current-source full normal regression after the claim-to-worker
  cancellation-race fix: `go test ./... -count=1 -timeout=30m` exited 0 for
  every package. App took 168.811s, Extension 113.560s, Knowledge 151.403s,
  Operations 8.110s, Runtime 53.621s, Storage 7.535s, Web 407.605s, and
  Scripts 1.007s; all command packages passed as well. This is the newest
  local integration baseline; SmartCare, release-host, Agent Host,
  cross-platform, and rollback acceptance remain open.

- Latest current-source full normal regression after the upload, conflict,
  reranker, and model-read context additions: `go test ./... -count=1
  -timeout=30m` exited 0 for every package. App took 169.721s, Extension
  149.829s, Knowledge 156.199s, Models 4.032s, Operations 12.164s, Runtime
  23.483s, Storage 9.865s, Web 329.407s, and Scripts 1.265s; all command
  packages passed as well. This is the newest local full-suite baseline;
  SmartCare, release-host, Agent Host, cross-platform, and rollback
  acceptance remain open.

- Latest current-source serialized full race regression after the upload,
  conflict, reranker, model-read, and claim-to-worker cancellation fixes:
  `go test -race ./... -p 1 -count=1 -timeout=30m` exited 0 for every package.
  App took 190.498s, Extension 122.113s, Knowledge 490.015s, Models 4.088s,
  Operations 28.223s, Runtime 19.929s, Storage 97.824s, Web 654.125s, and
  Scripts 3.248s; all command packages passed as well. No race report or
  timeout occurred. This closes the current local serialized race baseline,
  not the SmartCare, release-host, Agent Host, cross-platform, or rollback
  gates.

- Latest current-source full normal regression after operation-read context
  propagation: `go test ./... -count=1 -timeout=30m` exited 0 for every package.
  App took 160.821s, Extension 101.239s, Knowledge 152.769s, Operations
  10.520s, Runtime 19.248s, Storage 7.409s, Web 356.169s, and Scripts
  1.010s; all command packages passed as well. `go vet ./...` and the Writer
  static guard passed afterward. This is the newest local baseline, not a
  substitute for SmartCare, release-host, Agent Host, cross-platform, or
  rollback acceptance.

- Latest current-source full normal regression after the bounded final
  cancellation-marker fix: `go test ./... -count=1 -timeout=30m` exited 0 for
  every package. App took 160.087s, Extension 123.161s, Knowledge 147.471s,
  Operations 7.594s, Runtime 22.257s, Storage 7.534s, Web 365.832s, and
  Scripts 1.301s; all command packages passed as well. `go vet ./...` and the
  Writer static guard passed afterward. This is the newest local baseline;
  SmartCare, release-host, Agent Host, cross-platform, and rollback acceptance
 remain open.

- Latest current-source full normal regression after progress-context
  propagation and aggregate partial-result handling: `go test ./... -count=1
  -timeout=30m` exited 0 for every package. App took 158.833s, Extension
  97.846s, Knowledge 145.847s, Operations 7.603s, Runtime 45.407s, Storage
  8.812s, Web 345.566s, and Scripts 1.216s; all command packages passed as
  well. `go vet ./...`, the Writer static guard, and `git diff --check` also
  passed; the last command emitted only existing LF/CRLF conversion warnings.
  This is the newest local baseline; SmartCare, release-host, Agent Host,
  cross-platform, and rollback acceptance remain open.

- Current normal integration baseline after the visitor/counter change:
  `go test ./... -count=1 -timeout=30m` exited 0 for every package. App took
  167.500s, Extension 92.225s, Knowledge 156.194s, Operations 9.731s,
  Runtime 25.045s, Storage 7.567s, Web 312.764s, and Scripts 1.066s; all
  command packages also passed. `go vet ./...` passed and `git diff --check`
  reported no whitespace errors (only existing LF/CRLF conversion warnings).
  This is the latest local normal evidence and does not close SmartCare,
  release-host, Agent Host, cross-platform, or rollback gates.
- Web package rerun after the OCR admission-context fix:
  `go test ./internal/web -count=1 -timeout=10m` passed in 259.096s; the
  focused OCR cancellation regression also passed in normal and race modes.
  This closes the local regression check for that handler change only.
- Latest current-source full normal regression after directory context
  propagation: `go test ./... -count=1 -timeout=30m` exited 0 for every package.
  App took 165.726s, Extension 108.534s, Knowledge 148.058s, Operations
  10.288s, Runtime 22.136s, Storage 6.786s, Web 344.997s, and Scripts
  1.072s; all command packages passed as well. This is the newest local
  integration baseline; it still does not close SmartCare, release-host,
  Agent Host, cross-platform, or rollback gates.

## Latest Control-Context Evidence

- P1B-07 control-plane context propagation: `GetContext`, `CancelContext`, and
  `RetryContext` now bind operation reads, control Writer transactions, event
  inserts, and final snapshots to the caller context. The new Operation Web
  handlers, legacy job status/cancel handlers, and Extension operation
  status/cancel/retry tools pass their request context; compatibility wrappers
  retain the background-context API for older synchronous callers. Accepted
  workers continue using an independent lifecycle context, so a disconnected
  client cannot cancel already accepted business work. The new
  `TestOperationControlReadsHonorCanceledContext` plus cancellation/retry
  regressions passed in normal (0.935s) and race (3.998s) modes; the complete
  Web and Extension packages also passed in 328.045s and 87.862s. This closes
  the local control-request cancellation seam only; Writer-busy, Agent Host
  disconnect, cross-process control failure, and release-host evidence remain
  open.

- Directory worker-read follow-up: `importChildFile` now checks the worker
  context after the bounded file read and uses the context-aware title lookup;
  only a genuine `ErrNotFound` may continue as a new child, while cancellation
  or other database errors stop the import. `TestDirectoryChildTitleLookupHonorsCanceledContext`
  passed in normal (0.768s) and race (2.781s) modes. This closes the local
  directory title-conflict read seam; large-tree cancellation latency and
  release-host replay evidence remain open.

- After the directory worker-read follow-up, the complete Knowledge package
  regression `go test ./internal/knowledge -count=1 -timeout=15m` exited 0 in
  119.848s. This confirms the change against the package's full current test
  surface; it does not close SmartCare-scale, persistent-volume, or
  release-host gates.

- Model control-plane read follow-up: local model pagination now accepts a
  context, and App aggregation passes it through model manifests, reranker
  self-test KV reads, base configuration, runtime fallback status, OCR status,
  and model-removal preflight. Reranker self-test persistence also uses the
  worker context, and a cancelled managed-runtime fallback returns before any
  model self-test call. Models, Knowledge, and App focused normal/race
  regressions passed; the existing managed-runtime and singleton-effect tests
  passed normal (12.382s) and race (17.042s). This closes the local model-read
  cancellation seam only; large-cache, Agent Host disconnect, and release-host
  model-switch evidence remain open.

- Base/scope/stat read follow-up: Web base listing, detail/config checks,
  delete/restore/reindex admission, statistics, import, and file-operation
  handlers now use request context. Extension auto-context base enumeration and
  Search/document/stat/vector provider reads use the same context-aware path;
  background wrappers remain only for compatibility callers. The affected
  Knowledge, Models, App, and Web focused regressions passed in normal mode
  (0.363s, 1.245s, 31.921s, 42.556s) and serial race mode (1.918s, 2.199s,
  16.280s, 51.880s); `go vet ./...` and `git diff --check` also passed. This
  closes the local request-read cancellation seam only; large-library reads,
  Writer-busy behavior, Agent Host disconnect, and release-host replay remain
  open.

- Document/history read follow-up: Web document detail, directory/document
  admission probes, chunk paging, and explicit search-history read/delete paths
  now use request context; search-history writes use `ExecContext` so they still
  enter the bounded Writer while honoring cancellation. The Knowledge and Web
  focused normal regressions passed in 1.234s and 16.315s. Compatibility
  wrappers remain for synchronous callers; remaining synchronous write-entry
  audit, large-directory cancellation, Writer-busy behavior, and release-host
  disconnect evidence remain open.

- Latest full local regression after the document/history context changes:
  `go test ./... -count=1 -timeout=30m` passed all packages and command modules
  (App 196.540s, Extension 153.797s, Knowledge 164.315s, Models 2.605s,
  Operations 7.275s, Runtime 20.939s, Storage 8.192s, Web 538.621s,
  Scripts 1.106s). Serial `go test -race ./... -p 1 -count=1 -timeout=30m`
  also passed all packages (App 234.902s, Extension 145.294s, Knowledge
  526.890s, Models 3.840s, Operations 30.196s, Runtime 26.274s, Storage
  102.763s, Web 767.167s, Scripts 2.955s), with no race or timeout. This is
  the current local baseline only; SmartCare scale, persistent-volume fault,
  Agent Host, cross-platform, and rollback gates remain open.

- Durable worker-read bypass follow-up: text/file/batch imports, URL import and
  refresh replay, RestoreBase, directory deletion, and the compatibility base
  reindex entry now use context-aware Base/Document/Chunk/Raw reads throughout
  their worker path. Cancelled-context coverage for single, batch, URL, and
  restore entry points passed in normal (0.250s) and race (1.706s) modes. This
  closes the identified local worker-read bypass; synchronous write API context
  variants, large-library behavior, Writer-busy behavior, external URL/model
  failures, and release-host evidence remain open.

- Knowledge package regression after the Durable worker-read bypass fixes:
  `go test ./internal/knowledge -count=1 -timeout=20m` passed in 117.385s and
  `go test -race ./internal/knowledge -p 1 -count=1 -timeout=30m` passed in
  513.190s. No race or timeout was reported. This confirms the package-level
  local behavior after the production-path changes; SmartCare scale, Writer
  contention, persistent-volume faults, and release-host replay remain open.

- Synchronous write-entry context follow-up: Web base create/update, directory
  creation, source repoint, and document rename now pass request context;
  Knowledge base/document updates and directory creation propagate the context
  into the Writer while retaining background compatibility wrappers. The
  cancellation test accepts `storage.ErrWriteUnknown` when a queued commit's
  final outcome cannot be observed, matching the submission-unknown contract.
  The focused Knowledge normal/race regression passed in 0.454s and 1.787s;
  remaining group/scope/config write callers and release-host unknown-submit
  evidence remain open.

- Historical GC drill stabilization: the synthetic 600-document/24,000-chunk
  fixture previously aged all retired chunks with one UPDATE; under Windows
  `-race` that exceeded the bounded 30-second data Writer deadline and produced
  `ErrWriteUnknown` before the GC assertions. The fixture now uses rowid keyset
  batches of 500, preserving the pinned-reader and reclamation checks without
  changing production timeouts. The isolated drill passed normal (10.366s) and
  race (220.774s); SmartCare-scale GC and persistent-volume evidence remain
  open.

- Scope/config/historical context follow-up: explicit `kv` scope existence now
  returns context errors instead of silently selecting the default all-bases
  scope; Stats provider/vector-dimension reads, delete-recovery tombstone
  reads, and the first document read in `GetDocumentContext` retain the caller
  context. Atomic config persistence now has `SaveContext`, and the Web config
  update uses `UpdateConfigWithContext`; compatibility wrappers remain for
  synchronous callers. Cancellation tests cover scope, filtered search,
  historical context, and config save.

- Latest full local normal regression after the context seam changes passed:
  `go test ./... -count=1 -timeout=30m`, with App 323.442s, Extension
  200.517s, Knowledge 195.808s, Models 5.495s, Operations 16.073s, Runtime
  63.526s, Storage 13.929s, Web 648.492s, and Scripts 2.747s; all command
  packages also passed. Latest serial race passed with
  `go test -race ./... -p 1 -count=1 -timeout=30m`: App 337.014s, Extension
  200.496s, Knowledge 770.745s, Models 5.050s, Operations 51.830s, Runtime
  53.421s, Storage 146.414s, Web 1498.036s, and Scripts 3.720s. These local
  results do not replace SmartCare-scale, release-host, Agent Host,
  cross-platform, or rollback evidence.

- Agent integration removal gate initially exposed a real cold-start deadline
  mismatch: the first managed-runtime npm installation exceeded the 10-second
  extension startup budget, while the same Knowledge process later passed the
  Web-only health check. `extension.yaml`, the default Agent integration
  launcher, and the removal gate now use a 120-second startup budget (with a
  3-second health probe in the gate). The gate also replaced its stale count of
  14 tools with an exact 18-name Knowledge tool contract. A fresh local Agent
  binary run then passed with installed state 18 tools/1 route/healthy and
  removed state 0 tools/0 route/healthy. This closes the local C13/P5 removal
  check only; production Agent Host continuity, disconnect/restart, and
  cross-platform evidence remain open.

- Native release-host lifecycle stage: `cmd/release-acceptance` now supports a
  `host` profile that builds/identifies a candidate and runs it as a real
  `serve` process in an isolated data home with managed runtime disabled. It
  probes `/healthz`, starts a duplicate instance and requires the SQLite
  instance-lock rejection, force-terminates the owner, then starts the same
  data home again to verify lock release/restart. Windows local execution
  passed via `go run ./cmd/release-acceptance -profile host -allow-dirty`; the
  native stage took 573.8741ms and recorded duplicate stderr plus per-process
  logs under `.tmp/release-acceptance-host/20260915-125245/native-host`.
  This is local host evidence only; signal-specific, descendant-process,
  Linux/macOS, persistent-volume, and production Agent Host drills remain
  open.

- C14 package-contract drift follow-up: the candidate reports storage format 2
  with reader/writer 8, while the formal package script had retained old
  reader/writer 6 literals. `scripts/package_release.ps1` now extracts the
  three current contract constants from `internal/storage/migrate.go` and
  reuses them for binary validation and `BUILD-METADATA.json`. A syntax and
  preflight run reached the intended clean-candidate rejection on this dirty
  work tree, proving the updated script parses; a formal package still needs a
  clean candidate checkout and independent release audit.

- Current Linux delivery smoke: a `CGO_ENABLED=0` linux/amd64 binary built
  from the current source reported product `0.2.0-rc.1+storage.v2`, format 2,
  and reader/writer 8. In WSL2, an isolated data home passed `/healthz`, a
  duplicate `serve` process was rejected by the instance lock, and the owner
  exited cleanly after SIGTERM. This is explicitly WSL2 supplemental evidence;
  native Linux/macOS release-host and cross-platform process-tree acceptance
  remain open.

- Local P3 fault drills after the current source changes passed: Windows
  `TestCorpusStorageReconcileFileLockStopsAndConverges` completed in 46.919s
  using a real no-share handle over the 600-document/1,200-orphan corpus;
  `Test(IsolatedBackupRestoreAndIncompatibleReaderDrill|SyntheticLargeDatabaseBackupRollbackDrill)`
  completed in 11.713s; and
  `TestSQLiteDiskFullAtGenerationCommitPreservesActiveAndRecovers` completed
  in 6.680s. The tests prove local fail-closed/recovery behavior, but do not
  close SmartCare-scale, persistent-volume, or old-compatible-binary
  release-host gates.

- P1A writer-boundary hardening: `storage.DB.ExecPriority`, `Write`, and
  `WriteTx` now fail closed with `ErrWriterStopped` when no Writer is started;
  they no longer fall back to the embedded raw SQLite handle. The new
  `TestDBDoesNotBypassWriterWhenWriterIsUnavailable` proves that mutation and
  transaction callbacks are not executed without the Writer. The storage and
  scripts packages passed normal tests (17.257s and 1.632s) and race tests
  (285.513s and 4.131s), plus `go vet` and `git diff --check`. This closes the
  local fallback-bypass seam only; migration bootstrap, test-only direct
  transactions, full production call-graph review, Writer-busy pressure, and
  release-host recovery remain open.

- P1B upload-lease ordering fix: a non-retryable terminal operation could be
  observed as `failed` before its best-effort post-finish cleanup removed the
  staging file. `releaseUpload` and the startup terminal-lease sweep now remove
  staging bytes before publishing `released`; a crash between those steps is
  recovered by the still-bound terminal lease. The regression
  `TestUploadReleasesAfterNonRetryableFailure` passed 5/5 in normal and 5/5 in
  race builds. Operations and App package normal/race suites also passed
  serially (13.089s/89.613s and 179.679s/686.139s); this closes the local
  terminal-observation race only, not release-host crash/replay evidence.

- P2 resource-bound test stability: the normal slow-model/IO stress test keeps
  the three-second production drain budget and passed 3/3 with submit p95
  5.5-5.9ms, reader p95 26.9-28.3ms, and drain 0.489-0.514s. Race builds use a
  separate 10-second diagnostic budget for detector overhead and passed 3/3
  with 3.76-4.88s drain; the production normal threshold was not relaxed.
  This is local two-lane evidence only and does not close SmartCare persistent
  volume pressure or effective-throughput gates.

- Deterministic local regression follow-up: the first post-fix full normal run
  exposed that App/Extension/Knowledge/Web tests could enter
  `PrepareManagedRuntime` and wait on an external Node 22.14.0 archive when the
  host only had Node 24.19.0. Test entry points now set
  `SHUTU_KNOWLEDGE_DISABLE_MANAGED_RUNTIME=1`; production defaults and the
  dedicated managed-runtime tests are unchanged. Extension and Web package
  suites then passed in 1.310s and 55.138s, and the full repository normal
  suite passed (including Knowledge 145.388s, Operations 10.106s, Runtime
  13.503s, Storage 9.367s and Web 53.513s).

- Final local race regression after the Writer/lease/test-isolation changes:
  `go test -race ./... -p 1 -count=1 -timeout=30m` passed every package with
  no race report or timeout (Knowledge 518.485s, Operations 30.919s, Storage
  106.902s, Web 160.074s, App 15.627s, Scripts 2.951s). `go vet ./...` and
  `git diff --check` also passed; the latter emitted only the repository's
  existing LF/CRLF conversion warnings. These are local gates only and do not
  close SmartCare, production Agent Host, native Linux/macOS, persistent-volume
  or old-binary rollback gates.

- Native host acceptance after the current changes passed with candidate
  `f06066f296b74bca0ae8647b1c3219ad4336cdff`: Windows amd64, product
  `0.2.0-rc.1+storage.v2`, format 2, reader/writer 8; the isolated host
  verified health, duplicate-instance rejection, crash-restart on the same
  data home, and extension manifest consistency. Result:
  `.tmp/release-acceptance-host-current/20260915-165010/result.json`. This is
  dirty-worktree local evidence and does not replace clean-package,
  production-Agent-Host, Linux/macOS signal, persistent-volume, or rollback
  acceptance.

- Release-acceptance clean-candidate guard: `cmd/release-acceptance` now checks
  `git status --porcelain --untracked-files=all`, so untracked source and
  configuration files cannot be silently omitted from the clean-worktree gate.
  `TestGitStateIncludesUntrackedFiles` independently verifies this behavior in
  a temporary Git repository.
  Running the host profile without `-allow-dirty` on this tracked-plus-untracked
  worktree was rejected before candidate build; the diagnostic `-allow-dirty`
  run above remains the only local host result. A formal package still requires
  a clean candidate checkout.

- Release-acceptance profile separation: the native lifecycle stage is now
  limited to `host` and `all`; `core`, `agent`, and `web` profiles run only
  their declared package groups. The new profile-routing unit test passed, and
  a post-fix `host -allow-dirty` run passed with exactly the
  `native-host-lifecycle` and `extension-manifest` stages. Result:
  `.tmp/release-acceptance-host-profile-fix/20260915-165827/result.json`.

- Owned process-tree cleanup: native acceptance now configures a Unix process
  group and uses platform-specific tree termination (Windows `taskkill /T`,
  Unix process-group kill with direct-process fallback) instead of relying only
  on `Process.Kill`. The Windows host profile passed again after this change;
  result: `.tmp/release-acceptance-host-process-tree-2/20260915-170905/result.json`.
  Linux amd64 and macOS arm64 cross-compilation of the acceptance command also
  passed. Cross-compilation remains supplemental and is not native release-host
  evidence.

- Current-source regression after the acceptance and package-audit changes:
  `go test ./... -count=1 -timeout=30m` passed all packages, including
  Knowledge 136.023s, Operations 15.852s, Runtime 18.696s, Storage 7.651s,
  Web 49.790s, and the acceptance command 2.846s. `go vet ./...` and
  `git diff --check` also passed; this remains local evidence only.

- P0 baseline fingerprint correction: two runs of
  `cmd/architecture-baseline` over the same local database/config/dataset now
  produce the identical fingerprint
  `b7a5ca209cf7bdc28c95e85401327a33ae1d6d4e7dc8554fdcaa5243489cc657`.
  The fingerprint projection excludes collection time, host capacity and
  absolute paths while retaining input file/database/config digests. The
  local fixture is only tooling evidence; SmartCare baseline inputs remain
  required for P0-G0.

- The corrected baseline command passed normal and race package tests plus
  `go vet ./...`; the repeated local fixture runs produced the same fingerprint
  in both reports. This closes the tooling repeatability defect, not the
  SmartCare data and scale gates.

- Scenario-runner status aggregation now has an explicit regression test:
  `TestFinishMarksUnrunScenarioIncomplete` passed in both normal and race
  modes, and `go vet ./cmd/architecture-scenarios` passed. This confirms that
  API-runner scenarios which cannot execute OS-level restart, file-lock, or
  disk-failure controls are reported as `incomplete`, not falsely as `passed`.

- Scenario workload validity is now enforced: expected `429`/`408`/`504`
  responses are retained as outcomes and do not abort the first overload, but a
  continuous scenario fails when it has no successful work request. Search
  requests also reject HTTP-200 empty or unversioned hits. The mixed
  rejection/success, all-rejected, and empty-search regressions passed in normal
  and race modes. This strengthens the local P0 runner gate; SmartCare fixed
  retrieval cases and scale pressure remain required.

- The scenario runner now gives each `continuous-search` and `page-switch`
  scenario its own duration context. Previously, running both in one command
  let the first continuous workload consume the shared deadline and falsely
  reported the following scenario as having no successful work. The independent
  deadline regression passed in normal mode, and an isolated synthetic server
  run of `status,single-reindex,continuous-search,page-switch` passed with
  21,458 requests, one operation, and no errors. Report fingerprint:
  `07dedc924fb30a9f90a0861b046d0be450576078174f44e8d55e5d7e3c901dc9`.
  This is local synthetic evidence only; SmartCare two-base inputs and scale
  pressure remain open.

- The P5 raw-preview path now uses the same prefixed and cancellable request
  boundary as other GETs. `rawText` no longer calls an unprefixed bare
  `fetch`, and the shared JSON request helper now actually wires the current
  route signal into its internal timeout controller instead of overwriting it.
  The Node API regression covers the Agent proxy path and route cancellation;
  `npm run build`, `npm test`, and `npm run typecheck` passed. Production Agent
  Host rapid navigation, disconnect, and stale-preview evidence remain open.

- P6 shutdown now passes the App's shared shutdown deadline into
  `storage.DB.CloseWithContext` instead of starting an independent database
  close budget after Operations and Runtime teardown. A cancelled shutdown
  regression confirms the storage close returns promptly with the context
  error; `internal/storage` and `internal/app` normal/race focused suites
  passed. This tightens local bounded-exit behavior but does not replace
  stuck-helper, process-tree, or Linux/macOS release-host evidence.

- Parser cancellation seam: `Registry.ParseContext` now carries the Worker
  context into the optional legacy helper, document/URL ingestion uses the
  context-aware entry point, and MinerU polling waits on a cancellable timer.
  `TestRegistryParseContextPropagatesCancellationToLegacyHelper`,
  `TestParseFileContentHonorsCanceledContextBeforeParser`, and
  `TestMineruPollWaitHonorsCancellation` passed, as did
  `go vet ./internal/parser`. This closes the local parser wait seam only;
  remote-service interruption and release-host replay evidence remain gates.

- URL parse-failure persistence now uses `failDocumentContext` instead of the
  background compatibility wrapper, so a cancelled URL worker cannot silently
  continue its failure write outside the request/worker context. The URL import
  and parser-cancellation regressions passed in normal and race modes; Agent
  Host disconnect and remote URL interruption/unknown-commit drills remain.

- After the URL failure-path change, the full Knowledge package passed again in
  normal mode in 114.779s and serial race mode in 478.959s. This confirms the
  current URL/parser context seam across the package; it does not replace
  Agent Host disconnect or remote URL interruption/unknown-commit drills.

- The parser cancellation changes were followed by a fresh full normal
  regression (`go test ./... -count=1 -timeout=30m`) with every package passing
  (Knowledge 130.045s, Web 48.249s), and a serial full race regression
  (`go test -race ./... -p 1 -count=1 -timeout=30m`) with every package passing
  and no race report or timeout (Knowledge 481.666s, Operations 29.562s,
  Storage 98.702s, Web 90.611s). These are local gates only; SmartCare,
  release-host, Agent Host, cross-platform, and rollback gates remain open.

- Formal packaging clean-candidate guard: `scripts/package_release.ps1` uses the
  same tracked-plus-untracked status check. Running it against the current
  worktree was rejected at the clean-checkout gate before build, staging, or ZIP
  creation. This closes only the local dirty-candidate detection gap; it is not
  a package, secret-audit, deployment-smoke, or publication result.

- Formal package static secret audit: `scripts/audit_release_package.ps1` now
  scans release text files for private-key/high-confidence token patterns and
  non-empty structured credential fields, and `package_release.ps1` invokes it
  after archive extraction. The existing historical formal package passed with
  10 text files scanned; the current dirty worktree still cannot produce a
  formal package or deployment-smoke result.

- Legacy shutdown follow-up: `internal/jobs.Manager` now exposes bounded
  `StopWithContext`; the compatibility `Stop` wrapper uses a 15-second budget,
  queue closure is protected by `sync.Once`, and a non-cooperative task cannot
  keep the caller waiting indefinitely. `TestStopWithContextBoundsNonCooperativeLegacyTask`
  passed in normal mode (1.609s) and serial race mode (5.222s). The production
  App does not construct this legacy manager; this closes only its local
  lifecycle seam, not real helper process-tree or cross-platform release-host
  evidence.

- Final local validation after the legacy shutdown change: `go test ./... -count=1
  -timeout=30m` passed all packages, including Knowledge (132.287s) and Web
  (44.978s). Serial `go test -race ./... -p 1 -count=1 -timeout=30m` also
  passed all packages, including Knowledge (484.607s), Storage (96.433s), and
  Web (93.120s). `go vet ./...`, the Writer static guard, `npm run build`,
  `npm test`, and `npm run typecheck` passed as well. These close the current
  local code gates only; SmartCare, Agent Host, release-host, cross-platform,
  and rollback evidence remain open.

- Instance ownership shutdown follow-up: `InstanceLock.ReleaseWithContext` now
  receives the App shutdown context, closes the SQLite connection even when
  rollback observes cancellation, and keeps a bounded 5-second compatibility
  `Release` wrapper. The canceled-release regression passed in the focused
  storage/app normal run; a second owner acquired the same lock immediately
  after release. This is local lifecycle evidence only and does not replace
  native Linux/macOS signal, process-tree, or crash-restart release-host runs.

- Final local regression after instance-lock context propagation: full normal
  `go test ./... -count=1 -timeout=30m` passed all packages, including Knowledge
  (125.560s) and Web (47.691s). Serial full race
  `go test -race ./... -p 1 -count=1 -timeout=30m` also passed all packages,
  including Knowledge (487.793s), Storage (96.607s), and Web (90.606s).
  `go vet ./...`, the Writer static guard, and `git diff --check` passed after
  the run. These remain local gates only; external scale, host, cross-platform,
  and rollback gates are still open.

- Windows native host acceptance after instance-lock context propagation passed
  with `go run ./cmd/release-acceptance -profile host -allow-dirty`. The result
  is `.tmp/release-acceptance/20260915-191535/result.json`, using candidate
  commit `f06066f296b74bca0ae8647b1c3219ad4336cdff`; it passed healthz,
  duplicate-instance rejection, same-directory crash restart, and extension
  manifest checks. The workspace was dirty and managed runtime was disabled,
  so this is local host evidence only, not Linux/macOS, persistent-volume,
  production Agent Host, or old-binary rollback evidence.

- A CI execution path for P6-04 is now defined separately from the ordinary
  Ubuntu build: tag/manual runs execute `release-acceptance -profile host` on
  `windows-latest`, `ubuntu-latest`, and `macos-latest`, and upload each
  isolated host result. This makes native lifecycle evidence reproducible, but
  the matrix has not been run in this workspace; it is not yet evidence that
  the three release-host gates passed.

- Startup-maintenance cancellation follow-up: the background maintenance
  watcher now reads operation state through `GetContext(maintenanceCtx, ...)`
  instead of the unbounded compatibility `Get` wrapper. This keeps shutdown
  cancellation on the same context chain as operation admission and execution;
  focused App normal validation passed in 5.426s and serial race validation
  passed in 15.971s. A real blocked-maintenance and persistent-volume shutdown
  drill remains open.

- Durable worker/control context follow-up: post-claim operation and command
  payload reads, cancellation-intent reads, terminal `finish` writes, and
  upload-lease release now use the worker context plus a bounded independent
  finalization context. `Submit` returns through `GetContext`; the HTTP
  idempotency preflight and Scheduler status snapshot now accept request
  context. Operations normal/race passed in 7.034s/30.095s and Web normal/race
  passed in 45.711s/91.587s. The pre-dispatch cancellation regression remains
  green, including the executor-visible canceled context. Release-host Writer
  contention, disconnect, response-unknown, and post-commit replay drills
  remain open.

- Current full regression after the worker/control context fixes: normal
  `go test ./... -count=1 -timeout=30m` passed all packages (Knowledge 124.221s,
  Operations 12.185s, Web 46.301s). Serial race
  `go test -race ./... -p 1 -count=1 -timeout=30m` also passed all packages
  (Knowledge 488.511s, Operations 32.276s, Storage 97.286s, Web 91.645s).
  This refreshes the repository-local gate only; external host, scale, and
  rollback evidence remains required.

## Remaining Gates
- C13 import empty-state follow-up: when no knowledge base was selected, the
  Import route returned only the "select a knowledge base" message and omitted
  the shared base picker, leaving no way to choose a base from that page. The
  empty state now renders the base picker; when no bases exist it also offers a
  navigation action to the base-creation page. Chrome/CDP E2E now enters Import
  with a clean browser profile before creating a base and asserts that both the
  selector and guidance are visible. Web contract and the full browser
  lifecycle suite passed.

The architecture plan is not complete until the following are evidenced:

1. SmartCare product-dictionary and suite-scale copies are required for the
   two-base pressure matrix, API p95, writer latency, RSS/disk peaks, effective
   throughput, rejection rates, and recovery convergence.
2. Fault drills must cover release-host persistent-volume OS-level and
   corpus-scale disk exhaustion, release-host cross-platform corpus-scale
   file-lock behavior, long readers, and model switching at corpus scale.
3. SmartCare-scale isolated backup/rollback drills must record product
   version/SHA, format bounds, backup hashes, timing, post-backup deltas, and
   retrieval/delete validation; an old-compatible binary must replay the
   release-host rollback matrix.
4. Search recall, rerank, context, raw citation, and diagnostics now share
   generation/source audit fields and immutable generation-pinned source bytes.
   Remaining evidence must cover SmartCare-scale model changes and GC
   decisions with long-lived readers.
5. The plan's C01-C16 matrix needs executable, repeatable fault-injection
   evidence rather than inference from success-path tests.
6. Cross-platform process-tree and instance-lock runs still need Linux and
   macOS release-host evidence; current Linux coverage is local WSL2 and
   current macOS coverage is only a darwin/arm64 compile check.

- P1B/P3 metadata cancellation follow-up: `SchemaVersionContext` and
  `StorageFormatContext` now carry the health/status caller context through
  `QueryRowContext`; `TestStorageMetadataContextHonorsCancellation` passed in
  normal and race modes. Targeted Storage/App/Web normal runs passed in
  7.765s/6.798s/43.297s, and serial race runs passed in
  98.443s/16.850s/94.169s.
- Latest full local gate after the metadata-context change:
  `go test ./... -count=1 -timeout=30m` exited 0 (Knowledge 128.985s,
  Operations 9.457s, Storage 8.210s, Web 48.528s); serial
  `go test -race ./... -p 1 -count=1 -timeout=30m` exited 0 (Knowledge
  490.966s, Operations 30.119s, Storage 97.988s, Web 97.430s). `go vet
  ./...`, the Writer/handler guards, `npm run build`, `npm test`, and
  `npm run typecheck` also passed. These are local gates only; SmartCare,
  production Agent Host, persistent-volume faults, native Linux/macOS
  release-host, and old-binary rollback remain open.
- P6-01 readiness correction: deferred startup recovery now reports
  `status=starting` with `ready=false` until the recovery goroutine completes;
  `TestHealthSnapshotDoesNotReportReadyDuringDeferredRecovery` covers the
  contract. The normal ready response after recovery remains covered by the
  existing App/Extension/host tests; production Agent Host timing and route
  visibility during recovery remain external evidence.
- P6-01 local acceptance after the readiness correction: App/Web/Extension
  normal suites passed in 6.694s/44.652s/2.761s and serial race suites passed
  in 16.060s/94.340s/6.728s. The Windows host profile passed at
  `.tmp/release-acceptance/20260915-202626/result.json`, covering healthz,
  duplicate-instance rejection, same-directory crash restart, and the
  extension manifest. The local Agent install/remove gate also passed with
  18/0 Knowledge tools, 1/0 routes, and healthy removal state.
- Latest full regression after the readiness correction:
  `go test ./... -count=1 -timeout=30m` exited 0 (Knowledge 127.078s,
  Operations 12.179s, Storage 9.129s, Web 50.359s); serial
  `go test -race ./... -p 1 -count=1 -timeout=30m` exited 0 (Knowledge
  493.687s, Operations 30.663s, Storage 98.541s, Web 96.735s). This closes
  only the local regression gate; SmartCare, production Agent Host, native
  Linux/macOS release-host, persistent-volume faults, and old-binary rollback
  remain open.
- Worker-read audit found and closed one more bypass: the old-generation embedded
  chunk count used by the safe reindex fallback now uses `QueryRowContext`, and
  cancellation or a read failure stops the operation instead of being treated as
  zero published vectors. The new `TestEmbeddedChunkCountHonorsCanceledContext`
  covers the seam; a fresh full normal/race regression is required before this
  change can update the local baseline.
- The remaining Knowledge content-hash helper was also moved to a
  `QueryRowContext` implementation with a compatibility wrapper; the new
  `TestContentHashLookupHonorsCanceledContext` verifies cancellation. The helper
  had no current production caller, but the seam is now fail-closed if reused.
- Package-level validation after the content-hash seam change passed: normal
  `go test ./internal/knowledge -count=1 -timeout=30m` completed in 121.326s and
  serial race `go test -race ./internal/knowledge -p 1 -count=1 -timeout=30m`
  completed in 489.843s. `go vet ./...`, the Writer/Operation handler guards,
  and `git diff --check` also passed. Because this helper has no current
  production caller, this is package-level follow-up evidence rather than a
  new full-repository baseline.
- Remote CI was checked on 2026-09-16: the latest public `master` run,
  [run 34157958853](https://github.com/shutu-ai/Shutu-Knowledge/actions/runs/34157958853),
  succeeded for SHA `180d8a9238c9259ba62d14c9c55d3b77d787aecc`; the current
  worktree HEAD is `f06066f296b74bca0ae8647b1c3219ad4336cdff` and contains
  uncommitted changes, so that CI result is historical and does not validate
  this candidate.
- The formal package entry was run against the current worktree and correctly
  failed before producing an artifact because `scripts/package_release.ps1`
  requires a clean candidate checkout. This is an expected release-gate stop,
  not a package audit result; packaging must be rerun after the candidate is
  committed or exported to a clean checkout.
- Legacy-job seam closure: Knowledge Service no longer stores a legacy
  `jobs.Manager`, and the real-work async compatibility methods
  `ImportDirectoryTree`, `RescanDirectory`, and `ReindexBase` were removed.
  Directory and reindex tests now call `RunDirectoryImport`,
  `RunDirectoryRescan`, and the bounded `ForEachReindexDocumentBatch` entry
  points used by Durable workers. Web/Agent `jobId` compatibility remains a
  Durable Operation mapping rather than a second work queue.
- Validation after the legacy-job seam closure passed: full normal
  `go test ./... -count=1 -timeout=30m` (Knowledge 127.810s, Web 104.534s),
  serial race `go test -race ./... -p 1 -count=1 -timeout=30m` (Knowledge
  491.255s, Storage 99.713s, Web 102.796s), `go vet ./...`, Writer/Operation
  guards, `npm test`, `npm run typecheck`, and `npm run build`.
- The CI workflow now includes a tag/manual-only Windows `release-package`
  job. It runs the formal package script, which enforces a clean checkout,
  embeds version/storage identity, verifies the archive, and invokes the static
  secret audit before uploading the package artifact. The workflow YAML parses
  locally, but the job still needs a remote candidate run; no current artifact
  is claimed from the dirty worktree.
- The first full serial race after that worker-read change exposed a test-harness
  timing defect in `TestRunContinuousAllowsExpectedRejectionsWithSuccessfulWork`:
  its 20ms context could expire after the expected 429 under `-race`, leaving no
  successful sample. The production thresholds were not changed; the test-only
  observation window is now 250ms. The test passed three consecutive normal and
  race runs, and the corrected full serial race exited 0: Knowledge 498.574s,
  Operations 30.760s, Storage 97.036s, Web 92.349s, Scripts 3.347s. The fresh
  full normal also exited 0 (Knowledge 127.873s, Operations 11.716s, Storage
  8.541s, Web 47.478s). This closes the local regression gate for the current
  source; external SmartCare, Agent Host, release-host, persistent-volume, and
  rollback gates remain open.
- Deletion replay semantics were audited against the lifecycle fence boundary.
  A committed `operation_items` marker for `delete_document`, `delete_directory`,
  or `delete_base` proves only that the logical `deleting` fence committed; it
  does not prove that raw files, generations, and chunks were physically
  removed. The resolved branches now resume the bounded cleanup, and directory
  import/rescan/delete failures now return marker-write errors instead of
  discarding them. The regression
  `TestDurableDeleteReplayResumesCommittedPhysicalCleanup` creates a committed
  logical fence with a deliberately blocked raw path, verifies the first
  operation fails with its item still committed, then removes the blocker and
  verifies retry converges and removes the raw path.
- Validation for this deletion-fence correction passed:
  `go test ./internal/app -run TestDurableDeleteReplayResumesCommittedPhysicalCleanup -count=1 -timeout=30m`
  and the existing committed-cancel cleanup regression both passed. The full
  App package normal/race runs passed, `go vet ./...` passed, and the refreshed
  full normal `go test ./... -count=1 -timeout=30m` passed (Knowledge 128.932s,
  Web 49.006s). The prior full serial race baseline remains valid for the
  unchanged packages, while the App race package was rerun after this change;
  a new full serial race is still recommended before release. SmartCare scale,
  release-host, persistent-volume, and old-binary rollback gates remain open.
- A clean temporary candidate checkout was assembled from the current worktree
  without changing this repository. Formal Windows packaging completed with
  static secret audit passed (11 text files scanned), archive SHA-256
  `121b66782648de2096aac53e438dd14268a0bf00b724845ba6c24e3eddbec581`, and
  candidate commit `78181f2bc0723ceffa3d95d67e166c66b3b3226f`. The extracted
  package passed the `release-acceptance` identity profile, and the same clean
  candidate passed the Windows native host profile (health, duplicate instance
  lock, termination/restart, and manifest checks). No Knowledge process was
  left running. This is local Windows evidence only; remote candidate CI,
  deployment smoke, production Agent Host, native Linux/macOS, persistent
  volume, and old-binary rollback gates remain open.
- After the deletion replay correction, the refreshed serial full-repository
  race run `go test -race ./... -p 1 -count=1 -timeout=30m` exited 0: Knowledge
  500.442s, Operations 30.315s, Runtime 20.550s, Storage 98.310s, Web
  95.051s, and Scripts 3.196s; no race report or timeout occurred. Web
  `npm test`, `npm run typecheck`, and `npm run build` also passed. This closes
  the current local normal/race and Web validation gate only.
- Aggregate-operation error propagation was tightened after a local side-path
  audit: directory identity lookup and file conflict lookup now propagate
  database/cancellation errors instead of treating them as "no conflict";
  reranker cleanup errors during model removal are also returned. The change
  keeps `ErrNotFound` as the only non-error absence case and prevents a worker
  from reporting a successful durable result after a hidden control write or
  lookup failure. Validation passed with the full App test package and the
  Writer/Operation static guards; the remaining real fault-injection and
  release-host evidence is still required for P1b-G2.
- Directory synchronization failure closure was tightened as well: progress
  writes, failed-child records, failed-container state, and the final ready
  state now return Storage Writer errors to the worker. A failed lookup or
  status write can no longer be silently followed by a durable success with a
  stale `processing`/missing child state. Directory-focused Knowledge tests,
  App normal/race tests, and the Writer/Operation guards passed after this
  change.
- Current-source local validation after the side-path closure passed: full
  normal `go test ./... -count=1 -timeout=30m` (Knowledge 133.606s, Web
  53.818s), `go vet ./...`, and serial full race
  `go test -race ./... -p 1 -count=1 -timeout=30m` (Knowledge 502.051s,
  Operations 29.364s, Runtime 20.708s, Storage 100.265s, Web 95.087s,
  Scripts 3.274s). No race report or timeout occurred. These results close
  the current local Go normal/race gate only; SmartCare scale, production
  Agent Host, persistent-volume, native Linux/macOS, and old-binary rollback
  gates remain open.
- The side-path correction was then rebuilt in a new clean temporary
  candidate checkout. Formal Windows packaging and the static secret audit
  passed (11 text files scanned); package SHA-256 was
  `1c363e255743cfd20d5a0c27ebd9d609a1906b89798029530b22295c58de3d68`,
  candidate commit `53e9ae8343163fb0232ea0a164dccc37331c6946`. The extracted
  package passed the identity profile, and the clean candidate source passed
  the Windows host profile. No Knowledge process remained; the temporary
  candidate was removed after verification. This remains local Windows
  evidence and does not close remote CI, deployment smoke, Agent Host,
  native Linux/macOS, persistent-volume, or old-binary rollback gates.
- After the final directory-sync change, the current workspace was rechecked
  with full normal `go test ./... -count=1 -timeout=30m` (Knowledge 128.122s,
  Web 52.913s, Operations 11.070s, Runtime 16.972s, Storage 10.266s) and
  `go vet ./...`; both passed. The preceding serial full race was run against
  the same source after the change and passed as recorded above. Web sources
  were unchanged by this side-path correction; their existing contract,
  typecheck, and build evidence remains valid.
- Added `TestDirectorySyncStateWritesPropagateCancellation` to keep directory
  progress, failure-state, and failed-child persistence fail-closed. The test
  passed three normal runs and three serial race runs together with the nested
  directory import regression. The accepted cancellation outcomes are direct
  context cancellation or `storage.ErrWriteUnknown`, matching the Writer's
  admitted-but-cancelled reconciliation contract.
- The directory failure-record path now carries the resolved nested parent ID
  instead of always attaching failed children to the import root. The nested
  hierarchy assertion was included in the three-run normal/race regression.
- Final current-source validation after the nested-parent correction passed:
  full normal `go test ./... -count=1 -timeout=30m` (Knowledge 128.066s, Web
  50.485s, Operations 15.496s, Runtime 17.771s, Storage 10.296s) and serial
  full race `go test -race ./... -p 1 -count=1 -timeout=30m` (Knowledge
  501.541s, Operations 29.497s, Runtime 21.223s, Storage 99.380s, Web
  94.019s, Scripts 3.267s). No race report or timeout occurred.
- A fresh clean candidate including the nested-parent correction was packaged
  and accepted on Windows. Static secret audit passed (11 text files scanned);
  package SHA-256 was
  `4ee826a63cc641db2ac706baa365252fc6f453d2c8aedcf140662a158c1fff79`,
  candidate commit `0f70638b2c0063b98e98c8e22b43ad815f89a029`. The extracted
  package passed the identity profile and the clean candidate passed the
  Windows host profile. The temporary candidate was removed after confirming
  no Knowledge process remained. This is local Windows evidence only and does
  not close remote CI, deployment smoke, Agent Host, native Linux/macOS,
  persistent-volume, or old-binary rollback gates.
- Upload lease cleanup was tightened after the terminal-path audit: expired
  `uploading`/`complete` sessions and terminal `bound` sessions now remove the
  staging file before publishing `expired`/`released`. Path-resolution and
  removal errors are returned while the original durable state remains
  retryable; a process dying between removal and the state update is safe to
  replay because missing files are treated as already cleaned. Worker terminal
  persistence also receives one bounded idempotent retry after a writer/lock
  failure, so a live process does not abandon a completed business effect in
  `running` after the first finalization response is lost.
- `TestUploadCleanupFailurePreservesRetryableLeaseState` passed in normal and
  serial race mode, together with the batched upload cleanup regression:
  `go test ./internal/operations -run
  'Test(UploadMaintenanceCleanupProcessesRowsInBatches|UploadCleanupFailurePreservesRetryableLeaseState)$'
  -count=1 -timeout=30m` and the equivalent `go test -race ... -p 1` both
  exited 0. The result closes the local cleanup-order and retryable-state
  regression only; release-host crash replay, response-unknown reconciliation,
  and real caller evidence remain open.
- Current-source validation after the upload terminal cleanup change completed:
  the full normal `go test ./... -count=1 -timeout=30m`, serial race
  `go test -race ./... -p 1 -count=1 -timeout=30m`, `go vet ./...`, and
  `git diff --check` completed without a reported failure. The local normal/race
  result closes only the current source regression gate; it does not close
  SmartCare scale, production Agent Host, persistent-volume, native
  Linux/macOS, or old-binary rollback gates.
- A new clean Windows candidate was built after the upload terminal cleanup
  change. Formal packaging and the static secret audit passed (11 text files);
  ZIP SHA-256 was
  `aff1fd22d925c5a06333ba35d590c944e4bc9b5a07f240326d2e8b560713548b`,
  candidate commit `2139d4c0c2ec4dda3bdbc234e5e09b7a84489f16`. The extracted
  archive passed the identity profile and the clean candidate source passed the
  Windows host profile. No Knowledge process remained after acceptance. This
  is local Windows evidence only and does not close remote CI, deployment
  smoke, Agent Host, native Linux/macOS, persistent-volume, or old-binary
  rollback gates.
- P6-01 startup readiness was tightened: a deferred `RecoverInterrupted`
  failure is now retained as lifecycle state instead of being logged and then
  exposed as ready after the recovery goroutine exits. Health snapshots report
  `ready=false`, `unhealthy: startup-recovery`, and a failed startup component;
  the in-progress path continues to report `starting`. The normal and serial
  race `TestHealthSnapshotDoesNotReportReady` regression passed. This closes
  the local readiness-state regression only; real recovery failure, restart,
  signal, and release-host shutdown evidence remain open.
- Current dirty-workspace Windows host acceptance was rerun with
  `GOCACHE/GOMODCACHE` isolated under the workspace after the default Go cache
  path was denied by the managed environment. The report
  `.tmp/release-acceptance/20260916-053041/result.json` is `passed` and covers
  healthz, duplicate-instance rejection, termination/restart, and the
  Extension manifest; no Knowledge process remained.
- A final clean candidate including the P6-01 readiness fix and lifecycle
  field concurrency fix was packaged and accepted on Windows. Formal packaging
  and the static secret audit passed (11 text files); ZIP SHA-256 was
  `bc4f653eb52f44cabbb945624725163b170591b0858c212a0c7e46f1d5d8b154`,
  candidate commit `b275fcfcec1fb0234f90fe45b43d82e6e8dde67e`. The extracted
  archive passed the identity profile and the clean candidate passed the
  Windows host profile. The temporary candidate was removed after confirming
  no Knowledge or Go process remained. This remains local Windows evidence;
  remote CI, deployment smoke, production Agent Host, native Linux/macOS,
  persistent-volume, and old-binary rollback gates remain open.
- The P6 lifecycle follow-up now protects deferred startup/maintenance done and
  cancel fields with one lifecycle mutex, uses local completion channels inside
  goroutines, and snapshots cancellation/wait handles before `Close` proceeds.
  App lifecycle normal and serial race regressions passed after the change;
  this is local concurrency evidence only and does not close real Agent Host
  start/stop interleavings or three-platform release-host evidence.
- The latest current-source full regression after the lifecycle mutex and
  startup-recovery readiness fixes passed: `go test ./... -count=1
  -timeout=30m` exited 0 (Knowledge 138.477s, Operations 12.645s, Runtime
  18.614s, Storage 9.837s, Web 56.325s); serial `go test -race ./... -p 1
  -count=1 -timeout=30m` also exited 0 (Knowledge 527.945s, Operations
  30.558s, Runtime 41.264s, Storage 103.965s, Web 99.430s), with every
  package passing and no race report or timeout. This updates only the local
  normal/race gate; SmartCare, real Agent Host, persistent-volume, native
  Linux/macOS, and old-binary rollback gates remain open.
- The same current-source snapshot also passed the static and Web gates with
  explicit exit codes: `go vet ./...`, the Writer/Operation handler guard
  test, `npm test`, `npm run typecheck`, and `npm run build` all exited 0; the
  Web build generated 6 files. These results update only local static and
  contract gates and do not close external scale, Agent Host, cross-platform
  release-host, persistent-volume, or rollback gates.
- The P1a/P2 read-isolation follow-up moved storage-format/schema probes in App
  health, Web `/api/status`, and doctor from the writer handle to the dedicated
  `ReadDB()`. Normal and serial race tests for `cmd/shutu-knowledge`,
  `internal/app`, and `internal/web` all exited 0. This closes the local
  read-handle selection gap only; Writer-busy control latency and real-scale
  evidence remain open.
- The P6-03 HTTP shutdown boundary now uses a 15-second deadline in
  `cmdServe` instead of an unbounded `context.Background()`. A deliberately
  uncooperative shutdowner regression passed in both normal and race modes.
  This closes the local HTTP shutdown-budget gap only; real stuck helpers,
  child-process trees, and three-platform release-host exit evidence remain
  open.

- Current-worktree local verification on 2026-09-17 passed `go test ./...
  -count=1 -timeout=30m`, `go vet ./...`, and the Writer guard
  (`go test ./scripts -run '^TestWriterGuard$' -count=1`). Web contract/API
  tests (`npm test`), JavaScript syntax/type checks (`npm run typecheck`),
  and the embedded Web build (`npm run build`, six files) also exited 0.
  This confirms the current local regression and static gates only; it does
  not close SmartCare scale, production Agent Host, persistent-volume,
  native Linux/macOS release-host, remote CI, deployment smoke, or old-binary
  rollback evidence.
- The current dirty worktree also passed `go run ./cmd/release-acceptance
  -profile host -allow-dirty -output .tmp\\release-acceptance-current` on
  2026-09-17. The result recorded candidate SHA `f06066f`, storage format 2,
  Windows amd64, and passed healthz, duplicate-instance rejection, and
  crash-restart checks plus the Extension manifest check; no Knowledge
  process remained afterward. This is current Windows-local evidence only;
  it does not close production Agent Host, Linux/macOS release-host,
  persistent-volume, remote CI, deployment smoke, or old-binary rollback
  gates.
- The broader current-worktree `go run ./cmd/release-acceptance -profile all
  -allow-dirty -output .tmp\\release-acceptance-all` run also passed on
  2026-09-17 in 3m24s. Storage, Operations, Runtime, App, Knowledge,
  Extension, Web, Windows host lifecycle, and Extension manifest stages all
  passed for candidate SHA `f06066f`. The report records `sourceDirty=true`;
  this is a Windows-local acceptance result and does not close production
  Agent Host, SmartCare-scale, persistent-volume, native Linux/macOS,
  remote-CI, deployment-smoke, or old-binary rollback gates.
- P1a writer-boundary hardening removed the public `storage.DB.Begin` and
  `BeginTx` escape hatches, which could have allowed future production code to
  bypass the single Writer. The remaining direct transaction handles are now
  explicit test-only fixture/lock injections. The full package compile,
  cross-process historical-reader and synthetic long-reader drills, busy
  Writer HTTP regression, Writer guard, `go vet ./...`, and `git diff --check`
  passed after the change.
- The post-hardening full normal regression `go test ./... -count=1
  -timeout=30m` also exited 0 on 2026-09-17, including Knowledge (120.167s),
  Runtime (15.494s), Storage (9.591s), and Web (53.522s). This is current
  Windows worktree evidence and does not replace the remaining release-host
  or SmartCare-scale gates.
- The post-hardening targeted serial race run for Storage, Knowledge, and Web
  (`go test -race ... -p 1`) also exited 0 on 2026-09-17. It covered the raw
  store guard, cross-process historical reader, 600-document long-reader GC
  drill, and busy-Writer HTTP regression; Knowledge completed in 172.711s
  with no race report or timeout.
- The Writer static guard now also rejects nested production access to the
  embedded raw database handle (for example `app.DB.DB`), while allowing the
  Storage implementation's own handle and test-only fault fixtures. The
  complete `scripts` test package and `git diff --check` passed after this
  guard hardening.
- A current-source `CGO_ENABLED=0` `linux/amd64` binary was built on
  2026-09-17 and exercised in Ubuntu-22.04 WSL2 with an isolated data home.
  The smoke passed `/healthz`, rejected a second instance in the same data
  home with `database is locked (SQLITE_BUSY)`, terminated the first owner,
  and started the same data home again successfully. The run used
  `.tmp/linux-current/shutu-knowledge` and the temporary runner
  `.tmp/wsl-linux-host-smoke.sh`. This is supplemental WSL2 evidence only;
  it does not close native Linux release-host, macOS, persistent-volume,
  production Agent Host, or cross-platform process-tree gates.
- A real SmartCare product-dictionary legacy `.xls` file
  (`HUAWEI SEQ Analyst KPI Definition (DNS) 01-zh.xls`) passed the current
  Knowledge-managed Anydoc Office runtime smoke on 2026-09-17 in offline
  mode. Conversion returned non-empty Markdown (7,592 bytes; 12 ms), proving
  `.xls` is a supported import format and is not the SmartCare baseline
  blocker. The smoke used the existing cached managed runtime and models; it
  does not create the required product-dictionary or Suite SQLite snapshots.

## Rollback Notes

- Do not run a pre-P1 binary against migrated data: generations, lifecycle
  fences, durable commands, uploads, URL captures, and child indexes are not
  understood by the old reader/writer.
- Scheduler lane sizes and queue limits can be tuned without changing command
  format. Maintenance-only scheduling can be paused, but committed logical
  deletes must not be reversed.
- Quarantine retention is configurable; zero retains files until explicit
  purge. Disabling automatic retention cleanup is safe, but does not undo a
  committed logical delete.
- Migration `0013` raises the minimum reader/writer contract to 6 and the
  storage format to 2 because pre-migration binaries do not understand
  generation-pinned raw paths. Use an isolated format-2-compatible binary or
  restore the matching backup; do not point a version-5 binary at this data.
- `instance.lock.db` is an ownership marker and can be removed only after every
  Knowledge process using that data home is stopped.
