# Shutu-Knowledge 0.7 — Structured Data Model Intelligence

## 0. 正式基线

当前正式稳定版本：

```text
Shutu-Knowledge v0.6.4
```

Release source：

```text
39926aa84ed6321871eddbe57f4943b8fa851a7a
```

正式 closure：

```text
01eca25f9318ef9b7f9a55cb421627eba1da0bcb
```

v0.6.4 已验证：

* PDF ingestion correctness
* Excel structured ingestion
* 154,475 chunk 真实规模 Embedding
* Local Embedding / Reranker runtime
* BM25 / Vector / Hybrid / Reranker
* Background embedding + interactive search
* Cancellation / restart / recovery
* Generation isolation
* Cross-process historical reader / GC correctness
* Windows formal package
* Offline restart

因此：

> 0.7 不再处理 ingestion/runtime/release correctness 基础问题。

---

# 1. 真实问题已经被数据证明

真实 SmartCare 数据字典：

```text
50 Workbooks
2,109 Sheets
154,475 active chunks
```

Runtime 已经稳定。

但真实语义 QA 仍然：

```text
Semantic reverse lookup:
7 / 30

Cross-workbook evidence:
0 / 30

Join / data-model evidence:
3 / 20

Analysis requirement evidence:
2–3 / 20
```

最终分类：

```text
WEAK
```

因此当前主要瓶颈不是：

```text
Excel parser
Embedding
Reranker
Runtime
```

而是：

# Chunk-centric representation is insufficient for structured schema knowledge.

---

# 2. 0.7 唯一目标

将：

```text
Excel Data Dictionary
        ↓
154,475 searchable chunks
```

升级为：

```text
Excel Data Dictionary
        ↓
Structured Data Model Knowledge
        ↓
Logical Tables
Fields
Enums
Scopes
Business Concepts
Candidate Keys
        ↓
Schema-aware Retrieval
        ↓
Data Requirement Resolution
        ↓
Original Evidence
```

最终目标：

> AI 不只是“搜索 Excel 文本”，而是能够把真实数据字典当成数据模型知识来查找、组合和推理。

---

# 3. 0.7 不是 Excel 专用系统

设计必须保持通用。

核心抽象：

```text
LogicalTable
Field
Enum
EnumValue
Scope
KeyCandidate
BusinessConcept
DataRequirement
```

以后应可复用于：

```text
Excel Data Dictionary
CSV Schema
Database Schema
Parquet Metadata
API Schema
```

禁止创建：

```text
SmartCareField
SmartCareTable
SmartCareResolver
```

等产品专用核心模型。

---

# 4. 严格禁止事项

本版本禁止：

* GraphRAG
* Neo4j
* Full Knowledge Graph
* Heavy Ontology
* PostgreSQL
* New Vector DB
* Redis
* Distributed Workers
* SQL execution
* SQL Agent
* Recursive Research
* Agent core redesign
* LLM fine-tuning
* Excel parser full rewrite
* Temporal architecture changes
* PDF architecture changes

如果发现未来价值：

记录到：

```text
docs/0.8_candidate_gaps.md
```

不要在 0.7 实现。

---

# 5. Feature Scope

0.7 只允许实现：

```text
Data Model IR
Schema Knowledge Compiler
Field/Table Semantic Index
Schema-aware Retrieval
Cross-workbook Schema Reasoning
Candidate Join Reasoning
Data Requirement Resolver
Structured Data Context Compiler
Provenance
Incremental lifecycle
```

---

# 6. PH0 — Real Failure Audit

首先读取：

```text
docs/0.6.2_excel_dictionary_validation.md
```

以及本地 `.tmp/` 中原始 validation evidence。

重点分析：

```text
7/30 Semantic reverse lookup
0/30 Cross-workbook
3/20 Join/Data-model
2–3/20 Analysis requirement
```

---

# 7. PH0 必须回答

每类失败必须定位到：

```text
Representation
Scope
Retrieval
Knowledge Compiler
Semantic Mapping
Context Assembly
LLM Reasoning
```

不要只写：

```text
semantic quality insufficient
```

---

# 8. Gap Analysis Report

创建：

```text
docs/0.7_structured_data_model_gap_analysis.md
```

至少包含：

| Failure | Example Type | Root Cause | Layer | Proposed Mechanism |
| ------- | ------------ | ---------- | ----- | ------------------ |

---

# 9. PH1 — Data Model IR

在现有 Document IR 之上增加最小 Schema semantic layer。

不要替换 Document IR。

关系：

```text
Excel Source
   ↓
Document IR
   ↓
Structured Data Model IR
   ↓
Derived Schema Knowledge
```

---

# 10. LogicalTable

建议最小模型：

```text
LogicalTable {
    id
    name
    aliases
    description
    workbook_id
    sheet_id
    region_anchor
    scope
    source_path
    derived_from
    confidence
}
```

字段可按现有代码风格调整。

---

# 11. Field

建议：

```text
Field {
    id
    name
    display_name
    aliases
    data_type
    description
    nullable
    table_id
    enum_id
    key_hint
    source_anchor
    derived_from
    confidence
}
```

---

# 12. Field Identity

非常重要：

```text
Field identity ≠ field name
```

正确 identity 至少包含：

```text
scope
+
table
+
field
```

例如：

```text
Table A.status
Table B.status
```

默认必须是两个不同 Field。

---

# 13. Scope

Scope 至少能表达：

```text
Directory
Product
Workbook
Sheet
LogicalTable
```

任何 Field/Enum/KeyCandidate 都必须保留所属 scope。

---

# 14. Enum

建议：

```text
Enum {
    id
    field_id
    values
    labels
    description
    source_anchor
    derived_from
}
```

必须保留：

```text
value → meaning
```

映射。

---

# 15. KeyCandidate

不是正式 Foreign Key。

只表示：

```text
possible identifier / join candidate
```

建议：

```text
KeyCandidate {
    field_id
    type
    confidence
    reason
    evidence
}
```

---

# 16. KeyCandidate Type

可以支持最小类型：

```text
PRIMARY_KEY_HINT
IDENTIFIER
JOIN_CANDIDATE
PARTITION_KEY
TIME_KEY
UNKNOWN
```

不要构造复杂 DB constraint engine。

---

# 17. BusinessConcept

建立轻量：

```text
BusinessConcept
```

例如：

```text
User Identifier
Subscriber
Site
Location
RAT
Traffic
Revenue
Terminal
Time
Voice
Data
```

但这些只是示例。

不能硬编码 SmartCare 领域全集。

---

# 18. BusinessConcept 来源

优先从：

```text
Field name
Display name
Description
Enum
Table context
Existing Semantic Memory
```

派生。

必须保留：

```text
derived_from
```

---

# 19. Concept 不等于 Ontology

0.7 只需要：

```text
concept
→ candidate fields
```

不做：

```text
large hierarchical telecom ontology
```

---

# 20. PH2 — Data Model Detection

不能：

```text
any Excel
→ Data Dictionary
```

必须识别：

```text
schema-like table
```

---

# 21. Detection Signal

可结合：

```text
header names
row regularity
column semantics
field-name patterns
data-type column
description column
enum/value column
table-name context
```

---

# 22. Detection Confidence

记录：

```text
schema_detection_confidence
```

低 confidence：

```text
fallback to ordinary Excel behavior
```

不要强行编译 Schema。

---

# 23. Non-dictionary Excel Regression

创建 synthetic fixtures：

```text
financial.xlsx
calendar.xlsx
dashboard-export.xlsx
generic-table.xlsx
```

确保不会全部识别成 Data Dictionary。

---

# 24. PH3 — Logical Table Detection

真实 Sheet 不一定：

```text
1 Sheet = 1 Table
```

必须支持：

```text
one sheet
→ multiple logical table regions
```

复用现有已识别的：

```text
3,147 logical table regions
```

能力。

---

# 25. Table Context Propagation

如果 Table name 来自：

```text
merged cell
section heading
previous rows
```

必须正确传播到 Fields。

---

# 26. Multi-row Header

支持：

```text
row 1 category
row 2 field name/type/description
```

等多层 header。

不能把 header 当字段数据。

---

# 27. PH4 — Schema Knowledge Compiler

新增：

# Structured Data Model Compiler

但尽量作为现有 Knowledge Compiler 的 schema-aware path。

不要建立第二套完全独立平台。

---

# 28. Compiler Flow

```text
Excel Document IR
     ↓
Schema Detection
     ↓
Logical Table
     ↓
Field Extraction
     ↓
Enum Extraction
     ↓
Scope Binding
     ↓
Semantic Normalization
     ↓
Business Concept Mapping
     ↓
Schema Knowledge
```

---

# 29. 不允许 Cell → Generic Concept

禁止：

```text
every cell
→ Knowledge Concept
```

正确粒度：

```text
field-definition row
→ Field

logical region
→ LogicalTable

enum region
→ Enum
```

---

# 30. Field Knowledge Representation

用于 AI 的 Field Knowledge 至少应该包含：

```text
Field Name
Display Name
Aliases
Table
Workbook
Data Type
Meaning
Enum Summary
Business Concepts
Source Anchor
```

---

# 31. Table Knowledge Representation

至少：

```text
Table Name
Aliases
Description
Business Purpose
Fields
Identifier Candidates
Measure-like Fields
Dimension-like Fields
Scope
Source
```

但：

```text
Business Purpose
```

必须有 evidence。

---

# 32. 不得幻觉 Table Purpose

如果字典只有字段列表，没有明确业务用途：

必须表达：

```text
inferred / uncertain
```

或者不生成。

---

# 33. Schema Deduplication

允许：

```text
same exact table repeated in duplicate workbook
```

进行 cautious dedup。

但默认：

```text
same field name
≠ same field identity
```

---

# 34. False Dedup Hard Rule

以下字段：

```text
id
status
name
type
value
date
```

不得仅凭名字跨 Table 合并。

---

# 35. PH5 — Field Semantic Index

不要只向量化：

```text
field description
```

构造 scoped semantic representation：

```text
Field Name
Display Name
Aliases
Description
Table Name
Table Description
Workbook/Sheet Context
Enum Summary
Business Concepts
```

---

# 36. Field Vector

每个 Field 建立独立 semantic vector。

保留：

```text
entity_type = FIELD
```

---

# 37. Table Semantic Index

每个 LogicalTable 建立：

```text
entity_type = TABLE
```

representation。

建议包含：

```text
table name
description
field names
field semantic summary
business concepts
scope
```

---

# 38. Concept Semantic Index

如果实现 BusinessConcept：

可以建立：

```text
entity_type = BUSINESS_CONCEPT
```

但不要制造大量 synthetic concept。

---

# 39. Entity Index 与 Evidence Index 分离

必须能区分：

```text
Evidence Chunk
Field
Table
Concept
```

即使共用同一 storage/vector infrastructure。

---

# 40. Vector 数量控制

新增向量数量应近似：

```text
number of fields
+
number of tables
+
limited concepts
```

禁止产生关系组合爆炸。

---

# 41. PH6 — Query Classification

增加最小 Schema Intent：

```text
EXACT_FIELD
EXACT_TABLE
ENUM_LOOKUP
SEMANTIC_FIELD
TABLE_DISCOVERY
CROSS_TABLE
JOIN_CANDIDATE
DATA_REQUIREMENT
```

---

# 42. Exact Field Query

例如：

```text
home_rat 是什么？
```

优先：

```text
exact field lookup
```

如果多个 scope：

返回：

```text
Candidate 1
Candidate 2
...
```

不能随机选一个。

---

# 43. Exact Table Query

例如：

```text
voice_data_revenue 表有哪些字段？
```

优先加载：

```text
LogicalTable
+
Fields
+
Evidence
```

而不是普通 chunk recall。

---

# 44. Enum Query

例如：

```text
home_rat 有哪些取值？
```

优先：

```text
Field
→ Enum
→ EnumValue
→ Source
```

---

# 45. PH7 — Semantic Reverse Lookup

例如：

```text
哪些字段表示用户标识？
```

目标流程：

```text
Query
 ↓
Business concept / semantic intent
 ↓
Field semantic recall
 ↓
candidate fields
 ↓
scope grouping
 ↓
evidence verify
```

---

# 46. 不要把 semantic similarity 当 equivalence

例如：

```text
imsi
msisdn
subscriber_id
```

都可能与用户标识有关。

但不能说：

```text
these fields are identical
```

只能说：

```text
related/candidate fields
```

---

# 47. PH8 — Scope Expansion

这是 Cross-workbook 的关键。

找到 Field 后，可以扩展：

```text
Field
 ↓
LogicalTable
 ↓
related Fields
 ↓
Workbook
```

---

# 48. Cross-workbook Example

Query：

```text
哪些表同时存在用户标识、站点和流量字段？
```

不要：

```text
TopK 50 chunks
→ LLM guess
```

而应该：

```text
User Identifier
→ field set A

Site
→ field set B

Traffic
→ field set C

map fields → table IDs

intersection(A.tables, B.tables, C.tables)

→ candidate tables
→ evidence
```

---

# 49. Set-based Schema Reasoning

最少实现：

```text
union
intersection
group by table
group by workbook
filter by concept
```

只针对 Schema entities。

不需要通用 graph engine。

---

# 50. PH9 — Candidate Join Reasoning

Query：

```text
表 A 和表 B 可能通过哪些字段关联？
```

计算候选时可考虑：

```text
field-name similarity
semantic meaning
data type
identifier concept
explicit key hints
enum/domain
scope compatibility
```

---

# 51. Join Output

必须返回：

```text
Candidate Field A
Candidate Field B
Confidence
Reason
Evidence
```

---

# 52. Join Hard Rule

如果数据字典没有明确 FK：

不能输出：

```text
A must JOIN B ON X
```

只能：

```text
candidate join key
```

---

# 53. Unsupported Join Claim

必须作为硬错误统计。

---

# 54. PH10 — Data Requirement Resolver

这是 0.7 最关键能力之一。

输入：

```text
我要分析过去30天有5G流量但没有语音业务的用户
```

---

# 55. Resolver Output

输出：

```text
Business Concepts

Candidate Tables

Candidate Fields

Possible Filters

Candidate Join Keys

Enums / Units

Unknown / Need Confirmation

Evidence
```

---

# 56. 0.7 不生成 SQL

明确停止于：

```text
Natural Language
→ Data Model Resolution
```

不要进入：

```text
SQL generation
SQL execution
```

---

# 57. Resolver 必须能表达 Unknown

如果字典没有：

```text
voice active definition
```

必须回答：

```text
cannot determine exact criterion from current dictionary
```

不能编造。

---

# 58. PH11 — Structured Context Compiler

普通 Query Context：

```text
20 random chunks
```

不适用于 Data Model。

应生成：

```text
Requirement

Concept A
  Candidate Fields
  Tables
  Evidence

Concept B
  Candidate Fields
  Tables
  Evidence

Candidate Table Combination

Potential Join Keys

Unknowns
```

---

# 59. Token Budget

Structured Context 必须有明确 budget。

目标：

> 使用结构减少噪声，而不是增加 TopK。

---

# 60. Provenance

所有派生实体必须：

```text
derived_from
```

最终回到：

```text
Workbook
Sheet
Row / Range
```

---

# 61. Citation 粒度

对于 OOXML：

优先增强到：

```text
Workbook
Sheet
Range
```

例如：

```text
Dictionary.xlsx
Sheet: Subscriber
Range: B217:F217
```

---

# 62. Citation 不应阻塞整体 0.7

如果某些 legacy `.xls` 只能到：

```text
Workbook + logical section
```

允许降级。

但必须明确。

---

# 63. PH12 — Incremental Lifecycle

修改一个字段：

只更新：

```text
Field
Table summary
Concept mapping
Relevant vectors
Relevant join candidates
```

不要全库重编。

---

# 64. Delete

删除 Workbook：

必须删除：

```text
Tables
Fields
Enums
Concept links
Join candidates
Vectors
Derived schema knowledge
```

---

# 65. Atomic Generation

继续保持：

```text
old schema generation
↓
new compile
↓
atomic publish
```

不能产生：

```text
new Field
+
old Table
```

混合。

---

# 66. Restart

编译完成后 restart：

Schema Knowledge 必须保持一致。

---

# 67. Migration

v0.6.4 → 0.7：

必须：

```text
PASS
```

旧 KB 可以：

```text
schema knowledge absent
```

按需编译。

禁止强制重导所有原始文档。

---

# 68. Real Corpus 固定

继续使用原来的：

```text
50 SmartCare Data Dictionary Workbooks
```

不要换 corpus。

---

# 69. Query Set 固定

继续使用原来的真实 QA。

不要因为 0.7 设计改变而换题。

基线：

```text
Semantic reverse lookup:
7/30

Cross-workbook:
0/30

Join/Data-model:
3/20

Analysis:
2–3/20
```

---

# 70. Exact Query 也必须重跑

如果原有 Exact 50：

全部重跑。

目标：

```text
no regression
```

---

# 71. Benchmark Modes

比较：

```text
0.6.4 chunk-centric
vs
0.7 schema-aware
```

同：

* corpus
* embedding
* reranker
* LLM
* temperature
* query set

---

# 72. Semantic Reverse Lookup Goal

当前：

```text
7 / 30
```

要求：

```text
material improvement
```

不要为了目标强设 30/30。

---

# 73. Cross-workbook Goal

当前：

```text
0 / 30
```

这是最重要的一个。

0.7 必须：

```text
materially break through zero
```

如果仍接近 0：

说明 Scope Expansion / Set Reasoning 设计失败。

---

# 74. Join/Data-model Goal

当前：

```text
3 / 20
```

目标：

```text
significant improvement
```

同时：

```text
unsupported join claims ≈ 0
```

---

# 75. Analysis Goal

当前：

```text
2–3 / 20
```

必须验证：

```text
Requirement
→ Concept
→ Table
→ Field
```

真正起作用。

---

# 76. Hallucination Hard Metrics

统计：

```text
Hallucinated Table
Hallucinated Field
Wrong Enum
Wrong Data Type
Unsupported Join
Unsupported Business Meaning
```

---

# 77. Hallucination Rule

任何不存在的 Table/Field：

都是严重错误。

不要因为答案总体合理而忽略。

---

# 78. Structured Knowledge Audit

至少人工审计：

```text
100 Fields
50 Logical Tables
30 Enums
30 BusinessConcept mappings
20 JoinCandidates
```

---

# 79. Audit 分类

```text
CORRECT
WRONG_SCOPE
DUPLICATE
OVER_GENERALIZED
UNSUPPORTED
MISSING_EVIDENCE
```

---

# 80. Scope Collision Audit

至少：

```text
50 same-name fields across different tables
```

目标：

```text
wrong merge = 0
```

---

# 81. Semantic Alias Audit

至少：

```text
30 different-name / similar-meaning fields
```

检查：

```text
recall quality
```

但不要强制 equivalence。

---

# 82. BusinessConcept Pollution Audit

检查：

```text
generic concepts
duplicate concepts
wrong domain concepts
unsupported abstractions
```

---

# 83. Knowledge Growth

记录：

```text
LogicalTable count
Field count
Enum count
Concept count
JoinCandidate count
```

---

# 84. DB Growth

记录：

```text
v0.6.4 KB size
v0.7 KB size
delta
```

避免 schema layer 失控膨胀。

---

# 85. Embedding Growth

记录：

```text
chunk vectors
field vectors
table vectors
concept vectors
```

---

# 86. Performance

至少：

```text
schema compile time
incremental compile time
peak RSS
CPU
DB growth
search p50
search p95
```

---

# 87. Search Benchmark

分别：

```text
Exact field
Semantic field
Cross-workbook
Join candidate
Data requirement
```

记录：

```text
p50
p95
timeout
```

---

# 88. Runtime Regression

v0.6.4 已稳定的 runtime 不能退化。

必须验证代表性：

```text
full embedding
reranker
background search
cancel
restart
memory stability
```

---

# 89. PDF Regression

至少跑：

```text
PDF import
retrieval
quality metadata
```

smoke。

0.7 不能破坏 PDF。

---

# 90. Ordinary Excel Regression

不是数据字典的 Excel：

继续走普通结构化文档路径。

不得强行进入 Schema Compiler。

---

# 91. DOCX/PPTX Regression

运行最小 import/search smoke。

---

# 92. Temporal Regression

运行代表性：

```text
current
explicit version
historical
range
```

确保无无关回退。

---

# 93. API

如果需要新增：

优先：

```text
search_schema
get_table
get_field
resolve_data_requirement
```

保持最小。

---

# 94. Shutu-Agent

只允许通过现有 Extension/API 使用。

不修改 Agent Core。

---

# 95. UI

只做最小可观察性。

例如：

```text
Detected Data Models
Logical Tables
Fields
Enums
```

可以查看。

---

# 96. 不做 ER Diagram Designer

不要花时间做：

```text
drag/drop ER UI
visual graph designer
```

---

# 97. Diagnostics

开发模式建议可以输出：

```text
Query
Intent
Resolved Concepts
Candidate Fields
Candidate Tables
Set Operations
Join Candidates
Evidence
Final Context
```

---

# 98. Failure Taxonomy

创建：

```text
docs/0.7_structured_data_model_error_taxonomy.md
```

至少：

```text
SD1 Schema Detection Failure
SD2 Logical Table Detection Failure
SD3 Header Mapping Failure
SD4 Field Scope Loss
SD5 Enum Corruption
SD6 Same-name Wrong Merge
SD7 Semantic Field Miss
SD8 Table Expansion Failure
SD9 Cross-workbook Set Failure
SD10 Join Hallucination
SD11 Requirement Decomposition Failure
SD12 Wrong Concept Mapping
SD13 Context Compilation Failure
SD14 Provenance Error
```

---

# 99. P0

例如：

```text
wrong field assigned to table
different tables merged
data corruption
invalid provenance
hallucinated table frequently produced
generation inconsistency
```

---

# 100. P1

例如：

```text
cross-workbook reasoning systematically weak
semantic reverse lookup systematically weak
join candidate scoring systematically wrong
requirement resolver frequently misses concepts
```

---

# 101. P2

例如：

```text
UI polish
minor ranking
extra visualizations
```

不阻塞核心验证。

---

# 102. Validation Classification

最终：

```text
STRONG
ACCEPTABLE
WEAK
FAIL
```

---

# 103. STRONG

意味着：

```text
Exact lookup strong
Semantic reverse lookup strong
Cross-workbook materially solved
Analysis requirement useful
Join candidate useful
No field/table hallucination
Scope preservation strong
Runtime stable
```

---

# 104. ACCEPTABLE

意味着：

```text
clear real-world improvement
production-useful
only 1–3 specific high-value gaps remain
```

---

# 105. WEAK

意味着：

```text
schema layer implemented
but real QA remains poor
```

此时：

```text
DO NOT RELEASE v0.7.0
```

继续定位问题。

---

# 106. FAIL

例如：

```text
scope pollution
false table/field merges
data corruption
serious hallucination
runtime regression
```

停止 release。

---

# 107. Release Authorization Boundary

本任务允许：

```text
design
implementation
tests
benchmark
real-corpus validation
push feature branch
open/update PR
merge only if current repo workflow authorizes normal reversible merge
```

但：

# 不预授权 public release promotion

---

# 108. 不允许提前创建 v0.7.0 Tag

必须先达到：

```text
ACCEPTABLE
```

或：

```text
STRONG
```

---

# 109. Release Candidate Stop Point

完成以下后停止：

```text
implementation complete
full CI PASS
real 50-workbook benchmark complete
150-query QA complete
classification ACCEPTABLE/STRONG
formal artifact prepared
exact source SHA known
```

然后等待：

```text
explicit publication approval
```

---

# 110. 如果最终 WEAK

不要为了版本号继续做 release。

输出：

```text
Top observed gap
Root cause
Evidence
Next smallest fix
```

---

# 111. Commit Strategy

按 Phase 小提交。

建议类似：

```text
feat(schema): add data model IR
feat(schema): compile logical tables and fields
feat(schema): add schema semantic index
feat(schema): add schema-aware retrieval
feat(schema): add cross-workbook set reasoning
feat(schema): add requirement resolver
test(schema): add structured model regression
docs(schema): record real-world validation
```

不要形成一个巨大 commit。

---

# 112. Token 控制

禁止重新：

* 审计全部历史版本
* 研究 GraphRAG 全生态
* 重写 parser
* 重跑无关 PDF 大规模测试

优先：

```text
existing real corpus
existing QA
existing runtime
targeted schema work
```

---

# 113. Stop Rule

如果 PH1–PH4 完成后，early benchmark 显示：

```text
Semantic reverse lookup
Cross-workbook
```

完全没有改善：

先停下来定位 representation 问题。

不要继续堆更多 Resolver 逻辑。

---

# 114. Early Ablation

至少比较：

```text
Chunks only
Field entities only
Field + Table
Field + Table + Concept
Full schema-aware
```

验证哪个机制真正带来收益。

---

# 115. 不允许“更多 TopK”作为主要改进

如果改善主要来自：

```text
TopK 20 → TopK 200
```

不算成功。

0.7 必须证明：

```text
better representation
```

带来提升。

---

# 116. 不允许 LLM 直接扫描全 Schema

不能为了 QA：

```text
dump thousands of fields into context
```

然后让 LLM找答案。

必须通过：

```text
schema retrieval
set reasoning
context compilation
```

控制 context。

---

# 117. Context Efficiency

比较：

```text
0.6.4 tokens
0.7 tokens
```

尤其 Cross-workbook query。

目标：

> quality ↑ while context remains bounded.

---

# 118. Final Benchmark Table

最终必须输出：

| Category         | v0.6.4 | v0.7 | Delta |
| ---------------- | -----: | ---: | ----: |
| Exact            |        |      |       |
| Semantic Reverse |   7/30 |      |       |
| Cross-workbook   |   0/30 |      |       |
| Analysis         | 2–3/20 |      |       |
| Join/Data-model  |   3/20 |      |       |

---

# 119. Hallucination Report

必须输出：

```text
Hallucinated Tables:
Hallucinated Fields:
Wrong Enums:
Wrong Types:
Unsupported Join Claims:
Unsupported Business Meanings:
```

---

# 120. Structured Model Report

输出：

```text
Logical Tables
Fields
Enums
Business Concepts
Key Candidates

Wrong scope
False dedup
Invalid provenance
```

---

# 121. Performance Report

输出：

```text
Schema compile time
Incremental compile time
Peak RSS
DB size delta
Vector count delta
Search p50
Search p95
```

---

# 122. Regression Report

输出：

```text
PDF
Ordinary Excel
DOCX
PPTX
Temporal
Runtime
Generation
Restart
```

全部状态。

---

# 123. Final Question 1

必须回答：

> 0.7 是否真正将 Excel 数据字典从“可搜索的文本块”升级为“结构化数据模型知识”？

---

# 124. Final Question 2

必须回答：

> 用户只描述业务需求，不知道表名字段名时，系统是否能找到真实存在的相关表字段？

---

# 125. Final Question 3

必须回答：

> 系统是否能够进行跨 Workbook 的字段集合、表发现和候选 Join 推理，同时不编造不存在的数据模型？

---

# 126. Final Question 4

必须回答：

> 0.7 的提升来自 Schema Representation / Structured Reasoning，还是只是更多 Context/TopK？

---

# 127. 最终成功定义

0.7 成功不是：

```text
Excel search got better
```

而是：

> Shutu-Knowledge 能把真实数据字典编译成带 Scope、Table、Field、Enum、业务概念与候选关联关系的结构化知识；AI 能根据业务语义发现相关字段、跨 Workbook 组合数据模型、提出有证据支持的候选关联方式，并且所有结论仍可回溯到原始 Excel。

---

# 128. 最终原则

```text
Structure before more retrieval.

Scope is part of field identity.

Same name does not mean same meaning.

Similar meaning does not mean same schema.

Schema knowledge must remain evidence-grounded.

Cross-workbook reasoning should use set operations, not giant Top-K.

Join reasoning must remain probabilistic unless explicitly defined.

Resolve the data model before generating SQL.

Do not add GraphRAG where structured entities and relations are enough.

Do not let derived schema knowledge replace original Excel evidence.

Do not release v0.7.0 while the real benchmark remains WEAK.
```

最终目标：

# “让 AI 真正理解数据模型，而不是只会搜索数据字典里的文字。”
