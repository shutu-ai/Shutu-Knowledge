# 任务：完成 Shutu-Knowledge Final Runtime Parity Release Gate

目标项目：

```text
https://github.com/shutu-ai/Shutu-Knowledge
```

Agent：

```text
https://github.com/shutu-ai/shutu-agent
```

当前已完成真实 Runtime 实现，但尚未提交当前工作区。

当前已报告完成：

```text
- 内置 Node 22.14.0
- Transformers.js
- Tesseract.js
- PDF.js
- AnyDoc Runtime

- Local Embedding Runtime
- Local Reranker Runtime
- OCR Runtime
- PDF Full-page Rendering
- JBIG2 / JPX
- Legacy Office .doc/.ppt/.xls
- Runtime lifecycle
- health
- timeout/cancel
- offline restart
- corruption recovery
- Doctor/Web unified status
- Windows/Linux process safety
- Linux runtime-release CI
- runtime license inventory
```

已存在关键实现：

```text
runtime_implementation_report.md

internal/runtime/assets/managed-runtime.mjs

scripts/runtime_smoke.go

.github/workflows/ci.yml
```

当前状态：

```text
实现完成
但尚未：
- commit
- push
- 用最终 commit 做 fresh clone
- 跑最终 GitHub CI
- 验证正式发行包 runtime 完整性
- 完成最终 Out-of-Box parity 状态收口
```

本任务目标是：

# 不新增功能，只完成最终冻结、验证、发布证据和 parity 判定。

最终结果只能是：

```text
SHUTU-KNOWLEDGE OUT-OF-BOX PARITY READY
```

或者：

```text
SHUTU-KNOWLEDGE OUT-OF-BOX PARITY NOT READY
```

---

# 一、严格限制任务范围

本次任务允许：

```text
1. 审核当前未提交 Runtime 改动
2. 修复仅由最终验证暴露出的 release-blocking correctness 问题
3. 完善测试
4. 完善 packaging
5. 完善 license/provenance
6. 完善 parity/report 文档
7. commit / push
8. GitHub CI
9. fresh clone
10. release-package validation
```

明确禁止：

```text
新增 Knowledge 业务功能
新增新的 Parser 类型
新增新的 Retrieval 算法
重新设计 Embedding abstraction
重新设计 Reranker abstraction
重新设计 Auto-RAG
修改 Agent Extension Protocol
修改 Agent SDK
修改 shutu-agent
大规模 Web redesign
数据库架构重构
无关性能优化
```

原则：

> 本阶段是 RELEASE FREEZE，不是 Feature Development。

---

# 二、不要立即提交

第一步先完整审查当前工作区。

执行：

```bash
git status
git branch --show-current
git rev-parse HEAD
git log -10 --oneline
git diff --stat
git diff --check
```

记录 baseline HEAD。

必须确认：

```text
repo = Shutu-Knowledge
branch = master
```

---

# 三、保护用户原有未跟踪文件

如果存在：

```text
.codegraph
screenshots
logs
model cache
runtime cache
node cache
release-validation
temporary files
```

不得：

```text
git reset --hard
git clean
删除
覆盖
```

只提交本任务真正需要进入仓库的内容。

---

# 四、确认 shutu-agent 只读

找到本地 shutu-agent repo 后记录：

```bash
git -C <AGENT_PATH> rev-parse HEAD
git -C <AGENT_PATH> status --porcelain
```

结束时重复。

必须满足：

```text
Agent HEAD unchanged
本任务没有造成 Agent tracked changes
```

否则：

```text
FINAL = NOT READY
```

---

# 五、审查当前 Runtime Diff

重点阅读所有新增/修改文件。

至少覆盖：

```text
internal/runtime/**
internal/runtime/assets/**
internal/models/**
internal/embedding/**
internal/rerank/**
internal/parser/**
internal/ocr/**
scripts/runtime_smoke.go
web/**
.github/workflows/ci.yml
runtime_implementation_report.md
docs/out_of_box_parity_matrix.md
docs/runtime_dependencies.md
docs/runtime_license_inventory.md
THIRD_PARTY_NOTICES.md
```

如果实际目录不同，以真实项目为准。

---

# 六、确认没有把开发机环境误打进实现

搜索：

```text
C:\
D:\
/home/<user>
/Users/<user>
localhost-specific model path
developer cache
absolute Node path
absolute npm path
temporary download path
```

不得提交开发机绝对路径。

---

# 七、确认 Managed Runtime 真正自包含

重点检查：

```text
internal/runtime/assets/managed-runtime.mjs
```

确认正式运行时不是依赖：

```text
repo node_modules
开发机 npm global
开发机 NODE_PATH
本地 source checkout
```

除非这些依赖被正式 package/install 管理。

---

# 八、确认 Node 22.14.0 的真实交付方式

必须明确：

```text
BUNDLED_RUNTIME
或
AUTO_MANAGED_RUNTIME
```

不能只是：

```text
开发环境安装 Node 22.14.0
```

必须验证：

```text
fresh environment
没有系统 Node
Shutu-Knowledge 仍能启动 managed runtime
```

---

# 九、Node Runtime Gate

至少验证：

```text
runtime binary exists
version == expected pinned version
architecture correct
platform correct
checksum valid
spawn succeeds
stdin/stdout protocol works
stderr captured
shutdown works
```

---

# 十、不得从 latest 下载 Runtime

如果 Node 或其它 runtime 是下载式安装：

必须固定：

```text
version
platform
arch
URL/source
SHA-256
```

禁止：

```text
latest
stable latest
unversioned URL
```

---

# 十一、Transformers.js Runtime Gate

真实验证：

```text
runtime load succeeds
model tokenizer loads
embedding real inference
reranker real inference
```

不得只：

```text
import package PASS
```

---

# 十二、Local Embedding 最终 Gate

必须使用真实模型完成：

```text
fresh state
→ model installed/downloaded
→ runtime starts
→ model loads
→ smoke inference
→ READY
→ document embedding
→ vector index
→ query embedding
→ semantic retrieval
```

全部真实执行。

---

# 十三、Embedding READY 语义再次确认

状态：

```text
artifact complete
```

只能：

```text
INSTALLED
```

只有：

```text
artifact valid
+
runtime available
+
load PASS
+
real inference PASS
```

才能：

```text
READY
```

必须保留当前已修正语义。

---

# 十四、Embedding Failure Test

至少：

```text
missing runtime
corrupt model
wrong model revision
dimension mismatch
runtime crash
timeout
cancel
```

都不能误报 READY。

---

# 十五、Embedding Offline Restart

完成模型安装后：

```text
disable network
stop Knowledge
restart Knowledge
```

必须：

```text
runtime starts
model loads
inference works
retrieval works
```

不得重新联网下载。

---

# 十六、Local Reranker 最终 Gate

使用真实：

```text
query
candidate A
candidate B
candidate C
```

真实模型 scoring。

验证：

```text
ordering changes correctly
scores finite
batch works
```

---

# 十七、Reranker Failure Gate

如果 reranker optional：

```text
runtime failure
→ hybrid retrieval remains available
```

如果 required：

给明确 actionable error。

---

# 十八、Tesseract.js OCR Gate

真实运行：

```text
image OCR
scanned PDF OCR
Chinese
English
mixed
multi-page
rotated page if supported
```

不能只执行 fixture mock。

---

# 十九、PDF.js Full-page Render Gate

必须证明：

```text
PDF page
→ actual rasterization
→ image
→ OCR
```

不是简单：

```text
extract embedded text
```

---

# 二十、纯扫描 PDF Gate

准备没有 text layer 的真实 fixture。

必须：

```text
PDF render
→ OCR
→ extracted text
→ chunks
→ index
→ retrieval
```

PASS。

---

# 二十一、JBIG2 Gate

使用真实 JBIG2-containing PDF fixture。

必须证明：

```text
render succeeds
or decoder path succeeds
```

并保留真实测试证据。

如果测试文件其实不包含 JBIG2：

不能宣称 PASS。

---

# 二十二、JPX / JPEG2000 Gate

同理，必须真实 fixture。

验证：

```text
JPX page/image
→ render/decode
→ OCR/parser
```

---

# 二十三、不能用“PDF.js 理论支持”代替测试

最终 parity 判定要求：

```text
REAL FIXTURE PASS
```

不是：

```text
dependency documentation says supported
```

---

# 二十四、AnyDoc Runtime Gate

确认 AnyDoc 的：

```text
actual version
source
license
packaging mode
```

并执行真实 legacy Office test。

---

# 二十五、Legacy Office .doc Gate

真实：

```text
.doc
→ parse/convert
→ text
→ metadata
→ chunks
→ index
→ retrieval
```

---

# 二十六、Legacy Office .ppt Gate

真实：

```text
.ppt
→ parse/convert
→ slide content
→ chunks
→ retrieval
```

---

# 二十七、Legacy Office .xls Gate

真实：

```text
.xls
→ parse/convert
→ sheet/cell content
→ chunks
→ retrieval
```

---

# 二十八、Legacy Office Failure Tests

至少：

```text
corrupt .doc
corrupt .ppt
corrupt .xls
timeout
cancel
runtime unavailable
```

不得导致 worker/process crash。

---

# 二十九、统一 Runtime Lifecycle Gate

确认这些 runtime 没有各自偷偷维护完全不同的 lifecycle：

```text
Embedding
Reranker
OCR
PDF
Office
```

尽量通过当前统一 runtime manager 管理：

```text
discover
start
health
call
timeout
cancel
stop
restart
error
```

---

# 三十、Process Race Gate

重点检查当前实现是否存在：

```text
stdout/stderr read race
Wait-before-drain
Kill-vs-exit race
double close
context cancellation race
goroutine leak
zombie process
```

因为 Agent 之前已有类似 Linux CI 问题，不要在 Knowledge runtime 重复出现。

---

# 三十一、Windows Runtime Stress

至少：

```bash
go test -race ./internal/runtime -count=20
```

当前已通过则最终 commit 后重新运行。

如果存在专门 runtime process tests：

建议：

```bash
go test -race -count=100 -run '<critical runtime tests>' ./internal/runtime
```

---

# 三十二、Linux Runtime Gate

Linux/WSL 或真正 Linux runner：

```text
spawn
stdio
stderr
timeout
cancel
SIGTERM
SIGKILL
temporary files
permissions
offline restart
```

全部验证。

---

# 三十三、Linux GitHub Runtime Release Suite

当前已经新增 runtime-release CI。

检查 `.github/workflows/ci.yml`。

必须确保：

```text
不是只存在 workflow
而是真正会执行 runtime smoke
```

最终 GitHub Actions 中必须实际 PASS。

---

# 三十四、Doctor 与 Web 必须共享事实源

检查：

```text
Doctor
Web Settings
Web Models
Runtime API
```

不能各自推断 runtime 状态。

应共享当前统一的：

```text
runtime status/service
```

或真实等价机制。

---

# 三十五、状态语义

至少一致支持：

```text
NOT_INSTALLED
INSTALLED
RUNTIME_MISSING
LOADING
READY
FAILED
```

如果项目状态名不同没问题，但语义必须一致。

---

# 三十六、Runtime Corruption Recovery

至少真实破坏：

```text
runtime asset
或 model artifact
```

确认：

```text
READY → FAILED
Doctor FAIL
Web FAIL
actionable remediation
```

然后执行 repair/reinstall。

必须恢复：

```text
READY
```

---

# 三十七、Runtime Delete / Reinstall

测试：

```text
model READY
→ delete
→ state changes
→ reinstall
→ READY
```

不能依赖 restart 才刷新。

---

# 三十八、检查 release package 内容

在任何最终 READY 之前，必须真正构建正式发行包。

不是运行源码。

---

# 三十九、发行包必须包含所有 BUNDLED_RUNTIME

根据当前方案检查实际 package：

```text
Node 22.14.0
managed-runtime.mjs
required JS runtime assets
PDF.js
OCR assets
AnyDoc components
runtime manifest
default config
prompts if required
```

以实际设计为准。

---

# 四十、发行包不能依赖 repo node_modules

将发行包复制到完全独立目录。

确保没有：

```text
../node_modules
source checkout
npm cache
development JS
```

依赖。

---

# 四十一、发行包 Smoke

仅使用正式 package：

```text
extract
→ start
→ doctor
→ runtime smoke
→ embedding
→ reranker
→ OCR
→ PDF
→ Office
```

不能 fallback 到源码目录。

---

# 四十二、发行包 Offline Gate

正式 package 完成运行时安装/模型准备以后：

```text
断网
→ stop
→ start
```

必须核心本地 runtime 正常。

---

# 四十三、正式 Runtime Manifest

确认存在真实 manifest。

至少包含：

```text
component
version
platform
arch
source
checksum
license
packaging mode
```

如果当前 manifest 名称不同，保持项目现有设计。

---

# 四十四、Runtime Asset Integrity

对所有随包二进制/runtime asset：

建立 checksum。

安装/启动前验证必要 asset。

---

# 四十五、License Final Gate

这是 P0 release gate。

实际枚举发行包中第三方内容。

特别包括：

```text
Node.js 22.14.0
Transformers.js
Tesseract.js
PDF.js
AnyDoc
npm transitive runtime dependencies
embedding model
reranker model
tokenizer
OCR language/model assets
PDF decoder/runtime assets
```

---

# 四十六、不要只审顶层 package

需要审：

```text
transitive production dependencies
```

特别是最终随包分发的代码。

---

# 四十七、模型 License 独立审查

不要因为 inference runtime Apache/MIT 就认为模型可以随包分发。

必须分别确认：

```text
model license
weight redistribution
tokenizer files
model config
```

---

# 四十八、如果模型不随发行包分发

记录：

```text
download-on-demand
```

并保留：

```text
version/revision
source
license
checksum
```

---

# 四十九、Node.js 分发检查

确认当前 Node 官方 binary redistribution方式符合许可证/NOTICE要求。

将所需 attribution 写入：

```text
THIRD_PARTY_NOTICES.md
```

或现有对应文档。

---

# 五十、Transformers.js / Tesseract.js / PDF.js / AnyDoc

逐项记录：

```text
Version
License
Source
Redistributed?
Modified?
Attribution required?
```

---

# 五十一、更新 runtime license inventory

检查/更新：

```text
docs/runtime_license_inventory.md
```

不能残留：

```text
UNKNOWN
TODO
TBD
```

关键生产 runtime 若存在 UNKNOWN：

```text
FINAL = NOT READY
```

---

# 五十二、更新 THIRD_PARTY_NOTICES

必须和实际 package 一致。

不要列：

```text
未使用的 runtime
```

也不要漏：

```text
实际随包的 runtime
```

---

# 五十三、Source Provenance Gate

继续保持：

```text
dsh-knowledge = behavioral reference
```

检查此次 runtime 代码是否：

```text
复制/逐行翻译 AGPL runtime code
```

若有：

停止并报告。

---

# 五十四、不要修改 Apache-2.0 posture

项目本身仍：

```text
Apache-2.0
```

如果新增 dependency 导致发布许可不能成立：

```text
OUT-OF-BOX PARITY NOT READY
```

不得隐藏。

---

# 五十五、更新 docs/runtime_dependencies.md

最终用户文档必须明确：

```text
哪些 bundled
哪些 auto-downloaded
哪些 models downloadable
哪些 system dependencies
哪些 optional
```

目标是：

> 用户不需要看源码才能知道 Runtime 如何工作。

---

# 五十六、更新 docs/out_of_box_parity_matrix.md

重新逐项确认。

关键能力最终不能再是：

```text
MANUAL_EXTERNAL_RUNTIME
```

---

# 五十七、Embedding 最终状态

应根据真实设计标：

```text
BUNDLED_RUNTIME
或
AUTO_MANAGED_EXTERNAL_RUNTIME
```

---

# 五十八、Reranker 最终状态同上

---

# 五十九、OCR 最终状态同上

---

# 六十、PDF Renderer 最终状态同上

---

# 六十一、JBIG2 / JPX

如果由 PDF.js/decoder runtime 实际支持并有 fixture：

标明：

```text
SUPPORTED_BY_BUNDLED_RUNTIME
```

或矩阵已有等价状态。

---

# 六十二、Legacy Office

如果 AnyDoc runtime 随包或由 Knowledge 自动管理：

标：

```text
BUNDLED_RUNTIME
```

或：

```text
AUTO_MANAGED_EXTERNAL_RUNTIME
```

不能仍写 Manual Helper。

---

# 六十三、GAP-001 不伪装解决

当前：

```text
system prompt proactive guidance
```

如果仍然受 Agent Contract 限制：

保留：

```text
BLOCKED_BY_AGENT
NON-BLOCKING
```

不要因为 Runtime parity 完成就把它改 PASS。

---

# 六十四、GAP-002 同样保持真实状态

不要为了“全绿”修改历史事实。

---

# 六十五、Out-of-Box Parity 的定义必须写清楚

最终 READY 不等于：

```text
与 dsh-knowledge 每一行实现相同
```

而是：

```text
对应终端能力
在干净环境
无需用户开发 helper
可以真实完成
```

---

# 六十六、现有 V1 Release Ready 不回退

当前：

```text
SHUTU-KNOWLEDGE V1 RELEASE READY
```

保持。

新增独立状态：

```text
OUT-OF-BOX RUNTIME PARITY
```

---

# 六十七、提交前完整 Go Gate

执行：

```bash
go mod download
go build ./...
go vet ./...
go test -count=1 ./...
```

---

# 六十八、Race Gate

至少：

```bash
go test -race -count=1 ./...
go test -race ./internal/runtime -count=20
```

根据平台能力执行。

---

# 六十九、Web Gate

按 package.json 真实 scripts。

至少覆盖：

```text
contract test
typecheck
unit tests
build
E2E
```

如果当前 E2E 只 Chrome：

记录真实结果。

不要虚构 Firefox/WebKit。

---

# 七十、Runtime Smoke Gate

执行：

```text
scripts/runtime_smoke.go
```

或实际命令。

必须真实覆盖：

```text
Embedding
Reranker
OCR
PDF
Office
```

---

# 七十一、不要让 Smoke 使用 Mock Runtime

确认：

```text
managed-runtime.mjs
+
真实第三方 runtime
```

在执行。

---

# 七十二、Final Candidate Commit

所有本地验证通过后，创建候选提交。

建议：

```text
feat: complete out-of-box managed runtime parity
```

不要混入：

```text
screenshots
logs
runtime cache
models unless intentionally distributed
node_modules unless intentionally bundled strategy requires and legally reviewed
```

---

# 七十三、记录 Candidate SHA

例如：

```text
RUNTIME_PARITY_CANDIDATE=<SHA>
```

后续所有验证必须针对这个 SHA。

---

# 七十四、Push Candidate

推送：

```bash
git push origin master
```

---

# 七十五、GitHub CI 必须等待最终结果

检查对应 candidate commit 的：

```text
Go CI
Web CI
Architecture Gate
Linux Runtime Release Suite
```

全部：

```text
SUCCESS
```

---

# 七十六、如果 GitHub Linux CI 暴露 correctness 问题

允许修复：

```text
release-blocking runtime correctness
cross-platform portability
test determinism
packaging correctness
```

但不要借机扩功能。

每次修复后：

```text
new candidate commit
重新跑全部最终 Gates
```

---

# 七十七、Fresh Clone 必须来自 GitHub

远端 candidate CI PASS 后：

在新目录执行：

```bash
git clone https://github.com/shutu-ai/Shutu-Knowledge.git
```

确认 checkout 的就是最终 candidate。

---

# 七十八、Fresh Clone 禁止依赖本地 Agent

确认不存在：

```text
go.work
local replace
local Agent checkout
C:\dev-projects\Agent
```

---

# 七十九、Fresh Clone Go Gate

执行：

```bash
go mod download
go build ./...
go vet ./...
go test -count=1 ./...
```

可运行环境允许时执行 race。

---

# 八十、Fresh Clone Web Gate

执行真实：

```text
npm ci
typecheck
tests
build
E2E
```

---

# 八十一、Fresh Clone Runtime Gate

这是最关键的 Gate。

在 fresh clone：

```text
不复用开发目录 runtime
不复用开发目录模型
不复用开发 node_modules
```

执行：

```text
runtime install/setup
runtime smoke
```

---

# 八十二、Fresh Clone Local Embedding Gate

真实：

```text
install/download model
→ READY
→ embedding
→ retrieval
```

---

# 八十三、Fresh Clone Reranker Gate

真实：

```text
install model
→ READY
→ real rerank
```

---

# 八十四、Fresh Clone OCR/PDF Gate

真实 scanned PDF：

```text
render
→ OCR
→ index
→ retrieval
```

---

# 八十五、Fresh Clone JBIG2 / JPX Gate

使用真实 fixtures。

---

# 八十六、Fresh Clone Legacy Office Gate

真实：

```text
.doc
.ppt
.xls
```

全部处理 PASS。

---

# 八十七、Fresh Clone Offline Restart

runtime/model 准备好后：

```text
disconnect network
restart
```

至少：

```text
Embedding
Reranker
OCR/PDF if no first-run download needed
```

继续工作。

---

# 八十八、正式 Package 构建

如果项目已有 release script：

使用真实 script。

不得手工随意 zip 一个开发目录。

---

# 八十九、Package Inventory

输出正式 package 文件列表。

确认：

```text
required runtime assets present
dev artifacts absent
secret absent
runtime cache absent
personal config absent
```

---

# 九十、Package Secret Scan

至少检查：

```text
API key
token
password
cookie
.env
credentials
absolute paths
usernames
developer home
private endpoint
```

---

# 九十一、Package Runtime Smoke

只使用 package。

不从 repo 启动。

执行完整核心 runtime smoke。

---

# 九十二、Package SHA-256

计算：

```text
SHA-256
size
```

记录到最终报告。

---

# 九十三、更新 runtime_implementation_report.md

最终补充：

```text
Final candidate SHA
Windows result
Linux result
Fresh clone result
Package result
License result
```

---

# 九十四、新增/更新 Final Report

生成：

```text
final_runtime_parity_release_report.md
```

至少包含以下 Gates。

---

# Gate 1 — Runtime Implementation

```text
PASS / FAIL
```

---

# Gate 2 — Local Embedding Real Inference

```text
PASS / FAIL
```

---

# Gate 3 — Local Reranker Real Inference

```text
PASS / FAIL
```

---

# Gate 4 — OCR

```text
PASS / FAIL
```

---

# Gate 5 — PDF Full-page Rendering

```text
PASS / FAIL
```

---

# Gate 6 — JBIG2 / JPX

```text
PASS / FAIL
```

必须有真实 fixture evidence。

---

# Gate 7 — Legacy Office

```text
.doc PASS/FAIL
.ppt PASS/FAIL
.xls PASS/FAIL
```

---

# Gate 8 — Model Lifecycle

必须：

```text
install/download
verify
load
inference
READY
offline restart
corruption detection
recovery
```

全部 PASS。

---

# Gate 9 — Doctor / Web State Consistency

```text
PASS / FAIL
```

---

# Gate 10 — Windows

```text
PASS / FAIL
```

---

# Gate 11 — Linux

```text
PASS / FAIL
```

---

# Gate 12 — Race / Process Safety

```text
PASS / FAIL
```

---

# Gate 13 — Fresh Clone

```text
PASS / FAIL
```

---

# Gate 14 — Formal Package Runtime

```text
PASS / FAIL
```

---

# Gate 15 — Offline Package Restart

```text
PASS / FAIL
```

---

# Gate 16 — Runtime License

```text
PASS / FAIL
```

---

# Gate 17 — Source Provenance

```text
PASS / FAIL
```

---

# Gate 18 — GitHub CI

```text
PASS / FAIL
```

---

# Gate 19 — Agent Isolation

必须：

```text
Agent unchanged
0 shutu-agent/internal imports
```

---

# Gate 20 — Regression

原：

```text
SHUTU-KNOWLEDGE V1 RELEASE READY
```

所有核心回归继续 PASS。

---

# 九十五、最终状态矩阵

报告必须列：

| Capability | Runtime mode | Real E2E | Fresh clone | Package | Final |
|---|---|---:|---:|---:|---|
| Local Embedding | ... | PASS | PASS | PASS | READY |
| Local Reranker | ... | PASS | PASS | PASS | READY |
| OCR | ... | PASS | PASS | PASS | READY |
| PDF Renderer | ... | PASS | PASS | PASS | READY |
| JBIG2 | ... | PASS | PASS | PASS | READY |
| JPX | ... | PASS | PASS | PASS | READY |
| .doc | ... | PASS | PASS | PASS | READY |
| .ppt | ... | PASS | PASS | PASS | READY |
| .xls | ... | PASS | PASS | PASS | READY |

---

# 九十六、READY 的最终判定规则

只有关键 Runtime 能力不存在：

```text
MANUAL_EXTERNAL_RUNTIME
UNKNOWN
UNTESTED
```

并且真实 Fresh Clone + Formal Package E2E 全 PASS，才能：

# SHUTU-KNOWLEDGE OUT-OF-BOX PARITY READY

---

# 九十七、允许的非阻断 Gap

可以继续保留：

```text
GAP-001
BLOCKED_BY_AGENT / NON-BLOCKING

GAP-002
NON-BLOCKING
```

它们不属于 Runtime completeness。

---

# 九十八、如果 Formal Package 缺 Runtime

即使源码测试全部 PASS，也必须：

# SHUTU-KNOWLEDGE OUT-OF-BOX PARITY NOT READY

因为本任务评估的是：

```text
产品开箱能力
```

而不是：

```text
开发源码能力
```

---

# 九十九、如果只有开发机能运行

同样：

```text
NOT READY
```

---

# 一百、如果 Linux 真实 Runtime Gate 没跑

如果项目正式声明支持 Linux：

```text
NOT READY
```

或者准确降低平台声明。

不得虚构 Linux parity。

---

# 一百零一、Final Git Diff Review

提交后检查：

```bash
git show --stat HEAD
git diff <baseline>..HEAD --check
```

确认没有非 Runtime scope 的大规模意外改动。

---

# 一百零二、Final Repository Status

执行：

```bash
git status
```

项目 tracked 工作区必须干净。

用户原有未跟踪资源可以存在，但必须报告。

---

# 一百零三、再次确认 Agent

结束时：

```bash
git -C <AGENT_PATH> rev-parse HEAD
git -C <AGENT_PATH> status --porcelain
```

与 baseline 对比。

---

# 一百零四、最终输出内容

必须给出：

```text
Final commit SHA
GitHub CI run
Fresh clone path/result
Windows runtime result
Linux runtime result
Embedding real E2E
Reranker real E2E
OCR real E2E
PDF real E2E
JBIG2 real E2E
JPX real E2E
.doc/.ppt/.xls real E2E
offline restart
corruption recovery
formal package size
formal package SHA-256
license gate
provenance gate
Agent unchanged evidence
GAP-001
GAP-002
```

---

# 一百零五、最终结论只能二选一

全部关键 Gate PASS：

```text
SHUTU-KNOWLEDGE V1 RELEASE READY

SHUTU-KNOWLEDGE OUT-OF-BOX PARITY READY
```

否则：

```text
SHUTU-KNOWLEDGE V1 RELEASE READY

SHUTU-KNOWLEDGE OUT-OF-BOX PARITY NOT READY
```

并明确唯一剩余 blockers。

---

# 一百零六、最终原则

本轮不要再问：

> “代码里有没有对应 Runtime 接口？”

而要问：

> “我把正式 Shutu-Knowledge 发行包交给一台干净 Windows/Linux 机器，不带我的源码环境，不带我自己开发的 helper，不带隐藏 runtime，它是否真的能完成 Embedding、Reranking、OCR、PDF、JBIG2/JPX 和 Legacy Office？”

只有答案真实为：

```text
YES
```

才允许最终宣告：

# SHUTU-KNOWLEDGE OUT-OF-BOX PARITY READY

---

# 开始执行

现在不要重新做 capability discovery。

直接：

```text
审查当前 Runtime diff
→ 完成本地 final gates
→ candidate commit
→ push
→ GitHub CI
→ fresh clone
→ formal package
→ package runtime E2E
→ license/provenance
→ final parity matrix
→ final report
```

如果过程中出现普通的：

```text
cross-platform bug
process race
packaging bug
test determinism bug
runtime path bug
```

允许作为 release-blocking correctness fix 直接修复并重新验证。

只有出现以下情况才停止：

```text
必须修改 shutu-agent
必须改变 Extension Protocol
发现许可证不可合法发布
发现必须复制 AGPL 代码
需要重大架构重构
```

其余情况继续执行直到最终结论。