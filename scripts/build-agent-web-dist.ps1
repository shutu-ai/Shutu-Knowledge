[CmdletBinding()]
param(
    [string] $AgentRoot = "",
    [string] $KnowledgeRoot = ""
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

$agentWebRoot = Join-Path $AgentRoot "web"
$vite = Join-Path $agentWebRoot "node_modules\vite\bin\vite.js"
$config = Join-Path $agentWebRoot "vite.config.ts"
$navigationBridge = Join-Path $KnowledgeRoot "agent-web\extension-navigation.js"
$knowledgeLogo = Join-Path $KnowledgeRoot "web\src\new-logo-b.png"
$output = Join-Path $KnowledgeRoot "agent-web-dist"

foreach ($required in @(
    @{ Path = $agentWebRoot; Label = "Agent Web source" },
    @{ Path = $vite; Label = "Agent Web Vite" },
    @{ Path = $config; Label = "Agent Native Web config" },
    @{ Path = $navigationBridge; Label = "Knowledge-owned extension navigation bridge" },
    @{ Path = $knowledgeLogo; Label = "Knowledge-owned optimized Agent logo" }
)) {
    if (-not (Test-Path -LiteralPath $required.Path)) {
        throw "$($required.Label) not found: $($required.Path)"
    }
}

$oldNative = $env:SHUTU_UI_NATIVE
$env:SHUTU_UI_NATIVE = '1'
try {
    Push-Location $agentWebRoot
    try {
        & node $vite build --config $config --outDir $output --emptyOutDir
        if ($LASTEXITCODE -ne 0) {
            throw "Dedicated Agent Web build failed"
        }
    } finally {
        Pop-Location
    }
} finally {
    $env:SHUTU_UI_NATIVE = $oldNative
}

$index = Join-Path $output "index.html"
if (-not (Test-Path -LiteralPath $index -PathType Leaf)) {
    throw "Dedicated Agent Web index was not produced: $index"
}

# Keep both Knowledge entry points on the same lightweight brand asset. This
# overlays only the Knowledge-owned generated dist; the Agent checkout stays
# strictly read-only and retains its original public asset.
Copy-Item -LiteralPath $knowledgeLogo -Destination (Join-Path $output "new-logo-b.png") -Force

# The native shell does not own Knowledge-specific navigation. Inject this
# Knowledge-owned bridge into the generated copy so the sibling Agent source
# remains strictly read-only.
$bridgeTarget = Join-Path $output "shutu-knowledge-extension-navigation.js"
Copy-Item -LiteralPath $navigationBridge -Destination $bridgeTarget -Force
$indexHtml = [IO.File]::ReadAllText($index)
$bridgeTag = '<script defer src="/shutu-knowledge-extension-navigation.js"></script>'
if (-not $indexHtml.Contains($bridgeTag)) {
    if (-not $indexHtml.Contains('</body>')) {
        throw "Dedicated Agent Web index has no body close tag: $index"
    }
    $indexHtml = $indexHtml.Replace('</body>', "    $bridgeTag`r`n  </body>")
    [IO.File]::WriteAllText($index, $indexHtml, [Text.UTF8Encoding]::new($false))
}

$info = [ordered]@{
    builtAt = [DateTime]::UtcNow.ToString("o")
    owner = "shutu-knowledge"
    source = $AgentRoot
    entry = "Agent web/src/main.tsx (native-entry.ts)"
    purpose = "Knowledge-owned copy of the current Agent Native Web with extension navigation bridge"
}
$info | ConvertTo-Json | Set-Content -LiteralPath (Join-Path $output "build-info.json") -Encoding UTF8
Write-Host "Dedicated Agent Web dist: $output"
