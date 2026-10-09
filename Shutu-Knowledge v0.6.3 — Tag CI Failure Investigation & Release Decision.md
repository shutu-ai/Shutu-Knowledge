# Shutu-Knowledge v0.6.3 — Tag CI Failure Investigation & Release Decision

## 0. 当前状态

当前 release candidate：

```text
v0.6.3
```

已经推送 immutable annotated tag。

Tag object：

```text
5f4c3b21c1acd77dedd396a9c33434b9370eab48
```

Tag target / release source：

```text
4699a697674020ce5e90b69523a764917cb8bf55
```

禁止：

```text
move tag
delete/recreate tag
retarget tag
force push tag
```

---

# 1. 当前 Tag CI 失败证据

Tag CI：

```text
35706624209
```

结果：

```text
FAILED
```

失败 Job / Step：

```text
build → Test race
```

失败测试：

```text
TestCrossProcessHistoricalReaderSurvivesGenerationGC
```

Package：

```text
internal/knowledge
```

Failure location：

```text
cross_process_historical_reader_test.go:165
```

Failure：

```text
cross-process reader pinned  chunks, want 20
```

Runtime：

```text
820.896s
```

仍在配置的：

```text
30m timeout
```

范围内。

---

# 2. 其他 Tag CI 状态

以下正式 release jobs 已 PASS：

```text
release-package        PASS
runtime-release        PASS
release-host Windows   PASS
release-host macOS     PASS
release-host Ubuntu    PASS
```

因为 race step 失败：

```text
Benchmark smoke
Browser E2E
```

未继续执行。

---

# 3. 当前发布状态

明确：

```text
GitHub Release: NOT PUBLISHED
Artifact attached: NO
Stage A: BLOCKED
Stage B / 0.7: MUST NOT START
```

---

# 4. 本任务唯一目标

不是：

```text
让 CI 变绿
```

而是回答：

# 为什么 cross-process historical reader 在 generation GC 场景下没有得到预期的 20 chunks？

必须区分：

```text
PRODUCTION CORRECTNESS BUG
TEST SYNCHRONIZATION BUG
SQLITE/CROSS-PROCESS VISIBILITY BUG
RACE-ONLY TIMING DEFECT
EXTERNAL CI INFRASTRUCTURE FAILURE
```

在没有证据前：

不要预设结论。

---

# 5. 这是高优先级 correctness investigation

这个测试覆盖：

```text
historical reader
+
generation pinning
+
cross-process visibility
+
generation garbage collection
```

这些属于 Shutu-Knowledge 的核心一致性能力。

因此：

> 不能通过单纯 rerun 直到 PASS 来关闭问题。

---

# 6. Feature Freeze

本任务禁止：

* 0.7 development
* Structured Data Model Intelligence
* Excel semantic work
* GraphRAG
* Recursive Research
* Runtime feature expansion
* Retrieval redesign
* UI changes
* New storage architecture

只允许：

```text
diagnosis
instrumentation
targeted regression reproduction
minimal correctness fix if required
```

---

# 7. PH0 — Read the exact test

首先完整审查：

```text
internal/knowledge/cross_process_historical_reader_test.go
```

尤其：

```text
TestCrossProcessHistoricalReaderSurvivesGenerationGC
```

必须画出测试真实时序：

```text
Parent
Child Reader Process
SQLite/storage
Generation GC
```

输出实际事件顺序。

---

# 8. 明确测试想证明什么

至少回答：

```text
Which generation is historical?
Who pins it?
Where is the pin persisted?
When is the pin considered durable?
How does GC discover active historical readers?
How is cross-process liveness represented?
When can GC legally reclaim a generation?
When does reader enumerate/read chunks?
```

---

# 9. 预期 invariant

明确写出核心 invariant，例如：

> Once a historical reader has successfully established a durable visible pin on generation G, GC must not reclaim evidence/chunks required by that reader until the pin is released or expires according to the documented lifecycle.

如果当前实现实际 contract 不同：

必须以代码/设计文档为准并说明。

---

# 10. 首先复现单测试

运行：

```bash
go test ./internal/knowledge \
  -run '^TestCrossProcessHistoricalReaderSurvivesGenerationGC$' \
  -count=20 -v
```

记录：

```text
pass
fail
duration
failure shape
```

---

# 11. Race 模式复现

运行：

```bash
go test -race ./internal/knowledge \
  -run '^TestCrossProcessHistoricalReaderSurvivesGenerationGC$' \
  -count=20 -v
```

如时间过长：

可先：

```text
-count=5
```

确认是否稳定复现，再扩大。

但最终必须给出足够次数证据。

---

# 12. Package-level 复现

运行：

```bash
go test ./internal/knowledge -count=5
```

和：

```bash
go test -race ./internal/knowledge -count=3
```

目的是判断：

```text
isolated test
vs
package interaction
```

---

# 13. Whole-suite interaction

如果单测始终 PASS，但 package/full suite 偶发 FAIL：

调查：

```text
shared temp resources
environment variables
global state
ports
SQLite files
process cleanup
test ordering
CPU saturation
race instrumentation slowdown
```

---

# 14. 不允许马上改 timeout

禁止首先：

```text
increase timeout
sleep longer
retry more
```

这些只能掩盖 race。

---

# 15. Instrumentation

增加临时或正式诊断日志，至少记录：

```text
generation IDs
reader process PID
reader/session ID
pin creation timestamp
pin storage commit timestamp
parent-observed pin timestamp
GC start timestamp
GC candidate generations
GC protected generations
generation deletion timestamp
reader query timestamp
chunk count returned
reader release timestamp
```

---

# 16. 必须有 monotonic/event IDs

如果日志来自多个 process：

应能可靠排序。

至少包含：

```text
wall clock
process
event
generation
reader/pin id
```

如果已有 durable sequence：

优先复用。

---

# 17. 核查测试同步

重点验证测试是否实际保证：

```text
child process started
        ↓
reader opened historical generation
        ↓
pin persisted
        ↓
transaction committed
        ↓
parent independently observes pin
        ↓
ONLY THEN GC starts
```

---

# 18. 特别排查这种错误时序

```text
child process launched
        ↓
child emits "ready" too early
        ↓
parent assumes pin exists
        ↓
GC runs
        ↓
child actually persists pin
```

如果存在：

属于：

```text
TEST / PROTOCOL SYNCHRONIZATION BUG
```

但必须确认生产 API 是否也可能出现同样窗口。

---

# 19. Ready signal 的定义

如果 child 向 parent 发送：

```text
READY
```

必须确认 READY 意味着：

```text
process alive
```

还是：

```text
historical reader fully pinned and durable
```

这两个语义完全不同。

---

# 20. Cross-process pin persistence

检查 pin 是否保存于：

```text
memory
file
SQLite table
lock
lease
heartbeat
```

必须确认：

> GC 使用的保护信息与 child reader 建立的 pin 是同一 durable source of truth。

---

# 21. SQLite transaction visibility

如果 pin 在 SQLite 中：

调查：

```text
BEGIN / COMMIT timing
WAL mode
connection snapshot
read transaction lifetime
busy timeout
checkpoint behavior
cross-process visibility
```

重点回答：

> parent GC connection 是否可能在旧 snapshot 中看不到刚提交的 pin？

---

# 22. Long-lived read transaction

如果 GC 在：

```text
BEGIN
...
scan pins
...
delete generations
```

期间持有旧 read snapshot：

可能即使 child 已 commit，GC 仍观察不到。

必须检查真实事务边界。

---

# 23. GC transaction semantics

明确：

```text
when GC determines protected generations
when GC deletes
whether protection is rechecked before delete
```

如果：

```text
scan
→ long delay
→ delete
```

中间 reader pin 可以建立：

需要判断这是否是生产 race。

---

# 24. TOCTOU Audit

特别检查：

```text
Time Of Check
vs
Time Of Use
```

例如：

```text
GC reads active pins
       ↓
new reader pins historical generation
       ↓
GC deletes generation based on stale set
```

如果允许发生：

这是实际 correctness bug。

---

# 25. Reader pin lifecycle

检查：

```text
create
renew/heartbeat
release
crash
expiry
```

确保：

```text
pin not prematurely expired
```

尤其 race build 下速度大幅下降时。

---

# 26. Lease/TTL

如果 pin 有 TTL：

明确：

```text
TTL duration
heartbeat frequency
-race slowdown effect
```

如果测试用了 production TTL：

可能 race instrumentation 导致 lease 真正过期。

这不一定只是测试问题。

要判断 production 在高负载下是否也可能发生。

---

# 27. Cross-process process-liveness detection

如果系统通过 PID / process state 判断 reader 是否存活：

检查：

```text
PID reuse
Windows/Linux semantics
child cleanup
race runner behavior
```

但不要猜。

---

# 28. Chunk count failure

当前信息：

```text
cross-process reader pinned  chunks, want 20
```

必须拿到真实：

```text
got count
```

如果日志当前缺失：

改善 assertion：

```text
got=X want=20
```

并记录：

```text
generation
pin state
remaining chunks
```

---

# 29. 判断是否整个 generation 被 GC

失败时检查：

```text
generation record exists?
document exists?
chunks exist?
embedding exists?
derived knowledge exists?
```

不要只看最终 chunk count。

---

# 30. Generation consistency

如果部分对象被删、部分没删：

这是更严重的问题。

要求检查：

```text
generation metadata
documents
chunks
nodes
embeddings
derived knowledge
```

是否原子一致。

---

# 31. Reproduce with forced scheduling

如果怀疑同步窗口：

使用 deterministic barrier/channel/process protocol。

不要依赖随机 sleep。

可以设计：

```text
child:
  open reader
  persist pin
  wait for parent ACK

parent:
  wait until pin visible
  send ACK
  trigger GC
```

用于验证 invariant。

---

# 32. Reproduce with adversarial ordering

还应测试：

### Case A

```text
reader pins before GC scan
```

必须存活。

### Case B

```text
reader starts after GC legally begins reclaim
```

行为按 contract 明确。

### Case C

```text
reader pin commit races with GC protection scan
```

必须有确定、正确的语义。

---

# 33. 不能靠 sleep 修复

如果测试最终修复只是：

```text
Sleep(500ms) → Sleep(5s)
```

不接受。

应使用：

```text
observable durable condition
barrier
acknowledgment
```

---

# 34. 生产代码判断标准

如果调查发现：

```text
GC can reclaim a generation after reader has durably pinned it
```

这是：

# PRODUCTION CORRECTNESS BUG

必须修生产代码。

---

# 35. 测试代码判断标准

如果生产代码 contract 正确，但测试：

```text
triggers GC before pin durability is confirmed
```

则：

# TEST SYNCHRONIZATION BUG

修测试 handshake。

---

# 36. 两者都可能有问题

如果测试同步弱，同时 production 有 TOCTOU：

必须都修。

不要只修测试让 CI 绿。

---

# 37. External infrastructure 判断要求

只有以下证据充分时才能认定：

```text
EXTERNAL CI FAILURE
```

例如：

* runner disk/process failure
* OS-level crash outside code
* corrupted checkout
* infrastructure outage

而当前：

```text
wrong chunk count
```

默认不能视为 external noise。

---

# 38. Flaky 的定义

即使：

```text
1/100 fails
```

只要是 correctness race：

仍然是 bug。

不要因为低频就降级。

---

# 39. Runtime 820s

顺便分析：

```text
820.896s
```

为何如此长。

拆分：

```text
test setup
child startup
reader wait
GC
polling
cleanup
race overhead
```

---

# 40. 但性能不是第一目标

不要为了将 820s 降下来：

```text
remove checks
reduce protection window
skip cross-process scenario
```

correctness 优先。

---

# 41. 复现矩阵

最终至少输出：

| Mode           | Runs | Pass | Fail | Failure Rate |
| -------------- | ---: | ---: | ---: | -----------: |
| single normal  |      |      |      |              |
| single race    |      |      |      |              |
| package normal |      |      |      |              |
| package race   |      |      |      |              |

---

# 42. Root Cause Report

创建：

```text
docs/v0.6.3_tag_race_failure_analysis.md
```

包含：

```text
symptom
reproduction
event timeline
root cause
production impact
test impact
fix
validation
release decision
```

---

# 43. 如果不需要任何修改

只有当调查证明：

```text
source code correct
test correct
failure external/infrastructure-only
```

并有明确证据，

才允许考虑：

```text
rerun existing v0.6.3 Tag CI
```

---

# 44. 原 v0.6.3 Tag 的特殊规则

`v0.6.3` 已经推送且 immutable。

因此：

```text
NO SOURCE CHANGE
NO TEST CHANGE
```

才可能继续使用原 tag。

---

# 45. 如果需要任何源码修改

哪怕只有一行：

```text
production code
```

变更：

原 `v0.6.3` 不再可作为修复后的正式版本。

必须：

```text
new commit
full CI
new version
```

---

# 46. 如果只需要测试代码修改

即使 production code 不变，只要需要修改：

```text
cross_process_historical_reader_test.go
```

也不要重写 `v0.6.3`。

推荐：

```text
v0.6.4
```

因为正式 Tag Gate 已经暴露该 release source 的验证缺陷。

---

# 47. 版本决策

最终必须选择：

## Decision A

```text
v0.6.3 CONTINUE
```

仅当：

```text
NO code/test changes
external failure proven
existing source unchanged
```

---

## Decision B

```text
v0.6.4 REQUIRED
```

如果：

```text
production fix required
OR
test synchronization fix required
OR
CI/release logic change required
```

---

# 48. 不允许移动 v0.6.3

即使选择：

```text
v0.6.4
```

也必须保留：

```text
v0.6.3
```

原 tag 不动。

它可以是：

```text
failed release candidate
```

且没有 GitHub Release。

---

# 49. 修复范围

如果需要修复：

必须：

```text
minimal
targeted
regression-backed
```

不要顺手改 GC 架构。

---

# 50. Production Fix 示例边界

可能允许：

```text
recheck protected generation before deletion
atomic pin registration
transaction boundary correction
lease handling fix
```

但必须由 root cause 驱动。

不能提前实现。

---

# 51. Test Fix 示例边界

可能允许：

```text
durable pin acknowledgment
parent-visible barrier
deterministic child handshake
```

同样必须由证据驱动。

---

# 52. 新 regression tests

如果有真实 race：

至少加入：

```text
reader pinned before GC
reader/GC concurrent race
cross-process reader survives GC
reader release allows later GC
crashed reader eventually becomes collectible
```

按实际 contract 调整。

---

# 53. Release GC Regression

确保不破坏：

```text
generation cleanup
disk reclamation
stale generation deletion
```

避免修成：

```text
GC never deletes anything
```

---

# 54. Pin leak test

如果增强保护逻辑：

必须测试：

```text
reader exits
→ pin released/expired
→ generation eventually collectible
```

否则可能把 correctness bug 修成 storage leak。

---

# 55. Crash recovery

child process crash 时：

```text
pin cannot remain forever
```

必须符合现有 recovery contract。

---

# 56. Existing Storage Regression

必须重新运行：

```text
generation isolation
generation publish
rollback
delete
GC
restart
durable operations
```

相关测试。

---

# 57. Race soak

修复后必须：

```text
go test -race ./internal/knowledge \
  -run '^TestCrossProcessHistoricalReaderSurvivesGenerationGC$' \
  -count=20
```

PASS。

如果耗时现实上不可接受：

可使用等价 deterministic stress harness，

但正式 CI test 本身仍必须 PASS。

---

# 58. Package race

至少：

```text
go test -race ./internal/knowledge
```

PASS。

---

# 59. Full repository regression

修复后必须：

```text
go test ./...
go vet ./...
race
web typecheck
web tests
web build
browser E2E
```

全部通过。

---

# 60. Runtime fixes regression

不得破坏当前 v0.6.3 candidate 已验证：

```text
154,475 embeddings
reranker
cancel
restart
background search
bounded memory
```

至少执行代表性 smoke。

---

# 61. 不启动 Stage B

无论调查结果如何：

本任务期间：

```text
0.7 Structured Data Model Intelligence = FROZEN
```

直到 maintenance release 正式闭环。

---

# 62. 如果 Decision A：继续 v0.6.3

只有无任何修改且 external failure 被证明：

允许重新运行/触发原 tag CI。

必须：

```text
same tag object
same tag target
same source
```

---

# 63. Decision A release gate

只有新的完整：

```text
v0.6.3 Tag CI PASS
```

后：

才能：

```text
publish GitHub Release
```

---

# 64. 如果 Decision B：v0.6.4

流程：

```text
fix commit
↓
push CI PASS
↓
formal artifact
↓
package smoke
↓
annotated immutable v0.6.4
↓
Tag CI PASS
↓
GitHub Release
↓
post-download hash verify
↓
documentation-only closure
```

---

# 65. v0.6.4 定位

如果需要：

```text
Shutu-Knowledge v0.6.4
```

仍是：

```text
MAINTENANCE RELEASE
```

定位：

> Generation GC / Cross-Process Historical Reader Correctness & Release Closure

如果最终只是 test synchronization fix：

Release Notes 必须如实说明：

> release validation synchronization fix; no business feature expansion

---

# 66. 不要创建假 v0.6.3 GitHub Release

当前失败 candidate 不应发布 GitHub Release。

除非之后原 tag 自身完整 Tag CI 合法通过。

---

# 67. Release artifact lineage

任何正式发布必须保证：

```text
release source
=
artifact build source
=
tag target
```

除非项目既有 release pipeline 有明确不同机制。

不得混用旧 artifact 与新 source。

---

# 68. 如果进入 v0.6.4

必须重新构建 artifact。

不能复用：

```text
v0.6.3 artifact
```

哪怕 production code 没变而只改了 test。

正式 provenance 必须清晰。

---

# 69. 最终报告必须输出

## Classification

```text
PRODUCTION BUG
TEST BUG
BOTH
EXTERNAL INFRA FAILURE
```

只能根据证据选择。

---

# 70. Reproduction

输出：

```text
single normal:
single race:
package normal:
package race:
```

次数与失败率。

---

# 71. Timeline

输出关键时序：

```text
reader start
pin create
pin commit
parent observe
GC scan
GC delete
reader read
```

---

# 72. Root Cause

必须给出具体代码路径。

不能只写：

```text
race condition
```

---

# 73. Fix

如果有：

```text
files changed
behavior changed
why safe
```

---

# 74. Validation

至少：

```text
targeted test
race soak
package race
full CI
storage/generation regression
```

---

# 75. Release Decision

最终只能给：

```text
CONTINUE v0.6.3
```

或：

```text
CREATE v0.6.4
```

---

# 76. 授权边界

本任务授权：

```text
investigate
add diagnostics
run local tests
make minimal fix if root cause proven
push normal branch commits
open/update PR if needed
run CI
```

---

# 77. 不授权

本任务暂不授权：

```text
move/recreate v0.6.3 tag
publish failed v0.6.3 GitHub Release
push v0.6.4 tag
publish v0.6.4 GitHub Release
start 0.7
```

如果最终需要新 tag/release：

停在：

```text
release candidate fully verified
```

并报告 exact source SHA / artifact / CI evidence，等待显式 publication approval。

---

# 78. Stop Rule

如果 root cause 尚未证明：

不要：

```text
guess
change sleeps
weaken checks
rerun until green
```

继续调查。

---

# 79. Hard correctness rule

必须始终保持：

```text
A historical generation that is durably protected by an active reader must not be garbage-collected.
```

同时：

```text
An unprotected obsolete generation must eventually remain collectible.
```

两边都必须成立。

---

# 80. 最终成功定义

本任务成功不是：

```text
CI eventually turned green
```

而是：

> 明确知道为什么 v0.6.3 Tag CI 中 cross-process historical reader 在 generation GC 场景失败；证明这是生产代码、测试同步还是外部环境问题；若需要修复则用确定性 regression 锁住，并给出正确、不可变、可审计的 release path。

最终必须回答：

# “当另一个进程正在读取并已可靠 pin 住历史 generation 时，GC 是否在任何合法并发时序下都不会删除它需要的 chunks？”

只有这个问题有可重复证据支持的答案之后，maintenance release 才允许继续。
