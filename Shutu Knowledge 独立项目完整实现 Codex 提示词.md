# 任务：创建 shutu-knowledge —— 基于 shutu-agent Extension Platform v1，实现 dsh-knowledge 功能等价的独立知识库扩展

## 1. 项目目标

在当前新的、独立的 Git 项目目录中开发：

```text
shutu-knowledge
```

目标：

> 以 `shutu-agent` 为唯一 Agent 底座，通过其已经冻结的 Extension Platform v1 深度接入 Agent；参考、复用或重新实现 `dsh-knowledge` 的完整知识库能力，使 Knowledge 在用户体验和 Agent Runtime 中表现得像原生内置能力，但工程上仍然是完全独立项目。

参考项目：

```text
Agent 底座：
https://github.com/shutu-ai/shutu-agent

知识库能力参考：
https://github.com/Soren-ABT/dsh-knowledge
```

---

# 2. 最重要的架构约束

必须始终保持：

```text
shutu-knowledge
        │
        │ 单向依赖
        ▼
shutu-agent
```

绝对禁止：

```text
shutu-agent
        ↓
shutu-knowledge
```

---

# 3. shutu-agent 是只读依赖

`shutu-agent` 在本任务中视为：

```text
READ ONLY
```

允许：

```text
阅读源码
阅读 docs
阅读 sdk/extension
阅读 Extension Protocol v1
阅读测试
运行 Agent
调用公开 Extension API
```

禁止：

```text
修改 shutu-agent 源码
修改 shutu-agent docs
修改 shutu-agent tests
修改 shutu-agent Web
修改 shutu-agent config schema
给 Agent 增加 Knowledge 专用代码
import shutu-agent/internal/...
复制 Agent internal 代码绕过 public contract
```

无论实现 Knowledge 功能遇到多大困难：

> 都不得直接修改 shutu-agent。

---

# 4. Agent Contract 唯一允许入口

Knowledge 只能通过正式公开的：

```text
shutu-agent Extension Platform v1
```

接入。

包括但不限于：

```text
Extension Manifest
Extension Protocol v1
sdk/extension
Capability Negotiation
Native Context Provider
Tool Contribution
Tool Risk Metadata
Approval Integration
Lifecycle
Health
Events
Web Contribution
Web Navigation
Observability
```

如果使用 Go SDK：

```text
github.com/shutu-ai/shutu-agent/sdk/extension
```

可以依赖。

但：

```text
github.com/shutu-ai/shutu-agent/internal/...
```

任何 import 都视为架构 FAIL。

---

# 5. Agent 接口不足时的强制规则

如果实现 `dsh-knowledge` 某项能力时发现：

```text
Extension Platform v1
```

缺少所需能力：

立即遵守：

```text
发现 Agent Contract Gap
        ↓
停止该功能的绕路实现
        ↓
绝不修改 shutu-agent
        ↓
记录 gap
        ↓
继续其它不受阻塞功能
```

必须记录：

```text
docs/agent_extension_gap_report.md
```

每个 Gap 至少包括：

```text
Gap ID
Knowledge capability
业务场景
为什么当前 Extension v1 无法实现
当前 Agent 已有相关能力
缺失的最小通用能力
推荐 Agent Improvement Requirement
是否阻塞
是否存在不破坏架构的临时方案
```

推荐改进必须是：

```text
generic Agent capability
```

而不能是：

```text
Knowledge-specific API
```

例如禁止建议：

```text
Agent.AddKnowledgeContext()
Agent.SearchKnowledge()
/api/knowledge/search
```

应该提出：

```text
更通用的 Extension Context/Event/Web/Tool Contract
```

---

# 6. 禁止偷偷绕过 Contract

以下行为一律禁止：

```text
直接操作 Agent Session DB
直接修改 Agent message history
直接修改 Agent Prompt
读取 Agent 私有 runtime memory
通过文件系统修改 Agent config
patch Agent executable
复制 internal structs
通过反射或 unsafe 访问 Agent internals
```

如果发现必须这么做才能实现某功能：

```text
该功能 = BLOCKED BY AGENT CONTRACT
```

并写 Gap Report。

---

# 7. dsh-knowledge 的定位

`dsh-knowledge` 是：

```text
功能参考
行为参考
产品参考
算法参考
测试参考
UI/交互参考
```

目标不是只模仿首页或主要功能，而是：

# Capability-by-Capability Functional Equivalence

首先完整审计：

```text
https://github.com/Soren-ABT/dsh-knowledge
```

不要只阅读 README。

必须检查：

```text
source code
packages
server
client
tools
database
migrations
parsers
chunkers
retrieval
embedding
reranker
OCR
model handling
configuration
tests
Web UI
API
background jobs
directory ingestion
URL ingestion
document lifecycle
```

---

# 8. 先建立完整能力清单

在写大量生产代码之前生成：

```text
docs/dsh_knowledge_capability_inventory.md
```

至少分类为：

```text
Knowledge Base Management
Document Lifecycle
Input Sources
Document Parsing
OCR
Chunking
Semantic Chunking
Metadata
Indexing
Full Text Search
Vector Search
Hybrid Retrieval
RRF
MMR
Reranking
Context Composition
Context Window
Anchor / continuation
Embedding Models
Reranker Models
Model Management
Retrieval Testing
Knowledge Tools
Agent Integration
Automatic Retrieval
Web UI
Configuration
Persistence
Background Jobs
Observability
Error Handling
Security
```

每一项必须记录：

```text
Capability ID
dsh-knowledge implementation
User-visible behavior
Inputs
Outputs
Configuration
Dependencies
Persistence
Edge cases
Tests
shutu-knowledge target implementation
Status
```

---

# 9. 建立 Capability Gap Matrix

生成：

```text
docs/dsh_knowledge_equivalence_matrix.md
```

格式至少：

| Capability | dsh-knowledge | shutu-knowledge | Status | Evidence |
|---|---|---|---|---|
| Knowledge base CRUD | Yes | ... | TODO/PASS | test |
| File import | Yes | ... | ... | ... |
| URL import | Yes | ... | ... | ... |
| Directory refresh | Yes | ... | ... | ... |
| OCR | Yes | ... | ... | ... |
| Semantic chunking | Yes | ... | ... | ... |
| BM25 | Yes | ... | ... | ... |
| Vector | Yes | ... | ... | ... |
| RRF | Yes | ... | ... | ... |
| MMR | Yes | ... | ... | ... |
| Rerank | Yes | ... | ... | ... |
| Auto RAG | Yes | ... | ... | ... |
| Retrieval test | Yes | ... | ... | ... |
| Web UI | Yes | ... | ... | ... |

不得使用：

```text
大致支持
类似
基本完成
```

作为最终状态。

只能：

```text
PASS
PARTIAL
BLOCKED
NOT APPLICABLE
```

并解释原因。

---

# 10. dsh-knowledge 功能优先保留

除非：

```text
技术栈不兼容
设计明显依赖 DeepSeek Harness 特定机制
存在平台不适用功能
```

否则原则上：

> dsh-knowledge 有的用户功能，shutu-knowledge 都应该实现。

不要未经分析自行删减：

```text
高级检索
模型管理
目录刷新
Retrieval Test
Anchor
Context Window
Semantic Chunking
OCR
Web 管理能力
```

---

# 11. 允许参考和复制 dsh-knowledge，但必须处理许可证

可以：

```text
阅读源码
参考设计
参考算法
参考测试
参考 UI
参考 API
移植功能
在许可证允许的条件下复用代码
```

但必须先检查：

```text
dsh-knowledge LICENSE
第三方依赖许可证
被复制文件自身版权头
```

如果直接复制或改写受许可证约束代码：

必须：

```text
保留必要版权与许可证声明
记录来源
遵守原许可证义务
```

建立：

```text
THIRD_PARTY_NOTICES.md
```

以及：

```text
docs/source_reuse_inventory.md
```

至少记录：

```text
Source repository
Source file/module
Reused/translated/reimplemented
License
Local destination
Required attribution
```

禁止无记录复制源码。

如果目标项目许可证与直接复制存在冲突：

> 不直接复制，改为基于功能行为重新实现。

---

# 12. 不要求逐行翻译

目标：

```text
Capability Equivalence
+
Behavior Equivalence
```

不是：

```text
Source Translation Equivalence
```

例如 dsh-knowledge 使用 TypeScript/Cordis：

可以根据 `shutu-agent` 架构重新设计成更适合当前项目的实现。

---

# 13. 推荐技术架构

如果当前项目为空，优先采用：

```text
Go backend
+
Web frontend
```

以便：

```text
与 shutu-agent 部署模型一致
单二进制或少依赖
跨平台
高并发
本地运行稳定
容易通过 Extension Protocol v1 接入
```

但不要为了“Go 纯洁性”拒绝合理外部 runtime。

例如：

```text
OCR
复杂文档解析
本地模型
MinerU
Node/Python helper
```

如果第三方生态明显更成熟，可以使用：

```text
optional external runtime / helper process
```

但必须：

```text
明确 dependency
health check
错误提示
自动发现
可选安装
不让 Agent Core 依赖它
```

---

# 14. 推荐项目结构

根据真实实现调整，但可以参考：

```text
shutu-knowledge/
├── cmd/
│   └── shutu-knowledge/
│
├── internal/
│   ├── extension/
│   ├── knowledge/
│   ├── ingest/
│   ├── parser/
│   ├── chunk/
│   ├── embedding/
│   ├── rerank/
│   ├── retrieval/
│   ├── index/
│   ├── storage/
│   ├── models/
│   ├── jobs/
│   ├── web/
│   └── config/
│
├── web/
├── migrations/
├── examples/
├── docs/
├── tests/
├── go.mod
└── README.md
```

不要为了符合此示例强行改变更合理的结构。

---

# 15. Persistence Boundary

严格坚持：

```text
Agent owns Agent state
Knowledge owns Knowledge state
```

Knowledge 自己保存：

```text
knowledge bases
documents
chunks
metadata
indexes
model configs
jobs
source records
retrieval history
```

Agent 不理解这些结构。

---

# 16. 推荐数据目录

使用独立数据域，例如：

```text
~/.shutu/knowledge/
```

或当前项目已有统一 data-domain 规范。

可以包含：

```text
knowledge.db
raw/
indexes/
models/
cache/
tmp/
logs/
```

不要把 Knowledge 数据塞进 Agent Session DB。

---

# 17. Knowledge Base Management

完整实现：

```text
create
list
get
update
delete
stats
```

考虑：

```text
name
description
metadata
created_at
updated_at
document_count
chunk_count
index state
```

删除属于 destructive operation。

进入 Agent Tool Registry 时必须声明对应风险。

---

# 18. Document Lifecycle

实现完整生命周期：

```text
import
parse
chunk
index
ready
refresh
reindex
delete
error
```

必须有明确状态模型。

不要用：

```text
一个 bool indexed
```

覆盖所有状态。

至少考虑：

```text
pending
processing
ready
failed
stale
```

根据实际需要设计。

---

# 19. Input Sources

完整审计并对齐 dsh-knowledge 支持的输入来源，包括但不限于：

```text
local file
directory
text
URL
```

如果 dsh-knowledge 还有其它来源，加入 inventory。

---

# 20. 文件格式

审计 dsh-knowledge 当前真实支持格式。

目标尽量覆盖：

```text
PDF
DOCX
PPTX
XLSX
TXT
Markdown
HTML
CSV
EPUB
```

以及 dsh-knowledge 实际其它格式。

不得根据这份提示词假定支持范围。

必须以源码审计结果为准。

---

# 21. Parser Architecture

Parser 应采用：

```text
Parser Registry
```

或等价通用结构。

例如：

```text
Document
 ↓
MIME/type detection
 ↓
Parser
 ↓
Normalized Document
 ↓
Chunking
```

避免：

```go
if pdf {}
else if docx {}
else if pptx {}
...
```

无限堆积在主流程。

---

# 22. OCR

如果 dsh-knowledge 支持 OCR：

实现对应能力。

必须区分：

```text
native text extraction
OCR fallback
forced OCR
```

OCR failure 不应该破坏所有可解析文本。

---

# 23. MinerU 等高级解析

如果 dsh-knowledge 支持 MinerU 或类似高级 PDF pipeline：

能力应纳入 equivalence matrix。

如果需要外部程序：

设计：

```text
optional provider
health
availability detection
fallback
```

不得把 MinerU 依赖加入 Agent。

---

# 24. Chunking

实现并测试 dsh-knowledge 对应策略，例如：

```text
fixed/recursive
heading-aware
semantic
```

实际名称以源码为准。

每个 Chunk 至少保留：

```text
document
position
text
metadata
source reference
```

---

# 25. Semantic Chunking

如果 dsh-knowledge 有 Semantic Chunking：

不得因为实现复杂直接省略。

需要：

```text
独立策略
配置
测试
benchmark
fallback
```

---

# 26. Metadata Preservation

必须尽量保存：

```text
document title
source
URL/path
page
sheet
slide
section
heading
chunk order
timestamp
custom metadata
```

因为 Context Citation / Source Attribution 后续依赖这些信息。

---

# 27. Full-text Retrieval

如果 dsh-knowledge 使用：

```text
SQLite FTS5 / BM25
```

应实现等价全文检索能力。

不要因为已经有 Vector Search 就省略 BM25。

---

# 28. Vector Retrieval

实现：

```text
embedding
vector persistence/index
similarity search
top-k
filters
```

并处理：

```text
model change
dimension change
reindex
index corruption
```

---

# 29. Hybrid Retrieval

实现 dsh-knowledge 的真实混合检索流程。

预计类似：

```text
BM25
+
Vector
 ↓
RRF
 ↓
MMR
 ↓
Rerank
```

但必须以 dsh-knowledge 源码为准。

不能仅根据 README 猜测。

---

# 30. RRF

如果 dsh-knowledge 使用 Reciprocal Rank Fusion：

实现相同行为和可配置参数。

增加确定性测试。

---

# 31. MMR

如果存在 Maximum Marginal Relevance：

实现：

```text
relevance
+
diversity
```

并覆盖：

```text
duplicate chunks
near duplicate chunks
same document concentration
```

测试。

---

# 32. Reranker

支持：

```text
candidate retrieval
 ↓
reranker
 ↓
final evidence
```

Reranker 必须可以：

```text
enable/disable
configure
health check
fallback
```

Reranker failure 默认不应导致整个 Knowledge 服务不可用。

---

# 33. Embedding Model

模型层必须抽象成：

```text
EmbeddingProvider
```

或等价机制。

避免业务代码直接依赖某一个模型实现。

---

# 34. Reranker Model

同理：

```text
RerankerProvider
```

支持未来更换模型。

---

# 35. Local Model Management

如果 dsh-knowledge 有：

```text
download
discover
configure
status
delete
```

模型管理能力，应实现等价功能。

模型文件属于 Knowledge 项目，不属于 Agent。

---

# 36. Context Composer

Retrieval 最终不能简单返回：

```text
[]Chunk
```

给 Agent。

需要独立：

```text
Context Composer
```

负责：

```text
ranking
source grouping
dedup
context formatting
metadata/citations
context window
```

---

# 37. Context Window

如果 dsh-knowledge 支持：

```text
前后相邻 chunk
context expansion
```

必须保留。

例如命中：

```text
chunk #20
```

可以根据策略补：

```text
#19
#20
#21
```

但需要防止 context explosion。

---

# 38. Anchor / Continuation

如果 dsh-knowledge 有 Anchor 或 continuation 机制：

纳入完整实现。

不要因为不理解而删除。

先从源码和测试理解其真实业务目的。

---

# 39. Native Context Provider —— P0

这是与 `shutu-agent` 集成的最重要能力。

Knowledge 必须实现：

```text
Extension Context Provider
```

从而允许：

```text
User Message
      ↓
Agent Turn
      ↓
Model Step
      ↓
Extension Context Provider
      ↓
shutu-knowledge retrieval
      ↓
Evidence
      ↓
ContextContribution
      ↓
Agent Context Budget
      ↓
LLM
```

---

# 40. Knowledge 不拥有 Agent Context

Knowledge 只能返回：

```text
ContextContribution
```

不能：

```text
直接修改 Agent messages
直接追加 Prompt
修改 Session
控制 Compaction
```

Agent 保持 context ownership。

---

# 41. Context Cadence

根据知识库真实需求配置合适的：

```text
once_per_turn
before_every_model_call
on_user_input_change
after_tool_result
manual
```

不要一开始强制所有检索：

```text
before_every_model_call
```

需要根据成本和语义设计默认策略。

---

# 42. after_tool_result

`shutu-agent` 已实现真实：

```text
ToolResultBoundary
```

以及：

```text
per-provider at-most-once automatic consumption
```

Knowledge 应直接使用 Agent 提供的正式行为。

不要在 Knowledge 自己：

```text
猜 StepID
维护 Agent ToolResult sequence
```

---

# 43. Retrieval Request

Knowledge Context Provider 可依据 Extension Contract 获得被授权的：

```text
session
turn
step
workspace
user input
```

根据真实 SDK 能力使用。

不要要求更多权限，除非确有需要。

---

# 44. Evidence 输出

ContextContribution 应尽量包含：

```text
content
source
priority
metadata
token hint
```

实际字段以 Extension Contract 为准。

来源信息应能支持模型：

```text
解释证据来自哪里
```

---

# 45. Agent Token Budget

Knowledge 不决定最终 Agent context window。

Knowledge 可以：

```text
内部控制 retrieval top-k
内部控制 reranker candidates
内部做 context composing
提供估算
```

但最终：

```text
Agent Context Budget
```

必须继续由 `shutu-agent` 控制。

---

# 46. Knowledge Tools

完整实现 dsh-knowledge 暴露给 Agent 的 Tool 能力。

当前已知可能包括：

```text
knowledge_search
knowledge_list_bases
knowledge_create_base
knowledge_delete_base
knowledge_add_document
knowledge_list_documents
knowledge_delete_document
knowledge_import_url
knowledge_refresh_url
knowledge_stats
knowledge_get_document
knowledge_read_document
knowledge_reindex_document
knowledge_reindex_base
```

但：

> 不以此列表为最终依据。

必须审计当前 dsh-knowledge 源码获取准确 Tool inventory。

---

# 47. Tool Registry Integration

Knowledge Tool 通过：

```text
Extension Tool Contribution
```

或现有 MCP/Extension 机制进入：

```text
shutu-agent Tool Registry
```

不得建立第二套 Agent Tool Registry。

---

# 48. Tool Risk

每个 Tool 必须声明真实风险，例如：

```text
search/read
→ read

create/import/reindex
→ write

delete
→ destructive
```

最终审批策略由 Agent 决定。

Knowledge 不得绕过 Agent approval。

---

# 49. Lifecycle

Knowledge 必须实现 Extension Lifecycle：

```text
initialize
ready
health
shutdown
restart compatibility
```

支持 `shutu-agent` 作为：

```text
managed extension
```

运行。

如果也支持独立服务模式：

```text
shutu-knowledge serve
```

更好。

---

# 50. Health

Health 至少区分：

```text
process healthy
database healthy
index subsystem healthy
model availability
```

但不要因为可选 reranker 不可用就把整个 Extension 判死，除非配置要求它 required。

---

# 51. Events

只订阅 Knowledge 真正需要的 Agent Events。

不要：

```text
subscribe all
```

建议先审计是否真正需要：

```text
turn
tool
context
session.started
```

等事件。

Native Context Provider 不应通过 Event 模拟。

---

# 52. Web UI

实现完整 Knowledge Web UI。

目标：

```text
Shutu Agent
├── ...
└── Knowledge
      ├── Overview
      ├── Knowledge Bases
      ├── Documents
      ├── Import
      ├── Retrieval Test
      ├── Models
      └── Settings
```

具体页面以 dsh-knowledge 实际能力为准。

---

# 53. Web 必须通过 Extension Contribution 接入

Knowledge 自己提供 Web App。

Agent 负责：

```text
Navigation
Reverse Proxy
Auth shell
Route
```

Knowledge 负责：

```text
business UI
Knowledge API
business state
```

禁止修改 shutu-agent frontend。

---

# 54. Native Navigation

安装并启用 Extension 后：

```text
Knowledge
```

应通过 Agent 的：

```text
/api/extensions
```

等正式机制自动出现。

不能要求用户手工修改 Agent 菜单。

---

# 55. UI 独立构建

Knowledge Web UI 应在：

```text
shutu-knowledge
```

自己的 build pipeline 中完成。

不要编译进：

```text
shutu-agent/web
```

---

# 56. Retrieval Test

必须实现 dsh-knowledge 对应的 Retrieval Test 能力。

用户能够：

```text
输入 query
选择 Knowledge Base
执行 retrieval
查看：
  BM25 candidates
  Vector candidates
  Fusion result
  Rerank result
  Final context
```

具体可视化以原项目能力为准。

这是非常重要的可调试功能，不应省略。

---

# 57. Explainability

检索测试和 API 应尽量允许分析：

```text
为什么这个 chunk 被选中
各阶段 score
最终 rank
source
```

便于调 RAG。

---

# 58. Background Jobs

大文件处理不能堵塞 Web / Agent。

设计：

```text
Import Job
Parse Job
Embedding Job
Reindex Job
Refresh Job
```

根据项目复杂度选择轻量实现。

不要过度设计分布式队列。

---

# 59. Job Recovery

需要考虑：

```text
process crash
partial import
embedding interrupted
reindex interrupted
```

至少保证数据库不会进入不可恢复状态。

---

# 60. Incremental Update

目录/URL/文件刷新尽量做到：

```text
changed
→ update

unchanged
→ skip
```

避免每次全部重新 embedding。

行为参考 dsh-knowledge。

---

# 61. Duplicate Handling

考虑：

```text
same file
same URL
same content hash
same path changed
```

的去重和更新策略。

---

# 62. Database Migration

必须建立：

```text
versioned migrations
```

不要把：

```text
CREATE TABLE IF NOT EXISTS
```

散落在业务代码中作为长期 schema 管理机制。

---

# 63. Configuration

Knowledge 自己拥有配置，例如：

```text
database
raw store
chunking
embedding
reranker
retrieval
OCR
models
Web
jobs
```

不要把业务字段加入 `shutu-agent` config。

---

# 64. CLI

建议至少提供：

```text
shutu-knowledge serve
shutu-knowledge doctor
shutu-knowledge version
```

如果 dsh-knowledge 还有有价值 CLI 功能，可对应实现。

---

# 65. Doctor

`doctor` 应检查：

```text
database
migrations
model availability
parser dependencies
OCR dependencies
storage permissions
extension configuration
```

便于独立排障。

---

# 66. Observability

Knowledge 自己记录：

```text
import duration
parse duration
chunk count
embedding duration
retrieval duration
rerank duration
candidate count
context count
model error
job failure
```

同时通过 Agent Extension observability 暴露集成层状态。

---

# 67. 日志安全

默认不要记录：

```text
完整敏感文档
完整 retrieval evidence
用户完整问题
credentials
```

除非 debug mode 明确允许。

---

# 68. 性能

建立 benchmark：

```text
document ingestion
chunking
embedding
BM25 query
vector query
hybrid retrieval
rerank
end-to-end RAG
```

不要用极小数据集宣称性能优秀。

---

# 69. Retrieval Benchmark

建立可重复 benchmark dataset。

至少覆盖：

```text
exact lexical query
semantic query
multi-document
near duplicate
long document
Chinese
English
mixed language
```

如果目标支持其它语言，也加入。

---

# 70. dsh-knowledge 行为对照测试

对于关键算法和流程：

尽可能构造：

```text
same input
dsh-knowledge output behavior
shutu-knowledge output behavior
```

比较：

```text
功能
排序趋势
边界条件
错误语义
```

不是要求浮点 score 完全一样，但行为应等价。

---

# 71. 测试层级

必须包含：

```text
Unit
Integration
Extension Integration
Web
E2E
Regression
Benchmark
```

---

# 72. Extension Integration Test

必须真正启动：

```text
shutu-agent
+
shutu-knowledge external process
```

而不是 mock Extension Contract。

至少验证：

```text
Agent discovers Knowledge
Handshake succeeds
Health ready
Knowledge Web navigation appears
Knowledge tools enter registry
Context Provider injects evidence
Tool approval works
Events work
Restart recovers
Shutdown clean
```

---

# 73. Removal Test

非常重要：

移除/disable：

```text
shutu-knowledge
```

后：

```text
Agent 正常运行
Knowledge menu disappears
Knowledge tools disappear
Knowledge context injection disappears
其它 Agent 功能不受影响
```

这是单向依赖最关键验收之一。

---

# 74. Upgrade Independence Test

模拟：

```text
Agent version A
→ Agent version B
```

只要：

```text
Extension Protocol v1 compatible
```

Knowledge 不修改源码仍能运行。

---

# 75. Knowledge Upgrade Test

Knowledge 内部升级：

```text
new parser
new reranker
new embedding
new chunk strategy
```

Agent 不应需要修改。

---

# 76. 禁止 Agent-specific Domain Leakage

Knowledge 可以知道：

```text
Extension Contract
```

但核心 Knowledge Engine 最好不要直接依赖：

```text
Agent-specific DTO
```

推荐：

```text
Knowledge Core
        ↑
Extension Adapter
```

而不是：

```text
Knowledge Core = Agent Extension implementation
```

这样未来 Knowledge 可以：

```text
standalone
CLI
Web
Agent extension
```

多入口复用。

---

# 77. 推荐内部架构

目标：

```text
                  ┌──────────────────┐
                  │ Knowledge Core   │
                  └────────┬─────────┘
                           │
        ┌──────────────────┼──────────────────┐
        │                  │                  │
   Extension Adapter     Web API            CLI
        │
        ▼
   shutu-agent v1
```

而 Knowledge Core 包括：

```text
Ingestion
Parsing
Chunking
Storage
Indexing
Retrieval
Reranking
Context
```

---

# 78. 不要让 Agent Adapter 污染 Core

例如不要让：

```text
retrieval.Search()
```

要求参数：

```text
extension.ContextRequest
```

应该映射：

```text
extension.ContextRequest
        ↓
KnowledgeQuery
        ↓
Knowledge Core
```

---

# 79. 开发阶段划分

不要一次大爆炸完成。

## Phase 0 — Source Audit

输出：

```text
dsh_knowledge_capability_inventory.md
dsh_knowledge_equivalence_matrix.md
agent_contract_mapping.md
```

---

## Phase 1 — Project Foundation

完成：

```text
repository structure
config
logging
storage
migrations
health
CLI
Extension handshake
```

---

## Phase 2 — Knowledge Core

完成：

```text
KB CRUD
Document lifecycle
Parser registry
Chunking
Persistence
```

---

## Phase 3 — Retrieval

完成：

```text
BM25
Embedding
Vector
Hybrid
RRF
MMR
Reranker
Context Composer
```

---

## Phase 4 — Advanced Ingestion

完成：

```text
OCR
URL
Directory
refresh
incremental update
advanced parsers
semantic chunking
```

---

## Phase 5 — Agent Native Integration

完成：

```text
Context Provider
Tools
Risk metadata
Events
Lifecycle
Health
```

---

## Phase 6 — Web

完成：

```text
Knowledge UI
Agent Native Navigation
Retrieval Test
Model management
Settings
```

---

## Phase 7 — Equivalence Audit

逐项重新对照：

```text
dsh-knowledge
```

发现遗漏继续补齐。

---

## Phase 8 — Hardening

完成：

```text
race
crash
restart
large documents
bad documents
model unavailable
index corruption
migration
cancellation
resource cleanup
```

---

# 80. 每阶段必须提交

每阶段：

```text
audit
→ design
→ implementation
→ tests
→ self-review
→ commit
```

不要累计几十个不相关功能后一次提交。

---

# 81. Git 安全规则

开始任务时：

```bash
git status
git branch --show-current
git remote -v
```

确认当前工作目录是：

```text
shutu-knowledge
```

不是 `shutu-agent`。

---

# 82. Agent 仓库不可变检查

如果本地存在：

```text
<SHUTU_AGENT_PATH>
```

开始前记录：

```bash
git -C <SHUTU_AGENT_PATH> status --porcelain
git -C <SHUTU_AGENT_PATH> rev-parse HEAD
```

结束后再次执行。

必须证明：

```text
HEAD unchanged
+
没有因为本任务产生新的 tracked modifications
```

如果 Agent 原本就有用户未提交改动：

不要修改、不要清理、不要 reset。

只记录基线。

---

# 83. 严禁跨仓库提交

所有本任务 commit：

```text
只能出现在 shutu-knowledge repository
```

如果发现：

```text
shutu-agent git diff
```

包含本任务修改：

立即判定：

```text
ARCHITECTURE VIOLATION
```

撤销本任务对 Agent 的修改，但不得覆盖用户原有改动。

---

# 84. Reference Repository 也保持只读

`dsh-knowledge` 同样作为：

```text
reference repository
```

不要为了方便修改原仓库。

需要实验代码：

复制到：

```text
shutu-knowledge
```

合法位置。

---

# 85. Definition of Done

只有以下全部满足，项目才可以声明：

```text
SHUTU-KNOWLEDGE V1 READY
```

---

## Gate A — One-way dependency

```text
shutu-knowledge
    ↓
shutu-agent public Extension API
```

成立。

Agent 没有任何 Knowledge modification。

---

## Gate B — No internal dependency

搜索：

```text
shutu-agent/internal
```

结果必须：

```text
0 production imports
```

---

## Gate C — dsh-knowledge capability equivalence

Capability Matrix：

```text
所有目标功能
```

必须：

```text
PASS
```

或有明确：

```text
BLOCKED / N/A
```

理由。

不得存在未经解释的遗漏。

---

## Gate D — Native Context

真实：

```text
User
→ Agent
→ Knowledge retrieval
→ ContextContribution
→ Model
```

通过。

---

## Gate E — Tools

Knowledge Tools 进入 Agent 原 Tool Registry。

Approval 生效。

---

## Gate F — Web Native

Knowledge 菜单：

```text
动态出现
```

不修改 Agent frontend。

---

## Gate G — Lifecycle

```text
start
ready
health
restart
shutdown
```

通过。

---

## Gate H — Failure Isolation

至少：

```text
Knowledge crash
Embedding unavailable
Reranker timeout
Bad document
OCR failure
Web unavailable
```

不会导致 Agent Core crash。

---

## Gate I — Removal

删除 Knowledge 后：

```text
Agent still healthy
```

---

## Gate J — Independent Upgrade

Agent / Knowledge 均可独立版本升级。

---

# 86. 最终必须生成的文档

至少：

```text
README.md

docs/
├── architecture.md
├── agent_integration.md
├── dsh_knowledge_capability_inventory.md
├── dsh_knowledge_equivalence_matrix.md
├── agent_contract_mapping.md
├── agent_extension_gap_report.md
├── ingestion.md
├── retrieval.md
├── models.md
├── web.md
├── configuration.md
├── deployment.md
├── troubleshooting.md
└── testing.md

THIRD_PARTY_NOTICES.md
```

---

# 87. 最终实现报告

生成：

```text
shutu_knowledge_implementation_report.md
```

必须回答：

### 1

是否实现 dsh-knowledge 全部目标能力？

列出：

```text
PASS
PARTIAL
BLOCKED
N/A
```

---

### 2

是否修改过：

```text
shutu-agent
```

必须：

```text
NO
```

如果 YES：

```text
FINAL RESULT = FAIL
```

---

### 3

是否存在：

```text
import shutu-agent/internal/...
```

必须：

```text
NO
```

---

### 4

是否存在 Agent Contract Gap？

如果有：

列出 Gap ID。

不能因为 Gap 而私自修改 Agent。

---

### 5

是否真正实现：

```text
automatic RAG
+
tools
+
approval
+
web
+
events
+
lifecycle
```

---

### 6

是否经过真实：

```text
shutu-agent + shutu-knowledge
```

外部进程集成测试？

必须提供测试证据。

---

# 88. 最终审计

开发完成后重新从零审计：

```text
dsh-knowledge
        vs
shutu-knowledge
```

不要只根据最初 inventory。

因为开发期间可能漏掉隐藏功能。

进行：

```text
source tree comparison
tool comparison
config comparison
UI comparison
workflow comparison
test comparison
```

---

# 89. 不允许自己降低目标

如果发现：

```text
实现全部功能工作量很大
```

不要自行把目标改成：

```text
MVP
核心版
简化版
第一阶段够用
```

除非用户明确要求。

当前目标是：

# dsh-knowledge capability equivalence

不是 MVP。

---

# 90. 遇到复杂功能时

不要跳过。

应该：

```text
研究源码
理解行为
设计等价实现
实现
测试
```

如果确实由于 Agent Contract 被阻塞：

```text
Gap Report
```

而不是偷改 Agent。

---

# 91. 最终项目关系

必须最终保持：

```text
                    shutu-agent
                        ▲
                        │
                Extension Platform v1
                        │
                        │ one-way dependency
                        │
                 shutu-knowledge
                 ┌──────┼──────┐
                 │      │      │
              Core    Web   Models
```

而绝不能变成：

```text
shutu-agent
    ↕
shutu-knowledge
```

---

# 92. 最终开发原则

始终遵守：

> `shutu-agent` 提供通用机制，`shutu-knowledge` 提供知识库领域能力。

Agent owns：

```text
Agent loop
Session
Context policy
Tool policy
Approval
Extension lifecycle framework
Web shell
Extension observability
```

Knowledge owns：

```text
Documents
Knowledge bases
Parsing
OCR
Chunking
Embedding
Indexes
Retrieval
Reranking
Context composition
Models
Knowledge data
Knowledge UI
Knowledge configuration
```

---

# 93. 最终目标

我们需要的不是：

```text
把 Knowledge 代码塞进 Agent
```

而是：

> 一个独立的 `shutu-knowledge` 项目，通过 `shutu-agent Extension Platform v1` 接入以后，在用户看来与 Agent 原生知识库能力没有区别。

同时保证：

```text
独立 Git
独立版本
独立构建
独立测试
独立发布
独立升级
单向依赖
```

---

# 94. 开始执行方式

请首先完成：

```text
Phase 0 — Source Audit
```

不要一开始直接大量写代码。

第一轮先输出并提交：

```text
docs/dsh_knowledge_capability_inventory.md
docs/dsh_knowledge_equivalence_matrix.md
docs/agent_contract_mapping.md
docs/agent_extension_gap_report.md
docs/architecture.md
```

在这一步中：

1. 完整读取 dsh-knowledge。
2. 完整读取 shutu-agent Extension Platform v1 的公开文档、SDK 和测试。
3. 建立能力映射。
4. 标出任何 Agent Contract Gap。
5. 设计 shutu-knowledge 自身架构。
6. 确认没有修改 shutu-agent。

然后继续按照 Phase 1 → Phase 8 实施，不需要等待人工确认；除非遇到安全、许可证或无法通过现有 Extension Contract 解决的真正阻塞。

---

# 95. 最终验收结论格式

最终只允许给出以下之一：

```text
SHUTU-KNOWLEDGE V1 READY
```

或者：

```text
SHUTU-KNOWLEDGE NOT READY
```

如果 NOT READY：

必须列出明确的：

```text
remaining capability gaps
Agent Contract gaps
failed tests
license blockers
```

不要使用模糊的“基本完成”。

---

# 96. 最终一句架构检查

在每次重要设计决策前，都问：

> 如果明天把 shutu-knowledge 整个目录删除，shutu-agent 是否完全不受影响并可以继续正常升级？

如果答案不是：

```text
YES
```

则当前设计违反了本任务的核心架构原则。