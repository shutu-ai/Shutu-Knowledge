# Architecture Refactor Evidence

> Status: implementation in progress
>
> Baseline: `docs/architecture_refactor_plan.md` v1.1
>
> Updated: 2026-09-14

This record separates implemented behavior from gates that remain open. A
passing unit or browser test is not treated as SmartCare-scale acceptance.

## Implemented Evidence

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
- Legacy Web compatibility routes for text/base64-file/URL import, refresh,
  document/base batch deletion, directory import/rescan/delete, and re-index
  now submit durable operations while preserving `jobId` responses. They no
  longer execute parsing, model work, or physical cleanup inside the HTTP
  request.
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

- `/api/bases/{id}/documents/children` provides bounded parent-scoped paging
  and breadcrumbs; migration `0009_document_children.sql` adds the index.
- The documents page no longer loads an entire base to construct a tree.
- Lightweight statistics use a short TTL cache with invalidation on relevant
  mutations. Vector diagnostics remain live.
- Tests: `internal/web/documents_children_test.go` and
  `internal/knowledge/stats_cache_test.go`.

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
- Complete legacy-route migration follow-up: text, base64-file, URL, refresh,
  directory, single/bulk delete, and re-index compatibility routes submit
  durable operations. Their async HTTP contracts and proxied legacy clients are
  covered by targeted Web tests; all six initially stale compatibility tests
  were migrated to wait for durable terminal state and passed.
- Final full regression after the legacy-route migration: `go test ./...`
  passed (notably `extension` 91.322s and `web` 311.173s).
- `node web/scripts/contract-test.mjs`: passed.
- `node web/scripts/e2e.mjs`: passed; it built 6 web files and completed the
  Chrome/CDP lifecycle check after the route migration.
- `git diff --check`: no whitespace errors; existing CRLF warnings remain.
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
  cross-compiled environment and refuses a dirty tracked work tree unless
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

## Remaining Gates

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
