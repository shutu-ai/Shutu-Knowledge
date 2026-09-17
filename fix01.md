# 任务：完成 Shutu-Knowledge V1 Release Readiness 最终收尾

目标项目：

```text
https://github.com/shutu-ai/Shutu-Knowledge
```

当前已知基线：

```text
master
commit: 92490a9
```

如果当前 `origin/master` 已有更新提交，以最新版本为准。

---

# 一、背景

`Shutu-Knowledge` 当前已经完成：

```text
Knowledge V1
dsh-knowledge capability equivalence
Native Context Provider
Knowledge Tools
Web UI
Lifecycle
Health
Retrieval
Embedding
Reranker
OCR
Document ingestion
Hybrid retrieval
Agent Extension Integration
```

架构目标已经基本达成：

```text
Shutu-Knowledge
        │
        │ 单向依赖
        ▼
shutu-agent Extension Platform v1
```

并且：

```text
shutu-agent
```

在 Knowledge 项目中必须保持：

```text
READ ONLY
```

---

# 二、本任务不是功能开发

本任务目标只有：

# 将当前“功能 READY”升级为“公开仓库 RELEASE READY”。

只处理以下 4 类事项：

```text
1. GitHub CI 修复
2. Go Module 可移植构建
3. Clean-clone Release Gate
4. License / Source Provenance Release Audit
```

明确禁止借机：

```text
新增 Knowledge 功能
重构 Retrieval
修改 RAG
修改 Agent Extension Contract
修改 shutu-agent
增加新协议
重做 Web UI
改变数据库模型
修改现有业务行为
```

除非发现确定性的 release blocker。

---

# 三、首先确认仓库状态

开始前执行：

```bash
git status
git branch --show-current
git log -5 --oneline
git remote -v
```

必须确认：

```text
当前仓库 = Shutu-Knowledge
```

并记录：

```text
origin/master HEAD
```

如果工作区存在用户未提交修改：

```text
不得 reset
不得 clean
不得覆盖
```

只避开相关文件。

---

# 四、确认 shutu-agent 不可修改

如果本机有：

```text
C:\dev-projects\Agent\shutu-agent
```

或其它本地 Agent repo：

开始前记录：

```bash
git -C <SHUTU_AGENT_PATH> rev-parse HEAD
git -C <SHUTU_AGENT_PATH> status --porcelain
```

结束后再次检查。

要求：

```text
Knowledge release work
不得修改 shutu-agent
```

如果 Agent repo 出现由本任务造成的 tracked change：

```text
FINAL RESULT = FAIL
```

---

# 五、P0：修复 GitHub Actions CI

检查：

```text
.github/workflows/
```

当前已知可能存在 YAML 解析问题，例如：

```yaml
- name: Architecture gate: no shutu-agent internal imports
```

这种未加引号且包含 `:` 的 value 可能导致 GitHub Actions workflow syntax error。

必须：

1. 检查所有 workflow YAML；
2. 修复语法；
3. 不只是修已知一行；
4. 验证所有 workflow 可以被 GitHub Actions 正常解析。

例如需要时改成：

```yaml
- name: "Architecture gate: no shutu-agent internal imports"
```

但不要机械修改所有字符串。

---

# 六、CI 必须真正执行测试，而不是只“语法通过”

最终 CI 至少应该真正运行：

```text
checkout
Go setup
Node setup
dependencies
architecture gates
go build
go vet
go test
race tests
Web typecheck
Web tests
Web build
Web verify
```

如果项目已有 Playwright E2E，并且 CI 环境适合运行：

继续保留。

如果 E2E 需要额外 browser install：

正确配置：

```text
playwright install
```

或项目现有方式。

---

# 七、Architecture Gate 必须保留

CI 必须继续检查：

```text
Shutu-Knowledge production code
不得 import shutu-agent/internal/...
```

不能为了让 CI 通过把这个 gate 删除。

还应检查：

```text
不得出现 Agent source modification dependency
```

如果已有脚本：

复用。

---

# 八、P0：移除 go.mod 本机绝对路径

检查当前：

```text
go.mod
```

是否仍有类似：

```go
replace github.com/shutu-ai/shutu-agent => C:/dev-projects/Agent/shutu-agent
```

这种 committed local path。

如果存在：

# 必须删除。

公开仓库的：

```text
go.mod
```

不能依赖开发者本机目录。

---

# 九、使用正式 shutu-agent 版本

检查：

```text
https://github.com/shutu-ai/shutu-agent
```

当前已经发布并适合 Knowledge 使用的正式版本。

如果当前稳定版本是：

```text
v0.1.0
```

则优先：

```go
require github.com/shutu-ai/shutu-agent v0.1.0
```

并删除本地 replace。

但：

> 必须先验证该 tag 真实包含 Knowledge 当前使用的 Extension API。

不要只因为版本号存在就机械替换。

---

# 十、验证 Extension API compatibility

在切换正式 module dependency 后，检查 Knowledge 实际使用的：

```text
sdk/extension
```

API 是否存在于该正式 Agent release。

如果不存在：

不要重新加入：

```text
C:/...
```

这种本地 replace。

必须记录：

```text
Agent release/version gap
```

然后判断：

```text
是否存在更新的 Agent release
```

如果 Agent master 才有接口而没有 release tag：

记录成：

```text
RELEASE BLOCKER
```

不要私自修改 Agent 或用伪版本绕开公开 release 要求。

---

# 十一、本地开发推荐使用 go.work

如果开发者希望继续使用本地：

```text
shutu-agent
```

源码联调：

不要写进 `go.mod`。

可以在本地使用：

```text
go.work
```

例如概念：

```go
go 1.xx

use (
    .
    ../Agent/shutu-agent
)
```

但：

```text
go.work
```

是否提交，要根据项目现有政策决定。

如果它包含个人机器布局：

```text
不要提交
```

加入 `.gitignore` 或文档说明。

---

# 十二、不得使用 replace 掩盖版本问题

以下属于 Release Gate FAIL：

```go
replace github.com/shutu-ai/shutu-agent => ../shutu-agent
```

如果公开构建必须依赖旁边刚好存在一个 Agent repo。

Release 目标必须是：

```text
git clone Shutu-Knowledge
        ↓
go mod download
        ↓
直接解析公开 Agent module
        ↓
build
```

---

# 十三、go.mod / go.sum 整理

完成 dependency 修复后执行：

```bash
go mod tidy
```

检查：

```text
go.mod
go.sum
```

只包含必要变化。

确认没有：

```text
local path
file://
developer-specific path
temporary pseudo module
```

---

# 十四、P0：Clean Clone Release Gate

这是本任务最重要的最终验收。

不能只在当前开发工作目录执行测试。

必须创建一个：

```text
全新的临时目录
```

从 GitHub clone 当前候选提交。

例如：

```bash
git clone https://github.com/shutu-ai/Shutu-Knowledge.git
cd Shutu-Knowledge
```

确认环境中：

```text
不存在旁边的 shutu-agent repo dependency
```

不能依赖：

```text
C:\dev-projects\Agent
```

---

# 十五、Clean Clone Go Gate

在 clean clone 中执行：

```bash
go mod download
go build ./...
go vet ./...
go test -count=1 ./...
```

如果环境支持：

```bash
CGO_ENABLED=1 go test -race -count=1 ./...
```

Windows / Linux 的 CGO 环境不同，可以根据真实平台调整。

关键是：

```text
race tests 必须真实执行
```

不能因为 release cleanup 临时删掉。

---

# 十六、Clean Clone Web Gate

在 clean clone 中：

进入 Web 项目实际目录，按：

```text
package.json
```

定义执行真实命令。

例如：

```bash
npm ci
npm run typecheck
npm test
npm run build
npm run verify
```

如果已有：

```text
Playwright E2E
```

继续运行。

不要凭记忆使用不存在的 script。

先读取：

```text
package.json
```

---

# 十七、禁止 clean clone 偷用本地 cache 证明成功

Go/NPM cache 可以使用，这是正常的。

但必须确认：

```text
源码依赖解析
```

没有通过：

```text
本地 replace
go.work pointing to local Agent
NODE_PATH local project
symlink
```

等方式偷用开发目录。

---

# 十八、GitHub CI 与 Clean Clone 必须同时通过

Release Ready 不能只满足：

```text
本地 clean clone PASS
```

也不能只满足：

```text
GitHub Actions PASS
```

必须：

```text
Local/Fresh Clone Gate
+
GitHub CI Gate
```

同时 PASS。

---

# 十九、P1：License Audit

当前项目计划：

```text
Apache-2.0
```

必须重新检查：

```text
LICENSE
THIRD_PARTY_NOTICES.md
docs/source_reuse_inventory.md
go.mod
package.json
third-party binaries/models
```

确认许可证策略一致。

---

# 二十、重点：dsh-knowledge 为 AGPL 来源

参考项目：

```text
https://github.com/Soren-ABT/dsh-knowledge
```

如果其许可证是：

```text
AGPL-3.0
```

则当前 Apache-2.0 项目必须继续坚持：

```text
behavioral reference
+
clean/original implementation
```

不能未经处理直接复制 AGPL 源码。

---

# 二十一、Source Provenance Audit

做一次针对源码来源的 Release Audit。

目标不是证明两个项目“完全不像”，而是确认没有未经记录的直接复制。

重点检查：

```text
独特注释
错误信息字符串
长代码片段
独特函数拆分
独特数据结构
独特 SQL
独特 UI 文案
独特测试数据
独特 regex
复杂算法实现
```

如果这些明显来自 dsh-knowledge：

必须进一步核实是否：

```text
直接复制
翻译复制
行为重新实现
公共算法惯用实现
```

---

# 二十二、生成 provenance 报告

生成：

```text
docs/release_source_provenance_audit.md
```

至少包含：

```text
Area
Reference source
Implementation approach
Direct source copied?
License implication
Evidence
Result
```

Result：

```text
PASS
REVIEW
BLOCKED
```

---

# 二十三、不要用简单文本相似度自动下结论

可以使用：

```text
grep
diff
similarity tooling
```

辅助检查。

但不能：

```text
相似度 < X%
→ 自动认定无许可证问题
```

需要结合：

```text
代码结构
独特表达
算法通用性
来源记录
```

判断。

---

# 二十四、如果发现直接复用 AGPL 代码

不要隐藏。

立即：

1. 标记具体文件；
2. 记录来源；
3. 判断是否能在 Apache-2.0 项目中保留；
4. 如果不适合：

   * 重新独立实现；
   * 不参考该代码逐行改写；
   * 基于已定义的输入/输出/测试行为重构。

必须更新：

```text
source_reuse_inventory.md
THIRD_PARTY_NOTICES.md
```

---

# 二十五、第三方依赖许可证

检查 Go：

```text
go.mod
```

和 Web：

```text
package-lock.json / package.json
```

中核心生产依赖的许可证。

重点检查是否有：

```text
GPL
AGPL
SSPL
BUSL
non-commercial
source-available
```

等可能和当前发行策略冲突的依赖。

不要只检查 dsh-knowledge。

---

# 二十六、模型和外部组件

如果项目涉及：

```text
Embedding model
Reranker model
OCR model
MinerU
external parser
```

需要区分：

```text
源码许可证
模型许可证
模型权重使用条款
```

不要因为代码 Apache-2.0，就把所有模型称为 Apache-2.0。

在文档里正确标明：

```text
optional external dependency / model license applies separately
```

---

# 二十七、shutu-agent License 检查

检查：

```text
https://github.com/shutu-ai/shutu-agent
```

是否有明确：

```text
LICENSE
```

以及 Knowledge 依赖其：

```text
sdk/extension
```

时应如何声明。

如果 Agent 还没有明确许可证：

不要修改 Agent。

在本项目报告中记录：

```text
UPSTREAM LICENSE GOVERNANCE ITEM
```

如果你拥有 Agent 项目，可以另行产生独立 Agent maintenance requirement。

但：

```text
Knowledge release task
不得修改 Agent
```

---

# 二十八、THIRD_PARTY_NOTICES

完善：

```text
THIRD_PARTY_NOTICES.md
```

至少准确描述：

```text
shutu-agent
dsh-knowledge reference status
主要第三方 runtime/library
可选模型/外部组件
```

避免写模糊：

```text
License: per repository
```

如果可以明确具体许可证，应明确写出。

如果当前无法确认：

写：

```text
license verification required
```

而不是猜测。

---

# 二十九、README Release 信息

检查 README：

不要宣称：

```text
100% dsh-knowledge equivalent
```

如果 equivalence matrix 仍存在：

```text
BLOCKED
```

项。

应准确描述，例如：

```text
Target capability coverage complete except documented non-blocking upstream Extension Contract limitations.
```

或者用项目自己的更清晰措辞。

---

# 三十、GAP-001 / GAP-002

重新检查：

```text
docs/agent_extension_gap_report.md
```

当前：

```text
GAP-001
GAP-002
```

如果仍然：

```text
non-blocking
```

保留。

本任务禁止：

```text
为了消除 Gap 而修改 shutu-agent
```

只需要确认：

```text
当前 release 可以安全工作
```

---

# 三十一、Release 文档状态必须一致

检查这些文件之间不能互相矛盾：

```text
README.md
dsh_knowledge_equivalence_matrix.md
agent_extension_gap_report.md
shutu_knowledge_implementation_report.md
release_source_provenance_audit.md
```

例如不能同时出现：

```text
全部 100% PASS
```

和：

```text
GAP-001 BLOCKED
```

却没有解释。

---

# 三十二、生成正式 Release Gate 报告

生成：

```text
release_readiness_report.md
```

必须包含以下 Gate。

---

# Gate 1 — Repository Portability

检查：

```text
go.mod 无绝对路径
无 committed developer path
无必须存在的 sibling repo
```

必须：

```text
PASS
```

---

# Gate 2 — Fresh Clone Build

全新 clone：

```text
go mod download
go build ./...
go vet ./...
go test ./...
```

必须：

```text
PASS
```

---

# Gate 3 — Race

真实 race suite：

```text
PASS
```

如果某平台无法运行，必须说明：

```text
在哪个平台运行通过
```

不能简单 SKIP 后仍判完全 PASS。

---

# Gate 4 — Web

```text
npm ci
typecheck
tests
build
verify
E2E
```

按真实项目脚本执行。

必须：

```text
PASS
```

---

# Gate 5 — GitHub CI

最新：

```text
origin/master
```

对应 workflow：

```text
green
```

必须：

```text
PASS
```

---

# Gate 6 — Architecture

确认：

```text
0 production imports of shutu-agent/internal
shutu-agent repository unchanged
```

必须：

```text
PASS
```

---

# Gate 7 — One-way Dependency

必须仍然是：

```text
Shutu-Knowledge
       ↓
shutu-agent Extension Platform v1
```

不能出现 Agent → Knowledge。

必须：

```text
PASS
```

---

# Gate 8 — License

确认：

```text
Apache-2.0 project files
AGPL reference isolation
third-party notices
dependency license audit
```

无已知 blocking conflict。

必须：

```text
PASS
```

或：

```text
BLOCKED
```

不得模糊。

---

# Gate 9 — Source Provenance

确认：

```text
没有未记录的 dsh-knowledge 源码直接复制
```

如果存在需要人工法律审查：

明确：

```text
REVIEW REQUIRED
```

---

# Gate 10 — Capability Status

确认：

```text
V1 功能状态不因 release cleanup 回归
```

所有已有核心测试继续 PASS。

---

# 三十三、最终 Release 判定

只有：

```text
Gate 1–10
```

全部通过，才能更新：

```text
SHUTU-KNOWLEDGE V1 READY
```

为：

# `SHUTU-KNOWLEDGE V1 RELEASE READY`

如果其中任何 P0 Gate 失败：

必须：

```text
SHUTU-KNOWLEDGE V1 FUNCTIONALLY READY
BUT NOT RELEASE READY
```

---

# 三十四、不要伪造 CI 成功

如果代码已经 push，但 GitHub Actions 仍在失败：

不能因为本地测试通过而写：

```text
CI PASS
```

必须读取真实 GitHub workflow result。

如果 workflow 由于平台临时故障失败：

记录真实原因。

---

# 三十五、提交策略

建议拆成少量清晰提交：

```text
Commit 1
fix: make release build portable
```

包括：

```text
go.mod
go.sum
local workspace handling
```

---

```text
Commit 2
fix: repair CI release gates
```

包括：

```text
.github/workflows/*
```

---

```text
Commit 3
docs: complete release provenance and license audit
```

---

最后如果需要：

```text
Commit 4
docs: mark V1 release ready
```

只有 CI 真正通过后才能做最后状态更新。

---

# 三十六、不要创建无意义版本膨胀

本任务只是 release fix。

不要修改：

```text
V1 architecture
database schema
retrieval ranking
Extension Contract
Knowledge API
```

除非发现确定性 release blocker。

---

# 三十七、最终 Clean-room Release Test

最后再创建一个新的临时目录：

```text
release-validation/
```

执行：

```text
clone
dependency install
build
test
race
web test/build
```

并记录：

```text
OS
Go version
Node version
commit SHA
```

到：

```text
release_readiness_report.md
```

---

# 三十八、Git 状态

任务结束前：

```bash
git status
git diff --check
```

必须：

```text
working tree clean
```

除非存在用户原有未跟踪文件。

不要提交：

```text
screenshots
temporary logs
test databases
model downloads
node_modules
build artifacts
local go.work
```

除非项目明确要求。

---

# 三十九、最终检查 shutu-agent

再次执行：

```bash
git -C <SHUTU_AGENT_PATH> rev-parse HEAD
git -C <SHUTU_AGENT_PATH> status --porcelain
```

与任务开始基线比较。

必须证明：

```text
shutu-agent was not modified by this task
```

---

# 四十、最终输出

完成后给出：

```text
1. 最新 commit SHA
2. GitHub CI URL / result
3. clean-clone validation result
4. go.mod dependency result
5. license/provenance result
6. GAP-001 / GAP-002 状态
7. shutu-agent unchanged evidence
```

以及最终状态，只允许二选一：

```text
SHUTU-KNOWLEDGE V1 RELEASE READY
```

或者：

```text
SHUTU-KNOWLEDGE V1 NOT RELEASE READY
```

---

# 四十一、最终原则

这次任务只解决：

> 代码已经能工作，是否也能作为一个独立、公开、可复现、许可证边界清晰的仓库被第三方 clone、build、test 和使用。

不要把 Release Hardening 重新变成 Feature Development。
