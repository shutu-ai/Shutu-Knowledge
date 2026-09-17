# Shutu-Knowledge 0.2 Stabilization / Release Closure

你现在负责对当前 **Shutu-Knowledge** 进行一次“巩固、收尾、发布闭环”。

这不是新一轮架构重构，也不是功能扩展。

本任务的唯一目标是：

> 将当前已有能力收敛成一个稳定、可重复、可安装、可长期实际使用的 Windows 优先版本。

当前阶段正式进入：

# Feature Freeze / Stabilization

除非某项修改是修复阻断真实使用的问题，否则禁止增加新功能、扩大架构范围或引入新的技术栈。

---

# 1. 产品目标

本版本的完成定义只有一句话：

> 在一台普通 Windows PC 上，用户安装 Shutu-Knowledge 后，可以稳定创建知识库、导入真实 PDF / Word / PowerPoint / Excel 文档，完成解析、Embedding、索引、检索、Rerank，并通过 Shutu-Agent 获得可靠且可追溯引用的知识检索结果，无需用户手工配置复杂外部运行时。

本轮所有工作必须直接服务于这一目标。

如果某项工作不能明显提高上述主流程的：

* 可用性
* 稳定性
* 数据正确性
* 可恢复性
* 安装体验
* Agent 使用体验

则原则上不进入当前版本。

---

# 2. 本轮明确禁止事项

本轮禁止主动进行以下工作：

* 不重新设计整体架构。
* 不迁移 PostgreSQL。
* 不引入新的 Vector Database。
* 不引入 Redis、Kafka、独立 Worker Cluster 等分布式组件。
* 不进行微服务拆分。
* 不增加 Knowledge Graph。
* 不增加 LLM-Wiki。
* 不增加 GraphRAG。
* 不开发新的 Document Intelligence 大框架。
* 不增加新的 Embedding 模型体系。
* 不增加新的 Reranker 体系。
* 不为了形式上的 parity 再实现大量低价值边缘能力。
* 不进行 Linux / macOS 全量 release-host 验收。
* 不追求多用户、高并发、分布式部署。
* 不做百万级文档等当前没有真实需求的极端性能优化。
* 不因为发现局部问题而再次启动大规模架构重构。
* 不修改 Shutu-Agent 核心代码；Shutu-Knowledge 继续通过既有 Extension Contract 单向依赖 Shutu-Agent。

发现未来值得做的能力，只记录进入：

`docs/backlog.md`

不要在当前版本实施。

---

# 3. 平台策略

本版本重新定义平台优先级。

## Tier 1

Windows x64

这是当前正式支持和 Release Gate 所针对的平台。

要求：

* 安装
* 首次启动
* Runtime 初始化
* 模型初始化
* 文档导入
* 文档解析
* Embedding
* Index
* Retrieval
* Rerank
* Agent Integration
* Restart Recovery
* Upgrade
* Uninstall / Data Preservation

必须完成真实验证。

## Tier 2

Linux

只要求：

* `go build ./...`
* `go test ./...`
* `go vet ./...`
* Web typecheck/build
* 不明显破坏核心代码可移植性

不要求当前版本完成所有：

* process-tree
* OS lock
* crash recovery
* runtime packaging
* installer
* release-host

边缘验收。

## Tier 3

macOS

当前版本仅保持代码无明显主动破坏。

不作为 Release Blocker。

所有现有文档、Release Gate、CI、验收矩阵中，如果仍将“三平台全部完成”作为 0.2 发布硬门槛，应重新审查并合理降级。

---

# 4. 第一阶段：审计当前真实状态

不要直接修改。

首先全面检查当前仓库：

* 当前 branch
* 当前 HEAD
* git status
* 当前版本号
* 当前 Release Candidate 状态
* README
* architecture documents
* runtime documents
* release documents
* CI workflows
* test suites
* Windows packaging
* Agent integration
* Storage
* Search
* Indexing
* Runtime manager
* Web frontend

特别检查最新 master 当前 CI Failure。

已知此前观察到：

* latest master 曾存在 race test failure
* 本地 PASS 与 CI PASS 可能不一致

必须找出真实原因。

不得简单通过：

* 删除测试
* skip 测试
* 放宽 assertion
* 增加任意 sleep
* 禁用 race
* 降低 CI 标准

来实现表面绿色。

必须解决真实问题。

---

# 5. 建立 Stabilization Scope

审计后建立：

`docs/stabilization_0.2.md`

必须包含以下内容：

## A. Release Blocker

真正阻止当前版本实际使用或发布的问题。

## B. Must Fix

明显影响核心体验，但存在 workaround 的问题。

## C. Backlog

不阻断当前版本的问题。

所有问题必须按：

`P0 / P1 / Backlog`

分类。

禁止出现大量 P2/P3 当前实施项。

如果原架构计划中存在与当前产品目标无直接关系的未完成项，应明确：

`Deferred from 0.2 Release Gate`

而不是继续实施。

---

# 6. P0：CI 必须恢复稳定绿色

这是当前最高优先级。

必须验证：

```bash
go build ./...
go vet ./...
go test ./...
go test -race ./...
```

Web：

```bash
npm ci
npm run typecheck
npm run build
```

以及仓库当前正式定义的其它核心 CI。

要求：

* 连续多次执行稳定 PASS。
* CI Runner PASS。
* 本地 PASS。
* 不存在明显 flaky race。
* 不靠 sleep 掩盖并发问题。

如发现 race：

必须找真实共享状态、生命周期、goroutine、channel、SQLite transaction、scheduler、operation state 等根因。

修复后增加对应 regression test。

---

# 7. P0：核心用户路径必须做真实 E2E

建立一个真正的：

`Release Golden Path`

至少覆盖：

```text
Clean Windows PC
      ↓
Install
      ↓
First Start
      ↓
Runtime Initialization
      ↓
Create Knowledge Base
      ↓
Import Documents
      ↓
Parse
      ↓
Chunk
      ↓
Embedding
      ↓
Index
      ↓
Search
      ↓
Rerank
      ↓
Citation
      ↓
Agent Retrieval
      ↓
Restart
      ↓
Search Again
```

不得只做 Unit Test。

必须至少做一套真实样例库。

建议 Golden Corpus 至少包含：

* 普通文字 PDF
* PDF + 图片
* DOCX
* PPTX
* XLSX
* 中文文档
* 英文文档
* 中英混合文档
* 一个较长文档
* 同一文档修改后的更新版本

如果当前已经存在 sample corpus，应优先复用，不重复造数据。

---

# 8. P0：Runtime 开箱即用能力

重点验证已有：

* Embedding runtime
* Reranker runtime
* OCR runtime
* PDF renderer
* Legacy Office runtime

目标不是继续增加 runtime。

而是确认：

> 用户安装后能真正使用。

检查：

* 自动安装
* 自动下载
* revision 固定
* checksum
* READY 状态
* CORRUPTED 状态
* 下载中断
* 文件损坏
* 自动恢复
* offline restart
* runtime upgrade
* 路径包含空格
* Windows 用户目录
* 中文路径

禁止依赖用户手工安装 Python 环境。

禁止依赖用户手工执行复杂模型命令。

如果确实存在必要外部依赖，应让 Doctor 明确报告，而不是运行到中途才失败。

---

# 9. P0：知识库数据一致性

重点测试真实生命周期，而不仅是首次导入。

至少覆盖：

## Create

创建 KB。

## Import

导入文件。

## Update

原文件内容更新后重新导入。

要求旧数据不能与新 generation 混合。

## Delete Document

删除文档后：

* 文档记录消失或正确 tombstone。
* 旧 Chunk 不再出现在检索结果。
* Vector 不再参与当前 generation 搜索。
* Citation 不再指向已删除内容。

## Reindex

重新建立索引后：

* 不产生重复 Chunk。
* 不产生新旧 generation 混合。
* 不破坏原有其它文档。

## Delete KB

知识库删除必须完整且可预测。

## Restart

应用重启后：

* KB 存在。
* Index 可用。
* Runtime 状态正确。
* 搜索结果一致。

---

# 10. P0：Search Golden Tests

当前检索能力已经包含：

* BM25
* Vector
* RRF
* Multi-query
* Reranker
* Threshold
* MMR
* Context composition

本轮禁止继续增加 Retrieval 算法。

目标改为：

> 确认已有检索链路稳定、正确、可解释。

建立小规模 Golden Query Set。

例如 20～50 个真实问题。

每个问题至少记录：

* expected document
* expected section/chunk
* citation expected
* relevant / irrelevant

关注：

* Precision
* Citation correctness
* deleted document leakage
* old generation leakage
* duplicate result
* rerank ordering
* lexical fallback
* embedding unavailable fallback

当前版本不要求追求论文级 Recall benchmark。

重点是：

> 不出现明显错误。

---

# 11. P0：Agent Integration

必须验证真实 Shutu-Agent 集成，而不仅是 Knowledge Web 自身。

至少测试：

```text
Agent
  ↓
Knowledge Extension
  ↓
Search
  ↓
Evidence
  ↓
Citation
  ↓
LLM
```

确认：

* Agent 能看到 Knowledge capability。
* 参数协议正确。
* Search 正常。
* Error propagation 正确。
* Cancellation 正确。
* Citation 可消费。
* KB 不存在时行为正确。
* Runtime unavailable 时行为正确。
* Knowledge 重启后 Agent 可以继续使用。

不要因为本轮测试发现 Agent 可增强，就修改 Agent 架构。

如发现确实需要 Agent 提供新能力：

创建：

`docs/shutu_agent_requirements.md`

只记录需求，不在本项目跨仓库修改。

---

# 12. P0：任务恢复与状态正确性

当前版本不需要构建“企业级任务编排系统”。

只验证用户实际会遇到的情况：

* 导入过程中关闭程序。
* Embedding 中关闭程序。
* Indexing 中关闭程序。
* 程序异常退出。
* Windows 重启。

恢复后必须：

* 不出现假 `COMPLETED`。
* 不永远卡在 `RUNNING`。
* 不产生重复索引。
* 不破坏已成功文档。
* 可以重新执行失败任务。

如果当前 Durable Operation 已经满足这些要求，不再进一步抽象。

---

# 13. P0：错误不能静默

全面检查核心流程的 silent failure。

所有关键错误至少必须有：

* machine-readable state
* user-readable message
* log
* recovery suggestion

重点：

* Runtime missing
* Runtime corrupted
* Model unavailable
* PDF parse failure
* OCR failure
* Office parse failure
* Embedding failure
* Rerank failure
* SQLite error
* Disk full
* Permission denied
* File locked
* Import failure
* Index failure

不允许：

> UI 一直转圈但用户不知道为什么。

---

# 14. P1：中等真实规模稳定性

本轮不要追求极端 SmartCare 全量规模门槛。

先建立一个真实的中等规模数据集，例如：

* 数百文档
* 数万～十万 Chunk
* 多种格式

测试：

* Initial import
* Incremental update
* Restart
* Search
* Reindex
* Delete
* Memory
* Disk
* WAL
* CPU
* UI responsiveness

目标：

> 普通 32GB Windows PC 能够长期稳定使用。

如果存在 SmartCare 大规模 Corpus，可运行一次并记录结果。

但如果外部语料或 release-host 条件暂时不具备：

标记：

`Deferred validation`

不得因此无限拖延当前版本。

---

# 15. P1：安装与发布体验

验证真正的 Windows Release Artifact。

要求：

* fresh install
* first launch
* model/runtime initialization
* upgrade from previous supported version
* application uninstall
* data preservation policy
* clean uninstall behavior
* version reporting

特别检查：

* 数据目录
* 模型目录
* runtime 目录
* cache
* log
* DB
* user config

位置必须明确。

升级不能意外删除 Knowledge Base。

---

# 16. P1：UI 收尾

本轮 UI 不允许重新设计。

只处理阻断使用的问题：

* 卡死
* 无响应
* 错误信息看不到
* Progress 永远不结束
* 状态与后台不一致
* 删除后页面仍显示旧数据
* 重启后状态错误
* Runtime 状态错误
* 搜索结果 citation 无法定位

不做：

* 全新视觉设计
* 大规模组件重构
* 无关动画
* 新 Dashboard

---

# 17. Doctor

如果已有 Doctor，重点完善成真正的用户排障入口。

至少输出：

```text
Application
Storage
Database
Runtime
Embedding
Reranker
OCR
PDF
Office
Disk
Agent Integration
```

状态建议：

```text
READY
DEGRADED
MISSING
CORRUPTED
UNSUPPORTED
```

Doctor 必须能明确区分：

> 不影响核心使用

与：

> Release Blocker。

---

# 18. 文档收口

删除或修正会让用户误解的状态描述。

重点检查：

* README
* release docs
* parity report
* architecture plan
* runtime docs

不能同时出现：

```text
Release Ready
```

和：

```text
Architecture incomplete
```

但没有解释两者区别。

建议明确分为：

```text
v0.1.x:
Released

v0.2:
Stabilization / Release Candidate

Windows:
Tier 1

Linux:
Build/Test supported

macOS:
Best effort
```

另外建立：

`docs/release_0.2_acceptance.md`

作为唯一正式 Release Gate。

---

# 19. 简化 Release Gate

0.2 Release Gate 只保留以下硬门槛：

## Build

PASS

## Unit / Integration

PASS

## Race

PASS

## Web build

PASS

## Windows Golden Path

PASS

## Runtime smoke

PASS

## Document lifecycle

PASS

## Search golden test

PASS

## Agent integration

PASS

## Restart recovery

PASS

## Fresh install

PASS

## Upgrade

PASS

## No P0 defects

PASS

其余：

* Linux release-host
* macOS release-host
* extreme scale
* multi-user
* distributed
* advanced parser
* document IR
* Knowledge Graph
* LLM Wiki

全部不得阻塞 0.2。

---

# 20. 防止过度工程化原则

每发现一个问题，实施前必须判断：

## Question A

这个问题是否会影响当前 Windows 个人知识库真实使用？

如果不会：

→ Backlog。

## Question B

是否可以通过局部修复解决？

如果可以：

→ 禁止为它重构整个 subsystem。

## Question C

是否已有稳定实现？

如果已有：

→ 优先修复，不重写。

## Question D

这项修改会不会明显扩大测试矩阵？

如果会：

必须证明它是 Release Blocker，否则延期。

---

# 21. 修改原则

严格遵循：

1. Minimum Necessary Change。
2. 不破坏现有 API。
3. 不破坏数据格式，除非有完整 migration。
4. 新增 bugfix 必须尽量有 regression test。
5. 不为了测试通过删除真实检查。
6. 不降低已有安全边界。
7. 不修改已有正确 durable semantics。
8. 不引入新的重型依赖。
9. 不因为“未来可能需要”提前设计。
10. 保持 Shutu-Knowledge → Shutu-Agent 单向依赖。

---

# 22. 最终验证

完成所有修改后，从 fresh clone 开始进行最终验收。

必须记录：

```text
Git commit
Go version
Node version
Windows version
CPU
RAM
Build result
Go test result
Race result
Vet result
Web result
Golden Path result
Runtime result
Search result
Agent integration result
Restart recovery result
Installer result
Upgrade result
```

不能只引用历史测试结果。

必须使用最终代码重新运行。

---

# 23. 最终输出

最终只输出以下内容。

## 1. Release Status

必须明确：

```text
READY
```

或：

```text
NOT READY
```

不能使用模糊表达。

## 2. Commit

最终 commit SHA。

## 3. Fixed

本轮实际修复的问题。

## 4. Validation

每个 Release Gate：

```text
PASS / FAIL
```

## 5. Remaining P0

如果存在，逐项列出。

## 6. Deferred

明确哪些问题延期到后续版本。

## 7. Known Limitations

只列真实存在的问题。

## 8. Release Recommendation

如果所有硬门槛通过：

```text
Shutu-Knowledge 0.2 is ready for Windows Tier-1 release.
```

否则：

明确唯一剩余阻断项。

---

# 24. 最重要的执行原则

本轮成功不是：

> 做了更多功能。

而是：

> 删除了不必要的发布门槛，把已有核心能力真正变成稳定产品。

不要继续追求“理论完整”。

不要重新打开架构设计。

不要因为发现一个边缘问题不断扩大 Scope。

本阶段目标是：

> Freeze → Audit → Fix → Test → Stabilize → Package → Release

完成 0.2 后停止当前架构扩展。

后续 0.3 再单独规划：

* Document IR
* page / slide / sheet provenance
* table / figure understanding
* richer complex-document parsing
* Knowledge Understanding Layer
* LLM-Wiki-like capabilities

这些不属于本任务。
