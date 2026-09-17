# 任务：补齐 Shutu-Knowledge 的真实本地 Runtime，达到 Out-of-Box Parity

目标项目：

```text
https://github.com/shutu-ai/Shutu-Knowledge
```

Agent 底座：

```text
https://github.com/shutu-ai/shutu-agent
```

参考项目：

```text
https://github.com/Soren-ABT/dsh-knowledge
```

当前基线至少包含：

```text
commit 04dc70c6ed8b72aa29e63b6377a1be7fbafd6751
```

如果 `origin/master` 已有更新，以最新 master 为准。

---

# 一、当前状态

当前项目已经达到：

```text
SHUTU-KNOWLEDGE V1 RELEASE READY
```

但当前审计明确判定：

```text
SHUTU-KNOWLEDGE OUT-OF-BOX PARITY NOT READY
```

原因已经明确，不再需要重复做大范围 capability audit。

当前真正缺失的是：

```text
1. Local Embedding Runtime
2. Local Reranker Runtime
3. OCR Runtime
4. PDF Full-page Renderer
5. JBIG2 / JPX 等 PDF 图像兼容能力
6. Legacy Office Runtime (.doc / .ppt / .xls)
7. Model Download → Load → Real Inference → READY
8. Runtime Packaging / Discovery / Doctor / Recovery
```

本任务目标：

# 把以上“只有接口/contract/helper seam”的能力，真正实现成用户安装后即可使用的 Runtime。

---

# 二、核心完成标准

最终不能再因为存在：

```text
EmbeddingProvider
RerankerProvider
OCR helper contract
converter helper contract
```

就判 PASS。

必须满足：

> 用户在一台全新机器安装 Shutu-Knowledge 后，不需要自己编写任何 helper、不需要修改 shutu-agent、不需要手工开发运行时适配代码，即可完成本地 Embedding、Reranker、OCR、PDF 和 Legacy Office 任务。

允许：

```text
BUNDLED_RUNTIME
```

或者：

```text
AUTO_MANAGED_EXTERNAL_RUNTIME
```

但不允许最终仍是：

```text
MANUAL_EXTERNAL_RUNTIME
```

---

# 三、严格禁止修改 shutu-agent

`shutu-agent` 在本任务中：

```text
READ ONLY
```

允许：

```text
读取 Extension SDK
读取 Protocol 文档
运行 Agent 集成测试
```

禁止：

```text
修改源码
修改 SDK
修改 Extension Protocol
import shutu-agent/internal/...
修改 Agent Web
修改 Agent Prompt
修改 Agent Session
增加 Knowledge-specific API
```

所有：

```text
Embedding
Reranker
OCR
PDF
Office
Model Runtime
```

必须属于：

```text
Shutu-Knowledge
```

---

# 四、不重新设计已经通过的 Knowledge Core

除非发现确定性 bug，否则不要大规模重构：

```text
Knowledge CRUD
Document lifecycle
Chunking
Semantic Chunking
BM25
Vector index
RRF
MMR
Context Composer
ContextWindow
Auto-RAG
14 Tools
Web Shell
Extension Adapter
Jobs
Storage
```

本任务重点是：

```text
真实 inference
真实 parser/runtime
真实 packaging
真实 fresh-machine usage
```

---

# 五、执行顺序

严格建议按：

```text
Phase 1 — Local Embedding
Phase 2 — Local Reranker
Phase 3 — PDF Renderer
Phase 4 — OCR
Phase 5 — JBIG2 / JPX compatibility
Phase 6 — Legacy Office
Phase 7 — Unified Runtime Management
Phase 8 — Packaging / Doctor
Phase 9 — Fresh-machine E2E
Phase 10 — Final parity audit
```

不要同时开 8 个 runtime 导致无法稳定验收。

---

# 六、Phase 1：Local Embedding Runtime

首先完整读取当前：

```text
internal/embedding/
internal/models/
runtime helper abstractions
docs/runtime_dependencies.md
docs/out_of_box_parity_matrix.md
```

理解当前已有 seam。

不要建立第二套 Embedding pipeline。

目标是把：

```text
EmbeddingProvider
```

后面接上真正可运行的本地 inference backend。

---

# 七、Local Embedding Runtime 选型原则

需要根据当前实际目标选择成熟 runtime。

优先评估：

```text
ONNX Runtime
GGUF / llama.cpp-compatible embedding
OpenVINO
其它成熟、可合法分发的 CPU inference runtime
```

选择时必须比较：

```text
Windows amd64
Linux amd64
CPU inference
模型兼容性
中文/英文
部署体积
启动速度
内存
许可证
是否容易 bundle
是否容易自动下载
```

不要因为当前开发机已经装了某 runtime 就直接选。

---

# 八、不得为了纯 Go 牺牲能力

允许：

```text
Shutu-Knowledge
    ↓
managed helper process
    ↓
native inference runtime
```

只要：

```text
由 Knowledge 自己管理
用户不需要开发 helper
安装/发现/启动自动完成
```

即可。

---

# 九、Embedding 模型行为必须与参考目标一致

审计 dsh-knowledge 对应版本真实使用的：

```text
模型
tokenizer
query prompt
document prompt
pooling
normalization
embedding dimension
max sequence
batching
```

Shutu-Knowledge 不要求源码实现一样，但输出语义必须正确。

---

# 十、Embedding 必须真实闭环

实现：

```text
选择模型
 ↓
下载
 ↓
checksum / artifact validation
 ↓
runtime ready
 ↓
load model
 ↓
real smoke inference
 ↓
READY
 ↓
document embedding
 ↓
vector index
 ↓
query embedding
 ↓
retrieval
```

任何环节失败：

```text
不能显示 READY
```

---

# 十一、Embedding READY 定义

严格要求：

```text
artifact exists
+
artifact valid
+
runtime available
+
model successfully loaded
+
real inference smoke PASS
```

才：

```text
READY
```

只下载成功：

```text
INSTALLED
```

不能混淆。

---

# 十二、Embedding 真实行为测试

必须真实跑模型。

至少：

```text
Chinese
English
mixed Chinese/English
batch
long input
empty/whitespace input
```

验证：

```text
dimension stable
all finite
not zero vector
same/similar text cosine > unrelated text
query/document pipeline valid
```

---

# 十三、Embedding Retrieval E2E

必须真实：

```text
import docs
 ↓
local embedding
 ↓
vector indexing
 ↓
query
 ↓
retrieve semantically relevant chunk
```

不能只测：

```text
Embed("hello")
```

---

# 十四、Embedding Offline Restart

模型 READY 后：

```text
断网
停止 Knowledge
重新启动
```

必须：

```text
无需重新下载
无需手工配置
自动发现模型
恢复 READY
继续 inference
```

---

# 十五、Phase 2：Local Reranker Runtime

复用当前 Reranker abstraction。

不要建立第二套 candidate pipeline。

目标：

```text
query + candidates
        ↓
real local reranker
        ↓
scores
        ↓
sorted candidates
```

---

# 十六、Reranker 模型行为

审计 upstream 对应：

```text
model
tokenizer
input formatting
score semantics
batch size
max length
```

实现行为等价。

---

# 十七、Reranker E2E

测试：

```text
query
candidate A highly relevant
candidate B weak
candidate C irrelevant
```

必须：

```text
A rank rises / stays top
C falls
```

不能只验证返回三个 float。

---

# 十八、Reranker Failure Fallback

默认：

```text
Reranker unavailable
       ↓
Hybrid Retrieval still works
       ↓
return fused candidates
```

除非：

```text
reranker.required = true
```

已有 config 语义另有规定。

不要让可选 reranker 故障导致整个知识库不可查询。

---

# 十九、Phase 3：PDF Full-page Renderer

OCR 真正可用之前必须先补真实 PDF rasterization。

目标：

```text
PDF page
 ↓
render bitmap
 ↓
OCR
```

不是：

```text
从 PDF 提取已有 image object
```

而是真正支持 scanned PDF 的整页 render。

---

# 二十、Renderer 选型

评估成熟方案，例如：

```text
PDFium
MuPDF
Poppler
其它兼容 runtime
```

重点：

```text
Windows
Linux
license
redistribution
JBIG2
JPX/JPEG2000
encrypted PDF behavior
large pages
render DPI
```

---

# 二十一、许可证是硬 Gate

如果某 renderer：

```text
许可证不适合 Apache-2.0 项目直接 bundle
```

不要硬打进 ZIP。

可以：

```text
AUTO_MANAGED_EXTERNAL_RUNTIME
```

但必须：

```text
自动探测
明确安装方式
doctor 检查
无需用户编写 helper
```

---

# 二十二、Phase 4：OCR Runtime

实现真正：

```text
OCRProvider
```

背后的 runtime。

可以评估：

```text
PaddleOCR
Tesseract
其它成熟 OCR
```

根据 upstream 功能和许可证决定。

---

# 二十三、OCR Pipeline

必须支持：

```text
Native text extraction
      ↓
if insufficient
OCR fallback
```

以及：

```text
forced OCR
```

模式。

不能所有 PDF 默认 OCR。

---

# 二十四、OCR 真实 E2E

fixture 至少：

```text
纯扫描 PDF
中英文混合 PDF
旋转页面
多页
低质量扫描
图片文件
```

验证：

```text
text extracted
page metadata retained
document indexed
retrieval works
```

---

# 二十五、OCR Failure Isolation

一个 OCR 文档失败：

```text
不能导致整个 ingestion worker 崩溃
```

必须：

```text
job/document FAILED
error actionable
other docs continue
```

---

# 二十六、Phase 5：JBIG2 / JPX

检查当前 renderer 是否天然支持：

```text
JBIG2
JPEG2000 / JPX
```

如果支持：

增加 fixture/test 证明。

如果不支持：

不能静默判 PASS。

必须：

```text
SUPPORTED
或
runtime dependency required
或
明确 UNSUPPORTED
```

---

# 二十七、PDF 兼容错误必须可诊断

遇到：

```text
encrypted
damaged
unsupported compression
JBIG2 failure
JPX failure
huge image
```

Doctor/job diagnostics 要有：

```text
明确 component
明确原因
明确 remediation
```

---

# 二十八、Phase 6：Legacy Office

需要真正补：

```text
.doc
.ppt
.xls
```

解析。

不能继续只存在 converter helper contract。

---

# 二十九、Legacy Office 方案

优先评估：

```text
LibreOffice headless
成熟 format converter
其它合法 parser
```

如果 LibreOffice：

Knowledge 必须负责：

```text
discover executable
version check
arguments
temp workspace
conversion
timeout
cancel
stderr/stdout capture
cleanup
```

用户不能自己写 conversion script。

---

# 三十、Legacy Office 可以是系统依赖

允许：

```text
AUTO_MANAGED_EXTERNAL_RUNTIME
```

例如：

```text
发现 LibreOffice
→ ready

没发现
→ Doctor 明确提示安装
→ 自动重新检测
```

这可视为 Out-of-Box product integration。

但仍然不能是：

```text
请用户自己实现 converter helper
```

---

# 三十一、Legacy Office E2E

真实 fixture：

```text
.doc
.ppt
.xls
```

必须：

```text
parse/convert
preserve basic structure
chunk
index
retrieve
```

并测试：

```text
bad legacy document
timeout
missing LibreOffice
```

---

# 三十二、Phase 7：统一 Runtime Manager

不要为：

```text
embedding
reranker
OCR
PDF
office
```

分别复制：

```text
exec.Cmd
timeout
kill
stderr
health
```

逻辑。

如果当前项目已有 helper/runtime supervisor：

必须复用并增强。

---

# 三十三、Runtime Manager 最少统一能力

```text
discover
install/download
start
ready
health
call
timeout
cancel
restart
stop
version
logs/error capture
```

根据 runtime 类型按需实现。

---

# 三十四、Windows Process Safety

重点测试：

```text
cancel
process tree
stdout/stderr
fast exit
forced kill
restart
```

不要重新引入 Agent 之前修过的 subprocess race 类问题。

---

# 三十五、Linux Process Safety

至少 GitHub Linux CI 跑：

```text
runtime discovery
process start/stop
timeout
signal
temp path
file permission
```

---

# 三十六、Phase 8：Runtime Packaging

最终用户拿到 Shutu-Knowledge 后：

必须知道哪些东西：

```text
bundled
auto-downloaded
system dependency
optional
```

---

# 三十七、Packaging Manifest

建议建立：

```text
runtime-manifest.json
```

或项目现有等价机制。

记录：

```text
component
version
platform
architecture
source
checksum
license
install mode
```

---

# 三十八、不得下载未固定版本的 runtime

不要：

```text
download latest
```

必须：

```text
固定版本
固定 URL/source
checksum
```

确保可复现。

---

# 三十九、不要从不可信来源下载二进制

Runtime/model 下载源必须：

```text
官方
可信 registry
正式 release
```

并验证：

```text
checksum
```

必要时 signature。

---

# 四十、模型权重同样做 manifest

包括：

```text
model name
revision
files
checksum
dimension
runtime
license
```

---

# 四十一、Phase 9：Doctor

当前 Doctor 已能报告缺失 runtime。

现在要升级成真正可行动诊断。

至少：

```text
Embedding Runtime
Embedding Model
Reranker Runtime
Reranker Model
PDF Renderer
OCR
JBIG2
JPX
Legacy Office
Database
Storage
Extension integration
```

---

# 四十二、Doctor 输出

每项：

```text
PASS
WARN
FAIL
```

同时：

```text
detected version
path
ready state
last error
remediation
```

---

# 四十三、Doctor 不得自动掩盖错误

例如：

```text
Embedding artifact exists
runtime cannot load
```

必须：

```text
FAIL
```

不能：

```text
PASS because model downloaded
```

---

# 四十四、Web Model / Runtime UI

Models/Settings 页面至少能看见：

```text
Installed
Runtime Missing
Loading
Ready
Failed
```

对于外部 system dependency：

```text
Not Detected
Detected
Unsupported Version
```

---

# 四十五、UI 与 Doctor 状态必须来自同一底层事实

不要 Web 和 CLI 各自判断一套。

应复用：

```text
Runtime Status Service
```

或现有等价机制。

---

# 四十六、Phase 10：Fresh-machine E2E

这是本轮最终 Gate。

不是：

```text
developer machine test
```

而是：

```text
fresh environment
```

---

# 四十七、Fresh Environment 要求

测试环境不能预先依赖：

```text
开发目录模型
自定义 helper
隐藏环境变量
local go.work
本地 runtime 路径
已有 DB
已有 cache
```

如果依赖系统软件：

必须明确记录。

---

# 四十八、Local Embedding Fresh E2E

```text
fresh install
→ start
→ select/download embedding
→ runtime ready
→ smoke inference
→ READY
→ create KB
→ import document
→ index
→ semantic query
→ relevant result
```

必须 PASS。

---

# 四十九、Local Reranker Fresh E2E

```text
fresh install
→ install/download reranker
→ READY
→ retrieve candidates
→ rerank
→ expected ordering
```

---

# 五十、OCR Fresh E2E

```text
fresh install
→ import scanned PDF
→ renderer ready
→ OCR ready
→ text
→ chunk
→ index
→ retrieval
```

---

# 五十一、Legacy Office Fresh E2E

```text
fresh install
→ runtime discovery
→ import .doc
→ import .ppt
→ import .xls
→ convert/parse
→ index
→ retrieve
```

---

# 五十二、Offline Restart Gate

完成安装后：

```text
disable network
restart Shutu-Knowledge
```

至少：

```text
Embedding
Reranker
```

必须仍工作。

---

# 五十三、Corruption Gate

人为损坏一个：

```text
model
runtime
```

必须：

```text
READY → FAILED
Doctor detect
UI detect
remediation visible
```

不能 crash。

---

# 五十四、Delete / Reinstall Gate

删除模型：

```text
READY
→ removed
→ NOT_INSTALLED
```

重新下载：

```text
→ READY
```

---

# 五十五、性能验证

真实 runtime 后重新 benchmark。

至少：

```text
embedding load time
embedding single latency
embedding batch throughput
reranker latency
OCR page latency
PDF render latency
```

记录环境。

---

# 五十六、不要为了性能牺牲正确性

先保证：

```text
functional parity
```

再调：

```text
threads
batching
cache
quantization
```

---

# 五十七、模型/Runtime Licensing

新增：

```text
docs/runtime_license_inventory.md
```

至少：

| Component | Version | License | Redistributed? | Downloaded? | Attribution | Status |
|---|---|---|---|---|---|---|

---

# 五十八、Apache-2.0 Gate

Shutu-Knowledge 本身仍保持：

```text
Apache-2.0
```

任何新增 runtime 不得让仓库源代码许可状态变得不清晰。

---

# 五十九、如果 Runtime 许可证不适合直接 bundle

必须选：

```text
system dependency
或
auto-managed external runtime
```

不要隐藏许可证约束。

---

# 六十、不要复制 dsh-knowledge AGPL runtime 实现

仍然执行：

```text
行为参考
+
独立实现
```

不能为了速度复制：

```text
OCR helper
embedding worker
reranker child
PDF pipeline
```

AGPL 源码。

---

# 六十一、Source Reuse Inventory 更新

所有新增 runtime code 都更新：

```text
docs/source_reuse_inventory.md
```

---

# 六十二、System Prompt GAP 不在本轮解决

当前：

```text
GAP-001
```

如果仍受 Agent Contract 限制：

保持：

```text
BLOCKED_BY_AGENT / NON-BLOCKING
```

本轮不修改 Agent。

---

# 六十三、Health GAP 不在本轮强制解决

`GAP-002` 如果 Agent 仍只有汇总 health：

保持非阻断。

Knowledge 自己的 Doctor / Web 可以更详细。

---

# 六十四、测试要求

每个 runtime 都必须包含：

```text
unit
integration
real runtime
failure
cancellation
restart
clean environment
```

---

# 六十五、禁止只用 Mock 宣称完成

Mock 可以保留 unit test。

但是最终 Gate 必须是真：

```text
real model
real OCR
real renderer
real converter
```

---

# 六十六、Race Test

Go：

```bash
go test -race -count=1 ./...
```

新增 runtime manager 相关测试建议额外：

```bash
go test -race -count=20 <runtime packages>
```

---

# 六十七、Go Gate

每阶段保持：

```bash
go build ./...
go vet ./...
go test -count=1 ./...
```

---

# 六十八、Web Gate

继续：

```text
typecheck
unit tests
build
verify
E2E
```

按真实 scripts。

---

# 六十九、Clean Clone

最终必须：

```text
GitHub clone
→ no local Agent
→ no helper source tree
→ install/manage runtime
→ full tests
```

---

# 七十、GitHub CI

CI 必须覆盖至少：

```text
Go
Web
architecture gates
```

可以根据 runtime 成本将真实模型/OCR测试分：

```text
CI fast suite
+
release/runtime suite
```

但 Release Gate 必须跑真实 runtime suite。

---

# 七十一、不要让 CI 偷用开发机 runtime

Linux CI 上：

```text
所有需要安装的 runtime
```

必须通过 workflow 明确 setup。

---

# 七十二、提交策略

建议：

```text
Commit 1
feat: add production local embedding runtime

Commit 2
feat: add production local reranker runtime

Commit 3
feat: add PDF render and OCR runtime

Commit 4
feat: add legacy Office runtime integration

Commit 5
feat: unify runtime lifecycle and doctor

Commit 6
test: add fresh-install runtime parity suite

Commit 7
docs: mark out-of-box parity status
```

---

# 七十三、不得跨阶段隐藏失败

例如 Phase 1 Embedding 失败：

不要先把文档写成：

```text
OUT-OF-BOX READY
```

等最终 Gate。

---

# 七十四、更新严格矩阵

持续维护：

```text
docs/out_of_box_parity_matrix.md
```

最终各项必须是：

```text
BUILT_IN
BUNDLED_RUNTIME
AUTO_MANAGED_EXTERNAL_RUNTIME
BLOCKED_BY_AGENT
NOT_APPLICABLE
FAIL
```

---

# 七十五、Local Embedding 最终允许状态

必须：

```text
BUILT_IN
BUNDLED_RUNTIME
或
AUTO_MANAGED_EXTERNAL_RUNTIME
```

不能：

```text
MANUAL_EXTERNAL_RUNTIME
```

---

# 七十六、Local Reranker 同上

---

# 七十七、OCR 同上

---

# 七十八、PDF Renderer 同上

---

# 七十九、Legacy Office

允许：

```text
AUTO_MANAGED_EXTERNAL_RUNTIME
```

例如自动发现 LibreOffice。

不强求把 Office Suite 打入 Knowledge ZIP。

---

# 八十、JBIG2 / JPX

必须最终明确：

```text
SUPPORTED_BY_RENDERER
```

或者：

```text
AUTO_MANAGED_RUNTIME
```

不能 Unknown。

---

# 八十一、最终 Out-of-Box 报告

更新：

```text
out_of_box_parity_report.md
```

必须明确：

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
Model lifecycle
Fresh install
Offline restart
Doctor
Licenses
```

---

# 八十二、最终验收：Local Embedding

必须：

```text
fresh machine
+
real model
+
real inference
+
real vector retrieval
```

PASS。

---

# 八十三、最终验收：Reranker

必须：

```text
real model
+
real scoring
+
real reordering
```

PASS。

---

# 八十四、最终验收：OCR

必须：

```text
scanned PDF
→ real OCR
→ chunks
→ retrieval
```

PASS。

---

# 八十五、最终验收：PDF

必须：

```text
real full-page render
```

PASS。

---

# 八十六、最终验收：Legacy Office

必须：

```text
.doc
.ppt
.xls
```

都完成真实处理。

---

# 八十七、最终验收：Model Lifecycle

必须：

```text
download
verify
install
load
smoke
READY
restart
offline
delete
reinstall
```

PASS。

---

# 八十八、最终验收：Architecture

必须：

```text
0 shutu-agent modifications
0 shutu-agent/internal imports
```

---

# 八十九、最终验收：License

必须：

```text
Apache-2.0 posture remains valid
runtime/model licenses documented
no AGPL copied code
```

---

# 九十、最终验收：Regression

原：

```text
SHUTU-KNOWLEDGE V1 RELEASE READY
```

不能因为 runtime 补齐而回归。

---

# 九十一、最终状态

只有所有 runtime Gate PASS，才能写：

# SHUTU-KNOWLEDGE OUT-OF-BOX PARITY READY

允许同时保留：

```text
GAP-001:
BLOCKED_BY_AGENT / NON-BLOCKING

GAP-002:
NON-BLOCKING
```

因为这两个不属于本地 runtime completeness。

---

# 九十二、如果还有任何 Runtime 需要用户自己开发 Helper

则必须：

# SHUTU-KNOWLEDGE OUT-OF-BOX PARITY NOT READY

不能因为接口完整就改成 READY。

---

# 九十三、最终实施报告

新增：

```text
runtime_implementation_report.md
```

必须列：

```text
Runtime
Implementation
Packaging mode
Version
License
Platforms
Real test evidence
Fresh install result
Failure behavior
Final status
```

---

# 九十四、最终环境记录

至少记录：

```text
Windows version
Linux distribution
CPU
RAM
Go
Node
Embedding model
Reranker model
OCR runtime
PDF runtime
Office runtime
```

---

# 九十五、最终原则

每完成一项都问：

> 如果把我的开发机清空，只留下正式 Shutu-Knowledge 安装包和产品文档，普通用户是否能够完成这个任务，而不需要自己写代码或接 helper？

如果答案是：

```text
NO
```

这项就还没有真正完成。

---

# 九十六、开始执行

不要再次先花大量时间重做全项目审计。

首先从：

# Phase 1 — Local Embedding Runtime

直接开始：

```text
1. 审计现有 embedding seam
2. 确定 production runtime 方案
3. 做许可证检查
4. 实现真实 runtime
5. 真实模型测试
6. fresh-install E2E
7. 提交
```

Phase 1 PASS 后，再进入 Local Reranker。

继续执行直到所有 Phase 完成，不需要等待人工逐阶段确认。

如果遇到：

```text
许可证 blocker
必须修改 shutu-agent
必须改变公开 Extension Contract
```

才停止，并生成明确 blocker 报告。