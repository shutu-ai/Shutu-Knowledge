# Shutu-Knowledge 0.4 — Real-World Knowledge Internalization Validation

## 0. 当前正式基线

当前正式稳定版本：

```text
Shutu-Knowledge v0.4.0
```

正式发布证据：

```text
Source tag target:
6da3bcdd05741834a3bfea77db049905e9d838ff

Tag:
v0.4.0

Documentation closure:
4f0111bc1eac7ed6fe4f4e3dfa7c514f9d997da9

Release:
Shutu Knowledge v0.4.0
```

Artifact：

```text
shutu-knowledge-0.4.0-windows-amd64.zip

Size:
12,580,680 bytes

SHA-256:
d70a2ea2476b3f77f6e0d7de232bb48777a07038c14e37ab99069b23489ae49c
```

Release validation：

```text
Push CI: PASS
Tag CI: PASS
Package Smoke: PASS
No P0: PASS
```

已有 benchmark：

```text
0.2/0.3 support proxy:
score = 4.500
context = 454 tokens

0.4:
score = 5.667
context = 436 tokens
```

0.4 已证明：

> 更高知识支持质量，同时使用更少 Context Token。

当前状态：

```text
0.4.x MAINTENANCE MODE
```

---

# 1. 本任务不是 0.5 开发

非常重要。

本任务禁止直接进入：

* GraphRAG
* Knowledge Graph 扩展
* 新 Agent Planner
* 新 Storage Architecture
* 新 Vector Database
* Memory Consolidation 大开发
* Temporal Reasoning 大开发
* Ontology
* Logic Engine
* 新 Wiki Framework
* 新 Parser Framework
* 大规模 UI 重构

当前唯一目标：

> 使用真实知识库验证 Shutu-Knowledge 0.4 是否真正达到了“Non-Parametric Knowledge Internalization”的产品目标，并用数据决定 0.5 应该解决什么。

这是一轮：

# Real-World Validation

而不是：

# Feature Development

---

# 2. 核心问题

本轮必须回答：

> 当知识库从测试 Corpus 换成真实复杂资料以后，0.4 是否仍然比 0.3 更像“LLM 已经学习过这些资料”？

不能只回答：

```text
RAG 能不能检索到？
```

需要评估：

```text
模型是否理解整个知识域？
是否能建立跨文档关联？
是否能处理版本变化？
是否能回答全局问题？
是否能完成多跳问题？
是否减少临时 Top-K Chunk 拼接？
是否降低 Context Token？
是否仍然保持 Evidence/Citation 正确？
```

---

# 3. 本阶段产品目标

目标不是追求 benchmark 数字漂亮。

目标是识别：

```text
0.4 真正有效的机制
```

以及：

```text
0.4 真实使用中的薄弱环节
```

最终决定：

```text
0.5 应该做什么
```

和：

```text
0.5 不应该做什么
```

---

# 4. 必须保持 Feature Freeze

除非发现：

```text
P0 / P1 correctness defect
```

否则禁止修改 0.4 核心实现。

允许修复：

* 数据错误
* Citation 错误
* Provenance 错误
* Delete propagation 错误
* Generation contamination
* Migration defect
* Crash
* Deadlock
* Data corruption
* 明显 Knowledge Compiler correctness bug

不允许因为某个真实 Query 答得不好就立即：

```text
增加新架构
增加 Graph
增加新的 Retriever
增加更多 Prompt pipeline
```

首先必须证明问题类型。

---

# 5. 建立真实验证 Corpus

本轮至少使用三类真实知识库。

## Corpus A — Telecom / SmartCare / Product Knowledge

优先使用真实：

```text
SmartCare
GDE
Data Cube
Code Agent
Network Analytics
Product Description
Architecture
Deployment Guide
Feature Guide
Release Notes
```

等资料。

最好包含：

* 多个版本
* PDF
* PPT
* Excel
* Word
* 技术说明
* Release Notes
* 架构文档

目标验证：

```text
专业术语
跨文档关联
产品版本
Feature 演进
架构关系
```

---

## Corpus B — 3GPP / Telecom Technical Knowledge

例如：

```text
3GPP specifications
signalling
PS Core
IMS
5G
RRC
roaming
handover
```

无需一次导入全部 3GPP。

选取一个有代表性的子领域。

例如：

```text
Mobility / Registration / Handover
```

或：

```text
IMS / VoWiFi
```

目标验证：

```text
专业规范
缩写
跨规范引用
多跳推理
概念关联
```

---

## Corpus C — Shutu Code + Engineering Docs

导入：

```text
Shutu-Agent code
Shutu-Knowledge code
architecture docs
release reports
design docs
README
tests
```

目标验证：

```text
代码与文档联合理解
模块关系
设计原因
版本演进
```

---

# 6. 可增加第四类 Corpus

如果已有合适个人资料，可增加：

```text
Mixed Personal Knowledge
```

例如：

* 技术笔记
* Workshop
* SQL
* PPT
* Markdown
* Project reports

但不是必须。

---

# 7. 数据必须真实

尽量不要继续使用人工构造 Demo 数据作为主要结论来源。

允许：

```text
synthetic benchmark
```

用于 regression。

但核心结论必须来自：

```text
real documents
```

---

# 8. 隐私与仓库原则

真实私有文档：

禁止：

* commit 到 GitHub
* 加入 public testdata
* 上传到远程 CI
* 写入公开 Release artifact

建立：

```text
local validation corpus
```

Corpus 内容只存在本地。

仓库中只提交：

```text
benchmark schema
query definitions if non-sensitive
scripts
aggregated metrics
redacted reports
```

---

# 9. 建立固定 Validation Harness

创建：

```text
benchmarks/real_world_internalization/
```

或符合当前项目结构的目录。

需要支持：

```text
Corpus
Query
Expected Evidence
Expected Knowledge
Expected Answer Criteria
Evaluation Result
```

---

# 10. 每个 Query 必须分类

至少：

```text
FACT
LOCAL
GLOBAL
CROSS_DOCUMENT
MULTI_HOP
TEMPORAL
COMPARISON
EXPLANATION
```

---

# 11. FACT Query

测试：

> 精确事实能否快速准确找到？

例如：

```text
某版本支持哪些 Code Agent 能力？
```

评价：

* factual correctness
* exact evidence
* citation
* latency

FACT 不要求 Semantic Memory 一定参与。

---

# 12. LOCAL Query

例如：

```text
某一个章节的核心要求是什么？
```

测试：

```text
Document IR
Structure-aware evidence
Section summary
```

---

# 13. GLOBAL Query

这是重点。

例如：

```text
整套产品文档中，Code Agent 的整体架构是什么？
```

或：

```text
这些资料主要覆盖了哪些能力域？
```

必须比较：

```text
0.3 Structured RAG
vs
0.4 Compiled Knowledge
```

重点看：

* completeness
* organization
* coverage
* token cost

---

# 14. CROSS_DOCUMENT Query

例如：

```text
产品描述、部署手册和 Release Notes 对某功能分别如何描述？
```

目标验证：

```text
cross-document synthesis
```

而不是只返回某一篇文档。

---

# 15. MULTI_HOP Query

例如：

```text
Code Agent 如何通过 Resolver 最终访问 Data Cube 数据？
```

答案可能需要：

```text
Document A → concept
Document B → relation
Document C → implementation
```

验证：

```text
Concept
Relation
Evidence
```

是否真正带来帮助。

---

# 16. TEMPORAL Query

必须成为本轮重点。

使用真实多版本文档。

例如：

```text
26.2 与 26.3 Code Agent 能力有哪些变化？
```

或者：

```text
这个功能什么时候加入？
```

检查：

* 旧知识是否污染当前答案
* 当前版本识别
* superseded information
* release chronology

如果 0.4 当前 Temporal Knowledge 很弱：

只记录 gap。

不要立即开发。

---

# 17. COMPARISON Query

例如：

```text
方案 A 与方案 B 在架构、能力和限制上有什么差异？
```

评价：

```text
cross-source alignment
completeness
contradiction handling
```

---

# 18. EXPLANATION Query

这是“接近训练进模型”的重要测试。

例如：

```text
为什么这个系统采用 generation isolation？
```

普通 RAG 很可能找到：

```text
generation isolation exists
```

但不一定能够解释：

```text
why
```

评价：

* 能否结合多个证据形成正确解释
* 是否出现 unsupported reasoning

---

# 19. Query 数量

每个 Corpus 至少：

```text
30 queries
```

建议：

```text
40–60
```

三个 Corpus 总数至少：

```text
100 queries
```

但不要为了数量加入大量重复问题。

---

# 20. 建立 Baseline

必须比较：

## Baseline A

```text
0.3-style Structured RAG
```

即：

```text
Evidence Retrieval only
```

尽量禁用 0.4 Knowledge Compilation 增强。

---

## Baseline B

```text
0.4 AUTO
```

使用：

```text
Semantic Memory
Query Routing
Context Compiler
Evidence
```

---

# 21. 可增加 Ablation

必要时增加：

```text
Evidence Only

Evidence + Summary

Evidence + Semantic Memory

Full 0.4
```

但不要无限组合。

只在定位问题时使用。

---

# 22. 模型保持一致

Baseline 对比必须使用相同：

* LLM
* temperature
* context limit
* corpus
* question
* max answer length

否则结果没有可比性。

---

# 23. Retrieval 参数尽量固定

除非正在测试某项机制，否则：

```text
Embedding
Reranker
TopK
```

等参数保持一致。

---

# 24. 核心评价指标

每个 Query 至少记录：

```text
answer correctness
completeness
evidence correctness
citation correctness
context tokens
latency
```

---

# 25. 增加 Internalization 指标

设计：

# Knowledge Internalization Score

不要假装这是学术标准。

它只是项目内部指标。

建议由以下维度组成：

```text
Fact Accuracy
Global Understanding
Cross-document Integration
Multi-hop Success
Temporal Accuracy
Explanation Quality
Evidence Grounding
```

每项：

```text
0–5
```

最终可以有一个总分。

但报告必须同时显示各子项。

禁止只展示一个总分掩盖问题。

---

# 26. Grounding 必须独立评分

答案看起来很好并不代表正确。

需要单独检查：

```text
answer claims
↓
supported by evidence?
```

记录：

```text
SUPPORTED
PARTIAL
UNSUPPORTED
```

---

# 27. Citation Accuracy

至少检查：

```text
correct document
correct section
correct page/slide/sheet
correct evidence
```

0.4 不能因为 Semantic Memory 更强，反而 Citation 更差。

---

# 28. Context Efficiency

核心指标：

```text
quality / context tokens
```

记录：

```text
average context tokens
p50
p95
```

同时记录：

```text
answer score
```

目标：

> 0.4 不应该依赖持续增加 context 才取得提升。

---

# 29. Latency

记录：

```text
query routing
semantic retrieval
evidence retrieval
context compile
total latency
```

不要求极致。

但如果 0.4 比 0.3：

```text
10x slower
```

必须分析。

---

# 30. Compilation Cost

另外记录一次性：

```text
Knowledge Compilation
```

成本：

* time
* tokens
* disk
* memory

因为 0.4 本质是：

> 预编译知识换取查询阶段理解能力。

需要知道成本是否合理。

---

# 31. Incremental Compilation 测试

真实修改一个文档。

例如：

```text
Product v26.2 → v26.3
```

记录：

```text
affected documents
affected knowledge units
recompile duration
LLM tokens
unchanged knowledge reused
```

目标：

> 不应全库重编。

---

# 32. Delete Test

删除一个真实文档。

然后检查：

* Evidence 删除
* Semantic Memory 更新
* Wiki/Summary 更新
* Relations 更新
* old fact 不再回答
* 其它仍有证据支持的知识继续存在

---

# 33. Contradiction Test

人为选择真实存在冲突或版本变化的资料。

例如：

```text
older document says X
newer document says Y
```

查询：

```text
当前正确值是什么？
```

系统应至少：

* 暴露冲突
* 识别版本/时间
* 提供来源

如果无法可靠决定当前值：

宁可回答：

```text
sources differ
```

不要让 LLM 自己猜。

---

# 34. Knowledge Quality Audit

随机抽样：

```text
Facts
Concepts
Topics
Summaries
Relations
```

至少各：

```text
50
```

检查：

* duplicates
* meaningless concepts
* hallucinated relations
* over-generalization
* unsupported facts
* wrong provenance

---

# 35. Semantic Memory 污染

重点查：

```text
LLM-generated incorrect knowledge
```

是否会污染后续查询。

这是 0.4 比普通 RAG 更大的风险。

必须评估：

```text
错误 Knowledge Unit
↓
是否被多个 Query 反复复用？
```

---

# 36. 如果发现错误派生知识

不要立即重构。

首先确认根因属于：

```text
Extraction
Normalization
Dedup
Resolution
Clustering
Summary
Relation
Routing
Context Compilation
```

记录分类。

---

# 37. Error Taxonomy

创建：

```text
docs/0.4_real_world_error_taxonomy.md
```

建议类型：

```text
E1 Retrieval Miss
E2 Knowledge Extraction Error
E3 Wrong Concept Merge
E4 Relation Hallucination
E5 Summary Distortion
E6 Version Confusion
E7 Query Routing Error
E8 Context Selection Error
E9 Citation Error
E10 LLM Answer Error
```

---

# 38. 不要把所有错误归因于 RAG

需要区分：

```text
Knowledge system error
```

和：

```text
final LLM reasoning error
```

如果 Evidence 和 Context 都正确，但 LLM 回答错误：

不要为了它重构 Knowledge Compiler。

---

# 39. Expert Review

Telecom Corpus 建议人工专家复核。

每个重要 Query：

记录：

```text
correct
partially correct
incorrect
```

必要时写：

```text
review note
```

不要完全依赖 LLM-as-Judge。

---

# 40. LLM-as-Judge

可以使用。

但必须：

* 固定 Judge model
* 固定 rubric
* 保存 raw score
* 抽样人工复核

不能只靠 Judge 自动决定整个版本质量。

---

# 41. 真实使用 Scenario

除了固定 benchmark，还需要模拟实际交互。

例如：

```text
用户连续问 10 个关于 Code Agent 的问题
```

看看系统是否：

* 重复检索同样内容
* 利用已有 Semantic Memory
* 保持术语一致
* 出现前后矛盾

---

# 42. Knowledge Navigation

如果 0.4 有 Wiki/Topic/Concept 页面：

人工检查：

> 用户能否通过它快速理解一个陌生知识域？

这是另一种“知识内化”价值。

不只测试 QA。

---

# 43. Code Corpus 测试

至少测试：

```text
模块职责
跨文件调用关系
设计决策
API 与实现
代码与文档一致性
版本变更
```

如果效果差：

不要立即开发完整 Code Graph。

先记录是否是：

```text
0.5 candidate
```

---

# 44. 真实问答日志

如果已有日常实际使用问题：

加入 benchmark。

优先级高于人工构造问题。

---

# 45. 创建最终验证报告

创建：

```text
docs/0.4_real_world_validation.md
```

必须包括：

* Corpus
* Query mix
* Model
* Configuration
* Baseline
* Results
* Error taxonomy
* Key failures
* Context efficiency
* Compilation cost
* Recommended next step

---

# 46. 0.3 vs 0.4 对比

必须至少输出：

| Category       | 0.3 | 0.4 | Delta |
| -------------- | --: | --: | ----: |
| Fact           |     |     |       |
| Global         |     |     |       |
| Cross-document |     |     |       |
| Multi-hop      |     |     |       |
| Temporal       |     |     |       |
| Comparison     |     |     |       |
| Explanation    |     |     |       |

另外：

```text
Average Context Tokens
Latency
Citation Accuracy
Unsupported Claims
```

---

# 47. 不允许只报平均值

必须同时看各类别。

可能出现：

```text
Fact: same
Global: much better
Multi-hop: better
Temporal: poor
```

这类结果正是制定 0.5 的依据。

---

# 48. 0.5 Gap 必须由数据产生

本轮最终必须输出：

```text
docs/0.5_candidate_gaps.md
```

只能包含真实 benchmark 支持的问题。

禁止：

> 因为 GraphRAG 很流行，所以 0.5 做 GraphRAG。

---

# 49. 0.5 Candidate 优先级原则

每个 candidate 用以下维度评价：

```text
Observed Failure Frequency
User Impact
Potential Quality Gain
Complexity
Runtime Cost
Storage Cost
Token Cost
Compatibility Risk
```

---

# 50. 可能的 0.5 方向

只能作为候选，不预设必须实施。

例如：

## Temporal Knowledge

只有当：

```text
version confusion
```

是实际高频问题时进入。

---

## Cross-document Reasoning

只有当：

```text
multi-hop/cross-doc
```

仍明显不足时进入。

---

## Memory Consolidation

只有当：

```text
Semantic Memory duplicates/noise
```

随着知识库增长明显恶化时进入。

---

## GraphRAG

只有当：

```text
relation-intensive queries
```

benchmark 明确显示当前轻量 Relation 不够时进入。

---

## Domain Ontology

只有当 Telecom 专业概念：

```text
synonym
abbreviation
hierarchy
```

大量混乱时进入。

---

## Code Intelligence

只有真实 Code Corpus 显示普通 Document IR 不够时考虑。

---

# 51. 一个特别重要的判断

如果 0.4 在真实数据上已经表现很好：

不要因为“该进入 0.5”而强行开发 0.5。

可以让：

```text
v0.4.x
```

长期成为稳定主版本。

---

# 52. 只有高价值 gap 才启动 0.5

建议启动条件：

至少存在一个问题：

```text
high frequency
+
high user impact
+
current architecture cannot solve cheaply
```

否则：

```text
DO NOT START 0.5
```

---

# 53. 本轮允许的小修复

如果发现明确 P0/P1：

允许创建：

```text
v0.4.1
```

但必须保持：

```text
maintenance scope
```

例如：

* wrong delete propagation
* wrong provenance
* cross-generation leak
* broken migration
* severe routing bug
* corrupted semantic memory

---

# 54. 本轮禁止的“优化性修复”

例如：

```text
某类 global query 从 4.2 提升到 4.4
```

但需要引入整个 GraphRAG。

不做。

进入 0.5 candidate。

---

# 55. Real-World Acceptance Gate

本轮不设置为产品 Release Gate。

这是：

```text
Product Validation Gate
```

建议判定：

## STRONG

0.4 在真实 Corpus 上：

* Global 明显提升
* Cross-doc 明显提升
* Multi-hop 提升
* Citation 不退化
* Token 不增加或下降
* 无严重 Semantic Memory 污染

---

## ACCEPTABLE

部分复杂 Query 改善明显，存在少量可解释 gap。

---

## WEAK

0.4 相比 0.3 提升不稳定，或大量依赖更多 context。

---

## FAIL

出现：

* 知识污染
* Citation 严重下降
* 大量错误关系
* 版本知识混乱
* 实际质量低于 0.3

---

# 56. 最终必须给出产品判断

只能选择：

```text
0.4 REAL-WORLD VALIDATION: STRONG
```

或：

```text
ACCEPTABLE
```

或：

```text
WEAK
```

或：

```text
FAIL
```

---

# 57. 如果结果 STRONG

建议：

```text
0.4 remains stable
No immediate 0.5 development
```

继续真实使用。

---

# 58. 如果结果 ACCEPTABLE

只挑：

```text
Top 1–2 gaps
```

进入 0.5。

禁止开 5 个大方向。

---

# 59. 如果结果 WEAK

不要增加更多功能。

先定位：

```text
Knowledge Compiler
Semantic Memory
Context Compiler
```

哪一层价值不足。

---

# 60. 如果结果 FAIL

停止扩展 0.5。

优先修正：

```text
0.4 knowledge correctness
```

---

# 61. 最终交付物

必须生成：

```text
docs/0.4_real_world_validation.md
docs/0.4_real_world_error_taxonomy.md
docs/0.5_candidate_gaps.md
```

如有 benchmark 数据：

```text
benchmarks/real_world_internalization/results/
```

---

# 62. 最终输出格式

最终报告只输出：

## Validation Status

```text
STRONG / ACCEPTABLE / WEAK / FAIL
```

---

## Corpus

列出：

```text
Telecom/Product
3GPP
Code/Engineering
```

真实规模：

* documents
* pages/slides/sheets
* chunks
* knowledge units

---

## Benchmark

输出：

```text
0.3 vs 0.4
```

各类别结果。

---

## Context Efficiency

输出：

```text
0.3 average tokens
0.4 average tokens
delta
```

---

## Citation

```text
accuracy
unsupported claims
```

---

## Knowledge Quality

抽样：

```text
Fact
Concept
Topic
Summary
Relation
```

质量结果。

---

## Incremental

输出：

```text
update
delete
recompile
```

结果。

---

## Key Failures

只列影响真实使用的主要问题。

---

## 0.5 Candidates

最多：

```text
3
```

按优先级排序。

但不要开发。

---

## Recommendation

只能选择：

```text
KEEP 0.4 AND CONTINUE REAL USE
```

或：

```text
START 0.5 WITH SPECIFIC GAP
```

或：

```text
FIX 0.4 FIRST
```

---

# 63. 最终原则

这一步的成功不是：

> 又增加了一批代码。

而是：

> 我们终于知道 Shutu-Knowledge 0.4 在真实知识环境里究竟有多大价值，以及下一步最值得解决什么。

始终遵守：

```text
Use real knowledge.

Measure before redesign.

Do not confuse more features with more understanding.

Do not start GraphRAG because GraphRAG is fashionable.

Do not start 0.5 without evidence.

Prefer stable real-world usefulness over architectural ambition.
```

最终要回答的核心问题只有一个：

# “Shutu-Knowledge 0.4 是否真的让 LLM 像已经学过这些资料一样工作？”
