[CmdletBinding()]
param(
    [string] $AgentRoot = "",
    [string] $KnowledgeRoot = "",
    [switch] $SkipKnowledgeBuild,
    [switch] $OpenBrowser
)

$ErrorActionPreference = "Stop"

if (-not $KnowledgeRoot) {
    $KnowledgeRoot = Split-Path -Parent $PSScriptRoot
}
$KnowledgeRoot = [IO.Path]::GetFullPath($KnowledgeRoot)
if (-not $AgentRoot) {
    $AgentRoot = $env:SHUTU_AGENT_ROOT
}
if (-not $AgentRoot) {
    $AgentRoot = Join-Path (Split-Path -Parent $KnowledgeRoot) "Agent\shutu-agent"
}
$AgentRoot = [IO.Path]::GetFullPath($AgentRoot)

$agentExe = Join-Path $AgentRoot "sta.exe"
if (-not (Test-Path -LiteralPath $agentExe -PathType Leaf)) {
    throw "Agent executable not found: $agentExe (set SHUTU_AGENT_ROOT or -AgentRoot)"
}
$agentWebBuilder = Join-Path $KnowledgeRoot "scripts\build-agent-web-dist.ps1"
if (-not (Test-Path -LiteralPath $agentWebBuilder -PathType Leaf)) {
    throw "Knowledge-owned Agent Web builder not found: $agentWebBuilder"
}
$agentDist = Join-Path $KnowledgeRoot "agent-web-dist"
& $agentWebBuilder -AgentRoot $AgentRoot -KnowledgeRoot $KnowledgeRoot
if (-not (Test-Path -LiteralPath $agentDist -PathType Container)) {
    throw "Knowledge-owned Agent Web assets not found: $agentDist"
}

$manifest = Join-Path $KnowledgeRoot "extension.yaml"
if (-not (Test-Path -LiteralPath $manifest -PathType Leaf)) {
    throw "Knowledge extension manifest not found: $manifest"
}

$knowledgeBinDir = Join-Path $KnowledgeRoot ".tmp"
$knowledgeExe = Join-Path $knowledgeBinDir "shutu-knowledge.exe"

function Remove-OrphanKnowledgeExtensions([string] $Executable) {
    $processes = Get-CimInstance Win32_Process -Filter "Name='shutu-knowledge.exe'" -ErrorAction SilentlyContinue |
        Where-Object {
            $commandLine = [string] $_.CommandLine
            $commandLine.IndexOf($Executable, [StringComparison]::OrdinalIgnoreCase) -ge 0 -and
                $commandLine.TrimEnd().EndsWith(" extension", [StringComparison]::OrdinalIgnoreCase)
        }
    foreach ($process in $processes) {
        if (-not (Get-Process -Id $process.ParentProcessId -ErrorAction SilentlyContinue)) {
            Write-Host "Removing orphan Knowledge extension process $($process.ProcessId)..."
            Stop-Process -Id $process.ProcessId -Force -ErrorAction SilentlyContinue
        }
    }
}

function Assert-AgentWebPortAvailable([int] $Port) {
    $listeners = @(Get-NetTCPConnection -LocalPort $Port -State Listen -ErrorAction SilentlyContinue)
    if ($listeners.Count -eq 0) {
        return
    }
    $owners = foreach ($listener in $listeners) {
        $process = Get-CimInstance Win32_Process -Filter "ProcessId=$($listener.OwningProcess)" -ErrorAction SilentlyContinue
        if ($null -ne $process) {
            "PID $($process.ProcessId) $($process.Name): $($process.CommandLine)"
        } else {
            "PID $($listener.OwningProcess)"
        }
    }
    $ownerText = $owners -join "; "
    throw "Agent Web port $Port is already in use. Close the existing Agent Web instance before retrying. Current listener: $ownerText"
}

Remove-OrphanKnowledgeExtensions $knowledgeExe
Assert-AgentWebPortAvailable 18099
$needsKnowledgeBuild = -not $SkipKnowledgeBuild
if ($needsKnowledgeBuild) {
    New-Item -ItemType Directory -Force -Path $knowledgeBinDir | Out-Null
    $previousGoCache = $env:GOCACHE
    $env:GOCACHE = Join-Path $KnowledgeRoot ".gocache"
    Push-Location $KnowledgeRoot
    try {
        & go build -trimpath -o $knowledgeExe ./cmd/shutu-knowledge
        if ($LASTEXITCODE -ne 0) {
            throw "Knowledge binary build failed"
        }
    } finally {
        if ($null -eq $previousGoCache) {
            Remove-Item Env:GOCACHE -ErrorAction SilentlyContinue
        } else {
            $env:GOCACHE = $previousGoCache
        }
        Pop-Location
    }
}

function ConvertTo-YamlPath([string] $Path) {
    return $Path.Replace("\", "/").Replace('"', '\"')
}

$agentData = Join-Path ([IO.Path]::GetTempPath()) ("shutu-agent-knowledge-" + [guid]::NewGuid().ToString("N"))
$tempConfig = Join-Path ([IO.Path]::GetTempPath()) ("shutu-agent-knowledge-" + [guid]::NewGuid().ToString("N") + ".yaml")
$manifestYaml = ConvertTo-YamlPath $manifest
$agentDistYaml = ConvertTo-YamlPath $agentDist
$agentDataYaml = ConvertTo-YamlPath $agentData
$promptsYaml = ConvertTo-YamlPath (Join-Path $AgentRoot "config\prompts")

$extensionTools = @(
    "knowledge_search",
    "knowledge_list_bases",
    "knowledge_create_base",
    "knowledge_delete_base",
    "knowledge_add_document",
    "knowledge_list_documents",
    "knowledge_delete_document",
    "knowledge_import_url",
    "knowledge_refresh_url",
    "knowledge_stats",
    "knowledge_get_document",
    "knowledge_read_document",
    "knowledge_reindex_document",
    "knowledge_reindex_base",
    "knowledge_operation_status",
    "knowledge_operation_cancel",
    "knowledge_operation_retry",
    "knowledge_maintenance_storage"
)
$toolLines = @("    - get_time", "    - read") + ($extensionTools | ForEach-Object { "    - ext__shutu-knowledge__$_" })
$toolBlock = $toolLines -join [Environment]::NewLine

$config = @"
# Temporary Agent integration profile generated by Shutu Knowledge.
# It is deleted when this launcher exits and never modifies the Agent project.
model: deepseek-v4-flash
data_dir: "$agentDataYaml"
prompts_dir: "$promptsYaml"
tools:
  enabled:
$toolBlock
web_server:
  enabled: true
  addr: 127.0.0.1:18099
  token: ""
  dist_dir: "$agentDistYaml"
extensions:
  enabled: true
  startup_timeout_ms: 120000
  health_timeout_ms: 3000
  context_timeout_ms: 5000
  shutdown_timeout_ms: 3000
  global_context_chars: 4000
  max_contribution_chars: 2000
  global_context_tokens: 1000
  max_contribution_tokens: 500
  sources:
    - manifest: "$manifestYaml"
      required: true
      grants:
        - session.id
        - session.turn
        - user.input
"@

New-Item -ItemType Directory -Force -Path $agentData | Out-Null
Set-Content -LiteralPath $tempConfig -Value $config -Encoding UTF8

$oldPath = $env:Path
$oldLocation = Get-Location
$exitCode = 0
$browserWaiter = $null
try {
    $env:Path = "$knowledgeBinDir;$KnowledgeRoot;$oldPath"
    Set-Location $AgentRoot
    Write-Host "Agent integration entry: http://127.0.0.1:18099"
    Write-Host "Knowledge manifest: $manifest"
    if ($OpenBrowser) {
        # Agent initializes extensions before it binds the native Web listener.
        # Opening the URL immediately produces a misleading browser-level
        # ERR_CONNECTION_REFUSED during that normal startup window. Poll from
        # a short-lived background job and open only after HTTP is ready.
        $browserWaiter = Start-Job -ArgumentList "http://127.0.0.1:18099/" -ScriptBlock {
            param($url)
            $ErrorActionPreference = "SilentlyContinue"
            for ($attempt = 0; $attempt -lt 240; $attempt++) {
                try {
                    $response = Invoke-WebRequest -UseBasicParsing -Uri $url -TimeoutSec 1
                    if ($response.StatusCode -ge 200 -and $response.StatusCode -lt 500) {
                        Start-Process $url
                        return
                    }
                } catch {
                    # The listener is expected to be absent while Agent starts.
                }
                Start-Sleep -Milliseconds 250
            }
        }
        Write-Host "Waiting for Agent Web to become ready before opening the browser..."
    }
    & $agentExe --web-only --config $tempConfig
    $exitCode = $LASTEXITCODE
} finally {
    if ($null -ne $browserWaiter) {
        Stop-Job -Job $browserWaiter -ErrorAction SilentlyContinue
        Remove-Job -Job $browserWaiter -Force -ErrorAction SilentlyContinue
    }
    $env:Path = $oldPath
    Set-Location $oldLocation
    Remove-Item -LiteralPath $tempConfig -Force -ErrorAction SilentlyContinue
    Remove-Item -LiteralPath $agentData -Recurse -Force -ErrorAction SilentlyContinue
}
exit $exitCode
