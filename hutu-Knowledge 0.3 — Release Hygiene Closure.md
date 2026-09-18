# Shutu-Knowledge 0.3 — Release Hygiene Closure

当前 Shutu-Knowledge 0.3 的功能实现和自动化验证已经完成。

本任务不是功能开发，也不是架构调整。

唯一目标是：

> 修正 0.3 发布状态、README、Release Report、Tag/Artifact/Release 之间的元数据一致性，并完成正式发布闭环。

这是一次 **Release Hygiene / Metadata Closure**。

---

# 1. 当前已知状态

当前远端状态：

```text
master:
1c205414

v0.3.0 tag:
f1afd9a3
```

已知 CI：

```text
Push CI:
35334047540
PASS

v0.3.0 Tag CI:
35331687850
PASS
```

当前情况：

* 0.3 功能实现已完成。
* Document IR / Structured Parsing / Structured Chunking / Citation / Retrieval 相关功能已经验证。
* `v0.3.0` tag CI 已通过。
* `master` push CI 已通过。
* 当前 `master` 相比 `v0.3.0` 主要是发布报告状态更新。
* 没有新的业务代码需要修改。
* External cross-repository Agent Host 没有被宣称为已验收。
* 当前尚未创建正式 `v0.3.0` GitHub Release。
* 本地 `fix04.md` 和任务文档为用户自己的 untracked 文件，必须保留，不得删除、修改、提交或清理。

---

# 2. 当前需要修正的问题

当前存在几个发布状态一致性问题。

## 问题 A：v0.3.0 tag 内部 Release Report 状态过时

`v0.3.0` 指向：

```text
f1afd9a3
```

其内部发布报告仍保留类似：

```text
NOT READY
```

或：

```text
Remote Push CI and Tag CI were not run
```

的历史状态。

但实际：

```text
Push CI = PASS
Tag CI = PASS
```

因此：

> tag 中的历史报告与实际远端验证状态不一致。

不要移动 `v0.3.0` tag。

---

## 问题 B：master README 状态仍然过时

README 中如果仍存在类似：

```text
0.3.0 Document Intelligence Foundation — validation in progress
```

或：

```text
new capabilities belong to 0.3 backlog
```

应更新。

0.3 已经完成。

新的能力应进入：

```text
0.4+
```

而不是继续写成 0.3 backlog。

---

## 问题 C：External Agent Host 的门槛语义不一致

当前文档中可能同时存在：

```text
External Agent Host remains a release gate
```

和：

```text
0.3 is READY
```

这两个定义不能同时成立。

当前正式决策：

> Cross-repository external Agent Host acceptance 不再作为 Shutu-Knowledge 0.3 发布 blocker。

它应该被定义为：

```text
non-blocking cross-repository integration acceptance
```

或者：

```text
post-release integration validation
```

它仍然必须保留记录。

但不能再称：

```text
0.3 release gate
```

也不能宣称：

```text
PASS
```

---

# 3. 本任务严格禁止事项

本任务禁止修改：

* Retrieval
* BM25
* Vector Search
* RRF
* Reranker
* MMR
* Document IR
* Parser
* PDF
* DOCX
* PPTX
* XLSX
* OCR
* Runtime
* Storage
* SQLite
* Durable Operation
* Agent Contract
* Agent adapter behavior
* API
* UI 功能
* Schema
* Migration
* Installer 功能
* 业务实现代码

除非发现：

```text
明确阻断发布的真实 P0
```

否则不允许碰业务实现。

如果发现新的非阻断问题：

记录到 backlog。

不要修。

---

# 4. 不得修改已有不可变 Tag

现有：

```text
v0.3.0
```

已经推送。

定义为：

```text
IMMUTABLE
```

禁止：

* force move
* delete tag
* recreate tag
* retag 到 master
* 修改 tag 指针
* force push tag

`v0.3.0` 保留为：

> 已通过 Tag CI 的历史 candidate tag。

---

# 5. 版本策略

因为正式发布元数据需要产生新的 commit，而：

```text
v0.3.0
```

不能移动，因此正式 GitHub Release 使用：

```text
v0.3.1
```

目标：

```text
v0.3.0
= validated candidate

v0.3.1
= formal release
```

不要重新使用：

```text
v0.3.0
```

作为新 commit 的 tag。

---

# 6. 第一阶段：只审计发布相关文件

不要重新审计整个仓库。

只检查与发布状态直接相关的文件，例如：

```text
README.md

docs/release_report_0.3.0.md
docs/release_0.3_acceptance.md
docs/stabilization_0.3.md

Release Notes
version metadata
CI workflow
release packaging metadata
```

根据实际仓库结构调整。

不要重新阅读全部架构代码。

不要重新进行 0.3 feature audit。

---

# 7. README 收口

更新 README，使状态准确。

建议明确：

```text
Current stable release: v0.3.1

0.3 Document Intelligence Foundation:
Released

Primary release platform:
Windows x64
```

如果 Linux/macOS 只是 CI/release-host 验证而不是完整产品支持：

不要扩大正式支持声明。

---

README 中不应再写：

```text
0.3 validation in progress
```

如果已完成。

也不应再写：

```text
new capabilities belong to 0.3 backlog
```

应改为：

```text
new architecture and knowledge capabilities belong to 0.4+
```

---

# 8. Release Report 收口

更新正式 Release Report。

不要修改历史事实。

必须清楚记录：

```text
0.3 implementation commit
v0.3.0 candidate tag
documentation/release closure commit
v0.3.1 formal tag
```

例如：

```text
Implementation completed at:
<actual SHA>

Validated candidate:
v0.3.0 / f1afd9a3

Remote validation:
Push CI 35334047540 — PASS
Tag CI 35331687850 — PASS

Formal release:
v0.3.1 / <new SHA>
```

具体 SHA 必须从真实 git 状态读取。

禁止凭提示词猜测。

---

# 9. Release 状态

正式报告最终应明确：

```text
Release Status: READY
```

或者：

```text
Release Status: RELEASED
```

如果 GitHub Release 已实际创建：

优先：

```text
RELEASED
```

如果文档 commit 时尚未创建：

可以先写：

```text
READY FOR RELEASE
```

Release 创建后再以最小必要方式同步最终状态。

避免出现：

```text
NOT READY
```

与：

```text
READY
```

同时存在但没有解释的情况。

---

# 10. External Agent Host 的正式定义

统一所有文档中的措辞。

推荐表达：

```text
Cross-repository external Agent Host acceptance is not part of
the Shutu-Knowledge 0.3 release gate.

It remains a non-blocking post-release integration validation item.

The current release does not claim that this external acceptance
has passed.
```

必须保留这个事实：

```text
NOT CLAIMED AS PASS
```

同时明确：

```text
NOT A RELEASE BLOCKER
```

这两个状态不能混淆。

---

# 11. 不修改历史报告事实

如果旧文档中记录：

```text
At the time of this report, remote CI had not yet run.
```

不要简单删除历史事实导致时间线失真。

可以修改为：

```text
Initial local acceptance completed before remote CI.

Subsequent remote evidence:
- Push CI ...
- Tag CI ...
```

目标是：

> 补充后续证据，而不是重写历史。

---

# 12. Artifact 来源检查

检查现有正式 package 的：

```text
source SHA
tag SHA
filename
checksum
```

如果当前现有包不是从最终 `v0.3.1` SHA 构建：

不要复用为正式 `v0.3.1` artifact。

必须从最终正式 release commit/tag：

```text
重新构建
```

---

# 13. 正式 Package

最终版本文件名建议：

```text
shutu-knowledge-0.3.1-windows-amd64.zip
```

必须来自：

```text
v0.3.1 tag SHA
```

重新计算：

```text
SHA-256
```

不得复用旧版本 checksum。

---

# 14. Package Smoke

对正式 ZIP 执行已有 0.3 release smoke。

不要扩大测试 Scope。

至少使用项目已有正式路径验证：

```text
extract

start

runtime initialization

document import

structured parsing

Document IR availability

embedding/index

retrieval

structured citation

restart

search after restart
```

如果 Agent adapter/package smoke 已经属于正式门禁：

继续执行。

---

# 15. Source Validation

在创建 tag 前必须运行当前正式 release gate。

至少：

```bash
go build ./...
go vet ./...
go test ./...
go test -race ./...
```

以及：

```text
Web typecheck/build/E2E
```

和当前仓库明确规定的 0.3 Release Acceptance。

不要重新增加新测试矩阵。

---

# 16. Push

完成文档与 release metadata 修正后：

Commit。

Commit 内容应尽量仅限：

```text
release hygiene
version metadata
README
release report
release notes
```

Push 到：

```text
master
```

等待：

```text
Push CI
```

必须：

```text
PASS
```

---

# 17. 创建 v0.3.1 Tag

只有：

```text
master CI = PASS
```

后：

创建：

```text
v0.3.1
```

要求：

```text
annotated
immutable
```

并指向：

```text
最终 release closure SHA
```

---

# 18. Tag CI

推送：

```text
v0.3.1
```

后等待完整 Tag CI。

要求：

```text
Tag CI = PASS
```

不要因为之前 `v0.3.0` 已 PASS 就跳过。

`v0.3.1` 必须有自己的远端验证证据。

---

# 19. GitHub Release

只有在：

```text
Push CI PASS
Tag CI PASS
Package Smoke PASS
No P0
```

后创建：

```text
Shutu Knowledge v0.3.1
```

GitHub Release。

正式资产必须上传：

```text
shutu-knowledge-0.3.1-windows-amd64.zip
```

并记录：

```text
size
SHA-256
```

---

# 20. Release Notes

Release Notes 不要写成长篇架构报告。

用户视角只说明：

## Major

* Document IR
* Structured document parsing
* Structure-aware chunking
* Structured citation
* PDF/DOCX/PPTX/XLSX document intelligence
* Existing hybrid retrieval integration
* Windows release artifact

## Compatibility

* builds on 0.2.x foundation
* existing retrieval/storage design retained

## Known Limitations

例如：

```text
Cross-repository external Agent Host acceptance:
not yet independently completed; non-blocking follow-up.

Advanced LLM-Wiki / GraphRAG / Knowledge Graph:
not part of 0.3.
```

不要把内部 Codex 工作过程写进用户 Release Notes。

---

# 21. 0.3 Maintenance Mode

GitHub Release 成功后：

正式进入：

```text
0.3.x MAINTENANCE MODE
```

允许进入 0.3.x 的：

* P0/P1 bugfix
* security fix
* data correctness fix
* parser correctness fix
* citation correctness fix
* packaging fix
* compatibility fix

禁止进入 0.3.x 的：

* GraphRAG
* Knowledge Graph
* LLM Wiki
* Cross-document knowledge compiler
* 新 Storage Architecture
* 新 Vector DB
* 大规模 Agent architecture

全部进入：

```text
0.4+
```

---

# 22. Untracked 文件保护

当前用户本地存在：

```text
fix04.md
任务文档
```

等 untracked 文件。

必须：

```text
保留
```

禁止：

```text
git clean
git clean -fd
删除
提交
修改
重命名
```

任何 cleanup 操作前必须确保不会影响用户文件。

---

# 23. 不得为了“干净”修改无关文件

如果格式化、lint 或版本脚本导致大量无关文件变化：

停止。

本任务必须保持：

```text
small diff
```

目标不是重新格式化仓库。

---

# 24. Release Timeline 应最终清晰

完成后文档应该能够清楚解释：

```text
v0.2.1
    ↓
stable RAG foundation

0.3 implementation
    ↓
Document Intelligence

v0.3.0
    ↓
validated candidate
    ↓
Tag CI PASS

release metadata closure
    ↓

v0.3.1
    ↓
formal release
    ↓
maintenance mode
```

---

# 25. Final Release Gate

最终只需要验证：

```text
Source tests                  PASS
Race                          PASS
Web                           PASS
0.3 regression                PASS
Structured parsing            PASS
Document IR                   PASS
Structured citation           PASS
Package smoke                 PASS
Push CI                       PASS
Tag CI                        PASS
No P0                         PASS
```

不要重新引入：

```text
external cross-repo Agent Host
```

作为本次 blocking gate。

---

# 26. 如果发现真实 P0

如果在正式 package smoke 或 CI 中发现新的：

```text
data corruption
crash
incorrect citation
broken parser
broken migration
broken package
```

则：

停止发布。

记录真实 P0。

只修这个 P0。

不要趁机扩 Scope。

修完重新走：

```text
Push CI
→ package
→ tag
→ Tag CI
```

---

# 27. 最终输出

完成任务后，只输出以下内容。

## Release Hygiene

```text
PASS / FAIL
```

---

## Changes

列出实际修改文件。

确认：

```text
BUSINESS CODE CHANGED: YES / NO
```

预期：

```text
NO
```

---

## Final Commit

完整 SHA。

---

## Push CI

```text
run ID
PASS / FAIL
```

---

## Tag

输出：

```text
v0.3.1
```

及其完整 SHA。

同时确认：

```text
v0.3.0 unchanged: YES
```

---

## Tag CI

```text
run ID
PASS / FAIL
```

---

## Release Artifact

```text
filename
size
SHA-256
package smoke
```

---

## GitHub Release

```text
CREATED / NOT CREATED
```

如果创建：

输出 release 名称。

---

## External Agent Host

必须明确：

```text
Acceptance status:
NOT CLAIMED

Release blocking:
NO

Follow-up integration validation:
YES
```

---

## Untracked User Files

明确确认：

```text
fix04.md preserved
task documents preserved
```

---

## Maintenance

成功发布后：

```text
0.3.x MAINTENANCE MODE: ACTIVE
```

---

## Final Status

只能输出：

```text
READY
```

或：

```text
NOT READY
```

---

# 28. 最终原则

本任务不是：

> 再完善一次 0.3。

而是：

> 让已经完成的 0.3 形成一个版本、源码、Tag、CI、Artifact、Release Notes、GitHub Release 相互一致的正式发布快照。

始终遵守：

```text
Do not move immutable tags.

Do not rewrite working product code.

Do not convert follow-up integration validation into a release blocker.

Do not claim external acceptance that was not run.

Do not touch user untracked files.

Keep the diff minimal.

Close the release and stop.
```

完成正式 `v0.3.1` GitHub Release 后：

# STOP 0.3 DEVELOPMENT

进入：

```text
0.3.x Maintenance
```

新的知识能力和架构工作全部进入：

```text
0.4+
```
