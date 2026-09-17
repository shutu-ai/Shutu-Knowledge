param(
    [string] $AgentBinary = "C:\dev-projects\Agent\shutu-agent\sta.exe",
    [string] $RepoRoot = (Split-Path -Parent $PSScriptRoot),
    [string] $OutputRoot = ""
)

$ErrorActionPreference = "Stop"
$RepoRoot = [IO.Path]::GetFullPath($RepoRoot)
$AgentBinary = [IO.Path]::GetFullPath($AgentBinary)
if (-not $env:GOCACHE) { $env:GOCACHE = Join-Path $RepoRoot ".gocache" }
if (-not (Test-Path $AgentBinary)) { throw "Agent binary not found: $AgentBinary" }
$AgentDist = Join-Path (Split-Path -Parent $AgentBinary) "web\dist"
if (-not (Test-Path (Join-Path $AgentDist "index.html"))) { throw "Agent web dist not found: $AgentDist" }
if (-not $OutputRoot) { $OutputRoot = Join-Path $RepoRoot ".gocache\removal-gate" }
$OutputRoot = [IO.Path]::GetFullPath($OutputRoot)
New-Item -ItemType Directory -Force -Path $OutputRoot | Out-Null

$knowledgeBinary = Join-Path $OutputRoot "shutu-knowledge-gate.exe"
if (-not (Test-Path $knowledgeBinary)) {
    Push-Location $RepoRoot
    try {
        go build -o $knowledgeBinary ./cmd/shutu-knowledge
        if ($LASTEXITCODE -ne 0) { throw "knowledge build failed" }
    } finally {
        Pop-Location
    }
}

$manifestPath = Join-Path $OutputRoot "extension.yaml"
$sourceManifest = Join-Path $RepoRoot "extension.yaml"
$escapedBinary = ($knowledgeBinary -replace "\\", "/")
(Get-Content $sourceManifest -Raw) -replace "command: shutu-knowledge", ("command: " + $escapedBinary + "`n  env:`n    - SHUTU_KNOWLEDGE_HOME=" + (($OutputRoot -replace "\\", "/") + "/knowledge-data")) |
    Set-Content -LiteralPath $manifestPath

$dataDir = ($OutputRoot -replace "\\", "/") + "/agent-data"
$withConfig = Join-Path $OutputRoot "agent-with.yaml"
$withoutConfig = Join-Path $OutputRoot "agent-without.yaml"
$expectedKnowledgeTools = @(
    "ext__shutu-knowledge__knowledge_add_document",
    "ext__shutu-knowledge__knowledge_create_base",
    "ext__shutu-knowledge__knowledge_delete_base",
    "ext__shutu-knowledge__knowledge_delete_document",
    "ext__shutu-knowledge__knowledge_get_document",
    "ext__shutu-knowledge__knowledge_import_url",
    "ext__shutu-knowledge__knowledge_list_bases",
    "ext__shutu-knowledge__knowledge_list_documents",
    "ext__shutu-knowledge__knowledge_maintenance_storage",
    "ext__shutu-knowledge__knowledge_operation_cancel",
    "ext__shutu-knowledge__knowledge_operation_retry",
    "ext__shutu-knowledge__knowledge_operation_status",
    "ext__shutu-knowledge__knowledge_read_document",
    "ext__shutu-knowledge__knowledge_refresh_url",
    "ext__shutu-knowledge__knowledge_reindex_base",
    "ext__shutu-knowledge__knowledge_reindex_document",
    "ext__shutu-knowledge__knowledge_search",
    "ext__shutu-knowledge__knowledge_stats"
)
$toolLines = @("    - get_time", "    - read") + ($expectedKnowledgeTools | ForEach-Object { "    - $_" })
$toolBlock = $toolLines -join "`n"
$reserveListener = [System.Net.Sockets.TcpListener]::new([System.Net.IPAddress]::Loopback, 0)
$reserveListener.Start()
$Port = $reserveListener.LocalEndpoint.Port
$reserveListener.Stop()
@"
data_dir: $dataDir
tools:
  enabled:
$toolBlock
extensions:
  enabled: true
  sources:
    - manifest: $(($manifestPath -replace "\\", "/"))
  startup_timeout_ms: 120000
  health_timeout_ms: 3000
  context_timeout_ms: 4000
  shutdown_timeout_ms: 5000
web_server:
  enabled: true
  addr: 127.0.0.1:$Port
  token: ""
  dist_dir: $(($AgentDist -replace "\\", "/"))
"@ | Set-Content -LiteralPath $withConfig
@"
data_dir: $dataDir
extensions:
  enabled: false
web_server:
  enabled: true
  addr: 127.0.0.1:$Port
  token: ""
  dist_dir: $(($AgentDist -replace "\\", "/"))
"@ | Set-Content -LiteralPath $withoutConfig

function Invoke-CatalogGate([string] $Config, [string] $Catalog, [string] $ErrorLog) {
    $command = "`"$AgentBinary`" --config `"$Config`" --catalog-manifest `"$Catalog`" 2> `"$ErrorLog`""
    & $env:ComSpec /c $command
    if ($LASTEXITCODE -ne 0) {
        throw "Agent catalog run failed ($LASTEXITCODE); see $ErrorLog"
    }
    $catalogValue = Get-Content $Catalog -Raw | ConvertFrom-Json
    @($catalogValue.tools | Where-Object { $_.name -like "*shutu-knowledge*" }).Count
}

function Stop-ProcessTree([int] $RootId) {
    $pending = [System.Collections.Generic.Stack[int]]::new()
    $pending.Push($RootId)
    $seen = [System.Collections.Generic.HashSet[int]]::new()
    while ($pending.Count -gt 0) {
        $processId = $pending.Pop()
        if (-not $seen.Add($processId)) { continue }
        foreach ($child in @(Get-CimInstance Win32_Process -Filter "ParentProcessId = $processId" -ErrorAction SilentlyContinue)) {
            $pending.Push([int]$child.ProcessId)
        }
        Stop-Process -Id $processId -Force -ErrorAction SilentlyContinue
    }
}

function Invoke-WebStateGate([string] $Config, [string] $StateName) {
    $startInfo = [System.Diagnostics.ProcessStartInfo]::new()
    $startInfo.FileName = $AgentBinary
    $startInfo.Arguments = '--web-only --config "' + $Config + '"'
    $startInfo.WorkingDirectory = $OutputRoot
    $startInfo.UseShellExecute = $false
    $startInfo.CreateNoWindow = $true
    $agentProcess = [System.Diagnostics.Process]::new()
    $agentProcess.StartInfo = $startInfo
    $agentProcess.Start() | Out-Null
    try {
        $health = $null
        $extensions = $null
        foreach ($attempt in 1..40) {
            if ($agentProcess.HasExited) {
                throw "Agent web process exited with $($agentProcess.ExitCode) during $StateName"
            }
            try {
                $health = Invoke-RestMethod -Uri "http://127.0.0.1:$Port/api/health" -TimeoutSec 1
                $extensions = Invoke-RestMethod -Uri "http://127.0.0.1:$Port/api/extensions" -TimeoutSec 1
                break
            } catch {
                Start-Sleep -Milliseconds 250
            }
        }
        if (-not $health.ok) { throw "Agent health is not ok during $StateName" }
        if ($null -eq $extensions) { throw "Agent extension inventory is unavailable during $StateName" }
        $knowledgeRoutes = @($extensions.extensions | Where-Object { $_.extensionId -eq "shutu-knowledge" })
        return @{
            Health = $health.ok
            Routes = $knowledgeRoutes.Count
        }
    } finally {
        if (-not $agentProcess.HasExited) {
            Stop-ProcessTree $agentProcess.Id
            $agentProcess.WaitForExit(5000) | Out-Null
        }
        $agentProcess.Dispose()
    }
}

$withCount = Invoke-CatalogGate $withConfig (Join-Path $OutputRoot "catalog-with.json") (Join-Path $OutputRoot "with.err")
$withoutCount = Invoke-CatalogGate $withoutConfig (Join-Path $OutputRoot "catalog-without.json") (Join-Path $OutputRoot "without.err")
$withWeb = Invoke-WebStateGate $withConfig "installed"
$withoutWeb = Invoke-WebStateGate $withoutConfig "removed"

$catalogWith = Get-Content -LiteralPath (Join-Path $OutputRoot "catalog-with.json") -Raw | ConvertFrom-Json
$actualKnowledgeTools = @($catalogWith.tools | Where-Object { $_.name -like "ext__shutu-knowledge__*" } | ForEach-Object { $_.name })
$missingKnowledgeTools = @($expectedKnowledgeTools | Where-Object { $_ -notin $actualKnowledgeTools })
$unexpectedKnowledgeTools = @($actualKnowledgeTools | Where-Object { $_ -notin $expectedKnowledgeTools })

[pscustomobject]@{
    InstalledTools = $withCount
    RemovedTools   = $withoutCount
    InstalledRoutes = $withWeb.Routes
    RemovedRoutes   = $withoutWeb.Routes
    RemovedAgentHealthy = $withoutWeb.Health
    Catalog        = "PASS"
    MissingTools   = $missingKnowledgeTools -join ","
    UnexpectedTools = $unexpectedKnowledgeTools -join ","
} | Format-List

if ($withCount -ne $expectedKnowledgeTools.Count -or $withoutCount -ne 0 -or
    $missingKnowledgeTools.Count -gt 0 -or $unexpectedKnowledgeTools.Count -gt 0) {
    throw "removal gate failed: installed=$withCount removed=$withoutCount"
}
if ($withWeb.Routes -ne 1 -or -not $withWeb.Health) {
    throw "removal gate failed: installed route/health=$($withWeb.Routes)/$($withWeb.Health)"
}
if ($withoutWeb.Routes -ne 0 -or -not $withoutWeb.Health) {
    throw "removal gate failed: removed route/health=$($withoutWeb.Routes)/$($withoutWeb.Health)"
}
