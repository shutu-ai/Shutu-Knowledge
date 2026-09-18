# Shutu-Knowledge 0.3 — Document Intelligence / Structured Knowledge Foundation

## 0. 当前基线

Shutu-Knowledge `v0.2.1` 已正式发布并进入：

```text
0.2.x MAINTENANCE MODE
```

正式发布状态：

* Release: `Shutu Knowledge v0.2.1`
* Windows x64: Tier-1
* Push CI: PASS
* Tag CI: PASS
* Package smoke: PASS
* Runtime/OCR/PDF/Embedding/Rerank/Agent integration: PASS
* 0.2.x 后续只允许 bugfix / security / compatibility / data-correctness 修复

当前 0.3 开发必须基于稳定的 0.2.1 基础继续演进。

不要重新打开已经收口的基础设施架构问题。

---

# 1. 0.3 的核心目标

0.3 不再以：

```text
File
→ Text
→ Chunk
→ Embedding
→ Retrieval
```

作为知识处理的最终模型。

目标升级为：

```text
File
        ↓
Parser
        ↓
Structured Document IR
        ↓
Document Understanding
        ↓
Semantic Knowledge Units
        ↓
BM25 / Vector / Hybrid Retrieval
        ↓
Rerank / Context Composition
        ↓
Grounded Evidence
        ↓
Shutu-Agent / LLM
```

核心目标：

> 让 Shutu-Knowledge 不仅“能检索到文档里的文字”，还能够理解文字在文档中的结构、层级、页面、表格、图片、章节和上下文关系。

0.3 的本质是：

# Document Intelligence Foundation

而不是增加更多 Retrieval 算法。

---

# 2. 0.3 要解决的核心问题

当前传统 RAG 存在典型问题：

```text
复杂文档
     ↓
抽取纯文本
     ↓
切 Chunk
     ↓
文档结构丢失
```

例如 PPT：

```text
Slide 12
├─ Title
├─ KPI Chart
├─ Explanation
├─ Footnote
└─ Source
```

传统 Chunk 后可能只剩：

```text
Chunk 37
Chunk 38
Chunk 39
```

LLM 能看到文字，但不知道：

* 属于第几页/第几张 Slide
* 哪个标题控制这些内容
* 哪些数字属于某张表
* 哪个 Caption 对应哪张图
* 某段话是不是脚注
* 表格中行列关系
* 多栏页面的阅读顺序
* 某段文字和前后章节之间的关系

这就是 0.3 首先必须解决的问题。

---

# 3. 产品定位

0.3 之后 Shutu-Knowledge 的定位应该逐步变成：

> Local-first Agent Knowledge Runtime + Evidence Engine + Document Intelligence Layer

不是：

> Mini RAGFlow

也不是：

> Vector Database UI

更不是：

> 单纯 LLM Wiki。

---

# 4. 0.3 不做什么

严格控制 Scope。

本阶段禁止主动实施：

* GraphRAG
* 全量 Knowledge Graph
* 自动 Ontology 构建
* 企业级 multi-tenant
* PostgreSQL migration
* Elasticsearch
* Redis
* Kafka
* 分布式 worker
* 微服务拆分
* 多节点部署
* SaaS
* Linux/macOS 完整 release packaging
* 新一轮 Storage 大重构
* 新一轮 Agent Contract 大重构
* 新的 Vector DB
* 大量新增 Embedding 模型
* 大量新增 Reranker 模型
* 全自动 LLM Wiki
* 长期 autonomous knowledge agent
* 模型训练 / fine-tuning

如果这些能力未来有价值：

记录到：

```text
docs/backlog_0.4_plus.md
```

不要在 0.3 提前实现。

---

# 5. 第一原则：兼容 0.2

0.3 必须建立在 0.2.1 之上。

禁止破坏：

* Existing KB
* Existing SQLite storage
* Existing document records
* Existing search API
* Existing Agent Extension Contract
* Existing citation behavior
* Existing runtime manager
* Existing Embedding
* Existing Reranker
* Existing OCR
* Existing PDF renderer
* Existing update/delete/reindex semantics
* Existing generation isolation
* Existing durable operations

如果需要新增 schema：

必须使用：

```text
backward-compatible migration
```

旧知识库升级后必须可直接使用。

禁止要求用户删除并重建全部 KB，除非某个增强索引明确是 optional rebuild。

---

# 6. Phase 0 — 现状审计

不要直接编码。

首先审计现有代码。

重点回答：

## Parser Pipeline

当前各文件类型如何进入系统：

* PDF
* DOCX
* DOC
* PPTX
* PPT
* XLSX
* XLS
* HTML
* Markdown
* TXT
* EPUB

分别经过什么 parser/runtime。

---

## Intermediate Representation

确认现在 Parser 输出到底是什么：

* plain text
* blocks
* page structure
* headings
* tables
* images
* metadata

哪些信息已经存在但后续被丢失？

不要重复实现已经具备的信息。

---

## Chunk Model

当前 Chunk schema 包含哪些字段：

例如：

```text
document_id
chunk_id
chunk_index
text
heading
context
metadata
generation
```

确认缺失：

```text
page
slide
sheet
bbox
block
table
figure
caption
reading_order
parent
```

的真实情况。

---

## Citation Model

确认当前 citation 能定位到：

```text
document
chunk
```

还是已经具备更细的 source anchor。

---

## Search Pipeline

确认当前：

```text
BM25
Vector
RRF
Reranker
Threshold
MMR
Sibling Context
```

如何组织。

0.3 原则：

> 尽量复用，不重写 Retrieval Engine。

---

# 7. Phase 1 — 定义统一 Document IR

0.3 最重要的设计任务是建立：

# Document IR

它必须是：

* parser-independent
* format-independent
* serializable
* versioned
* deterministic
* backward compatible
* usable without LLM

建议概念模型：

```text
Document
│
├── Metadata
│
├── Node[]
│
└── Relationships[]
```

Node 至少支持：

```text
Document
Section
Page
Slide
Sheet
Block
Paragraph
Heading
List
ListItem
Table
TableRow
TableCell
Figure
Image
Caption
CodeBlock
Footnote
Header
Footer
```

不要一开始设计成无限复杂的通用 AST。

必须满足真实 Knowledge 使用需求即可。

---

# 8. Document IR 核心字段

每个 Node 建议至少具有：

```text
id
type

document_id

parent_id
children_ids

order

text

heading_path

source_anchor

page_number
slide_number
sheet_name

bbox

metadata

parser
parser_version

confidence
```

具体字段必须根据现有代码合理调整。

不要机械照抄本提示词。

---

# 9. Source Anchor

Source Anchor 是 0.3 的核心。

目标：

> 任何检索结果都能够尽可能回到原始文档中的实际位置。

例如：

PDF：

```text
document
page=12
bbox=(x1,y1,x2,y2)
```

PPT：

```text
document
slide=8
shape=17
```

Excel：

```text
document
sheet="Revenue"
range="B12:F18"
```

Word：

```text
document
section
paragraph
```

HTML：

```text
heading path
dom-like logical anchor
```

如果某种格式无法提供精确 anchor：

使用 degrade strategy。

不得伪造不存在的位置。

---

# 10. Provenance 必须贯穿全链路

Document IR 的 provenance 必须一直传递到：

```text
Chunk
↓
Embedding
↓
Search Result
↓
Reranker
↓
Context
↓
Citation
↓
Agent
```

禁止出现：

> Parser 有 page 信息，但 Chunk 丢了。

或者：

> Chunk 有 page 信息，但 Search API 丢了。

或者：

> Search 有 anchor，但 Agent citation 不提供。

---

# 11. Phase 2 — PDF Structured Parsing

PDF 作为第一优先级。

建立真实复杂 PDF 测试集。

至少包括：

* 普通单栏 PDF
* 双栏 PDF
* 带目录 PDF
* 多级标题 PDF
* 大量表格 PDF
* 带图片/Caption PDF
* 扫描 PDF
* 中英文混合 PDF
* 页眉/页脚明显 PDF
* 100+ 页长 PDF

目标不仅是提取文字。

必须保留：

```text
page
block
reading order
heading hierarchy
table
figure/caption when available
bbox when available
```

---

# 12. Parser Backend 策略

不得把 Shutu-Knowledge 核心绑定到单个外部 parser。

建立：

```text
DocumentParser interface
```

例如：

```text
Parse(input) -> DocumentIR
```

可允许 Backend：

```text
Native
PDF.js
MinerU
Docling
future parser
```

但 0.3 不要求一次支持所有 backend。

优先：

> 把当前已经存在的 parser 能力标准化到 Document IR。

如果 MinerU 当前已经可用：

优先复用。

如果 Docling 有明显结构优势：

可以作为 optional backend 做技术验证。

但禁止因此重构整个 Runtime。

---

# 13. Phase 3 — Office Structured Parsing

## DOCX

保留：

* Heading hierarchy
* Paragraph
* List
* Table
* Caption if detectable
* Section
* reading order

---

## PPTX

这是重点格式。

至少保留：

```text
slide number
slide title
text block
shape order
table
image/figure reference
speaker notes if available
```

检索时必须能够知道：

> 这几个 Chunk 原本来自同一张 Slide。

---

## XLSX

不能简单把 Excel 当纯文本。

至少保留：

```text
sheet
used range
table-like regions
row/column position
cell value
formula when relevant
merged cells
header relationship
```

目标是回答类似：

> Revenue Sheet 中 2025 Q4 APAC 数值是多少？

系统能够知道：

* Sheet
* Row
* Column
* Header

而不是只有一堆字符串。

---

# 14. Legacy Office

`.doc/.ppt/.xls`

仍使用现有 Legacy Office runtime。

0.3 原则：

先转换成现代格式或统一 intermediate format，然后进入：

```text
Document IR
```

不要另外建立第二套 Legacy Knowledge Pipeline。

---

# 15. Phase 4 — Structure-Aware Chunking

当前 Chunking 不删除。

在现有 chunking 上增加：

# Structure-Aware Chunking

Chunk 不能再只根据字符数/token 数。

必须综合：

* heading boundary
* paragraph boundary
* page/slide/sheet
* table boundary
* figure/caption
* section hierarchy
* size limits

例如：

```text
Section
 ├─ Paragraph
 ├─ Paragraph
 ├─ Table
 └─ Paragraph
```

不应该随意切成：

```text
Chunk A = half paragraph + half table
Chunk B = remaining table + paragraph
```

---

# 16. Chunk 必须保留双重身份

每个 Chunk 应同时具有：

## Retrieval Unit

用于：

```text
BM25
Embedding
Rerank
```

## Structural Unit

知道自己：

```text
属于哪个 document
哪个 section
哪个 page/slide/sheet
哪个 parent node
前后 sibling 是谁
```

不要让 Retrieval Unit 破坏 Document Structure。

---

# 17. Parent/Child Context

增加结构化 Context Expansion。

例如某个搜索命中：

```text
Table Cell
```

最终上下文可以合理组合：

```text
Section Heading
+
Table Header
+
Matched Row
+
Caption
```

而不是简单：

```text
previous chunk + current chunk + next chunk
```

现有 sibling context 保留。

增加：

```text
structural context
```

但不要一次实现复杂 Graph Traversal Engine。

---

# 18. 表格是 0.3 的重点

Table 不应该只序列化成无结构文本。

Document IR 中必须保留基本：

```text
Table
Row
Cell
row_span
col_span
position
header relationship
```

索引层可以生成两类文本：

## Table Summary Representation

例如：

```text
Table: Regional Revenue
Columns: Region, Q1, Q2, Q3, Q4
```

## Row Representation

例如：

```text
Region=APAC
Q1=100
Q2=120
Q3=130
Q4=150
```

用于 Retrieval。

但 citation 必须仍然回到真实 table/source anchor。

---

# 19. 图片/图表范围

0.3 不要求实现完整视觉问答。

但必须建立 Figure 概念。

至少保留：

```text
figure id
page/slide
bbox
caption
nearby text
image reference
```

如果现有 parser 可以获得图像：

保留。

不要丢弃。

---

# 20. Optional Figure Understanding

如果现有 Runtime 能低成本调用本地/外部视觉模型：

可以设计可选：

```text
figure description
```

但必须：

* optional
* cached
* provenance marked
* 不作为 0.3 Release Blocker

0.3 核心仍是：

> 保存 Figure 结构。

不是一定理解所有图片。

---

# 21. Phase 5 — Document Outline

基于 Document IR 自动生成：

```text
Document Outline
```

必须优先使用 deterministic structure：

例如：

```text
1 Introduction
2 Architecture
  2.1 Storage
  2.2 Retrieval
3 Deployment
```

如果原始文档没有明确 heading：

允许使用 heuristic。

LLM enhancement 可以 optional。

不要让 LLM 成为基础 Outline 的强依赖。

---

# 22. Knowledge Understanding Layer v0

完成 Document IR 后，可以开始最小 Knowledge Understanding Layer。

本版本只实现基础能力：

```text
Document Summary
Section Summary
Key Concepts
```

不要做完整 LLM Wiki。

这些派生知识必须保存：

```text
derived_from
```

关系。

例如：

```text
Section Summary
    ↓ derived_from
Section nodes
```

保证可以追溯到原始 Evidence。

---

# 23. Derived Knowledge 不能替代 Evidence

建立严格原则：

```text
Derived Knowledge != Primary Evidence
```

LLM summary 可以帮助：

* routing
* understanding
* query expansion
* context selection

但最终回答中的 grounded citation：

必须尽量指向：

```text
original document evidence
```

而不是只引用 LLM Summary。

---

# 24. Search Integration

现有 Search Pipeline：

```text
BM25
+
Vector
↓
RRF
↓
Reranker
↓
Threshold
↓
MMR
↓
Context
```

继续保留。

0.3 增强：

```text
Query
↓
Document/Section routing
↓
Existing Hybrid Retrieval
↓
Structural Context Expansion
↓
Evidence
```

不要重写 BM25、Vector 或 RRF。

---

# 25. Metadata Filtering

Document IR 落地后支持结构过滤：

例如：

```text
document_id
page
section
slide
sheet
node_type
```

至少让内部 API 支持。

UI 是否全部暴露可以后续决定。

---

# 26. Query Example

系统最终应该能够更好回答：

```text
第 12 页的表格主要说明什么？
```

```text
PPT 中哪一页讨论 churn？
```

```text
Revenue Sheet 里 APAC Q4 是多少？
```

```text
架构章节对 Storage 有哪些要求？
```

```text
这张表和上一节结论是否一致？
```

而不仅是：

```text
检索包含关键词的 Chunk。
```

---

# 27. Citation v2

设计 Citation v2。

保持当前 Citation backward compatibility。

增强字段可以包括：

```text
document
document_id

page
slide
sheet

section

node_id
chunk_id

bbox
cell_range

snippet
```

API 必须允许老 Agent 继续使用旧字段。

---

# 28. Agent Integration

Shutu-Agent 不做大改。

Shutu-Knowledge Extension 可以逐步返回更丰富 Evidence。

例如：

```json
{
  "text": "...",
  "document": "...",
  "section": "...",
  "page": 12,
  "citation": {...}
}
```

Agent 不需要理解整个 Document IR。

只消费：

```text
Evidence + Structured Citation
```

这是重要边界。

---

# 29. Storage Strategy

继续使用 SQLite。

不要切数据库。

新增数据建议分层：

```text
documents
document_nodes
document_relationships
chunks
chunk_node_links
derived_knowledge
```

具体 schema 必须根据现有 schema 设计。

不要为了“模型漂亮”做过度 normalization。

---

# 30. Document IR Version

Document IR 必须具有：

```text
ir_version
```

例如：

```text
document-ir/v1
```

未来升级 Document IR 时：

不得让历史数据无法识别。

---

# 31. Parser Version

每个 Document 应记录：

```text
parser
parser_version
parse_config
```

这样 parser 升级后可以判断：

```text
needs reparse
```

而不是无条件全部重建。

---

# 32. Determinism

相同：

```text
file
parser version
parse config
```

应该尽量产生稳定：

```text
Node structure
Node ordering
Chunk structure
```

避免每次重新导入产生完全不同 Node ID。

建议研究 deterministic ID。

---

# 33. Node ID

Node ID 应避免随机 UUID 导致：

> 同一文档重新解析后所有节点都变成完全新对象。

考虑基于：

```text
document
logical position
node type
source anchor
```

生成稳定 ID。

但必须考虑：

文档修改后的 collision 和 version semantics。

先设计并测试，再实现。

---

# 34. Incremental Update

Document IR 必须与现有 generation semantics 协同。

文档更新：

```text
old generation
       ↓
parse new IR
       ↓
index new generation
       ↓
atomic visibility switch
       ↓
old generation cleanup
```

不得让 Search 同时看到：

```text
old IR + new chunks
```

或：

```text
new IR + old chunks
```

---

# 35. Delete Semantics

删除 Document 后：

必须清理/失效：

```text
Document IR
Nodes
Chunks
Embeddings
Derived Knowledge
Citations
Search visibility
```

不得留下 orphan derived knowledge。

---

# 36. Failure Recovery

解析大型 PDF 时如果崩溃：

不得留下半完成 IR 作为正式可见数据。

建议继续利用现有 durable operation / generation design。

不要另造第二套 transaction system。

---

# 37. Performance原则

Document Intelligence 不能让普通文档导入速度无限恶化。

建立阶段性能指标。

至少记录：

```text
parse time
IR build time
chunk time
embedding time
peak RSS
IR storage size
```

重点比较：

```text
0.2 baseline
vs
0.3 structured pipeline
```

不要求 0.3 完全一样快。

但明显数量级退化必须解释。

---

# 38. LLM 使用原则

0.3 可以使用 LLM 做：

* Section summary
* Document summary
* concept extraction
* weak heading inference
* optional figure understanding

但是：

## Parser Core

不能强依赖 LLM。

## Document IR

不能依赖 LLM 才能生成。

## Basic Retrieval

不能因为 LLM 不可用而失效。

LLM enhancement 应：

```text
optional
degradable
cached
```

---

# 39. Offline Principle

Shutu-Knowledge 的差异化之一是：

```text
Local / Offline
```

Document IR 和基本 Document Intelligence 必须在：

```text
offline
```

环境下可运行。

如果某个增强能力需要 Online LLM：

必须标记：

```text
optional
```

不能成为 Release Gate。

---

# 40. 真实 Golden Corpus

建立：

```text
testdata/document_intelligence/
```

或符合当前项目习惯的目录。

至少包含：

## PDF

* simple.pdf
* multicolumn.pdf
* tables.pdf
* figures.pdf
* scanned.pdf
* long.pdf

## Office

* structured.docx
* tables.docx
* presentation.pptx
* spreadsheet.xlsx

## Languages

* English
* Chinese
* Mixed

尽量使用：

* 可公开
* 可提交
* 小体积
* 稳定

的测试文件。

---

# 41. Golden IR Tests

针对固定测试文档验证：

例如：

```text
pages == 5
headings include X
table count == 3
slide count == 8
sheet names == [...]
```

不要只 snapshot 整个巨大 JSON。

应验证关键语义。

---

# 42. Golden Retrieval Tests

增加复杂文档查询。

每个 query 定义：

```text
question
expected document
expected section
expected page/slide/sheet
expected evidence type
```

例如：

```text
Question:
"What was APAC revenue in Q4?"

Expected:
document = financial.xlsx
sheet = Revenue
row = APAC
column = Q4
```

---

# 43. Citation Accuracy Tests

验证：

检索结果引用的位置确实包含相关内容。

重点：

* page mismatch
* wrong slide
* wrong sheet
* table row mismatch
* stale generation
* wrong document version

Citation correctness 是 0.3 的核心质量指标之一。

---

# 44. Regression

必须保证 0.2 能力不倒退。

包括：

* plain TXT
* Markdown
* simple PDF
* current hybrid retrieval
* reranking
* MMR
* OCR
* runtime
* Agent search
* document delete
* reindex
* restart

---

# 45. UI 范围

UI 只做必要增强。

建议增加：

## Document Structure Viewer

可以看到：

```text
Document
├─ Section
├─ Page
├─ Table
└─ Figure
```

## Search Result Provenance

显示：

```text
Document
Section
Page/Slide/Sheet
```

如果已有 document preview：

增强 anchor 定位。

不要做大规模 UI redesign。

---

# 46. Debug View

建议增加开发模式：

```text
Document IR Inspector
```

用于查看：

* Node tree
* source anchor
* text
* parent
* children
* reading order
* Chunk links

这对 0.3 开发非常重要。

但可以是：

```text
developer/debug feature
```

无需做成漂亮产品页面。

---

# 47. Doctor

Doctor 增加：

```text
Document Parser
Document IR
Structured Index
```

状态。

不要把 optional LLM enrichment 不可用标为整体 ERROR。

区分：

```text
CORE READY
ENRICHMENT UNAVAILABLE
```

---

# 48. Telemetry / Diagnostics

本地记录：

```text
parser selected
parse duration
node counts
table count
figure count
chunk count
fallback path
```

不要上传隐私数据。

主要用于本地诊断。

---

# 49. Parser Fallback

例如 PDF：

```text
Native Structured Parser
        ↓ fail
MinerU
        ↓ fail
OCR
```

具体顺序根据现有实现确定。

但最终所有路径必须输出：

```text
Document IR
```

而不是各自产生完全不同的数据模型。

---

# 50. Confidence

对于 OCR / heuristic heading / inferred reading order：

可记录：

```text
confidence
```

但不要设计复杂 probability framework。

主要用于未来 debug 和 fallback。

---

# 51. Document Understanding v0

在 Document IR 稳定之后，实现最小派生知识。

建议：

```text
DocumentSummary
SectionSummary
KeyConcept
```

每一个必须记录：

```text
source_node_ids
model
model_version
generated_at
```

如果 Knowledge 内容变更：

必须 invalidated/rebuilt。

---

# 52. 不要提前做 Entity Graph

即使已经抽取：

```text
KeyConcept
```

也暂时不要扩展成：

```text
Concept
→ Entity
→ Relation
→ Graph DB
```

0.3 只需要建立未来可以扩展的数据边界。

---

# 53. Query Routing v0

可以增加一个轻量 routing：

例如用户问：

```text
“整个文档讲了什么？”
```

优先：

```text
Document Summary
```

用户问：

```text
“第12页表格中……”
```

优先：

```text
structured evidence
```

用户问普通事实：

继续：

```text
hybrid retrieval
```

不要实现复杂 Agent Planner。

---

# 54. 质量目标

0.3 Release 必须证明以下几件事。

## A

结构信息没有在 parser → chunk → retrieval → citation 过程中丢失。

## B

复杂 PDF / PPT / Excel 查询比 0.2 明显更可靠。

## C

普通 RAG 查询没有明显退化。

## D

0.2 KB 可以正常升级。

## E

Shutu-Agent 不需要重大修改。

---

# 55. 0.3 Release Gate

建议正式 Release Gate：

```text
Build                         PASS
Go Test                       PASS
Race                          PASS
Web Build                     PASS

0.2 Migration                 PASS

PDF Structured Parsing        PASS
DOCX Structured Parsing       PASS
PPTX Structured Parsing       PASS
XLSX Structured Parsing       PASS

Document IR Golden Tests      PASS

Structure-aware Chunking      PASS

Structured Citation           PASS

Golden Retrieval              PASS
Citation Accuracy             PASS

Update/Delete/Reindex         PASS

Restart Recovery              PASS

Agent Integration             PASS

Windows Package Smoke         PASS

No P0                         PASS
```

---

# 56. 非 Release Blocker

以下不作为 0.3 blocker：

```text
Linux full release package
macOS full release package
million-document scale
GraphRAG
Knowledge Graph
full LLM Wiki
Visual Question Answering
advanced chart understanding
distributed parsing
multi-user deployment
```

---

# 57. 分阶段实施

不要一次性提交巨大改动。

建议实施顺序：

## PH0

Audit + Design

输出：

```text
docs/document_ir_design.md
docs/0.3_gap_analysis.md
```

---

## PH1

Document IR Core

完成：

```text
schema
storage
migration
versioning
source anchors
```

---

## PH2

PDF → IR

先把 PDF 做正确。

---

## PH3

DOCX / PPTX / XLSX → IR

---

## PH4

Structure-Aware Chunking

---

## PH5

Structured Retrieval + Citation v2

---

## PH6

Agent Integration

---

## PH7

Document Understanding v0

只做：

```text
summary
section summary
key concepts
```

---

## PH8

Golden Corpus / Regression / Performance

---

## PH9

Windows Packaging / Release

---

# 58. 每个 Phase 的规则

每个 Phase 必须：

1. 先审计现状。
2. 给出最小设计。
3. 实现。
4. 加测试。
5. `go test`.
6. `go test -race`.
7. Web 验证。
8. 文档更新。
9. Commit。

禁止积累大量未提交改动后一次提交。

---

# 59. Stop Condition

如果某个阶段发现必须：

* 重写 Storage
* 修改 Shutu-Agent 核心架构
* 引入重型分布式组件
* 推翻 0.2 Retrieval
* 推翻现有 Runtime

暂停扩大实现。

在：

```text
docs/0.3_architecture_issue.md
```

记录：

* 问题
* 证据
* 为什么现有边界不够
* 最小替代方案

优先寻找兼容方案。

---

# 60. Token / Scope 控制

0.2 最终 Release Gate 曾出现一次约：

```text
282k tokens
```

的大任务。

0.3 必须避免 Agent 无限制全面重审。

每个 Phase：

只读取与当前任务相关代码。

不要每个阶段重复：

* 全仓 architecture audit
* 全量历史文档重读
* 全量 Git history 分析

已有结论如果没有新证据：

直接复用。

---

# 61. 不允许“为了完整而完整”

每增加一个字段、Node type、抽象或接口，都必须回答：

> 它实际改善了什么真实文档理解场景？

如果答案只是：

```text
未来可能有用
```

则不要实现。

---

# 62. 0.3 最终验收示例

Release Candidate 至少现场证明：

## PDF

问：

```text
第 12 页表格主要说明什么？
```

系统返回：

```text
正确内容
+
page=12
+
table provenance
```

---

## PowerPoint

问：

```text
哪一页讨论了用户流失？
```

系统返回：

```text
slide=N
+
slide title
+
relevant evidence
```

---

## Excel

问：

```text
Revenue Sheet 中 APAC Q4 收入是多少？
```

系统返回：

```text
正确 cell/row
+
sheet
+
range provenance
```

---

## Long document

问：

```text
Storage 章节有哪些核心约束？
```

系统能够利用：

```text
heading hierarchy
+
section context
+
retrieved evidence
```

回答。

---

# 63. Release 目标

0.3 成功的标准不是：

> 支持更多文件扩展名。

而是：

> 相同文件导入以后，Shutu-Knowledge 对文档“知道得更多”。

0.2：

```text
I can retrieve relevant text.
```

0.3：

```text
I know where the text came from,
what structure it belongs to,
and how nearby evidence is related.
```

这才是版本价值。

---

# 64. 0.4 预留方向

0.3 完成以后再考虑：

```text
Knowledge Understanding Layer v1
LLM Wiki
Cross-document concepts
Entity relationship
GraphRAG
Knowledge Graph
Long-term knowledge compilation
```

不要提前进入。

---

# 65. 最终交付报告

最终必须输出：

## Version

```text
0.3.x
```

## Final Commit

完整 SHA。

## Architecture

Document IR 最终结构简述。

## Migration

0.2 → 0.3：

```text
PASS / FAIL
```

## Parser

分别：

```text
PDF
DOCX
PPTX
XLSX
Legacy Office
```

状态。

## Golden Tests

列出：

```text
Document IR
Retrieval
Citation
Update
Delete
Restart
Agent
```

PASS / FAIL。

## Regression

确认 0.2 基础能力是否全部保持。

## Performance

至少提供：

```text
parse
index
search
memory
```

关键结果。

## Release Artifact

```text
filename
size
SHA-256
```

## CI

```text
push CI
tag CI
```

## Known Limitations

只列真实限制。

## Deferred

明确 0.4+ 内容。

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

# 66. 最终执行原则

整个 0.3 开发始终遵守：

```text
Structure before intelligence.

Evidence before summary.

Deterministic parsing before LLM enrichment.

Compatibility before redesign.

Real document quality before feature count.
```

最终目标：

> 把 Shutu-Knowledge 从一个成熟的 Local RAG Engine，推进为具备结构化文档理解能力的 Agent Knowledge System。

不要把 0.3 做成另一次基础设施重构。

不要追求“大而全”。

集中完成：

# Document IR

# Structured Parsing

# Structure-Aware Chunking

# Structured Citation

# Document Understanding v0

做到这些以后停止扩 Scope，进入稳定化与 Release。
