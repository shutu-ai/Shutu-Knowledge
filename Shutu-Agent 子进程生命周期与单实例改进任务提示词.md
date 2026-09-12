# Shutu-Agent 子进程生命周期与单实例改进任务提示词

请在 `C:\dev-projects\Agent\shutu-agent` 项目内执行以下专项改造。任务范围限定为 Agent 侧的 web-only 单实例管理、扩展子进程生命周期管理和启动失败诊断；不要通过修改 Shutu-Knowledge 业务逻辑来掩盖 Agent 的生命周期问题。

## 一、问题背景与已确认现象

Shutu-Knowledge 通过以下方式被 Agent 托管：

```powershell
sta.exe --web-only --config <temporary-agent-config.yaml>
```

临时 Agent 配置中的扩展传输为：

```yaml
transport:
  type: stdio
  command: shutu-knowledge
  args:
    - extension
```

已观察到以下问题：

1. Agent 初始化扩展时可能报错：

   ```text
   extension: initialize shutu-knowledge: extension: connection lost: process exited during initialize
   ```

2. Agent 退出后，Windows 上仍可能残留：

   ```text
   shutu-knowledge.exe extension
   ```

   且其父进程已经不存在。

3. 再次运行启动脚本时可能出现：

   ```text
   web server: listen tcp 127.0.0.1:18099: bind: Only one usage of each socket address ...
   ```

4. 多次启动可能留下多个 `sta.exe`、多个 Knowledge 扩展子进程，导致初始化失败、端口占用、数据库/资源占用和 Web UI 启动不确定。

当前 Agent 代码中需要重点审计的区域包括：

- `internal/extensionhost/transport.go`
  - `stdioConnection.startLocked`
  - `stdioConnection.Call`
  - `stdioConnection.Close`
  - 子进程 `exec.Cmd` 的启动、等待、关闭和错误传播
- `internal/extensionhost/host.go`
  - 扩展 initialize 失败后的连接回收
  - 扩展替换、重启和卸载路径
- `internal/extensionhost/lifecycle.go`
  - restart policy、初始化失败和 shutdown 路径
- `cmd/sta/main.go`
  - `--web-only` 启动顺序
  - Web listener 创建、扩展初始化和重复实例行为
- Agent 现有的进程树实现，例如 `internal/code/process_tree_*`，应优先复用其设计和平台约定，不要重复发明互相冲突的实现。

## 二、改造目标

### 1. Agent web-only 必须具备单实例保护

对 `sta.exe --web-only` 增加可靠的单实例机制：

- 同一个 web-only 配置/端口不能重复运行多个 Agent 实例。
- 第二次启动必须快速失败并给出明确错误，例如说明端口或实例已被占用。
- 重复实例失败前，不应先初始化扩展、启动模型、创建会话或产生子进程。
- 正常 Agent 退出后，锁必须可靠释放。
- 异常退出后，不能永久留下不可恢复的锁；应使用 Windows named mutex、锁文件租约或其他具有明确崩溃恢复语义的方案。
- 不得按进程名全局杀掉所有 `sta.exe`，因为用户可能确实运行多个不同配置实例。

优先保证 listener/单实例保护在扩展初始化之前生效，避免重复实例先启动 Knowledge 子进程后才发现 18099 被占用。

### 2. Agent 必须拥有并回收扩展子进程

对于 stdio 扩展子进程，建立明确的 ownership contract：

- 每个 `stdioConnection` 记录自己启动的 `exec.Cmd`、PID、stdin/stdout、wait 状态和进程树句柄。
- Agent 正常 shutdown 时，必须关闭 stdin、停止读取、等待子进程退出；超时后强制终止整个子进程树。
- Agent 初始化失败、协议握手失败、扩展被替换、restart、连接关闭时，都必须走同一个幂等清理路径。
- Agent 自身突然退出时，Windows 上仍必须能回收其扩展子进程。优先使用 Windows Job Object，并设置适当的 kill-on-job-close 语义；Unix 使用现有进程组/进程树方案。
- 子进程的孙进程也不能在 Agent 退出后存活。
- 清理逻辑必须可重复调用、线程安全，不能因 wait goroutine、Close 和错误路径并发执行而 double close、死锁或泄漏。
- 不得通过“扫描所有同名进程然后杀掉”实现 ownership；只能终止当前 Agent 实例实际启动并拥有的 PID/进程树。

### 3. initialize 失败必须可诊断

当前子进程 stderr 被静默丢弃，导致 Agent 只能报告 `process exited during initialize`。请改为：

- 保留子进程退出码、启动错误和 stderr 的有限尾部内容。
- stderr 必须有上限，避免异常子进程无限输出撑爆内存或日志。
- 错误信息应包含扩展 ID、调用阶段、退出原因和 stderr 尾部（如有）。
- 不得输出 API key、token、密码、cookie 或完整环境变量。
- 进程退出时，所有等待中的 RPC 请求都必须得到确定错误；不能永久阻塞。
- 初始化失败后，子进程必须确认已退出或已被终止，不能只清理 Go 对象。

### 4. 启动顺序和失败隔离

请检查并修正 web-only 启动顺序，使以下条件成立：

- 单实例锁/HTTP listener 冲突在扩展初始化之前可见。
- Knowledge 扩展 initialize 慢、失败或退出，不应留下一个不可用但仍占用 18099 的半启动 Agent。
- 扩展失败时，Agent 主进程应给出可读的失败状态，并完整清理本次启动的子进程。
- 一个扩展的失败不能泄漏子进程，也不能破坏其他已运行扩展。
- shutdown 和 restart 必须等待旧连接清理完成，再创建替代连接。

## 三、实现要求

1. 先阅读现有 `extensionhost`、`cmd/sta` 和进程树实现，给出根因说明和拟修改文件清单，再开始编辑。
2. 优先抽取一个小而明确的 owned-process/owned-process-tree 抽象，避免把平台分支散落在 transport.go 中。
3. 明确区分：
   - Agent 实例锁
   - Agent Web listener
   - 扩展 stdio 连接
   - 扩展子进程/孙进程树
   - 扩展初始化失败
4. 所有 cleanup API 必须幂等并可从正常、失败、取消、超时和进程异常退出路径调用。
5. 不要修改扩展协议版本，不要改变已有扩展工具语义，不要通过放宽超时掩盖进程泄漏。
6. 保留现有非目标行为和兼容性；如果发现现有测试或用户未提交改动，先隔离，不要覆盖。
7. 如果需要修改 Agent 的日志或错误类型，保持现有调用方可识别的错误链，并补充稳定的错误上下文。

## 四、必须补充的测试

至少补充以下测试；Windows 专属测试必须在 Windows runner 上真实执行：

### 单实例

- 同一 web-only 端口/配置启动两次，第二次在初始化扩展前失败。
- 第一个实例正常退出后，第二次可以立即启动。
- 第一个实例被强制终止后，第二次不会被永久锁阻塞。
- 两个不同配置、不同端口的实例仍可按现有设计并行运行（如果产品明确不支持，必须写清楚并测试拒绝理由）。

### 扩展进程生命周期

- initialize 成功后关闭 Agent，Knowledge 子进程及其孙进程全部退出。
- initialize 失败后，子进程及其孙进程全部退出。
- 子进程在 initialize 期间主动退出，Agent 返回带退出原因/stderr 尾部的错误，且无 goroutine/RPC 请求永久阻塞。
- 扩展 restart/替换时，旧子进程先被回收，再启动新子进程。
- 并发 Close、wait、请求失败路径不会 panic、死锁或 double close。
- Windows Job Object/进程树回收必须有真实 child-process fixture 证明，不能只测试 mock。

### web-only 集成

- 用固定端口启动 Agent，确认页面可访问。
- 重复执行启动命令，确认不会产生第二个 Agent 或第二个 Knowledge 扩展。
- 终止 Agent 后检查 `sta.exe`、`shutu-knowledge.exe extension` 和监听端口均已释放。
- 连续启动/停止至少 3 次，确认没有残留进程和端口占用。

## 五、建议验证命令

根据仓库现有脚本调整包路径，但至少执行：

```powershell
go test ./internal/extensionhost/... -count=1
go test ./cmd/sta/... -count=1
go test ./... -count=1
go test -race ./... -count=1
```

然后执行 Windows 真实集成验证：

```powershell
.\sta.exe --web-only --config <test-config.yaml>
```

并在另一个 PowerShell 中重复启动、停止、检查进程和端口。检查时使用精确的 PID、命令行和父子关系；不要使用全局 `Stop-Process` 杀掉所有同名进程。

## 六、验收标准

只有同时满足以下条件才算完成：

- 重复 web-only 启动被可靠阻止，且第二个实例不会先启动扩展子进程。
- Agent 正常退出、异常退出、initialize 失败、扩展重启后均无 Knowledge 扩展孤儿进程。
- Windows 子进程树回收有真实测试证据。
- initialize 失败信息不再只有 `process exited during initialize`，而是包含安全的退出原因和有限 stderr 诊断。
- 所有现有 Agent 测试保持通过，新增测试通过。
- 连续 3 次真实启动/停止通过，18099 可重复绑定，进程列表无本次测试残留。
- 最终报告必须列出：根因、修改文件、生命周期状态图或文字流程、测试命令及结果、已知限制。

## 七、禁止事项

- 不要修改 Shutu-Knowledge 的业务功能来掩盖 Agent 子进程泄漏。
- 不要按 `shutu-knowledge.exe` 或 `sta.exe` 镜像名全局杀进程。
- 不要把 stderr 永久丢弃。
- 不要只增加启动等待时间来“修复” initialize 断连。
- 不要把失败的单实例测试、真实 Windows 子进程测试或重复启停测试标记为 PASS。
- 不要提交与本任务无关的 UI、模型、检索或知识库数据改动。

请先输出根因审计结果和改造计划，再实施代码修改；完成后给出上述验收证据。
