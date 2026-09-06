param(
    [string] $AgentBinary = "C:\dev-projects\Agent\shutu-agent\sta.exe",
    [string] $RepoRoot = (Split-Path -Parent $PSScriptRoot),
    [string] $OutputRoot = ""
)

$ErrorActionPreference = "Stop"
if (-not $env:GOCACHE) { $env:GOCACHE = Join-Path $RepoRoot ".gocache" }
if (-not (Test-Path $AgentBinary)) { throw "Agent binary not found: $AgentBinary" }
$AgentDist = Join-Path (Split-Path -Parent $AgentBinary) "web\dist"
if (-not (Test-Path (Join-Path $AgentDist "index.html"))) { throw "Agent web dist not found: $AgentDist" }
if (-not $OutputRoot) { $OutputRoot = Join-Path $RepoRoot ".gocache\removal-gate" }
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
$reserveListener = [System.Net.Sockets.TcpListener]::new([System.Net.IPAddress]::Loopback, 0)
$reserveListener.Start()
$Port = $reserveListener.LocalEndpoint.Port
$reserveListener.Stop()
@"
data_dir: $dataDir
extensions:
  enabled: true
  sources:
    - manifest: $(($manifestPath -replace "\\", "/"))
  startup_timeout_ms: 10000
  health_timeout_ms: 1000
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

[pscustomobject]@{
    InstalledTools = $withCount
    RemovedTools   = $withoutCount
    InstalledRoutes = $withWeb.Routes
    RemovedRoutes   = $withoutWeb.Routes
    RemovedAgentHealthy = $withoutWeb.Health
    Catalog        = "PASS"
} | Format-List

if ($withCount -ne 14 -or $withoutCount -ne 0) {
    throw "removal gate failed: installed=$withCount removed=$withoutCount"
}
if ($withWeb.Routes -ne 1 -or -not $withWeb.Health) {
    throw "removal gate failed: installed route/health=$($withWeb.Routes)/$($withWeb.Health)"
}
if ($withoutWeb.Routes -ne 0 -or -not $withoutWeb.Health) {
    throw "removal gate failed: removed route/health=$($withoutWeb.Routes)/$($withoutWeb.Health)"
}
