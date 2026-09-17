# Shutu-Knowledge 健壮性架构改造方案

> 状态：实施工作计划（基于 v1.4 任务执行复核重生成；尚未完成）
>
> 版本：v1.5
>
> 日期：2026-09-16
>
> 最后更新：2026-09-16（持续实施记录）
>
> 适用范围：Shutu-Knowledge 后端、Web UI、Agent 集成、文档导入/扫描/索引/删除、模型调用及运行时生命周期

## 0. 使用规则

本文档是后续架构改造的约束基线和可执行工作计划，不是零散的优化建议。

v1.5 保留 v1.1 的异步协议、可重放命令、祖先删除栅栏、搜索版本固定、单 Writer、在线检索边界和回滚契约，并把评审结果及本轮实现反馈落实为阶段状态、任务编号、实现范围、证据产物和发布门禁。本文中的“已实现”只表示代码或本地测试已有证据；没有规模、跨平台或 release-host 证据的条目不能标记为完成。

状态标签统一使用：

- **已实现基础**：当前代码和已有测试已覆盖，但仍可能需要 release-host 或 SmartCare 规模复验。
- **部分完成**：存在可用纵向切片，但仍有绕过边界、缺字段、缺调用方迁移或缺退出证据。
- **未闭合**：尚无足够实现或验收证据，不能作为下一阶段启用条件。
- **门禁通过**：实现、回归、压力、故障、报告和回滚证据全部齐全。

后续每项实现任务必须说明：

1. 对应本文档的阶段和条目。
2. 是否改变了本文档定义的状态机、任务契约或数据一致性约束。
3. 如何验证 Web 可用性、任务正确性和资源上限。
4. 如果需要偏离方案，必须先增加“偏离记录”，写清原因、风险、替代方案和回滚方式。

当前基线结论：P1a/P1b/P2/P3/P4/P5/P6 均已有不同程度的实现，但整体仍未完成。下一次代码变更必须先选择本文的任务编号，并在对应门禁通过前禁止宣称“架构改造完成”或“可发布”。

禁止为了通过旧测试或暂时消除页面卡顿而重新引入以下做法：

- 在 HTTP handler 中同步执行导入解析、索引、批量模型处理、物理删除或 VACUUM；在线检索按 §5 不变量 A 的有界请求契约执行。
- 在多个模块中各自创建后台 Worker、各自限流、各自维护任务状态。
- 通过进程名或全局进程列表杀死所有同名进程。
- 通过先删除旧索引再写入新索引来实现重新索引。
- 通过增加轮询频率、整页重渲染或 Toast 掩盖后端没有明确任务状态的问题。

---

## 1. 决策摘要

当前系统并非完全错误的架构。SQLite WAL、读写分离、单写连接、有限的 IO Worker、Embedding 并发限制和路由版本保护，已经构成了有效的基础。

但目前仍有三条边界没有彻底建立：

1. 控制面（Web/Agent/API）与执行面（导入/索引/删除/模型调用）没有统一隔离。
2. 任务调度、磁盘 IO、数据库写锁、模型并发没有统一的资源调度器。
3. 前端路由渲染、任务轮询和后台刷新共同修改一个全局 DOM 容器。

因此，推荐采用以下路线：

> 先在单进程内完成控制面、任务面、资源面、存储面和前端状态面的分离；通过真实压力数据判断是否需要拆分 Control/Worker 进程。

不在第一阶段直接迁移 PostgreSQL、向量数据库或多进程。数据库迁移和进程拆分都应当是压力验证后的决策，而不是卡顿问题的默认答案。

实施顺序采用“先满足安全启用条件，再切换入口”：P0 先测现状；P1a 完成统一 Writer、最小 generation/tombstone、全部读取路径兼容和恢复基础并通过门禁；随后才启用 P1b 的持久化 Operation、异步写接口及 Web/Agent 适配。P2 完善资源调度，P3 完善大规模清理与恢复。不能在旧的大事务仍可运行时，单独上线“等待 SQLite 持久化后返回”的任务入口。

---

## 2. 当前架构审计结论

### 2.1 已有的有效基础

当前实现中以下机制应保留并纳入统一架构：

- SQLite WAL 模式。
- 独立读库连接和单写连接。
- IO 任务队列和有限的导入并发槽。
- 目录导入、目录扫描、文档重新索引的任务进度。
- 文档列表避免直接返回完整原文。
- 前端路由 generation 检查。
- Agent 集成入口与后台恢复延后启动的方向。

这些措施说明系统已经开始向异步和受控并发演进，但现在不同模块仍然存在绕过统一边界的路径。

### 2.2 主要问题与证据位置

| 问题 | 当前表现 | 证据位置 |
|---|---|---|
| 同步长请求 | 导入、删除、`RestoreBase`、启动 SQLite 维护及 OCR/本地模型/Ollama 删除、模型缓存迁移计划等长操作入口已转为 Durable Operation；仍需静态审计所有兼容入口 | [internal/web/api.go](../internal/web/api.go)、[internal/app/operations.go](../internal/app/operations.go) |
| 长数据库事务 | Chunk 替换已拆为有界 stage/batch/finalize 写入；其余删除、统计和维护路径仍需按压力数据继续拆分 | [internal/knowledge/store.go](../internal/knowledge/store.go)、[internal/storage/writer.go](../internal/storage/writer.go) |
| 队列粒度过粗 | Durable Scheduler 已成为生产任务入口；旧 `jobs` 包仅保留 Knowledge 测试/兼容 seam，仍需完成全仓静态审计后再删除兼容代码 | [internal/jobs/manager.go](../internal/jobs/manager.go)、[internal/app/app.go](../internal/app/app.go) |
| 任务竞态 | generation、tombstone、ancestor fence 和 attempt 条件提交已有基础；仍需审计所有旁路并统一 Writer/Operation 边界 | [internal/knowledge/service_doc.go](../internal/knowledge/service_doc.go)、[internal/knowledge/service_dir.go](../internal/knowledge/service_dir.go) |
| 跨存储不一致 | raw/generation 保护已有实现；剩余写路径尚未全部经过统一写入准入和短事务发布标记 | [internal/knowledge/store.go](../internal/knowledge/store.go)、[internal/storage/rawstore.go](../internal/storage/rawstore.go) |
| 维护任务竞争 | Durable maintenance lane 已存在，启动 SQLite 维护已统一到 `maintenance_storage`；仍需补充启动重启、配置变更和大库资源证据 | [internal/app/app.go](../internal/app/app.go)、[internal/app/operations.go](../internal/app/operations.go) |
| 全库读取 | 主文档页、索引状态、重建、目录删除和模型选择控制路径已有有界读取；仍需完成大目录预算和旧完整列表退役 | [internal/web/api.go](../internal/web/api.go)、[internal/knowledge/store.go](../internal/knowledge/store.go)、[internal/app/app.go](../internal/app/app.go)、[web/src/app.js](../web/src/app.js) |
| 前端渲染竞态 | OperationStore、AbortController 和数据级刷新已有基础；仍需在生产 Agent Host 验证压力下的 stale-response 和空态行为 | [web/src/app.js](../web/src/app.js) |
| 进程边界不足 | 当前单进程是既定目标；真正未闭合的是实例所有权、旧任务接管和有界退出的 release-host 证据，不应直接拆进程掩盖问题 | [internal/extension/extension.go](../internal/extension/extension.go)、[internal/app/app.go](../internal/app/app.go) |

### 2.3 “Web 卡顿”的完整因果链

不能把所有卡顿都归因于数据库。当前至少存在四类原因：

```text
HTTP 同步长任务 ─┐
                 ├─ 请求超时 / 页面等待 / 重复点击
SQLite 长写事务 ─┤
                 ├─ 读接口延迟上升 / 磁盘队列增长
解析和模型推理 ──┤
                 ├─ 进程 CPU、内存或磁盘竞争
前端异步竞态 ────┘
                   页面只有标题、空白或旧数据覆盖新页面
```

架构改造必须同时覆盖可用性、吞吐、数据正确性和可观测性，不能只调整一个并发数。

### 2.4 2026-09-16 状态重判定

| 阶段 | 当前状态 | 已有证据 | 仍阻塞完成的事项 |
|---|---|---|---|
| P0 | 未闭合 | 已有导入/检索阶段指标、Operation 的排队/运行耗时字段、生命周期/阶段事件及按类型状态/拒绝/超时/耗时聚合、Writer 聚合及 control/normal/maintenance 分项耗时、RSS/WAL/temp/upload 采样和配置映射 | 可重复资源/场景报告、冻结 SmartCare 双库基线、将阶段指标与每类操作报告稳定关联 |
| P1a | 部分完成 | generation、source、tombstone、ancestor fence、immutable raw、历史读视图、Storage Writer、主要写路径迁移、全 internal writer guard 和故障测试；目录同步进度/失败/收尾状态写入错误已向上闭合 | 全部旁路盘点、完整写路径分项计时、剩余批次/大事务压力和兼容备份恢复门禁 |
| P1b | 部分完成 | Durable Operation、幂等、取消、恢复、上传/临时 staging 配额、过期 terminal payload/result 清理、持久化 operation resource budget、`restore_base`、导入/删除/模型删除/Ollama 删除 Web 适配和 C01-C16 本地覆盖；生产 App 不再启动旧队列，Knowledge Service 不再持有旧任务执行器；已提交逻辑删除栅栏的 replay 会继续物理清理 | 剩余聚合命令失败结果/marker 语义、模型/输入/结果资源所有权、规模/故障证据和 release-host 重放 |
| P2 | 部分完成 | 资源 lane、队列上限、per-base 限制、priority aging、模型并发、检索 deadline、按 operation 预留 memory/disk/temp、RSS/WAL/temp/upload/卷可用空间状态、低水位提交拒绝策略、上传原子配额、非阻塞 runtime 状态快照和重建等价键初版 | 双库公平性压力、等价键跨入口/并发故障证据、持久卷 ENOSPC/锁行为、阈值校准、双库压力数据和吞吐预算对账 |
| P3 | 基础完成 | quarantine、retention、repair、Durable 启动维护、generation/GC 和本地磁盘/锁故障测试 | SmartCare 规模 GC/长读/模型切换、持久卷 ENOSPC、备份恢复与旧版本回滚 |
| P4 | 基本完成 | 子节点分页、懒加载、统计缓存；重建、目录删除、索引状态和模型缓存计划控制入口已改为 SQL 标量/状态过滤、可取消扫描与有界读取；兼容平面列表支持分页并标记旧无参数模式弃用 | 验证重建/删除/索引状态/模型选择与大目录接口预算，并决定旧无参数完整列表的最终退役时间 |
| P5 | 本地基本完成 | OperationStore、刷新恢复、路由取消、数据级刷新、E2E | 生产 Agent Host 和持续压力下的 UI 证据 |
| P6 | 部分完成 | starting/degraded、实例锁、恢复、App/Operation/Runtime 有界关闭和部分进程树测试 | Linux/macOS release-host、生产 Agent 重启、真实卡死/残留 PID 演练 |

因此本计划的下一工作点不是新增功能，而是按 P0 → P1a → P1b → P2 → P3 → P4/P5 → P6 的门禁顺序，关闭上述“仍阻塞完成的事项”。

---

## 3. 目标与非目标

### 3.1 目标

1. Web/Agent 控制请求在后台重任务运行时保持可用。
2. 所有长任务都有持久化状态、阶段、进度、错误和明确的取消边界。
3. 多个知识库同时运行时，任务按资源配额、公平性和优先级执行。
4. 重新索引失败时保留旧索引，不让文档进入不可检索状态。
5. 删除、重新索引、目录扫描之间具有明确的冲突规则。
6. 进程异常退出后可以恢复、重试或明确标记中断任务。
7. 文档页面和导入页面共享任务状态，不通过整页刷新同步状态。
8. 通过 SmartCare 两个知识库进行可重复的 CPU、IO、数据库和响应时间验收。

### 3.2 非目标

第一阶段不做：

- 无压力证据支撑的 PostgreSQL 迁移。
- 无向量查询基准支撑的 ANN/独立向量数据库迁移。
- 为了隔离问题而直接拆成多个进程。
- 大范围重写已有模型配置和知识库业务规则。

---

## 4. 目标架构

```text
Agent / Browser
       │
       ▼
┌──────────────────────────────────────┐
│ Control Plane                         │
│ HTTP API / Agent protocol / Read API  │
│ 校验、提交命令、查询状态、有界检索      │
└──────────────────┬───────────────────┘
                   ▼
┌──────────────────────────────────────┐
│ Operation Service                     │
│ 持久化任务、幂等、取消、恢复、事件       │
└──────────────────┬───────────────────┘
                   ▼
┌──────────────────────────────────────┐
│ Resource Scheduler                    │
│ 优先级、配额、公平调度、互斥、背压       │
└──────┬───────────┬───────────┬────────┘
       │           │           │
       ▼           ▼           ▼
  Parser CPU   Model lane   Storage lane
                              │
                              ▼
┌──────────────────────────────────────┐
│ Storage                               │
│ Metadata SQLite / Chunk+FTS / Vector  │
│ Raw staging/quarantine/final files    │
└──────────────────────────────────────┘
```

第一阶段以上模块仍可在一个进程中运行，但必须通过明确的接口通信，禁止业务代码绕过 Operation Service 直接启动后台 goroutine 或直接进行长写入。

---

## 5. 必须遵守的架构不变量

### 不变量 A：写操作异步，交互检索有界

导入、目录扫描、索引解析和 Embedding、批量 Rerank、自检、模型下载、重建索引、单项/批量/目录/知识库删除及维护任务都使用 Operation。提交 handler 只做参数、权限和路径校验，持久化命令或上传会话记录，然后返回接收状态。文件字节传输由独立上传接口承担，传输时间不计入任务提交延迟。

`POST /api/search`、文档上下文查询、Agent 检索工具和自动上下文回调保留同步结果契约。查询时的 Embedding/Rerank 受同一 Scheduler 管理，但不为每次交互查询创建持久化 Operation。它们必须有端到端 deadline、排队等待预算、查询变体/候选数/输入 token/输出体积上限，并传播请求取消。超预算时返回明确超时/资源不足，或按已有检索模式明确报告降级；不得静默把失败当空结果，也不得用后台任务 ID 替代证据正文。

轻量配置和元数据修改允许在有界短事务完成后同步返回；凡会触发解析、模型加载、重索引或批量清理的部分必须拆成 Operation。在线检索及轻量写入也是统一资源调度的调用方。

### 不变量 B：所有数据库写入必须经过 Storage Writer

业务层不能在任意 goroutine 中直接写 SQLite。数据库写入必须经过统一的写入通道，并满足：

- 一个写资源令牌。
- 短事务，提交、取消和发布结果等控制写入优先于批量索引写入。
- 批量操作有上限。
- 不在事务内执行网络请求、模型推理或文件解析。
- 每个事务记录耗时。

令牌按事务获取和释放，禁止整项任务占有 Writer。提交成功以命令事务实际提交为准；进度可以合并刷新，命令接收、取消意图和最终结果不可只保存在内存中。

### 不变量 C：任务可识别、可恢复，取消有明确边界

每个任务必须有：

- 唯一 ID。
- 类型和目标资源。
- 幂等键。
- 当前阶段。
- 已完成单位和总单位。
- 可重试错误信息。
- 取消状态。
- 启动和结束时间。

任务还必须持久化可版本化的命令输入或稳定引用、请求指纹、预期资源版本、结果及分项提交记录。仅有状态和一个进程内函数闭包不满足可恢复要求。

### 不变量 D：新索引完成后才能替换旧索引

重建索引使用 generation。新 generation 未完成并验证前，不能删除或替换 active generation。

一次搜索的召回、重排、相邻 Chunk、证据和原文引用必须使用固定的 generation/source version。旧 generation 在仍被请求引用时不可回收；只让各条 SQL 分别读取“当前 active”不满足此约束。

### 不变量 E：删除先标记，再清理

删除必须先持久化逻辑 `deleting` 状态。目标字段采用知识库和文档/目录的 `lifecycle_state = active | deleting`，与现有文档处理 `status` 分开，避免直接向旧 `status` CHECK 约束写入不支持的值。搜索、列表和新任务提交立即排除该对象及其后代，物理清理由后台任务分批完成。

### 不变量 F：任务不能覆盖更新的操作

所有文档变更都需要 mutation epoch/version；知识库及目录也要有持久化栅栏。任务必须在接收、开始执行和每次可见结果提交时检查目标及祖先的生命周期和预期版本。最终检查与写入在同一事务完成，不能先查版本、释放锁、再写结果。任务互斥是优化，持久化条件检查才是删除后不复活的保证。

### 不变量 G：前端后台更新不能直接破坏当前页面

后台任务只更新统一状态源和受影响的数据；不能直接清空并重建当前全局 DOM。

---

## 6. 详细改造设计

### 6.1 统一 Operation Service

建议新增内部模块：

```text
internal/operations/
  service.go
  repository.go
  state.go
  events.go
  recovery.go
```

Operation Service 接管现有 `internal/jobs` 的任务身份、持久化、取消和恢复；迁移期 `jobs.Manager` 只作为适配器，不再维护另一套队列和权威状态。已有模型自检、下载、目录任务的进度和查询能力必须保留。

任务状态：

```text
queued → running → succeeded / failed
   └→ cancelled
running → cancelling → cancelled / failed / succeeded
running / cancelling → interrupted
failed / interrupted → queued（允许重试时，attempt 增加）
```

任务记录及附属表至少包含以下信息，存储布局可在实现中细化，但不能省略语义：

| 分组 | 必要信息 |
|---|---|
| 身份与作用域 | `id`、`type`、`base_id`、`document_id`、`parent_operation_id`、授权主体/作用域引用 |
| 可重放命令 | `command_schema_version`、`command_payload` 或 `input_ref`、输入 Hash、固定的 source version、模型/解析配置快照或版本引用 |
| 冲突与幂等 | `idempotency_key`、`request_fingerprint`、目标及祖先 `expected_epochs`、已分配的文档/generation ID |
| 调度与执行 | `state`、单调递增 `state_revision`、`phase`、所需资源、`priority`、`attempt`、当前执行者/进程实例标识、`next_attempt_at` |
| 进度 | `completed_units`、`total_units`、`completed_bytes`、`total_bytes`；未知总量可为空 |
| 取消与错误 | `cancel_requested`、`retryable`、`error_code`、脱敏的 `error_message`、分项错误 |
| 结果与时间 | `result`/`result_ref`、分项提交标记、`requested_at`、`started_at`、`finished_at`、保留到期时间 |

命令输入必须足以在重启后重新构造执行器，例如 URL/标题、批量目标清单、目录根、冲突策略和上传会话引用。不得持久化闭包、临时内存地址或会过期的临时文件路径。配置快照保存语义参数及凭据引用，不把 API Key 复制进任务表；执行时重新校验授权和路径。输入缺失、命令版本不受支持或凭据不可用时给出明确错误，不能换用最新输入后声称恢复了原任务。

**接收与幂等：**

1. 先验证调用主体、任务记录的访问权限及幂等凭据真实性，再在接收事务内查询原键绑定和请求指纹。命中已有命令时返回原 Operation，不重新按新任务检查容量、目标生命周期或输入是否仍可绑定；因此删除已生效、队列后来变满时，原键仍能找回任务。目标已删除不等于任务审计记录无权访问，记录访问由接收时保存的主体/作用域和当前授权共同决定。
2. 仅对未绑定的新命令校验凭据有效期、容量、目标/祖先生命周期及版本，然后原子写入命令、幂等记录、稳定目标 ID、输入所有权引用；单项删除还在此事务写入 tombstone 和清理意图，批量删除的父命令仅绑定清单，子项按 §6.5 分批建立栅栏。唯一约束作用于“主体/作用域 + 操作类型 + 幂等键”。相同键、不同参数返回 `409 idempotency_conflict`。指纹包含目标、输入版本、冲突策略和语义配置；显式参数与服务端首次解析的配置版本均保存，重试使用首次解析结果，不能因全局默认配置后来变化把原请求误判为不同命令。
3. 提交成功后才返回接收响应。内存队列只发送唤醒信号；Dispatcher 按持久化的可执行记录补领任务，所以“提交后、入队前”崩溃不会丢任务。Worker 使用带状态/attempt 条件的领取，旧执行者不能写回新 attempt。
4. 响应丢失或提交结果不确定时，客户端保留并重用原幂等键查询/重试。服务端不能确认未提交时，不得建议生成新键。仅在确认未提交时才返回可安全重新接收的队列满/暂不可用错误。
5. 幂等保留期作为公开配置：任务未终结时不清理绑定；终结后，绑定/去重摘要的最早清理时间为 `max(凭据失效时间, finished_at + 客户端结果重试窗口)`。结果大对象可先到期，但仍须保留去重摘要、终态和结果过期标识，不能让仍有效的旧凭据重新成为未绑定新命令。终态过期返回明确的 `410 operation_expired`，不得让旧键静默变成新操作；采用 §7.1 的有界有效期服务端幂等凭据和保留期内的去重摘要共同判定，先查已绑定命令，再决定凭据是否还能接收新命令。

**业务提交与恢复：**

- 文档发布事务同时写入 active generation/source version、元数据和该 Operation 的分项结果/提交标记。对一个目标的业务效果提交最多一次；调度与计算允许至少一次执行。
- 单项操作可在发布事务同时进入 `succeeded`；父批量任务按分项标记汇总。若进程在业务提交后、父任务终态写入前退出，恢复器据分项结果补齐终态，不重新导入、重新命名或重新删除已完成项。
- 外部下载、文件移动等使用稳定目标路径、校验和及持久化步骤记录；恢复先检查效果是否已经存在，再执行幂等补偿。不能宣称数据库事务能原子覆盖文件系统或远端服务。
- `result` 至少提供产生/影响的资源 ID、成功/失败/跳过数量和分项错误。批量部分失败进入 `failed` 且 `result.partial = true`，已成功项保留；重试只处理未提交项。用户取消批量任务时也保留部分结果，停止尚未提交的子任务；已提交删除意图的子项继续清理并在结果中单列。
- 重启先校验执行者身份和提交标记，将没有活 Worker 的记录标为 `interrupted`。安全可重放且输入完备的任务按有界退避自动重试；其余保留结果并等待显式重试或报不可重试错误。不能仅凭 `running` 状态推断业务尚未提交。

**取消与重试：** 未越过不可取消边界的 `queued` 可直接取消；运行中的可取消阶段先持久化意图，再通知 Worker，清理完成后才标终态。取消与发布竞争由同一提交事务裁决：发布先提交则保留成功，取消先提交则拒绝发布并清理未发布 generation。重复取消和重复重试不产生并行 attempt。`cancelled` 不自动恢复；用户重新发起操作使用新键。删除的不可取消边界见 §6.5。

`cancel_requested` 一经提交，在重启恢复时优先于自动/显式重试：无已提交业务效果的任务只继续清理未发布资源并进入 `cancelled`，不得走 `interrupted → queued` 再执行原业务；已有提交标记的分项保留真实结果。只有 `cancel_requested = false` 且没有旧执行者的可重试失败/中断任务才可重新领取。已提交删除的清理责任是明确例外，不接受取消，不因停止父批量任务而撤销。

### 6.2 资源 Scheduler 与背压

将当前“普通/IO”二分队列升级为资源调度器：

| 资源 | 典型操作 | 初始策略 |
|---|---|---:|
| `db_write` | 文档、Chunk、FTS、向量写入 | 1 个令牌 |
| `disk_read` | 目录扫描、读取 raw | 1～2 个令牌 |
| `parser_cpu` | PDF/XLSX/Word 解析 | 按 CPU 配置 |
| `embedding` | Embedding 推理 | 本地模型默认 1 |
| `rerank` | Rerank 推理 | 默认 1～2 |
| `network` | URL 和模型下载 | 限速、限并发 |
| `memory_bytes` | 解析中间文本、待写 Chunk、向量、检索读视图 | 按字节预算接收和分批；不能只限制任务个数 |
| `disk_bytes` | staging、新旧索引、quarantine、模型缓存 | 配额、剩余空间水位和预留空间 |
| `maintenance` | optimize、VACUUM | 最低优先级 |

调度规则：

- 交互查询和任务提交优先于维护任务。
- 同一文档同时只能有一个互斥写任务。
- 同一知识库设置并发上限，避免一个库独占资源。
- 长任务必须分阶段、可让出资源。
- 队列超过上限时返回明确的排队结果或 `429`，不能无限堆积。
- 重复的重新索引任务合并。
- 删除操作优先级高于未开始的重建索引。
- FTS optimize、VACUUM 不得抢占正常导入的写资源。

接收配额以持久化的未完成任务数量、输入字节及预计临时空间计费，父子任务都要计入；拒绝前不得留下无人负责的 staging 或已标删除的对象。上传会话也有独立字节/数量/存活时间配额。参数在 P0 后写入配置及验收报告，不得以无限值作为默认值。

每阶段声明资源需求，由 Scheduler 统一准入；阶段完成即释放。禁止持有 Writer 等待模型、持有父任务资源等待子任务完成，或由各 Worker 自行按不同顺序获取多个令牌。控制写入优先，索引写入按库公平分批；在线查询与批量模型计算分配独立等待预算，并在批次边界让出模型资源。最高优先级也不能抢占已开始的 SQLite 事务，因此短事务是调度的前提。

### 6.3 上传、导入和索引分层

目标流程：

```text
创建上传会话
  → 文件写入 staging
  → 校验大小、Hash、路径
  → 持久化上传完成状态
  → 创建 import Operation，并绑定输入所有权
  → 解析
  → 分块
  → 生成 Embedding
  → 写入新 generation
  → 校验
  → 原子切换 active generation
```

较大文件应逐步使用 multipart 或分块上传，避免 Base64 JSON 同时造成内存放大和长 HTTP 请求。

即使兼容期接收 Base64 JSON，也必须限制请求体、解码后总字节、文件数和并发解码预算；不得把整批 `[]byte` 长期留在内存任务队列。文本和 URL 命令同样要持久化输入，URL 下载后的内容转为不可变 source version，后续重试索引不得重新抓取成另一个版本。

上传会话使用 `uploading → complete → bound → released/expired` 状态。未 complete 的会话不能创建导入任务；接收事务原子从 complete 绑定到命令。拒绝接收时保持会话可重试，取消或失败后的释放由有界清理任务完成。绑定输入保留到任务终态及重试窗口结束，启动清理器不得只按文件年龄删除活任务输入。

raw 的最终路径必须包含不可变 source version，采用同卷临时写入、校验、受支持的持久化/重命名流程；替换文件、URL 刷新、目录扫描发现变化也走新版本构建，禁止先删除旧文档再导入。发布事务更新 source 引用及索引；发布前失败保持旧 source 和索引配对。不同平台的文件落盘保证要以崩溃测试验证并记录边界。

### 6.4 版本化索引

文档至少需要支持以下字段：

```text
source_version
active_index_generation
desired_index_generation
index_state
mutation_epoch
```

Chunk、FTS 和向量记录需要带：

```text
index_generation
embedding_provider
embedding_model
embedding_dimension
indexed_at
```

实现可将模型信息集中在 `document_index_generations`，Chunk/FTS/向量通过 `(document_id, index_generation)` 引用；模型身份包含实际 provider/model/revision、维度及影响向量空间的配置指纹。generation 记录保存 `building/validated/active/retired/aborted`、source version、Chunk 数和向量就绪状态。

新 Chunk ID 必须包含 generation 或使用全局唯一 ID，不能继续让同文档不同 generation 共用当前的 `docID:index` 主键。所有 Chunk、向量更新、缓存复用物化、邻居查询和删除谓词都须携带 generation。向量缓存可按内容 Hash 与完整模型身份复用，但复用结果必须落到目标 generation。

重新索引流程：

1. 读取固定版本的 raw 文件。
2. 创建新的 generation。
3. 解析和写入新 Chunk。
4. 生成并写入 Embedding/Vector。
5. 校验 Chunk 数量、FTS 可见性、向量覆盖率、维度、模型和 source 版本；取消或失败时只清理未发布版本。
6. 在统一 Writer 的短事务内复核目标/祖先 epoch、取消意图和 attempt，然后同时切换 active generation、active source、计数、模型就绪状态及 Operation 提交结果。
7. 将旧 generation 标为 retired，登记持久化 GC 意图；确认没有读引用后分批清理。

这样可以准确回答“文档是否使用了本地 Embedding 模型”，也可以避免重建索引失败后旧索引消失。

**读取和模型切换：**

- 单进程阶段使用统一的读视图注册器。搜索在短读快照中确定作用域内固定的 `(doc_id, generation, source_version, model_profile)` 并注册读引用；与发布/GC 的协调必须保证选中的 generation 不会在注册前被回收。关闭读事务后再进行模型调用，不跨网络等待持有 SQLite 事务。
- 新读视图只能选择建立时的 active generation；已注册的视图继续读取其固定版本，即使随后发布新版本使它变为 retired。词法/向量召回、排序、邻居拼接、原文及证据引用均按视图中的 generation 集合过滤，不能再按查询执行时的 active 状态过滤。分页或后续详情链接携带 generation/source version，过期返回明确错误或提示重新检索，不能无提示替换成最新内容。相邻 Chunk 查询必须按 generation 分组，不能只按文档 ID 和 idx 拼接。
- 请求 deadline、作用域大小、视图内引用数及内存均有上限。实际读取停止后释放引用；仅 deadline 到期而 Worker 仍在读时，GC 不能假定引用已释放。进程退出后的引用由实例锁和进程存活边界判定失效；P7 多进程必须先替换成跨进程租约协议。
- 删除提交后，正在执行的查询在返回结果前重新校验目标和祖先 lifecycle，将已删除对象及其证据全部剔除。读取完成的线性化点在此校验；已发送给客户端的历史结果不追溯撤回。
- 查询 Embedding 使用读视图固定的 generation/model_profile 对应的模型空间，不能在模型调用前重新读取实时 active 配置。配置从 A 改为 B 后，B 只成为 desired 配置；新 generation 发布前保留 A 的查询能力。混合迁移按模型分组检索，禁止将 B 的查询向量用于 A 的存量向量。旧模型不可用时按请求模式报告资源错误或明确降级，并保留可读的旧词法索引。
- 首次导入允许按既有策略以 lexical-only、附带降级信息发布；已有可用向量索引的重建默认不以 Embedding 失败的版本替换它。显式要求禁用向量的配置变更可发布词法版本，必须在命令中记录该意图。

**迁移与可见性：** 现有 Chunk 原地补为 generation 0，历史 Chunk ID 保持可解析；在所有 Web/Agent 读取、统计、恢复计数和写入路径都识别 generation/lifecycle 前，不允许写入第二个 generation 或启用 tombstone。FTS 候选 LIMIT 前必须按固定读视图过滤：未发布或未被该视图选中的版本不得入选；该视图已固定、后来 retired 的版本仍可召回。保留触发器时还需检查共用 FTS 语料统计对 BM25 排名的影响，不能只以“查不到 staging 行”验收一致性；实现方案及排序容差须有固定检索样本证明。

### 6.5 删除、目录删除和清理恢复

删除流程：

```text
接收事务：目标 lifecycle_state = deleting，递增 mutation_epoch
  + 写入删除 Operation / 幂等记录 / 清理意图
  → 列表/搜索排除目标及后代
  → 拒绝其下新写入；阻止旧任务发布
  → 分批删除 Chunk/FTS/Vector
  → raw 文件移动到 quarantine
  → 确认清理完成
  → 保留必要的 tombstone/结果/去重记录，回收其余元数据
```

数据库和文件系统不能使用同一个事务，因此必须使用 staging/quarantine 和恢复扫描来实现最终一致性。

知识库删除在 base 上建立栅栏；目录删除在该目录上建立祖先栅栏，无需在接收事务枚举全库逐个标记。所有子项创建和更新，包括扫描新发现文件、上传、重命名/移动、URL 刷新、恢复及 GC，都必须检查祖先链。稳定 UUID 不复用；同一路径重新创建是显式的新对象，旧任务的祖先引用不能自动改绑。

普通业务发布要求目标和祖先均为 active；删除清理器则凭已提交删除意图、匹配的 tombstone epoch 和所有权执行，只能清理意图覆盖的资源。清理器不按普通导入的 active 条件拒绝自身，也不能绕过 epoch 去清理后来创建的新对象。retired generation 的 GC 同样必须复核非 active、未被引用和清理意图身份。

| 并发组合 | 裁决规则 |
|---|---|
| 同文档重复重建 | 相同输入及配置版本合并；不同 desired 版本递增 epoch，旧任务不得发布 |
| 文档重建与删除 | 删除事务建立栅栏后拒绝任何旧任务发布；已发布结果再由删除任务清理 |
| 目录/知识库删除与扫描、导入 | 提交及每次子项写入检查祖先；即使子文档 ID 是扫描中新建的也必须拒绝 |
| 修改父目录、移动对象与删除 | 更新祖先关系须在事务中检查源和目标祖先链及 epoch；不能借移动逃逸已提交的删除范围 |
| 删除后旧任务恢复 | 缺失祖先或版本不符按冲突终止，绝不重新创建被删除的目标 |

删除的逻辑效果在接收事务提交时生效，因此单项删除在返回 `202` 时已经越过不可取消点。此后取消返回 `409 deletion_committed`，后台清理必须持续重试至完成或以可观察错误保留清理责任，不能因用户取消将对象恢复为 active。批量删除按子项逐个提交 tombstone，父任务可取消尚未提交的子项；已提交子项继续清理，父结果列出它们的 Operation ID 和状态。UI 必须在提交前说明此取消边界。

清理任务逐批记录进度，移动 raw 前登记原路径、隔离路径、source version 和校验和；移动后更新步骤标记，重启可根据两处文件状态幂等续做。缺失文件不意味着整个删除失败，权限/占用错误不得丢弃清理意图。读引用未释放的 generation/source 只保持逻辑隐藏，隔离移动和物理删除都必须等待引用释放，不能使已持有视图的读者在打开固定 raw 路径时找不到文件。

进程启动时扫描：

- `deleting` 文档。
- staging 中未提交的文件。
- quarantine 中未完成清理的文件。
- 没有元数据的孤立文件。
- 有元数据但 raw 文件丢失的记录。
- 未完成的 index generation。

恢复先按 Operation、source/generation 清单及上传所有权确认引用，再启动分批扫描。活任务的 staging、building generation 和当前读引用不属于孤儿；使用实例/epoch 检查防止扫描快照落后于新写入。旧 `ReconcileStorage` 的“未在文档 raw 路径中出现就删除”和“按全部 Chunk 修正计数”逻辑必须在启用新存储格式前替换。

目录删除不能重复为每个子目录加载全库元数据，应改为父目录范围查询、批次处理和统一的父 Operation。

### 6.6 SQLite、FTS 和维护任务

SQLite WAL 和单 Writer 继续保留，但要增加：

- Chunk/FTS/Vector 批量写入上限。
- 写事务耗时指标。
- WAL 大小和 checkpoint 指标。
- FTS 批量操作基准。
- 维护任务低优先级调度。
- 正常负载期间禁止自动长时间 VACUUM。

**任务日志与 Writer 的选择：** 第一版 Operation、业务元数据和发布标记放在同一个 SQLite 数据库，保持命令接收和业务提交的事务边界；不新增第二个数据库或异步落盘确认。P1a 必须先把所有可达的大写操作改为可让出的有界批次，全部写入口接入 Writer 后，P1b 才启用持久化接收。

任务提交设置有界的 Writer 排队和事务 deadline。写队列没有余量时尽早拒绝，确认未提交的请求返回 `429 queue_full` 或 `503 storage_unavailable`；事务可能已提交时返回 `submission_unknown` 并要求使用原键查询/重试。客户端网络超时同样按提交结果未知处理，不能转为新键重复提交。资源恢复后 Dispatcher 从数据库续领，不能依靠请求 goroutine 存活。

批次按行数和估算字节双重限制，结合事务耗时反馈缩小批次；不以“每 256 行检查取消”冒充每 256 行提交事务。控制写队列长度、超时、拒绝率和提交 p95 一并验收。现有 `TestSubmitDoesNotWaitForSQLiteWriter` 的非阻塞目标应转化为“已接收必持久化 + 写繁忙下有界响应”的测试；既不能沿用内存成功，也不能删掉延迟验证。

FTS5 触发器带来写放大。不能简单删除触发器来换取速度；应验证以下候选方案并保留一致性测试：

1. 受控批量写入。
2. staging FTS generation 完成后切换。
3. 特定批量操作结束后重建 FTS。
4. 对大库采用独立的索引维护窗口。

候选方案必须以搜索结果一致性、崩溃恢复和响应延迟为共同验收条件。

### 6.7 读取路径

文档和目录读取改为：

- 按 `base_id + parent_id` 查询子节点。
- 目录树懒加载。
- 文件列表分页或游标分页。
- 列表接口只返回元数据摘要。
- 原文、Chunk 和向量使用单独详情接口。
- 统计使用增量计数或短 TTL 缓存。
- 新搜索固定视图建立时的 active generation，后续读取保持该视图，允许继续读其后来 retired 的版本。

generation、lifecycle、固定读视图及候选 LIMIT 前过滤是 P1a 的正确性前置项，覆盖词法/向量/短词查询、文档上下文、raw 预览、统计、搜索历史回放和 Agent 工具。P4 仅负责分页、懒加载和统计等性能改造，不能到 P4 才补正确性过滤。带历史引用的详情请求也必须校验对象/祖先生命周期；被删除资源返回不可用，不因为历史版本仍待 GC 而继续暴露。

向量检索暂时可以保留暴力扫描，但必须先通过基准证明是否为瓶颈；只有规模和 p95 延迟不满足目标时才引入 ANN。

### 6.8 前端 OperationStore

P1b 同步交付可工作的统一前端任务状态层，P5 再完成页面级整理：

```text
OperationStore
  ├─ active operations
  ├─ queue position
  ├─ phase
  ├─ progress
  ├─ error
  ├─ result / partial results
  ├─ state revision
  └─ affected resources
```

前端规则：

- 只保留一个任务轮询器，或使用 SSE 并以轮询作为降级方案。
- 任务状态跨页面保留，刷新后从服务端恢复。
- 后台完成任务时只刷新受影响的数据源。
- 每个路由请求支持 AbortController。
- 页面切换后旧请求不得提交结果。
- 页面加载失败显示错误面板和重试按钮，不能留下标题空壳。
- 任务进行中禁用重复的破坏性按钮。
- 长任务状态显示在任务面板，不依赖 Toast。
- 不确定总量时显示阶段型不确定进度，不伪造固定百分比。
- `202` 只显示“已接收/排队”，只有 `succeeded` 才显示完成；失败、取消及部分结果始终可查看。表单输入在提交结果未知时保留，重试沿用同一个幂等键。
- SSE/轮询状态按 `state_revision` 合并，旧响应不能让终态倒退；重试使 revision 增加并展示 attempt。SSE 是状态通知，权威结果仍可通过查询接口重建。
- 任务取消按钮由服务端 `cancellable`/提交边界控制，已提交删除显示后台清理状态；不能把 UI 隐藏当作任务取消成功。

现有 `routeGeneration` 应保留作为保护措施，但不能代替统一状态管理。

### 6.9 Agent 与进程生命周期

启动阶段拆成：

```text
process lock / 数据格式兼容检查
  → 必要数据库迁移与最小恢复核对
  → 监听端口
  → Agent 协议注册
  → Control Plane 可查询状态（starting/degraded）
  → 栅栏和执行者核对完成 / Scheduler ready / 允许提交任务
  → 分批后台恢复与孤儿扫描
  → 低优先级维护
```

健康状态建议为：

```text
starting / degraded / ready / stopping
```

Agent 初始化不能等待大型文档恢复、模型扫描或 VACUUM 完成。

最小恢复只核对格式、命令提交标记、残留执行者及已提交栅栏，不递归遍历 raw 目录。若它尚未完成，健康接口报告原因，查询/取消已有任务按存储就绪度开放，新写命令返回 `503 runtime_not_ready`；不能先运行新任务，再由旧恢复逻辑批量将它们标为中断。首次大规模存储格式迁移单独安排维护窗口，不伪装成后台就绪。

进程管理必须使用当前实例拥有的 PID/进程树边界；不能按 `sta.exe` 进程名全局清理。退出时：

1. 停止接收新任务。
2. 持久化停止意图，取消可取消 Worker 的上下文，停止 Dispatcher 领取；未完成的删除保留清理意图。
3. 等待有限时间让 Worker 退出并释放读引用，不能提前写入 `cancelled` 假装已停止。
4. Worker 退出后关闭存储和监听器；超时按当前实例所有权终止进程树，持久化记录交给重启恢复器核对，不继续让 Worker 使用已关闭数据库。
5. 重启后依据提交标记、attempt 和取消意图恢复；普通停机中断不等同于用户取消，已取消任务不自动复活。

---

## 7. API 迁移策略

### 7.1 新接口

```http
POST /api/operation-keys
POST /api/uploads
PUT  /api/uploads/{id}/content
POST /api/uploads/{id}/complete
POST /api/operations
GET  /api/operations/{id}
GET  /api/operations
POST /api/operations/{id}/cancel
POST /api/operations/{id}/retry
GET  /api/operations/{id}/events
```

以上是目标协议与验收契约。截至 v1.5，Operation key、upload、operation 查询/事件/取消/重试、聚合上传/临时 staging 配额、operation 级资源预算及部分旧接口适配已有初步实现；仍必须按 P1B-01～P1B-05 补齐字段语义、模型/输入/结果资源所有权、剩余聚合长任务的故障语义和 release-host 验证。删除的 committed marker 现在只作为逻辑栅栏边界，重放会继续有界 raw/chunk 清理；目录操作的失败 marker 写入错误也会显式失败。旧 `jobs.Manager` 已从生产 Knowledge Service 依赖中移除，Web/Agent 旧 `jobId` 仅保留 Durable Operation 兼容映射。`GET /api/status` 增加 `operationsV1`、上传能力、结果协议版本及输入限制。列表支持状态、作用域、父任务过滤和游标分页，禁止全量返回历史任务。

新写命令通过 `POST /api/operations` 接收，首次成功返回 `202 Accepted`，响应带 `Location`、`Retry-After` 和 Operation ID。幂等重复命中仍在执行的任务返回 `202`，终态返回 `200` 和原结果。任务状态查询返回 `200`；上传、查询、取消、重试是各自的协议，不套用“所有接口都返回 202”。

客户端先通过 `POST /api/operation-keys` 获得绑定主体/作用域/类型及有效期的服务端幂等凭据，作为 `Idempotency-Key`。凭据校验密钥随实例数据持久化；轮换后的旧校验密钥至少保留到相关凭据失效且其全部绑定/去重摘要已按 §6.1 到期清理，重启不使有效凭据或仍保留的绑定不可查询。发放接口限流并返回截止时间。已绑定任务在运行期间仍可查回；原绑定和去重摘要在保留期内存在，已过期且记录已清理的凭据只能返回 `410`，不能重新接收。取消/重试不创建新命令键，使用 Operation ID 和预期 revision 做条件状态转换；重复相同转换返回当前快照，不新增 attempt。

文本导入请求示例（文件导入使用 `uploadId`，批量删除使用固定目标清单）：

```json
{
  "type": "import_text",
  "commandSchemaVersion": 1,
  "target": { "baseId": "base-a" },
  "input": { "title": "示例", "content": "待导入文本" }
}
```

接收响应沿用当前 API 的外层 envelope，明确区分接收与完成：

```json
{
  "ok": true,
  "value": {
    "operationId": "op-a",
    "state": "queued",
    "stateRevision": 1,
    "submitted": true,
    "cancellable": true,
    "statusUrl": "/api/operations/op-a"
  }
}
```

终态查询示例：

```json
{
  "ok": true,
  "value": {
    "operationId": "op-a",
    "state": "succeeded",
    "stateRevision": 8,
    "attempt": 1,
    "cancellable": false,
    "result": {
      "documents": [{ "id": "doc-a", "title": "示例", "chunkCount": 12 }],
      "succeeded": 1,
      "failed": 0,
      "skipped": 0,
      "partial": false
    }
  }
}
```

任务执行失败仍能以 `200` 查询到 `state = failed`、类型化错误及部分结果；这与 HTTP 请求本身失败不同。所有引用 URL 必须经前端已有的 Agent 代理前缀适配访问，SSE 同样验证 standalone 和 Agent 代理模式。SSE 支持 revision/event ID 续传；超出事件保留窗口时要求重新读取状态快照，不能遗漏终态。

### 7.2 兼容旧接口

异步接收无法保持旧同步接口“返回即完成”的语义，不能将 Operation 对象冒充文档或 `{deleted: true}`。迁移按以下矩阵执行：

| 调用方 | 切换规则 |
|---|---|
| 当前文本/文件/URL 导入及同步删除 Web 调用 | P1b 内改用 Operation 接收和终态跟踪，保留最终文档/数量/冲突结果展示 |
| 旧同步 HTTP 写接口 | 启用前保持原版本行为；启用后仅接受显式 `X-Knowledge-Operation-Version: 1` 的适配请求并返回新协议。未声明的旧客户端收到 `409 client_upgrade_required`，不执行写入 |
| 已返回 `jobId` 的目录、重建、自检、下载接口 | 保留 `jobId` 响应别名；提交客户端同批升级并携带 §7.1 幂等凭据。未携带凭据的旧提交收到 `409 client_upgrade_required`，适配器不得为每次重试另发新键；查询/取消旧路径继续兼容 |
| `/api/jobs/{id}` 与取消接口 | 适配新权威记录；queued→pending，running/cancelling→running，succeeded→done，failed/interrupted→failed，cancelled→cancelled，保留 phase/字节进度和错误。取消仍执行新的提交边界 |
| Agent 写工具 | 同次更新 Knowledge 扩展工具描述和结果契约，返回 `operationId`、`submitted`、状态及结果查询方式；完成的文档信息由终态结果取得 |
| Agent 读工具和上下文回调 | 保持同步检索/证据结果，不返回 Operation ID |

P1b 新增 Knowledge 扩展工具 `knowledge_create_operation_key`、`knowledge_get_operation`、`knowledge_list_operations`、`knowledge_cancel_operation`、`knowledge_retry_operation`，均复用相同的主体/知识库授权检查。写工具声明必填 `idempotencyKey`，调用方先领取凭据并在同一意图的重试中复用；工具适配器不得在重试时暗中换键。当前扩展 SDK/宿主加载工具定义的兼容性必须验证；不能假定宿主会轮询新结果，也不以修改 Agent 宿主作为本方案的默认前提。宿主不支持该工具契约时，阻止对应写工具切换并报告版本不兼容。

旧持久化 jobs 保留 ID、进度、终态和查询兼容。没有可重放输入的历史 pending/running 行标记为明确的 `interrupted/legacy_command_unavailable`，不得从进度 payload 猜测原命令。单进程切换须停收旧任务并完成/核对在途工作后再接管队列。

新后端、Web 构建产物、扩展 Manifest/工具定义和客户端协议测试作为一个发布单元。P1b 上线门禁包括后台实际失败时 Web/Agent 均不会误报成功、旧浏览器缓存能识别升级要求，以及端到端查询/取消/重试可达。P5 不是首次适配异步响应的时间点。兼容窗口的起止发布版本、最低客户端版本和旧路径弃用日志必须在启用前写入发布说明。

### 7.3 错误契约

| 场景 | HTTP / 错误码 | 客户端行为 |
|---|---|---|
| 参数/输入体积错误 | `400 invalid_command` / `413 input_too_large` | 修正输入，不自动重试 |
| 权限/路径拒绝 | `403 access_denied` / `path_denied` | 修正权限或路径，不泄露敏感路径详情 |
| 队列/配额已满且未接收 | `429 queue_full` / `quota_exceeded`，带 Retry-After | 原键退避重试，不显示已接收 |
| 同键不同请求、版本冲突、删除已提交 | `409 idempotency_conflict` / `mutation_conflict` / `deletion_committed` | 展示具体冲突；不能无限重试 |
| 老客户端 | `409 client_upgrade_required` | 提示升级/刷新，禁止冒充提交成功 |
| 存储/模型暂不可用、启动未就绪 | `503 storage_unavailable` / `model_unavailable` / `runtime_not_ready` | 展示原因及退避，任务接收与执行状态分别处理 |
| 提交结果不确定 | `503 submission_unknown` 或客户端连接超时 | 保留输入和原键，查原任务或用原键重试 |
| 有界同步检索超时 | `504 query_timeout` | 显示超时或已定义的显式降级；不转成长任务 |
| 任务/历史版本过期 | `410 operation_expired` / `generation_expired` | 明确过期，要求用户重新操作或重新检索 |

Operation 执行错误另含 `retryable`、阶段、分项结果和下一步建议。自动重试次数、退避上限和权限/配置重新验证规则必须有限；不可重试错误不得循环占据队列。

前端根据错误类型显示“重试、排队、取消冲突任务或检查配置”，不能统一显示“请求失败”。

---

## 8. 详细实施工作计划

本节是 v1.5 的执行主表。任务按编号提交、评审和验收；没有通过前置门禁时，可以并行编写后续代码或测试，但不得启用后续生产写路径。

### 8.0 执行规则与依赖顺序

阶段依赖固定为：

```text
P0 基线/观测
  -> P1A-01 兼容格式与备份
  -> P1A-02 Storage Writer
  -> P1A-03 全写路径迁移
  -> P1A-04 短事务与有界批次
  -> P1A-05 generation/删除/固定读视图全路径审计
  -> P1A-06 旧任务交接
  -> P1a 门禁
  -> P1B-01～P1B-07 Durable Operation 与调用方迁移
  -> P2 资源配额/公平调度
  -> P3 规模化维护/故障/回滚
  -> P4/P5 读路径与前端压力验收
  -> P6 release-host 生命周期验收
  -> 是否需要 P7 进程拆分
```

每个任务必须同时产出以下五类结果：

1. 代码或配置变更，以及不改变的兼容行为。
2. 针对成功、拒绝、错误、超时、取消、重启的测试。
3. 资源指标和有效吞吐，不能只报告成功请求的 p95。
4. 失败后的数据状态、输入所有权和恢复责任。
5. 回滚目标版本/SHA、格式边界、备份或隔离恢复方式。

任务状态只能取 `未开始`、`开发中`、`本地通过`、`release-host 待验`、`SmartCare 待验`、`门禁通过`。`本地通过`不等于完成；C01-C16 需要真实数据库、真实任务循环和可控故障点，不能用纯状态单元测试替代。

### 8.1 P0：建立基线和观测（当前：未闭合）

P0 的目标是先知道当前系统的容量、瓶颈和失败边界，冻结后续比较所需的输入、配置和环境。P0 报告完成前不得以新的并发参数、队列拒绝或缓存命中率替代基线。

| 编号 | 工作项 | 实施内容与涉及范围 | 交付与验收 |
|---|---|---|---|
| P0-01 | 细分耗时与结果指标 | 在 `internal/operations`、`internal/knowledge`、`internal/storage`、`internal/web` 统一记录 `queue_wait_ms`、`run_time_ms`、`parse_time_ms`、`model_time_ms`、`disk_read_ms`、`db_wait_ms`、`db_transaction_ms`、`fts_time_ms`、`vector_time_ms`；Operation 状态已暴露由请求/领取/终态时间派生的排队与运行耗时，生命周期和去抖后的阶段变化写入 `operation_events`，`OperationMetrics` 按类型聚合状态、拒绝、超时和耗时，Writer 统计已按 control/normal/maintenance 保留兼容聚合字段并提供分项快照；同时记录成功/拒绝/错误/超时/取消、有效吞吐、WAL、RSS、临时空间和收敛时间。计时使用单调时钟，状态 API 只返回聚合值；`cmd/architecture-scenarios` 的 Operation 记录现在保留有限的有序 `eventTrace`（ID、revision、时间、相邻事件间隔、phase/进度），不保存 payload。 | 每类操作至少有一条可追踪 trace；数据库事务、模型等待和磁盘读取不再全部归入一个总耗时；指标不会泄露原文、Token 或凭据。 |
| P0-02 | 冻结双库数据集 | 准备 SmartCare 产品数据字典库和 SmartCare Suite 库的隔离副本；使用只读的 `cmd/architecture-baseline` 生成输入文件清单、SHA-256、文件/文档/Chunk 数、目录深度、文本分布、模型身份/维度、配置和 storage format 清单。工具只输出元数据并对配置凭据脱敏。 | `baseline/` 报告可由同一 SHA、同一数据目录和同一配置重复生成，并带稳定 `reportFingerprint`；原始用户数据不在仓库中。真实 SmartCare 数据尚待现场输入。 |
| P0-03 | 固定基线场景 | 使用 `cmd/architecture-scenarios` 运行单库导入/重建、双库并行重建、跨库导入+删除、持续检索和页面分页切换；分别记录冷启动、热启动、缓存命中和模型等待。重启恢复、写锁竞争和磁盘临界由 release-host 控制器执行。每个终态 Operation 还保留有序、脱敏的生命周期/阶段 `eventTrace`。 | 每个场景同时报告请求量、拒绝量、失败量、超时量、有效吞吐、p50/p95/p99、RSS 峰值、临时空间峰值和 WAL 增长；每个 Operation 可由 eventTrace 关联阶段顺序与时间；runner 对未具备 OS 控制的场景明确输出 `unrun`，不能伪造通过。 |
| P0-04 | 冻结资源预算 | 将队列容量、每库并发、Writer 批次、事务 deadline、查询 deadline、模型等待、上传/内存/临时磁盘/结果保留量映射到配置；记录机器 CPU、内存、磁盘和文件系统。 | 产生一张“配置 → 资源预算 → 验收阈值”表；不允许通过把所有请求拒绝来制造低延迟。 |
| P0-05 | 固定检索回归集 | `cmd/retrieval-regression` 已提供独立的固定语料执行器：输入文件与报告分离，按 case 固定 query/base/mode/topK/MMR/正负样本，并以 `debug=true` 采集命中 ID、排序、generation、source version、分数、rerank provider/model/status、分阶段诊断候选；同时按首个命中验证 generation/source pinned 的 context 与 raw citation。报告只保留 query SHA-256 和元数据，不落盘 query、命中文本、上下文、raw bytes 或 reranker 错误消息。 | 本地契约测试已覆盖 Hit@1/Hit@3/MRR、负样本失败、版本/诊断字段和内容不泄露；仍需用 SmartCare 产品字典/Suite 固定 ID 生成 `retrieval-corpus.json`，在 P1a/P2/P3 每次运行并比较召回、排序、证据版本和失败闭合，不接受只比较 HTTP 200。 |
| P0-G0 | P0 门禁 | 由开发者和评审者共同确认数据集、SHA、配置、报告和脚本齐全。 | 门禁通过后才能冻结并发/批次参数，进入 P1a 的安全写入改造；否则只允许补基线。 |

**P0 当前差距：**代码已增加导入、检索、磁盘读取、DB 等阶段字段，Operation 已提供生命周期/阶段事件及按类型结果/拒绝/超时/排队与运行耗时聚合，并报告 Writer 聚合及 control/normal/maintenance 分项耗时、RSS/WAL/temp/upload footprint；`cmd/architecture-baseline` 已提供可重复的只读数据/配置/SQLite 元数据收集和稳定指纹，`cmd/architecture-scenarios` 已提供 API 场景执行、操作轮询、请求分位数和脱敏状态/指标报告，`cmd/retrieval-regression` 已提供固定检索集的版本/排序/诊断/负样本及 context/raw citation 元数据报告，但真实 SmartCare 双库输入、固定检索 case 的现场 ID、冷/热启动控制、重启/文件锁/OS ENOSPC 注入，以及阶段指标与每类操作报告在规模负载下的稳定关联仍未闭合。

P0-02 的收集命令约定如下；必须在应用停止或接受一致性快照后执行，报告输出目录不得放回待扫描的数据目录：

```powershell
go run ./cmd/architecture-baseline `
  -dataset product-dictionary=D:\baseline\smartcare-product `
  -dataset suite=D:\baseline\smartcare-suite `
  -database product-dictionary=D:\baseline\smartcare-product\knowledge.db `
  -database suite=D:\baseline\smartcare-suite\knowledge.db `
  -config config.yaml `
  -output baseline\YYYY-MM-DD\input-inventory.json
```

P0-04 的预算对账以同一份配置快照为唯一输入，不在压测命令中临时改变
并发或配额。第一版报告至少按下表输出“配置 → 运行时观测 → 验收判断”；
其中 `unrun`、缺失观测或只得到拒绝而没有有效请求的行都不能判定为通过：

| 配置/预算 | 运行时观测 | 验收判断 |
|---|---|---|
| `scheduler.queueLimit`、`maxPerBase`、各资源 lane | `queuedTotal`、每库 active/queued、等待 p95、`queue_full`/`quota_exceeded` 拒绝数、每库有效吞吐 | 队列有界；任一库不能独占全部执行槽；存在成功样本，不能靠全量拒绝制造低延迟 |
| `scheduler.memoryBytes`、operation memory reservation | reserved/active memory 与 RSS peak | reservation 不超过预算；RSS 峰值、模型等待和检索结果保留量可解释并能与操作类型关联 |
| `scheduler.diskBytes`、`diskLowWaterBytes` | operation disk reservation、数据库/WAL/raw/quarantine footprint、free bytes | reservation 不超过上限；低于水位只拒绝新输入/低优先级工作，active 数据仍可读 |
| `scheduler.tempBytes`、`uploadBytes` | staging/temp/upload peak、释放时间、孤立文件数 | 峰值和绑定输入均在预算内；终态后占用收敛，无孤立 staging |
| `modelWaitMs`、model lane | model wait p50/p95、model error/degraded、查询 p95/p99 | 慢模型只造成有界等待或明确降级，不阻塞控制面，也不混用向量空间 |
| `retrieval.searchTimeoutMs`、`contextTimeoutMs`、TopK/候选上限 | search/context 成功、429、408/504、实际候选数、有效吞吐 | 超时/资源不足有明确错误；固定检索集的召回、排序和版本字段仍可对比 |
| Writer batch/transaction deadline | `dbWaitMs`、`dbTransactionMs`、批次数、WAL 增长、提交 p95 | 每批可让出且事务有界；控制写优先；阶段时间不能全部折叠成总耗时 |

对账产物由 `input-inventory.json`、`api-scenarios.json`、
`retrieval-regression.json` 和一张人工审核的预算表组成；表中必须引用
配置 SHA、应用版本/SHA、数据集 SHA、运行时间窗和报告 fingerprint。
只有 SmartCare 双库在冷/热启动、成功/拒绝混合负载下产生这些字段后，
P0-04/P0-G0 才能从“工具已具备”提升为“门禁通过”。

P0-03 的 API 场景报告使用运行中的隔离实例；`-base-id-2` 用于双库场景，
跨库删除必须显式提供第二库已有文档的 `-delete-document-id`，报告输出仍须
放在数据目录之外：

```powershell
go run ./cmd/architecture-scenarios `
  -base-url http://127.0.0.1:8765 `
  -base-id <product-base-id> `
  -base-id-2 <suite-base-id> `
  -delete-document-id <suite-document-id> `
  -scenario status,single-import,single-reindex,dual-reindex,continuous-search,page-switch `
  -duration 5m -concurrency 4 `
  -output baseline\YYYY-MM-DD\api-scenarios.json
```

该 runner 不伪造重启、写锁或 OS 级磁盘故障证据；这些场景会在报告中标记为
`unrun`，必须由 release-host 控制器执行并附加进程树、锁、磁盘和恢复快照。

每次报告必须保存 `reportFingerprint`、输入路径、配置 SHA、storage format 和运行机器信息；路径不可用、数据库不可读或任一数据集缺失时，命令必须失败，不能用空目录替代现场数据。

P0-05 的固定检索报告由现场维护的 case 文件驱动；case 文件可以包含查询
原文，但必须与报告分离、纳入访问控制，不提交到仓库。每条 case 至少固定
`id`、`baseId`、`mode`、`topK`、正样本 ID；负样本、MMR、context/raw
citation 和 model profile 按数据集能力补充：

```powershell
go run ./cmd/retrieval-regression `
  -base-url http://127.0.0.1:8765 `
  -base-id <default-base-id> `
  -input baseline\YYYY-MM-DD\retrieval-corpus.json `
  -output baseline\YYYY-MM-DD\retrieval-regression.json
```

命令退出码、报告 `status`、每条 case 的 expected/negative 判断和依赖检查
结果共同决定门禁；仅有 HTTP 200、空 hits 或缺少 generation/source 字段
都不能通过。检索报告 fingerprint 与输入 SHA 用于比较同一数据集在 P1a、
P2、P3 的变化，不能用新生成的正样本 ID 替代固定回归集。

### 8.2 P1a：存储写入、一致性与安全启用条件（当前：部分完成）

P1a 是 P1b 的硬前置。核心原则是：先保证任何任务都不能绕过写入和生命周期边界，再允许异步入口对外承诺“已接收”。

| 编号 | 工作项 | 实施内容与涉及范围 | 交付与验收 |
|---|---|---|---|
| P1A-01 | 兼容格式与隔离备份 | 在不修改旧 migration 的前提下，明确 `storage_format_version`、`min_reader_version`、`min_writer_version`、migration 状态、generation 0 和旧 ID 映射；迁移前停止 Worker/GC，生成 SQLite 一致性备份及 raw/source/命令输入/配置/版本清单。 | 新旧版本对未来格式明确拒绝；隔离目录恢复后验证检索、删除可见性、raw、任务和计数；记录备份 Hash、恢复点和新增/删除差异。 |
| P1A-02 | 建立唯一 Storage Writer | 在 `internal/storage` 增加唯一写入入口、控制写优先级、普通写队列、每事务 context/deadline、批次边界和事务耗时记录；Writer 同时输出 aggregate 与 control/normal/maintenance 分项的准入、拒绝、丢弃、成功、失败、排队等待、运行耗时、`dbWaitMs` 和 `dbTransactionMs`；`operations` 自身的提交、进度、取消、终态也必须走 Writer；Writer 未启动时所有 `DB.Exec/Write/WriteTx` 统一 fail closed，不得回退裸句柄。读连接不能执行写操作。 | 静态盘点和测试证明生产路径没有直接 `db.Exec/Begin` 写入；Writer 忙时提交/取消/状态读取有界；控制写不会被无限业务写阻塞；覆盖 C05。 |
| P1A-03 | 全写路径迁移 | 盘点并迁移 `internal/knowledge/store.go`、`internal/operations/service.go`、上传/URL capture、维护、实例锁、恢复和 raw 映射等所有 `Exec/Begin/Commit`；保留 raw 文件系统操作与 SQLite 发布标记之间的可恢复边界。 | 每个写路径都有 owner、resource class、事务 deadline、失败补偿和恢复依据；新增代码通过 lint/static guard 阻止绕过 Writer。 |
| P1A-04 | 短事务和有界批次 | 将 Chunk、FTS、向量、generation mapping、文档删除、目录删除、启动恢复和统计修复拆为有界批次；文档树/知识库清理按稳定 ID 分页，chunk/generation/recovery 更新按固定批次提交；事务内不得包含网络、模型、解析或不可控文件操作；进度更新合并而不是每个 Chunk 单独提交。 | 记录批次大小、事务时长、WAL 增长和重试点；强制退出发生在每个批次前后时，可恢复且不重复业务效果；覆盖 C03/C05/C12。 |
| P1A-05 | 生命周期与固定读视图全路径审计 | 对导入、目录扫描、重建、删除、恢复、URL 刷新、raw citation、邻居读取和诊断逐一确认 generation/source/model、`active|deleting`、ancestor epoch、读引用和 stale attempt 条件；补齐未覆盖的旁路。 | 旧 generation 保留到读引用释放；重建失败不替换 active；删除后旧任务、旧子文档和新 child ID 全部 fail closed；覆盖 C06-C10。 |
| P1A-06 | 维护与旧任务交接设计 | 明确旧 `jobs.Manager` 只允许作为兼容适配器，不得继续作为第二个权威队列；定义已有任务如何映射到 Durable Operation、如何恢复和如何向旧 jobId 报告。 | 形成迁移清单和停机/不停机交接方案；在 P1a 门禁前不删除兼容响应，但不再新增直接使用旧队列的业务路径。 |
| P1A-G1 | P1a 门禁 | 综合运行 Writer 忙、长读、删除/重建竞争、generation 失败、迁移前后备份恢复和进程退出。 | 只有当所有生产写入可证明经过 Writer、长事务已拆分、旧格式兼容边界可恢复、C05-C10 基础门禁通过，才能启用 P1b 的完整异步接收契约。 |

**P1a 当前差距：**generation、tombstone、ancestor fence、immutable raw、Storage Writer、主要业务迁移、全 internal `writer_guard`、Writer control/normal/maintenance 分项统计、事务次数/运行耗时、`dbWaitMs`/`dbTransactionMs`、SQL callback span 和 commit span 已有；本轮又移除了 Writer 未启动时回退裸 `*sql.DB` 的分支并加入 fail-closed 回归，同时闭合了目录同步进度/失败子项/成功收尾状态写入的错误传播。仍需完成全部旁路复核和完整写路径分项计时、剩余大事务压力证据及 release-host 回滚演练。

### 8.3 P1b：Durable Operation 与调用方迁移（当前：部分完成）

| 编号 | 工作项 | 实施内容与涉及范围 | 交付与验收 |
|---|---|---|---|
| P1B-01 | 补齐持久化命令语义 | `operations` schema 已增加主体/作用域、input reference/hash、source/config/model snapshot reference、目标/祖先 epoch、allocated document/generation、retry schedule、recovery/result/retention 字段，`operation_items` 提供 per-item commit marker；App admission boundary 为新命令填充非敏感的真实 base/document epoch、source/config/model 引用，request fingerprint 纳入调用方提供的不可变语义；executor result 按与 command payload 相同的有界持久化上限校验，超限不写入大对象并标记不可重试；文档导入、单项/批量删除和重建已通过 Knowledge `CommitHook` 将业务发布与 committed marker 纳入同一短事务，并有回滚测试；`import_files` 现在为每个批次项持久化预分配 document ID，重放复用该 ID；单项/批量重建重放前按有界 item 查询跳过已 committed/skipped 项；已进入 deleting 状态的单项、目录和知识库删除重试会重新执行有界物理清理并进行 scope 校验，committed marker 只代表逻辑栅栏已提交而不代表 raw/chunk 已清理；`import_directory`/`rescan_directory`/`delete_directory` 已为聚合根记录 committed/failed marker 和失败结果，marker 写入失败不再被吞掉；`delete_base` 的生命周期栅栏也支持 marker 原子提交和 deleting 状态清理恢复。仍需覆盖剩余聚合业务路径和故障注入。 | 同键提交在容量/生命周期检查前返回原 Operation；同键不同输入返回 409；响应丢失可用原键查回且不重复执行；结果不会绕过持久化边界；所有生产入口可从 envelope 重放；关键文档业务效果与 marker 原子提交；批量导入重放不产生第二个文档；批量/知识库/目录删除在逻辑栅栏或物理清理失败后可继续清理；覆盖 C02-C04。 |
| P1B-02 | 统一 Executor 与 Worker | 所有长任务从一个 Operation Service 注册和执行：文本/文件/目录/URL、重建、单项/批量/知识库删除、自检、模型下载/删除、维护和恢复；模型、解析、磁盘、DB 资源按固定顺序获取。 | 任务状态只由 Durable Operation 裁决；旧 jobs 只做兼容查询/事件适配，不持有第二份权威状态；Worker 重启可从持久化输入重放。 |
| P1B-03 | 消除同步长请求 | 重点迁移 `RestoreBase`、剩余模型下载/维护和所有遗漏的导入/扫描/删除路径；OCR、本地模型、Ollama 删除以及模型缓存迁移已采用同一提交边界。HTTP handler 只做校验、持久化命令、绑定输入和返回 receipt。 | 用静态检查和真实 HTTP 测试证明 handler 不解析、不调用模型、不做物理删除；202 只表示 received/queued，最终成功必须来自终态；覆盖 C01/C13。 |
| P1B-04 | 输入所有权与聚合配额 | 在提交时原子绑定命令和 upload/staging；增加活动上传会话数/字节、命令输入字节、任务数、内存预算、临时索引、模型缓存、可用磁盘低水位和结果保留量；区分单文件 100 MiB 与 HTTP envelope 上限；绑定输入在成功/取消、不可重试失败或达到最大重试次数后释放，启动恢复补扫终态 lease。 | 超额请求明确返回 `429 queue_full` 或 `quota_exceeded`，不产生孤立输入；原键重试不重新占用或重复绑定；终态与进程退出窗口均不遗留 staging；覆盖 C04/C12/C15。 |
| P1B-05 | Web/Agent/旧 API 同批迁移 | 完成 Operation submit/status/events/cancel/retry、Web OperationStore、Agent 查询/取消/重试工具和 `jobId` 兼容层；统一错误码、Retry-After、终态和分项结果。 | 浏览器刷新、路由切换、Agent 连接中断后仍能查询；旧客户端不能把 202 当完成；覆盖 C01/C11/C13。 |
| P1B-06 | 崩溃、取消和不确定提交 | 在提交后/唤醒前、业务发布后/终态前、cancel intent 后、response 丢失、worker 强退和 retry 竞争点注入故障；验证 attempt/revision 单调和业务效果至多一次。 | C02-C05、C11、C16 具备可重复脚本、原始日志、数据库快照和结果摘要；错误不会暴露 Token、密码、Cookie 或原始敏感路径。 |
| P1B-07 | 控制请求 context 闭合 | `GetContext`、`CancelContext`、`RetryContext` 使用请求 context 进行查询、control Writer 事务、事件写入和终态读取；Web 新 Operation API、旧 job API 与 Extension 工具统一传递 request context；兼容无 context 包装器只供旧同步调用。 | 已取消请求不继续占用 DB/Writer；取消/重试的持久化控制意图仍由 Operation 状态裁决，已接收 worker 不继承 HTTP context；normal/race 覆盖 Get/Cancel/Retry 及 Web/Extension 调用方，覆盖 C05/C11/C13。 |
| P1B-G2 | P1b 门禁 | 运行完整 Operation 状态机、Web/Agent/旧 jobId、上传配额和故障矩阵。 | 只有持久化提交成功才返回 accepted；没有孤立输入、虚假成功或重复业务效果；P1a/P1b 各自有可执行回滚方式，才能对外宣称异步迁移完成。 |

**P1b 当前差距：**Durable Operation、幂等、取消、恢复、上传、`restore_base`、模型缓存迁移及其计划、SQLite 维护、可取消的本地模型删除、过期 terminal payload/result 清理、Operation/Web/runtime 共享错误边界脱敏、Ollama 删除和协议适配已有较多实现；生产 App 已不再启动旧 `jobs.Manager`，上传聚合配额、临时 staging reservation、已完成上传实测字节绑定、终态/最大重试输入 lease 释放及启动补偿、可取消的模型复制/删除/SQLite 维护、operation 级 memory/disk/temp 预算、result 大小边界、持久化命令 envelope/`operation_items` schema、新命令的非敏感 source/config/model/epoch enrichment、关键文档导入/删除/重建和 `delete_base` 的业务发布-marker 原子边界已接入；旧 Knowledge 异步方法已移除，目录/重建测试直接复用 Durable worker 的执行入口；`restore_base` 已改为可取消的固定游标分页读取，并为每个恢复文档接入稳定 item marker，失败重试会复用已 ready 的派生文档；批量导入结果写入已消除多 worker 结果竞争，单项/批量重建重放会先跳过已解决 item；P1B-07 的查询/取消/重试控制入口也已接入请求 context。仍需完成剩余聚合操作的完整失败语义、完整规模证据、故障矩阵和 release-host 重放。

**本轮 P1B-01 补齐：**单项文本/文件导入、单项删除、知识库删除、批量删除以及 URL 导入/刷新在执行器入口先读取 resolved item marker；已提交业务效果在终态丢失后的重放不会再次抓取、删除或导入。批量 `import_files` 将已 committed/skipped 的项状态传入 Knowledge 规划器，在冲突解析和 worker 之前直接跳过，稳定 allocation 和结果标题可重建。`import_directory`、`rescan_directory`、`delete_directory` 也在文件系统操作前检查聚合 marker；目录导入按规范化 source path 找回稳定容器，重扫/删除按目录 ID 重建结果。文件导入的 `replace` 冲突清理改为使用可取消 context，并为被替换对象写入独立 `replace:<documentId>` marker；批量文件替换同样通过 item-hook 传递取消和提交边界。对于“同标题且同内容”的 replace，规划阶段不会把被替换旧文档的同哈希误判为 duplicate，确保标题删除后仍重新导入；新增 normal/race 回归。对应历史 post-publish kill fixture 明确模拟“旧数据已有业务效果但缺 marker”，以继续覆盖兼容恢复路径。

**本轮剩余单例副作用补齐：**OCR/本地与受管模型下载及删除、Reranker 自检、模型缓存计划/迁移、Ollama pull/delete、SQLite/Raw maintenance 均使用 `effect:*` 分项 marker；marker 同时保存有界结果，resolved 重放优先恢复该结果，再在外部调用或文件系统维护前短路。可从本地状态或 Ollama tags 判定的重试会先完成补偿确认，再决定是否重做副作用。新增 `TestSingletonExecutorsSkipResolvedEffects` normal/race 回归；OCR 下载 HTTP 提交也透传请求 context，并由 `TestOCRModelDownloadHonorsRequestCancellation` 覆盖取消时不创建 operation。真实远端服务中断、跨平台文件系统故障和 release-host 重放仍属于待验门禁。

**本轮维护清理边界补齐：**过期 terminal operation payload/result、过期 `ready/failed` URL capture、过期 `uploading/complete` 会话以及终态 `bound` upload lease 均改为按 `cleanupBatchSize=100` 固定批次循环；每轮查询/写入/文件删除前后检查 context，写入统一使用 control Writer，文件删除仍逐个执行并允许重试。新增超过一个批次的 normal/race 回归，验证数据库状态、capture body 和 staging 文件全部收敛；取消场景保持可观察的 context 错误。仍需在 SmartCare/release-host 规模下记录积压量、批次数、WAL/事务耗时、文件残留和收敛时间。

**本轮删除/恢复长路径补齐：**文档树和知识库物理清理不再一次性物化全部 cleanup refs，而是按稳定 document ID 分页；generation/chunk/base cleanup 按固定批次经 control Writer 执行，并透传 durable operation 的 context。`RecoverInterrupted` 同样按稳定 ID 选择有限批次，避免 pending 行因状态保持不变而重复扫描；URL 自动刷新按 `(created_at,id)` 游标分页，取消可在下一页前停止。新增超过分页边界的目录删除、超过恢复批次的 crash-recovery 以及 normal/race 回归；取消后保留 deleting/可重试状态。仍需在 SmartCare/release-host 规模下记录长读、WAL、批次耗时和恢复收敛。

**本轮 P1a context 传播补齐：**重建/导入的文档、base、raw citation 读取，legacy raw 迁移、启动 ingest 栅栏、进度状态写入、向量复用查询和向量批次写入均新增 context 版本，并由长路径调用；兼容包装仍使用 background。Writer 在取消发生于已入队写入后可能返回 `storage.ErrWriteUnknown`，调用方必须按持久状态对账，不能将其解释为“肯定未写入”。新增 `TestReindexAndRawCitationHonorCanceledContext`、`TestPutChunkVectorsHonorsCanceledContext` normal/race 回归。仍需 release-host 取消/故障注入确认提交后恢复语义。

**本轮 P1b 控制面 context 传播补齐：**Operation 查询、取消和重试新增 `GetContext`、`CancelContext`、`RetryContext`，其 SQL 查询、control Writer 事务、事件写入和终态读取均绑定调用方 context；Web 的新 Operation API、旧 `/api/jobs/{id}` 兼容取消/状态接口及 Extension 的 operation status/cancel/retry 工具统一传递请求 context。Durable worker 仍使用独立生命周期 context，避免客户端断开导致已接收任务被误取消；控制请求取消只停止自身等待，不撤销已提交的持久化意图。`TestOperationControlReadsHonorCanceledContext` 及取消/重试回归 normal/race 均通过；仍需真实 Agent Host 断线、Writer 忙和跨进程 control-failure 演练。

**本轮目录 worker-read 旁路补齐：**目录同步的 `importChildFile` 在有界文件读取后再次检查 worker context，并通过 `findDocumentByTitleContext` 完成标题冲突查询；数据库取消错误不再被当作“未找到”而继续执行删除/导入。`TestDirectoryChildTitleLookupHonorsCanceledContext` normal/race 均通过，补齐目录同步与单文件/批量导入之间的 context 契约。

**本轮模型控制面读取补齐：**本地模型分页新增 `ListPageContext`，App 的模型列表聚合、reranker 自测结果、base 配置读取和 runtime 状态回退均透传 context；OCR 状态、模型删除前 manifest 探测和自测持久化也不再绕过调用方 context。取消发生在 managed runtime fallback 前会立即返回，不会把已取消请求继续推进到模型自测。对应 Models、Knowledge 和 App 的 normal/race 定向回归通过；仍需真实 Agent Host 断线、模型缓存大规模读取和 release-host 模型切换演练。

**本轮 Base/scope/stat 读取补齐：**Web 基础库列表、详情、配置校验、删除/恢复/重建前探测、统计、导入和文件操作均改用请求 context；Extension 自动上下文的基础库枚举、Search provider 解析、文档列表/子树读取、统计及向量维度/模型计数也沿同一 context 链路执行。`ListBases`、`Stats`、provider/base 查询保留 background 兼容包装，但生产请求不再从这些包装器绕过取消。相关 Knowledge、Models、App、Web 定向 normal/race 回归及 `go vet ./...`、`git diff --check` 已通过；仍需真实 Writer 忙、Agent Host 断线、SmartCare 长读和 release-host 重放证据。

**本轮文档/历史读取补齐：**Web 的文档详情、目录/文档删除与重建前探测、Chunk 分页、显式检索历史查询及清理均改用请求 context；检索历史写入也通过 `ExecContext` 进入有界 Writer。兼容无参 `GetDocument`、`ListChunks` 和历史 API 仍保留给旧同步调用。对应 Knowledge/Web 回归通过；仍需补齐剩余同步写入口的静态审计，并在大目录、Writer 忙和 Agent Host 断线场景验证取消收敛。

**本轮 Durable worker-read 旁路复核：**文本/文件/批量导入、URL 导入与刷新、RestoreBase、目录删除以及兼容重建入口中的 Base/Document/Chunk/Raw 读取均改用 worker context；取消或数据库错误不再被旧包装器吞掉。取消测试覆盖单项、批量、URL 和恢复入口并通过 normal/race。仍需继续收敛同步写 API 的 context 版本，并在大库、Writer 忙、外部 URL/模型和 release-host 故障点验证长路径收敛。

**本轮同步写入口 context 收敛：**Web 建库/改库、目录创建、源码重指向和文档改名均接入请求 context；Knowledge `putBase`、Base/Document 更新及目录创建保留 background 兼容包装，同时向 Writer 传播调用方 context。取消进入 Writer 后若提交结果无法判定，测试按 `storage.ErrWriteUnknown` 对账，不把它误判为肯定未写入。定向 normal/race 回归通过；仍需全包回归、剩余 group/scope/config 写入口复核及 release-host 的不确定提交演练。

**本轮历史 GC 测试稳定性修复：**`TestSyntheticHistoricalCorpusLongReaderGCDrill` 原先对全部 retired chunks 做单次大 UPDATE，在 Windows `-race` 下超过 Writer 的 30 秒数据事务上限，稳定表现为 `ErrWriteUnknown: context deadline exceeded`。测试夹具现按 rowid keyset 每 500 行更新，保留 pinned reader/GC 断言并真实覆盖有界维护；未放宽生产 Writer 超时。该测试 normal/race 重放通过，仍需 SmartCare 规模和持久卷长读/GC 验收。

### 8.4 P2：资源调度、背压与公平性（当前：部分完成）

| 编号 | 工作项 | 实施内容与涉及范围 | 交付与验收 |
|---|---|---|---|
| P2-01 | 统一资源模型 | 固化 `db_write`、`io`、`disk`、`network`、`model`、`maintenance` 及 `memory_bytes`、`disk_bytes`、`temp_bytes` 计量；为每种任务声明需求、释放点和最大占用。模型目录读取优先使用 runtime 的非阻塞状态快照，避免健康/推理探测占用控制面；文件系统 resource sampler 对 raw/upload footprint 使用 2 秒缓存，DB/WAL、RSS、heap 和磁盘剩余空间每次刷新，避免每次 `/api/status` 递归扫描大目录。 | `/api/status` 返回配置、active、queued、拒绝原因和水位；资源计数与实际 RSS/WAL/临时空间可对账，目录 footprint 采样不会把模型/控制面请求变成长读，模型目录请求不等待 helper 的长 health 调用。 |
| P2-02 | 公平调度与优先级 | 实现 per-base quota、删除/控制写优先、老化防饥饿、跨资源固定获取顺序、维护降级和有限队列；避免同一 base 的重建无限挤占另一个 base。当前已有 `TestPerBaseQuotaDoesNotStarveAnotherBase` 验证 base-a 占用配额时 base-b 仍可获得 lane。 | 双库压力下记录每库等待、运行、拒绝和吞吐；没有无限队列、死锁或低优先级永久饥饿；覆盖 C15。当前仅为本地小规模证据，仍需双库持续压力。 |
| P2-03 | 字节配额与水位背压 | 对 upload/staging、raw、临时索引、模型缓存和结果保留实施可配置硬上限/低水位；磁盘不足时先拒绝新输入或暂停低优先级维护。 | 注入 ENOSPC、慢盘和空间回收，验证 active 数据不损坏、输入可重试、占用最终收敛；覆盖 C12/C15。 |
| P2-04 | 等价任务合并 | Web/Agent/通用 Operation 入口对重建使用 `reindex.auto.*` 摘要键；单文档/显式批次按规范化 ID 和 source/content/raw identity 摘要，整库目标使用随 source identity 与子树删除栅栏原子递增的 `bases.mutation_epoch`，再叠加 active generation、目标集合及 resolved model/config snapshot。三类入口共享同一摘要契约，整库生成键不再扫描全量文档。 | 并发相同重建只保留一个业务效果；跨入口得到同一 Operation；状态变化、键冲突、失败重试和重启场景不错误合并；本地边界测试已通过，仍需双库压力、故障注入和 release-host 重放证据确认。 |
| P2-05 | 检索有界与显式降级 | 保持搜索 deadline、model admission wait、TopK/variants/candidate/context 上限；把 429、408/504、model unavailable 和 lexical fallback 区分。 | 固定检索集比较召回和版本字段；慢模型不会拖垮控制面，降级结果不混用错误向量空间。 |
| P2-G3 | P2 门禁 | 使用 P0 冻结的双库数据和预算，运行持续提交、跨库竞争、慢模型、慢读者和磁盘压力。 | 同时报告有效吞吐和拒绝率；队列、RSS、WAL、临时空间均在预算内；否则回到 P2-01/P2-03，不进入 P3 规模优化。 |

### 8.5 P3：规模化维护、故障恢复与回滚（当前：基础完成，验收未完成）

| 编号 | 工作项 | 实施内容与涉及范围 | 交付与验收 |
|---|---|---|---|
| P3-01 | 启动维护统一调度 | 将 `StartBackgroundMaintenance` 改为提交 `maintenance_storage` Operation，启动阶段只报告 starting/degraded，不直接启动绕过 Scheduler 的业务 goroutine。 | 启动维护有 operationId、进度、取消/恢复和 maintenance lane；正常 API 不被扫描或 VACUUM 阻塞。 |
| P3-02 | 大库 generation/FTS/GC | 对退役 generation、raw mapping、quarantine、FTS 和 chunk 做保留期、读引用和分批回收；文档树/知识库物理清理按稳定 ID 分页，chunk/generation/recovery 更新按固定批次和调用方 context 执行；operation terminal payload/result、过期 URL capture、过期 upload lease 和终态 upload lease 清理均使用固定批次、可取消的维护循环；禁止正常路径无界 VACUUM。 | SmartCare 规模长读期间 GC 不混视图、不误删 raw；读引用释放后空间收敛；维护积压不会被单次全量扫描或长 UPDATE 放大；报告 WAL、事务和磁盘峰值；覆盖 C08-C10。 |
| P3-03 | 文件锁和磁盘故障 | 在 release-host 持久卷验证 Windows no-share、POSIX advisory lock、raw rename 前后、quarantine 前后和 OS-level ENOSPC。 | 每个断点有旧/新 authoritative 状态、暂存文件、重试和清理责任；不以本地 tmpfs/loopback 结果代替持久卷验收；覆盖 C12/C15。 |
| P3-04 | 模型切换和长期读者 | 在 SmartCare 规模验证 A→B、失败的 C、禁用 embedding、模型缓存释放和长生命周期读者；固定 model key/dimension/source。 | 失败不替换 active；无混合向量空间；历史证据在 retention 前可读、过期后 fail closed；记录 GC 与模型切换耗时。 |
| P3-05 | 备份、迁移和旧版本回滚 | 对真实规模隔离备份记录产品版本/SHA、format bounds、schema、备份 Hash/大小/耗时、恢复耗时和备份后差异；用旧兼容二进制演练 release-host 回滚。 | 旧二进制不会直接读写 format 2 新数据；恢复后验证检索、删除、raw、任务和计数；回滚差异可解释且不覆盖现有目录。 |
| P3-G4 | P3 门禁 | 完成双库规模 GC、长读、模型切换、持久卷空间耗尽、文件锁、备份恢复和回滚报告。 | C08-C10/C12/C14/C15 的 SmartCare/release-host 证据齐全，故障后收敛时间有界，才可宣称维护和恢复完成。 |

### 8.6 P4：读取路径和大目录性能（当前：本地实现基本完成，规模门禁未完成）

| 编号 | 工作项 | 实施内容与涉及范围 | 交付与验收 |
|---|---|---|---|
| P4-01 | 消除完整列表依赖 | 保留 `documents/children` 的 parent-scoped paging/breadcrumb；`reindex_base` 已改用计数、固定 `(created_at,id)` 上界和 keyset 游标批读；目录删除入口改用递归 SQL 标量计数，目录导入/重扫只读取目标子树 metadata，根路径查找改为索引查询；批量导入查重只查询本批次 content hash；索引状态接口在 SQL 层过滤 `pending/processing`，最多物化 200 条并透传请求取消；`restore_base` 的源分页只读取 metadata，再逐文档按需读取原文/原始文件，避免一页同时物化 50 份 `raw_text`；模型选择器通过 `models.Manager.ListPage` 和 `GET /api/local-models?limit=&offset=` 有界读取并返回 `nextOffset/hasMore`；缓存迁移同路径时只用 manifest 计数，不再把完整模型目录加载到内存；旧无参数模型接口仅保留 `models`/`cacheDir` 响应形状，按默认上限返回并标记 `Deprecation: true`，不再提供无界兼容读取；旧文档接口保留数组形状但只返回默认 50 条；Extension 的 `knowledge_list_documents` 及 `knowledge_list_bases(baseId)` outline 也改为默认 50、上限 200 的有界页，并保留 `documents` 字段。 | 重建/目录导入与删除/索引状态/恢复/模型选择/Extension 控制面不再复制整个 base 或模型缓存；分页接口只返回有界行数；维护计数支持取消且不解码完整目录；接口 p95、返回字节数和 SQL 行数有报告；完成旧模式退役决策。 |
| P4-02 | 读视图和详情拆分 | 列表只返回摘要和状态；详情、raw、诊断和上下文按需读取并使用固定 generation/source；继续保持 tombstone 和历史引用校验。 | 当前页只刷新受影响数据；历史/删除对象不会被旧请求重新显示；覆盖 C08/C09。 |
| P4-03 | 统计缓存边界 | 统计使用短 TTL/增量计数，相关 mutation 失效；向量诊断和模型状态不伪造缓存结果。 | 并发导入/删除时统计最终收敛；记录 cache hit/miss 和失效延迟。 |
| P4-G5 | P4 门禁 | 运行大目录、多任务、快速翻页和持续后台写入。 | 文档页、导入页、搜索页不加载全库；控制读满足 P0 预算，兼容 API 已有明确弃用/分页策略，并完成大目录预算报告。 |

### 8.7 P5：前端 OperationStore 与数据级更新（当前：本地基本完成）

| 编号 | 工作项 | 实施内容与涉及范围 | 交付与验收 |
|---|---|---|---|
| P5-01 | 单一任务状态源 | OperationStore 负责启动、刷新恢复、独立轮询、取消、重试、失败/取消卡片和 `state_revision`；禁止页面组件各自维护后台任务状态。 | 同一 operation 只有一个轮询器；202 后显示 received/queued/running，不提前显示完成；刷新后状态和结果一致。 |
| P5-02 | 路由竞态隔离 | 为普通路由 GET 使用 AbortController；轮询不被路由切换取消；所有响应校验 route generation、selected base 和 state revision。 | 快速切换文档/导入/模型/设置不会出现旧数据覆盖新页面、空白壳或失去按钮。 |
| P5-03 | 数据级刷新 | 任务完成只刷新当前 children page、受影响 preview、任务卡和统计；保留用户选择、滚动、周边上下文和失败重试入口。 | 真实浏览器持续提交/切换场景通过；出现 stale-response 时能在日志中定位请求 generation。 |
| P5-04 | 缓存与部署身份 | 保持 deterministic web buildId、asset ETag/no-cache、Agent prefix 和 restart 后一致性；兼容客户端得到明确 upgrade/refresh 错误。 | Web contract、E2E、重启后 304 和代理路径测试通过；记录浏览器版本、构建 SHA 和失败截图/日志。 |
| P5-G6 | P5 门禁 | 本地 E2E 通过后，在真实 Agent Host 运行持续任务、断线、刷新、路由切换和服务重启。 | 生产 Host 不出现空态死路、旧请求覆盖或错误成功；否则只标记本地通过，不关闭 P5。 |

### 8.8 P6：启动、实例所有权和有界退出（当前：部分完成）

| 编号 | 工作项 | 实施内容与涉及范围 | 交付与验收 |
|---|---|---|---|
| P6-01 | 启动状态机 | 明确 starting/degraded/ready/unavailable；恢复、维护、模型初始化和端口监听分别可观察；健康接口不得在未完成初始化时伪报业务 ready；Durable Operation worker 只有在 Knowledge provider、Models、Ollama、Runtime 和可选健康检查完成 wiring 后才启动，避免恢复任务与 provider 配置并发读写。 | 冷启动期间控制面可用且状态真实；初始化失败有边界、脱敏 stderr 和明确退出原因；恢复任务不会在依赖 wiring 完成前运行。 |
| P6-02 | 受控恢复 | 统一恢复顺序、旧任务接管、输入清理、maintenance resume 和 operation wakeup；重复启动必须在扩展、模型、会话和子进程创建前失败。 | C16 在同一数据目录、端口、实例锁和真实子进程下可重复；不会双重执行或遗留孤儿进程。 |
| P6-03 | 全链路有界退出 | 审计 `App.Close`、`Jobs.Stop`、`Operations.Stop`、runtime process tree 和 HTTP server 的等待；所有等待有 context/deadline，拥有者只终止自己创建的 PID 树。 | 正常退出、取消退出、Worker 卡死、子进程卡死均在预算内返回；记录退出耗时和残留 PID；禁止无界 `WaitGroup.Wait()` 阻塞退出。 |
| P6-04 | 跨平台 release-host | 在 Windows、Linux、macOS release-host 运行实例锁、重复启动、端口错误、子进程树回收、SIGTERM/CTRL+C、数据恢复和 Agent 重启。 | 产出平台、OS、架构、二进制 SHA、日志、退出码和进程树证据；本地 WSL2 或交叉编译只能作为补充。 |
| P6-G7 | P6 门禁 | 完成 C13/C16 的真实 Agent Host 和跨平台运行，并复核前述所有回滚和敏感错误输出。 | 控制面、恢复、重复启动和退出均满足预算，才允许进入最终发布评审。 |

**本轮 P6-03 补齐：**`App.Close`、`UpdateConfig` 和 `New` 失败清理路径统一通过
`closeRuntimeWithContext`，对支持 `CloseWithContext` 的 runtime 传播 deadline，对旧
compatibility controller 的 `Close` 使用有界等待；超时后不阻塞 DB 与实例锁释放。
runtime `CloseWithContext` 进一步改为不获取忙请求的串行锁，直接触发拥有者进程树
终止并等待进程句柄；helper 响应读取同时监听请求 deadline 与进程退出，避免 stdout
管道收尾把退出拖到请求超时。新增真实测试 helper 的
`TestManagerCloseWithContextBoundsBusyHelper` normal/race 回归，并保留
`TestCloseRuntimeWithContextBoundsLegacyController` normal/race 回归。真实 runtime
子进程树、卡死 helper、SIGTERM/CTRL+C 及 Linux/macOS release-host 仍需实机证据；
helper 启动竞态也增加关闭标志和前后检查，避免关闭后迟到的首次启动。不能以本地
测试关闭 P6-G7。

**本轮 P6-03 兼容队列收口：**旧 `internal/jobs.Manager` 虽已不再由生产 App
启动，但其 `Stop` 仍属于可复用的生命周期 API；现增加 `StopWithContext`，通过
一次性关闭队列、取消已接收任务和可等待的完成信号约束退出预算，旧 `Stop` 仅保留
15 秒兼容包装。`TestStopWithContextBoundsNonCooperativeLegacyTask` normal/race
通过，证明不合作的旧任务不会让调用方无限等待；任务自身仍必须响应 context 才能
最终回收。该结果只关闭本地 legacy shutdown seam，仍需真实子进程树、卡死 helper、
SIGTERM/CTRL+C 及 Linux/macOS release-host 证据。

### 8.9 P7：进程拆分决策（默认不做）

只有 P0-P6 的实现和压力证据全部完成，且单进程仍无法满足 Web/检索/恢复 SLO，才进入决策：

```text
shutu-knowledge-control.exe
shutu-knowledge-worker.exe
```

拆分前必须提供：单进程优化后的基线对比、RPC/任务所有权/备份/回滚设计、跨进程 Storage Writer 责任、实例锁策略、升级兼容方案和新增故障矩阵。没有这些证据，不得以拆进程替代 Writer、Operation 或配额改造。

---

## 9. 验收指标与证据报告

以下是第一版目标值，最终以 P0 基线和实际机器能力校准：

| 指标 | 目标 |
|---|---:|
| 任务提交 API p95 | < 500ms |
| 任务状态 API p95 | < 500ms |
| `/api/stats` p95 | < 500ms |
| 文档列表 API p95 | < 1s |
| 普通数据库写事务 | 目标 < 500ms |
| 数据库写事务告警 | > 2s |
| 写命令 HTTP handler | 不执行导入解析/索引/物理删除；接收必须持久化 |
| 任务重复提交 | 幂等返回原任务 |
| 页面切换 | 后台任务运行时仍可用 |
| 任务恢复 | 重启后可恢复、重试或明确中断 |
| 数据一致性 | 无删除后复活、无旧任务覆盖新任务 |
| 搜索及上下文版本 | 同文档证据固定 generation/source，向量按模型空间匹配；历史版本过期明确返回 |
| 查询 deadline 与模型等待 | P0 按在线模型/宿主预算冻结；超时、取消和降级可区分 |
| 接收可靠性 | 接收响应之后退出仍能查到命令；响应未知时原键重试不重复执行 |
| 业务提交可靠性 | 发布与分项提交标记原子，终态修复不重复产生业务效果 |
| 资源上限 | 任务数、上传/内存/临时磁盘字节、读引用和结果保留都有配置上限 |
| 降级与回滚 | 升级后可回到已声明的兼容版本；不兼容旧版本不得直接读写新数据 |

磁盘不设置脱离设备的固定百分比目标，而是以 API p95、磁盘队列长度、WAL 增长和任务吞吐共同决定限流阈值。磁盘达到 100% 利用率但 Web 仍满足 SLO 可能是可接受的；磁盘只有 2% 但页面超时也不能视为健康。

验收同时报告成功、拒绝、错误、超时的请求数与比例，以及持续提交负载下的有效吞吐，不能靠拒绝全部任务降低 p95。上传传输耗时单列；Operation 提交耗时从接收完整有界命令开始计算，包含持久化等待。数据库 batch 上限、各队列容量、查询 deadline、字节配额、结果/幂等保留期必须在 P0 报告中给出具体值及配置映射，并在后续阶段保持同负载对比。RSS 峰值、临时空间峰值和故障后的收敛时间作为验收数据，未测量不视为满足资源上限。

### 9.1 每个阶段的最低证据包

每个 `P*-G*` 门禁必须提交一个可复核证据包，至少包括：

- 执行提交的完整 Git SHA、二进制 SHA、Go/Node/浏览器/SQLite/OS 版本。
- 数据目录、输入清单 Hash、模型/Provider/config snapshot 和是否冷/热缓存。
- 命令、场景、并发数、队列/批次/超时/配额配置，以及开始/结束时间。
- success/rejection/error/timeout/cancel 数量和比例，有效吞吐，p50/p95/p99。
- queue wait、run、parse、model、disk、DB、FTS、vector 分项耗时。
- 峰值 RSS、WAL、数据库大小、临时空间、磁盘水位、活跃/排队任务数。
- 故障注入点、退出码、状态转移、数据库快照、raw/upload 残留和收敛时间。
- 失败项、已知限制、是否满足门禁、回滚版本/SHA 和恢复点差异。

报告必须区分“未运行”“运行但不通过”“本地通过”“release-host 通过”和“SmartCare 通过”。缺少环境或输入说明的截图、单次成功日志和纯单元测试不能单独关闭门禁。

### 9.2 当前验收状态

当前已知的本地验证不等于最终完成：目标仓库已有 Go/Web contract/E2E 和多项故障测试记录，但 evidence 文档仍将 C01-C16 的 SmartCare、release-host、跨平台和回滚工作列为剩余门禁。新的报告必须在原有结果上追加，而不是覆盖或重新解释历史结果。

截至 2026-09-16 的本地验证快照如下，作为后续报告的起点而不是门禁替代物：

- 当前源码 `go test ./... -count=1` 通过，包含 App、Extension、Knowledge、Operations、Runtime、Storage、Web、Scripts 和全部 command 包；`go vet ./...` 与 `git diff --check` 通过。
- Runtime 管道关闭修复后的最新 `go test ./... -count=1` 仍通过全部包，App 133.246s、Extension 124.294s、Knowledge 156.689s、Runtime 21.042s、Storage 7.378s、Web 302.574s、Scripts 1.091s；这组结果覆盖了 P6-03 受影响代码。
- `go test -race ./... -p 1 -count=1 -timeout=25m` 已串行通过全部包；串行执行用于隔离 Windows 包间资源争用，App 174.746s、Knowledge 483.287s、Operations 22.903s、Runtime 20.278s、Storage 95.570s、Web 509.890s、Scripts 3.215s，未出现 race 报告或超时。
- helper 关闭/启动竞态修复后的再次 `go test -race ./... -p 1 -count=1 -timeout=30m` 仍串行通过全部包，App 176.531s、Knowledge 483.739s、Operations 23.253s、Runtime 19.227s、Storage 96.318s、Web 509.055s、Scripts 3.212s，未出现 race 报告或超时。
- 本轮 P4 索引状态旁路修复后的 `go test ./internal/knowledge ./internal/web ./internal/storage -count=1 -timeout=20m` 通过，Knowledge 131.833s、Web 262.242s、Storage 7.012s；聚焦 `TestIndexingStatusIsBoundedAndCancellable` 与 `TestRawRestoreProbeAndIndexingAPI` 的 normal/race 回归也通过。生产 `GET /api/indexing-status` 现在在 SQL 层只读取 active 的 `pending/processing`，最多物化 200 条，并透传 HTTP 取消。
- 本轮恢复路径优化新增 `TestRestoreSourcePagesUseMetadataOnly`：恢复源分页只返回 metadata，单文档读取仍能取得 `raw_text`；目标文档存在性检查和源内容读取均透传恢复 context。该测试的 normal/race 回归均通过，避免固定 50 行分页把大文档原文批量载入内存。
- 恢复分页改动后的 `go test ./internal/knowledge -count=1 -timeout=15m` 全包通过，耗时 110.025s；`go vet ./...` 也通过。
- 本轮 P1B URL capture 清理补齐：`import_url`/`refresh_url` 在已提交 item marker 的重放路径先执行幂等 `ConsumeURLCapture`，避免业务效果已发布但后续清理中断时保留大 payload 至过期；`TestResolvedURLOperationConsumesLeftoverCapture` 的 normal/race 回归均通过。
- URL capture 清理改动后的 `go test ./internal/app -count=1 -timeout=15m` 全包通过，耗时 95.583s；该结果与新增 race 聚焦回归共同验证 App executor 注册路径。
- 本轮维护清理改为固定批次和可取消循环：terminal payload/result、URL capture、过期 upload session 及终态 bound upload lease 不再一次性物化或执行无界 UPDATE；`TestMaintenanceCleanupProcessesRowsInBatches`、`TestURLCaptureCleanupProcessesRowsInBatches`、`TestUploadMaintenanceCleanupProcessesRowsInBatches` 的 normal/race 回归均通过，覆盖超过 100 条、staging 删除和取消边界。`go test ./internal/operations -count=1 -timeout=15m` 通过，耗时 7.578s。
- 本轮删除/恢复长路径改为稳定 ID 分页、固定 chunk/generation 批次和可取消 context：`TestDirectoryDeleteCleanupUsesBoundedPages`、`TestRecoverInterruptedUsesBoundedCancellableBatches` 的 normal/race 回归均通过，覆盖超过 128 条 cleanup refs、超过 256 条恢复行和取消边界；URL 自动刷新也改为 `(created_at,id)` 游标分页。
- 本轮存储巡检改为 visitor/counter 读取：active raw 文件不再先物化完整路径列表，文档引用和 retired generation raw 路径按 visitor 建立引用集合，chunk 计数与修正透传 context；`Test(ReconcileStorageSafeQuarantinesAndPurgesOrphans|ReconcileStorageRemovesOrphansAndFixesCounts)` 及 `TestSyntheticCorpusStorageReconcileMovesExactlyOrphans` 的 normal/race 回归通过。合成 1,200 文档/1,200 孤儿文件巡检 normal 47.277s、race 125.418s；这仍不替代 SmartCare 规模、持久卷故障和 release-host 证据。
- 前一轮含 migration 0017 的全仓 `go test ./... -count=1 -timeout=30m` 通过全部包：App 136.676s、Extension 95.728s、Knowledge 157.059s、Operations 9.025s、Runtime 47.886s、Storage 9.993s、Web 305.647s、Scripts 1.053s；全部 command 包也通过。该结果保留作历史对照，不能替代 SmartCare、release-host 或 Agent Host 门禁。
- 当前工作树在 visitor/counter 存储巡检改动后再次通过全仓 `go test ./... -count=1 -timeout=30m`：App 167.500s、Extension 92.225s、Knowledge 156.194s、Operations 9.731s、Runtime 25.045s、Storage 7.567s、Web 312.764s、Scripts 1.066s；全部 command 包也通过。该结果与 `go vet ./...`、`git diff --check` 一起作为最新本地证据，仍不能替代 SmartCare、release-host 或 Agent Host 门禁。
- OCR 下载提交 context 修复后的 `go test ./internal/web -count=1 -timeout=10m` 全包通过，Web 259.096s；其 OCR normal/race 取消回归也已通过。该结果补充验证当前 Web 源码，不能替代跨平台和真实 Agent Host 门禁。
- 目录长链路 context 补齐后的 `TestRunDirectoryImportHonorsCanceledContext` 以及目录导入/重扫/重指向/递归删除 normal/race 定向回归均通过；取消的同步目录导入在创建稳定容器前返回。该结果只关闭本地目录入口与 worker context seam，不替代大目录取消延迟、release-host 重放和跨平台门禁。
- Operation executor 的重放/准入分支也已切换到可取消的 Knowledge 文档、Base、deleting tombstone 和目录稳定路径读取；App normal 108.320s，Operation 聚焦 race 149.320s 通过，覆盖聚合、单例、URL、取消、重试和已提交清理。该结果只关闭本地 operation-read context seam，不替代 release-host 取消和提交后重放证据。
- Operation executor 的 committed/skipped/failed item marker 写入也已切换到 worker context，避免取消后落入无界 background 写入；兼容 helper 仅保留给非 worker 调用。该结果仍需 release-host 提交后 marker/replay 演练确认。
- 取消竞态回归显示，worker context 已取消后仍必须有界持久化最终 `cancelled` marker；因此仅该 control 写使用 `context.WithoutCancel` 加 5 秒 deadline，业务执行仍保持可取消。App operation normal 79.767s、race 133.880s 通过；仍需 release-host 提交后 marker/replay 演练。
- bounded final cancellation-marker 修复后的最新 `go test ./... -count=1 -timeout=30m` 全部通过：App 160.087s、Extension 123.161s、Knowledge 147.471s、Operations 7.594s、Runtime 22.257s、Storage 7.534s、Web 365.832s、Scripts 1.301s，全部 command 包也通过；随后 `go vet ./...` 与 Writer static guard 通过。该结果是当前本地基线，仍不能替代 SmartCare、release-host、Agent Host、跨平台和回滚门禁。
- progress callback context 补齐及删除/基座重索引聚合 partial 结果修复后的最新 `go test ./... -count=1 -timeout=30m` 全部通过：App 158.833s、Extension 97.846s、Knowledge 145.847s、Operations 7.603s、Runtime 45.407s、Storage 8.812s、Web 345.566s、Scripts 1.216s，全部 command 包也通过；随后 `go vet ./...`、Writer static guard 与 `git diff --check` 通过（后者仅有既存 LF/CRLF 转换警告）。该结果是当前本地基线，仍不能替代 SmartCare、release-host、Agent Host、跨平台和回滚门禁。
- C11 取消竞态补齐：Worker 在 pre-dispatch hook 前先注册可取消 context，并在注册后重新读取持久化 `cancel_requested`，避免 claim 后取消请求落库但错过 worker 的窗口；`TestCancelRecordedBeforeWorkerRegistrationCancelsContext` normal/race 均通过，Operations 全包 normal 6.558s、race 27.480s 通过。该结果只关闭本地 claim-to-worker-registration seam，仍需跨进程 kill/restart 与 release-host 证据。
- P1b worker 读取 seam 补齐：上传 session 校验、批量/单文件导入的冲突与替名查询、自定义 reranker KV 清理均新增 context 版本并接入 worker；对应 canceled-context normal/race 回归和 App/Knowledge 定向回归通过。该结果只关闭本地 worker read seam，仍需 release-host 重放、规模压力和跨平台门禁。
- P1b 模型操作读取 seam 补齐：本地 model manifest 与 OCR 状态检查新增 context 版本，下载/删除重放分支接入；`TestModelReadContextHonorsCancellation` 及相关 App/Models normal/race 回归通过。该结果只关闭本地模型读取 seam，仍需 SmartCare 规模模型切换、长读者和 release-host 回滚证据。
- worker-read/model-read context additions 后的 App、Knowledge、Models、Operations 包级 normal 回归分别以 109.130s、124.914s、2.759s、7.310s 通过；Knowledge/Models 与操作 context 聚焦 race 也通过。该结果补充本地证据，仍需最终串行全仓回归及 SmartCare/release-host 门禁。
- 目录 worker-read 旁路补齐后的 Knowledge 全包 normal `go test ./internal/knowledge -count=1 -timeout=15m` 通过，耗时 119.848s；目录标题冲突查询在 worker context 下的取消 normal/race 定向回归也通过。该结果确认当前 Knowledge 包回归面，仍需大目录取消延迟、SmartCare 规模和 release-host 重放证据。
- claim-to-worker-registration 取消竞态修复后的最新 `go test ./... -count=1 -timeout=30m` 全部通过：App 168.811s、Extension 113.560s、Knowledge 151.403s、Operations 8.110s、Runtime 53.621s、Storage 7.535s、Web 407.605s、Scripts 1.007s，全部 command 包也通过。该结果是当前本地集成基线，仍不能替代 SmartCare、release-host、Agent Host、跨平台和回滚门禁。
- upload/conflict/reranker/model-read context additions 后的最新 `go test ./... -count=1 -timeout=30m` 全部通过：App 169.721s、Extension 149.829s、Knowledge 156.199s、Models 4.032s、Operations 12.164s、Runtime 23.483s、Storage 9.865s、Web 329.407s、Scripts 1.265s，全部 command 包也通过。该结果是当前本地全仓基线，仍不能替代 SmartCare、release-host、Agent Host、跨平台和回滚门禁。
- upload/conflict/reranker/model-read 与 claim-to-worker cancellation 修复后的最新串行全仓 race `go test -race ./... -p 1 -count=1 -timeout=30m` 全部通过：App 190.498s、Extension 122.113s、Knowledge 490.015s、Models 4.088s、Operations 28.223s、Runtime 19.929s、Storage 97.824s、Web 654.125s、Scripts 3.248s，全部 command 包也通过且无 race/timeout。该结果只关闭当前本地串行 race 基线，仍不能替代 SmartCare、release-host、Agent Host、跨平台和回滚门禁。
- Base/scope/stat read context follow-up 后，Knowledge、Models、App、Web 受影响定向 normal 分别通过（0.363s、1.245s、31.921s、42.556s），串行 race 分别通过（1.918s、2.199s、16.280s、51.880s）；`go vet ./...` 与 `git diff --check` 通过。该结果只证明本地请求读取取消边界，仍需大库长读、Writer 忙、Agent Host 断线及 release-host 重放证据。
- 文档详情/Chunk/删除重建前探测及检索历史读写也已切换到 request context；Knowledge/Web 相关 normal 定向回归分别以 1.234s、16.315s 通过。该结果只关闭本地文档与检索历史读取 seam，仍需全仓静态写入口复核、Writer 忙和 release-host 断线证据。
- 上述文档/历史读取改动后的最新全仓 `go test ./... -count=1 -timeout=30m` 全部通过：App 196.540s、Extension 153.797s、Knowledge 164.315s、Models 2.605s、Operations 7.275s、Runtime 20.939s、Storage 8.192s、Web 538.621s、Scripts 1.106s，全部 command 包也通过；随后串行 `go test -race ./... -p 1 -count=1 -timeout=30m` 全部通过，App 234.902s、Extension 145.294s、Knowledge 526.890s、Models 3.840s、Operations 30.196s、Runtime 26.274s、Storage 102.763s、Web 767.167s、Scripts 2.955s。该结果关闭当前本地全仓 normal/race 回归门，但仍不能替代 SmartCare、release-host、Agent Host、跨平台和回滚门禁。
- Durable worker-read 旁路复核后的 Knowledge 取消聚焦 normal/race 分别以 0.250s、1.706s 通过，覆盖单项/批量/URL/RestoreBase 读取入口；随后 Knowledge 全包 normal/race 分别以 117.385s、513.190s 通过。该结果补齐本地 worker context seam，仍需规模、Writer 忙、SmartCare 和 release-host 故障证据。
- Operation-read context 改动后的最新 `go test ./... -count=1 -timeout=30m` 全部通过：App 160.821s、Extension 101.239s、Knowledge 152.769s、Operations 10.520s、Runtime 19.248s、Storage 7.409s、Web 356.169s、Scripts 1.010s，全部 command 包也通过；随后 `go vet ./...` 与 Writer static guard 通过。该结果是当前本地基线，仍不能替代 SmartCare、release-host、Agent Host、跨平台和回滚门禁。
- 目录 context 改动后的最新 `go test ./... -count=1 -timeout=30m` 全部通过：App 165.726s、Extension 108.534s、Knowledge 148.058s、Operations 10.288s、Runtime 22.136s、Storage 6.786s、Web 344.997s、Scripts 1.072s，全部 command 包也通过；`go vet ./...` 同样通过。该结果是当前本地集成基线，仍不能替代 SmartCare、release-host、Agent Host、跨平台和回滚门禁。
- 作用域/配置/历史上下文取消边界补齐：`kv` 显式存在性判断不再把取消误判为“未配置即全库启用”；Stats 的 Provider/向量维度查询、删除恢复 tombstone 查询、`GetDocumentContext` 的首个文档读取均透传调用方 context；配置原子保存新增 `SaveContext`，Web 设置更新通过 `UpdateConfigWithContext` 绑定请求生命周期，兼容无参包装器仍保留。新增取消测试覆盖作用域、过滤检索、历史文档上下文和配置保存；全仓 normal 及串行 race 均通过，但仍需 release-host 断线/未知提交和跨平台证据。
- 本轮 context seam 修改后的全仓 normal `go test ./... -count=1 -timeout=30m` 全部通过：App 323.442s、Extension 200.517s、Knowledge 195.808s、Models 5.495s、Operations 16.073s、Runtime 63.526s、Storage 13.929s、Web 648.492s、Scripts 2.747s；串行 race `go test -race ./... -p 1 -count=1 -timeout=30m` 也全部通过：App 337.014s、Extension 200.496s、Knowledge 770.745s、Models 5.050s、Operations 51.830s、Runtime 53.421s、Storage 146.414s、Web 1498.036s、Scripts 3.720s。该结果只更新本地 normal/race 门，不关闭 SmartCare、release-host、Agent Host、跨平台和回滚门禁。
- C13/P5 Agent 集成门禁补齐并通过：首次冷启动失败根因是扩展 manifest/门禁配置的 10 秒 startup deadline 小于 managed runtime 首次 npm 安装时间；现统一为 120 秒，并把 removal gate 的旧 14 工具数量断言改为当前 18 个工具名称集合校验。使用本机 Agent 二进制执行安装/移除双配置验证，安装态为 18 tools/1 route/healthy，移除态为 0 tools/0 route/healthy。该结果关闭本机 Agent 集成移除回归，不替代生产 Agent Host 持续任务、断线、刷新和重启证据。
- P6 native-host 验收器补齐：`cmd/release-acceptance` 新增 `host` profile，在隔离 data home 中启动候选二进制并探测 `/healthz`，验证第二实例被 `instance.lock.db` 拒绝、首实例被终止后同一数据目录可重启；结果记录候选版本、平台、阶段日志和退出信息。Windows 本地 `go run ./cmd/release-acceptance -profile host -allow-dirty` 通过，native stage 573.874ms。该结果关闭本地实例锁/崩溃重启检查，不替代三平台 release-host 的信号、进程树和真实 Agent 重启证据。
- C14 发布脚本版本漂移修复：`scripts/package_release.ps1` 不再硬编码 reader/writer 6，而是从 `internal/storage/migrate.go` 读取当前格式契约（当前 format 2、reader/writer 8），并将同一值写入候选校验和 `BUILD-METADATA.json`；clean-checkout 门禁现使用 `git status --porcelain --untracked-files=all`，在当前脏工作树上已验证会在生成产物前拒绝。新增 `scripts/audit_release_package.ps1` 扫描发布包文本文件中的高置信度凭据/私钥模式和非空 credential 字段，并由正式打包流程强制调用；历史正式包审计通过。正式 ZIP 仍需干净候选 checkout 和部署 smoke。
- Linux 交付 smoke 补充：当前源码已构建 `CGO_ENABLED=0` 的 linux/amd64 二进制，并在 WSL2 隔离 data home 中完成 `/healthz` 启动、同目录重复实例锁拒绝和 SIGTERM 退出；版本报告 format 2、reader/writer 8。该结果仅是 WSL2 补充证据，不能关闭 P6-04 要求的 Linux release-host，也不能替代 macOS 原生运行。
- P3 本地故障回归补充：Windows 真实 no-share 文件锁 corpus `TestCorpusStorageReconcileFileLockStopsAndConverges` 通过（46.919s）；隔离备份/不兼容 reader 与 41.1MB 大库回滚 `Test(IsolatedBackupRestoreAndIncompatibleReaderDrill|SyntheticLargeDatabaseBackupRollbackDrill)` 通过（11.713s）；SQLite page ceiling 磁盘满提交恢复 `TestSQLiteDiskFullAtGenerationCommitPreservesActiveAndRecovers` 通过（6.680s）。这些结果关闭本地故障夹具回归，不替代 SmartCare 规模、持久卷 ENOSPC/锁和旧兼容二进制 release-host 演练。
- P1B 上传 lease 时序修复后的本地验证：非重试失败在发布终态可见时已先删除 staging；`TestUploadReleasesAfterNonRetryableFailure` normal/race 各连续 5 次通过；Operations/App 串行 normal/race 全包分别通过（13.089s/89.613s、179.679s/686.139s）。该结果只关闭本地终态观察竞态，不替代 release-host 崩溃重放。
- P1B 终态清理失败闭环补齐：过期/终态 upload lease 现在先做 staging 删除，再提交 `expired`/`released`；路径解析或删除失败保留原 lease 状态；worker 终态写入失败会再做一次有界幂等重试。`TestUploadCleanupFailurePreservesRetryableLeaseState` 与批量清理回归 normal/race 通过；当前源码随后完成全仓 normal、serial race、`go vet ./...` 和 `git diff --check` 本地复核。该结果仍只关闭本地终态/清理回归，不替代真实调用方响应未知、release-host 崩溃重放和 lease 对账。
- 上述终态清理修改已重建干净 Windows 候选：正式 ZIP 静态密钥审计通过（11 个文本文件），ZIP SHA-256 为 `aff1fd22d925c5a06333ba35d590c944e4bc9b5a07f240326d2e8b560713548b`，候选 commit 为 `2139d4c0c2ec4dda3bdbc234e5e09b7a84489f16`；解压 identity profile 与 clean-candidate Windows host profile 均通过。该候选仅为本地 Windows 证据，仍不关闭远端 CI、部署 smoke、生产 Agent Host、原生 Linux/macOS、持久卷和旧二进制回滚门禁。
- P6-01 延迟恢复失败语义补齐：`RecoverInterrupted` 失败不再只写日志后恢复为 `ready`；失败会被保存在 App 生命周期状态中，后续 healthz/status 返回 `503`、`ready=false` 和 `startup-recovery=failed`。新增 normal/race 健康回归；仍需 release-host 验证真实恢复失败、重启和退出时间线。
- P6 当前 Windows host acceptance 复核已通过：在隔离 `GOCACHE/GOMODCACHE` 后，当前工作区的 host profile 验证 healthz、重复实例拒绝、终止后重启和 Extension manifest，报告为 `.tmp/release-acceptance/20260916-053041/result.json`；该结果是脏工作区本地证据。随后包含 P6-01 修复和生命周期字段并发收口的干净候选完成正式打包、静态密钥审计、identity profile 与 clean host profile，ZIP SHA-256 为 `bc4f653eb52f44cabbb945624725163b170591b0858c212a0c7e46f1d5d8b154`，候选 commit 为 `b275fcfcec1fb0234f90fe45b43d82e6e8dde67e`。两者仍不关闭 Linux/macOS、生产 Agent Host、持久卷和旧二进制回滚门禁。
- P6 生命周期字段并发访问已收口：启动恢复/维护的 done 与 cancel 状态统一受生命周期锁保护，goroutine 使用局部 done channel，`Close` 在快照后取消并等待；App 生命周期相关 normal/race 回归通过。该结果只关闭本地字段竞态，不替代真实 Agent Host 启停交错和三平台 release-host 证据。
- P2 资源边界测试稳定性修复：normal 保留 3 秒生产排空预算并连续 3 次通过（submit p95 5.5-5.9ms、reader p95 26.9-28.3ms、drain 0.489-0.514s）；race 使用独立 10 秒诊断预算并连续 3 次通过（drain 3.76-4.88s），没有放宽 normal 生产阈值。该结果仍只是本地双 lane 证据。
- 本地回归环境隔离：首次全仓回归曾因 Node 版本不匹配进入外部 managed runtime 下载并超时；app/web/extension 测试入口现统一禁用 managed runtime 下载，生产默认行为和专门 runtime 测试不变。隔离后 Extension/Web 全包通过（1.310s/55.138s），全仓 normal 通过（Knowledge 145.388s、Operations 10.106s、Runtime 13.503s、Storage 9.367s、Web 53.513s），串行全仓 race 通过（Knowledge 518.485s、Operations 30.919s、Storage 106.902s、Web 160.074s、App 15.627s、Scripts 2.951s），并通过 `go vet ./...` 和 `git diff --check`。这些结果只关闭本地回归门，不替代 SmartCare、生产 Agent Host、原生 Linux/macOS、持久卷或旧二进制回滚门禁。
- 当前变更后的 Windows native-host acceptance 通过：`go run ./cmd/release-acceptance -profile host -allow-dirty` 在候选 `f06066f296b74bca0ae8647b1c3219ad4336cdff` 上验证 healthz、同目录重复实例拒绝、终止后同目录重启和 Extension manifest；结果位于 `.tmp/release-acceptance-host-current/20260915-165010/result.json`。该结果仍是脏工作树本地证据，不关闭 clean package、生产 Agent Host、Linux/macOS、持久卷或旧版本回滚门禁。
- release-acceptance clean-candidate 门禁已修正为 `git status --porcelain --untracked-files=all`，未跟踪源码/配置不会再被漏判为 clean；`TestGitStateIncludesUntrackedFiles` 在临时 Git 仓库中独立验证了该行为。当前 tracked+untracked 工作树在不带 `-allow-dirty` 时已于候选构建前拒绝；正式发布仍必须在干净候选 checkout 上执行。
- release-acceptance profile 分流已修正：只有 `host`/`all` 执行 native lifecycle，`core`、`agent`、`web` 只执行各自声明的测试组；新增分流单测通过，修正后的 host 结果位于 `.tmp/release-acceptance-host-profile-fix/20260915-165827/result.json`。
- native acceptance 的终止路径已改为按平台回收 owned PID tree：Unix 进程使用独立 process group，Windows 使用 `taskkill /T` 并保留直接进程兜底；Windows host acceptance 重新通过，结果位于 `.tmp/release-acceptance-host-process-tree-2/20260915-170905/result.json`；Linux amd64 与 macOS arm64 平台文件交叉编译通过，但仍不能替代两平台原生 release-host 运行。
- 验收器/包审计改动后的当前源码全仓 `go test ./... -count=1 -timeout=30m` 全部通过（Knowledge 136.023s、Operations 15.852s、Runtime 18.696s、Storage 7.651s、Web 49.790s、release-acceptance 2.846s）；`go vet ./...` 与 `git diff --check` 通过。该快照只关闭本地回归门，不关闭 SmartCare、生产 Agent Host、跨平台和回滚门禁。
- P0 baseline 指纹修正：同一配置、数据库和本地数据集连续运行 `cmd/architecture-baseline` 两次，均得到 `b7a5ca209cf7bdc28c95e85401327a33ae1d6d4e7dc8554fdcaa5243489cc657`；指纹现排除采集时间、主机容量和绝对路径，仅保留输入摘要。该结果只证明工具可重复，SmartCare 双库仍需现场数据完成 P0-G0。
- 指纹修正后的 `cmd/architecture-baseline` normal/race 包测试及 `go vet ./...` 均通过；该结果关闭工具级可重复性缺陷，不关闭 SmartCare 数据与规模门禁。
- 场景运行器状态聚合补齐回归：`TestFinishMarksUnrunScenarioIncomplete` normal/race 均通过，确认重启、文件锁和磁盘故障等不能由当前 API runner 执行的场景会进入 `incomplete`，不会被误报为 `passed`；`go vet ./cmd/architecture-scenarios` 同样通过。
- P0 场景报告有效性补齐：持续负载现在容忍并记录预期的 429/408/504，而不是首个过载响应即中止；持续场景无成功工作请求时失败，`/api/search` 的 HTTP 200 空 hits 或缺少 generation/source 字段时失败。新增全量拒绝、混合拒绝/成功和空检索结果回归，normal/race 均通过；仍需 SmartCare 固定检索集和规模压力。
- P0 场景 runner 的多场景时限修复：`-duration` 现在只为每个 `continuous-search`/`page-switch` 场景分别建立独立预算，不会因前一个持续场景耗尽共享 context 而把后续分页场景误报为“无成功工作请求”。新增独立 deadline 回归，并在隔离本地实例实跑 `status,single-reindex,continuous-search,page-switch` 通过（报告 `07dedc924fb30a9f90a0861b046d0be450576078174f44e8d55e5d7e3c901dc9`，21,458 requests、1 operation）；该报告是合成本地验证，不替代 SmartCare 双库门禁。
- P5 raw preview 读路径补齐：Web `rawText` 不再绕过统一 API 请求层，现通过带 Agent 前缀、请求超时和路由 `AbortController` 的 text helper 获取 raw 内容；同时修正统一 JSON request helper 原先仅把 route signal 写入待发送选项、随后又被内部 controller 覆盖而未真正传播的缺陷。新增 Node API 回归覆盖 Agent 代理路径和路由取消，`npm run build`、`npm test`、`npm run typecheck` 均通过；仍需真实 Agent Host 快速切页/断线证据。
- P6 关闭预算继续收紧：Storage 新增 `DB.CloseWithContext`，App 关闭时将统一的 15 秒 shutdown context 传入 Writer/SQLite 关闭路径；取消上下文回归证明关闭请求不会等待完整默认预算。`internal/storage` 与 `internal/app` normal/race 定向测试均通过；仍需真实卡死 helper、子进程树和 Linux/macOS release-host 退出证据。
- 当前 legacy shutdown seam 收口后的本地门禁：`go test ./... -count=1 -timeout=30m` 全仓通过，Knowledge 132.287s、Web 44.978s；串行 `go test -race ./... -p 1 -count=1 -timeout=30m` 全仓通过，Knowledge 484.607s、Storage 96.433s、Web 93.120s；另有 `go vet ./...`、Writer static guard、`npm run build`、`npm test`、`npm run typecheck` 通过。该结果只更新本地代码门禁，仍需 SmartCare、Agent Host、release-host、跨平台和回滚证据。
- P6 实例所有权释放也已接入 shutdown context：新增 `InstanceLock.ReleaseWithContext`，取消时仍强制关闭连接以释放 SQLite 所有权，并保留 5 秒兼容 `Release` 包装；新增取消释放后可再次获取实例锁的 normal/race 回归。仍需真实三平台信号、进程树和崩溃重启 release-host 证据。
- 实例锁 context 收口后的最终本地回归：全仓 normal 通过，Knowledge 125.560s、Web 47.691s；串行全仓 race 通过，Knowledge 487.793s、Storage 96.607s、Web 90.606s；随后 `go vet ./...`、Writer static guard 和 `git diff --check` 通过。前端 build/test/typecheck 仍沿用本轮已通过结果；以上均不能替代 SmartCare、Agent Host、release-host、跨平台和回滚证据。
- Windows 本地 host profile 在实例锁 context 改动后再次通过：`go run ./cmd/release-acceptance -profile host -allow-dirty`，报告位于 `.tmp/release-acceptance/20260915-191535/result.json`；候选 SHA `f06066f296b74bca0ae8647b1c3219ad4336cdff`，验证 healthz、重复实例拒绝、终止后同目录重启和 Extension manifest。该结果为 dirty workspace 的本地证据，仍不关闭 Linux/macOS、持久卷、生产 Agent Host 和旧二进制回滚门禁。
- P6-04 CI 执行入口已补齐：新增 tag 或手动触发的 `release-host` 三平台矩阵（Windows/Ubuntu/macOS），各平台运行 `go run ./cmd/release-acceptance -profile host` 并上传隔离 host 证据；该 workflow 只建立可执行门禁，未产生运行结果前不得标记三平台 release-host 通过。
- P3/P6 启动维护取消旁路补齐：`StartBackgroundMaintenance` 的 Durable Operation 状态轮询改用 `Operations.GetContext(maintenanceCtx, ...)`，关闭时不会回到无界 background 查询；App normal 5.426s、serial race 15.971s 通过。仍需真实卡死维护、持久卷和 release-host 退出证据。
- P1B worker/control context 旁路补齐：Durable worker 的 claim 后 operation/payload/cancel 读取、终态 `finish` 与 upload lease 释放均绑定 worker/finalization context；提交后的 operation 读取、上传完成返回、HTTP 幂等预检和 `/api/status` Scheduler 查询也透传请求 context。取消在 pre-dispatch hook 发生时仍让 executor 收到已取消 context；Operations normal/race 7.034s/30.095s、Web normal/race 45.711s/91.587s 通过。仍需 release-host 的 Writer 忙、断线、响应未知和提交后重放证据。
- worker/control context 修复后的当前全仓门禁：`go test ./... -count=1 -timeout=30m` 全部通过，Knowledge 124.221s、Operations 12.185s、Web 46.301s；串行 `go test -race ./... -p 1 -count=1 -timeout=30m` 全部通过，Knowledge 488.511s、Operations 32.276s、Storage 97.286s、Web 91.645s。仍需重新取得前端构建身份、SmartCare、Agent Host、release-host 和回滚证据。
- P1B/P3 元数据读取 context 收口：`SchemaVersionContext`、`StorageFormatContext` 已接入健康检查和 `/api/status`，取消不会退化为无界 `QueryRow`；`TestStorageMetadataContextHonorsCancellation` normal/race 通过。受影响的 Storage/App/Web 定向 normal/race 分别为 7.765s/98.443s、6.798s/16.850s、43.297s/94.169s。
- 元数据 context 收口后的最新本地全量门禁：`go test ./... -count=1 -timeout=30m` 退出码 0，Knowledge 128.985s、Operations 9.457s、Storage 8.210s、Web 48.528s；串行 `go test -race ./... -p 1 -count=1 -timeout=30m` 退出码 0，Knowledge 490.966s、Operations 30.119s、Storage 97.988s、Web 97.430s。`go vet ./...`、Writer guard、`npm run build`、`npm test`、`npm run typecheck` 也通过；这些只关闭当前本地门禁，不关闭 SmartCare、Agent Host、release-host、跨平台持久卷或旧二进制回滚门禁。
- P6-01 启动就绪语义收口：延迟恢复期间 `HealthSnapshot` 现在返回 `status=starting` 且 `ready=false`，不再把后台恢复中的控制面伪报为业务 ready；新增 `TestHealthSnapshotDoesNotReportReadyDuringDeferredRecovery`，同时保留恢复完成后的正常 ready 契约。仍需在生产 Agent Host 验证启动期间的路由/工具展示和恢复完成时间线。
- P6-01 修复后的本地门禁：App/Web/Extension normal 分别 6.694s/44.652s/2.761s 通过，serial race 分别 16.060s/94.340s/6.728s 通过；Windows `host` profile 于 `.tmp/release-acceptance/20260915-202626/result.json` 通过，验证 healthz、重复实例拒绝、终止后同目录重启和 manifest；本机 Agent 安装/移除门禁也通过（18/0 工具、1/0 路由、移除态健康）。
- P6-01 修复后的最新全仓回归：`go test ./... -count=1 -timeout=30m` 退出码 0，Knowledge 127.078s、Operations 12.179s、Storage 9.129s、Web 50.359s；串行 `go test -race ./... -p 1 -count=1 -timeout=30m` 退出码 0，Knowledge 493.687s、Operations 30.663s、Storage 98.541s、Web 96.735s。该结果仍只关闭本地回归门，不关闭 SmartCare、生产 Agent Host、Linux/macOS 原生 release-host、持久卷故障和旧二进制回滚门禁。
- P1B worker-read 复核再补一处：重建/重新导入前的旧 generation embedded chunk 统计新增 `countEmbeddedChunksByDocGenerationContext`，取消或数据库读取失败会 fail closed，不再把错误吞成“没有旧向量”；新增 `TestEmbeddedChunkCountHonorsCanceledContext`。仍需重新取得 Knowledge 全包 normal/race 和 SmartCare 规模长读证据。
- P1a/P1b 读取静态审计再收口：遗留 `hasContentHash` helper 新增 context 实现和兼容包装，避免未来复用时直接调用无 context 的 `QueryRow`；新增 `TestContentHashLookupHonorsCanceledContext`。
- 上述 P1a/P1b 读取 seam 的 Knowledge 全包复核已完成：`go test ./internal/knowledge -count=1 -timeout=30m` normal 通过（121.326s），`go test -race ./internal/knowledge -p 1 -count=1 -timeout=30m` 也通过（489.843s）；`go vet ./...`、Writer/Operation handler guard 和 `git diff --check` 通过。该结果只更新 Knowledge 本地包级证据，不替代 SmartCare 规模、Agent Host、release-host、跨平台和回滚门禁。
- 远端 CI 复核（2026-09-16）：最新公开 `master` 成功运行 [34157958853](https://github.com/shutu-ai/Shutu-Knowledge/actions/runs/34157958853) 对应 SHA `180d8a9238c9259ba62d14c9c55d3b77d787aecc`，不是当前工作区 HEAD `f06066f296b74bca0ae8647b1c3219ad4336cdff`；当前工作区仍有未提交变更，因此该 CI 结果只能作为历史参考，不能关闭本候选的 CI 门禁。
- 正式打包入口在当前工作区按预期于生成产物前停止：`scripts/package_release.ps1` 检测到未提交文件并拒绝继续；这证明 clean-checkout 保护生效，但不是包审计或部署 smoke 证据，必须在候选提交或导出到干净 checkout 后重跑。
- 旧 jobs seam 收敛：Knowledge Service 已移除 legacy `jobs.Manager` 字段及 `ImportDirectoryTree`、`RescanDirectory`、`ReindexBase` 三个真实执行的异步兼容方法；旧目录/重建测试改为直接调用 `RunDirectoryImport`、`RunDirectoryRescan` 和有界 `ForEachReindexDocumentBatch`。全仓 normal/race、`go vet`、Writer/Operation guard 和 Web build/test/typecheck 均通过；Web/Agent 的旧 `jobId` 兼容仍由 Durable Operation 映射承载。
- 发布工作流补齐：`.github/workflows/ci.yml` 新增仅 tag/手动触发的 Windows `release-package` job，执行 `scripts/package_release.ps1` 的 clean-checkout、版本/存储格式核对、归档解压验证和静态密钥审计，并上传 formal package artifact；该 workflow 变更仍需在远端候选 SHA 上实际触发，不能用本地工作区结果替代。
- 场景 runner race 稳定性修复：`TestRunContinuousAllowsExpectedRejectionsWithSuccessfulWork` 原先使用 20ms 测试观察窗口，在 `-race` 下首个预期 429 会耗尽预算，无法观察后续成功请求；仅将测试 harness 窗口调为 250ms，未改变生产请求/场景阈值。该测试 normal/race 各连续 3 次通过；随后全仓 normal 通过（Knowledge 127.873s、Operations 11.716s、Storage 8.541s、Web 47.478s），serial race 通过（Knowledge 498.574s、Operations 30.760s、Storage 97.036s、Web 92.349s）。
- P1B 解析取消边界补齐：`Registry.ParseContext` 将 Worker context 传入可阻塞的 legacy helper，`parseFileContent`/URL ingest 不再调用无 context 的解析入口，MinerU 轮询等待改为可取消 timer；`TestRegistryParseContextPropagatesCancellationToLegacyHelper`、`TestParseFileContentHonorsCanceledContextBeforeParser`、`TestMineruPollWaitHonorsCancellation` normal 回归及 `go vet ./internal/parser` 通过。该结果关闭本地解析等待 seam，不替代远端服务中断和 release-host 重放证据。
- URL 解析失败的终态写入也已收敛到 `failDocumentContext`，不再因兼容包装器绕过请求 context；`TestURLImportAndRefresh` 与取消解析回归 normal/race 均通过，随后 Knowledge 全包 normal/race 分别以 114.779s/478.959s 通过。仍需 Agent Host 断线和远端 URL 中断后的持久化未知提交演练。
- 解析取消 seam 修改后的当前全仓回归已重新通过：`go test ./... -count=1 -timeout=30m` normal 全部包通过（Knowledge 130.045s、Web 48.249s）；串行 `go test -race ./... -p 1 -count=1 -timeout=30m` 也全部包通过（Knowledge 481.666s、Operations 29.562s、Storage 98.702s、Web 90.611s），无 race 报告或超时。该结果只更新本地 normal/race 门，不关闭 SmartCare、release-host、Agent Host、跨平台和回滚门禁。
- 一次并发执行 `go test -race ./... -count=1` 受到 Windows 资源争用影响，出现 Knowledge 超时和 Operations p95 断言失败；两个受影响测试分别重跑通过，不能把该次并发结果记为全量 race 通过，也不能直接记为代码回归失败。
- 本地结果只关闭对应的回归项；SmartCare 双库规模、持久卷 ENOSPC/文件锁、Linux/macOS release-host、真实 Agent Host 和备份回滚仍必须分别产生报告。
- 删除重放语义复核后，`delete_document`、`delete_directory`、`delete_base` 的
  resolved 分支会先确认对象仍处于 `deleting` 生命周期，再恢复有界 raw/chunk
  物理清理；active 或已不存在对象的历史 marker 仍保持兼容 no-op。目录导入、
  重扫和删除的失败 marker 写入错误不再被忽略。新增
  `TestDurableDeleteReplayResumesCommittedPhysicalCleanup`，并通过 App normal/race
  与全仓 normal 回归。对应计划验收见 P1B-01 和执行表第 4 项；仍需真实
  release-host 的 raw/Writer 故障重放。
- 在不改写当前工作区的临时干净候选 checkout 中，正式 Windows package 已完成：
  11 个文本文件静态密钥审计通过，ZIP SHA-256 为
  `121b66782648de2096aac53e438dd14268a0bf00b724845ba6c24e3eddbec581`，候选提交
  为 `78181f2bc0723ceffa3d95d67e166c66b3b3226f`；解包 identity profile 和 Windows
  native-host profile 均通过。该证据仍不能替代远端候选 CI、部署 smoke、生产
  Agent Host、原生 Linux/macOS、持久卷和旧二进制回滚。
- 删除重放修复后的当前本地门禁已刷新：串行全仓 `go test -race ./... -p 1
  -count=1 -timeout=30m` 全部通过（Knowledge 500.442s、Operations 30.315s、
  Storage 98.310s、Web 95.051s、Scripts 3.196s），Web `npm test`、
  `npm run typecheck`、`npm run build` 也通过。该证据只关闭本地 normal/race/Web
  门，不关闭 SmartCare、真实 Agent Host、跨平台 release-host、持久卷或回滚门禁。
- 生命周期锁与启动恢复就绪语义修复后的最新当前源码全量回归已通过：
  `go test ./... -count=1 -timeout=30m` 退出码 0（Knowledge 138.477s、
  Operations 12.645s、Runtime 18.614s、Storage 9.837s、Web 56.325s）；
  串行 `go test -race ./... -p 1 -count=1 -timeout=30m` 退出码 0
  （Knowledge 527.945s、Operations 30.558s、Runtime 41.264s、Storage
  103.965s、Web 99.430s），无 race 报告或超时。该证据更新当前本地代码门，
  仍不关闭 SmartCare、真实 Agent Host、持久卷、原生 Linux/macOS 和旧二进制
  回滚门禁。
- 同一当前源码快照的静态与 Web 门禁也取得明确退出码：`go vet ./...`、
  `go test ./scripts -run 'Test(WriterGuard|OperationHandlerGuard|GenericOperationHandlerGuard)$'`
  通过，Web `npm test`、`npm run typecheck`、`npm run build` 均通过（构建
  生成 6 个 Web 文件）。这些结果只更新本地静态/契约门禁，不关闭外部规模、
  Agent Host、跨平台 release-host 或回滚门禁。
- P1a/P2 只读隔离旁路已修复：App 健康检查、Web `/api/status` 和 doctor
  的 storage-format/schema 查询统一使用专用 `ReadDB()`，不再与长写事务共享
  写连接；`cmd/shutu-knowledge`、`internal/app`、`internal/web` 的 normal
  与串行 race 定向回归均通过。该结果关闭当前只读连接选择的本地缺口，仍需
  Writer 忙和真实规模控制面延迟证据。
- P6-03 HTTP 关闭预算已补齐：`cmdServe` 的 `http.Server.Shutdown` 现在使用
  15 秒 deadline，不再传入无界 `context.Background()`；新增不合作 handler
  的超时回归，`cmd/shutu-knowledge` normal/race 均通过。该结果关闭本地
  HTTP 关闭边界缺口，仍需真实卡死 helper、子进程树和三平台 release-host
  退出证据。

---

## 10. 测试矩阵

### 功能测试

- 单文件导入、目录递归导入、URL 导入。
- 单文档和目录重新索引。
- 单文档、目录、批量和知识库删除。
- Embedding/Rerank 本地模型配置和实际调用标识。
- 任务取消、重复提交和冲突任务。

### 并发压力测试

- SmartCare 产品数据字典单库重建索引。
- SmartCare Suite 单库重建索引。
- 两库同时重建索引。
- 一个库导入、另一个库删除。
- 多个文档同时重新索引。
- 后台任务运行期间持续切换页面并查询统计。

### 故障测试

- 解析中强制退出。
- Embedding 调用中强制退出。
- Chunk/FTS 写入中强制退出。
- raw 文件移动中强制退出。
- 删除和重建索引同时发生。
- 磁盘空间不足、权限不足、文件被占用。
- Agent 连接中断、Worker 退出、服务重启。

### UI 测试

- 任务从导入页启动后切换到文档页。
- 多个任务同时运行时切换模型和设置页。
- 浏览器刷新后恢复任务状态。
- 连续快速切换路由。
- 后台刷新与用户点击同时发生。
- API 超时、服务器返回 500、任务失败时页面仍显示可操作错误态。

### 必须具备的契约与故障断点测试

| 编号 | 场景 / 注入点 | 必须观察到的结果 | 最早门禁 |
|---|---|---|---|
| C01 | Web/Agent 导入返回 202 后，Worker 随后失败 | 先显示接收，最终显示失败；无提前成功和虚构 Chunk 数，终态可查 | P1b |
| C02 | 命令事务提交后、唤醒 Dispatcher 前强退 | 重启可查原命令且自动补领；输入与目标不变 | P1b |
| C03 | 文档发布/分项结果已提交，父终态尚未写入时强退 | 补齐结果，不重复创建文档、不重复执行已提交子项 | P1b |
| C04 | 响应丢失、相同键并发提交、删除后/队列满后原键重试、相同键不同输入、结果早于凭据到期、密钥轮换与重启 | 同请求同 Operation；已接收请求不重新执行准入检查；有效旧凭据的去重摘要不提前清理；不同请求 409；到期已清理凭据 410；无重复业务效果 | P1b |
| C05 | Writer 正忙时持续提交/查询/取消，或注入持久化失败 | 有界响应；成功必持久化；不确定结果沿原键核对；无内存成功 | P1b |
| C06 | 扫描已读目录清单后并发删除祖先，再创建新的子文档 ID | 子项写入被栅栏拒绝；Web/Agent 不显示后代；重启不复活 | P1a |
| C07 | 文档/祖先删除与重建发布、对象移动、旧 attempt 回调竞争 | 条件提交唯一裁决，旧写入/移动不能逃逸删除范围 | P1a |
| C08 | 固定视图后首次召回前、或词法召回后切换 generation，在模型/邻居读取期间触发 GC | 已固定的 retired 版本仍可召回，所有证据使用固定版本；读者释放前旧数据不被物理回收 | P1a |
| C09 | 查询进行中删除对象，或打开历史 raw/Chunk 引用 | 最终可见性校验排除对象；历史接口不绕过 tombstone | P1a |
| C10 | 模型 A→B 迁移失败、同维度不同模型、模型禁用 | 不混用向量空间；旧索引仍可解释地检索或明确降级 | P1a |
| C11 | queued 取消、取消意图持久化后强退、取消/发布同时提交、重复 retry、删除后取消、批量部分完成后取消 | revision/attempt 单调；取消意图恢复后只清理不重跑；成功不回退；删除已提交返回明确边界，已提交子项继续清理，未提交项停止且结果保留 | P1b |
| C12 | staging 绑定前后、raw/quarantine 移动前后强退 | 输入所有权可恢复，清理不删活任务输入；磁盘占用最终收敛 | P1a 基础 / P3 完整 |
| C13 | 旧同步客户端、旧 jobId 客户端、缓存 Web、真实 Agent 代理 | 升级要求明确；兼容状态正确；查询/取消/重试和 URL 前缀可达 | P1b |
| C14 | 多 generation/删除中状态下回退兼容版本及恢复备份 | 兼容回退无未发布索引可见、无被删对象复活；备份恢复按声明恢复点核对新增/删除差异，验证检索、raw 和任务结果 | P1a 基础 / P3 大库 |
| C15 | 超容量提交、大文件解析、慢模型、慢读者、长期失败清理 | 队列/内存/磁盘均有界，有效吞吐与拒绝率同时报告 | P1b 静态拒绝 / P2 完整 |
| C16 | 启动恢复和新任务接收竞争、退出超时、重复启动 | 新任务不被误标旧任务；实例所有权生效；未提交任务不虚报完成 | P1b 基础 / P6 完整 |

这些测试要求真实数据库、任务循环和可控故障点，不以纯状态结构单元测试替代。断点要覆盖事务提交前后，不能只在解析函数里返回一个错误。基础正确性用隔离数据集运行；两套 SmartCare 大库用于后续相同契约的规模与压力验证，不直接破坏用户正在使用的知识库。

---

## 11. 回滚策略

每阶段必须声明“回退功能”与“恢复旧数据”的区别。新增字段/保留旧字段不意味着旧二进制能够安全读写；旧代码按文档读取/删除所有 Chunk，也不认识 tombstone，因此不能在新格式数据上直接回退到任意历史版本。

### 11.1 兼容基线与迁移门禁

- P1a 建立一个具体发布版本及提交 SHA 的兼容基线，识别 generation、source version、lifecycle、Operation 结果/清理标记及输入所有权，全部读写和旧恢复路径都遵守新约束。其后续可承接的命令 schema 和格式版本须明示。
- 数据目录保存 `storage_format_version`、`min_reader_version`、`min_writer_version` 和迁移状态。新运行入口在执行恢复/Worker 前检查；不兼容时停止写入并说明原因。发布启动流程不得将不识别版本标记的历史二进制指向升级数据目录；它们只能操作隔离的兼容备份。
- 初始扩展迁移新增表/字段并为旧数据赋 generation 0，保留旧 ID。删除状态用新增 `lifecycle_state`；如必须调整旧表 CHECK/触发器，单列建表复制、索引/触发器重建及完整性验证步骤，不通过改旧 migration 文件绕过现有数据库。
- 开启第二 generation、tombstone 或新命令类型前，验证兼容基线已可读取它们，并写入新的最低兼容版本。每次提高该版本都是回滚边界，不能只用功能开关掩盖。
- 首次迁移前停止接收和全部 Worker/GC，等待写入静止，生成经过恢复校验的 SQLite 一致性备份及其匹配的 raw/source、命令输入、配置和版本清单。不能在 WAL 正写入时只复制主 `.db` 文件。迁移阶段保留足够空间，失败不得销毁旧副本。

### 11.2 按阶段回退矩阵

| 阶段 | 可执行的回退 | 不允许的回退 |
|---|---|---|
| P0 | 回退观测代码/配置，保留基线报告 | 用不同负载重新定义已报告的通过结果 |
| P1a，尚未启用新格式写入 | 迁移验证后按声明的旧格式兼容路径回退，必要时恢复迁移前备份 | 未核对格式就启动旧程序 |
| P1a/P1b，已产生新格式数据 | 停收新命令，停止/核对 Worker，回退到能识别当前格式及命令的兼容基线；无法识别的命令保留 interrupted 和输入引用 | 在新数据上启动原始旧版，重启旧恢复器或改回同步长请求 |
| P2/P3 | 回退调度策略、批次参数或维护策略；暂停物理 GC，保留 Writer、栅栏、读视图和持久化恢复 | 关闭 tombstone/generation 检查、删除任务日志或忽略输入所有权 |
| P4/P5 | 回退到兼容的分页/任务 UI 版本，SSE 降级为轮询/任务查询页 | 回退到把 202 当完成的 UI |
| P6/P7 | 回退到同格式、同任务协议的单进程 Worker；先确认另一执行者停止 | 两种 Worker 同时拥有同一任务/数据目录 |

配置开关只能切换符合相同一致性契约的实现，不能重新启用绕过 Operation/Writer 的业务路径。退役 generation 和 quarantine 清理可以暂停；已经提交的逻辑删除不因暂停清理而撤销。

### 11.3 不兼容版本的备份恢复

确需回到兼容基线之前的版本时，使用停机恢复流程：停止所有执行者并保留当前数据快照 → 在隔离目录恢复匹配版本的数据库/raw/输入与配置 → 验证检索、删除可见性、原文、任务与计数 → 明确备份时间之后的新增/删除/任务差异及无法重放部分 → 切换运行目录。备份恢复会回到备份时点，可能重新出现之后删除的对象，必须在恢复说明中列出并按已确认的恢复范围处理，不能宣称无损或直接覆盖现有数据。

每次演练记录实际版本/SHA、备份时间与 Hash、命令 schema、耗时、数据差异、失败处理和新数据保留位置。禁止使用 `git reset --hard` 或覆盖用户工作区实现回滚。P1a 未完成一次隔离恢复演练不得启用新格式写入；P3 必须补齐两库规模下的迁移/回退/恢复证据。

---

## 12. 风险与决策原则

### SQLite 是否需要替换

SQLite 对单用户、中等规模知识库仍然适用。当前优先解决的是长事务、任务绕过队列、FTS 写放大和一致性，而不是立即更换数据库。

只有当以下指标经过优化仍不达标，才评估 PostgreSQL：

- 写入队列持续积压。
- 单写连接成为稳定瓶颈。
- WAL 和数据库体积影响恢复时间。
- 多进程或多用户并发成为明确需求。

### 是否需要向量数据库

先记录向量检索耗时、候选 Chunk 数、内存和 p95。当前暴力向量扫描只有在真实基准证明是瓶颈后才迁移。

### 是否需要拆进程

如果单进程完成 Scheduler、短事务、异步 API、前端状态隔离和启动治理后仍无法满足 Web SLO，再拆 Control/Worker。提前拆分会增加 RPC、部署、恢复和一致性复杂度。

---

## 13. 任务卡片、代码盘点与交付格式

每个任务必须单独填写以下卡片；卡片缺少资源上限、失败行为、回滚方式或验证命令时，不得进入实现或合并。

```text
任务编号/名称：P*-*
负责人/评审者：
当前状态：未开始/开发中/本地通过/release-host 待验/SmartCare 待验/门禁通过
对应不变量：A/B/C/D/E/F/G
依赖任务及启用门禁：
问题证据与当前行为：
目标行为与不变行为：
涉及文件、数据库表、API、Agent 工具和前端状态：
命令 schema、输入引用、fingerprint/idempotency、retention：
业务提交点、分项 marker、attempt/revision、恢复依据：
generation/source/model、目标/祖先 epoch、读视图和 GC 边界：
资源需求、队列、批次、deadline、内存/磁盘/上传配额：
成功、拒绝、错误、超时、取消和响应未知行为：
测试命令、故障断点和 C01-C16 对应项：
压力输入、环境、指标、阈值和有效吞吐：
证据文件路径、Git SHA、二进制 SHA 和日志摘要：
回滚目标版本/SHA、格式边界、备份 Hash 和恢复点差异：
偏离本文档：否/是（若为是，填写原因、风险、替代方案和回滚）
```

代码盘点必须覆盖以下模式，不能只查 HTTP handler：

- SQLite 的 `Exec`、`ExecContext`、`Begin`、`BeginTx`、`Commit`、`Rollback` 和直接 SQL 写入。
- raw/staging/quarantine 的创建、写入、rename、删除和恢复。
- 所有 `go` 后台 goroutine、`jobs.Manager`、Worker、Scheduler、模型调用和外部进程启动。
- Web/Agent 入口、旧 `jobId` 适配、任务查询/取消/重试和前端轮询。
- 完整列表读取、统计扫描、重建/删除前的数量计算及缓存失效。

盘点结果必须形成“入口 → Operation → resource lane → Writer/文件发布 → 终态/清理”的链路表；任何没有 owner、deadline、失败补偿和恢复依据的入口都保持未闭合。

---

## 14. 分批执行顺序与下一批任务

以下是从当前评审状态开始的实际执行顺序。每一批都设置停止条件；停止条件未通过时，可以修复当前批次，但不能启用下一批次的生产路径。

### 14.0 评审后当前执行队列

以下队列是当前唯一的优先顺序。每一行关闭前，不应把后续阶段的本地测试数量当作进度替代；代码变更必须回填到对应任务卡片和 evidence 文档。

| 顺序 | 当前动作 | 前置条件 | 必须交付 | 关闭条件 |
|---:|---|---|---|---|
| 1 | 关闭 P0-01～P0-G0 的现场基线 | 可访问 SmartCare 产品字典库和 Suite 库的隔离副本 | `input-inventory.json`、`api-scenarios.json`、`retrieval-regression.json`、配置/环境/预算对账表；固定 SHA 和 `reportFingerprint` | 双库冷/热启动、成功/拒绝混合负载、固定检索集和资源峰值均可重复；否则停留 P0 |
| 2 | 关闭 P1A-01～P1A-G1 的安全启用条件 | P0 配置和数据集已冻结 | 全仓写路径盘点、兼容格式说明、隔离备份/恢复记录、剩余大事务拆分和 C05-C10 故障报告 | 生产调用图无 Writer 旁路，迁移前后恢复可验证，才允许继续开放/扩大异步入口 |
| 3 | 收敛 P1B-01～P1B-G2 的命令语义 | P1a-G1 通过 | 剩余聚合命令的业务提交/失败 marker 表、输入 lease 所有权表、旧 `jobs` 兼容 seam 清单、C01-C05/C11/C13/C16 重放报告 | 所有长任务均可从 envelope 重放；响应未知、终态丢失、取消和失败重试不重复业务效果；否则不得宣称异步迁移完成 |
| 4 | 关闭 P2-01～P2-G3 的资源与公平性 | P0 预算冻结、P1b 命令边界稳定 | 双库持续压力、跨入口等价键并发、慢模型/慢读者、低水位和持久卷锁/空间测试；同时报告有效吞吐、拒绝率、RSS/WAL/temp | 两库不互相饿死，资源峰值和收敛时间在预算内；只有 p95 或全量拒绝时保持未闭合 |
| 5 | 关闭 P3-01～P3-G4 的规模维护和回滚 | P2-G3 通过或已记录可接受的资源边界 | SmartCare 规模 GC/长读/模型切换、持久卷 ENOSPC、文件锁、隔离备份恢复和旧兼容二进制回滚报告 | generation/raw/任务结果可恢复，数据差异可解释，故障后收敛有界 |
| 6 | 完成 P4-G5 与 P5-G6 的真实页面验收 | P1b 协议稳定，复用 P0 数据与配置 | 大目录分页/计数预算、浏览器版本/构建 SHA、Agent Host 持续任务/断线/刷新/路由切换日志和截图 | 无全库读取、空态死路、stale overwrite 或把 202 显示为成功；本地 E2E 不能替代 Agent Host |
| 7 | 关闭 P6-01～P6-G7 并做最终 Go/No-Go | P3/P4/P5 证据齐全 | Windows/Linux/macOS 实例锁、重复启动、进程树回收、SIGTERM/CTRL+C、重启恢复、退出超时和脱敏日志 | 三平台 release-host 证据齐全，且发布包审计、部署 smoke、格式/回滚核对全部独立通过；否则不得进入发布 |

执行期间如现场输入、平台权限或 release-host 不可用，任务状态必须记为 `release-host 待验` 或 `SmartCare 待验`，不得用本地 mock、WSL2、交叉编译或单次成功日志替代。

### 14.0.1 当前快照与重新生成后的落点

截至 2026-09-16，代码和本地回归已经形成 P1a/P1b/P2/P3/P4/P5/P6 的多个纵向切片，但没有任何外部环境门禁可以由本地测试自动推导。后续执行应按下面的状态推进：

| 阶段 | 当前状态 | 已有证据 | 下一步唯一落点 |
|---|---|---|---|
| P0 基线 | SmartCare 待验 | baseline/scenario/retrieval 工具和本地合成场景可重复 | 取得两套 SmartCare 隔离库，冻结输入/配置/model SHA，生成双库固定报告 |
| P1a 存储基础 | 部分完成/本地通过 | 唯一 Writer、格式兼容、generation/tombstone、恢复与写路径 guard | 在冻结快照上完成全仓写路径链路表、隔离备份恢复、C05-C10 故障断点和剩余旁路清单 |
| P1b Durable Operation | 部分完成/本地通过 | envelope、marker、lease、取消、恢复、旧 jobs 兼容 seam、worker/control context；删除逻辑栅栏提交后会继续物理清理，目录失败 marker 和同步状态写入失败会显式返回；expired/released lease 先清理 staging 再发布状态，终态写入失败会做一次幂等重试 | 在真实调用方上做响应未知、提交后终止、删除/目录/基座物理清理失败后的重放、失败重试和 upload lease 对账 |
| P2 资源边界 | 本地通过/规模待验 | 双 lane、配额、fairness、RSS/WAL/temp 和资源回归 | 用两套 SmartCare 库跑持续混合负载，报告有效吞吐、拒绝率、峰值和收敛时间 |
| P3 维护与回滚 | 本地通过/release-host 待验 | 启动维护、GC/长读、备份恢复和本地故障夹具 | 在持久卷执行 ENOSPC、文件锁、长读、模型切换，并用旧兼容二进制验证回滚 |
| P4 Web 控制面 | 本地通过/大目录待验 | 分页、取消、请求层、状态字段和本地 Web 回归 | 在大目录上测返回字节、SQL 行数、p95、取消和非全库读取 |
| P5 Agent 集成 | 本地通过/Agent Host 待验 | Agent 安装/移除门禁、路由/工具契约和本地 E2E | 在生产 Agent Host 做持续任务、断线、刷新、路由切换和重启时间线 |
| P6 生命周期 | Windows 本地通过/三平台待验 | Windows host profile、owned PID tree、实例锁和有界关闭 | 运行干净候选 checkout 的 Windows/Ubuntu/macOS release-host 矩阵，补 SIGTERM/CTRL+C、进程树、持久卷和残留 PID 证据 |
| 发布门禁 | 未闭合 | 本地 Go/Web/guard 已通过，CI release-host 与 tag/manual `release-package` 入口已建立 | 完整 normal/race/vet/contract、干净包审计、部署 smoke、格式/回滚核对；外部发布另行授权 |

执行规则：每个阶段完成后必须新增一条带命令、环境、输入 SHA、报告 fingerprint 和限制说明的 evidence；如果只有本地证据，状态最多写“本地通过”。任何一行的唯一落点未完成时，不得把后续阶段的测试数量累计为总体完成度。

### 14.1 当前评审后的可执行任务卡

以下是从 2026-09-16 本地验证快照继续推进时的最小闭环。角色名是责任角色，不代表已经分配给具体个人；每项关闭时必须把实际负责人、提交 SHA 和证据路径填入 §13 卡片及 `docs/architecture_refactor_evidence.md`。

| 顺序 | 对应任务 | 责任角色 | 前置输入 | 具体动作与命令 | 必须交付 | 关闭条件 |
|---:|---|---|---|---|---|---|
| 1 | P0-02/P0-05 | 知识库负责人、产品数据负责人 | SmartCare 产品字典库和 Suite 隔离副本、脱敏配置、可复现应用版本 | 停止写入或建立一致性快照；运行 `cmd/architecture-baseline`；由现场维护 `retrieval-corpus.json` 后运行 `cmd/retrieval-regression` | 两库 `input-inventory.json`、固定检索集、数据/配置/model SHA、`reportFingerprint` | 两次运行指纹一致；固定 case 的正/负样本、generation/source/model 字段齐全；缺数据时标记 `SmartCare 待验` |
| 2 | P0-01/P0-03/P0-04/P0-G0 | 性能负责人、后端负责人、评审者 | P0-02 输入冻结；同一配置快照；可记录 RSS/WAL/temp/free bytes 的主机 | 运行 `cmd/architecture-scenarios` 的单库、双库、跨库、持续检索和分页场景；冷/热启动分开跑；重启、锁、OS 空间故障交给 release-host 控制器 | `api-scenarios.json`、预算对账表、成功/拒绝/错误/超时/取消、有效吞吐、p50/p95/p99、阶段 eventTrace 和资源峰值 | 存在有效成功样本且不是全量拒绝；每类操作可关联阶段指标；预算、配置和报告 fingerprint 可对账 |
| 3 | P1A-01～P1A-06 | 存储负责人、架构评审者 | P0 配置/数据集冻结；候选版本和兼容版本 SHA | 完成 `Exec/Begin/Commit` 全仓盘点、Writer 静态 guard、格式边界核对；在隔离目录执行备份/恢复、Writer 忙、长读、重建/删除竞争和批次前后强退 | 写路径链路表、format/reader/writer 说明、备份 Hash、恢复差异、C05-C10 日志与数据库快照 | 生产调用图无 Writer 旁路；每条写路径有 owner/deadline/补偿；旧格式拒绝边界和隔离恢复可验证 |
| 4 | P1B-01～P1B-07/P1B-G2 | Operation 负责人、API/Agent 负责人 | P1a-G1；真实 HTTP/Extension/旧 `jobId` 调用方；可控进程终止 | 在提交后/唤醒前、业务发布后/终态前、取消竞态、响应丢失和重试点终止进程；重放单项、批量、目录、RestoreBase、模型和维护操作；人为制造 raw 删除/Writer 失败，核对逻辑栅栏与物理清理是否分阶段收敛 | envelope/marker/lease 对账表、C01-C05/C11/C13/C16 重放报告、未知提交和失败重试快照、deleting tombstone/raw/chunk 清理前后对账 | `202` 只表示已接收；原键可查回；输入 lease 无孤立；业务发布与 marker 不重复；已提交删除栅栏不会因 marker fast-path 遗留物理清理；marker 写失败不被吞掉；旧 jobs 不执行实际业务 |
| 5 | P2-01～P2-05/P2-G3 | 调度负责人、性能负责人 | P0 预算；两套 SmartCare 数据；慢模型、慢读者和持久卷 | 持续提交两个 base 的混合负载，叠加同键跨 Web/Agent/通用入口并发、删除优先级、低水位、锁和空间压力；记录 rejection 与有效吞吐 | 双库公平性、等价键、资源 reservation、RSS/WAL/temp/free bytes、拒绝原因和收敛时间报告 | 两库均有有效吞吐；无永久饥饿/无界队列；资源不越过预算；锁/ENOSPC 只影响可重试输入，不破坏 active 数据 |
| 6 | P3-01～P3-05/P3-G4 | 维护负责人、发布负责人 | P2 资源边界；SmartCare 规模副本；持久卷；旧兼容二进制 | 运行启动维护、GC/长读、模型 A→B/失败 C、持久卷 ENOSPC、文件锁；建立隔离备份并用旧二进制执行回滚 | 维护积压与空间收敛、模型/读视图、备份 Hash/大小/耗时、恢复差异和旧版本回滚报告 | 长读期间不误删；失败不替换 active；旧二进制明确拒绝新格式且不写坏目录；恢复后的检索/删除/raw/任务/计数可核对 |
| 7 | P4-01～P4-G5 | Web/Knowledge 负责人 | P0 大目录数据和预算；同一构建 SHA | 测量重建、目录导入/删除、索引状态、RestoreBase、模型选择和 Extension 分页的返回字节、SQL 行数、p95；确认旧无参数接口退役日期 | 大目录预算报告、兼容接口弃用决定、分页/取消日志 | 所有控制读有界；取消可传播；不物化全库；接口预算与 P0 对账一致 |
| 8 | P5-01～P5-G6 | 前端负责人、Agent 集成负责人 | P1b 协议；真实 Agent Host；浏览器版本与构建 SHA | 连续提交任务并执行断线、刷新、路由切换、服务重启；核对 OperationStore、stale-response、空态和 202 展示 | 浏览器日志、截图/录屏、Agent Host 版本、断线/重启时间线 | 无旧响应覆盖新路由；刷新后任务和结果一致；无空态死路；真实 Agent Host 通过而非仅本地 E2E 通过 |
| 9 | P6-01～P6-G7 | 平台/运行时负责人 | P3-P5 证据；Windows/Linux/macOS release-host；可控 helper | 各平台执行实例锁、重复启动、端口错误、SIGTERM/CTRL+C、子进程树回收、卡死 helper、恢复和 Agent 重启；记录 owner PID 树 | 平台/OS/架构/二进制 SHA、退出码、进程树、stderr 脱敏摘要、退出耗时和残留 PID | 三平台真实运行证据齐全；只终止 owned PID 树；所有 Close/Stop 在预算内；无孤儿进程 |
| 10 | P0-G0～P6-G7、发布门禁 | 发布负责人、评审者 | 所有阶段证据；干净候选 checkout；部署环境 | 运行完整 normal/race/vet/contract；执行 `scripts/package_release.ps1`；静态审计 ZIP 密钥/凭据；部署 smoke；核对版本与 storage format | 最终 Go/No-Go 表、发布 ZIP 审计、部署 smoke、回滚说明 | 所有门禁为 `门禁通过`；发布包与源码/格式一致；外部上传仍需单独明确授权 |

执行细则：

1. 第 1、2 项可以在 P1a 代码审计期间准备，但 P0-G0 未通过前不得冻结新的并发、批次或拒绝阈值。
2. 第 3、4、5、6 项必须沿用同一数据集 SHA、配置快照和报告时间窗；更换其中任何一项都要重新标记为新基线。
3. 本地 normal/race、Windows native host、WSL2 smoke 和本地故障夹具只能填写“本地通过”；SmartCare 数据、持久卷、生产 Agent Host、Linux/macOS 原生运行和旧二进制回滚必须分别取得对应环境证据。
4. 任一任务出现 `unrun`、缺少有效成功样本、只验证 HTTP 200、只验证状态单元测试或无法说明回滚目标时，任务保持 `未闭合`，后续门禁不得顺延关闭。

### 批次 A：P0 基线和观测

执行 `P0-01` 至 `P0-05`，先补指标、数据集、场景脚本、预算和固定检索集。输出一份改造前基线报告和一份可重复运行命令清单。

停止条件：任何关键指标缺失、输入 Hash 不稳定、双库副本不可重复、请求全部被拒绝或没有报告 RSS/WAL/临时空间时，停止在 P0。

### 批次 B：P1a 安全写入前置

依次执行 `P1A-01`、`P1A-02`、`P1A-03`。先建立兼容格式和恢复备份，再实现唯一 Writer，最后迁移全部 SQLite 写入。此批次不得通过“仍保留旧路径但新代码通常不用”来验收。

停止条件：仍能从生产调用图到达直接 `db.Exec/Begin`、Writer 忙时控制接口无界等待、或迁移前备份无法在隔离目录恢复时，不能进入 P1b。

### 批次 C：P1a 批次事务和生命周期闭合

执行 `P1A-04` 至 `P1A-06`：拆分 Chunk/FTS/vector/delete 事务，审计 generation/source/model 和祖先栅栏全路径，完成旧 jobs 的接管设计和维护调度边界。运行 C05-C10 基础故障测试及 P1A-G1。

停止条件：重建失败会丢 active、删除后旧任务仍能写入、历史读引用会被 GC 破坏、或存在无法说明的 raw/SQLite 中间状态时，不能开放完整异步接收。

### 批次 D：P1b Operation 和调用方闭环

执行 `P1B-01` 至 `P1B-07`。优先补齐命令语义和业务提交 marker，再迁移旧 jobs、同步 RestoreBase/模型维护路径，统一 Web/Agent/旧 API 的状态语义、请求 context 与配额。运行 C01-C05、C11、C13、C16；其中控制请求取消必须与已接收 worker 的独立生命周期 context 分别验证。

停止条件：202 被解释为完成、响应未知只能重新创建任务、旧 jobs 仍执行实际业务、存在孤立 upload/staging、或失败重试产生第二个业务效果时，不能通过 P1B-G2。

### 批次 E：P2 资源边界

执行 `P2-01` 至 `P2-05`，用 P0 基线设置 queue、per-base、memory、upload、temp disk 和 model 预算。持续测试至少包含两个知识库、慢模型、慢读者、删除优先级和磁盘低水位。

停止条件：只报告 p95、不报告有效吞吐和拒绝率，或配额只限制任务数而不限制字节/磁盘/RSS 时，不能通过 P2-G3。

### 批次 F：P3 规模化恢复和回滚

执行 `P3-01` 至 `P3-05`。先把启动维护纳入 Operation，再运行 SmartCare 规模 GC/长读/模型切换/持久卷 ENOSPC，最后完成备份恢复和旧兼容二进制回滚。

停止条件：只能在 WSL2/tmpfs/loopback 通过，不能在 release-host 持久卷重复；或恢复报告没有版本/SHA、Hash、时间和数据差异时，不能通过 P3-G4。

### 批次 G：P4/P5/P6 最终验收

执行 P4 的完整列表清理、P5 的 Agent Host UI 压力、P6 的有界退出和跨平台进程树/实例锁。复用同一批次 A 的数据和配置，追加 C13/C16 证据，避免用不同负载比较。

停止条件：`App.Close` 或任一 Stop 路径存在无 deadline 等待、旧 PID 树可能被误杀、或生产 Agent Host 的路由/任务状态未验证时，不得发布。

### 批次 H：最终发布评审

只有 P0-P6 全部 `门禁通过` 后，才整理变更清单、测试报告、已知限制、回滚说明和发布包。发布包的密钥审计、版本/格式核对和部署 smoke 是独立门禁；技术测试通过不自动授权外部上传或发布。

---

## 15. 完成定义与 Go/No-Go 清单

只有以下条件全部为“是”，才能把本文档状态改为“完成”：

### 架构与代码

- [ ] 所有后台长任务均通过 Durable Operation；HTTP handler 不解析、不调用模型、不执行物理删除或长维护。
- [ ] 所有 SQLite 写入均通过唯一 Storage Writer；事务有 deadline、批次边界、控制写优先和耗时指标。
- [ ] 所有任务都有版本化可重放输入、幂等、attempt/revision、取消意图、最终结果和分项提交 marker。
- [ ] 旧 `jobs.Manager` 不再作为第二个业务队列；旧 API 只保留兼容适配，不执行旁路任务。
- [ ] generation/source/model、tombstone、ancestor epoch、读引用和 raw immutable contract 覆盖所有读写及恢复路径。
- [ ] upload/staging、任务、内存、临时磁盘、模型缓存和结果保留均有实际配额和失败闭合。

### 协议与用户体验

- [ ] Web/Agent/旧 `jobId` 协议对 202、终态、取消、重试、冲突、过期和 submission unknown 的语义一致。
- [ ] OperationStore 是唯一后台任务状态源；刷新、路由切换、断线和重启不会丢任务或发生 stale overwrite。
- [ ] 大目录、导入、模型和设置页在后台压力期间可切换，控制路径不依赖全库列表读取。

### 验收与回滚

- [ ] P0 基线可重复，包含两套 SmartCare 数据、固定检索集、配置映射和完整资源指标。
- [ ] C01-C16 的适用项均有真实数据库/任务循环/故障注入证据，不是只测成功路径。
- [ ] 双库压力满足目标，并同时报告成功、拒绝、错误、超时、有效吞吐、RSS、WAL、临时磁盘和收敛时间。
- [ ] release-host 已完成持久卷 ENOSPC、文件锁、长读、模型切换、备份恢复和旧兼容二进制回滚。
- [ ] Linux、Windows、macOS 的实例锁、重复启动、受控进程树和有界退出均有运行证据；交叉编译不算运行验收。
- [ ] 每个阶段都有回滚目标版本/SHA、格式边界、备份 Hash、恢复点差异和已知限制。
- [ ] 发布包完成静态密钥审计、版本/格式核对、部署 smoke；外部发布前另行取得明确授权。

任何一项为“否”，结论只能是“部分完成”或“未完成”，不能用“编译通过”“有进度条”“偶尔不卡”替代架构完成。

---

## 16. 偏离记录

当前无已批准偏离。后续若需要改变单进程边界、任务确认语义、Storage Writer、数据格式、兼容版本或回滚方式，必须在本节追加编号、日期、原因、证据、风险、替代方案、迁移步骤和回滚步骤，并由评审者确认后才能实施。
