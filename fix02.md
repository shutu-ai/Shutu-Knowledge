# 任务：完成 Shutu-Knowledge V1 最终 Release Gate，并正式标记 RELEASE READY

目标项目：

```text
https://github.com/shutu-ai/Shutu-Knowledge
```

当前背景：

* `Shutu-Knowledge` V1 功能已经完成。
* dsh-knowledge capability equivalence 已完成。
* 单向依赖架构已经通过。
* GitHub CI、clean clone、Go/Web/E2E 已通过。
* 当前唯一 Release Blocker 原来是：

  * `shutu-agent v0.2.0` 没有明确许可证。
* 该问题现在已经解决：

  * `shutu-agent v0.2.1` 已正式发布。
  * License：Apache-2.0。
  * GitHub Release 已发布。
  * Extension Platform v1 无 breaking change。
  * `sdk/extension` 可作为公开 Go module 使用。

因此本任务目标是：

# 将 Shutu-Knowledge 对 shutu-agent 的依赖从 v0.2.0 升级到 v0.2.1，重新执行完整 Release Gate，解除 Gate 8 blocker，并正式宣告：

```text
SHUTU-KNOWLEDGE V1 RELEASE READY
```

---

# 一、严格限制任务范围

本次任务只允许：

```text
1. 升级 shutu-agent dependency
2. go.mod / go.sum 整理
3. release/license/provenance 文档状态更新
4. clean-clone release validation
5. GitHub CI validation
6. 最终 Release Ready 状态更新
```

明确禁止：

```text
新增 Knowledge 功能
修改 Retrieval
修改 RAG
修改 Embedding
修改 Reranker
修改 OCR
修改 Parser
修改 DB schema
修改 Web 产品功能
修改 Agent Extension Contract
修改 Tool 行为
修改 Context Provider 行为
修改 shutu-agent
重新设计架构
```

原则：

> 这是 Final Release Gate，不是 Feature Development。

---

# 二、确认当前仓库

开始前执行：

```bash
git status
git branch --show-current
git log -10 --oneline
git remote -v
```

必须确认：

```text
repository = Shutu-Knowledge
branch = master
```

记录：

```text
origin/master HEAD
```

如果存在用户未提交文件：

```text
不得 reset
不得 clean
不得覆盖
```

---

# 三、确认 shutu-agent 不可修改

如果本机存在：

```text
C:\dev-projects\Agent\shutu-agent
```

或其它 shutu-agent repo：

记录：

```bash
git -C <SHUTU_AGENT_PATH> rev-parse HEAD
git -C <SHUTU_AGENT_PATH> status --porcelain
```

任务结束后再次检查。

必须证明：

```text
shutu-agent HEAD unchanged
+
没有本任务造成的 tracked modification
```

本任务绝对禁止修改 Agent。

---

# 四、确认当前 Dependency Baseline

读取：

```text
go.mod
go.sum
```

确认当前是否仍然依赖：

```go
github.com/shutu-ai/shutu-agent v0.2.0
```

同时确认：

```text
没有本地 replace
没有绝对路径
没有 go.work 注入到 release build
```

---

# 五、升级到 shutu-agent v0.2.1

目标：

```go
require github.com/shutu-ai/shutu-agent v0.2.1
```

执行正式依赖升级。

建议：

```bash
go get github.com/shutu-ai/shutu-agent@v0.2.1
go mod tidy
```

然后检查：

```text
go.mod
go.sum
```

确保：

```text
v0.2.1
```

是真实公开 dependency。

禁止加入：

```go
replace github.com/shutu-ai/shutu-agent => ../shutu-agent
```

或：

```go
replace ... => C:/...
```

---

# 六、验证公开 SDK Compatibility

重点确认当前 Knowledge 实际 import：

```text
github.com/shutu-ai/shutu-agent/sdk/extension
```

在：

```text
v0.2.1
```

中可以直接：

```text
compile
test
run
```

不得依赖 Agent master。

如果 v0.2.1 与当前 Knowledge API 不兼容：

```text
FINAL RESULT = BLOCKED
```

不要通过 local replace 绕过。

---

# 七、确认 Apache-2.0 License

检查公开：

```text
github.com/shutu-ai/shutu-agent@v0.2.1
```

包含：

```text
LICENSE
```

并确认：

```text
Apache-2.0
```

。

最好通过：

```bash
go env GOPATH
```

定位 module cache，再检查：

```text
github.com/shutu-ai/shutu-agent@v0.2.1/LICENSE
```

实际存在。

---

# 八、更新 THIRD_PARTY_NOTICES

检查当前：

```text
THIRD_PARTY_NOTICES.md
```

如果仍写：

```text
shutu-agent
License: unknown
```

或：

```text
License: per repository
```

必须更新为明确：

```text
shutu-agent
Version: v0.2.1
License: Apache-2.0
```

如果项目已有更详细 attribution：

保持现有格式。

---

# 九、更新 Source Provenance / License Audit

检查：

```text
docs/release_source_provenance_audit.md
docs/source_reuse_inventory.md
release_readiness_report.md
```

把原来：

```text
shutu-agent license = BLOCKED
```

更新为：

```text
PASS
```

但不要改写历史事实。

建议写清楚：

```text
Previous blocker:
shutu-agent v0.2.0 had no explicit license.

Resolved:
shutu-agent v0.2.1 is published under Apache-2.0.
```

---

# 十、GAP-001 / GAP-002 保持不变

检查：

```text
docs/agent_extension_gap_report.md
```

当前：

```text
GAP-001
GAP-002
```

仍是：

```text
non-blocking Agent Contract limitations
```

本次：

```text
不要删除
不要修改 Agent
不要伪装成已解决
```

只确认：

```text
它们不阻塞 V1 Release Ready
```

---

# 十一、不要重新声明 100% 无 Gap

如果 equivalence matrix 仍存在：

```text
BLOCKED / non-blocking
```

项：

不要把 README 改成：

```text
100% identical to dsh-knowledge
```

应准确描述：

```text
V1 target capability coverage is release-ready with documented non-blocking upstream contract limitations.
```

或项目现有等价措辞。

---

# 十二、首先执行当前工作区回归

依赖升级后执行：

```bash
go mod download
go build ./...
go vet ./...
go test -count=1 ./...
```

并：

```bash
go test -race -count=1 ./...
```

如果 Windows CGO/race 有项目已有专用命令：

使用真实命令。

---

# 十三、Web 回归

读取：

```text
web/package.json
```

使用真实 script。

至少执行现有：

```bash
npm ci
npm run typecheck
npm test
npm run build
npm run verify
npm run test:e2e
```

如果 E2E 当前支持：

```text
Chromium
Firefox
WebKit
```

全部执行。

---

# 十四、Extension Integration Regression

重点确认：

```text
Knowledge Extension
        ↓
shutu-agent v0.2.1 public SDK
```

以下不回归：

```text
Manifest
Handshake
Context Provider
Auto RAG
Knowledge Tools
Approval metadata
Lifecycle
Health
Events
Web Contribution
Native Navigation
Shutdown
Restart
```

---

# 十五、重新验证单向依赖

搜索整个生产源码：

```text
shutu-agent/internal
```

必须：

```text
0 production imports
```

确认只有：

```text
github.com/shutu-ai/shutu-agent/sdk/extension
```

或其它正式公开 Extension API。

---

# 十六、重新验证 Agent 独立性

删除/disable Knowledge Extension 的集成测试继续应证明：

```text
Agent continues running
Knowledge menu disappears
Knowledge tools disappear
Knowledge context disappears
```

如果现有测试已经覆盖，只需重新运行，不重新设计。

---

# 十七、Clean Clone Release Validation

这是最终 Gate 的核心。

必须在新的、与当前开发目录隔离的位置从 GitHub clone：

```bash
git clone https://github.com/shutu-ai/Shutu-Knowledge.git
```

但注意：

> 如果当前 dependency upgrade commit 尚未 push，先 push release candidate，再从 GitHub clone该 commit/branch。

Clean clone 目录不得依赖：

```text
C:\dev-projects\Agent
local shutu-agent repo
local go.work
local replace
symlink to Agent
```

---

# 十八、Fresh Clone Go Validation

在 clean clone 中执行：

```bash
go mod download
go build ./...
go vet ./...
go test -count=1 ./...
go test -race -count=1 ./...
```

全部：

```text
PASS
```

---

# 十九、Fresh Clone Web Validation

在 clean clone 中执行真实 Web Gate：

```bash
npm ci
npm run typecheck
npm test
npm run build
npm run verify
npm run test:e2e
```

全部：

```text
PASS
```

---

# 二十、Fresh Clone 必须确认 dependency 来源

在 clean clone 中确认：

```bash
go list -m github.com/shutu-ai/shutu-agent
```

必须返回：

```text
github.com/shutu-ai/shutu-agent v0.2.1
```

并确保没有：

```text
replace
```

。

可以检查：

```bash
go list -m -json github.com/shutu-ai/shutu-agent
```

验证真实模块来源。

---

# 二十一、GitHub CI

Push candidate commit 后：

必须检查真实 GitHub Actions。

不能只依据本地：

```text
PASS
```

最终必须：

```text
latest CI
= SUCCESS
```

---

# 二十二、Architecture Gate 保持

CI 中必须继续保留：

```text
no shutu-agent/internal imports
```

Gate。

禁止为了通过 release 而删除架构保护。

---

# 二十三、License Gate

最终重新评估：

```text
Gate 8 — License
```

必须检查：

```text
Shutu-Knowledge = Apache-2.0
shutu-agent v0.2.1 = Apache-2.0
dsh-knowledge = AGPL behavioral reference only
source provenance = PASS
third-party audit = PASS
```

如果这些成立：

```text
Gate 8 = PASS
```

---

# 二十四、Source Provenance 不需要重新大审计

如果本次只有：

```text
go.mod
go.sum
docs
```

等 release cleanup 改动：

不需要重新对全部 Knowledge 源码做一次 2 万行 provenance audit。

只需要确认：

```text
dependency change
没有引入新的 source reuse
```

---

# 二十五、更新 Release Readiness Report

更新：

```text
release_readiness_report.md
```

必须逐项给出：

---

## Gate 1 — Repository Portability

```text
PASS
```

---

## Gate 2 — Fresh Clone Build

```text
PASS
```

---

## Gate 3 — Race

```text
PASS
```

---

## Gate 4 — Web

```text
PASS
```

---

## Gate 5 — GitHub CI

```text
PASS
```

---

## Gate 6 — Architecture

```text
PASS
```

---

## Gate 7 — One-way Dependency

```text
PASS
```

---

## Gate 8 — License

原：

```text
BLOCKED
```

目标：

```text
PASS
```

理由必须明确：

```text
shutu-agent v0.2.1 is now explicitly Apache-2.0 licensed.
```

---

## Gate 9 — Source Provenance

```text
PASS
```

---

## Gate 10 — Capability Status

```text
PASS
```

并保留：

```text
GAP-001 / GAP-002 non-blocking
```

说明。

---

# 二十六、更新 Implementation Report

检查：

```text
shutu_knowledge_implementation_report.md
```

把最终状态更新为：

```text
SHUTU-KNOWLEDGE V1 RELEASE READY
```

但仅在全部 Release Gate PASS 后。

---

# 二十七、更新 README

README 最终应明确：

```text
Status: V1 Release Ready
License: Apache-2.0
Agent dependency: shutu-agent v0.2.1
```

不要夸大：

```text
100% identical source implementation
```

---

# 二十八、版本号

检查项目当前版本策略。

如果尚未正式发布 V1：

根据项目已有约定决定：

```text
v1.0.0
```

或当前既定版本。

不要在本任务中擅自改变版本规划。

如果仅要求 Release Ready 而暂不打 tag：

只完成 ready commit。

---

# 二十九、Release Candidate Commit

建议提交：

```text
chore: finalize Knowledge V1 release readiness
```

内容只应包括：

```text
go.mod
go.sum
license/provenance docs
release reports
README/status
```

以及必要 release metadata。

不应出现大规模业务代码 diff。

---

# 三十、最终 Git Diff Review

执行：

```bash
git diff <baseline>..HEAD --stat
git diff <baseline>..HEAD
```

确认没有意外修改：

```text
retrieval
embedding
reranker
parser
web product logic
database
extension runtime behavior
```

如果出现：

必须解释或移除。

---

# 三十一、最终工作区

执行：

```bash
git status
git diff --check
```

要求：

```text
working tree clean
```

用户已有未跟踪文件除外。

不要提交：

```text
.codegraph
screenshots
node_modules
temporary DB
logs
models
release-validation directory
local go.work
```

---

# 三十二、再次确认 shutu-agent 未修改

任务结束时：

```bash
git -C <SHUTU_AGENT_PATH> rev-parse HEAD
git -C <SHUTU_AGENT_PATH> status --porcelain
```

必须和开始基线一致。

如果因本任务修改 Agent：

```text
FINAL RESULT = FAIL
```

---

# 三十三、最终 Release Gate

只有以下全部成立：

```text
1. shutu-agent dependency = v0.2.1
2. no replace/local path
3. go build PASS
4. go vet PASS
5. go test PASS
6. race PASS
7. Web tests PASS
8. E2E PASS
9. fresh clone PASS
10. GitHub CI PASS
11. Agent License = Apache-2.0 confirmed
12. Source provenance PASS
13. no shutu-agent/internal import
14. Agent repo unchanged
15. GAP-001 / GAP-002 remain non-blocking
```

才允许：

# `SHUTU-KNOWLEDGE V1 RELEASE READY`

---

# 三十四、如果任何 Gate 失败

不得继续宣告 Release Ready。

输出：

```text
SHUTU-KNOWLEDGE V1 NOT RELEASE READY
```

并列：

```text
failed gate
root cause
required remediation
```

不要使用：

```text
基本 ready
几乎完成
理论可发布
```

---

# 三十五、最终报告

完成后给出：

```text
1. final commit SHA
2. shutu-agent dependency version
3. go.mod replace status
4. clean-clone validation result
5. Go build/vet/test/race result
6. Web unit/build/E2E result
7. GitHub CI run URL/result
8. License Gate result
9. provenance result
10. GAP-001 / GAP-002 status
11. shutu-agent unchanged evidence
12. final repository status
```

---

# 三十六、最终输出格式

如果全部通过，只输出明确结论：

```text
SHUTU-KNOWLEDGE V1 RELEASE READY
```

并附：

```text
Release commit:
<commit SHA>

Agent dependency:
github.com/shutu-ai/shutu-agent v0.2.1

License:
Apache-2.0

CI:
PASS

Fresh clone:
PASS

Architecture:
PASS

GAP-001:
NON-BLOCKING

GAP-002:
NON-BLOCKING
```

如果未通过：

```text
SHUTU-KNOWLEDGE V1 NOT RELEASE READY
```

---

# 三十七、最终原则

本任务只完成：

```text
功能已经 Ready
        ↓
依赖许可证 blocker 已解决
        ↓
升级到合法公开 Agent SDK
        ↓
重新跑完整 Release Gates
        ↓
正式 Release Ready
```

不要再把最后一步变成新的功能开发或架构重构。
