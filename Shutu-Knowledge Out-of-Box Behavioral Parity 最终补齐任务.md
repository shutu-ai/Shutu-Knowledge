# 任务：补齐 Shutu-Knowledge 与 dsh-knowledge 的 Out-of-Box Behavioral Parity

目标项目：

```text
https://github.com/shutu-ai/Shutu-Knowledge
```

Agent 底座：

```text
https://github.com/shutu-ai/shutu-agent
```

知识库参考：

```text
https://github.com/Soren-ABT/dsh-knowledge
```

---

# 一、背景

当前 `Shutu-Knowledge V1` 已经完成：

```text
Knowledge Base / Document CRUD
Document lifecycle
Modern document parsing
Chunking
Semantic Chunking
BM25 / FTS
Vector Retrieval
Hybrid Retrieval
RRF
MMR
Reranker abstraction
Context Composer
ContextWindow
Anchor / continuation
Auto-RAG
14 Knowledge Tools
Native Extension integration
Approval integration
Lifecycle / Health
Web UI
Retrieval Test
Jobs
Persistence
CI / clean clone / race / E2E
Apache-2.0 release readiness
```

当前架构已经满足：

```text
Shutu-Knowledge
       │
       │ one-way dependency
       ▼
shutu-agent Extension Platform v1
```

不得改变这一关系。

---

# 二、本轮真正目标

之前的 capability matrix 更偏向：

```text
是否存在能力接口
是否存在对应行为
是否有测试
```

本轮增加一个更严格标准：

> 用户在全新机器安装 Shutu-Knowledge 后，是否能够真正完成 dsh-knowledge 对应任务，而不需要用户自己再开发一个 inference/OCR/converter helper。

最终要建立：

# Out-of-Box Behavioral Parity

重点补齐以下领域：

```text
1. Local Embedding
2. Local Reranker
3. OCR Runtime
4. PDF Full-page Rendering
5. JBIG2 / JPX 等 PDF 图像能力
6. Legacy Office .doc / .ppt / .xls
7. Model Download → Ready → Inference 完整闭环
8. Packaging / Dependency Discovery / Doctor
9. System-prompt proactive guidance 的最终可实现性审计
```

---

# 三、严格架构约束

`shutu-agent` 在本任务中：

```text
READ ONLY
```

允许：

```text
读取公开 SDK
读取 Extension Protocol
读取 docs
读取 tests
运行 Agent 集成测试
```

禁止：

```text
修改 shutu-agent
修改 Extension Platform
import shutu-agent/internal/...
修改 Agent Prompt
修改 Agent Session
增加 Knowledge-specific Agent API
修改 Agent frontend
```

如果某项能力确实受 Agent Contract 限制：

```text
BLOCKED BY AGENT CONTRACT
```

并记录 Gap。

不得跨仓库偷偷修。

---

# 四、不要重新实现已经通过的功能

除非测试发现确定性 bug，否则不要重构：

```text
BM25
Vector index
RRF
MMR
Context Composer
Knowledge CRUD
Jobs
Web shell
Extension lifecycle
Tool definitions
Auto-RAG core pipeline
```

本任务是：

```text
runtime completeness
+
packaging completeness
+
real inference/parsing completeness
```

不是第二次重写 Knowledge。

---

# 五、首先重新锁定 dsh-knowledge 基线

在修改代码前，确认当前项目原 equivalence matrix 使用的准确：

```text
dsh-knowledge version
commit SHA
```

不得用 README 搜索缓存或其它版本替代。

生成或更新：

```text
docs/out_of_box_parity_audit.md
```

记录：

```text
Reference repository
Reference commit
Reference version
Audit date
Target capabilities
```

所有后续判断必须基于同一个 upstream commit。

---

# 六、建立新的严格状态定义

本轮不得只使用：

```text
PASS
```

必须进一步区分：

```text
BUILT_IN
BUNDLED_RUNTIME
AUTO_MANAGED_EXTERNAL_RUNTIME
MANUAL_EXTERNAL_RUNTIME
BLOCKED_BY_AGENT
NOT_APPLICABLE
FAIL
```

定义：

### BUILT_IN

无需独立 runtime，Shutu-Knowledge 自身即可执行。

### BUNDLED_RUNTIME

发行包已经包含所需 runtime/helper，安装后可直接使用。

### AUTO_MANAGED_EXTERNAL_RUNTIME

runtime 不一定打进主 binary，但 Shutu-Knowledge 能：

```text
detect
download/install
configure
start
health-check
restart
upgrade
```

用户不需要自己开发或手工接线。

### MANUAL_EXTERNAL_RUNTIME

用户需要自行安装、开发或配置 helper。

这不能再计为完整 Out-of-Box PASS。

### BLOCKED_BY_AGENT

只有 Agent Contract 缺口才能导致。

---

# 七、最终目标

以下关键能力除 `system-prompt guidance` 外，应尽量从：

```text
MANUAL_EXTERNAL_RUNTIME
```

提升到：

```text
BUILT_IN
或
BUNDLED_RUNTIME
或
AUTO_MANAGED_EXTERNAL_RUNTIME
```

才能判定：

```text
OUT-OF-BOX PARITY PASS
```

---

# 八、P0：Local Embedding 真正可运行

当前如果只存在：

```text
EmbeddingProvider interface
helper command
health
model management
```

但没有随项目真正提供 inference implementation：

不得继续判完整 parity。

必须实现真正可运行的 Local Embedding。

---

# 九、审计 dsh-knowledge Local Embedding

确认参考版本实际使用：

```text
模型
runtime
模型格式
tokenizer
pooling
normalization
dimension
query/document prompt
batching
threading
device selection
cache
download source
```

不能只做：

```text
text → arbitrary vector
```

来满足测试。

---

# 十、选择适合 Shutu-Knowledge 的本地推理方案

必须根据：

```text
Go
Windows
Linux
CPU-first local deployment
独立 Knowledge project
```

选择可维护方案。

可以评估：

```text
ONNX Runtime
llama.cpp / GGUF
OpenVINO
专用 helper
其它成熟 runtime
```

不要为了“全 Go”牺牲正确性。

推荐：

```text
Knowledge Core
      ↓
EmbeddingProvider
      ↓
Local Runtime Adapter
      ↓
managed inference runtime
```

---

# 十一、Local Embedding 必须真正完成闭环

最终必须真实验证：

```text
Model Download
      ↓
Artifact Validation
      ↓
Runtime Discovery
      ↓
Model Load
      ↓
Embedding(text)
      ↓
correct dimension
      ↓
normalization
      ↓
Vector Index
      ↓
Retrieval
```

不能只验证：

```text
process starts
health = ready
```

---

# 十二、Embedding 下载与模型管理

用户在 Web/CLI 选择模型后：

```text
download
verify
ready
```

应成为完整流程。

至少处理：

```text
partial download
checksum
corrupt artifact
resume/retry
disk full
model incompatible
dimension mismatch
runtime unavailable
```

---

# 十三、Local Embedding 真实测试

必须使用真正模型执行：

```text
Chinese query
English query
mixed-language query
batch input
long input
empty input
```

验证：

```text
stable vector dimension
finite values
non-zero vector
similar text > unrelated text
```

不要只 mock provider。

---

# 十四、P0：Local Reranker

要求和 Embedding 相同。

最终：

```text
query
+
candidate chunks
      ↓
real local reranker inference
      ↓
scores
      ↓
reordered candidates
```

必须真实工作。

---

# 十五、Reranker 行为

至少验证：

```text
relevant chunk moves upward
irrelevant chunk moves downward
batch works
timeout works
model unavailable fallback works
```

---

# 十六、Embedding / Reranker failure isolation

如果 Local Reranker 不可用：

```text
retrieval should degrade gracefully
```

例如：

```text
Hybrid Retrieval
→ skip reranking
→ return fused result
```

除非配置：

```text
reranker.required = true
```

Embedding 如果是当前索引必需模型，则必须给出明确不可用状态，而不是产生错误维度向量。

---

# 十七、P0：OCR Runtime 真正交付

当前若 OCR 只是：

```text
helper contract
```

但没有真正 runtime：

必须补齐。

目标至少覆盖参考 dsh-knowledge 实际支持的 OCR pipeline。

---

# 十八、OCR 分层

需要明确：

```text
Native Text Extraction
      ↓ insufficient
OCR Fallback
```

以及：

```text
Forced OCR
```

模式。

不要对所有 PDF 默认 OCR。

---

# 十九、OCR Runtime

根据 upstream 实现审计真实能力：

```text
PaddleOCR
Tesseract
其它 OCR runtime
```

选择适合当前项目的交付模式。

可以：

```text
bundled
auto-managed
```

但不能再要求用户自己写 helper。

---

# 二十、OCR Health / Doctor

`shutu-knowledge doctor` 必须能显示：

```text
OCR runtime
version
languages
model files
renderer
ready/unavailable
remediation
```

错误必须可行动。

不要只显示：

```text
OCR failed
```

---

# 二十一、OCR E2E

真实测试至少包含：

```text
image-only PDF
mixed text/image PDF
rotated scan
Chinese
English
multi-page
low-quality scan
```

验证：

```text
text extracted
page metadata retained
failure fallback correct
```

---

# 二十二、P0：PDF Full-page Rendering

如果 OCR 依赖整页 rasterization：

必须提供真正 renderer。

用户不应自行安装开发 helper 才能 OCR PDF。

可以使用：

```text
PDFium
MuPDF
Poppler
其它许可证兼容实现
```

必须做 license audit。

---

# 二十三、JBIG2 / JPX

审计参考 dsh-knowledge 对：

```text
JBIG2
JPEG2000/JPX
```

的真实支持。

如果这些格式是 OCR/PDF pipeline 的必要兼容能力：

必须明确：

```text
内置 decoder
runtime decoder
unsupported with explicit diagnosis
```

不得静默丢页。

---

# 二十四、PDF 失败策略

遇到：

```text
encrypted PDF
corrupted PDF
unsupported compression
missing font
huge page
```

应该：

```text
明确 error
不中断其它 documents
job status failed
diagnostic 可见
```

---

# 二十五、P0：Legacy Office 真正解析

重点：

```text
.doc
.ppt
.xls
```

当前如果只是：

```text
converter helper contract
```

不能再算完整 Out-of-Box parity。

---

# 二十六、Legacy Office 实现选择

审计 dsh-knowledge 的真实实现后，可选择：

```text
LibreOffice headless
成熟 converter
专门 parser
其它许可证兼容 runtime
```

如果选择 LibreOffice：

Knowledge 应自动：

```text
discover executable
validate version
run conversion
timeout
capture output
clean temp files
health check
```

不能要求用户写 conversion command。

---

# 二十七、允许“依赖外部已安装应用”，但必须自动管理接线

如果因为许可证/体积原因不打包 LibreOffice：

可以归类：

```text
AUTO_MANAGED_EXTERNAL_RUNTIME
```

前提是：

```text
自动探测
无需编写 helper
明确安装提示
自动调用
doctor 检查
```

这比当前：

```text
请配置自定义 converter helper
```

高一个完整等级。

---

# 二十八、Legacy Office E2E

真实 fixture：

```text
.doc
.ppt
.xls
```

验证：

```text
content
sheet/slide structure
metadata
conversion cleanup
timeout
bad file
```

---

# 二十九、P0：Model Download → Ready → Inference

这是本轮最关键的总体验 Gate。

UI 或 CLI 不能出现：

```text
Downloaded
```

但实际还无法 inference。

必须建立明确状态：

```text
NOT_INSTALLED
DOWNLOADING
VERIFYING
INSTALLED
RUNTIME_MISSING
LOADING
READY
FAILED
```

根据项目实际简化。

---

# 三十、Ready 的严格定义

只有：

```text
model artifact exists
+
checksum/version valid
+
runtime available
+
model successfully loaded
+
minimum inference smoke passed
```

才能：

```text
READY
```

不能：

```text
文件存在 = Ready
```

---

# 三十一、模型 UI

Models 页面应能显示：

```text
model
type
version
size
runtime
download status
load status
ready
last error
```

并支持合理操作：

```text
Download
Retry
Validate
Delete
```

---

# 三十二、离线场景

下载完成以后：

```text
断网
→ restart Knowledge
→ Local Embedding/Reranker still works
```

必须测试。

---

# 三十三、Runtime Packaging

建立：

```text
docs/runtime_dependencies.md
```

清楚区分：

```text
built into main binary
bundled with release
auto-download
system dependency
optional
```

用户不能靠阅读源码猜。

---

# 三十四、Windows / Linux

至少明确支持：

```text
Windows amd64
Linux amd64
```

如果本轮只能完成 Windows：

必须明确：

```text
Linux = BLOCKED / TODO
```

不能宣称跨平台完全 parity。

优先保持与 Shutu Agent 当前支持平台一致。

---

# 三十五、Runtime Process Management

所有 helper runtime 必须经过统一管理：

```text
start
stdin/stdout RPC if applicable
stderr capture
timeout
cancel
kill
restart
health
shutdown
```

不要每个 helper 自己写一套不一致的 process logic。

如果项目已有 runtime supervisor：

复用。

---

# 三十六、不要污染 Agent

所有：

```text
embedding runtime
reranker runtime
OCR
LibreOffice
PDF renderer
```

都属于：

```text
Shutu-Knowledge
```

不得加入：

```text
shutu-agent
```

---

# 三十七、Packaging

如果选择：

```text
BUNDLED_RUNTIME
```

最终 release package 必须包含必要文件。

如果选择：

```text
AUTO_MANAGED_EXTERNAL_RUNTIME
```

release package 必须包含：

```text
installer/downloader
manifest
version metadata
doctor support
```

而不是一段 README 手工命令。

---

# 三十八、License / Provenance

这是强制 Gate。

因为 Shutu-Knowledge 是：

```text
Apache-2.0
```

而 dsh-knowledge 是：

```text
AGPL reference
```

本轮禁止：

```text
复制 AGPL runtime implementation
逐行翻译
复制独特 helper code
```

继续保持：

```text
behavior reference
+
independent implementation
```

---

# 三十九、新 Runtime License

重点检查：

```text
ONNX Runtime
PaddleOCR
Tesseract
MuPDF
PDFium
Poppler
LibreOffice
模型权重
Tokenizer
```

实际选用哪些就审哪些。

注意：

```text
library license
model license
model weight distribution terms
```

是不同问题。

---

# 四十、不可为了 Out-of-Box 随包打入许可证不允许的 runtime

如果某 runtime 不适合直接 redistribute：

选择：

```text
AUTO_MANAGED_EXTERNAL_RUNTIME
```

而不是偷偷放进 ZIP。

---

# 四十一、System Prompt Guidance 最终审计

当前 `GAP-001`：

```text
dsh-knowledge 可向 system prompt 注入 proactive guidance
shutu-agent Extension v1 没有对应通用 contribution
```

本轮重新确认一次。

---

# 四十二、禁止为了 GAP-001 修改 Agent

如果 Extension v1 仍然没有安全、公开的对应机制：

保持：

```text
GAP-001 = BLOCKED_BY_AGENT
```

不要：

```text
改 Agent prompt
调用 internal API
修改 session
注入 hidden message
```

---

# 四十三、检查是否已经存在合法等价机制

可以审计：

```text
Tool description
ContextContribution
Extension metadata
Native Context Provider
```

是否能在不改变 Agent 的情况下实现真正等价效果。

但不能把：

```text
效果差不多
```

错误写成：

```text
system prompt parity
```

---

# 四十四、GAP-001 的最终状态

如果仍无法完全等价：

允许最终结果：

```text
BLOCKED_BY_AGENT
NON-BLOCKING
```

只要：

```text
Auto-RAG
+
Tool description
```

保证核心产品可用。

它不应阻止 Runtime Parity 达成。

---

# 四十五、GAP-002

Health structured subcomponents 同样保持现状，除非当前 Agent v1 已经支持。

本轮不要改 Agent。

---

# 四十六、建立 Out-of-Box Parity Matrix

生成：

```text
docs/out_of_box_parity_matrix.md
```

至少：

| Capability | dsh-knowledge | Current Shutu | Final Mode | Test | Status |
|---|---|---|---|---|---|

重点包括：

```text
Local Embedding
Local Reranker
OCR
PDF Renderer
JBIG2
JPX
.doc
.ppt
.xls
Model Download
Model Validation
Model Inference
Offline Restart
Doctor
Packaging
System Prompt Guidance
```

---

# 四十七、最终不能继续把 helper contract 本身算 PASS

例如：

```text
Local Embedding:
Provider interface exists
```

只能说明：

```text
ARCHITECTURE READY
```

不能说明：

```text
OUT-OF-BOX READY
```

---

# 四十八、Clean-machine Test

建立一个真正的 clean-machine / clean-environment gate。

不能利用开发机已经安装的：

```text
Python
Node
LibreOffice
OCR runtime
模型
环境变量
```

而自己不知道。

---

# 四十九、环境依赖必须显式

测试开始先记录：

```text
PATH
runtime discovery
model directory
optional dependencies
```

如果测试依赖系统已经安装软件：

报告必须明确。

---

# 五十、Fresh-install Scenario

至少测试：

```text
fresh Shutu-Knowledge
no knowledge DB
no model
no cache
```

然后：

```text
start
→ Web
→ create KB
→ install/download local embedding
→ READY
→ import document
→ index
→ query
→ local embedding works
```

---

# 五十一、OCR Fresh-install Scenario

```text
fresh install
→ import scanned PDF
→ runtime discovery/install
→ OCR
→ chunks
→ retrieval
```

必须真实走完。

---

# 五十二、Legacy Office Fresh-install Scenario

```text
fresh install
→ .doc/.ppt/.xls
→ runtime detected/managed
→ conversion
→ parse
→ index
→ retrieval
```

---

# 五十三、Reranker Fresh-install Scenario

```text
download/install reranker
→ load
→ query
→ candidates reranked
```

---

# 五十四、Restart Scenario

所有本地 runtime READY 后：

```text
stop Knowledge
start Knowledge
```

必须：

```text
自动恢复
不重新下载
不需要重新配置
```

---

# 五十五、Removal Scenario

删除模型：

```text
model status changes
runtime unloads
retrieval gives actionable error/fallback
```

不能继续显示 READY。

---

# 五十六、Corruption Scenario

故意损坏：

```text
model artifact
OCR runtime dependency
index
```

需要：

```text
detect
mark unhealthy
give remediation
```

---

# 五十七、Doctor 最终标准

`shutu-knowledge doctor` 应成为一站式诊断工具。

至少检查：

```text
Knowledge DB
storage
migrations
embedding runtime
embedding model
reranker runtime
reranker model
OCR
PDF renderer
legacy office converter
disk permissions
Extension config
Web
```

输出：

```text
PASS
WARN
FAIL
remediation
```

---

# 五十八、Web Settings / Models

如果 runtime 有配置，必须通过 Knowledge 自己的 UI 管理。

禁止要求用户修改：

```text
shutu-agent config
```

---

# 五十九、不要把 Runtime Secrets 放进 Agent

如果 remote embedding/rerank 需要 credential：

仍由 Knowledge 管理自己的配置。

遵循已有安全模型。

---

# 六十、性能

真实 Local Embedding/Reranker 上线后重新 benchmark：

```text
single query latency
batch latency
throughput
memory
startup/model load time
```

不要让 mock benchmark 继续代表真实 runtime。

---

# 六十一、Benchmark 基线

记录：

```text
CPU
RAM
OS
model
quantization
threads
```

避免不可重复性能数字。

---

# 六十二、优先级

建议按：

```text
Phase 1 Local Embedding
Phase 2 Local Reranker
Phase 3 PDF Renderer + OCR
Phase 4 Legacy Office
Phase 5 Packaging / Doctor
Phase 6 Fresh-install E2E
Phase 7 Final parity audit
```

实施。

---

# 六十三、每阶段测试和提交

每阶段：

```text
audit
→ design
→ implementation
→ unit test
→ real runtime integration test
→ failure test
→ self-review
→ commit
```

不要等全部完成后一次提交。

---

# 六十四、禁止 MVP 化

当前任务不是：

```text
先支持一个模型即可
```

而是：

> 补齐当前 equivalence matrix 中“接口存在但实际 runtime 未随产品完整交付”的能力。

但也不要随意扩大到 upstream 不支持的功能。

---

# 六十五、与 dsh-knowledge 的最终行为对照

关键输入使用相同或等价 fixture。

比较：

```text
是否能完成任务
解析文本
metadata
retrieval result trend
rerank behavior
OCR result
model lifecycle
failure behavior
```

不要求：

```text
浮点完全一致
内部技术栈一致
```

---

# 六十六、真正的最终分类

最终每项只能是：

```text
BUILT_IN
BUNDLED_RUNTIME
AUTO_MANAGED_EXTERNAL_RUNTIME
BLOCKED_BY_AGENT
NOT_APPLICABLE
FAIL
```

不再用单独 `PASS` 隐藏 runtime 差异。

---

# 六十七、Release Readiness 与 Parity 分开

现有：

```text
SHUTU-KNOWLEDGE V1 RELEASE READY
```

不应因为本轮开发前状态而删除。

但新增：

```text
OUT-OF-BOX PARITY
```

单独判定。

如果本轮没完成：

```text
V1 RELEASE READY
OUT-OF-BOX PARITY NOT COMPLETE
```

是合法状态。

不要修改历史。

---

# 六十八、最终 Gate A：Local Embedding

必须：

```text
real inference
+
fresh-install usable
+
offline restart
```

---

# 六十九、Gate B：Local Reranker

必须：

```text
real inference
+
real reordering
+
fallback
```

---

# 七十、Gate C：OCR

必须：

```text
real scanned document
→ OCR
→ retrieval
```

---

# 七十一、Gate D：PDF Renderer

必须：

```text
full-page render
+
real OCR integration
```

---

# 七十二、Gate E：Legacy Office

必须：

```text
.doc
.ppt
.xls
```

至少通过真实 E2E。

---

# 七十三、Gate F：Model Lifecycle

必须：

```text
download
verify
load
inference
restart
delete
failure
```

完整闭环。

---

# 七十四、Gate G：No Agent Modification

必须确认：

```text
shutu-agent HEAD unchanged
0 tracked modifications
```

---

# 七十五、Gate H：No Agent Internal Import

必须：

```text
0 production imports
```

---

# 七十六、Gate I：License

新增 runtime/model：

```text
license audit PASS
```

不得破坏 Apache-2.0 release posture。

---

# 七十七、Gate J：Fresh-install

必须在干净环境完成至少：

```text
Local Embedding
OCR
Legacy Office
Reranker
```

核心路径。

---

# 七十八、Gate K：Regression

原 V1：

```text
Go build/vet/test/race
Web tests/build/E2E
Extension integration
CI
```

全部继续 PASS。

---

# 七十九、最终报告

生成：

```text
out_of_box_parity_report.md
```

必须明确回答：

### A

Local Embedding 是否真正随产品可用？

```text
YES / NO
```

### B

Local Reranker 是否真正可用？

### C

OCR 是否不需要用户自己开发 helper？

### D

PDF rasterization 是否真实可用？

### E

`.doc/.ppt/.xls` 是否真实可解析？

### F

模型是否：

```text
download → ready → inference
```

真正闭环？

### G

是否修改 shutu-agent？

必须：

```text
NO
```

### H

GAP-001 最终状态？

### I

GAP-002 最终状态？

---

# 八十、最终结果只能二选一

如果 Local Runtime 相关 Gate 全部通过：

# SHUTU-KNOWLEDGE OUT-OF-BOX PARITY READY

并保留：

```text
GAP-001:
BLOCKED_BY_AGENT / NON-BLOCKING
```

如仍适用。

如果 Local Runtime 仍需要用户自己开发 helper：

# SHUTU-KNOWLEDGE OUT-OF-BOX PARITY NOT READY

并明确剩余项。

---

# 八十一、最终架构原则

始终保持：

```text
                  shutu-agent
                       ▲
                       │ Extension Platform v1
                       │
               Shutu-Knowledge
                ┌──────┼───────┐
                │      │       │
             Core   Runtime    Web
                      │
          ┌───────────┼───────────┐
          │           │           │
      Embedding    Reranker      OCR
                              PDF/Office
```

所有领域 runtime 都属于 Knowledge。

Agent 永远不理解：

```text
Embedding
OCR
Reranker
PDF Renderer
LibreOffice
```

---

# 八十二、最终一句检查标准

对每一个目前标记为 PASS 的本地能力问：

> 如果把 Shutu-Knowledge 安装到一台没有我的开发环境、没有自定义 helper、没有手工脚本的新机器上，这项能力是否仍然可以按照产品文档完成？

如果答案是：

```text
NO
```

则该项还不能判定为真正 Out-of-Box parity。