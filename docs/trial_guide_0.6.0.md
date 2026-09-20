# Shutu Knowledge v0.6.0 试用测试指导

## 1. 试用目标

验证以下核心能力：

1. 正式包可以解压、初始化、启动并进入 Web UI。
2. 文档导入、索引、检索、引用证据可用。
3. 0.6 新增能力可用：
   - 当前版本理解
   - 指定历史版本理解
   - 历史阶段 / 演进理解
   - 模糊时间范围和历史范围检索
4. 重启后数据和模型可用。
5. 不出现伪造版本、伪造时间边界或无引用答案。

---

## 2. 环境要求

| 项目 | 建议 |
|---|---|
| 操作系统 | Windows 10 / 11 x64 |
| 内存 | 8 GB 起，推荐 16 GB |
| 磁盘 | 预留 10 GB 以上 |
| 网络 | 首次使用会下载本地模型、OCR、Node runtime 等托管组件 |
| 浏览器 | Chrome / Edge 最新稳定版 |
| 服务地址 | 默认 `127.0.0.1:7730`，不要直接暴露公网 |

正式包：

```text
shutu-knowledge-0.6.0-windows-amd64.zip
size: 12660331 bytes
SHA-256: d0178c173ef0af203af979267716521d9b0830bcb29b1d5a3e424ab716385e1a
```

下载地址：

```text
https://github.com/shutu-ai/Shutu-Knowledge/releases/download/v0.6.0/shutu-knowledge-0.6.0-windows-amd64.zip
```

---

## 3. 下载并校验

在 PowerShell 中执行：

```powershell
$root = "$env:USERPROFILE\ShutuKnowledgeTrial-0.6.0"
New-Item -ItemType Directory -Force -Path $root | Out-Null

$zip = "$root\shutu-knowledge-0.6.0-windows-amd64.zip"

# 如果你已手动下载，可把文件复制到 $root，再继续。

$expected = "d0178c173ef0af203af979267716521d9b0830bcb29b1d5a3e424ab716385e1a"
$actual = (Get-FileHash -Algorithm SHA256 -LiteralPath $zip).Hash.ToLowerInvariant()

if ($actual -ne $expected) {
    throw "SHA-256 mismatch: $actual"
}

$actual
```

校验通过后解压：

```powershell
Expand-Archive -LiteralPath $zip -DestinationPath "$root\package" -Force
```

---

## 4. 初始化并启动

建议为试用准备独立数据目录，避免影响已有数据。

```powershell
$env:SHUTU_KNOWLEDGE_HOME = "$root\trial-data"

& "$root\package\bin\shutu-knowledge.exe" doctor --init
& "$root\package\bin\shutu-knowledge.exe" doctor
```

启动服务：

```powershell
& "$root\package\bin\shutu-knowledge.exe" serve
```

保持这个 PowerShell 窗口运行。然后打开：

```text
http://127.0.0.1:7730
```

如果端口被占用，可以在启动前改端口：

```powershell
$env:SHUTU_KNOWLEDGE_ADDR = "127.0.0.1:7800"
```

然后访问对应端口。

---

## 5. 基础健康检查

另开一个 PowerShell 窗口：

```powershell
$api = "http://127.0.0.1:7730"

Invoke-RestMethod "$api/api/status"
Invoke-RestMethod "$api/api/runtime-status"
Invoke-RestMethod "$api/api/version"
```

预期：

- `/api/status` 中服务 ready。
- `/api/runtime-status` 能显示托管 runtime 状态。
- `/api/version` 显示 `0.6.0`。

---

## 6. 创建试用知识库

### 方式 A：Web UI

1. 打开 `http://127.0.0.1:7730`。
2. 进入 **Bases / 知识库**。
3. 创建一个新知识库，例如：
   - Name: `Trial 0.6 History`
   - Group: `Trial`
4. 进入 **Import / 导入**。
5. 导入几份 Markdown、TXT、PDF 或 DOCX 文件。
6. 进入 **Documents / 文档** 页面，等待文档状态变为 ready。
7. 进入 **Recall / 检索** 页面进行查询。

### 方式 B：API 快速构造版本化测试数据

使用下面的脚本创建一个简单、可控的试用知识库。

```powershell
$api = "http://127.0.0.1:7730"

$baseBody = @{
    name        = "Trial 0.6 History"
    description = "Windows package trial for range and historical reasoning"
    group       = "Trial"
    config      = @{}
} | ConvertTo-Json -Depth 10

$base = (
    Invoke-RestMethod `
        -Method Post `
        -Uri "$api/api/bases" `
        -ContentType "application/json" `
        -Body $baseBody
).value

$base.id

function Wait-Operation($operationId) {
    do {
        Start-Sleep -Milliseconds 500
        $op = (Invoke-RestMethod "$api/api/operations/$operationId").value
    } until ($op.state -in @("succeeded", "failed", "cancelled"))

    if ($op.state -ne "succeeded") {
        throw "Operation failed: $($op.errorMessage)"
    }
}

$docs = @(
    @{
        title   = "Release 1.0.0"
        content = "# Release 1.0.0`nRelease 1.0.0 introduced alpha indexing."
    },
    @{
        title   = "Release 1.1.0"
        content = "# Release 1.1.0`nRelease 1.1.0 introduced beta filters and retained alpha indexing."
    },
    @{
        title   = "Release 1.2.0"
        content = "# Release 1.2.0`nRelease 1.2.0 introduced gamma caching and is the current release."
    }
)

foreach ($doc in $docs) {
    $body = $doc | ConvertTo-Json -Depth 5

    $accepted = (
        Invoke-RestMethod `
            -Method Post `
            -Uri "$api/api/bases/$($base.id)/documents" `
            -ContentType "application/json" `
            -Body $body
    ).value

    Wait-Operation $accepted.operationId
}
```

---

## 7. 0.6 历史 / 时间范围试用用例

以下接口会返回 Knowledge 编译出的上下文，而不是让模型自由发挥。重点检查 `temporalIntent`、`resolvedVersion`、`evidence` 和 `citations`。

```powershell
function Test-HistoryQuery($name, $query) {
    $body = @{
        query       = $query
        tokenBudget = 4096
    } | ConvertTo-Json -Depth 5

    $result = (
        Invoke-RestMethod `
            -Method Post `
            -Uri "$api/api/bases/$($base.id)/semantic/context" `
            -ContentType "application/json" `
            -Body $body
    ).value

    [pscustomobject]@{
        Case            = $name
        Query           = $query
        Intent          = $result.temporalIntent
        ResolvedVersion = $result.resolvedVersion
        EvidenceCount   = @($result.evidence).Count
        Citations       = @($result.citations).Count
    }

    return $result
}
```

### 用例 1：当前版本

```powershell
$current = Test-HistoryQuery "Current" "What is the current release feature?"
```

预期：

```text
Intent: CURRENT
Evidence: 大于 0
Evidence 中应能找到 Release 1.2.0 / gamma caching
Citations: 大于 0
```

---

### 用例 2：指定历史版本

```powershell
$explicit = Test-HistoryQuery "Explicit version" "What was true in version 1.1.0?"
```

预期：

```text
Intent: AS_OF_VERSION
ResolvedVersion: 1.1.0
Evidence 中应能找到 beta filters
不应把 1.2.0 的 gamma caching 当作 1.1.0 的事实
```

---

### 用例 3：历史范围

```powershell
$range = Test-HistoryQuery "Range history" "What happened before release 1.2.0?"
```

预期：

```text
Intent: RANGE_HISTORY
Evidence: 大于 0
Evidence 应围绕 1.2.0 之前的阶段或边界
Citations: 大于 0
```

---

### 用例 4：版本演进

```powershell
$evolution = Test-HistoryQuery "Evolution" "How did indexing evolve from 1.0.0 to 1.2.0?"
```

预期：

- 能检索到 1.0.0 和后续版本的相关证据。
- 不应只返回单一最新版本。
- 不应伪造未导入的版本或功能。

---

### 用例 5：模糊历史问题

```powershell
$ambiguous = Test-HistoryQuery "Ambiguous history" "What did the early system look like?"
```

预期：

- 返回早期阶段的证据。
- 不应随意捏造一个具体版本。
- 如果证据不足以确定精确边界，应保留模糊性并给出引用。

---

### 用例 6：未知版本

```powershell
$unknown = Test-HistoryQuery "Unknown version" "What changed in release 9.9.9?"
```

预期：

- 不应把不存在于知识库中的 `9.9.9` 当作事实。
- 应缺少证据、明确说明无法支持，或返回失败/空结果。
- 不应编造 release notes。

---

## 8. 普通检索回归

在 Web UI 的 **Recall / 检索** 中测试：

```text
What is alpha indexing?
What is gamma caching?
What capabilities does the system provide?
```

预期：

- 能返回相关文档。
- 能显示来源、文档标题或引用。
- 不会因为普通问题被误判成历史版本查询。
- 不会把普通全局问题错误限制到某个版本。

也可以用 API：

```powershell
Invoke-RestMethod `
    -Method Post `
    -Uri "$api/api/search" `
    -ContentType "application/json" `
    -Body (@{
        query   = "What is gamma caching?"
        mode    = "auto"
        topK    = 5
        baseIds = @($base.id)
    } | ConvertTo-Json -Depth 5)
```

---

## 9. 重启与持久化测试

1. 回到服务窗口，按 `Ctrl+C` 停止服务。
2. 重新启动：

```powershell
$env:SHUTU_KNOWLEDGE_HOME = "$root\trial-data"
& "$root\package\bin\shutu-knowledge.exe" serve
```

3. 再次打开 Web UI。
4. 检查：
   - 知识库仍存在。
   - 文档仍存在。
   - 检索仍能返回相同主题。
   - 历史查询仍能返回正确意图和引用。

可选离线重启测试：

```powershell
$env:SHUTU_KNOWLEDGE_OFFLINE = "1"
& "$root\package\bin\shutu-knowledge.exe" serve
```

如果你已经完成首次模型下载，离线重启后基础检索仍应可用。

---

## 10. 试用通过标准

| 类别 | 通过标准 |
|---|---|
| 安装启动 | zip 校验通过，服务启动，Web UI 可访问 |
| 文档导入 | 文档状态 ready，chunk 数大于 0 |
| 基础检索 | 相关结果稳定，来源可见 |
| 当前版本 | `CURRENT`，返回当前版本证据 |
| 指定版本 | `AS_OF_VERSION`，返回指定版本证据 |
| 历史范围 | `RANGE_HISTORY`，返回边界相关证据 |
| 演进 | 返回多阶段证据，不退化为单版本 |
| 模糊历史 | 不伪造精确边界，保留不确定性 |
| 未知版本 | 不编造事实 |
| 引用 | 每个关键结论都有可追踪 evidence/citation |
| 重启 | 数据和检索能力保留 |

---

## 11. 常见问题

### 1. 首次查询很慢

第一次使用本地 embedding、OCR、PDF 渲染等能力时，会自动下载和初始化托管 runtime。请保持网络可用并等待完成。

### 2. 端口被占用

```powershell
$env:SHUTU_KNOWLEDGE_ADDR = "127.0.0.1:7800"
```

然后重新启动，并访问 `http://127.0.0.1:7800`。

### 3. 文档一直不在 ready 状态

检查导入操作：

```powershell
Invoke-RestMethod "$api/api/operations?limit=100"
```

也可以查看服务窗口日志。不要重复导入同一批文件，先等待当前 operation 完成。

### 4. 查询结果没有历史意图

确认查询里确实包含时间 / 版本 / 历史 / 边界语义，例如：

```text
current
version 1.1.0
before release 1.2.0
what changed from 1.0.0 to 1.2.0
early history
```

### 5. 需要清理试用数据

停止服务后删除：

```powershell
Remove-Item -LiteralPath "$env:USERPROFILE\ShutuKnowledgeTrial-0.6.0" -Recurse -Force
```

这会同时删除试用包和独立数据目录。
