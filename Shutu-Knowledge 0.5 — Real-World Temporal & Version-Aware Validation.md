# Shutu-Knowledge 0.5 — Real-World Temporal & Version-Aware Validation

## 0. 当前正式基线

当前正式稳定版本：

```text
Shutu-Knowledge v0.5.0
```

正式发布状态：

```text
RELEASED
```

Release source：

```text
9bdf28993dd15effceaf04b3eed73d46df93e695
```

Immutable annotated tag：

```text
v0.5.0
```

Tag CI：

```text
35453835673
PASS
```

正式 Windows Artifact：

```text
shutu-knowledge-0.5.0-windows-amd64.zip
```

Size：

```text
12,621,004 bytes
```

SHA-256：

```text
813e8c88aa3bff125d07adfa0a6c9c69fddc86491c84dc418c2a27d9c563b90c
```

Release closure：

```text
72733cd573b3797054999e68d683315d9d0794b8
```

当前：

```text
0.5.x MAINTENANCE MODE
```

0.5 已实现：

* Temporal Metadata
* Version Identity
* Version Ordering
* Supersession
* Conflict Modeling
* Temporal-aware Knowledge Compilation
* Temporal Query Understanding
* Version-aware Retrieval
* Version-aware Context Compilation
* Temporal Semantic Memory
* Incremental Temporal Lifecycle
* Temporal Provenance
* Current / Historical / Evolution / Conflict Query Support

---

# 1. 本任务不是 0.6 开发

非常重要。

本任务唯一目标：

> 使用真实多版本、多时间点、存在演进和冲突的知识资料，验证 Shutu-Knowledge 0.5 是否真正能够判断“什么知识在什么时候有效”，并判断是否存在足够高价值的新 Gap 值得启动 0.6。

这是一轮：

# Real-World Temporal Validation

不是：

# New Feature Development

---

# 2. Feature Freeze

除非发现真实：

```text
P0 / P1 correctness defect
```

否则禁止修改 0.5 核心功能。

允许修复：

* Current version 选错
* Explicit historical version 选错
* Superseded knowledge 被错误作为 current
* Historical knowledge 被错误删除
* False supersession
* Conflict 被错误吞掉
* Temporal provenance 错误
* Citation 错误
* Generation contamination
* Incremental update/delete temporal state 错误
* Restart 后 temporal state 错误
* Migration/data corruption
* Crash/deadlock

禁止因为：

```text
某类问题效果还可以更好
```

就直接：

* 引入 GraphRAG
* 引入 Ontology
* 增加复杂 reasoning engine
* 重构 Knowledge Compiler
* 引入 Graph DB
* 新建 Agent Planner
* 新增大量 Temporal Relation 类型

先验证，再决定。

---

# 3. 本轮必须回答的核心问题

最终必须回答：

## Q1

用户问：

```text
“当前版本是什么行为？”
```

系统是否稳定使用真正当前有效知识？

---

## Q2

用户明确问：

```text
“旧版本 26.2 是什么行为？”
```

系统是否能够保留历史视角，而不会被当前版本覆盖？

---

## Q3

用户问：

```text
“从 26.2 到 26.3 发生了什么变化？”
```

系统是否能够正确描述：

* Added
* Removed
* Changed
* Deprecated
* Unchanged

---

## Q4

用户问：

```text
“这个能力从什么时候开始支持？”
```

系统是否能给出有证据支持的 introduced version/time？

---

## Q5

用户问：

```text
“这个配置现在还有效吗？”
```

系统是否能正确识别：

* active
* superseded
* historical
* conflicted
* unknown

---

## Q6

两个当前来源冲突时：

系统是否明确暴露：

```text
Sources disagree
```

而不是自己猜一个？

---

## Q7

0.5 的 Temporal 机制是否破坏了 0.4 已有：

* Global
* Cross-document
* Multi-hop
* Comparison
* Explanation

能力？

---

# 4. 真实 Corpus 要求

本轮必须使用真实多版本知识资料。

至少三类 Corpus。

---

# 5. Corpus A — Product / Open5GS / Telecom Product Evolution

继续使用已有：

```text
Open5GS product/documentation corpus
```

但必须增加真正的版本差异。

优先包含：

* 不同 Release
* Changelog
* Versioned configuration
* Feature changes
* Deprecated behavior
* API/config evolution

目标：

> 检验 current / historical / evolution selection。

---

# 6. Corpus B — 3GPP Versioned Specifications

继续使用：

```text
3GPP TS 23.501
3GPP TS 23.502
```

但必须确保 Corpus 中存在：

```text
Release / version differences
```

例如：

```text
Rel-17
Rel-18
```

或多个 revision。

重点测试：

* Spec version
* Procedure change
* Terminology evolution
* New feature introduction
* Changed behavior
* Cross-spec evolution

---

# 7. Corpus C — Shutu-Knowledge / Shutu-Agent Version History

使用真实工程历史资料：

```text
v0.2
v0.3
v0.4
v0.5
```

包括：

* Release Notes
* README
* Architecture docs
* Release reports
* Source code where useful
* Git/version metadata where already available

重点问题：

```text
什么时候加入 Document IR？
```

```text
0.4 相比 0.3 增加了什么？
```

```text
当前 Knowledge Compiler 是哪个版本引入的？
```

```text
旧版本的 release gate 和当前有什么变化？
```

---

# 8. Optional Corpus D — Real Product Version Documentation

如果本地已有真实 SmartCare / GDE 等多版本资料，可以加入。

但：

* 不上传敏感文件
* 不提交到 GitHub
* 不进入 CI artifact

只提交：

```text
aggregated metrics
redacted query definitions
non-sensitive results
```

---

# 9. 数据隐私

真实私有 Corpus：

禁止：

* git add
* commit
* push
* 上传 CI
* 上传 Release

Benchmark harness 必须支持：

```text
local private corpus
```

---

# 10. 建立固定 Temporal Validation Harness

优先复用已有：

```text
benchmarks/real_world_internalization/
```

不要新造第二套系统。

扩展：

```text
temporal_validation
```

或符合当前结构的位置。

---

# 11. Query 数量

本轮至少：

```text
120 Temporal/Version Queries
```

建议：

```text
150–200
```

不能全部是 current/latest 类问题。

必须覆盖不同 Temporal 类型。

---

# 12. Query 分类

至少包含：

```text
CURRENT
EXPLICIT_VERSION
HISTORICAL
EVOLUTION
INTRODUCED_IN
REMOVED_IN
VALIDITY
COMPARE_VERSIONS
CONFLICT
UNKNOWN_VERSION
AMBIGUOUS_TEMPORAL
```

---

# 13. CURRENT Query

例：

```text
当前版本支持哪些功能？
```

```text
现在推荐的配置是什么？
```

评价：

* 当前版本是否选对
* 是否抑制 superseded knowledge
* 是否保留必要 provenance

---

# 14. EXPLICIT_VERSION

例：

```text
26.2 支持哪些能力？
```

```text
Rel-17 中这个流程是什么？
```

必须验证：

> Explicit version 优先于 current。

---

# 15. HISTORICAL

例：

```text
旧版本以前是怎么处理的？
```

```text
这个机制最初是怎样设计的？
```

评价：

> 历史知识是否仍然可访问。

---

# 16. EVOLUTION

例：

```text
26.2 到 26.3 有哪些变化？
```

```text
v0.3 到 v0.5 的知识系统演进是什么？
```

必须区分：

* introduced
* removed
* modified
* retained

---

# 17. INTRODUCED_IN

例：

```text
Document IR 是从哪个版本开始支持？
```

必须要求：

```text
有直接 Evidence
```

禁止 LLM 猜版本。

---

# 18. REMOVED_IN / DEPRECATED

例：

```text
这个旧配置什么时候被废弃？
```

如果证据不足：

必须回答：

```text
Unable to determine from available evidence.
```

而不是推测。

---

# 19. VALIDITY

例：

```text
这个配置现在还有效吗？
```

评价：

```text
ACTIVE
SUPERSEDED
CONFLICTED
UNKNOWN
```

是否正确。

---

# 20. COMPARE_VERSIONS

例：

```text
Rel-17 和 Rel-18 在该流程上有什么差异？
```

```text
0.4 和 0.5 在知识检索策略上有什么差别？
```

必须同时激活多个目标版本，而不是默认 current-only。

---

# 21. CONFLICT

选择真实或受控冲突：

```text
Source A says X
Source B says Y
```

要求：

```text
系统明确指出冲突
```

并展示双方 provenance。

---

# 22. UNKNOWN_VERSION

例如：

```text
版本 99.9 支持什么？
```

必须：

```text
not found / unknown
```

不能 fallback 到 latest 后假装是 99.9。

---

# 23. AMBIGUOUS_TEMPORAL

例如：

```text
以前是什么样？
```

如果资料存在多个历史版本：

系统应：

* 给合理范围
* 或说明存在多个阶段

不能无依据只挑某一个。

---

# 24. Baseline

必须至少比较：

## Baseline A

```text
0.4 behavior
```

即：

> 无完整 0.5 Temporal Awareness 的行为。

---

## Candidate B

```text
0.5 behavior
```

---

# 25. 如果不能直接运行 0.4 binary

可使用：

```text
0.5 with temporal features disabled
```

作为 support proxy。

但报告必须明确：

```text
proxy
```

不得假装是真实 0.4 binary。

---

# 26. 模型保持一致

0.4 baseline/proxy 与 0.5 必须使用相同：

* LLM
* temperature
* embedding
* reranker
* max context
* answer limit
* corpus

否则不公平。

---

# 27. 核心指标

至少记录：

```text
Temporal Accuracy
Current-Version Accuracy
Explicit-Version Accuracy
Historical Accuracy
Evolution Accuracy
Introduced-In Accuracy
Validity Accuracy
Conflict Detection Accuracy
Citation Accuracy
Unsupported Claims
Invalid Provenance
False Supersession
Context Tokens
Latency
```

---

# 28. Current-Version Accuracy

这是最重要指标之一。

定义：

```text
用户问 current/latest/now
→ selected knowledge is actually current
```

目标：

> 应明显高于 0.4 baseline。

---

# 29. Historical Preservation

验证：

> 0.5 没有因为 current resolution 而破坏 historical query。

---

# 30. False Supersession

必须单独统计：

```text
False Supersession Count
```

这是重要 correctness 指标。

例如两个并行功能不能被错误判断成：

```text
A supersedes B
```

---

# 31. Missing Supersession

也统计：

```text
Missed Supersession
```

即：

明明是新旧替代关系，却没有识别。

---

# 32. Conflict Detection

至少构造：

```text
20–30
```

个 conflict cases。

统计：

```text
true conflict detected
false conflict
missed conflict
```

---

# 33. Version Ordering

测试：

```text
v0.4.9
v0.4.10
```

```text
Release 17
Release 18
```

```text
26.2
26.3
```

确保没有字符串比较错误。

---

# 34. 不可比较版本

例如：

```text
Draft-A
Release-Beta
```

系统如果无法排序：

必须保持：

```text
UNKNOWN
```

不能瞎排。

---

# 35. Published Date ≠ Effective Version

至少加入场景：

```text
文件修改时间更新
但内容属于旧版本
```

验证：

> Current resolution 不依赖 file mtime。

---

# 36. Source Authority Test

例如：

```text
Old Design Draft
vs
Official Release Notes
```

验证 source priority 是否合理。

但不能：

> 只因为来源名字叫 Release Notes，就无条件覆盖所有 Evidence。

---

# 37. Temporal Knowledge Audit

随机抽样至少：

```text
100 Knowledge Units
```

检查：

* version metadata
* validity
* status
* source
* provenance
* supersession
* conflict

---

# 38. Supersession Relation Audit

随机抽样至少：

```text
50 supersession relations
```

逐项检查。

分类：

```text
CORRECT
FALSE
AMBIGUOUS
```

---

# 39. Conflict Audit

随机抽样至少：

```text
30 conflict relations
```

检查是否真的是：

```text
same scope + incompatible claims
```

---

# 40. Semantic Memory Pollution

重点观察：

> 错误 Temporal metadata 是否会被 Semantic Memory 反复复用。

例如一个错误：

```text
current=true
```

是否污染多个 Query。

---

# 41. Error Amplification

记录：

```text
single wrong Knowledge Unit
→ number of affected queries
```

如果 amplification 很大：

必须标记为高风险。

---

# 42. Update Test

加入一个真实新版本：

```text
vN+1
```

验证：

```text
new version imported
→ temporal compile
→ new current
→ old becomes historical/superseded
```

---

# 43. Incremental Cost

记录：

* documents affected
* knowledge units affected
* recompilation duration
* tokens used
* unchanged units reused

目标：

> 不能因为新版本加入而全库重编。

---

# 44. Delete Latest Version

删除最新版本。

验证：

```text
current resolution
```

是否重新计算。

如果旧版本仍合法：

可以成为：

```text
current candidate
```

---

# 45. Delete Historical Version

删除历史版本：

不能破坏当前知识。

---

# 46. Rollback Test

如果已有 generation rollback：

验证：

```text
Evidence generation
Temporal generation
Semantic Memory generation
```

一致回退。

---

# 47. Restart

重启前后：

同一 Temporal Query：

结果应保持一致。

---

# 48. Offline Restart

正式 Windows 包环境下测试：

```text
online initial setup
↓
offline restart
↓
temporal query
```

必须 PASS。

---

# 49. Global Regression

重新跑 0.4 Real-World Validation 的代表性 Global subset。

至少：

```text
20
```

题。

确保：

> Temporal filtering 没有过度抑制有价值的历史/补充证据。

---

# 50. Cross-document Regression

至少：

```text
20
```

题。

---

# 51. Multi-hop Regression

至少：

```text
20
```

题。

---

# 52. Comparison Regression

至少：

```text
20
```

题。

---

# 53. Explanation Regression

至少：

```text
20
```

题。

---

# 54. Context Efficiency

比较：

```text
0.4/proxy
vs
0.5
```

记录：

```text
mean
median
p95
```

Context Tokens。

目标：

> Temporal Accuracy 明显提升，但 Token 不大幅增加。

---

# 55. 特别关注 Current Query Token

CURRENT Query 理论上应该：

```text
更少历史噪声
```

因此 Context Token 最好：

```text
下降
```

或至少持平。

---

# 56. Evolution Query Token

EVOLUTION Query 可以合理增加 Context。

但需要：

```text
quality gain > token cost
```

---

# 57. Latency

分开记录：

```text
Non-temporal query
Temporal query
Evolution query
```

至少：

* p50
* p95

---

# 58. Compilation Cost

记录 Temporal compilation：

* wall time
* CPU
* peak memory
* LLM tokens
* disk growth

---

# 59. Scale

不做极端百万文档。

但至少选择一个：

```text
representative real KB
```

确保 Temporal Metadata 不只在几十个文档下工作。

---

# 60. LLM-as-Judge

可以用于评分。

但必须固定：

```text
judge model
prompt
rubric
temperature
```

并人工复核至少：

```text
20%
```

---

# 61. Expert Review

3GPP / Telecom Temporal Query：

优先人工专家复核。

特别是：

```text
introduced
removed
changed
```

类型。

---

# 62. 不允许只看 Support Proxy

最终不能只报告：

```text
score ↑
```

必须同时报告：

```text
Temporal Accuracy
False Supersession
Citation
Unsupported Claims
Provenance
Token
Latency
```

---

# 63. Error Taxonomy

创建：

```text
docs/0.5_real_world_temporal_error_taxonomy.md
```

至少：

```text
RT1 Wrong Current Resolution
RT2 Historical Overridden
RT3 Explicit Version Ignored
RT4 False Supersession
RT5 Missed Supersession
RT6 False Conflict
RT7 Missed Conflict
RT8 Wrong Version Ordering
RT9 Temporal Metadata Missing
RT10 Temporal Metadata Hallucinated
RT11 Context Selection Error
RT12 Final LLM Temporal Reasoning Error
```

---

# 64. Root Cause

每个失败尽量定位：

```text
Metadata
Compiler
Relation
Router
Retriever
Context Compiler
LLM
```

不要全部归类成：

```text
Temporal quality issue
```

---

# 65. P0 / P1 定义

## P0

例如：

* 当前知识大面积选旧版本
* History 无法访问
* Temporal data corruption
* Cross-generation contamination
* False supersession 大量覆盖正确知识
* Citation/provenance broken

---

## P1

例如：

* 某类 temporal intent 系统性识别错误
* conflict 明显漏报
* introduced_in 大量不准

---

# 66. 本轮允许修复

如果验证中发现真正 P0/P1：

允许：

```text
small targeted fix
```

必须：

* 加 regression test
* 重跑相关 benchmark
* 不扩大架构

---

# 67. 版本策略

如果只产生：

```text
validation harness
reports
tests
```

无需立刻发新版本。

如果修复真实 P0/P1：

可以考虑：

```text
v0.5.1
```

但必须走完整 maintenance release gate。

---

# 68. 不要因为验证失败立刻做 0.6

即使发现多个问题：

先排序。

必须找出：

```text
Top 1–3 observed gaps
```

---

# 69. 0.6 Candidate 只能来自数据

创建：

```text
docs/0.6_candidate_gaps.md
```

每个候选必须包含：

```text
Observed evidence
Frequency
Impact
Root cause
Possible direction
Expected gain
Complexity
Risk
```

---

# 70. 可能的 0.6 Candidate

只是示例，不预设：

## Memory Consolidation

只有当出现：

```text
semantic memory duplicates/noise
```

并随规模增长明显恶化时。

---

## Advanced Cross-document Reasoning

只有当：

```text
multi-hop/cross-doc
```

仍是高频弱项。

---

## Domain Ontology

只有当：

```text
3GPP / Telecom abbreviation / synonym / hierarchy
```

导致大量错误。

---

## GraphRAG

只有当：

```text
relation-intensive problems
```

当前 lightweight relations 明显不够。

---

## Code Intelligence

只有 Code Corpus 明确证明普通 Document IR 不够。

---

# 71. 如果没有高价值 Gap

必须允许最终结论：

```text
DO NOT START 0.6
```

这不是失败。

如果 0.5 已经足够稳定：

继续真实使用更有价值。

---

# 72. Real-World Validation 分级

最终只能选择：

```text
STRONG
ACCEPTABLE
WEAK
FAIL
```

---

# 73. STRONG

满足：

* Current-Version 准确稳定
* Historical 正确
* Evolution 明显改善
* False Supersession 很低
* Conflict 正确
* Citation 不退化
* Unsupported claims 接近 0
* Context 不明显增加
* 0.4 Global/Multi-hop 能力不退化

建议：

```text
KEEP 0.5 AS STABLE
DO NOT START 0.6 YET
```

---

# 74. ACCEPTABLE

Temporal 明显改善，但仍存在：

```text
1–2 high-value gaps
```

建议：

```text
START 0.6 ONLY FOR TOP GAP
```

---

# 75. WEAK

Temporal 提升不稳定，或带来明显 regression。

建议：

```text
FIX 0.5 FIRST
```

---

# 76. FAIL

出现：

* temporal knowledge pollution
  -大量 false supersession
* current selection 明显错误
* historical knowledge 丢失
* citation/provenance regression

必须停止扩展。

---

# 77. 最终报告

创建：

```text
docs/0.5_real_world_temporal_validation.md
docs/0.5_real_world_temporal_error_taxonomy.md
docs/0.6_candidate_gaps.md
```

---

# 78. Benchmark Result 文件

保存：

```text
benchmarks/real_world_internalization/temporal_results/
```

或现有项目约定位置。

---

# 79. 最终 Benchmark 表

至少：

| Category         | 0.4/Proxy | 0.5 | Delta |
| ---------------- | --------: | --: | ----: |
| Current          |           |     |       |
| Explicit Version |           |     |       |
| Historical       |           |     |       |
| Evolution        |           |     |       |
| Introduced-In    |           |     |       |
| Validity         |           |     |       |
| Comparison       |           |     |       |
| Conflict         |           |     |       |
| Global           |           |     |       |
| Multi-hop        |           |     |       |

---

# 80. Quality 指标

必须输出：

```text
Temporal Accuracy
Current-Version Accuracy
Historical Accuracy
Evolution Accuracy
Conflict Detection
False Supersession
Missed Supersession
Citation Accuracy
Unsupported Claims
Invalid Provenance
```

---

# 81. Efficiency

输出：

```text
Average Context Tokens
Median Context Tokens
P95 Context Tokens

P50 Latency
P95 Latency
```

---

# 82. Lifecycle

输出：

```text
Incremental Update: PASS / FAIL
Delete Latest: PASS / FAIL
Delete Historical: PASS / FAIL
Rollback: PASS / FAIL
Restart: PASS / FAIL
Offline Restart: PASS / FAIL
```

---

# 83. Knowledge Audit

输出：

```text
Temporal Knowledge Units audited
Supersession relations audited
Conflict relations audited
Errors found
Error rate
```

---

# 84. Regression

输出：

```text
Global
Cross-document
Multi-hop
Comparison
Explanation
```

是否保持或改善。

---

# 85. Final Recommendation

只能选择：

```text
KEEP 0.5 AND CONTINUE REAL USE
```

或：

```text
START 0.6 WITH SPECIFIC GAP
```

或：

```text
FIX 0.5 FIRST
```

---

# 86. 如果建议启动 0.6

最多列：

```text
3 candidates
```

但必须明确：

```text
PRIMARY
SECONDARY
DEFERRED
```

不要同时开发三个。

---

# 87. Commit 原则

验证 harness / docs / tests：

可以提交。

真实私有 Corpus：

不得提交。

如果产生代码修复：

独立 commit。

---

# 88. Push CI

所有提交必须通过当前：

```text
Build
Go Test
Race
Web
Benchmark Smoke
Browser E2E
```

---

# 89. 不要创建新 Tag

除非：

```text
真正修复了 P0/P1
并准备发布 v0.5.1
```

纯验证结果不创建新 tag。

---

# 90. Token 控制

前一轮 release promotion 消耗较高。

本任务必须避免：

* 全仓重新审计
* 重读所有 0.2/0.3/0.4 历史
* 重复研究所有参考项目
* 无关架构分析

优先：

```text
reuse existing harness
reuse existing reports
target temporal behavior
```

---

# 91. Stop Rule

如果验证已经明确得到：

```text
STRONG
```

不要继续挖问题直到找到新功能需求。

停止。

---

# 92. 不要为了 0.6 而寻找 0.6

最终目标不是：

> 找理由开发下一版本。

而是：

> 判断当前 0.5 是否已经足够成为长期稳定知识系统。

---

# 93. 最终成功定义

0.5 Real-World Validation 成功意味着：

当知识库中同时存在：

```text
旧知识
当前知识
不同版本
演进关系
冲突信息
```

Shutu-Knowledge 能够稳定回答：

```text
What is true now?
What was true before?
What changed?
When did it change?
Is this still valid?
Which sources disagree?
```

并且每个结论仍然能够回到：

```text
Original Evidence
```

---

# 94. 最终原则

始终遵守：

```text
Validate before evolving.

Prefer correct uncertainty over confident chronology.

History must remain queryable.

Current knowledge must be explainable.

Supersession must be evidence-based.

Temporal intelligence must not weaken provenance.

Improve knowledge selection, not context volume.

Do not start 0.6 without observed need.
```

最终必须回答一个问题：

# “Shutu-Knowledge 0.5 是否真的知道：什么知识现在有效、过去是什么、以及它是如何变化的？”
