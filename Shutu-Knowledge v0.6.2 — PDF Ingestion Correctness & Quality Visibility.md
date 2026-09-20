# Shutu-Knowledge v0.6.2 — PDF Ingestion Correctness & Quality Visibility

## 0. 当前正式基线

当前正式版本：

```text
Shutu-Knowledge v0.6.1
```

Release source：

```text
7026da6fa50a7e699e1cf137fa995c9bf55c7f6b
```

Closure commit：

```text
77b1db92a0dc0ac5e26ce6f6701c2b0d4527d46c
```

v0.6.1 已完成正式发布，不允许移动或重建已有 tag。

本任务目标版本：

```text
v0.6.2
```

类型：

```text
MAINTENANCE RELEASE
```

不是新功能版本。

---

# 1. 背景：真实 SmartCare 文档验证暴露 P0

真实测试语料：

```text
C:\Documents\SmartCare_Suite_7.1.0
```

共：

```text
9 PDFs
~330 MB
11,859 pages
~9.75M reference-extracted characters
```

导入配置：

```text
embedding=none
OCR=auto
chunking=smart / 800 / 100
importWorkers=5
```

导入结果：

```text
9/9 operations completed
~37 minutes
no reported errors
```

但实际数据质量严重错误。

---

# 2. 实际覆盖率

```text
文档包信息        ~98%
参考信息          ~96.7%

安全管理          ~0.04%
安装部署          ~0.8%
对接配置          ~8%
故障管理          ~0.1%
特性指南          ~2.3%
解决方案描述      ~4.5%
运维管理          ~3.9%
```

全库仅保留约：

```text
34%
```

的 reference text。

其中绝大多数可用内容集中在两份正常走 native extraction 的文档。

7/9 PDF 实际损失：

```text
92%–99.96%
```

内容。

---

# 3. 更严重的问题：Silent Success

所有 9 个文档最终均显示：

```text
status = ready
incomplete = 0
error = none
```

即使某份 3,285 页 PDF 只留下约 2,740 个字符。

这是：

# P0 DATA CORRECTNESS DEFECT

原因：

> 系统宣称知识已成功导入，但实际绝大部分内容不存在。

这会直接污染：

* Retrieval
* Semantic Memory
* Knowledge Compiler
* Citation
* Cross-document reasoning
* Agent answers

因此在修复完成前：

```text
暂停 0.7 / Recursive Research / GraphRAG / 新 Knowledge 能力
```

---

# 4. 当前根因链

已定位以下核心问题。

---

## Root Cause A — Weak primary PDF extraction

当前：

```text
internal/parser/pdf.go
```

使用：

```text
ledongthuc/pdf
```

这批 Huawei 导出 PDF 的文本对象特点：

```text
每个 glyph/字符成为独立 text item
平均 line length ≈ 1
约 1% glyph 无 ToUnicode
产生 U+FFFD
```

原始 plain extraction 因而被判断为 fragmented / unhealthy。

---

## Root Cause B — U+FFFD 一票否决

当前流程类似：

```text
plain
↓
AverageLineLength >= 5 ?
hasReplacementRune == false ?
```

失败后进入：

```text
reassemblePDFLayout()
```

实际验证表明：

> layout reassembly 能恢复绝大部分正常文本。

但随后又因为：

```text
hasReplacementRune(reassembled)
```

直接拒绝整个结果。

即：

```text
~1% bad glyph
→ discard ~99% usable text
```

而另一条 extraction branch 又没有同样的 FFFD 一票否决。

当前逻辑：

```text
INCONSISTENT
```

---

# 5. Root Cause C — OCR 无条件覆盖 native text

当前：

```text
internal/knowledge/service_doc.go
```

fragmented layer 路径中：

```text
runOCR()
```

只要返回非空：

```text
return OCR text
```

导致：

```text
good-but-imperfect native text
          ↓
discarded
          ↓
weak/partial OCR text
```

这是直接数据破坏。

---

# 6. Root Cause D — OCR hard limit = 100 pages

当前：

```text
MaxOCRRenderPages = 100
```

对于：

```text
306
792
1,412
1,614
3,285
3,645
```

页的 PDF：

OCR 最多覆盖前 100 页。

但系统仍：

```text
status=ready
```

并且没有明确：

```text
partial coverage warning
```

---

# 7. Root Cause E — No strong native fallback

测试中使用独立 reference extraction 工具读取全部：

```text
11,859 pages
```

只需要约：

```text
7 seconds
```

并能恢复约 9.75M 字符。

这证明：

> 这些 PDF 大部分并不是扫描件。

真正问题是：

```text
current native PDF extractor compatibility
```

而不是：

```text
OCR capability missing
```

---

# 8. 本任务唯一目标

修复：

# PDF Ingestion Correctness

确保：

```text
Good source text must never be silently replaced by worse extraction.

Partial extraction must never be reported as complete success.

Low-quality import must be observable.

OCR must be fallback, not destructive replacement.
```

---

# 9. 明确禁止扩大范围

本任务禁止：

```text
GraphRAG
Ontology
Recursive Research
Agent planner
Semantic Memory redesign
Temporal redesign
Document IR redesign
New vector DB
PostgreSQL
Distributed workers
LLM fine-tuning
Large UI redesign
```

也不要借本任务：

```text
重写整个 parser architecture
```

只做：

```text
targeted ingestion correctness fix
```

---

# 10. P0-A — 统一 PDF Text Quality Evaluator

首先不要继续在各分支写零散：

```text
if averageLineLength...
if replacementRune...
```

实现统一：

```text
PDFTextQuality
```

或等效内部结构。

建议至少包含：

```text
chars
chars_per_page
replacement_rune_count
replacement_rune_ratio
printable_ratio
cjk_ratio
alnum_ratio
average_line_length
median_line_length
fragment_ratio
empty_page_ratio
page_coverage
```

如果现有结构更适合其他字段，可以调整。

核心要求：

> 所有 native / reassembled / fallback / OCR candidate 使用同一套质量评价逻辑。

---

# 11. 不允许单个 U+FFFD 一票否决

禁止：

```text
hasReplacementRune == true
→ reject entire extraction
```

改成：

```text
replacement_ratio
```

作为质量指标之一。

例如：

```text
0.5% / 1%
```

坏 glyph 不应该摧毁：

```text
99% healthy content
```

可以：

* strip isolated U+FFFD
* normalize
* retain candidate
* add warning

但不能：

```text
throw away whole document
```

---

# 12. Candidate Selection

PDF extraction 应形成候选：

```text
Candidate A: native plain
Candidate B: layout reassembled
Candidate C: strong native fallback
Candidate D: OCR / page-level OCR
```

每个候选带：

```text
quality metrics
coverage
warnings
source method
```

然后择优。

禁止继续：

```text
OCR non-empty
→ unconditional winner
```

---

# 13. 优先顺序

建议逻辑：

```text
Primary native extractor
        ↓
If healthy
        → use

If fragmented
        ↓
Layout reassembly
        ↓
If healthy enough
        → use

If still low quality
        ↓
Strong native fallback
        ↓
If healthy
        → use

Only remaining bad/missing pages
        ↓
OCR
```

重点：

# OCR SHOULD BE THE LAST RESORT

---

# 14. Page-level 优先

如果架构允许，优先逐页判断：

```text
Page 1 native good       → native
Page 2 native good       → native
Page 3 no text           → OCR
Page 4 fragmented bad    → fallback/OCR
...
```

然后合并。

比：

```text
whole-document OCR replacement
```

更安全。

如果本次实现成本过大，可以先 document candidate selection，但必须：

```text
不允许 partial OCR 替代 complete native candidate
```

---

# 15. OCR Replacement Safety Rule

加入硬规则：

如果：

```text
OCR page coverage < native page coverage
```

或者：

```text
OCR chars << native chars
```

不得整体覆盖 native result。

至少应满足合理：

```text
coverage
quality
```

提升才可胜出。

---

# 16. Strong Native Fallback

在 OCR 前加入一个更强的 PDF native text fallback。

候选技术需先做：

```text
license
distribution
Windows packaging
CGO/runtime dependency
binary size
offline operation
```

审计。

优先目标：

```text
high compatibility
per-page text extraction
reading-order acceptable
offline
Windows x64 reliable
license compatible with project
```

不要仅仅因为测试使用某个库，就直接纳入 release。

必须输出：

```text
docs/pdf_native_fallback_evaluation.md
```

说明：

```text
candidate
license
integration complexity
performance
quality
packaging impact
final decision
```

---

# 17. 不要把 Reference Extractor 当绝对 Ground Truth

测试报告中的约 9.75M 字符称为：

```text
reference extraction
```

不要正式命名：

```text
absolute ground truth
```

因为不同 extractor 可能存在：

* reading order 差异
* hidden text
* tables
* repeated headers
* glyph normalization

真正验收重点是：

```text
coverage order-of-magnitude
+
semantic usability
```

---

# 18. OCR 100 页限制

当前：

```text
MaxOCRRenderPages = 100
```

不能静默。

至少做到：

```text
pages_total = 3285
pages_ocr_attempted = 100

ocr_partial = true
warning:
OCR_PAGE_LIMIT_REACHED
```

---

# 19. 更优方案：OCR batching

如果实现风险可控：

```text
100 pages / batch
```

持续处理。

例如：

```text
1–100
101–200
...
```

同时暴露：

```text
processed_pages / total_pages
```

但不能为了本任务引入大型 job architecture 重构。

---

# 20. OCR 必须避免无意义全量运行

对于：

```text
native/fallback quality already good
```

不得再 OCR 数千页。

否则：

```text
fast native extraction
→ 37-minute OCR path
```

完全不合理。

---

# 21. Import Quality Model

新增持久化或可计算的：

```text
ImportQuality
```

至少包含：

```text
parser
pages_total
pages_text
pages_ocr
pages_failed
chars_extracted
chars_per_page
quality_score
quality_status
warnings
partial
```

字段可根据现有 schema 调整。

---

# 22. Quality Status

建议至少：

```text
GOOD
WARNING
LOW_QUALITY
INCOMPLETE
FAILED
```

不要只有：

```text
ready
```

---

# 23. ready 的语义

可以保留业务：

```text
status=ready
```

表示 operation 完成。

但必须增加独立：

```text
quality_status
```

例如：

```text
ready + GOOD
ready + WARNING
ready + LOW_QUALITY
```

而真正：

```text
critical incomplete extraction
```

应该：

```text
incomplete = true
```

或者等效显式状态。

---

# 24. 最低质量保护

不能简单使用：

```text
<100 chars/page
```

作为唯一绝对阈值。

因为真实 PDF 可能：

* 图片页
* 封面
* 表格
* 大量空白
* diagram-heavy

应综合：

```text
chars/page
page coverage
replacement ratio
fragment ratio
document size/page count
```

---

# 25. Silent Catastrophic Loss Detector

必须专门检测这种情况：

```text
3000 pages
+
2700 chars
+
status GOOD
```

这是禁止状态。

例如：

```text
pages > 100
AND
chars/pages extremely low
AND
OCR/native coverage incomplete
```

至少标记：

```text
LOW_QUALITY / INCOMPLETE
```

---

# 26. Chunk Hygiene

解决真实测试：

```text
20%–91%
```

小碎片 chunk。

针对：

```text
<10 meaningful characters
```

的 chunk：

优先：

```text
merge with adjacent compatible chunk
```

如果无法合并且没有独立语义价值：

```text
discard
```

但：

不要误删：

* CLI command
* error code
* acronym
* parameter value
* table cell

因此不要只按字符长度机械删除。

---

# 27. Fragment Detection

可以综合：

```text
length
alphabetic/CJK content
punctuation ratio
neighbor structure
source block
```

避免这种噪声：

```text
ESN F5: eRe
kr 3 3 / S :
时 = 则图
Ht hd a i
```

单独成为高分 chunk。

---

# 28. Retrieval Guard

低质量 extraction 产生的 chunk：

如果 quality_status：

```text
LOW_QUALITY
```

应考虑：

```text
retrieval penalty
```

或不进入正式索引。

不要让：

```text
OCR garbage
```

以：

```text
0.91
```

混入 Top-K。

优先原则：

> Bad evidence is often worse than missing evidence.

---

# 29. Import API Result

目录导入完成后，应提供汇总：

```text
9 files

GOOD: 8
WARNING: 1
LOW_QUALITY: 0
INCOMPLETE: 0

Pages: ...
Native pages: ...
Fallback pages: ...
OCR pages: ...
Warnings: ...
```

---

# 30. Per-file Result

至少能看到：

```text
SmartCare Suite_7.1.0_故障管理.pdf

parser:
native / reassembled / fallback / mixed

pages:
3285

quality:
GOOD

coverage:
...

warnings:
...
```

---

# 31. Web UI

只做最小 UI。

文档列表增加：

```text
Quality
```

例如：

```text
GOOD
WARNING
INCOMPLETE
```

详情可查看：

```text
parser
pages
OCR pages
warnings
```

不要做大型 dashboard。

---

# 32. Operation UI

目录导入结束不能只显示：

```text
9/9 completed
```

还要：

```text
9 imported
7 good
1 warning
1 incomplete
```

---

# 33. Embedding 不属于本次 P0

当前真实测试：

```text
embedding=none
```

导致 BM25-only 语义能力有限。

这是已知问题。

但：

```text
embedding=none
```

不是这次 92–99% 数据丢失的原因。

因此不要让 embedding 工作分散 P0 修复。

---

# 34. 文档说明

更新文档明确：

```text
embedding=none
→ lexical/BM25 retrieval only
→ semantic retrieval unavailable
```

可以增加开箱引导。

但本次不要求新模型系统。

---

# 35. Golden Regression Fixtures

不能提交真实 SmartCare PDF。

必须构造 synthetic/minimal fixtures 模拟真实问题。

至少包括：

## Fixture A — Per-glyph PDF

模拟：

```text
one glyph per text item
```

---

## Fixture B — Small U+FFFD Ratio

例如：

```text
1% replacement glyph
99% healthy text
```

预期：

```text
do not reject healthy extraction
```

---

## Fixture C — Fragmented plain but good reassembly

预期：

```text
reassembled candidate selected
```

---

## Fixture D — Worse OCR than native

预期：

```text
native retained
```

---

## Fixture E — Partial OCR

例如：

```text
300 pages
OCR only 100 pages
```

预期：

```text
partial warning
not complete GOOD
not overwrite full native
```

---

## Fixture F — True scanned PDF

预期：

```text
OCR used
quality visible
```

---

# 36. Parser Unit Tests

必须测试：

```text
quality scoring
replacement ratio
candidate comparison
page coverage
fragment detection
OCR selection
```

---

# 37. Service-level Tests

测试：

```text
import
→ quality metadata
→ document status
→ chunk creation
→ search
```

---

# 38. Restart / Durable Operation Regression

v0.6.1 刚修过：

```text
interrupted durable operation retry
```

本任务不能破坏。

必须 rerun：

```text
durable operation
restart
retry
active task count
```

相关 regression。

---

# 39. Existing Knowledge Regression

不能破坏：

```text
Document IR
Knowledge Compiler
Semantic Memory
Temporal Knowledge
Historical Reasoning
Citation
```

至少运行现有相关 smoke/regression。

---

# 40. Real Corpus Acceptance

修复完成后必须重新使用原始：

```text
C:\Documents\SmartCare_Suite_7.1.0
```

完整导入。

使用：

```text
clean isolated home
```

但可以复用 runtime/model cache。

不能复用旧入库结果。

---

# 41. Real Corpus Acceptance — 第一目标

首先不是问答。

第一验收：

# TEXT INGESTION QUALITY

逐文件输出：

```text
pages
reference chars
ingested chars
coverage proxy
fragment/noise ratio
native/fallback/OCR pages
quality status
warnings
```

---

# 42. Coverage 目标

不要机械要求字符数完全一致。

但对这批文本型 SmartCare PDF：

7 个此前失败文档必须从：

```text
0.04%–8%
```

提升到接近正常 extraction 的量级。

建议目标：

```text
>= 90% reference-character-scale
```

更理想：

```text
>= 95%
```

如果某文档低于此：

必须有明确原因报告。

---

# 43. Hard Release Blocker

以下任一存在则禁止发布：

```text
large text PDF coverage <50%
AND quality reported GOOD
```

或者：

```text
partial OCR silently marked complete
```

或者：

```text
worse OCR replaces substantially better native text
```

---

# 44. Real Corpus Noise

碎片 chunk：

当前：

```text
20%–91%
```

的异常文档。

目标：

```text
dramatic reduction
```

建议：

```text
<5%
```

如果不能达到，必须分析原因。

---

# 45. Retrieval Re-test

复用已失败的真实 Query：

```text
Data Cube 内部通信证书管理
```

```text
备份时桶名如何预置
```

```text
检查datacatalog-broker服务状态
```

```text
Yacht 服务端证书认证
```

要求：

```text
target document can now be retrieved
relevant text readable
garbage chunks not dominating Top-K
```

---

# 46. Existing Healthy Docs

必须验证：

```text
文档包信息
参考信息
```

没有因为新 parser path 回退。

当前约：

```text
98%
96.7%
```

至少不能明显下降。

---

# 47. Performance

重新记录：

```text
total import time
per-document import time
native/fallback/OCR time
peak memory
DB size
chunk count
```

目标：

> 修复正确性的同时，不应继续把文本型 PDF 无意义地走大规模 OCR。

理想上总耗时应该明显低于当前约：

```text
37 min
```

但：

```text
correctness > performance
```

---

# 48. Import Performance Breakdown

最好输出：

```text
native extraction
layout reassembly
fallback extraction
OCR
chunking
storage
```

耗时。

方便以后定位。

---

# 49. No Full-Corpus OCR

如果这批 PDF 的 native/fallback 能恢复文本：

应该看到：

```text
OCR pages << 11,859 pages
```

而不是重新 OCR 整套文档。

---

# 50. Quality Diagnostics Persistence

Quality 信息必须在：

```text
restart
```

后仍存在。

不能只是 operation memory 中临时显示。

---

# 51. Update/Reimport

测试重新导入同一 PDF：

```text
old generation
→ new generation
```

确保：

```text
quality metadata
chunks
knowledge
```

原子切换。

不能混用旧乱码 chunk 与新正常 chunk。

---

# 52. Delete

删除文档后：

```text
chunks
quality metadata
derived knowledge
```

正确清除。

---

# 53. Migration

现有 0.6.1 KB 升级到 0.6.2：

必须：

```text
PASS
```

老文档如果没有 quality metadata：

允许：

```text
UNKNOWN
```

不要凭空标：

```text
GOOD
```

---

# 54. 是否强制旧库重建

不要强制整个知识库 rebuild。

建议提供：

```text
reimport/reindex recommended
```

或现有可用入口。

但对于：

```text
UNKNOWN / suspicious low-density old PDF
```

可以显示：

```text
Recommend re-import
```

---

# 55. Low-quality Existing Docs

如果能安全检测：

```text
pages >> 0
chars/pages implausibly low
```

可以将旧文档标记为：

```text
UNKNOWN / SUSPECT
```

但不要自动删除。

---

# 56. Release Documentation

创建：

```text
docs/release_notes_0.6.2.md
docs/release_report_0.6.2.md
```

Release Notes 重点写：

```text
PDF ingestion correctness
quality visibility
OCR/native selection safety
large-PDF partial extraction detection
chunk hygiene
```

不要宣传新知识能力。

---

# 57. Root Cause Report

创建：

```text
docs/pdf_ingestion_real_world_failure_analysis.md
```

记录：

```text
symptom
root cause
fix
before/after evidence
remaining limitations
```

不要提交任何私有 SmartCare 内容。

---

# 58. Private Corpus Safety

严禁提交：

```text
C:\Documents\SmartCare_Suite_7.1.0
```

中的：

* PDF
* extracted text
* screenshots
* proprietary excerpts
* filenames if project policy requires redaction

如果 filenames 已允许用于内部报告，也尽量仅存：

```text
Doc A
Doc B
...
```

公开仓库只提交：

```text
aggregated statistics
synthetic fixtures
non-sensitive test results
```

---

# 59. Release Gate

v0.6.2 必须全部满足：

```text
Build                              PASS
Go vet                             PASS
Go tests                           PASS
Race                               PASS
Web build/typecheck/tests          PASS
Browser E2E                        PASS

Parser regression                  PASS
PDF quality evaluator              PASS
Candidate selection                PASS
U+FFFD tolerance                   PASS
OCR replacement safety             PASS
OCR partial coverage detection      PASS
Chunk hygiene                       PASS

Migration 0.6.1 → 0.6.2            PASS
Durable operation regression       PASS
Restart/recovery                    PASS

Document IR regression             PASS
Retrieval regression               PASS
Knowledge Compiler smoke           PASS
Semantic Memory smoke              PASS
Temporal regression                PASS

SmartCare real-corpus import        PASS
No silent catastrophic loss         PASS
Real retrieval probes               PASS

Windows formal package             PASS
Offline restart                     PASS

No P0                              PASS
```

---

# 60. SmartCare Acceptance Report

生成本地验证报告。

如果不能公开提交，可输出到：

```text
.tmp/
```

同时只提交脱敏汇总。

最终至少给出：

| Metric                  | v0.6.1 | v0.6.2 |
| ----------------------- | -----: | -----: |
| Documents               |      9 |      9 |
| Pages                   | 11,859 | 11,859 |
| Approx text coverage    |    34% |        |
| Low-quality docs        |      7 |        |
| Silent incomplete docs  |      7 |        |
| Fragment noise          |        |        |
| Total import time       |   ~37m |        |
| OCR pages               |        |        |
| Retrieval probes passed |        |        |

---

# 61. Per-document Comparison

必须给出脱敏形式：

```text
Doc A
before coverage:
after coverage:

Doc B
...
```

不要只给一个整体平均数。

避免：

```text
两份大文档很好
掩盖七份仍然失败
```

---

# 62. Stop Condition

如果真实 SmartCare 重新导入仍出现：

```text
majority text loss
```

禁止进入 release promotion。

继续修复 ingestion。

---

# 63. 不允许降低标准来过 Gate

禁止通过：

```text
放宽测试
删除 fixture
降低质量阈值
忽略 partial warning
```

制造 PASS。

必须修根因。

---

# 64. v0.6.2 Release Source

所有修复完成后：

先形成明确：

```text
release source SHA
```

并完整跑：

```text
push CI
```

---

# 65. 正式 Windows Artifact

构建：

```text
shutu-knowledge-0.6.2-windows-amd64.zip
```

记录：

```text
size
SHA-256
source SHA
```

---

# 66. Formal Package Smoke

从 ZIP 解压后的正式包测试：

```text
startup
runtime status
PDF import
quality metadata
retrieval
restart
offline restart
```

必须包含至少一个：

```text
fragmented text fixture
```

和：

```text
true OCR fixture
```

---

# 67. Tag

只有：

```text
push CI PASS
real corpus PASS
formal package PASS
```

后才允许创建：

```text
v0.6.2
```

要求：

```text
annotated
immutable
```

---

# 68. Tag CI

完整通过现有：

```text
build
release-package
runtime-release
release-host Windows
release-host Linux
release-host macOS
```

及全部现行 release gates。

---

# 69. GitHub Release

只有 Tag CI：

```text
PASS
```

后创建：

```text
Shutu Knowledge v0.6.2
```

上传：

```text
shutu-knowledge-0.6.2-windows-amd64.zip
```

然后：

```text
download published asset
recompute SHA-256
verify exact match
```

---

# 70. Closure

最后只允许：

```text
documentation-only closure commit
```

更新：

```text
docs/release_report_0.6.2.md
```

记录：

```text
release source
tag object
tag target
push CI
tag CI
artifact
size
SHA-256
post-download verification
real-corpus validation
```

---

# 71. Maintenance Mode

v0.6.2 发布后：

继续：

```text
0.6.x MAINTENANCE MODE
```

不要自动启动：

```text
0.7
```

---

# 72. 发布后下一步

只有 v0.6.2 修复并重新导入真实 SmartCare 文档成功后，才恢复：

```text
real-world knowledge quality evaluation
```

之后再测试：

```text
fact
cross-document synthesis
troubleshooting
multi-hop
semantic memory
temporal reasoning
recursive-research need
```

---

# 73. 本任务最重要的原则

```text
Data fidelity before reasoning sophistication.

A completed operation is not necessarily a successful import.

Never replace better evidence with worse OCR.

One bad glyph must not destroy an otherwise healthy document.

Partial extraction must be explicit.

Quality must be observable.

Bad evidence should not silently enter retrieval.

OCR is fallback, not the default recovery path.

Real documents define correctness.

Do not build 0.7 on corrupted input.
```

---

# 74. 最终成功定义

v0.6.2 成功不是：

```text
9/9 import operations completed
```

而是：

> 对真实的 11,859 页 SmartCare Suite 7.1.0 文档，Shutu-Knowledge 能够忠实保留绝大部分可提取文本，只有真正缺失文本层的页面才进入 OCR；任何 partial/low-quality 情况都能够被系统识别并展示，且错误 OCR 不再覆盖更好的原始文本。

最终回答：

# “用户看到 READY 时，这份文档真的已经可靠地进入知识库了吗？”

只有答案能够是：

```text
YES
```

才允许发布 v0.6.2。
