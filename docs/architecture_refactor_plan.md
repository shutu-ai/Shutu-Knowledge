# Shutu-Knowledge 健壮性架构改造方案

> 状态：实施基线（Proposal / Implementation Baseline）  
> 版本：v1.0  
> 日期：2026-09-13  
> 适用范围：Shutu-Knowledge 后端、Web UI、Agent 集成、文档导入/扫描/索引/删除、模型调用及运行时生命周期

## 0. 使用规则

本文档是后续架构改造的约束基线，不是零散的优化建议。

后续每项实现任务必须说明：

1. 对应本文档的阶段和条目。
2. 是否改变了本文档定义的状态机、任务契约或数据一致性约束。
3. 如何验证 Web 可用性、任务正确性和资源上限。
4. 如果需要偏离方案，必须先增加“偏离记录”，写清原因、风险、替代方案和回滚方式。

禁止为了通过旧测试或暂时消除页面卡顿而重新引入以下做法：

- 在 HTTP handler 中执行不可预测时长的解析、Embedding、索引、删除或 VACUUM。
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
| 同步长请求 | 文本、文件、URL 导入以及部分删除仍可能占用 HTTP 请求 | [internal/web/api.go](../internal/web/api.go)、[internal/knowledge/service_doc.go](../internal/knowledge/service_doc.go) |
| 长数据库事务 | Chunk 替换和删除会触发大量 FTS5 写放大 | [internal/knowledge/store.go](../internal/knowledge/store.go)、[internal/storage/migrations/0001_init.sql](../internal/storage/migrations/0001_init.sql) |
| 队列粒度过粗 | 普通队列/IO 队列无法表达数据库、磁盘、解析、模型等不同资源 | [internal/jobs/manager.go](../internal/jobs/manager.go) |
| 任务竞态 | 缺少统一的同文档互斥、幂等和删除/重建索引操作栅栏 | [internal/knowledge/service_doc.go](../internal/knowledge/service_doc.go)、[internal/knowledge/service_dir.go](../internal/knowledge/service_dir.go) |
| 跨存储不一致 | SQLite 记录、索引和 raw 文件不在同一事务中 | [internal/knowledge/service_doc.go](../internal/knowledge/service_doc.go)、[internal/knowledge/service_base.go](../internal/knowledge/service_base.go) |
| 维护任务竞争 | FTS optimize 和 VACUUM 可能占用写连接 | [internal/storage/maintenance.go](../internal/storage/maintenance.go) |
| 全库读取 | 目录操作和部分统计会重复加载整个知识库的元数据 | [internal/knowledge/service_dir.go](../internal/knowledge/service_dir.go) |
| 前端渲染竞态 | 路由、任务完成刷新和按钮事件共同替换全局 screen | [web/src/app.js](../web/src/app.js) |
| 进程边界不足 | Web、Agent 协议、任务、恢复和存储仍由同一进程承载 | [internal/extension/extension.go](../internal/extension/extension.go)、[internal/app/app.go](../internal/app/app.go) |

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

---

## 3. 目标与非目标

### 3.1 目标

1. Web/Agent 控制请求在后台重任务运行时保持可用。
2. 所有长任务都有持久化状态、阶段、进度、错误和取消能力。
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
│ 只做校验、提交命令、查询状态            │
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

### 不变量 A：HTTP 不执行长任务

导入、目录扫描、解析、Embedding、Rerank、重建索引、批量删除、删除知识库和维护任务都必须返回 Operation ID。

HTTP handler 只允许做：

1. 参数校验。
2. 权限和路径安全校验。
3. 写入任务记录或上传 staging 记录。
4. 返回任务 ID。

### 不变量 B：所有数据库写入必须经过 Storage Writer

业务层不能在任意 goroutine 中直接写 SQLite。数据库写入必须经过统一的写入通道，并满足：

- 一个写资源令牌。
- 短事务。
- 批量操作有上限。
- 不在事务内执行网络请求、模型推理或文件解析。
- 每个事务记录耗时。

### 不变量 C：任务必须可识别、可取消、可恢复

每个任务必须有：

- 唯一 ID。
- 类型和目标资源。
- 幂等键。
- 当前阶段。
- 已完成单位和总单位。
- 可重试错误信息。
- 取消状态。
- 启动和结束时间。

### 不变量 D：新索引完成后才能替换旧索引

重建索引使用 generation。新 generation 未完成并验证前，不能删除或替换 active generation。

### 不变量 E：删除先标记，再清理

删除必须先写入 `deleting` 状态。搜索、列表和新任务提交立即排除该对象，物理清理由后台任务分批完成。

### 不变量 F：任务不能覆盖更新的操作

所有文档变更都需要 mutation epoch/version。旧任务提交结果前必须验证版本，防止删除后旧的重建索引重新写回文档。

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

任务状态：

```text
queued → running → succeeded
                 ├→ failed
                 ├→ cancelled
                 └→ interrupted

running → cancelling → cancelled / failed / succeeded
```

任务表建议包含：

```text
id
type
base_id
document_id
parent_operation_id
state
phase
resource_class
priority
idempotency_key
completed_units
total_units
completed_bytes
total_bytes
requested_at
started_at
finished_at
attempt
cancel_requested
retryable
error_code
error_message
```

重复提交同一个幂等键时返回原 Operation，而不是创建新任务。

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

### 6.3 上传、导入和索引分层

目标流程：

```text
创建上传会话
  → 文件写入 staging
  → 校验大小、Hash、路径
  → 创建 import Operation
  → 解析
  → 分块
  → 生成 Embedding
  → 写入新 generation
  → 校验
  → 原子切换 active generation
```

较大文件应逐步使用 multipart 或分块上传，避免 Base64 JSON 同时造成内存放大和长 HTTP 请求。

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

重新索引流程：

1. 读取固定版本的 raw 文件。
2. 创建新的 generation。
3. 解析和写入新 Chunk。
4. 生成并写入 Embedding/Vector。
5. 校验 Chunk 数量、向量维度和模型信息。
6. 通过短事务切换 `active_index_generation`。
7. 异步删除旧 generation。

这样可以准确回答“文档是否使用了本地 Embedding 模型”，也可以避免重建索引失败后旧索引消失。

### 6.5 删除、目录删除和清理恢复

删除流程：

```text
document.status = deleting
  → 列表/搜索排除
  → 取消同文档未开始任务
  → 分批删除 Chunk/FTS/Vector
  → raw 文件移动到 quarantine
  → 确认清理完成
  → 删除最终元数据
```

数据库和文件系统不能使用同一个事务，因此必须使用 staging/quarantine 和恢复扫描来实现最终一致性。

进程启动时扫描：

- `deleting` 文档。
- staging 中未提交的文件。
- quarantine 中未完成清理的文件。
- 没有元数据的孤立文件。
- 有元数据但 raw 文件丢失的记录。
- 未完成的 index generation。

目录删除不能重复为每个子目录加载全库元数据，应改为父目录范围查询、批次处理和统一的父 Operation。

### 6.6 SQLite、FTS 和维护任务

SQLite WAL 和单 Writer 继续保留，但要增加：

- Chunk/FTS/Vector 批量写入上限。
- 写事务耗时指标。
- WAL 大小和 checkpoint 指标。
- FTS 批量操作基准。
- 维护任务低优先级调度。
- 正常负载期间禁止自动长时间 VACUUM。

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
- 搜索只读取 active generation。

向量检索暂时可以保留暴力扫描，但必须先通过基准证明是否为瓶颈；只有规模和 p95 延迟不满足目标时才引入 ANN。

### 6.8 前端 OperationStore

建议新增统一的前端任务状态层：

```text
OperationStore
  ├─ active operations
  ├─ queue position
  ├─ phase
  ├─ progress
  ├─ error
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

现有 `routeGeneration` 应保留作为保护措施，但不能代替统一状态管理。

### 6.9 Agent 与进程生命周期

启动阶段拆成：

```text
process lock
  → 监听端口
  → Agent 协议注册
  → Control Plane ready
  → Scheduler ready
  → 后台恢复
  → 低优先级维护
```

健康状态建议为：

```text
starting / degraded / ready / stopping
```

Agent 初始化不能等待大型文档恢复、模型扫描或 VACUUM 完成。

进程管理必须使用当前实例拥有的 PID/进程树边界；不能按 `sta.exe` 进程名全局清理。退出时：

1. 停止接收新任务。
2. 标记正在运行的任务为 cancelling/interrupted。
3. 等待有限时间让 Worker 停止。
4. 关闭存储和监听器。
5. 重启后由恢复器继续或重试任务。

---

## 7. API 迁移策略

### 7.1 新接口

```http
POST /api/operations
GET  /api/operations/{id}
GET  /api/operations
POST /api/operations/{id}/cancel
GET  /api/operations/{id}/events
```

所有导入、扫描、重建索引、删除、自检和模型下载都返回 `202 Accepted` 与 Operation ID。

### 7.2 兼容旧接口

旧接口在兼容期内保留，但内部改为：

```text
旧请求 → 参数校验 → 创建 Operation → 返回兼容响应
```

不得让旧接口继续绕过新 Scheduler。兼容接口需要记录弃用日志，并在文档中说明迁移时间点。

### 7.3 错误契约

错误必须区分：

- 参数错误。
- 路径安全错误。
- 队列已满。
- 资源暂时不可用。
- 任务冲突。
- 可重试失败。
- 不可重试失败。
- Agent/运行时未就绪。

前端根据错误类型显示“重试、排队、取消冲突任务或检查配置”，不能统一显示“请求失败”。

---

## 8. 分阶段实施计划

### P0：建立基线和观测

新增请求、队列、数据库和模型耗时指标，至少记录：

```text
queue_wait_ms
run_time_ms
parse_time_ms
model_time_ms
disk_read_ms
db_wait_ms
db_transaction_ms
fts_time_ms
vector_time_ms
```

先使用 SmartCare 两个知识库建立基线，不在没有基线时调整并发数。

### P1：统一长任务入口

- 新增 Operation Service。
- 文本、文件、URL 导入全部异步化。
- 批量删除和删除知识库异步化。
- 所有旧接口改为新 Service 的适配层。
- 任务提交成功必须先持久化任务，再进入内存队列。

退出条件：任何长任务运行时，提交任务和查询状态不依赖长任务 HTTP 请求完成。

### P2：资源调度和背压

- 将普通/IO 队列升级为资源队列。
- 增加每库、每文档互斥锁。
- 增加幂等和重复任务合并。
- 增加队列上限、优先级和维护任务降级。
- 所有存储写入接入统一 Writer。

退出条件：两个知识库并行工作时，没有无限排队、无限创建重复任务或磁盘长期失控。

### P3：索引一致性和短事务

- generation 化 Chunk、FTS 和向量。
- 重建索引采用新版本构建后切换。
- 删除使用 tombstone/quarantine。
- Chunk、FTS 和向量分批写入。
- 增加崩溃恢复和孤儿清理。

退出条件：在解析失败、模型失败、进程退出和磁盘不足时，旧索引不会被无故破坏。

### P4：读取性能

- 目录懒加载。
- 文件列表分页。
- 父目录范围查询。
- 统计缓存或增量计数。
- 搜索和详情接口拆分。

退出条件：大目录和多任务运行时，目录页面不会加载全库数据。

### P5：前端状态架构

- 建立 OperationStore。
- 统一任务轮询/SSE。
- 路由级 AbortController。
- 后台刷新改为数据级更新。
- 加载、空态、错误、重试和冲突态统一。

退出条件：快速切换“文档、导入、模型、设置”不会出现空白页、标题壳或旧请求覆盖新页面。

### P6：启动和进程治理

- 建立 starting/degraded/ready 状态。
- Agent 初始化不等待后台恢复。
- 增加实例锁、端口错误提示和受控进程树清理。
- 实现优雅退出和任务恢复。

退出条件：重启后控制面先可用，后台恢复可观察、可取消，不再依赖等待一段时间后页面才出现。

### P7：是否拆分进程

仅当 P0～P6 完成后仍无法满足 Web 延迟目标，才考虑：

```text
shutu-knowledge-control.exe
shutu-knowledge-worker.exe
```

拆分前必须已有持久化任务、幂等、generation、tombstone、恢复和本地 RPC 契约。

---

## 9. 验收指标

以下是第一版目标值，最终以 P0 基线和实际机器能力校准：

| 指标 | 目标 |
|---|---:|
| 任务提交 API p95 | < 500ms |
| 任务状态 API p95 | < 500ms |
| `/api/stats` p95 | < 500ms |
| 文档列表 API p95 | < 1s |
| 普通数据库写事务 | 目标 < 500ms |
| 数据库写事务告警 | > 2s |
| 长任务 HTTP handler | 不执行解析/索引/删除 |
| 任务重复提交 | 幂等返回原任务 |
| 页面切换 | 后台任务运行时仍可用 |
| 任务恢复 | 重启后可恢复、重试或明确中断 |
| 数据一致性 | 无删除后复活、无旧任务覆盖新任务 |

磁盘不设置脱离设备的固定百分比目标，而是以 API p95、磁盘队列长度、WAL 增长和任务吞吐共同决定限流阈值。磁盘达到 100% 利用率但 Web 仍满足 SLO 可能是可接受的；磁盘只有 2% 但页面超时也不能视为健康。

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

---

## 11. 回滚策略

每个阶段都必须可单独回滚：

- P1/P2 通过配置开关保留旧适配路径，但旧路径也必须有超时和资源限制。
- 数据库迁移采用新增字段和新增表，禁止第一阶段删除旧字段。
- generation 和 tombstone 清理必须可暂停，不能依赖立即物理删除。
- 前端新任务面板失败时可降级到查询接口，但不能回退为同步长请求。
- 进程拆分必须可以重新启用单进程 Worker。
- 禁止使用 `git reset --hard` 或覆盖工作区方式回滚用户已有修改。

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

## 13. 实施任务模板

后续每个改造任务都使用以下格式：

```text
任务名称：
对应阶段：P0/P1/P2/P3/P4/P5/P6/P7
解决的不变量：A/B/C/D/E/F/G
涉及模块：
不改变的兼容行为：
数据迁移：
失败和取消行为：
资源上限：
测试用例：
压力验证：
回滚方式：
是否偏离本文档：是/否
```

如果一个任务无法填写“资源上限、失败行为、回滚方式和验证方法”，则不应直接进入实现。

---

## 14. 第一批可执行任务

建议下一轮按以下顺序实施：

1. 增加 Operation 数据结构和状态迁移测试，不改变现有 UI。
2. 将文本、文件、URL 导入改为统一 Operation 入口。
3. 将批量删除和删除知识库改为统一 Operation 入口。
4. 增加任务幂等键、同文档互斥和冲突规则。
5. 增加队列等待、数据库写锁等待和阶段耗时指标。
6. 用两个 SmartCare 知识库运行 P0 基线压力测试。
7. 根据基线确定 Scheduler 的实际并发和背压阈值。
8. 再实施 generation、tombstone 和分批写入。

不得跳过第 5、6 项直接凭感觉调整并发数。

---

## 15. 完成定义

本架构改造只有在以下条件全部满足后，才能标记完成：

- 所有长任务不再由 HTTP handler 同步执行。
- 所有任务有统一的持久化状态和幂等行为。
- 数据库写入受统一资源调度和事务时长控制。
- 删除、重建索引和进程重启经过故障测试。
- 文档列表、导入、模型和设置页面在压力运行期间可切换。
- 两个 SmartCare 知识库的并发压力数据达到验收目标。
- Agent 启动、退出、重启和重复启动经过验证。
- 变更、测试结果、已知限制和回滚方法已经记录。

“编译通过”“有进度条”“偶尔不卡”都不等于架构改造完成。

