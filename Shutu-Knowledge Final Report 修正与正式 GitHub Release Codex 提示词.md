# 任务：修正 Shutu-Knowledge 最终 Runtime Parity 报告，并完成正式 GitHub Release

目标仓库：

```text
https://github.com/shutu-ai/Shutu-Knowledge
```

当前已经完成：

```text
SHUTU-KNOWLEDGE V1 RELEASE READY
SHUTU-KNOWLEDGE OUT-OF-BOX PARITY READY
```

当前 Runtime candidate：

```text
369c3436045165198e8360edb36693f01c0201da
```

当前最终报告 commit：

```text
a871af6
```

正式 Windows ZIP：

```text
shutu-knowledge-0.1.0-windows-amd64.zip

Size:
11,571,335 bytes

SHA-256:
e7d4ee862acc213c2641cfbae018a819da2da4a13c9cc2b53f14b803051dfdae
```

当前 `final_runtime_parity_release_report.md` 中仍记录了较早的 CI Run ID。

最终实际 CI 是：

```text
Ordinary CI:
34097716164

Final Runtime Release CI:
34097927780
```

本任务目标：

```text
修正文档最终证据
        ↓
确认最终 release commit
        ↓
确认版本/tag
        ↓
正式 GitHub Release
        ↓
上传 ZIP + release metadata
        ↓
远端资产校验
        ↓
正式冻结
```

---

# 一、严格限制范围

本任务是：

```text
Documentation Correction
+
Release Engineering
```

不是 Feature Development。

允许：

```text
1. 修正最终报告中的 CI Run ID
2. 修正 release metadata 中与最终事实不一致的字段
3. 更新 README / release notes / release status
4. 创建最终 release commit
5. 创建 annotated tag
6. Push tag
7. 创建 GitHub Release
8. 上传正式 ZIP / checksum / metadata
9. 验证远端 Release
```

禁止：

```text
修改 Retrieval
修改 Embedding/Reranker Runtime
修改 OCR
修改 PDF Runtime
修改 Office Runtime
修改 Runtime Manager
修改 Knowledge API
修改数据库
修改 Agent integration
修改 shutu-agent
重新设计 UI
新增功能
```

除非发布验证暴露确定性的：

```text
release metadata bug
packaging bug
security bug
checksum mismatch
```

否则不要修改运行时代码。

---

# 二、确认仓库状态

执行：

```bash
git status
git branch --show-current
git rev-parse HEAD
git log -10 --oneline
git remote -v
```

必须确认：

```text
repository = Shutu-Knowledge
branch = master
```

记录当前：

```text
HEAD
origin/master
```

不得：

```text
git reset --hard
git clean
```

不得删除用户已有未跟踪文件。

---

# 三、确认最终 Runtime candidate

确认 Git 历史中存在：

```text
369c3436045165198e8360edb36693f01c0201da
```

并确认 Runtime 实现没有在之后发生未经验证的功能性变化。

当前最终文档 commit：

```text
a871af6
```

如果 `master` 已经位于更后提交：

逐个审计这些提交。

不得默认：

```text
latest HEAD == validated release state
```

---

# 四、修正 final_runtime_parity_release_report.md

打开：

```text
final_runtime_parity_release_report.md
```

当前里面旧的 Run ID 必须替换为最终真实值：

旧记录例如：

```text
ordinary CI:
34093542980

runtime-release CI:
34097221413
```

最终应记录：

```text
ordinary CI:
34097716164

final runtime-release CI:
34097927780
```

如果报告有：

```text
candidate-specific old run
```

且它是历史证据的一部分，可以保留，但必须明确区分：

```text
Earlier candidate validation
```

和：

```text
FINAL RELEASE VALIDATION
```

最终状态部分必须明确使用：

```text
34097716164
34097927780
```

---

# 五、不要改写历史事实

报告中已经验证的：

```text
Windows fresh clone
Linux fresh clone
Embedding
Reranker
OCR
PDF
JPX
Office
offline restart
corruption recovery
package-only smoke
```

保持原有事实。

仅修正最终 evidence pointer。

---

# 六、确认最终 ZIP

找到正式：

```text
shutu-knowledge-0.1.0-windows-amd64.zip
```

重新计算：

```bash
sha256sum <ZIP>
```

Windows 可使用：

```powershell
Get-FileHash <ZIP> -Algorithm SHA256
```

必须得到：

```text
e7d4ee862acc213c2641cfbae018a819da2da4a13c9cc2b53f14b803051dfdae
```

文件大小必须：

```text
11,571,335 bytes
```

如果任一不一致：

```text
STOP RELEASE
```

不要上传不同 ZIP 却继续沿用旧 SHA。

---

# 七、检查 ZIP 内容未发生变化

重新检查正式 ZIP：

必须包含：

```text
shutu-knowledge executable
LICENSE
THIRD_PARTY_NOTICES
runtime manifest
runtime checksums
runtime documentation
extension metadata
managed runtime required assets
```

必须不存在：

```text
.git
source checkout
node_modules（除非正式 packaging 设计明确需要）
model cache
runtime cache
npm cache
临时 home
开发机绝对路径
日志
截图
测试输出
API key
token
password
cookie
.env
credentials
个人配置
```

---

# 八、Runtime 下载模型策略保持不变

确认：

```text
Qwen embedding weights
BGE reranker weights
```

仍然：

```text
download-on-demand
```

而不是被意外打进正式 ZIP。

必须继续使用：

```text
pinned revision
+
artifact SHA-256
```

---

# 九、License Final Check

重新确认正式 package 对应：

```text
Shutu-Knowledge = Apache-2.0
shutu-agent v0.2.1 = Apache-2.0
```

并确认：

```text
Node
Transformers.js
ONNX Runtime
PDF.js
Tesseract.js
AnyDoc
模型
tokenizer
OCR data
```

的实际分发方式和 attribution 与：

```text
THIRD_PARTY_NOTICES.md
docs/runtime_license_inventory.md
```

一致。

本任务不要重新做一次全源码 provenance audit。

只确认：

```text
最终 package 与已有 license audit 一致
```

---

# 十、确认 Agent 未修改

如果本地存在 Agent repo：

```bash
git -C <AGENT_PATH> rev-parse HEAD
git -C <AGENT_PATH> status --porcelain
```

最终仍必须确认：

```text
Agent unchanged
```

不得修改 Agent。

---

# 十一、创建文档修正提交

如果唯一变化只是最终证据修正：

建议 commit：

```text
docs: finalize runtime parity release evidence
```

提交后记录：

```text
FINAL_RELEASE_COMMIT=<SHA>
```

---

# 十二、重新跑轻量 Final Gate

因为只是文档修正，不需要重新下载大模型并重跑全部 Runtime E2E。

至少执行：

```bash
git diff --check
go test ./...
go vet ./...
go build ./...
```

以及 Web 的真实快速 Gate：

```text
contract
typecheck
build
```

如果 CI 会自动完整执行，则仍等待远端 CI。

---

# 十三、Push 最终 release commit

执行：

```bash
git push origin master
```

然后确认：

```text
origin/master == FINAL_RELEASE_COMMIT
```

---

# 十四、等待最终 GitHub CI

最终 release commit 对应 CI 必须：

```text
SUCCESS
```

如果新的 commit 只是 docs-only 且 workflow 根据 path filter 不触发：

在报告中明确：

```text
Runtime validation evidence:
34097927780

Ordinary full validation:
34097716164

Final docs-only commit:
<final SHA>
```

这种情况是可接受的。

不要为了制造一个新 run 去修改无关源码。

---

# 十五、版本号审计

不要擅自决定版本。

检查：

```bash
git tag --list
```

以及：

```text
README
release metadata
package filename
version constants
```

当前正式 ZIP 名称显示：

```text
shutu-knowledge-0.1.0-windows-amd64.zip
```

因此重点确认：

```text
项目当前既定正式版本是否确实为 0.1.0
```

---

# 十六、版本选择规则

如果项目当前：

```text
version = 0.1.0
```

并且远端不存在：

```text
v0.1.0
```

则本次使用：

```text
v0.1.0
```

如果已经存在公开 `v0.1.0`：

```text
绝对不要移动/覆盖 tag
```

应按照 SemVer 和项目现有版本策略选择新的 patch/minor version。

不要 force-update 已发布 tag。

---

# 十七、版本一致性

最终必须确保：

```text
Git tag
GitHub Release
ZIP filename
release metadata
binary version
README
```

版本一致。

例如如果最终确认：

```text
v0.1.0
```

则：

```text
Tag:
v0.1.0

ZIP:
shutu-knowledge-0.1.0-windows-amd64.zip
```

---

# 十八、Annotated Tag

只在最终 commit 和版本确认后：

```bash
git tag -a v0.1.0 <FINAL_RELEASE_COMMIT> -m "Shutu-Knowledge v0.1.0"
```

版本若不是 0.1.0，用实际确认的版本。

检查：

```bash
git show v0.1.0 --no-patch
```

必须准确指向：

```text
FINAL_RELEASE_COMMIT
```

---

# 十九、不要立即覆盖远端 Tag

先：

```bash
git ls-remote --tags origin
```

确认该 tag 不存在。

如果已经存在：

```text
STOP
```

不要：

```text
git push --force
git tag -f
```

---

# 二十、Push Tag

确认无冲突后：

```bash
git push origin <VERSION_TAG>
```

再次：

```bash
git ls-remote --tags origin
```

验证远端。

---

# 二十一、GitHub Release

创建：

```text
Shutu-Knowledge <VERSION>
```

对应：

```text
<VERSION_TAG>
```

不要标记：

```text
pre-release
```

除非项目版本策略明确当前仍是 preview/beta。

---

# 二十二、Release Assets

至少上传：

```text
shutu-knowledge-0.1.0-windows-amd64.zip
```

以及项目已有正式 metadata 文件，例如：

```text
release.json
SHA256SUMS
runtime manifest
```

如果 packaging 已生成。

不要临时创造一堆与项目没有约定的新格式。

---

# 二十三、建议生成 SHA256SUMS

如果当前还没有：

建议生成：

```text
SHA256SUMS
```

至少：

```text
e7d4ee862acc213c2641cfbae018a819da2da4a13c9cc2b53f14b803051dfdae  shutu-knowledge-0.1.0-windows-amd64.zip
```

但只有在项目现有发布规范允许时添加。

---

# 二十四、Release Notes

Release Notes 不要写成：

```text
dsh-knowledge 100% identical clone
```

推荐准确描述：

```text
Shutu-Knowledge reaches V1 release readiness and out-of-box runtime parity
for the supported Knowledge capability scope.
```

---

# 二十五、Release Notes 核心内容

至少说明：

```text
- Native integration with shutu-agent Extension Platform v1.
- Knowledge CRUD and document lifecycle.
- Hybrid BM25/vector retrieval with RRF/MMR.
- Local Qwen embedding runtime.
- Local BGE reranker runtime.
- OCR and scanned-PDF ingestion.
- Full-page PDF rendering and JPX support.
- Legacy DOC/PPT/XLS ingestion.
- Auto-RAG and Knowledge tools.
- Managed runtime lifecycle, Doctor and Web health state.
- Windows/Linux runtime validation.
- Offline runtime restart and corruption recovery.
- Apache-2.0 project license.
```

---

# 二十六、明确两个已知 Gap

Release Notes 中可以放：

```text
Known non-blocking host-integration limitations
```

包括：

```text
GAP-001
Agent Extension v1 has no public systemPrompt.section guidance channel.

GAP-002
Agent-side historical lifecycle/diagnostic parity remains partial.
```

不要隐藏。

也不要把它们写成 Knowledge Runtime failure。

---

# 二十七、Release Notes 中说明模型

说明：

```text
Embedding and reranker model weights are downloaded on demand
using pinned revisions/checksums and are not redistributed inside the ZIP.
```

这能避免用户误以为 11 MB ZIP 已包含完整模型权重。

---

# 二十八、上传前 Secret Scan

对最终 ZIP 和 metadata 再执行：

```text
API key
token
password
cookie
credential
private endpoint
absolute developer path
.env
```

扫描。

只在 PASS 后上传。

---

# 二十九、上传 GitHub Release Assets

上传：

```text
ZIP
checksum/metadata
```

---

# 三十、远端资产一致性

上传后重新从 GitHub Release 下载资产。

不要只相信上传成功。

计算远端下载文件：

```text
size
SHA-256
```

必须仍然：

```text
11,571,335 bytes

e7d4ee862acc213c2641cfbae018a819da2da4a13c9cc2b53f14b803051dfdae
```

如果 GitHub asset metadata 显示大小不同：

先实际下载并 hash 验证。

任何 hash 不一致：

```text
RELEASE VERIFICATION FAILED
```

---

# 三十一、远端 Package Smoke

使用从 GitHub Release 下载的 ZIP：

```text
extract to new directory
```

不得依赖源码 checkout。

至少执行：

```text
--version
doctor
startup smoke
runtime discovery
```

如果已有 release-smoke 脚本：

执行正式脚本。

---

# 三十二、不要重复下载全部大型模型，除非 release smoke 要求

因为：

```text
369c343
```

和对应 runtime-release CI 已经完成真实模型 Runtime E2E。

正式远端 asset smoke 的目标主要是验证：

```text
上传资产没有损坏
正式 ZIP 可以启动
manifest/runtime discovery 正常
```

如果已有缓存隔离 release-smoke 很容易跑完整 Runtime，可继续执行。

---

# 三十三、更新最终报告

GitHub Release 完成后，再更新：

```text
final_runtime_parity_release_report.md
```

增加：

```text
Final release version
Final release tag
Final release commit
GitHub Release
Remote asset size
Remote asset SHA-256
Release asset verification
```

---

# 三十四、避免形成无限发布循环

如果更新报告本身会产生新的 commit：

允许：

```text
release evidence follow-up commit
```

但不要因此移动已经发布的 tag。

正确模型：

```text
release tag
     ↓
immutable

post-release documentation
     ↓
master can advance
```

不要为了让 tag 包含“GitHub Release URL”而移动 tag。

---

# 三十五、Tag 必须不可变

一旦：

```text
<VERSION_TAG>
```

推送并公开：

禁止：

```text
移动
删除后重建
force push
```

未来 bug 使用新版本。

---

# 三十六、最终 README 状态

README 应准确显示：

```text
Release Ready: YES
Out-of-Box Runtime Parity: READY
License: Apache-2.0
Agent dependency: shutu-agent v0.2.1
```

不要写：

```text
100% code-identical to dsh-knowledge
```

---

# 三十七、最终功能定位

推荐：

```text
Capability parity:
READY

Out-of-box runtime parity:
READY

Host-integration behavioral identity:
Not fully identical; documented non-blocking Agent-owned gaps remain.
```

---

# 三十八、正式发布后的最终验证报告

生成或更新：

```text
release_verification_report.md
```

至少包括：

```text
Version
Tag
Tag commit
master HEAD
GitHub Release URL
ZIP name
ZIP size
ZIP SHA-256
Remote download SHA-256
ordinary CI run
runtime-release CI run
fresh clone result
Windows result
Linux result
license result
provenance result
Agent unchanged
GAP-001
GAP-002
```

---

# 三十九、最终 Git 状态

执行：

```bash
git status
git log -5 --oneline
git tag --points-at <release commit>
```

报告：

```text
tracked workspace clean
```

用户原有未跟踪文件继续保留。

---

# 四十、再次确认 Agent 未修改

执行：

```bash
git -C <AGENT_PATH> rev-parse HEAD
git -C <AGENT_PATH> status --porcelain
```

应保持原状态。

---

# 四十一、最终 Release Gate

只有以下全部成立：

```text
1. final report CI Run IDs corrected
2. final package SHA matches
3. package size matches
4. license/provenance PASS
5. Agent unchanged
6. final release commit pushed
7. final tag immutable and correct
8. GitHub Release created
9. remote asset downloaded and SHA verified
10. package startup/release smoke PASS
11. known GAP-001/GAP-002 preserved
```

才能正式输出：

# SHUTU-KNOWLEDGE OFFICIAL RELEASE COMPLETE

---

# 四十二、最终输出格式

成功时输出：

```text
SHUTU-KNOWLEDGE OFFICIAL RELEASE COMPLETE

Version:
<version>

Release commit:
<SHA>

Tag:
<tag>

GitHub Release:
<URL>

Ordinary CI:
34097716164 — PASS

Runtime Release CI:
34097927780 — PASS

Windows ZIP:
<filename>

Size:
11,571,335 bytes

SHA-256:
e7d4ee862acc213c2641cfbae018a819da2da4a13c9cc2b53f14b803051dfdae

Remote asset verification:
PASS

V1 Release Ready:
PASS

Out-of-Box Runtime Parity:
READY

License:
Apache-2.0

Agent dependency:
github.com/shutu-ai/shutu-agent v0.2.1

GAP-001:
NON-BLOCKING

GAP-002:
NON-BLOCKING
```

---

# 四十三、失败规则

如果：

```text
ZIP hash mismatch
tag conflict
license blocker
secret found
remote asset corrupted
runtime startup failure
```

任何一项出现：

不要继续宣告成功。

输出：

```text
SHUTU-KNOWLEDGE OFFICIAL RELEASE BLOCKED
```

并明确：

```text
failed gate
root cause
required remediation
```

---

# 四十四、最重要的原则

这一步不再证明：

```text
功能有没有实现
```

这个阶段已经完成。

现在只证明：

```text
被验证过的那个实现
        =
Git 中被发布的版本
        =
Tag 指向的版本
        =
GitHub Release 中的 ZIP
        =
用户真正下载到的 ZIP
```

五者必须完全闭环。