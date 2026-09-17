# Testing

## Test Layers

| Layer | Location / command | Scope |
|---|---|---|
| Unit / regression | `go test ./...` | parsers, chunking, stores, retrieval, model providers, service lifecycle |
| API integration | `go test ./internal/web` | HTTP contracts, conflict handling, metrics, raw restore, write-only per-base credentials |
| Extension integration | `go test ./internal/extension` | manifest, stdio lifecycle, tools, automatic context policy |
| Runtime integration | `go test ./internal/runtime` | real helper process handshake, calls, restart, idle lifecycle |
| Storage | `go test ./internal/storage` | migrations, FTS, raw-store guards/private modes, maintenance |
| Web | `cd web && npm test` | API route contracts and build integrity |
| Browser E2E | `cd web && npm run test:e2e` | real Chrome/CDP lifecycle: base create, import, ready state, chunk expand/collapse, Recall Test, history replay/delete, OCR model workflow, dismissable toast and Ollama isolation policy, zh/en shell localization, and base delete |
| Race / concurrency | `go test -race ./...` | jobs, batch ingestion, runtime processes, service state |
| Benchmark | `go test ./internal/knowledge -run '^$' -bench Benchmark -benchmem` | repeatable corpus timing and retrieval quality |

Gate evidence is consolidated in `docs/gates.md`; the Phase 8 security review
and residual deployment risks are recorded in `docs/security_review.md`. The
one-for-one map from 25 upstream spec areas to current test layers is in
`docs/test_parity.md`.

On the current Windows workspace, the reproducible full-race command is
`go test -race ./... -p 1 -count=1 -timeout=30m`; `-p 1` prevents unrelated
packages from contending for the same temporary files and process resources.
The parallel form remains useful for stress, but a contention-induced timeout
must not be reported as a race failure without an isolated rerun.

Application, Web, and Extension test entry points set
`SHUTU_KNOWLEDGE_DISABLE_MANAGED_RUNTIME=1` so ordinary tests do not download
the production-pinned Node runtime from the network. Managed-runtime install,
checksum, startup, and real Agent integration remain separate acceptance
coverage; this setting does not change the production default.

CI runs web build/test, the Agent-internal import gate, build, vet, unit/race
tests, a one-iteration benchmark smoke run, and the browser E2E lifecycle.
Browser E2E builds the Web assets and Go binary, starts a temporary Knowledge
data domain on loopback, drives real Chrome through CDP, checks the dedicated
PaddleOCR model/runtime state, chunk preview expansion, upstream UI-policy
behavior (dismissable toasts and Ollama browsing/pulling isolated from provider
persistence), persistent zh/en localization, and cleans successful
temporary state automatically. It requires Chrome, Chromium, or Edge; CI
selects `google-chrome`.

## Retrieval Benchmark Dataset

The Go benchmark fixture creates the same 119-document corpus on every run. It
covers:

- English and Chinese lexical queries,
- semantic/vector concepts,
- multi-document relevance,
- exact duplicate copies with one distinguishing identifier,
- a long document,
- mixed-language finance vocabulary,
- and deterministic filler documents so ranking is not trivial.

The correctness gate checks Hit@3, MRR, and context recall across six labeled
queries. Deterministic vectors make this gate reproducible in CI; it evaluates
retrieval composition, not neural-model quality.

## Benchmark Commands

Fast CI smoke:

```sh
go test ./internal/knowledge -run '^$' -bench Benchmark -benchtime=1x
```

Longer local run:

```sh
go test ./internal/knowledge -run '^$' -bench Benchmark -benchmem -benchtime=3s
```

The suite reports ingestion, chunking, embedding hash reuse, lexical, vector,
hybrid, rerank, and end-to-end ingest/search timings plus allocations. Numbers
are machine-dependent and are useful for regression comparison, not absolute
product-performance claims.

## Architecture P0 Input Baseline

`cmd/architecture-baseline` collects repeatable, metadata-only input evidence
for the architecture refactor. Run it against stopped or consistency-snapshotted
SmartCare copies; keep the report outside the scanned directories:

```powershell
go run ./cmd/architecture-baseline `
  -dataset product-dictionary=D:\baseline\smartcare-product `
  -dataset suite=D:\baseline\smartcare-suite `
  -database product-dictionary=D:\baseline\smartcare-product\knowledge.db `
  -database suite=D:\baseline\smartcare-suite\knowledge.db `
  -config config.yaml `
  -output baseline\YYYY-MM-DD\input-inventory.json
```

The report contains sorted file paths, sizes, SHA-256 digests, aggregate text
distribution, model/storage metadata, redacted configuration, host capacity,
and a stable `reportFingerprint`; it never stores source bytes or credentials.
Missing or unreadable dataset/database inputs are errors, not empty baselines.

For API workload evidence, run the bounded scenario collector against an
isolated running instance. It records request outcomes, latency percentiles,
Durable Operation timing fields, and sanitized status/metrics snapshots without
storing search hits or complete responses:

```powershell
go run ./cmd/architecture-scenarios `
  -base-url http://127.0.0.1:8765 `
  -base-id <product-base-id> `
  -base-id-2 <suite-base-id> `
  -scenario status,single-import,single-reindex,dual-reindex,continuous-search,page-switch `
  -duration 5m -concurrency 4 `
  -output baseline\YYYY-MM-DD\api-scenarios.json
```

Terminal Operation entries include an ordered `eventTrace` containing only
event ID, revision, timestamp, adjacent-event elapsed milliseconds, kind, and phase progress counters. Command
payloads, error text, and unknown fields are excluded. Use it to verify
lifecycle/phase order; it is local traceability evidence, not SmartCare-scale
acceptance evidence.

The continuous workload keeps expected `429`/`408`/`504` responses in the
report and continues sampling instead of aborting on the first overload. It
fails the scenario if no work request succeeds. `/api/search` additionally
requires at least one hit carrying non-empty document/base IDs and positive
`indexGeneration`/`sourceVersion`; HTTP 200 with empty or unversioned hits is
not accepted as valid retrieval evidence. OS restart, file-lock, and disk-fault
scenarios remain `incomplete` until a release-host controller supplies them.

Durable Operation command payloads and executor results share a 1 MiB
persistence boundary. An oversized result must finish as a sanitized,
non-retryable `result_too_large` operation without writing a result blob;
bounded partial results remain readable for failed or cancelled batch work.
Batch operations also persist bounded `operation_items` markers for each
committed, skipped, or failed item; a committed marker is monotonic across
attempts and is exposed through the Operations service's bounded item listing.
For document import, delete, and reindex, the committed marker is written by
the Knowledge commit hook inside the business publication/fence transaction;
physical raw-file cleanup remains a separately recoverable step. The rollback
regression is `go test ./internal/knowledge -run TestCommitHookRollbackProtectsDocumentFence`.
Directory import/rescan and recursive delete additionally persist a bounded
aggregate-root marker and preserve a bounded failure result; an already
fenced delete root remains eligible for cleanup on retry. Re-index executors
query one item marker at a time before replay so committed/skipped items are
not executed again.
App-created operations additionally record a local principal, base/document
epoch references, source version, and hashed non-secret configuration/model
snapshot references. Idempotency lookup precedes this enrichment so replay
still works after a target is deleted.

The local-model picker is also bounded: use `GET /api/local-models?limit=100&offset=0`
and advance with the returned `nextOffset` while `hasMore` is true. The legacy
no-parameter response keeps `models`/`cacheDir` for compatibility and is marked
with `Deprecation: true`; managed-runtime entries are included only on the first
page.

Extension document reads are bounded by default as well. `knowledge_list_documents`
and `knowledge_list_bases` with `baseId` accept optional `limit`/`offset` and return
the compatible `documents` field together with `total`, `limit`, `offset`, and
`hasMore`; the default limit is 50 and the service cap is 200.

The indexing-status control endpoint is bounded independently of document-list
pagination. `GET /api/indexing-status` filters to active `pending`/`processing`
rows in SQL, returns at most 200 entries, and honors the HTTP request context.
Regression command:

```powershell
go test ./internal/knowledge ./internal/web ./internal/storage -run `
  'TestIndexingStatusIsBoundedAndCancellable|TestRawRestoreProbeAndIndexingAPI' `
  -count=1
```

The full affected package run is:

```powershell
go test ./internal/knowledge ./internal/web ./internal/storage -count=1 -timeout=20m
```

Restore source-page memory behavior is covered by:

```powershell
go test -race ./internal/knowledge -run `
  'TestRestoreSourcePagesUseMetadataOnly' -count=1
```

The test verifies that the fixed-size restore page does not materialize
`raw_text`; the restore worker then performs one cancellable full-source lookup
only for the document currently being restored.

URL operation replay cleanup is covered by:

```powershell
go test -race ./internal/app -run `
  'TestResolvedURLOperationConsumesLeftoverCapture' -count=1
```

This simulates a published item marker with a still-ready capture and verifies
that replay clears the capture body before returning the durable result.

HTTP admission cancellation for the OCR model operation is covered by:

```powershell
go test ./internal/web -run `
  'TestOCRModel(APIStatusAndRemove|DownloadHonorsRequestCancellation)$' `
  -count=1
go test -race ./internal/web -run `
  'TestOCRModel(APIStatusAndRemove|DownloadHonorsRequestCancellation)$' `
  -count=1
```

The cancelled request must not create a durable operation; an already accepted
operation remains independent and is recovered through the normal operation
status/replay path.

Maintenance cleanup batching and cancellation are covered by:

```powershell
go test ./internal/operations -run `
  'Test(MaintenanceCleanupProcessesRowsInBatches|URLCaptureCleanupProcessesRowsInBatches|UploadMaintenanceCleanupProcessesRowsInBatches)' `
  -count=1
go test ./internal/operations -run `
  'Test(MaintenanceCleanupProcessesRowsInBatches|URLCaptureCleanupProcessesRowsInBatches|UploadMaintenanceCleanupProcessesRowsInBatches)' `
  -race -count=1
```

These tests seed more than one fixed cleanup batch and verify that terminal
payloads/results, expired URL captures, upload sessions, lease state, capture
bodies, and staging files converge without an unbounded in-memory ID list.
They also verify that a cancelled maintenance context stops before starting a
new batch.

Document-tree deletion and crash recovery boundedness are covered by:

```powershell
go test ./internal/knowledge -run `
  'Test(DirectoryDeleteCleanupUsesBoundedPages|RecoverInterruptedUsesBoundedCancellableBatches|DirectoryDeleteTombstoneCleanupCanResume)' `
  -count=1
go test ./internal/knowledge -run `
  'Test(DirectoryDeleteCleanupUsesBoundedPages|RecoverInterruptedUsesBoundedCancellableBatches|DirectoryDeleteTombstoneCleanupCanResume)' `
  -race -count=1
```

The tests verify stable-ID cleanup paging beyond one page, fixed-size chunk
deletion, recovery batches larger than 256 rows, and cancellation before a
new recovery page. A cancelled durable delete leaves the lifecycle fence in
`deleting` so a later attempt can resume physical cleanup.

Long-path context propagation and Writer cancellation outcomes are covered by:

```powershell
go test ./internal/knowledge -run `
  'Test(ReindexAndRawCitationHonorCanceledContext|PutChunkVectorsHonorsCanceledContext)' `
  -count=1
go test -race ./internal/knowledge -run `
  'Test(ReindexAndRawCitationHonorCanceledContext|PutChunkVectorsHonorsCanceledContext)' `
  -count=1
```

The tests cover cancelled reindex/raw-citation reads and vector-batch writes.
Depending on whether cancellation wins before or after Writer admission, the
write test accepts `context.Canceled` or `storage.ErrWriteUnknown`; the latter
means the caller must reconcile durable state.

Directory import/rescan admission and worker context propagation are covered
by:

```powershell
go test ./internal/knowledge -run `
  'TestRunDirectoryImportHonorsCanceledContext|TestDirectory|TestImport|TestRescan|TestRepoint|TestDelete' `
  -count=1
go test -race ./internal/knowledge -run `
  'TestRunDirectoryImportHonorsCanceledContext|TestDirectory|TestImport|TestRescan|TestRepoint|TestDelete' `
  -count=1
```

The cancellation regression verifies that a cancelled import returns before
binding or creating its stable directory container; the broader normal/race
selection covers directory sync, rescan, repoint, and recursive deletion.

Operation executor replay/admission reads use the worker context and are
covered by the App operation regression set:

```powershell
go test ./internal/app -run `
  'Test(AppOperationEnricher|ImportFiles|DirectoryExecutors|SingletonExecutors|ResolvedURL|Cancel|QueuedDelete|DurableDelete)' `
  -count=1
go test -race ./internal/app -run `
  'Test(AppOperationEnricher|ImportFiles|DirectoryExecutors|SingletonExecutors|ResolvedURL|Cancel|QueuedDelete|DurableDelete)' `
  -count=1 -timeout=20m
```

Cancellation may require one bounded final control write to persist an item
`cancelled` marker after the worker context is cancelled; this write is
detached only with a five-second deadline and does not resume business work.
Operation progress callbacks use the same worker context for their control
transaction and phase event; terminal operation completion remains a separate
durable boundary. Delete and base-reindex aggregate executors retain
`partial=true` when an item marker fails after earlier effects committed.
Upload-session validation, import conflict/rename lookups, and custom reranker
KV cleanup also have cancellable worker-context variants; their compatibility
wrappers remain available for synchronous callers.
Local model manifest and OCR status reads used by model operation replay also
check cancellation before and after filesystem inspection.

The full race gate is run serially on Windows to avoid package-level resource
contention masking code results:

```powershell
go test -race ./... -p 1 -count=1 -timeout=30m
```

Storage reconciliation visitor/cancellation behavior is covered by:

```powershell
go test ./internal/knowledge -run `
  'Test(ReconcileStorageSafeQuarantinesAndPurgesOrphans|ReconcileStorageRemovesOrphansAndFixesCounts)' `
  -count=1
go test ./internal/knowledge -run `
  'TestSyntheticCorpusStorageReconcileMovesExactlyOrphans' `
  -race -count=1 -timeout=10m
```

The synthetic regression uses 1,200 documents and 1,200 orphan raw files and
verifies exact orphan selection, referenced-file preservation, quarantine and
purge convergence, chunk-count repair, and generation-pinned raw citations.
The production path uses `WalkAll`/`CountAll` and visitor-based references, so
it does not retain a complete active raw-file listing or document-reference
slice. SmartCare-scale and release-host volume/file-lock drills remain
separate gates.

Use `-delete-document-id` when running `import-delete`; it must be an explicit
document from the second base. `restart`, `writer-lock`, and `disk-critical`
are reported as `unrun` because they require a release-host process or volume
controller. A report containing `unrun` or missing SmartCare inputs is not a
passed P0 gate.

`-duration` is the budget for each continuous scenario, not for the whole list
of scenarios. This allows one invocation to run both `continuous-search` and
`page-switch`; each receives an independent context and must produce at least
one successful work request. The resulting local synthetic report is still not
a substitute for the SmartCare two-base pressure run.

For the fixed retrieval regression set, create a local corpus file from the
SmartCare snapshot. The input is intentionally separate from the report so
the report can be shared as metadata-only evidence:

```json
{
  "schemaVersion": 1,
  "cases": [
    {
      "id": "product-lexical-001",
      "query": "exact product dictionary term",
      "baseId": "<product-base-id>",
      "mode": "lexical",
      "topK": 10,
      "expectedDocIds": ["<expected-document-id>"],
      "negativeDocIds": ["<must-not-match-document-id>"],
      "context": true,
      "rawCitation": true
    },
    {
      "id": "suite-hybrid-001",
      "query": "semantic suite question",
      "baseId": "<suite-base-id>",
      "mode": "hybrid",
      "topK": 10,
      "expectedDocIds": ["<expected-document-id>"]
    }
  ]
}
```

Run it with the current source or a release candidate binary:

```powershell
go run ./cmd/retrieval-regression `
  -base-url http://127.0.0.1:8765 `
  -input baseline\YYYY-MM-DD\retrieval-corpus.json `
  -output baseline\YYYY-MM-DD\retrieval-regression.json
```

The runner executes every case with `debug=true`, then records hit rank/ID,
generation, source version, score fields, reranker provider/model/status, and
stage diagnostic counts/candidates. It computes Hit@1, Hit@3 and MRR, rejects
unexpected negative hits, and uses the first hit as an explicitly versioned
anchor for context and raw-citation checks. Query text, hit text, context,
raw bytes, and reranker error messages are never written to the report. A
missing expected hit, negative hit, HTTP failure, or dependent-check failure
makes the case and report fail; no hit means dependent checks are `unrun`.

### 上传输入 lease 回收回归

`internal/operations` 还覆盖两条输入所有权边界：不可重试终态/达到最大
attempt 后释放绑定 upload，以及进程在终态写入与后置释放之间退出时由下一次
启动补偿释放。对应测试为：

```powershell
go test -race ./internal/operations -run `
  'TestUpload(ReleasesAfterNonRetryableFailure|BindsAtomicallyAndReleasesOnSuccess)|TestStartupReleasesTerminalBoundUpload' `
  -count=1
```

测试必须同时确认数据库状态为 `released`、staging 文件已删除；该本地测试不
替代 release-host 持久卷、跨平台锁和 ENOSPC 验收。

### 单项与批量业务 marker 回归

Durable executor 重放前会查询标量 `operation_items` marker。文本/文件导入、
单项删除、知识库删除、批量删除和 URL 导入/刷新在已提交项上直接返回已知
结果；文件 `replace` 冲突的旧文档删除使用独立 `replace:<documentId>` marker，
避免把旧文档删除误当作新文档导入已提交。跨进程 post-publish 测试同时保留
无 marker 的历史数据兼容分支，验证 ready 文档不会被重复创建。

模型、OCR、Ollama、自检、缓存迁移和维护的 singleton effect marker 同样保存
有界终态结果；若进程在副作用完成和 Operation 终态之间退出，重放会先返回
marker 结果而不是重新触发副作用。

对于替换与去重边界，运行：

```powershell
go test -race ./internal/knowledge -run `
  'TestAddFiles(ConflictStrategiesAndDedup|ReplaceReimportsSameContentAfterTitleCollision|UsesResolvedConflictStrategy)' `
  -count=1
```

同标题同内容的 `replace` 必须以重新导入的 ready 文档结束，而不是
`Skipped`；本地通过仍不能替代 SmartCare 规模或 release-host 证据。

批量 Operation 重放还必须验证已 committed/skipped 的项在进入冲突解析前
被跳过：

```powershell
go test -race ./internal/app -run `
  'TestImportFiles(ExecutorSkipsResolvedItems|OperationPersistsStableItemAllocations)' `
  -count=1
```

目录聚合操作也必须在业务入口前检查 marker：

```powershell
go test -race ./internal/app -run `
  'TestDirectoryExecutorsSkipResolvedAggregates' `
  -count=1
```

该测试会在 marker 写入后移除源目录；若执行器仍扫描、重扫或删除，测试必须失败。

模型、OCR、Ollama、模型缓存迁移、自检和 SQLite/Raw 维护执行器也必须在外部
副作用前检查 marker，并在成功后持久化 marker；本地状态可判定的重试还要能从
文件系统或远端模型清单补偿。统一回归为：

```powershell
go test -race ./internal/app -run `
  'TestSingletonExecutorsSkipResolvedEffects' `
  -count=1
```

该测试通过预置 marker 验证这些执行器不会触发模型下载、运行时自检、Ollama
请求、缓存迁移或维护副作用；SmartCare/release-host 的真实下载、断点和跨平台
故障仍需单独验收。

### Runtime 关闭边界

`App.Close`、配置热更新和启动失败清理都必须给 runtime 关闭传播 deadline；旧
compatibility controller 只有无 context 的 `Close` 时，也不能无限阻塞应用的
DB/实例锁清理。回归命令：

```powershell
go test -race ./internal/app -run `
  'TestCloseRuntimeWithContextBoundsLegacyController' `
  -count=1
```

该测试只证明 compatibility fallback 的等待边界；真实 helper 进程树回收和跨平台
release-host 退出演练仍需单独运行。

实例锁释放的取消边界使用：

```powershell
go test ./internal/storage ./internal/app -run 'TestInstanceLockReleaseWithContextRemainsBoundedAndReleasesOwnership' -count=1
go test -race ./internal/storage ./internal/app -run 'TestInstanceLockReleaseWithContextRemainsBoundedAndReleasesOwnership' -count=1 -p 1
```

该回归验证取消的 shutdown context 不会留下实例所有权；真实跨平台信号和进程树
验收仍需 release-host 执行。

启动维护 watcher 的 context 链路由 App 包回归覆盖：

```powershell
go test ./internal/app -count=1 -timeout=15m
go test -race ./internal/app -p 1 -count=1 -timeout=20m
```

维护状态查询必须使用可取消的 `maintenanceCtx`；真实卡死维护和持久卷退出仍需
release-host 演练。

Durable worker 的 claim 后读取、终态持久化和 upload lease 释放，以及 HTTP
幂等预检/Scheduler 状态读取的 context 链路由以下回归覆盖：

```powershell
go test ./internal/operations ./internal/web -count=1 -timeout=20m
go test -race ./internal/operations ./internal/web -p 1 -count=1 -timeout=30m
```

取消在 pre-dispatch hook 发生时，executor 仍须收到已取消 context；终态 marker
则使用独立的有界 finalization budget，不能因业务 context 取消而丢失。真实 Writer
繁忙、断线和提交后重放仍需 release-host 验收。

健康检查与 `/api/status` 的存储元数据读取也必须保留调用方 context：

```powershell
go test ./internal/storage ./internal/app ./internal/web -count=1 -timeout=20m
go test -race ./internal/storage ./internal/app ./internal/web -p 1 -count=1 -timeout=30m
```

其中 `TestStorageMetadataContextHonorsCancellation` 验证已取消 context 不会
退化为无界 `QueryRow`；该测试只证明本地读取 seam，不能替代 release-host 的
Writer 忙、持久卷故障或 Agent Host 断线验收。

重新导入前的旧 generation 向量计数取消边界使用：

```powershell
go test ./internal/knowledge -run TestEmbeddedChunkCountHonorsCanceledContext -count=1
go test -race ./internal/knowledge -run TestEmbeddedChunkCountHonorsCanceledContext -count=1 -p 1
go test ./internal/knowledge -run TestContentHashLookupHonorsCanceledContext -count=1
go test -race ./internal/knowledge -run TestContentHashLookupHonorsCanceledContext -count=1 -p 1
```

读取失败必须停止安全回退路径，不能把未知状态当作零向量继续发布。

场景 runner 的“预期拒绝后仍必须出现成功样本”测试使用独立的测试观察预算：

```powershell
go test ./cmd/architecture-scenarios -run TestRunContinuousAllowsExpectedRejectionsWithSuccessfulWork -count=3
go test -race ./cmd/architecture-scenarios -run TestRunContinuousAllowsExpectedRejectionsWithSuccessfulWork -count=3 -p 1
```

该预算只用于让测试在 race 开销下观察完整的拒绝/成功序列，不改变生产请求
超时或场景验收阈值。

Knowledge 旧 jobs seam 的回归约束：

```powershell
go test ./internal/knowledge -count=1 -timeout=30m
go test -race ./internal/knowledge -p 1 -count=1 -timeout=30m
```

Knowledge Service 不再持有 legacy `jobs.Manager`，目录导入/重扫和整库重建
测试直接调用 Durable worker 共用的 `RunDirectoryImport`、`RunDirectoryRescan`
和有界 `ForEachReindexDocumentBatch`。Web/Agent 的旧 `jobId` 只允许映射到
Durable Operation，不得恢复第二个真实工作队列。

删除栅栏的重放回归：

```powershell
go test ./internal/app -run TestDurableDeleteReplayResumesCommittedPhysicalCleanup -count=1 -timeout=30m
go test ./internal/app -run TestDurableDeleteCleanupContinuesAfterCommittedCancel -count=1 -timeout=30m
```

`operation_items.committed` 只表示逻辑删除栅栏已提交；如果 raw/chunk 物理
清理随后失败，`delete_document`、`delete_directory` 和 `delete_base` 的重放
必须继续清理 deleting tombstone，而不是直接返回成功。目录导入/重扫/删除的
失败 marker 写入失败也必须作为执行失败返回，不能被忽略。

目录同步状态写入的取消传播回归：

```powershell
go test ./internal/knowledge -run 'TestDirectory(SyncStateWritesPropagateCancellation|TreeImportsMultipleNestedLevels)$' -count=3 -timeout=30m
go test -race ./internal/knowledge -run 'TestDirectorySyncStateWritesPropagateCancellation|TestDirectoryTreeImportsMultipleNestedLevels' -p 1 -count=3 -timeout=30m
```

进度、失败容器、失败子项和成功收尾的状态写入不能静默吞掉取消或 Writer
错误；已入队但调用方取消时允许返回 `storage.ErrWriteUnknown`，调用方必须
按 Durable Operation 状态继续对账。

上传 lease 清理的失败闭环回归：

```powershell
go test ./internal/operations -run 'Test(UploadMaintenanceCleanupProcessesRowsInBatches|UploadCleanupFailurePreservesRetryableLeaseState)$' -count=1 -timeout=30m
go test -race ./internal/operations -run 'Test(UploadMaintenanceCleanupProcessesRowsInBatches|UploadCleanupFailurePreservesRetryableLeaseState)$' -p 1 -count=1 -timeout=30m
```

过期上传和终态 bound lease 都必须先删除 staging 文件，再发布 `expired`/
`released`；路径解析或删除失败时保留原状态，等待下一轮对账。worker 完成后的
终态写入失败会再进行一次有界幂等重试；这仍不能替代 release-host 的进程终止、
响应未知和 lease 对账演练。

延迟启动恢复失败的就绪语义回归：

```powershell
go test ./internal/app -run 'TestHealthSnapshotDoesNotReportReady' -count=1 -timeout=30m
go test -race ./internal/app -run 'TestHealthSnapshotDoesNotReportReady' -p 1 -count=1 -timeout=30m
```

恢复进行中返回 `starting/ready=false`；恢复失败后保持 `unhealthy: startup-recovery`
和 `ready=false`，不能仅因恢复 goroutine 已退出就报告 ready。真实恢复故障、重启
以及 release-host 退出预算仍需在目标平台复验。

延迟启动恢复的就绪语义由 App 生命周期回归覆盖：

```powershell
go test ./internal/app ./internal/web ./internal/extension -count=1 -timeout=20m
go test -race ./internal/app ./internal/web ./internal/extension -p 1 -count=1 -timeout=30m
```

恢复未完成时必须返回 `status=starting` 且 `ready=false`；恢复完成后仍须
返回正常 ready。Windows host profile 和生产 Agent Host 仍需分别验证实际
启动时间线、路由展示和重启行为。

真实 helper 忙于长请求时，runtime 关闭还必须绕过请求串行锁，终止拥有者进程并让
响应读取协程在进程退出后收敛：

```powershell
go test -race ./internal/runtime -run `
  'TestManagerCloseWithContextBoundsBusyHelper' `
  -count=1
```

关闭 API 的 deadline 与调用协程的最终收敛是两个断言；race 工具的管道收尾开销不能
被误报为无限等待，但真实 release-host 的进程树和信号演练仍是必需门禁。

旧 jobs 兼容队列的退出预算也单独回归：

```powershell
go test ./internal/jobs -run TestStopWithContextBoundsNonCooperativeLegacyTask -count=1
go test -race ./internal/jobs -run TestStopWithContextBoundsNonCooperativeLegacyTask -count=1 -p 1
```

该测试只验证旧任务管理器的调用方不会被不合作任务无限阻塞；生产 App 已不再
启动该队列，真实 helper 进程树和跨平台 release-host 退出仍需单独验收。

### Operation 控制请求取消边界

Operation 查询、取消和重试必须使用调用方 context；已接收的 Durable
worker 则继续使用独立生命周期 context。这样客户端断开只取消当前控制请求，
不会把已经持久化的业务任务变成半提交状态。回归命令：

```powershell
go test ./internal/operations -run `
  'TestOperationControlReadsHonorCanceledContext|TestCancelRecordedBeforeWorkerRegistrationCancelsContext|TestCancelRunningOperationAndRejectRepeatRetry|TestRetryHonorsAttemptLimitAndRecordsEvents' `
  -count=1
go test -race ./internal/operations -run `
  'TestOperationControlReadsHonorCanceledContext|TestCancelRecordedBeforeWorkerRegistrationCancelsContext|TestCancelRunningOperationAndRejectRepeatRetry|TestRetryHonorsAttemptLimitAndRecordsEvents' `
  -count=1
```

Web 新 Operation API、旧 job API 和 Extension 工具必须继续传递请求 context；
完整 Web/Extension 包回归已通过，但 Agent Host 断线、Writer 忙和跨进程
control-failure 仍需 release-host 演练。

目录同步的文件读取和标题冲突查询也必须在 worker context 内完成：

```powershell
go test ./internal/knowledge -run `
  'TestDirectoryChildTitleLookupHonorsCanceledContext|TestImportConflictLookupsHonorCanceledContext|TestRunDirectoryImportHonorsCanceledContext' `
  -count=1
go test -race ./internal/knowledge -run `
  'TestDirectoryChildTitleLookupHonorsCanceledContext|TestImportConflictLookupsHonorCanceledContext|TestRunDirectoryImportHonorsCanceledContext' `
  -count=1
```

取消或数据库错误不得被解释为标题不存在并继续物理删除/导入；该回归仍不
替代大目录和 release-host 重放验证。

### 模型控制面读取取消边界

本地模型分页、OCR 状态、模型删除前探测和 reranker 自测结果/持久化必须
使用调用方或 worker context；managed runtime fallback 不能吞掉取消：

```powershell
go test ./internal/models -run 'TestModelReadContextHonorsCancellation' -count=1
go test -race ./internal/models -run 'TestModelReadContextHonorsCancellation' -count=1
go test ./internal/app -run 'TestManagedRerankerSelfTestAcceptsRuntimeCache|TestSingletonExecutorsSkipResolvedEffects' -count=1
go test -race ./internal/app -run 'TestManagedRerankerSelfTestAcceptsRuntimeCache|TestSingletonExecutorsSkipResolvedEffects' -count=1
```

取消请求不得继续进入 manifest/运行时自测或把失败状态写入后台；大模型缓存
和真实模型切换仍需 SmartCare/release-host 验收。

### Base/scope/stat 读取取消边界

Web 基础库列表、详情、统计、恢复/重建前探测以及 Extension 自动上下文的
基础库/provider/document/vector 读取必须保留请求或 worker context；兼容的
无参 API 只能用于旧同步调用：

```powershell
go test ./internal/knowledge ./internal/models ./internal/app ./internal/web -run `
  'TestBaseAndDocumentReadPathsHonorCanceledContext|TestModelReadContextHonorsCancellation|TestManagedRerankerSelfTestAcceptsRuntimeCache|TestOCRModelAPIStatusAndRemove|TestRawRestoreProbeAndIndexingAPI' `
  -count=1
go test -race ./internal/knowledge ./internal/models ./internal/app ./internal/web -run `
  'TestBaseAndDocumentReadPathsHonorCanceledContext|TestModelReadContextHonorsCancellation|TestManagedRerankerSelfTestAcceptsRuntimeCache|TestOCRModelAPIStatusAndRemove|TestRawRestoreProbeAndIndexingAPI' `
  -count=1 -p 1
```

取消不得继续进行大库枚举、统计或模型/文件探测；大库长读、Writer 忙和
release-host 断线重放仍需单独验收。

文档详情、Chunk 分页、删除/重建前探测及显式检索历史读写也必须使用请求
context；兼容无参方法只供同步调用：

```powershell
go test ./internal/knowledge ./internal/web -run `
  'Test(BaseAndDocumentReadPathsHonorCanceledContext|DirectoryChildTitleLookupHonorsCanceledContext|RecallSearchHistoryAPI|RawRestoreProbeAndIndexingAPI)' `
  -count=1
go test -race ./internal/knowledge ./internal/web -run `
  'Test(BaseAndDocumentReadPathsHonorCanceledContext|DirectoryChildTitleLookupHonorsCanceledContext|RecallSearchHistoryAPI|RawRestoreProbeAndIndexingAPI)' `
  -count=1 -p 1
```

取消不得继续物化完整文档/Chunk 或把历史记录写入后台；大目录和 Writer 忙
仍需 release-host 重放。

Durable worker 的单项/批量/URL 导入、RestoreBase 和目录删除不得通过兼容
包装器进行不可取消读取：

```powershell
go test ./internal/knowledge -run `
  'TestBaseAndDocumentReadPathsHonorCanceledContext|TestReindexAndRawCitationHonorCanceledContext|TestRunDirectoryImportHonorsCanceledContext' `
  -count=1
go test -race ./internal/knowledge -run `
  'TestBaseAndDocumentReadPathsHonorCanceledContext|TestReindexAndRawCitationHonorCanceledContext|TestRunDirectoryImportHonorsCanceledContext' `
  -count=1 -p 1
```

取消必须在 Base/Document/Chunk/Raw 读取前后都可观察；真实大库和外部依赖
故障仍需 release-host 演练。

同步写入口的取消测试必须区分 `context.Canceled` 与已入队但结果未知的
`storage.ErrWriteUnknown`：

```powershell
go test ./internal/knowledge -run TestBaseAndDocumentReadPathsHonorCanceledContext -count=1
go test -race ./internal/knowledge -run TestBaseAndDocumentReadPathsHonorCanceledContext -count=1 -p 1
```

若返回 `storage.ErrWriteUnknown`，必须按持久状态核对是否已提交，不能直接
重发新 ID；真实 Writer 忙和 release-host 断线仍需单独验证。

文件解析链路也必须保留 Worker context：可调用外部 legacy helper 的注册表
使用 `ParseContext`，MinerU 轮询等待不得使用不可取消的 sleep；已取消的解析
不能继续进入 OCR 或持久化：

```powershell
go test ./internal/parser -run 'TestRegistryParseContextPropagatesCancellationToLegacyHelper|TestMineruPollWaitHonorsCancellation' -count=1
go test -race ./internal/parser -run 'TestRegistryParseContextPropagatesCancellationToLegacyHelper|TestMineruPollWaitHonorsCancellation' -count=1
go test ./internal/knowledge -run TestParseFileContentHonorsCanceledContextBeforeParser -count=1
go test -race ./internal/knowledge -run TestParseFileContentHonorsCanceledContextBeforeParser -count=1 -p 1
```

该回归只证明本地解析等待 seam；远端服务中断、外部 helper 进程树和
release-host 重放仍须单独验收。

历史 generation GC 压力夹具必须按固定 rowid 批次老化 retired chunks，避免
测试本身构造一个超过 Writer deadline 的无界事务：

```powershell
go test ./internal/knowledge -run TestSyntheticHistoricalCorpusLongReaderGCDrill -count=1
go test -race ./internal/knowledge -run TestSyntheticHistoricalCorpusLongReaderGCDrill -count=1 -p 1
```

仍需在 SmartCare 规模、长读者和持久卷上验证 GC 收敛、WAL 与磁盘峰值。

### 作用域、历史上下文与配置保存取消边界

作用域的显式存在性、带 metadata filter 的 Search、历史文档上下文首读以及
Web 配置原子保存必须在调用方取消后立即返回；取消不能被“没有 scope 配置”
降级为默认全库，也不能继续执行文件替换：

```powershell
go test ./internal/config ./internal/knowledge ./internal/app ./internal/web -run `
  'Test(SaveContextHonorsCancellation|BaseAndDocumentReadPathsHonorCanceledContext)' `
  -count=1
go test -race ./internal/config ./internal/knowledge ./internal/app ./internal/web -run `
  'Test(SaveContextHonorsCancellation|BaseAndDocumentReadPathsHonorCanceledContext)' `
  -count=1 -p 1
```

完整回归仍需使用：

```powershell
go test ./... -count=1 -timeout=30m
go test -race ./... -p 1 -count=1 -timeout=30m
```

本地通过只关闭代码级取消/竞态门；Writer 忙、断线后的提交未知、SmartCare
规模以及 release-host/Agent Host/跨平台回滚仍需单独报告。

### Web raw preview and route cancellation

Raw preview must use the shared text request helper, so the Agent reverse-proxy
prefix, request timeout, and current route `AbortController` apply to raw
content as they do to other detail reads. The API regression checks the prefixed
URL and verifies that a route change aborts the pending request as
`request_aborted`:

```powershell
Push-Location web
npm run build
npm test
npm run typecheck
Pop-Location
```

This is a local browser-API contract check; production Agent Host rapid
navigation, disconnect, and stale-preview behavior remain separate gates.

资源边界测试的 normal 构建保持三秒生产排空预算；race 构建使用单独的十秒
诊断预算，以隔离 race detector 的同步开销。不得把 race 专用预算写回生产配置，
也不得用它替代 SmartCare/release-host 的实际吞吐和资源门禁。

### Agent 集成安装/移除门禁

该门禁使用本机 Agent 二进制和真实扩展加载流程，验证冷启动、工具目录、Web
路由与健康状态；首次 managed runtime 安装必须允许 120 秒：

```powershell
pwsh -NoProfile -File .\scripts\removal_gate.ps1 `
  -AgentBinary C:\dev-projects\Agent\shutu-agent\sta.exe `
  -RepoRoot C:\dev-projects\shutu-knowledge `
  -OutputRoot C:\dev-projects\shutu-knowledge\.tmp\removal-gate-YYYYMMDD
```

通过条件是安装态精确注册 18 个 Knowledge 工具、1 条路由且健康，移除态为
0 个工具/0 条路由但 Agent 仍健康；工具名称集合必须完整匹配，不能只比较数量。
本机门禁不替代生产 Agent Host 的持续任务、断线、刷新、重启和跨平台验收。

### Native release-host 生命周期 profile

候选二进制的实例锁、重复启动拒绝和进程终止后重启可单独执行：

```powershell
go run ./cmd/release-acceptance -profile host -allow-dirty `
  -output .tmp\release-acceptance-host
```

该 profile 会在隔离 data home 启动 `serve`，检查 `/healthz`，要求第二实例因
`instance.lock.db` 失败，再终止首实例并验证同一目录可重启。`-allow-dirty` 只
适合本地诊断；正式 release-host 必须使用干净候选 checkout，验收器会同时检查
tracked 和 untracked 文件。它不替代
SIGTERM/CTRL+C、子进程树、Linux/macOS、持久卷和生产 Agent Host 验收。
`host` 只执行 native lifecycle 与 manifest 校验；`core`、`agent`、`web` 及
`all` 的测试组按 profile 额外执行，其中 `all` 才组合全部阶段。
native lifecycle 的终止操作只针对它创建的 owned PID tree：Unix 使用进程组，
Windows 使用 `taskkill /T`；平台交叉编译只能验证代码可编译，不能替代原生运行。

### HTTP 关闭预算

Standalone `serve` 收到退出信号后，HTTP server 使用 15 秒关闭 deadline；不合作
的请求处理器不能让后续 App/数据库清理无界等待。对应的 normal/race 回归为：

```powershell
go test ./cmd/shutu-knowledge -run TestShutdownServerWithTimeoutBoundsUncooperativeHandler -count=1
go test -race ./cmd/shutu-knowledge -run TestShutdownServerWithTimeoutBoundsUncooperativeHandler -count=1 -p 1
```

该测试只证明 HTTP 关闭预算，真实卡死 helper、子进程树和 release-host 信号验收
仍需按 P6-03/P6-04 执行。

WSL2 交付 smoke 可用于快速检查 Unix 构建和基本生命周期，但只能作为补充：
应记录 `CGO_ENABLED=0`、二进制版本/存储契约、`/healthz`、重复实例锁拒绝和
SIGTERM 退出；不能将 WSL2 或交叉编译结果记为 Linux/macOS release-host 通过。

P3 本地故障回归命令：

```powershell
go test ./internal/knowledge -run TestCorpusStorageReconcileFileLockStopsAndConverges -count=1 -timeout=20m
go test ./internal/storage -run 'Test(IsolatedBackupRestoreAndIncompatibleReaderDrill|SyntheticLargeDatabaseBackupRollbackDrill)' -count=1 -timeout=20m
go test ./internal/knowledge -run TestSQLiteDiskFullAtGenerationCommitPreservesActiveAndRecovers -count=1 -timeout=20m
```

本地夹具通过后仍必须在 SmartCare 规模持久卷和旧兼容二进制上重复，并记录
版本/SHA、备份 Hash、空间峰值、故障点、恢复差异与收敛时间。

正式包静态敏感信息审计：

```powershell
pwsh -NoProfile -File .\scripts\audit_release_package.ps1 `
  -PackageRoot .tmp\formal-release\shutu-knowledge-<version>-windows-amd64
```

该审计扫描文本文件中的私钥、高置信度 token 和非空 credential 字段；正式
`package_release.ps1` 会在归档解压校验后自动调用它。审计通过不等于部署 smoke
或外部发布授权。
