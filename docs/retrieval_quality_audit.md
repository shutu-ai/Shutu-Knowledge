# Shutu-Knowledge 检索质量专项审计报告

审计日期：2026-09-08

## 结论

已修复两个会泛化影响技术文档检索的根因：服务层 MMR 在 rerank 前使用统一 relevance=1，导致 diversity 可以替换真实答案；文本窗口和 reranker 输入使用 byte offset，中文 UTF-8 边界可能被截断。PDF 的替换字符现在会被识别为不健康文本并触发已有 OCR/重建链，而不是静默当作正常文本。

仓库中没有随代码提交的 GDE 26.3.0 PDF 或知识库数据库，因此不能声称已经对用户现场库重新导入并取得线上排名。本报告把任务提示中的现场观察作为 Before，把本地可复现的 GDE 词汇合成回归作为 After。

## Root Cause Report

### 1. MMR 破坏 relevance

| 项目 | 证据 |
|---|---|
| 问题 | MMR ON 曾使 Code Agent 正确文档从 4/4 变为 0/4。 |
| 根因 | `internal/knowledge/search.go` 在 rerank 前构造 `RankedHit{Score: 1}`；所有候选 relevance 相同，MMR 实际只按 embedding 相似度去重。 |
| 影响 | 相邻高相关 chunk 和答案 chunk 会被“多样性”替换；最终 TopK 还可能包含弱候选。 |
| 修复 | 顺序改为 Recall → RRF → Rerank → relevance floor → optional MMR → Final；MMR 使用真实 rerank/vector/fusion relevance，启用时采用 best 的 60% 相对 floor；默认仍 OFF。 |
| 验证 | `TestMMRUsesRelevanceAfterRerank`、`TestMMRRelevanceFloorOrdering`、GDE-RAG-004 回归通过。 |

### 2. Final Context 硬凑 TopK

| 项目 | 证据 |
|---|---|
| 问题 | 旧逻辑只在 rerank/vector 或显式阈值时过滤，hybrid 默认按融合顺序填到 TopK。 |
| 根因 | 没有统一的最终 relevance gate。 |
| 影响 | vector-only 弱证据会填充上下文，降低“无可靠答案时返回 0 条”的能力。 |
| 修复 | vector 默认使用 0.35 保守 floor；hybrid 中没有 lexical evidence 的候选也必须达到 0.35；显式 threshold 仍优先。过滤后允许少于 TopK。 |
| 验证 | `TestNegativeRetrievalDoesNotFabricateLexicalContext`、`TestNegativeVectorRetrievalDoesNotFillTopK` 通过。 |

### 3. Chunk 跨主题与 UTF-8 截断风险

| 项目 | 证据 |
|---|---|
| 问题 | PDF 文本常丢失 Markdown 标记；原 chunker 只会把 Markdown heading 当边界。窗口大小和 fallback/clip 使用 byte slicing。 |
| 根因 | numbered heading（如 `1.2 Code Agent`）无法建立 section boundary；CJK 的 `len(string)` 是字节数，而 `EstimateTokens` 是 rune 数。 |
| 影响 | 一个 PDF 长段落可能跨 section；中文窗口或 reranker 输入可能产生非法 UTF-8，UI 显示为 `�`。 |
| 修复 | 增加 numbered-PDF heading 识别、保留 heading lineage；chunk window、fallback 和 reranker clip 全部按 rune 切分；`CharsPerToken` 改为 rune 计数。 |
| 验证 | `TestChunkRecognizesNumberedPDFHeadings`、`TestChunkNeverSplitsUTF8` 通过。 |

### 4. PDF 乱码误判为健康文本

| 项目 | 证据 |
|---|---|
| 问题 | `��`/`大�` 是合法 UTF-8 的 U+FFFD，不会被 `utf8.Valid` 发现。 |
| 根因 | PDF parser 只检查平均行长，未检查 replacement rune；字体 ToUnicode 缺失时会静默保留乱码。 |
| 影响 | 乱码正文会进入索引、embedding 和最终上下文。 |
| 修复 | parser 检查 U+FFFD；优先尝试 layout reconstruction，仍不健康则返回 `NeedsOCR`，交给既有 OCR/content fallback；不做字符串 replace。 |
| 验证 | `TestPDFReplacementRuneIsMarkedUnhealthy` 及 parser 全套测试通过。 |

## Before / After

| Case | Before（任务现场观察） | After（本地合成回归） |
|---|---|---|
| GDE-RAG-001 | 正确文档可召回，answer-bearing chunk 约 #3 | Code Agent 指南 answer-bearing chunk #1 |
| GDE-RAG-002 | 表现较好 | answer-bearing chunk #1 |
| GDE-RAG-003 | 表现很好 | 六种能力解释 chunk #1 |
| GDE-RAG-004 | MMR OFF 正确文档 4/4、答案约 #3；MMR ON 正确文档 0/4 | MMR 开启后 answer-bearing chunk #1，且弱候选不能越过 floor |

这些 After 结果由 `internal/knowledge/retrieval_quality_test.go` 的正式回归断言；由于没有现场 GDE 数据，未把合成结果冒充现场 PDF 指标。

## 最终 Retrieval Pipeline

```text
Query + variants
  ↓
Scope / document filters
  ↓
BM25 lexical + normalized vector cosine
  ↓
weighted RRF (k=60)
  ↓
optional rerank (query/document order preserved, 352-token rune-safe input)
  ↓
relevance filtering (explicit threshold or conservative vector floor)
  ↓
optional MMR (post-rerank, relative floor, no forced document diversity)
  ↓
Final Context (MaxTopK, may be fewer than TopK)
```

`debug: true` 时，SearchResult 增加 `diagnostics`，包括 `bm25`、`vector`、`rrf`、`rerank`、`mmrInput`、`mmrOutput` 和 `final`。每条记录包含 document、chunkId、sectionPath、BM25/vector rank and score、RRF、rerank、MMR 和 final rank；默认响应不包含该字段。

## Chunking Specification

- Markdown heading 和 PDF numbered heading 优先建立 section boundary；heading path 继承到后续 paragraph/list/code block。
- 默认预算仍为 800 tokens，overlap 仍为 100 tokens；超长 block 只在自身 section 内按 scored boundary、句号或换行窗口化。
- paragraph、list 和 fenced code block 是结构边界；没有专用表格 AST，PDF 表格仍依赖原生文本/layout/OCR 结果，这是剩余风险。
- `Heading` 是当前持久化的 section path，`Context`/`EmbeddingText` 只用于检索，用户正文仍是 `Text`。
- embedding representation 为 `document title + section path + chunk text`，标题和章节不会重复写入用户正文。
- 当前 chunk schema 尚未持久化 pageStart/pageEnd、heading1/2/3 和独立 documentVersion；详见版本设计建议。

## MMR Specification

- 默认开关：OFF；用户和配置均需显式启用。
- lambda：配置 `mmrDiversity`，默认 0.75；其含义是 relevance 权重。
- candidate pool：已有 `TopK*3`、最小 12 的 recall pool。
- relevance floor：启用 MMR 时至少为最高 relevance 的 60%，显式 threshold 更高时采用显式值。
- 位置：rerank 后、Final Context 前。
- duplicate 策略：使用候选 embedding 与已选候选的最大 cosine similarity；不强制跨文档，正确文档的多个高相关 chunk 可以共同入选。

## Test Report

- PASS：`go test ./...`
- PASS：`go vet ./...`
- PASS：`go test -race ./...`
- PASS：`go test ./internal/knowledge -run '^$' -bench Benchmark -benchtime=1x`；EndToEndRAG 15.71 ms/op（单次 smoke，机器相关）。
- PASS：`cd web; npm run typecheck`
- PASS：`cd web; npm run build`
- PASS：`cd web; npm test`
- PASS：`cd web; npm run test:e2e`，Chrome/CDP lifecycle passed。
- PASS：新增 GDE-RAG-001..004、BM25 技术词 tokenizer、MMR、negative retrieval、numbered heading、UTF-8 和 PDF replacement-rune 回归。

## Remaining Risks

1. 现场 GDE PDF 未在仓库中，尚未完成真实库的重新导入、Top20 阶段诊断和真实 reranker 分布 benchmark；交付前应在现场库运行 `debug:true` 并保存 JSON。
2. PDF 原生文本中的字体映射、复杂表格、页眉页脚和跨页表格仍依赖 `ledongthuc/pdf` 的输出与已有 OCR fallback；本次未引入新的 PDF AST/table extractor。
3. page range、documentVersion/current-superseded lineage 尚未迁移到 chunks/documents schema；长期设计见独立文档。
4. 0.35 vector floor 是跨模型的保守默认，不等价于所有 embedding 模型的校准概率；生产环境应以真实 negative benchmark 调整显式 threshold。
