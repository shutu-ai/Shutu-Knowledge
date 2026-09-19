# Shutu-Knowledge 0.5 — Temporal Knowledge & Version-Aware Reasoning

## 0. 当前稳定基线

当前正式稳定版本：

```text
Shutu-Knowledge v0.4.0
```

0.4 已完成并验证：

* Knowledge Compiler
* Semantic Memory
* Context Compiler
* Query Routing
* Structured Evidence
* Document IR
* Cross-document Knowledge
* Global Understanding
* Multi-hop Support
* Provenance
* Citation
* Incremental Compilation
* Delete Propagation
* Atomic Knowledge Generations
* Restart Recovery
* Agent Integration

0.4 Real-World Validation 已完成。

真实验证 Corpus：

* Open5GS product documentation
* 3GPP TS 23.501 / 23.502
* Shutu-Knowledge source code + engineering documentation

固定 Query：

```text
120
```

真实验证结果：

```text
Overall support proxy:
0.817 → 0.858

Average context tokens:
1541 → 1520

Citation accuracy:
1.000

Unsupported claims:
0

Invalid provenance:
0
```

0.4 在以下能力上确认有正向提升：

* Global understanding
* Comparison
* Explanation
* Cross-document reasoning
* Multi-hop reasoning

但真实验证同时发现：

# Temporal / Version Selection Regression

这是当前最高优先级的真实能力 Gap。

0.5 必须只围绕这一 Gap 展开。

---

# 1. 0.5 核心目标

0.5 的唯一核心目标：

> 当同一知识主题存在多个版本、多个时间点、多个状态或相互替代的知识时，Shutu-Knowledge 能够识别“什么知识在什么时候有效”，并根据用户问题中的时间/版本意图选择正确 Evidence 和 Knowledge。

目标不是：

```text
找到所有相关版本
```

而是：

```text
知道哪个版本应该用于当前问题
```

---

# 2. 目标体验

当前可能出现：

```text
Version 26.2:
Feature A/B/C

Version 26.3:
Feature A/B/C/D
```

普通 Semantic Retrieval 可能同时命中：

```text
26.2
26.3
```

然后交给 LLM 自己猜哪个是当前版本。

0.5 目标：

```text
Knowledge A
valid_for = 26.2

Knowledge B
valid_for = 26.3

Knowledge B supersedes Knowledge A
```

用户问：

```text
当前版本支持哪些功能？
```

系统优先激活：

```text
26.3
```

用户问：

```text
26.2 支持哪些功能？
```

系统明确选择：

```text
26.2
```

用户问：

```text
从哪个版本开始支持 D？
```

系统能够识别：

```text
introduced_in = 26.3
```

---

# 3. 0.5 的核心能力

本版本只聚焦四个核心能力：

## A. Temporal Metadata

知识必须能够携带：

* version
* release
* valid_from
* valid_to
* published_at
* effective_at
* status
* source priority

---

## B. Supersession / Conflict Model

支持：

```text
supersedes
superseded_by
conflicts_with
valid_for
introduced_in
removed_in
```

---

## C. Temporal Query Understanding

能够识别：

```text
当前
现在
最新
旧版本
以前
从什么时候开始
什么时候废弃
26.2
26.3
Rel-17
Rel-18
2025
2026
```

等 temporal/version intent。

---

## D. Version-Aware Context Compilation

最终 Context Compiler 必须根据 Query Intent：

```text
选择当前有效知识
或
选择指定历史版本
或
展示版本变化
或
展示冲突
```

而不是简单把多个版本一起塞给 LLM。

---

# 4. 本版本严格禁止事项

0.5 禁止主动开发：

* Full GraphRAG
* Neo4j
* Full Knowledge Graph
* Ontology framework
* Logic theorem prover
* General-purpose reasoning engine
* Autonomous memory agent
* Memory consolidation 大重构
* New Vector DB
* PostgreSQL migration
* Elasticsearch
* Redis
* Kafka
* Distributed worker
* New parser architecture
* New Document IR
* New Embedding framework
* New Reranker framework
* New Agent architecture
* Full code intelligence engine

如果发现价值：

记录到：

```text
docs/0.6_candidate_gaps.md
```

不要在 0.5 实施。

---

# 5. 0.5 不重新研究所有项目

0.4 已完成广泛 Reference Research。

0.5 不允许重新做一轮：

```text
WeKnora
RAGFlow
HippoRAG
GraphRAG
KAG
Graphiti
RAPTOR
...
```

全面研究。

只允许针对：

```text
Temporal Knowledge
Versioned Knowledge
Supersession
Conflict Resolution
Temporal Retrieval
```

进行定向研究。

---

# 6. 定向参考研究

可重点参考：

## Graphiti

研究：

* temporal fact lifecycle
* fact validity
* update
* supersession
* temporal edge semantics

---

## KAG / OpenSPG

研究：

* professional knowledge alignment
* conflict
* versioned domain knowledge
* evidence relationship

---

## Temporal RAG / Temporal GraphRAG 相关实现

只研究：

> query-time temporal selection

不要扩成完整 GraphRAG。

---

# 7. Phase 0 — 精确复现当前 Regression

不要直接开发。

首先从现有：

```text
benchmarks/real_world_internalization/
```

提取所有 Temporal / Version Query。

确认当前 0.4 的真实失败模式。

至少回答：

* 是 Retrieval 命中了错误版本？
* Semantic Memory 合并了多个版本？
* Knowledge Compiler 丢失了版本信息？
* Query Router 没识别 temporal intent？
* Context Compiler 没优先正确版本？
* Final LLM 在正确 Context 下仍选错？
* Summary 把多个版本融合成错误“当前知识”？

---

# 8. 输出 Temporal Gap Analysis

创建：

```text
docs/0.5_temporal_gap_analysis.md
```

必须包含：

```text
Failure Type
Observed Example
Root Cause
Affected Layer
Recommended Fix
```

禁止只写：

> Temporal needs improvement.

---

# 9. 必须建立 Error Taxonomy

至少分类：

```text
T1 Version Metadata Missing
T2 Version Metadata Extracted Wrong
T3 Knowledge Merge Across Versions
T4 Superseded Fact Still Active
T5 Query Temporal Intent Missed
T6 Context Wrong-Version Selection
T7 Conflict Not Exposed
T8 Latest-Version Resolution Wrong
T9 Historical Query Resolved to Current
T10 Final LLM Temporal Reasoning Error
```

---

# 10. 先区分知识错误还是 LLM 错误

这是必须遵守的原则。

如果：

```text
Evidence correct
Temporal metadata correct
Context correct
```

但最终 LLM 回答错：

不要立即重构 Temporal Knowledge。

必须记录为：

```text
Final LLM reasoning issue
```

---

# 11. Phase 1 — Temporal Metadata Model

在现有 Knowledge Unit 基础上增加最小 Temporal Model。

不要新建独立 Temporal Database。

建议能力：

```text
valid_from
valid_to

published_at
effective_at

version
release

status
```

实际 schema 根据现有模型设计。

---

# 12. Temporal Metadata 必须允许“不知道”

不能假设所有知识都有：

```text
valid_from
valid_to
```

支持：

```text
UNKNOWN
```

或 null。

禁止伪造时间。

---

# 13. Version 不等于时间

必须区分：

```text
version
```

与：

```text
date
```

例如：

```text
3GPP Release 17
```

不是一个普通日期。

应支持：

```text
VersionIdentity
```

或等价概念。

---

# 14. Version Identity

至少能表达：

```text
26.2
26.3
v0.4.0
Release 17
Rel-18
TS 23.501 v18.x
```

不要假设 version 都可直接转 float。

---

# 15. Version Ordering

需要轻量：

```text
version comparison
```

但不要设计通用 package manager version engine。

只支持项目真实需要的：

* semantic-ish version
* release number
* document version string
* explicit ordering metadata

如果无法可靠排序：

标记：

```text
UNKNOWN ORDER
```

不要猜。

---

# 16. Temporal Source Metadata

Parser / Document metadata 应尽可能提取：

```text
document version
release
published date
effective date
revision
```

但：

0.5 不重新设计 Parser。

只复用：

* Document metadata
* filename
* heading
* existing metadata
* explicit user metadata

---

# 17. 来源优先级

同一知识可能来自：

```text
Product Description
Release Notes
Old Design Doc
Code
README
```

可以设计轻量：

```text
source authority / priority
```

但禁止复杂评分系统。

目标只是帮助：

```text
current truth selection
```

---

# 18. Temporal Provenance

所有 Temporal 判断必须可以解释：

例如：

```text
This fact is considered current because:
source release = 26.3
supersedes = 26.2
```

不能只输出一个：

```text
current=true
```

却无法解释。

---

# 19. Phase 2 — Supersession Model

实现轻量 Relation：

```text
supersedes
superseded_by
```

例如：

```text
Fact 26.3
supersedes
Fact 26.2
```

---

# 20. 不要过度自动 Supersession

禁止：

> 只要内容不同，就认为新文档 supersedes 旧文档。

必须有较强证据：

* same concept
* same subject
* same property
* newer version
* comparable scope

---

# 21. Supersession Confidence

可以记录：

```text
EXPLICIT
HIGH
INFERRED
UNKNOWN
```

不要设计复杂概率。

---

# 22. Conflict

支持：

```text
conflicts_with
```

用于：

```text
两个当前有效来源互相矛盾
```

这种情况不能自动硬选。

系统应该：

```text
surface conflict
```

---

# 23. Conflict 与 Supersession 不同

必须区分：

```text
Old Fact A
↓ superseded by
New Fact B
```

和：

```text
Current Source A
conflicts_with
Current Source B
```

前者可以优先新知识。

后者必须向用户暴露不一致。

---

# 24. Phase 3 — Temporal Knowledge Compilation

Knowledge Compiler 增加 Temporal-aware compilation。

例如：

```text
Old:
Max Context = 128k

New:
Max Context = 1M
```

不要简单 merge 成：

```text
Max Context = 128k / 1M
```

应该形成：

```text
Fact A:
value = 128k
valid_for = old version

Fact B:
value = 1M
valid_for = new version

B supersedes A
```

---

# 25. 禁止跨版本错误 Dedup

这是重点。

当前 Knowledge Dedup 可能把：

```text
Feature A supports X
```

和：

```text
Feature A supports Y
```

当成同一 Concept 下的可合并 Fact。

0.5 必须检查：

> 它们是否其实是不同版本状态。

---

# 26. Temporal-aware Dedup

Dedup key 不应只有：

```text
subject + predicate
```

还需考虑：

```text
version scope
temporal scope
source generation
```

---

# 27. Summary 不允许抹平版本差异

例如：

```text
26.2 supports A
26.3 supports A+B
```

Summary 不应该简单生成：

```text
System supports A+B.
```

除非明确标记：

```text
Current summary
```

历史 Summary 应保留版本边界。

---

# 28. Summary Types

可考虑：

```text
CURRENT
HISTORICAL
EVOLUTION
```

例如：

```text
Current Summary:
26.3 supports A+B

Evolution Summary:
26.2 added A
26.3 added B
```

---

# 29. Phase 4 — Temporal Query Understanding

扩展现有 Query Router。

识别：

## Current

```text
当前
现在
最新
目前
current
latest
```

---

## Historical

```text
以前
旧版本
之前
当时
```

---

## Explicit Version

```text
26.2
26.3
v0.3.1
Rel-17
```

---

## Evolution

```text
如何演进
有什么变化
什么时候加入
什么时候删除
```

---

## Validity

```text
现在还支持吗？
当前还有效吗？
```

---

# 30. Query Temporal Intent

建议内部输出：

```text
TemporalIntent
```

例如：

```text
CURRENT
AS_OF_VERSION
AS_OF_TIME
HISTORICAL
EVOLUTION
COMPARE_VERSIONS
VALIDITY
NONE
```

---

# 31. Query Parser 必须可降级

如果无法判断 Temporal Intent：

```text
NONE
```

然后继续现有 0.4 路径。

不要过度分类。

---

# 32. Explicit Version 优先

用户如果明确问：

```text
26.2
```

则必须优先：

```text
AS_OF_VERSION 26.2
```

不能因为 26.3 更新而自动替换。

---

# 33. “Current” Resolution

这是 0.5 核心。

必须定义：

```text
current
```

到底怎么判断。

可考虑：

```text
latest valid version
+
not superseded
+
highest authority source
```

但必须有清晰规则。

---

# 34. 不允许 Current = 最新文件日期

因为：

```text
一个旧版本说明文档
```

可能比正式 Release Notes 修改时间更新。

Current resolution 必须基于：

```text
knowledge validity
```

而不是 file mtime。

---

# 35. Phase 5 — Version-Aware Retrieval

Evidence Retrieval 不重写。

只加入 Temporal Filtering / Boosting。

例如：

```text
Query = current behavior

retrieval candidates:
26.2
26.3
```

Version-aware stage：

```text
boost 26.3
downrank 26.2
```

---

# 36. 不要直接删除历史 Evidence

历史知识仍然有价值。

应：

```text
downrank / scope filter
```

不是：

```text
physical delete
```

---

# 37. Historical Query

用户明确问旧版本：

```text
26.2 behavior
```

则：

```text
26.2
```

必须重新成为高优先级。

---

# 38. Evolution Query

例如：

```text
26.2 到 26.3 有什么变化？
```

必须同时激活：

```text
26.2
+
26.3
+
supersession/evolution relations
```

---

# 39. Phase 6 — Version-Aware Context Compiler

Context Compiler 增加 Temporal Section。

例如：

```text
Temporal Intent:
CURRENT

Resolved Version:
26.3

Superseded Evidence:
26.2

Current Facts:
...

Historical Facts:
...

Exact Evidence:
...
```

---

# 40. 默认不要塞全部历史

CURRENT Query 默认 Context：

```text
current evidence
+
必要 supersession explanation
```

不要把十个旧版本全部放进 Context。

---

# 41. Historical Context

只有：

```text
COMPARE
EVOLUTION
HISTORY
```

Query 才加载多版本。

---

# 42. Context Token 目标

0.5 不应该因为 Temporal Support 导致：

```text
Context tokens 大幅增加
```

应尽量做到：

```text
更正确地选
而不是
更多地塞
```

---

# 43. Phase 7 — Temporal Semantic Memory

Semantic Memory retrieval 也必须 temporal-aware。

例如 Concept：

```text
Code Agent
```

下有：

```text
Fact v26.2
Fact v26.3
```

Semantic Memory 默认返回：

```text
current Fact
```

但保留：

```text
history link
```

---

# 44. Knowledge Unit Status

可增加：

```text
ACTIVE
SUPERSEDED
CONFLICTED
HISTORICAL
UNKNOWN
```

根据实际模型精简。

---

# 45. 删除与 Temporal 不同

不要把：

```text
SUPERSEDED
```

当成：

```text
deleted
```

旧知识必须仍然可查。

---

# 46. Phase 8 — Real-World Temporal Benchmark

直接复用现有：

```text
benchmarks/real_world_internalization/
```

不要重新造整套 Harness。

扩展 Temporal Query。

---

# 47. Benchmark Corpus

继续使用：

```text
Open5GS
3GPP TS 23.501 / 23.502
Shutu-Knowledge code/docs
```

如果需要增加：

```text
multi-version release notes
```

允许加入。

---

# 48. Temporal Query 数量

至少：

```text
40
```

建议：

```text
50–80
```

必须覆盖不同类型。

---

# 49. Temporal Benchmark 分类

至少：

```text
CURRENT
EXPLICIT_VERSION
HISTORICAL
EVOLUTION
COMPARE
INTRODUCED_IN
REMOVED_IN
VALIDITY
CONFLICT
```

---

# 50. 典型测试问题

例如：

```text
当前版本支持哪些能力？
```

```text
26.2 支持什么？
```

```text
26.2 到 26.3 增加了什么？
```

```text
这个功能从哪个版本开始支持？
```

```text
这个配置现在还有效吗？
```

```text
Rel-17 和 Rel-18 在这个流程上有什么差异？
```

```text
旧设计文档和当前代码哪个反映当前行为？
```

---

# 51. Benchmark Baseline

必须比较：

## A

```text
0.4
```

当前 Temporal behavior。

## B

```text
0.5
```

Temporal-aware behavior。

---

# 52. 其它 Query 不能退化

同时重新跑：

```text
Fact
Global
Cross-document
Multi-hop
Explanation
Comparison
```

至少 regression subset。

目标：

> 修 Temporal，不能破坏 0.4 已经变好的能力。

---

# 53. 核心指标

至少记录：

```text
Temporal Accuracy
Current-Version Accuracy
Historical-Version Accuracy
Evolution Accuracy
Conflict Detection
Citation Accuracy
Unsupported Claims
Context Tokens
Latency
```

---

# 54. Current-Version Accuracy

重点指标：

```text
用户问 current/latest
系统是否使用正确当前版本
```

这是 0.5 最关键指标之一。

---

# 55. Historical Accuracy

用户明确指定旧版本时：

必须保证：

```text
不会被新版本覆盖
```

---

# 56. Evolution Accuracy

问题：

```text
A → B 发生了什么变化
```

评价：

* additions
* removals
* changed behavior
* source correctness

---

# 57. Conflict Detection

构造或选择真实：

```text
两个同时有效但互相矛盾
```

的来源。

系统必须：

```text
report conflict
```

不能静默选一个。

---

# 58. Citation 继续保持 1.000 目标

0.4 已经达到：

```text
Citation Accuracy = 1.000
```

0.5 不应为了 Temporal inference 牺牲 Citation。

---

# 59. Unsupported Claims

继续目标：

```text
0
```

尤其 Temporal inference 很容易出现：

```text
LLM 猜 introduced_in
```

这种情况必须严格防止。

---

# 60. Temporal Provenance Audit

随机抽样至少：

```text
50 Temporal Knowledge Units
```

检查：

* version correct
* valid scope correct
* supersession correct
* source correct
* provenance valid

---

# 61. Supersession Audit

随机抽样：

```text
30 supersession relations
```

检查：

```text
true supersession
vs
mere difference
```

---

# 62. False Supersession 是严重问题

例如：

```text
Document A
Feature supports mode X

Document B
Feature supports mode Y
```

它们可能是并存，不一定 supersede。

如果系统错误判断 supersession：

这是 P1/P0 correctness issue。

---

# 63. Current-Version Contradiction Probe

保留并加强已有：

```text
current-version contradiction probe
```

至少验证：

```text
old fact
new fact
current query
historical query
```

---

# 64. Incremental Update

新增一个新版本文档：

```text
vN+1
```

系统必须：

* 编译新 Knowledge
* 建立 supersession
* 更新 current
* 保留 old history
* 不全库重编

---

# 65. Delete

删除最新版本文档：

系统必须重新判断：

```text
current version
```

如果旧版本仍存在：

可以恢复为 current candidate。

不得留下：

```text
dangling supersession
```

---

# 66. Rollback

如果当前 generation 回滚：

Temporal Knowledge 也必须跟随 generation。

不能出现：

```text
Evidence rolled back
Knowledge still new
```

---

# 67. Atomic Visibility

Temporal compilation 必须继续遵守：

```text
new generation complete
↓
atomic switch
↓
old generation historical
```

---

# 68. Restart Recovery

重启后：

```text
current version selection
supersession graph
temporal metadata
```

必须保持一致。

---

# 69. Doctor / Diagnostics

可增加轻量：

```text
Temporal Knowledge
```

诊断：

```text
current version
versions discovered
superseded facts
conflicts
unknown temporal metadata
```

不要做大 UI。

---

# 70. UI 范围

UI 只做必要增强。

例如 Evidence / Knowledge 页面可以显示：

```text
Version
Current
Superseded
Conflict
```

以及：

```text
History
```

不做 Timeline 大可视化。

---

# 71. Knowledge Explorer

如果已有：

```text
Concept
Topic
Wiki
```

可显示：

```text
Current Knowledge
Historical Knowledge
```

即可。

---

# 72. Wiki / Summary

如果 Wiki 页面涉及多版本：

默认生成：

```text
Current View
```

并允许：

```text
Version History
```

但 0.5 不做复杂 Wiki editor。

---

# 73. Agent Integration

Shutu-Agent 最终只需要看到更正确的：

```text
Context
Evidence
Citation
Temporal metadata
```

不修改 Agent 核心架构。

---

# 74. 返回结构

必要时可给 Evidence 增加：

```text
version
temporal_status
valid_from
valid_to
```

必须 backward-compatible。

---

# 75. Query Diagnostics

开发模式最好能看到：

```text
Query:
"当前..."

Temporal Intent:
CURRENT

Resolved Knowledge Version:
26.3

Suppressed Historical:
26.2
```

这对调试极其重要。

---

# 76. Phase 顺序

严格建议：

## PH0

Temporal Regression Audit

只分析，不写业务代码。

---

## PH1

Temporal Metadata Model

---

## PH2

Supersession / Conflict

---

## PH3

Temporal-aware Knowledge Compilation

---

## PH4

Temporal Query Understanding

---

## PH5

Version-aware Retrieval

---

## PH6

Version-aware Context Compiler

---

## PH7

Incremental Temporal Lifecycle

---

## PH8

Benchmark / Regression / Audit

---

## PH9

Stabilization / Release

---

# 77. 每个 Phase 规则

每个 Phase：

1. 明确问题。
2. 最小设计。
3. 实现。
4. Test。
5. Race。
6. Benchmark subset。
7. 文档。
8. Commit。

禁止一次巨大提交。

---

# 78. 不重新审计全仓

只读取当前 Phase 涉及代码。

不要重复：

```text
0.4 architecture audit
all parser audit
all storage audit
all Agent audit
```

已有结论复用。

---

# 79. Token 控制

0.5 是单一 Gap 修复版本。

禁止 Codex 为此消耗大量 token 在：

```text
无关架构研究
历史 commit 全量分析
全项目重新设计
```

优先：

```text
Targeted diagnosis
Targeted change
Measured validation
```

---

# 80. Performance

Temporal logic 不应让普通 Fact Query 明显变慢。

记录：

```text
0.4 latency
vs
0.5 latency
```

分：

```text
non-temporal query
temporal query
```

---

# 81. Context Efficiency

目标：

```text
Temporal Accuracy ↑
```

同时：

```text
Context Tokens ≈ same
```

最好下降。

禁止：

> 为解决版本选择，每次塞所有历史版本。

---

# 82. Storage

继续 SQLite。

只增加必要：

```text
temporal metadata
relations
indexes
```

不要引入 Graph DB。

---

# 83. Migration

0.4 → 0.5：

必须：

```text
PASS
```

旧 KB 没有 Temporal Metadata：

继续正常使用。

可以：

```text
UNKNOWN
```

而不是强制重建。

---

# 84. Optional Recompile

对于已有 0.4 KB：

可以选择运行：

```text
Temporal Recompile
```

增强版本知识。

不允许强制重建 Evidence Index。

---

# 85. Degradation

如果 Temporal Compilation 失败：

系统必须退化为：

```text
0.4 Semantic Memory + Structured RAG
```

Knowledge Base 不能整体不可用。

---

# 86. Release Gate

0.5 Release Gate：

```text
Build                         PASS
Go Test                       PASS
Race                          PASS
Web                           PASS

0.4 Migration                 PASS
0.4 Regression                PASS

Temporal Metadata             PASS
Version Identity              PASS
Supersession                  PASS
Conflict Model                PASS

Temporal Compilation          PASS
Temporal Query Routing        PASS
Version-aware Retrieval       PASS
Version-aware Context         PASS

Current Query                 PASS
Historical Query              PASS
Explicit Version              PASS
Evolution Query               PASS
Comparison Query              PASS
Conflict Query                PASS

Temporal Provenance           PASS
Citation Accuracy             PASS
Unsupported Claims            PASS

Incremental Update            PASS
Delete                        PASS
Rollback                      PASS
Restart                       PASS

Context Efficiency            PASS
Performance Regression        PASS

Agent Integration             PASS
Windows Package Smoke         PASS

No P0                         PASS
```

---

# 87. Release Success 必须基于真实数据

不能因为：

```text
all tests pass
```

就宣布成功。

必须在真实 benchmark 上证明：

```text
Temporal / Version Selection
```

明显优于 0.4。

---

# 88. 最低成功标准

建议至少：

```text
Temporal support proxy:
明显高于 0.4
```

并且：

```text
Current-version selection:
无明显错误
```

同时：

```text
Citation accuracy:
不得下降
```

---

# 89. 其它 0.4 能力

以下不得明显退化：

* Global
* Cross-document
* Multi-hop
* Comparison
* Explanation

---

# 90. 如果 Temporal 提升但 Global 下降

不能直接发布。

必须分析是不是：

```text
Temporal filtering overly aggressive
```

导致历史或补充 Evidence 被错误过滤。

---

# 91. 如果 Conflict 处理太保守

允许系统回答：

```text
Sources disagree.
```

这比强行判断更好。

---

# 92. 如果 Version 无法排序

允许：

```text
Unable to determine authoritative latest version.
```

不要猜。

---

# 93. 0.6 Candidate

本轮只记录，不开发。

例如：

```text
Memory Consolidation
Advanced Cross-document Reasoning
Domain Ontology
GraphRAG
Code Intelligence
```

只有真实 benchmark 支持才进入。

---

# 94. 最终报告

创建：

```text
docs/release_report_0.5.0.md
docs/0.5_temporal_validation.md
docs/0.5_temporal_error_taxonomy.md
docs/0.6_candidate_gaps.md
```

---

# 95. 最终 Benchmark Report

至少输出：

| Category         | 0.4 | 0.5 | Delta |
| ---------------- | --: | --: | ----: |
| Current          |     |     |       |
| Explicit Version |     |     |       |
| Historical       |     |     |       |
| Evolution        |     |     |       |
| Comparison       |     |     |       |
| Conflict         |     |     |       |
| Global           |     |     |       |
| Multi-hop        |     |     |       |

---

# 96. Context

输出：

```text
0.4 average context tokens
0.5 average context tokens
delta
```

---

# 97. Accuracy

输出：

```text
Temporal Accuracy
Citation Accuracy
Unsupported Claims
Invalid Provenance
False Supersession
```

---

# 98. Lifecycle

输出：

```text
Update
Delete
Rollback
Restart
```

PASS / FAIL。

---

# 99. Final Release Artifact

输出：

```text
filename
size
SHA-256
```

---

# 100. CI

输出：

```text
push CI
tag CI
```

PASS / FAIL。

---

# 101. Release Status

只能：

```text
READY
```

或：

```text
NOT READY
```

---

# 102. 最终成功定义

0.5 的价值不是：

> Shutu-Knowledge 多了 Temporal 字段。

而是：

> 当知识库同时包含过去、现在、不同版本和相互冲突的信息时，LLM 能够知道“什么时候应该相信哪一份知识”。

最终实现：

```text
Knowledge
   │
   ├── Current
   ├── Historical
   ├── Superseded
   ├── Conflicted
   └── Unknown
```

Query：

```text
"What is true now?"
```

系统激活：

```text
Current Knowledge
+
Exact Evidence
```

Query：

```text
"What was true in version 26.2?"
```

系统激活：

```text
Historical Knowledge 26.2
+
Exact Evidence
```

Query：

```text
"What changed from 26.2 to 26.3?"
```

系统激活：

```text
26.2
+
26.3
+
Supersession
+
Evolution Evidence
```

---

# 103. 最终执行原则

始终遵守：

```text
Fix the observed gap.

Do not start a new architecture era.

Version awareness before graph complexity.

Prefer explicit uncertainty over guessed chronology.

Preserve history; do not overwrite it.

Current knowledge must be explainable.

Temporal inference must remain grounded in evidence.

Improve selection, not context size.
```

0.5 完成以后，Shutu-Knowledge 应该从：

```text
I understand these documents.
```

进一步变成：

```text
I understand how this knowledge changed over time,
which version applies now,
and what was true before.
```

这才是 0.5 的完成标准。
