# Shutu-Knowledge 0.4 — Knowledge Compiler & Semantic Memory

## 0. 当前稳定基线

Shutu-Knowledge 当前正式稳定版本：

```text
v0.3.1
```

当前状态：

```text
0.3.x MAINTENANCE MODE
```

0.3 已经完成：

* Local-first Knowledge Runtime
* Document IR
* Structured Parsing
* PDF / DOCX / PPTX / XLSX 结构化理解
* Structure-Aware Chunking
* Structured Citation
* Existing Hybrid Retrieval
* BM25 / Vector / RRF / Reranker / MMR
* Runtime / OCR / PDF / Office
* Generation isolation
* Durable operations
* Restart recovery
* Shutu-Agent integration
* Windows Tier-1 release

0.4 必须建立在 `v0.3.1` 之上。

禁止重新打开已经稳定的基础设施问题。

---

# 1. 0.4 的最终目标

0.4 的核心目标不是：

```text
增加更多 RAG 算法
```

也不是：

```text
简单增加 Wiki
```

更不是：

```text
直接增加 Knowledge Graph
```

真正目标是：

> 在不训练或修改 LLM 权重的情况下，通过 Knowledge Compilation、Semantic Memory、Knowledge Association、Hierarchical Abstraction 和 Dynamic Context Assembly，使 LLM 使用知识库时尽可能接近“已经学习并内化这些知识”的效果。

将这一目标定义为：

# Non-Parametric Knowledge Internalization

或者：

# Knowledge Internalization without Weight Training

---

# 2. 目标体验

传统 RAG：

```text
Question
   ↓
Search
   ↓
Top-K Chunks
   ↓
LLM
```

0.4 目标：

```text
Question
   ↓
Query Understanding
   ↓
Knowledge Activation
   ↓
Concept / Topic / Relation / Summary
   ↓
Evidence Retrieval
   ↓
Dynamic Context Compilation
   ↓
LLM
```

系统最终应该表现得更像：

> “我理解这套资料。”

而不是：

> “我刚刚搜到了几个相关段落。”

---

# 3. 0.4 不是参数训练

必须明确：

0.4 不进行：

* Continued Pretraining
* Fine-tuning
* LoRA
* Weight editing
* Model distillation

知识仍然保存在外部 Knowledge System。

但目标是获得类似参数化知识的：

* 快速知识激活
* 概念关联
* 跨文档理解
* 全局理解
* 多跳推理
* 层次抽象
* 持续更新

同时保留外部知识系统优势：

* 可更新
* 可删除
* 可版本化
* 可引用
* 可纠错
* 可追溯

---

# 4. 0.4 的整体架构目标

目标架构：

```text
                    Source Documents
                           │
                           ▼
                 0.3 Document IR
                           │
              ┌────────────┴────────────┐
              │                         │
              ▼                         ▼
        Evidence Layer          Knowledge Compiler
              │                         │
        Original Facts              Extract
        Structured Nodes            Normalize
        Exact Citation              Deduplicate
        Tables/Figures              Resolve
        Page/Slide/Sheet             Cluster
                                     Summarize
                                     Abstract
                                     Link
                                        │
                         ┌──────────────┼──────────────┐
                         ▼              ▼              ▼
                       Facts         Concepts        Topics
                         │              │              │
                         ├──────── Relations ──────────┤
                         │              │              │
                         ▼              ▼              ▼
                     Summaries       Wiki Pages     Hierarchy
                         │              │              │
                         └──────────────┼──────────────┘
                                        ▼
                                 Semantic Memory
                                        │
                                        ▼
                              Query Understanding
                                        │
                   ┌────────────────────┼────────────────────┐
                   ▼                    ▼                    ▼
             Semantic Memory       Existing RAG       Relations
                   │                    │                    │
                   └────────────────────┼────────────────────┘
                                        ▼
                                Context Compiler
                                        ▼
                                  Shutu-Agent / LLM
```

---

# 5. 第一原则

始终遵守：

```text
Evidence before abstraction.

Structure before graph.

Knowledge compilation before autonomous agents.

Derived knowledge must remain traceable.

No feature is accepted only because another project has it.

Benchmark before adoption.

Compatibility before redesign.
```

---

# 6. 0.4 第一阶段不是编码

0.4 必须首先执行：

# Research & Gap Analysis

不要直接开始开发 Wiki、GraphRAG 或 Knowledge Graph。

首先研究优秀项目，理解它们解决了什么问题、用了什么机制、哪些机制适合 Shutu-Knowledge。

---

# 7. 必须研究的项目

至少研究以下项目当前实现和核心设计。

## WeKnora

重点：

* Living Wiki
* Auto Wiki
* Wiki lifecycle
* Incremental maintenance
* Interlinked knowledge
* Version/history
* Knowledge Graph navigation
* Wiki + RAG relationship

重点回答：

> Wiki 在它的系统里究竟解决了什么普通 RAG 无法解决的问题？

---

## RAGFlow

重点研究：

* Knowledge Compilation
* Wiki
* Tree
* Graph
* PageIndex
* Mind Map
* Timeline
* To Skills
* Agentic RAG

重点：

> RAGFlow 为什么逐渐从 Retrieval 转向 Knowledge Compilation？

---

## HippoRAG / HippoRAG 2

重点：

* Non-parametric continual learning
* Associativity
* Long-term semantic memory
* Multi-hop retrieval
* Knowledge integration
* Memory activation

重点：

> 如何让外部知识更接近人类长期记忆，而不是 top-K 检索？

---

## LightRAG

重点：

* Entity extraction
* Relation extraction
* Graph + Vector
* Incremental update
* Local/global retrieval

重点：

> 图结构什么时候确实改善了 Retrieval，而什么时候只是增加复杂度？

---

## Microsoft GraphRAG

重点：

* Entity
* Relation
* Community
* Community hierarchy
* Community summary
* Local search
* Global search

重点：

> 如何回答“整个知识库总体说明什么”这类全局问题？

---

## OpenSPG / KAG

重点：

* Knowledge + Chunk 双向索引
* Professional-domain knowledge
* Semantic alignment
* Logical-form guided reasoning
* Knowledge provenance

重点：

> 对 SmartCare、3GPP、网络产品文档这类专业知识库，有哪些机制比普通 GraphRAG 更有价值？

---

## Graphiti

重点：

* Temporal Knowledge
* Fact lifecycle
* Incremental knowledge update
* Temporal relation
* Provenance
* Agent memory

重点：

> 如何处理知识变化，而不是让旧知识一直残留？

---

## RAPTOR

重点：

* Recursive abstraction
* Clustering
* Hierarchical summaries
* Multi-level retrieval

重点：

> 如何解决“全文/整个知识库整体讲了什么”的问题？

---

## RAG-Anything

重点：

* Multimodal knowledge
* Table
* Figure
* Formula
* Cross-modal representation

重点：

> 0.3 已经有 Structured Document IR 后，哪些 multimodal knowledge 表示值得继续吸收？

---

## LlamaIndex PropertyGraph

重点：

* Property graph abstraction
* Vector + graph retrieval
* Graph schema
* Query composition

重点：

> 是否存在可以借鉴但无需引入完整 Graph DB 的轻量模式？

---

# 8. 允许增加参考项目

如果研究过程中发现其它明显优秀且相关的项目，可以加入。

但必须满足：

> 与 Knowledge Internalization / Semantic Memory / Knowledge Compilation 有直接关系。

禁止为了“调研完整”无限扩大范围。

---

# 9. 第一阶段必须输出研究报告

创建：

```text
docs/0.4_reference_research.md
```

至少包含：

| 项目 | 核心机制 | 解决问题 | 适合 Shutu | 不适合 Shutu | 借鉴方式 |
| -- | ---- | ---- | -------- | --------- | ---- |

禁止只列功能。

必须分析：

```text
Mechanism
→ Why it works
→ Cost
→ Shutu applicability
```

---

# 10. 必须建立 Gap Analysis

创建：

```text
docs/0.4_gap_analysis.md
```

比较：

```text
Shutu 0.3
vs
目标 Knowledge Internalization 能力
```

至少分析：

* Local fact recall
* Global understanding
* Cross-document reasoning
* Multi-hop reasoning
* Concept abstraction
* Topic abstraction
* Relation awareness
* Temporal knowledge
* Incremental knowledge update
* Long-document understanding
* Evidence traceability
* Knowledge reuse
* Query routing
* Context token efficiency

---

# 11. 禁止照抄任何项目架构

必须遵守：

> Learn from mechanisms, not architectures.

不要因为：

```text
WeKnora uses X
RAGFlow uses Y
GraphRAG uses Z
```

就复制对应技术栈。

Shutu-Knowledge 继续坚持：

* Local-first
* Offline-first
* Single-user oriented
* Lightweight
* SQLite
* Single-process preferred
* Agent-native
* Evidence-grounded

---

# 12. 0.4 禁止引入的基础设施

除非通过 benchmark 明确证明不可替代，否则禁止主动引入：

* Neo4j
* PostgreSQL
* Elasticsearch
* Redis
* Kafka
* distributed queue
* graph server
* separate microservice
* cloud-only dependency

图关系优先考虑：

```text
SQLite
```

或现有 storage abstraction。

---

# 13. Phase 1 — 定义 Knowledge Model

研究完成后，不立即做 Wiki。

首先定义统一：

# Knowledge Model

建立在 Document IR 之上。

建议至少支持：

```text
Fact
Concept
Topic
Summary
Relation
KnowledgePage
```

不要一开始设计几十种实体。

---

# 14. Knowledge Unit

建议统一抽象：

```text
KnowledgeUnit
```

字段可考虑：

```text
id
type

title
content

source_node_ids
source_chunk_ids

confidence

generation

created_at
updated_at

compiler
compiler_version

metadata
```

最终结构必须根据现有代码设计。

不要机械照抄提示词。

---

# 15. Derived Knowledge 必须可追溯

所有派生知识必须存在：

```text
derived_from
```

链。

例如：

```text
WikiPage
   ↓
Concept
   ↓
Fact
   ↓
DocumentIR Node
   ↓
Original File
```

最终回答需要原始事实支持时：

必须能够回到原始 Evidence。

---

# 16. Fact Layer

0.4 应考虑从 Document IR 中构建：

```text
Fact
```

Fact 不是简单 Chunk。

例如：

```text
"Qwen3-Embedding-0.6B uses 1024-dimensional vectors."
```

应关联：

```text
document
section
page
source node
citation
```

但不要强迫所有文本都转成 Fact。

优先处理高价值、明确事实。

---

# 17. Concept Layer

从大量 Fact / Sections 中识别：

```text
Concept
```

例如：

```text
Restricted Token
Vector Retrieval
Hybrid Search
Document IR
Reranker
```

Concept 应连接：

```text
facts
sections
documents
related concepts
```

---

# 18. Topic Layer

比 Concept 更高一级：

```text
Topic
```

例如：

```text
Windows Process Isolation
Knowledge Retrieval
Document Intelligence
```

用于：

* Query routing
* Global understanding
* Context selection

---

# 19. Hierarchical Knowledge

支持：

```text
Fact
  ↓
Concept
  ↓
Topic
  ↓
Domain
```

但不要建立固定 ontology。

先允许轻量层级。

---

# 20. Relation

关系只实现真正有价值的少数类型。

初期可考虑：

```text
derived_from
part_of
mentions
related_to
supports
contradicts
supersedes
same_topic
```

禁止一开始做上百种 Relation Schema。

---

# 21. Temporal Knowledge

研究 Graphiti 等方案后，至少设计：

```text
valid_from
valid_to
superseded_by
```

或等价机制。

目标解决：

> 旧版本知识和新版本知识冲突时，系统知道哪个是当前有效知识。

例如：

```text
v0.2 behavior
vs
v0.3 behavior
```

不应该都被当成同等“当前事实”。

---

# 22. Knowledge Compiler

0.4 核心组件：

# Knowledge Compiler

负责：

```text
Document IR
    ↓
Extract
    ↓
Normalize
    ↓
Deduplicate
    ↓
Resolve
    ↓
Cluster
    ↓
Summarize
    ↓
Link
    ↓
Abstract
    ↓
Knowledge Units
```

Knowledge Compiler 不是一个单 Prompt。

它必须是可重复、可诊断、可增量的 pipeline。

---

# 23. Knowledge Compiler 必须分阶段

不要写：

```text
LLM: "请理解整个知识库并生成 Wiki"
```

必须拆解为可控制步骤。

例如：

```text
PH1 extract candidates
PH2 normalize
PH3 resolve duplicates
PH4 cluster topics
PH5 summarize
PH6 link relations
PH7 publish knowledge views
```

每步应：

* 可缓存
* 可重试
* 可调试
* 可追溯

---

# 24. LLM 的角色

LLM 可以参与：

* Fact extraction
* Concept extraction
* Relation proposal
* Topic clustering assistance
* Summary
* Wiki composition
* Contradiction detection

但：

## Core Evidence

不能依赖 LLM 才存在。

## Document IR

不能重新依赖 LLM。

## Existing RAG

LLM 不可用时仍应工作。

LLM Knowledge Compilation 必须：

```text
optional
cached
versioned
incremental
```

---

# 25. Knowledge Compiler Versioning

必须记录：

```text
compiler_version
model
model_version
prompt_version
```

如果未来修改 compiler：

系统必须知道哪些 Knowledge Units 是旧版本生成的。

---

# 26. Incremental Compilation

这是 0.4 的 Release 核心能力之一。

不能：

```text
修改一个文档
→ 重编整个知识库
```

目标：

```text
Document changed
     ↓
Affected IR Nodes
     ↓
Affected Facts
     ↓
Affected Concepts
     ↓
Affected Topics
     ↓
Affected Wiki Pages
     ↓
Incremental Recompile
```

---

# 27. Delete Propagation

删除一个文档后：

不能只删 Chunk。

必须考虑：

```text
Fact
Concept
Relation
Summary
Wiki
```

哪些需要：

* remove
* recompute
* downgrade confidence
* keep because other evidence still supports

禁止产生 orphan knowledge。

---

# 28. Contradiction Handling

0.4 应至少能够识别：

```text
同一 subject
同一 attribute
不同 value
```

的潜在冲突。

例如：

```text
Version A:
Max context = 128k

Version B:
Max context = 1M
```

不要立即自动决定真伪。

应该保留：

```text
source
version
time
provenance
```

让系统知道存在：

```text
supersedes / conflict
```

---

# 29. Semantic Memory

Knowledge Units 构成：

# Semantic Memory

Semantic Memory 不替代 Evidence Index。

形成双层：

```text
Evidence Memory
+
Semantic Memory
```

---

# 30. Evidence Memory

继续使用 0.3：

```text
Document IR
Chunk
BM25
Vector
RRF
Rerank
Citation
```

不要重写。

---

# 31. Semantic Memory

新层包含：

```text
Facts
Concepts
Topics
Summaries
Relations
Wiki Pages
```

用于：

* Query understanding
* High-level routing
* Global questions
* Multi-hop reasoning
* Context compression

---

# 32. Wiki 只是一个 View

非常重要：

# Wiki 不是核心数据模型

Wiki 应该是：

```text
Knowledge Units
      ↓
Wiki View
```

而不是：

```text
Documents
→ Wiki
→ 一切知识全部存在 Markdown
```

这样未来还可以生成：

```text
Tree
Mind Map
Timeline
Topic Map
Skills
```

而不需要重做知识底层。

---

# 33. Living Wiki

可以参考 WeKnora。

实现：

```text
WikiPage
```

至少支持：

* title
* summary
* sections
* concept links
* related pages
* source evidence
* generation/version

---

# 34. Wiki 必须可重新生成

Wiki 属于 derived view。

必须能够：

```text
rebuild
```

而不会影响：

```text
original evidence
```

用户手工编辑如果未来支持，必须设计：

```text
generated content
vs
user-authored content
```

不要 0.4 初期混在一起。

---

# 35. Cross-document Knowledge

0.4 重点必须从：

```text
single document understanding
```

升级到：

```text
cross-document understanding
```

例如多个文档共同说明一个 Topic：

```text
Doc A
Doc B
Doc C
  ↓
Topic
  ↓
Cross-document Summary
```

---

# 36. Global Query

必须支持这种问题：

```text
整个知识库主要讲了哪些技术方向？
```

```text
这些版本的架构演进是什么？
```

```text
不同文档对 Storage 的要求有哪些共同点和差异？
```

普通 top-K RAG 很难很好回答。

0.4 必须通过：

```text
Topic
Hierarchy
Summary
Relations
```

改善。

---

# 37. Local Query

普通事实问题仍应优先使用 Evidence RAG。

例如：

```text
Qwen3 Embedding 维度是多少？
```

不要为了任何问题都先跑 Knowledge Graph。

---

# 38. Query Understanding

增加轻量：

# Query Understanding

判断查询偏向：

```text
Exact Fact
Document-local
Cross-document
Global Summary
Comparison
Multi-hop
Temporal
```

不要做复杂 Agent Planner。

---

# 39. Retrieval Routing

根据 Query 类型选择：

```text
Evidence Retrieval
Semantic Memory Retrieval
Relation Traversal
Hierarchical Summary
Hybrid
```

例如：

```text
Exact Fact
→ Evidence first
```

```text
Global Question
→ Topic/Summary first
```

```text
Multi-hop
→ Concept/Relation + Evidence
```

---

# 40. Context Compiler

0.4 的另一核心组件：

# Context Compiler

目标：

> 给 LLM 的不是一堆独立 top-K chunks，而是一份针对当前问题编译后的 Knowledge Context。

例如：

```text
Query Intent

Relevant Topic Summary

Relevant Concepts

Relevant Relations

Critical Facts

Exact Evidence

Citations
```

---

# 41. Context Compiler 必须控制 Token

不能：

```text
知识越多
→ context 越长
```

必须优化：

```text
information density
```

支持：

* dedup
* hierarchy
* summary
* evidence selection
* token budget

---

# 42. Context Package

建议设计内部结构，例如：

```text
ContextPackage

query

intent

knowledge_summary[]

concepts[]

relations[]

facts[]

evidence[]

citations[]

token_budget

diagnostics
```

具体 API 根据现有架构调整。

---

# 43. Dynamic Context

同一知识库针对不同问题：

应生成不同 context。

不要提前把整个 Wiki 塞给 LLM。

---

# 44. Multi-hop Reasoning

建立测试：

```text
Fact A
→ Relation
→ Concept B
→ Relation
→ Fact C
```

让系统能够找到跨文档链。

但 0.4 不要求实现通用逻辑证明器。

---

# 45. Graph 原则

0.4 可以有：

```text
Knowledge Graph
```

但它应该首先只是：

```text
Knowledge Units + Relations
```

而不是先部署 Graph Database。

优先：

```text
SQLite adjacency / relation table
```

证明有价值再扩展。

---

# 46. GraphRAG 原则

GraphRAG 可以：

```text
研究
prototype
benchmark
```

但不要默认成为正式主路径。

只有当它在：

* multi-hop
* global
* cross-doc

明显优于现有方案时才保留。

---

# 47. RAPTOR / Hierarchical Summary

研究并实现最小层次摘要能力。

例如：

```text
Paragraph
↓
Section
↓
Document
↓
Topic
↓
Knowledge Base
```

但不要让 Summary 替代 Evidence。

---

# 48. Summary Provenance

任何 Summary 必须记录：

```text
source_units
```

并能回溯。

---

# 49. Knowledge Confidence

对于 LLM 派生知识，可记录：

```text
confidence
```

但不要建立复杂概率系统。

重点区分：

```text
directly extracted
inferred
summarized
conflicting
```

---

# 50. Knowledge Status

Knowledge Unit 可考虑：

```text
ACTIVE
STALE
SUPERSEDED
CONFLICTED
ORPHANED
```

根据实际需求精简。

---

# 51. Knowledge Refresh

当：

* Document changed
* Compiler changed
* Model changed

应可以标记：

```text
needs_refresh
```

不要自动全部重编。

---

# 52. Background Compilation

如果已有 durable operation：

复用。

不要另建第二套 job system。

Knowledge compilation 应能够：

* resume
* cancel
* retry
* report progress

---

# 53. Failure Isolation

某个 Document compilation 失败：

不能导致整个 Knowledge Base 不可用。

Existing RAG 必须继续工作。

---

# 54. Degradation Strategy

如果：

```text
Knowledge Compiler unavailable
```

系统应退化为：

```text
0.3 Structured RAG
```

不能整体失败。

---

# 55. Offline Strategy

核心 0.4 能力仍需支持：

```text
offline
```

如果 LLM enrichment 需要外部 API：

必须 optional。

优先支持本地 LLM。

---

# 56. Model Independence

Knowledge Compiler 不绑定特定模型。

必须允许：

```text
local model
external API model
```

但不要为此重构整个 provider 层。

复用现有能力。

---

# 57. Benchmark 是核心

0.4 所有机制都必须通过 benchmark 证明价值。

禁止：

> 因为某项目有这个功能，所以 Shutu 也做。

---

# 58. 建立 0.4 Benchmark

创建：

```text
benchmarks/knowledge_internalization/
```

或符合项目结构的位置。

必须比较：

```text
A. 0.2 Plain/Hybrid RAG
B. 0.3 Structured RAG
C. 0.4 Compiled Knowledge
```

---

# 59. Query Types

Benchmark 至少包括：

## Fact

```text
某参数是多少？
```

## Local document

```text
某章节说明什么？
```

## Global

```text
整份资料的核心架构是什么？
```

## Cross-document

```text
A/B/C 三个文档对某问题分别怎么描述？
```

## Multi-hop

```text
X 与 Z 之间通过什么机制关联？
```

## Temporal

```text
当前版本与旧版本有什么变化？
```

## Comparison

```text
两个方案差异是什么？
```

---

# 60. 指标

至少记录：

```text
Answer correctness
Evidence correctness
Citation correctness
Completeness
Multi-hop success
Global understanding
Cross-document consistency
Hallucination rate
Context tokens
Latency
```

---

# 61. Context Efficiency

0.4 必须特别关注：

```text
答案质量 / context token
```

目标不是：

```text
塞更多内容
```

而是：

```text
用更少、更高信息密度的 context
得到更完整答案
```

---

# 62. Ablation

至少做：

```text
Evidence only
Evidence + Summary
Evidence + Concept
Evidence + Relation
Full compiled context
```

比较贡献。

这样才能知道：

> 哪些 Knowledge Compiler 能力真的有价值。

---

# 63. 不保留无价值机制

如果某个机制：

* 增加复杂度
* 增加 latency
* 增加 token
* 没有明显提升 benchmark

则：

```text
remove or defer
```

---

# 64. SmartCare / Telecom Scenario

0.4 建议增加一个专业领域 corpus。

例如：

```text
SmartCare / Telecom / 3GPP
```

验证专业知识。

重点测试：

* 专业术语
* 缩写
* 多文档版本
* 规范之间关联
* 版本变化
* 复杂因果链

---

# 65. Code Knowledge Scenario

因为 Shutu-Knowledge 也面向个人代码知识库：

加入：

```text
code repository + docs
```

场景。

例如：

```text
某模块为什么这么设计？
```

```text
某 API 与 Storage 的关系？
```

```text
这个功能在哪几个文件实现？
```

但不要 0.4 重新开发完整 Code Intelligence Engine。

---

# 66. Storage Strategy

继续 SQLite。

建议新增逻辑表：

```text
knowledge_units
knowledge_relations
knowledge_sources
knowledge_versions
knowledge_compilation_runs
```

具体 schema 由实现决定。

不要为了理论美观过度 normalization。

---

# 67. Versioning

必须区分：

```text
Document generation
Knowledge generation
Compiler version
```

避免旧 Knowledge 与新 Evidence 混合。

---

# 68. Atomic Visibility

Knowledge recompile 时：

```text
new knowledge generation
        ↓
complete
        ↓
atomic switch
        ↓
old generation cleanup
```

不要让用户搜索到半编译状态。

---

# 69. Citation

0.4 的 Knowledge Answer 可以引用：

```text
Knowledge Unit
```

但最终必须能展开到：

```text
Original Evidence Citation
```

---

# 70. Explainability

建议提供诊断：

```text
Why was this knowledge activated?
```

至少开发模式可看到：

```text
query
→ topic
→ concept
→ fact
→ evidence
```

---

# 71. UI 范围

UI 只做必要功能。

建议：

## Knowledge Explorer

查看：

```text
Topics
Concepts
Wiki Pages
Relations
```

## Provenance

点击 Knowledge Unit：

看到：

```text
derived from which documents/nodes
```

## Compilation Status

查看：

```text
READY
STALE
COMPILING
FAILED
```

不要重新设计整个 Web UI。

---

# 72. Wiki UI

如果实现 Wiki：

支持：

* page list
* page content
* links
* evidence
* source documents

不要求：

* rich editor
* collaboration
* comments
* enterprise workflow

---

# 73. Graph UI

Graph visualization 不是 0.4 Release blocker。

如果低成本可以做：

作为 debug/exploration。

不要为漂亮图谱投入大量前端工作。

---

# 74. Agent Integration

Shutu-Agent 只需要消费：

```text
Compiled Context
Evidence
Citation
```

不要修改 Shutu-Agent 核心架构。

如果需要 Agent 新能力：

记录：

```text
docs/shutu_agent_0.4_requirements.md
```

不要跨仓库偷偷修改。

---

# 75. API

现有 search API 保持兼容。

可新增：

```text
search mode
knowledge search
context compile
```

但旧接口必须继续可用。

---

# 76. Query Modes

内部可以支持：

```text
AUTO
EVIDENCE
KNOWLEDGE
HYBRID
```

用户默认：

```text
AUTO
```

不要要求普通用户理解所有模式。

---

# 77. Auto Mode

AUTO 根据 query 判断：

```text
fact
global
cross-document
multi-hop
temporal
```

然后选择路径。

必须可以查看 diagnostics。

---

# 78. Phase 结构

0.4 不允许一次性做完。

建议：

## PH0 — Research

输出：

```text
0.4_reference_research.md
0.4_gap_analysis.md
```

不写业务代码。

---

## PH1 — Knowledge Model

完成：

```text
KnowledgeUnit
Relation
Provenance
Versioning
Storage
```

---

## PH2 — Knowledge Compiler v0

只实现：

```text
Fact
Concept
Topic
Summary
```

---

## PH3 — Incremental Compilation

实现：

```text
update
delete
recompile
generation isolation
```

---

## PH4 — Semantic Memory Retrieval

支持：

```text
Concept/Topic/Summary retrieval
```

---

## PH5 — Context Compiler

把：

```text
Semantic Memory
+
Evidence
```

编译成高质量 Context。

---

## PH6 — Query Routing

实现：

```text
Fact
Global
Cross-doc
Multi-hop
Temporal
```

轻量分类。

---

## PH7 — Living Wiki

基于 Knowledge Units 生成 Wiki View。

---

## PH8 — Relation / Lightweight Graph

只实现 benchmark 证明有价值的关系。

---

## PH9 — Benchmark & Ablation

比较：

```text
0.2
0.3
0.4
```

---

## PH10 — Stabilization / Release

---

# 79. Phase Stop Rule

每个 Phase 完成后必须回答：

```text
Does this materially improve Knowledge Internalization?
```

如果答案：

```text
NO
```

则不要继续扩展。

---

# 80. Commit 原则

每个 Phase：

* 小范围修改
* 独立测试
* 独立 commit
* 文档同步

禁止一个 commit 改几百个无关文件。

---

# 81. Token 控制

Codex 不要每个 Phase 重新全面审计仓库。

PH0 完成后：

后续阶段复用研究结论。

只读取：

```text
当前 Phase 相关代码
```

---

# 82. 兼容性

0.3 KB 升级到 0.4：

必须：

```text
PASS
```

即使用户不运行 Knowledge Compilation：

原有 RAG 仍然正常。

---

# 83. Optional Compilation

建议允许：

```text
Knowledge Compilation = optional
```

用户可以：

```text
只使用 0.3 RAG
```

或：

```text
启用 0.4 semantic memory
```

---

# 84. Existing KB

对于已有 KB：

不强制全量 rebuild Evidence Index。

只新增 Knowledge Compilation。

---

# 85. Release Gate

0.4 最终 Release Gate：

```text
Build                         PASS
Go Test                       PASS
Race                          PASS
Web                           PASS

0.3 KB Migration              PASS
0.3 Search Regression         PASS

Knowledge Model               PASS
Knowledge Compiler            PASS
Incremental Compilation       PASS
Delete Propagation            PASS
Generation Isolation          PASS

Semantic Memory Retrieval     PASS
Context Compiler              PASS
Query Routing                 PASS

Fact Queries                  PASS
Global Queries                PASS
Cross-document Queries        PASS
Multi-hop Queries             PASS
Temporal Queries              PASS

Evidence Citation             PASS
Knowledge Provenance          PASS

Benchmark vs 0.3              PASS
Context Efficiency            PASS

Restart Recovery              PASS
Agent Integration             PASS
Windows Package Smoke         PASS

No P0                         PASS
```

---

# 86. 0.4 成功判据

不能只说：

```text
功能完成
```

必须证明：

> 与 0.3 Structured RAG 相比，0.4 在全局理解、跨文档关联、多跳问题、知识复用或 context efficiency 上有可测量提升。

如果没有提升：

0.4 不能仅凭功能数量判定成功。

---

# 87. 非 Release Blocker

以下不作为 0.4 blocker：

* Neo4j
* Full GraphRAG
* Full Knowledge Graph
* Logic theorem prover
* Rich Wiki editor
* Multi-user collaboration
* SaaS
* Linux full packaging
* macOS full packaging
* Million-document scale
* Autonomous Agent memory
* Fine-tuning
* Model training

---

# 88. 0.5+ 预留

0.4 完成后才考虑：

```text
Advanced GraphRAG
Domain ontology
Logical reasoning
Autonomous knowledge maintenance
User feedback learning
Memory consolidation
Skill compilation
Knowledge-to-agent planning
```

---

# 89. 最终报告

最终必须输出：

## Research

研究过哪些项目。

提炼了哪些机制。

最终采纳哪些。

拒绝哪些。

为什么。

---

## Architecture

最终：

```text
Knowledge Model
Knowledge Compiler
Semantic Memory
Context Compiler
Query Routing
```

结构。

---

## Benchmark

至少提供：

```text
0.3 vs 0.4
```

在：

* Fact
* Global
* Cross-document
* Multi-hop
* Temporal

上的比较。

---

## Context Efficiency

输出：

```text
average context tokens
answer quality
```

---

## Migration

```text
0.3 → 0.4
PASS / FAIL
```

---

## Regression

确认：

```text
0.3 Structured RAG
```

没有明显倒退。

---

## CI

```text
push CI
tag CI
```

---

## Artifact

```text
filename
size
SHA-256
```

---

## Known Limitations

只列真实限制。

---

## Deferred

列入 0.5+。

---

## Release Status

只能：

```text
READY
```

或：

```text
NOT READY
```

---

# 90. 最终执行原则

0.4 的成功标准不是：

> Shutu-Knowledge 也有 Wiki、Graph、RAPTOR、GraphRAG 了。

而是：

> LLM 在使用 Shutu-Knowledge 时，明显减少“临时拼几个 Chunk 再猜答案”的行为，开始表现出对整个知识域的持续、关联、层次化理解。

最终目标：

```text
Documents
    ↓
Structured Evidence
    ↓
Knowledge Compilation
    ↓
Semantic Memory
    ↓
Dynamic Context
    ↓
LLM
```

让模型行为尽量接近：

> “我已经学习过这些资料。”

同时仍然保留：

```text
Evidence
Citation
Update
Delete
Version
Correction
```

这些外部知识系统比参数化知识更强的能力。

---

# 91. 最重要的边界

不要把 0.4 做成：

```text
Shutu-WeKnora
```

也不要做成：

```text
Mini RAGFlow
```

或者：

```text
Mini GraphRAG
```

而应该：

> 从所有优秀系统中提取真正有效的机制，经过 benchmark，只保留适合 Shutu-Knowledge 的部分。

最终形成：

# Shutu-native Knowledge Compiler

而不是任何项目的复制品。
