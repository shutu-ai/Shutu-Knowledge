# Shutu-Knowledge 检索质量专项审计与修复

请对当前 `Shutu-Knowledge` 仓库进行一次**基于真实检索案例、证据驱动的 RAG 检索质量专项审计**。

目标不是表面调参，也不是为了让下面几个测试用例“硬编码通过”，而是确认当前知识库从：

**文档解析 → 结构识别 → Chunking → 索引 → BM25 → Vector → Hybrid/RRF → MMR → Rerank → Final Context**

整个链路是否设计正确，并修复能够泛化到不同知识库、不同技术文档的根因问题。

---

# 一、重要架构约束

1. `Shutu-Knowledge` 单向依赖 `Shutu-Agent`。
2. **不要修改 Shutu-Agent。**
3. 如果确认某项能力必须由 Agent 基座提供：
   - 不要直接修改 Agent；
   - 单独生成 Agent 改进需求文档；
   - 明确接口、能力、原因、兼容性要求。
4. 不允许针对 `GDE`、`Code Agent`、`AI4App` 等测试词写 hardcode。
5. 修复必须能够泛化到：
   - 产品手册
   - 技术规范
   - API文档
   - 运维手册
   - 软件版本文档
   - 3GPP/Telecom文档
   - 企业制度与流程文档
6. 保持已有公开接口、数据和已有用户知识库兼容，除非确有必要进行 migration。
7. 优先修复根因，不允许仅通过提高 TopK 或扩大候选池掩盖问题。

---

# 二、真实问题背景

当前导入知识库：

`GDE 26.3.0`

包含：

- GDE产品描述
- Code Agent编排指南
- 快速入门
- 应用开发概述
- API Fabric功能描述
- 数据治理
- 系统规格指标
- 等大量 PDF 文档

检索测试发现：

## Case A：MMR 开启

Query：

`26.3版本中Code Agent有哪些能力？`

最终上下文 Top4 曾返回：

1. GDE产品描述
2. 快速入门
3. 应用开发概述
4. API Fabric功能描述

而真正包含答案的：

`GDE_26.3.0_Code Agent编排指南.pdf`

没有进入最终上下文。

这是明显的 Retrieval Failure。

---

## Case B：关闭 MMR 后

相同 Query：

`26.3版本中Code Agent有哪些能力？`

Top4 全部来自：

`GDE_26.3.0_Code Agent编排指南.pdf`

其中正确答案所在 Chunk 能够召回。

因此初步证据表明：

**基础 Recall/Hybrid Retrieval 并未完全失效，MMR 很可能在当前实现或参数下严重破坏 relevance。**

---

# 三、Golden Regression Cases

请将下面测试正式加入检索质量回归测试。

## GDE-RAG-001

Query：

`Code Agent有哪些能力？`

Expected：

- 正确文档：
  `GDE_26.3.0_Code Agent编排指南.pdf`
- Answer-bearing Chunk 应尽量进入 Top1
- 必须能够召回：
  - AI4App
  - AI4Model
  - AI4UI
  - AI4Script
  - AI4Flow
  - AI4Assist

当前情况：

- 正确文档能够召回
- 真正直接回答问题的 Chunk 大约排 #3
- 属于 Recall PASS / Ranking 不理想

---

## GDE-RAG-002

Query：

`Code Agent整体规划了哪些能力？`

Expected：

Top1/Top2 应包含：

- AI4App
- AI4Model
- AI4UI
- AI4Script
- AI4Flow
- AI4Assist

当前表现较好。

必须作为 regression，避免后续优化导致退化。

---

## GDE-RAG-003

Query：

`AI4App、AI4Model、AI4UI、AI4Script、AI4Flow、AI4Assist分别是什么？`

Expected Top1：

包含六种能力及解释：

- AI4App：辅助应用开发
- AI4Model：辅助模型编排
- AI4UI：辅助UI编排
- AI4Script：辅助脚本编排
- AI4Flow：辅助流程/服务编排
- AI4Assist：智能知识问答

当前表现很好。

必须保持。

---

## GDE-RAG-004

Query：

`26.3版本中Code Agent有哪些能力？`

Expected：

- 正确版本：26.3
- 正确文档：Code Agent编排指南
- Answer-bearing Chunk 应进入 Top1 或至少 Top2

MMR OFF 当前：

- 正确文档 4/4
- 真正答案大约 #3

MMR ON 曾出现：

- 正确文档 0/4

重点作为 MMR regression case。

---

# 四、当前已观察到的 Chunking 异常

目前某些 Chunk 明显跨越多个语义章节。

例如一个 Chunk 中同时出现：

- 服务理解
- 模型理解
- 页面理解
- 大屏理解
- 流程泳道图
- 图片要求
- 节点规范
- 操作步骤
- 流程反例
- 1.2 Code Agent辅助编排
- 1.2.1 关于Code Agent辅助编排
- Code Agent整体能力

也就是说：

一个 Chunk 横跨：

`1.1.4.x → 1.2.x`

甚至多个完全不同主题。

这会导致：

- Embedding semantic dilution
- Reranker 输入污染
- 长 Chunk 截断
- Answer-bearing 内容位于 Chunk 尾部
- BM25/Vector score 不稳定
- MMR 对相邻 Chunk 的相似性判断失真

请重点检查。

---

# 五、专项审计任务

## 1. PDF/Text Extraction

检查：

- PDF 文本提取链
- Unicode 处理
- 字体映射
- UTF-8 边界
- 页面拼接
- 表格解析
- 标题识别
- 页眉页脚去除
- OCR fallback
- Chunk 切割是否可能截断 UTF-8 字符

真实结果中已经发现：

`��`

以及类似：

`大�`

等乱码。

请定位：

- 乱码来自 PDF parser
- 文本 normalization
- 数据库写入
- Chunk slicing
- UI显示
- 还是 UTF-8 byte/rune 切分错误

不得仅做 replace "�"。

必须找到根因。

---

# 六、结构化 Chunking 审计

重点检查当前 Chunker 是否主要基于：

- 固定字符数
- 固定 token 数
- 固定页数
- overlap

而缺少：

- Heading-aware
- Section-aware
- Paragraph-aware
- List-aware
- Table-aware
- Semantic boundary

对于技术手册，期望优先使用：

`Document Structure → Heading → Subheading → Paragraph`

而不是纯固定 token 切分。

例如：

```text
1.2 Code Agent辅助编排
  1.2.1 关于Code Agent辅助编排
    场景介绍
    能力介绍
```

应优先形成一个语义完整 Chunk。

如果内容太长，再在该 Section 内继续切分。

不应让：

`1.1.4.6 如何绘制流程泳道图`

和：

`1.2.1 关于Code Agent辅助编排`

大量混入同一个 Chunk。

---

# 七、Chunk Metadata 审计

检查当前 Chunk 是否保存足够 metadata。

建议至少具备：

- baseId
- documentId
- documentTitle
- documentVersion
- sectionPath
- heading1
- heading2
- heading3
- pageStart
- pageEnd
- chunkIndex

确认目前 UI 显示的：

`Code Agent编排指南.pdf — 需求列表`

是否因为 section title 没有随着文档结构正确更新。

例如实际 Chunk 已经进入：

`1.2.1 关于Code Agent辅助编排`

但 metadata 仍然显示：

`需求列表`

如果属实，需要修复 section lineage。

---

# 八、Embedding 输入设计

审计目前 Embedding 输入到底是：

```text
正文
```

还是：

```text
文档标题
章节标题
正文
```

建议评估：

```text
[Document]
Code Agent编排指南

[Section]
Code Agent辅助编排 > 关于Code Agent辅助编排

[Content]
...
```

作为 embedding representation。

但是：

- metadata enrichment 不应污染用户最终看到的正文
- 不允许无限重复文档名
- 不要让 common metadata（GDE、26.3）获得过强权重

请通过 benchmark 判断，而不是凭感觉修改。

---

# 九、BM25 / Lexical Retrieval

验证 BM25 是否真实工作。

针对：

`Code Agent有哪些能力？`

打印或测试：

BM25 TopN。

确认包含：

`Code Agent`

的 Chunk 是否获得明显 lexical advantage。

特别检查：

- 中英文混合 tokenizer
- `Code Agent`
- `AI4App`
- `AI4Flow`
- `DataPipeline`
- `DataMold`
- `API Fabric`

这种英文产品/模块名称是否被正确索引。

检查 tokenizer 是否把：

`Code Agent`

正确作为可检索 lexical terms。

---

# 十、Vector Retrieval

检查：

- query embedding
- document embedding
- normalization
- cosine/dot-product 使用是否匹配模型
- vector dimensions
- distance / similarity 方向
- score normalization

请确认没有：

- distance 越小越相关却按越大排序
- cosine 未 normalize
- model mismatch
- query/document prompt 不匹配

等问题。

不要因为当前测试基本可用就跳过基本正确性审计。

---

# 十一、Hybrid / RRF

检查：

BM25 + Vector → RRF

实现是否正确。

需要输出测试诊断：

```text
Query

BM25 Top20
Vector Top20
RRF Top20
Rerank TopN
MMR Input
MMR Output
Final Context
```

至少为 Golden Cases 提供可重复诊断模式。

检查：

- RRF constant
- rank starting position
- duplicate candidate merge
- chunk identity
- document identity
- BM25/Vector candidate pool size
- score是否被错误混合

---

# 十二、Reranker

重点确认：

1. Reranker 是否实际运行，而不是配置存在但执行路径未调用。
2. Query/Document 输入顺序是否正确。
3. Score direction 是否正确。
4. Long Chunk 是否被 tokenizer 截断。
5. 如果被截断：
   - 是否答案恰好位于 Chunk 尾部而被丢失。
6. 是否支持中文 + 英文技术词混合文本。
7. 是否存在 batch ordering bug。
8. reranker failure 是否静默 fallback。

针对：

`Code Agent有哪些能力？`

请确认真正含答案的 Chunk 为什么不是 Top1。

必须有可观测证据。

---

# 十三、MMR 专项审计

这是本次最高优先级问题之一。

当前事实：

MMR ON：

`26.3版本中Code Agent有哪些能力？`

曾出现正确文档 0/4。

MMR OFF：

正确文档 4/4。

请完整检查：

## 13.1 MMR Formula

是否标准实现类似：

```text
MMR =
lambda * relevance(query, candidate)
-
(1-lambda) * maxSimilarity(candidate, selected)
```

确认：

- relevance score 方向
- candidate similarity 方向
- lambda意义是否实现反了
- similarity 是否 normalize
- MMR 输入是否使用正确 embedding

---

## 13.2 MMR Placement

确认 MMR 当前位于：

```text
Recall
→ RRF
→ MMR
→ Rerank
```

还是：

```text
Recall
→ RRF
→ Rerank
→ MMR
```

请评估哪种更合理。

建议重点评估：

```text
Recall
→ Hybrid/RRF
→ Rerank
→ Relevance Floor
→ Optional MMR
→ Final Context
```

即：

**先保证结果相关，再在高相关结果之间去重。**

---

## 13.3 Relevance Floor

MMR 不允许为了 diversity 把明显低相关结果拉入 Final Context。

例如：

```text
CodeAgent 0.92
CodeAgent 0.90
CodeAgent 0.88
CodeAgent 0.86
API Fabric 0.55
数据治理 0.50
```

MMR 不应为了“多样性”选择 0.50 的结果。

请考虑实现：

- absolute minimum relevance
或
- relative-to-best relevance floor

例如：

```text
candidateScore >= maxScore * threshold
```

具体策略必须 benchmark 后决定。

---

## 13.4 Document Diversity

不要默认认为：

“来自不同文档 = 更好”。

企业知识库中：

同一本正确文档的多个相关 Chunk

通常比：

来自四本不同但弱相关文档

更有价值。

MMR 应主要解决：

**semantic duplicate chunks**

而不是强制：

**document diversity**

---

## 13.5 默认值

在问题完全解决并有 benchmark 证明前：

建议评估是否将：

`MMR default = OFF`

保留为安全默认。

不要为了功能存在而默认开启。

---

# 十四、Final Context Filtering

检查当前是否存在：

```text
TopK = 4
```

然后无论质量如何都必须返回 4 条。

如果是，需要调整设计。

正确语义应更接近：

```text
MaxTopK = 4
```

不是：

```text
ExactTopK = 4
```

Final Context 应允许：

- 4条
- 3条
- 2条
- 1条
- 0条

如果只有一个相关 Chunk：

返回 1 条比硬凑 4 条更好。

如果无可靠答案：

返回 0 条，并让上层知道：

`insufficient evidence`

而不是强行给 LLM 填充噪声。

---

# 十五、版本信息

当前文档文件名均类似：

```text
GDE_26.3.0_xxx.pdf
```

本次先不要过度重构 version-aware knowledge system，但请审计：

- version 是否仅存在文件名
- 是否已经有 metadata
- Query 中 `26.3` 是否作为普通全文关键词参与排序
- 是否可能污染 relevance

如当前缺乏 version metadata：

请生成独立设计建议，不要求此次必须完成完整版本管理。

未来建议支持：

```text
product
documentFamily
version
status=current/superseded
supersedes
supersededBy
```

但不要让这个长期需求阻塞当前检索质量修复。

---

# 十六、PDF 结构继承

重点检查：

- 一个标题出现后
- 后续段落/表格
- 跨页内容

是否能够正确继承 section path。

例如：

```text
1.2
  1.2.1
```

下一页正文仍应知道：

```text
sectionPath =
1.2 Code Agent辅助编排
>
1.2.1 关于Code Agent辅助编排
```

不要因为分页失去上下文。

---

# 十七、测试指标

至少增加以下指标：

## Retrieval

- Document Recall@1
- Document Recall@3
- Chunk Recall@1
- Chunk Recall@3
- MRR

## Answer-bearing Retrieval

重点增加：

`Answer-bearing Chunk @1`

这是本轮最重要指标。

不是仅看：

“是否命中正确 PDF”。

而是：

“真正支持答案的 Chunk 是否排在第一”。

---

## Noise

增加：

- irrelevant context rate
- duplicate context rate

例如：

4个 Final Context 中：

```text
4/4相关 → 优秀
3/4相关 → 可接受
2/4相关 → 较差
1/4相关 → 严重
0/4相关 → FAIL
```

---

# 十八、无答案测试

增加至少以下 negative tests：

```text
GDE如何配置5G AMF Registration Reject？
GDE是否支持量子计算调度？
```

如果知识库没有相关内容：

Final Context 不应硬凑 4 条。

目标：

```text
0 high-confidence evidence
```

而不是返回：

数据治理、API Fabric、系统规格等无关内容。

---

# 十九、可观测性

增加 Debug / Diagnostic Mode。

对于一个 Query，可以输出：

```text
Query
Normalized Query

Scope/Base IDs

BM25 candidates
Vector candidates

RRF results

Rerank scores

MMR before
MMR after

Final threshold

Final context
```

每个 Chunk 显示：

```text
document
chunkId
sectionPath

bm25Rank
bm25Score

vectorRank
vectorScore

rrfScore

rerankScore

mmrScore

finalRank
```

要求：

- 默认用户界面不显示这些内部信息
- Debug模式可以查看
- regression tests 可以读取结构化 diagnostics

---

# 二十、修复顺序

严格按照：

## Phase 1：Audit

不要修改代码。

先完成：

- 当前数据流
- Chunking算法
- Retrieval Pipeline
- MMR位置
- Reranker位置
- Final Context逻辑
- PDF parser

形成根因报告。

---

## Phase 2：Instrument

增加最低必要 diagnostics。

复现 Golden Cases。

形成：

```text
BM25
Vector
RRF
Rerank
MMR
Final
```

逐阶段证据。

---

## Phase 3：Fix root causes

优先级：

### P0

MMR relevance destruction

### P0

Chunk 跨章节/语义污染

### P1

Answer-bearing Chunk ranking

### P1

Final Context relevance threshold

### P1

UTF-8/PDF乱码

### P2

metadata/section hierarchy

### P2

version-aware metadata design

---

# 二十一、验收条件

最终必须满足：

## Golden Tests

GDE-RAG-001：

Answer-bearing Chunk @3 = PASS  
目标 @1

GDE-RAG-002：

Answer-bearing Chunk @1 = PASS

GDE-RAG-003：

Answer-bearing Chunk @1 = PASS

GDE-RAG-004：

Answer-bearing Chunk @3 = PASS  
目标 @1

---

## MMR Regression

MMR 开启后：

不得出现：

```text
MMR OFF → 正确文档 4/4
MMR ON  → 正确文档 0/4
```

如果 MMR 无法在不损失 relevance 的情况下稳定工作：

保持默认 OFF。

---

## Negative Retrieval

无答案问题：

不得为了 TopK 返回明显无关证据。

---

## Chunking

禁止出现：

一个普通 Chunk 横跨多个一级/二级主题，并把多个完全不同业务语义混在一起。

允许 overlap，但 overlap 必须是局部连续上下文，而不是跨章节混合。

---

## Compatibility

必须通过：

- go test ./...
- go vet ./...
- race tests（如项目已有）
- Web typecheck
- Web build
- Existing E2E
- Existing knowledge import/rebuild tests
- fresh clone build/test

不得破坏现有知识库。

---

# 二十二、最终交付

完成后不要只回复“已修复”。

必须给出：

## 1. Root Cause Report

按照：

```text
问题
证据
根因
影响
修复
验证
```

逐项列出。

---

## 2. Before / After

至少提供：

```text
GDE-RAG-001
GDE-RAG-002
GDE-RAG-003
GDE-RAG-004
```

优化前后排名对比。

---

## 3. Retrieval Pipeline

明确最终真实链路：

```text
Query
↓
Scope
↓
BM25 + Vector
↓
RRF
↓
Rerank
↓
Relevance Filtering
↓
Optional MMR
↓
Final Context
```

如果实际设计不同，说明原因。

---

## 4. Chunking Specification

明确：

- heading 优先级
- 最大 token
- overlap
- paragraph split
- table处理
- heading inheritance
- metadata
- embedding representation

---

## 5. MMR Specification

明确：

- 默认开关
- lambda
- candidate pool
- relevance floor
- 使用位置
- duplicate策略

---

## 6. Test Report

列出：

- 所有新增测试
- benchmark
- Golden Cases
- Negative Cases
- PASS / FAIL

---

## 7. Remaining Risks

明确还有哪些问题此次没有解决。

不要隐藏风险。

---

# 最重要的原则

本次优化目标不是：

> “让搜索结果更丰富”。

而是：

> **让真正能够支持答案的知识优先进入上下文。**

遵循优先级：

```text
Correctness
>
Relevance
>
Evidence quality
>
Recall
>
Diversity
```

而不是：

```text
Diversity
>
Relevance
```

特别是企业知识库：

**多个来自同一本正确手册的高相关 Chunk，是正常且合理的。**

不要因为 MMR 或“多文档多样性”而故意替换成弱相关知识。

先完成审计和 diagnostics，根据实际证据确认根因，再实施修复。