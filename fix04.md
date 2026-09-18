# Shutu-Knowledge 0.2 Release Gate Finalization

当前 Shutu-Knowledge 已基本完成 0.2 稳定化收尾。

当前已知状态：

* release source：
  `6a733edcaaaca9554ea03031e422eea86579a456`
* 同 SHA push CI：
  `PASS`
* CI run：
  `#35263902762`
* Windows 正式包：
  `.tmp/formal-release-020/shutu-knowledge-0.2.0-windows-amd64.zip`
* SHA-256：
  `06b1ab5bfd0411802743b282dca988efa3c1b3cb77b4a2924cb155a3662b55f9`
* 正式包 smoke：
  `PASS`
* 已完成：

  * 在线导入
  * OCR
  * 向量检索
  * 离线重启
  * 重启后检索
* 当前无已知产品 P0。
* `v0.2.0` tag 已推送，并指向上述 SHA。
* `v0.2.0` tag 被视为不可变，不允许移动。
* Tag CI 两次失败。
* 两次失败均集中在 race 环境下的性能门槛：
  `statusP95 ≈ 127–130ms`
* 其它 release jobs 均通过。
* 尚未创建 GitHub Release。
* 尚未正式进入 maintenance mode。
* 当前 worktree clean。
* 没有为了通过 CI 放宽测试阈值。

---

# 1. 本任务目标

本任务不是继续开发 Shutu-Knowledge。

唯一目标是：

> 判断当前 tag CI 的 `statusP95` failure 到底是真实产品性能回退，还是 Release Gate 将 production performance SLA 错误应用到了 `go test -race` 环境；如确认属于后者，仅修正 Release Gate 语义，并完成最终发布闭环。

本轮工作必须严格限制在：

```text
Diagnose
→ Prove
→ Correct Gate
→ Revalidate
→ Release
```

禁止扩大 Scope。

---

# 2. Feature Freeze 继续有效

本任务禁止：

* 新增产品功能
* 修改 Retrieval 算法
* 修改 Embedding / Reranker 能力
* 修改 Document Parser 架构
* 修改 Storage 架构
* 修改 Agent Contract
* 引入新的 runtime
* 做 Linux/macOS 扩展
* 做极端规模优化
* 做 Document IR
* 做 LLM Wiki
* 做 Knowledge Graph
* 做任何与当前失败无直接关系的重构

如果发现其它非阻断问题：

记录到 backlog。

不要处理。

---

# 3. 第一阶段：精确定位失败

首先检查当前：

* tag workflow
* push workflow
* race job
* performance tests
* statusP95 的具体实现
* threshold 定义
* test invocation
* concurrency
* test environment
* CI runner resource differences

必须回答：

## A. 哪个测试产生 `statusP95`？

给出：

* 文件
* 测试名
* threshold
* metric collection method

## B. 它是在什么环境下失败？

明确：

```text
normal
```

还是：

```text
-race
```

或两者都会失败。

## C. push CI 与 tag CI 是否执行完全相同的 race workload？

如果不同：

列出差异。

重点检查：

* 并行 job 数
* release-host
* runtime job
* packaging
* CPU contention
* disk contention
* cache state
* GitHub runner 类型
* environment variables
* test flags

禁止在没有证据的情况下直接判断“runner 慢”。

---

# 4. 建立真实性能基线

必须使用当前最终代码建立 normal build 性能证据。

针对产生 `statusP95` 的测试或 benchmark：

连续运行至少：

```text
10 次
```

normal build。

记录每次：

```text
p50
p95
p99（如果已有）
max
pass/fail
```

如测试本身只输出 p95，则至少记录 p95。

输出：

```text
min
median
max
mean
```

目标：

确认 normal build 是否稳定满足当前原始 production threshold。

禁止：

* 修改 threshold 后再测试
* 先优化业务代码
* 删除异常样本
* 人工挑选通过结果

---

# 5. 建立 race 环境证据

对完全相同场景执行：

```bash
go test -race ...
```

连续执行至少：

```text
10 次
```

记录：

* statusP95
* 是否有 DATA RACE
* 是否有 deadlock
* 是否 timeout
* 是否出现错误状态
* 是否出现数据不一致
* 是否 goroutine leak
* 是否 bounded completion

必须区分：

```text
Latency failure
```

与：

```text
Correctness failure
```

如果：

* 没有 DATA RACE
* 没有 deadlock
* 没有 timeout
* 没有 incorrect result
* 只有绝对 latency threshold 超标

则不得自动将其认定为产品 P0。

---

# 6. 判定规则

按照以下规则执行。

## Case A：Normal 也失败

如果 normal build 多次无法稳定满足原 production threshold：

状态：

```text
REAL PERFORMANCE REGRESSION
```

该问题升级为 P0。

此时：

* 停止发布
* 找出真实性能回退原因
* 修复实际产品代码
* 不修改 production threshold
* 添加 regression test
* 重新执行完整验证

只有真实修复完成后才能继续 Release。

---

## Case B：Normal 稳定 PASS，Race 仅 latency FAIL

如果：

* normal build 稳定满足 production threshold
* `-race` 无任何 data race
* 无 correctness failure
* 无 deadlock
* 无 timeout
* 仅 `statusP95` 超过 production absolute latency threshold

则判定为：

```text
RELEASE GATE SEMANTICS ISSUE
```

不是产品性能 P0。

此时只修 Release Gate。

---

# 7. Release Gate 正确语义

必须保持两个不同目标。

## Production Performance Gate

用于验证真实产品性能。

运行环境：

```text
normal non-race build
```

继续执行现有严格 production SLA。

例如：

```text
statusP95 <= 原阈值
```

不得因为本问题：

* 提高 production threshold
* 删除 production performance assertion
* 取消 performance gate

---

## Race Correctness Gate

目标是检查：

* DATA RACE
* concurrent correctness
* deadlock
* bounded completion
* shared state safety
* lifecycle correctness

不应使用 normal production absolute latency SLA 作为唯一失败条件。

如果现有测试把：

```text
production p95 threshold
```

直接应用到：

```text
go test -race
```

应重构测试语义。

---

# 8. 推荐修改方式

优先采用最小必要修改。

推荐结构：

```text
same functional test logic
        │
        ├── normal
        │     └── strict production latency budget
        │
        └── race
              └── correctness + bounded diagnostic budget
```

race 环境可以：

* 保留 timeout
* 保留 bounded completion
* 保留极端异常检测

但不得把正常 production SLA 当作 race build 的硬性能 SLA。

如果已有项目机制可以判断 race build：

优先复用现有机制。

不要引入复杂 framework。

---

# 9. 不允许的“修复”

禁止以下方式：

```text
100ms → 150ms
100ms → 200ms
```

直接扩大 production threshold。

禁止：

* 删除整个 performance test
* `t.Skip()`
* CI 中跳过目标测试
* `|| true`
* 忽略 exit code
* 去掉 `-race`
* 增加无依据 sleep
* 把失败改成 warning
* 降低 assertion 到几乎永远通过
* 通过 retry 无限掩盖 flaky

任何 threshold 差异必须有明确语义：

```text
production performance budget
```

vs

```text
race diagnostic bound
```

---

# 10. 修改范围

如确认 Case B，修改范围应尽量限定为：

* test code
* CI workflow
* test helper
* release gate documentation

原则上：

```text
不修改业务实现
```

除非诊断过程中发现真实 correctness bug。

如果需要改动业务代码：

必须解释为什么它是实际 bug，而不是为了追 race latency。

---

# 11. 修改后本地验证

完成修改后重新执行：

```bash
go build ./...
go vet ./...
go test ./...
go test -race ./...
```

Web：

```bash
npm ci
npm run typecheck
npm run build
```

以及当前正式 release acceptance 所要求的核心测试。

特别要求：

## Normal Performance Gate

必须继续 PASS。

## Race

必须 PASS。

## Target Test

至少重复运行多次确认非 flaky。

---

# 12. Push CI 验证

生成新的 commit。

Push。

必须等待同 SHA：

```text
push CI
```

全部 PASS。

记录：

* SHA
* CI run ID
* target performance result
* race result

如果失败：

继续定位真实原因。

禁止直接发布。

---

# 13. Tag 不可变原则

现有：

```text
v0.2.0
```

已经推送并指向：

```text
6a733edcaaaca9554ea03031e422eea86579a456
```

此 tag 定义为：

```text
IMMUTABLE
```

禁止：

* force move
* delete + recreate
* retag 到新 commit
* 修改远端 tag 指针

如果本任务产生任何新 commit：

当前 `v0.2.0` 不再作为最终正式 release source。

保留它作为历史未发布 candidate tag。

---

# 14. 新版本号策略

如果无需任何 commit 修改，只是重新运行 CI 并确认全部绿色：

可继续使用：

```text
v0.2.0
```

创建 Release。

---

如果修改了：

* test
* CI
* release gate
* docs
* source

导致 SHA 发生变化：

禁止复用 `v0.2.0`。

推荐正式版本升级为：

```text
v0.2.1
```

理由：

> 已经推送的不可变 `v0.2.0` tag 不移动。

不要为了“版本号漂亮”破坏 immutable tag 原则。

---

# 15. v0.2.1 候选验证

如果产生新 commit：

在创建正式 tag 前：

1. 完成本地验证。
2. push。
3. 等待同 SHA push CI 全绿。
4. 构建正式 Windows ZIP。
5. 对 ZIP 重新执行 package-level smoke。

至少验证：

```text
fresh extraction
runtime initialization
document import
OCR
embedding
vector retrieval
rerank if enabled
citation
offline restart
restart retrieval
```

全部 PASS 后：

计算新的 SHA-256。

---

# 16. 正式 Tag

只有在：

```text
same SHA
push CI = PASS
package smoke = PASS
no P0
```

后创建：

```text
v0.2.1
```

tag 必须：

* annotated
* immutable
* 指向唯一 release source

推送 tag 后：

必须等待：

```text
tag CI
```

全部 PASS。

---

# 17. Tag CI 最终条件

正式 Release 的唯一技术条件：

```text
Build              PASS
Unit               PASS
Integration        PASS
Race               PASS
Production Perf    PASS
Web                PASS
Runtime            PASS
Package            PASS
Windows Smoke      PASS
Agent Gate         PASS
No P0              PASS
```

注意：

`Production Perf PASS`

必须来源于正常非 race 环境。

`Race PASS`

表示并发正确性通过。

两者不得再混淆。

---

# 18. GitHub Release

仅在最终 tag CI 全绿后创建 GitHub Release。

正式资产必须来自最终 release SHA。

不要上传旧：

```text
0.2.0
```

包作为：

```text
0.2.1
```

release asset。

重新构建并重新计算 checksum。

Release 至少包括：

```text
Windows ZIP
SHA-256
Release Notes
```

---

# 19. Release Notes 范围

Release Notes 只写用户需要知道的内容。

包括：

* Windows Tier-1
* Local managed runtime
* PDF / Office / OCR
* Embedding
* Hybrid retrieval
* Reranker
* Citation
* Shutu-Agent integration
* durable/restart recovery
* Windows package

Known limitations：

* Linux full release-host validation deferred
* macOS full release validation deferred
* extreme-scale validation deferred
* Document Intelligence / Document IR 属后续版本

不要把内部架构审计过程写成长篇用户 Release Notes。

---

# 20. Maintenance Mode

GitHub Release 成功创建以后：

正式宣布：

```text
Shutu-Knowledge 0.2.x enters Maintenance Mode.
```

从此：

允许：

* P0/P1 bugfix
* security fix
* data corruption fix
* compatibility fix
* packaging fix

禁止继续进入 0.2.x：

* Document IR
* LLM Wiki
* Knowledge Graph
* 新 retrieval 架构
* 新 storage architecture
* 大规模平台扩展

上述进入：

```text
0.3 backlog
```

---

# 21. 文档同步

最终更新：

* README
* release acceptance document
* stabilization document
* release notes
* backlog

状态必须统一。

正式发布后建议：

```text
Version: 0.2.1
Status: Released
Primary Platform: Windows x64
Maintenance: Active
```

如果最后无需产生新 commit 并成功发布 v0.2.0：

则使用：

```text
Version: 0.2.0
```

不得出现版本状态冲突。

---

# 22. 最终报告

完成后只输出以下内容。

## Release Gate Diagnosis

明确：

```text
REAL PERFORMANCE REGRESSION
```

或：

```text
RACE/PERFORMANCE GATE SEMANTICS ISSUE
```

并给出证据。

---

## Normal Performance

输出实际多轮：

```text
min
median
max
threshold
PASS / FAIL
```

---

## Race Validation

输出：

```text
DATA RACE: YES / NO
DEADLOCK: YES / NO
TIMEOUT: YES / NO
CORRECTNESS FAILURE: YES / NO
RESULT: PASS / FAIL
```

---

## Changes

列出实际修改文件。

---

## Final Commit

完整 SHA。

---

## CI

列出：

```text
push CI
tag CI
```

以及：

```text
PASS / FAIL
```

---

## Release Artifact

输出：

```text
filename
size
SHA-256
```

---

## Tag

输出最终正式 tag。

明确旧 `v0.2.0` 是否仅保留为历史 candidate。

---

## GitHub Release

输出：

```text
CREATED
```

或：

```text
NOT CREATED
```

如没有创建：

只说明剩余真正 blocker。

---

## Maintenance

成功发布后输出：

```text
0.2.x MAINTENANCE MODE: ACTIVE
```

---

# 23. 最终原则

当前目标不是证明：

> race 环境也达到 production latency。

而是分别证明：

> production build 达到 production performance SLA。

以及：

> race build 没有并发正确性问题。

测试环境必须与它要证明的属性一致。

不要为了绿色 CI 降低产品性能标准。

也不要让 race instrumentation 的固有性能成本错误阻塞已经通过真实 package smoke 的产品。

如果 normal 性能正常且 race correctness 正常：

只修 Release Gate。

然后发布。

不要开始 fix04 功能开发。

本任务完成后：

> Shutu-Knowledge 0.2 收官。
