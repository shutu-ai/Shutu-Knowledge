param(
    [Parameter(Mandatory = $true)]
    [string] $PackageZip,
    [Parameter(Mandatory = $true)]
    [string] $OCRImage,
    [string] $Doc = "",
    [string] $Ppt = "",
    [string] $Xls = "",
    [string] $CodecPDF = "",
    [string] $WorkRoot = ""
)

$ErrorActionPreference = "Stop"

function Invoke-JsonRequest([string] $Method, [string] $Uri, $Body = $null) {
    if ($null -eq $Body) {
        return Invoke-RestMethod -Method $Method -Uri $Uri -TimeoutSec 900
    }
    $json = $Body | ConvertTo-Json -Depth 30 -Compress
    return Invoke-RestMethod -Method $Method -Uri $Uri -ContentType "application/json" -Body $json -TimeoutSec 900
}

function Stop-PackageServer($Process) {
    if ($null -eq $Process) { return }
    if (-not $Process.HasExited) {
        & taskkill.exe /PID $Process.Id /T /F | Out-Null
        $Process.WaitForExit(10000) | Out-Null
    }
    $Process.Dispose()
}

if (-not (Test-Path -LiteralPath $PackageZip -PathType Leaf)) { throw "package not found: $PackageZip" }
if (-not (Test-Path -LiteralPath $OCRImage -PathType Leaf)) { throw "OCR fixture not found: $OCRImage" }

if (-not $WorkRoot) {
    $WorkRoot = Join-Path ([IO.Path]::GetTempPath()) ("shutu-package-smoke-" + [guid]::NewGuid().ToString("N"))
}
$packageRoot = Join-Path $WorkRoot "package"
$dataHome = Join-Path $WorkRoot "data"
$modelCache = Join-Path $dataHome "models"
New-Item -ItemType Directory -Force -Path $packageRoot, $dataHome | Out-Null
Expand-Archive -LiteralPath $PackageZip -DestinationPath $packageRoot -Force

$binary = Join-Path $packageRoot "bin\shutu-knowledge.exe"
if (-not (Test-Path -LiteralPath $binary -PathType Leaf)) { throw "package binary missing: $binary" }
foreach ($required in @("LICENSE", "THIRD_PARTY_NOTICES.md", "runtime-manifest.json", "checksums.sha256")) {
    if (-not (Test-Path -LiteralPath (Join-Path $packageRoot $required) -PathType Leaf)) {
        throw "package file missing: $required"
    }
}
if (@(Get-ChildItem -LiteralPath $packageRoot -Recurse -Force |
        Where-Object { $_.FullName -match '(node_modules|\.git|model-cache|runtime-cache|\.npm-cache)' }).Count -gt 0) {
    throw "package contains a development/runtime cache"
}

$listener = [Net.Sockets.TcpListener]::new([Net.IPAddress]::Loopback, 0)
$listener.Start()
$port = $listener.LocalEndpoint.Port
$listener.Stop()
$configPath = Join-Path $dataHome "config.yaml"
@"
server:
  addr: 127.0.0.1:$port
embedding:
  provider: local
  model: onnx-community/Qwen3-Embedding-0.6B-ONNX
  batch: 4
rerank:
  enabled: true
  model: local:Xenova/bge-reranker-base
retrieval:
  mode: vector
  topK: 10
models:
  cacheDir: $($modelCache -replace '\\', '/')
ocr:
  mode: auto
runtime:
  offline: false
"@ | Set-Content -LiteralPath $configPath -Encoding UTF8

$baseURL = "http://127.0.0.1:$port"
$server = $null
try {
    $startInfo = [Diagnostics.ProcessStartInfo]::new()
    $startInfo.FileName = $binary
    $startInfo.Arguments = "serve"
    $startInfo.WorkingDirectory = $packageRoot
    $startInfo.UseShellExecute = $false
    $startInfo.CreateNoWindow = $true
    $startInfo.RedirectStandardOutput = $true
    $startInfo.RedirectStandardError = $true
    $startInfo.EnvironmentVariables["SHUTU_KNOWLEDGE_HOME"] = $dataHome
    $server = [Diagnostics.Process]::new()
    $server.StartInfo = $startInfo
    [void]$server.Start()

    $health = $null
    for ($attempt = 0; $attempt -lt 120; $attempt++) {
        if ($server.HasExited) {
            throw "packaged server exited during startup: $($server.ExitCode)"
        }
        try {
            $health = Invoke-JsonRequest "GET" "$baseURL/api/status"
            if ($health.ready) { break }
        } catch { }
        Start-Sleep -Milliseconds 500
    }
    if ($null -eq $health -or -not $health.ready) { throw "packaged server did not become healthy" }

    $baseResponse = Invoke-JsonRequest "POST" "$baseURL/api/bases" @{ name = "formal-package-smoke"; description = "package-only runtime validation"; group = "runtime"; config = @{} }
    $baseID = $baseResponse.value.id
    if ([string]::IsNullOrWhiteSpace($baseID)) { throw "package API did not create a base" }

    $textResponse = Invoke-JsonRequest "POST" "$baseURL/api/bases/$baseID/documents" @{ title = "expense-reimbursement.md"; content = "An expense report requires the original invoice and manager approval before reimbursement." }
    if ($textResponse.value.status -ne "ready") { throw "package text import was not ready: $($textResponse.value.status)" }

    $fixtures = @(@{ Path = $OCRImage; Kind = "ocr" })
    foreach ($candidate in @(@{ Path = $Doc; Kind = "doc" }, @{ Path = $Ppt; Kind = "ppt" }, @{ Path = $Xls; Kind = "xls" }, @{ Path = $CodecPDF; Kind = "codec-pdf" })) {
        if (-not [string]::IsNullOrWhiteSpace($candidate.Path)) { $fixtures += $candidate }
    }
    $imported = @()
    foreach ($fixture in $fixtures) {
        if (-not (Test-Path -LiteralPath $fixture.Path -PathType Leaf)) { throw "fixture not found: $($fixture.Path)" }
        $data = [Convert]::ToBase64String([IO.File]::ReadAllBytes($fixture.Path))
        $response = Invoke-JsonRequest "POST" "$baseURL/api/bases/$baseID/files" @{ conflict = "rename"; files = @(@{ fileName = [IO.Path]::GetFileName($fixture.Path); contentBase64 = $data }) }
        $accepted = @($response.value.accepted)
        if ($accepted.Count -ne 1) { throw "package $($fixture.Kind) import was not accepted" }
        $document = Invoke-JsonRequest "GET" "$baseURL/api/documents/$($accepted[0].id)?includeChunks=false"
        if ($document.value.status -ne "ready" -or $document.value.chunkCount -lt 1) {
            throw "package $($fixture.Kind) import was not ready: status=$($document.value.status) chunks=$($document.value.chunkCount)"
        }
        $imported += $accepted[0].id
    }

    $semantic = Invoke-JsonRequest "POST" "$baseURL/api/search" @{ query = "What invoice and approval are needed for expense reimbursement?"; mode = "vector"; topK = 3; baseIds = @($baseID) }
    if (@($semantic.value.hits).Count -lt 1 -or $semantic.value.hits[0].documentTitle -ne "expense-reimbursement.md") {
        throw "package semantic retrieval returned the wrong result"
    }
    $ocrSearch = Invoke-JsonRequest "POST" "$baseURL/api/search" @{ query = "Knowledge Runtime OCR 7788"; mode = "vector"; topK = 10; baseIds = @($baseID) }
    if (@($ocrSearch.value.hits).Count -lt 1) { throw "package OCR vector retrieval returned no hits" }

    $status = Invoke-JsonRequest "GET" "$baseURL/api/runtime-status"
    $statusText = ($status.value | ConvertTo-Json -Depth 20 -Compress)
    if ($statusText -notmatch '"ready"\s*:\s*true') { throw "package runtime status did not expose a ready capability" }
    Stop-PackageServer $server
    $server = $null

    (Get-Content -LiteralPath $configPath -Raw) -replace 'offline: false', 'offline: true' | Set-Content -LiteralPath $configPath -Encoding UTF8
    $startInfo = [Diagnostics.ProcessStartInfo]::new()
    $startInfo.FileName = $binary
    $startInfo.Arguments = "serve"
    $startInfo.WorkingDirectory = $packageRoot
    $startInfo.UseShellExecute = $false
    $startInfo.CreateNoWindow = $true
    $startInfo.RedirectStandardOutput = $true
    $startInfo.RedirectStandardError = $true
    $startInfo.EnvironmentVariables["SHUTU_KNOWLEDGE_HOME"] = $dataHome
    $startInfo.EnvironmentVariables["SHUTU_KNOWLEDGE_OFFLINE"] = "1"
    $server = [Diagnostics.Process]::new()
    $server.StartInfo = $startInfo
    [void]$server.Start()
    for ($attempt = 0; $attempt -lt 120; $attempt++) {
        if ($server.HasExited) { throw "packaged offline server exited: $($server.ExitCode)" }
        try {
            $offlineHealth = Invoke-JsonRequest "GET" "$baseURL/api/status"
            if ($offlineHealth.ready) { break }
        } catch { }
        Start-Sleep -Milliseconds 500
    }
    $offlineSearch = Invoke-JsonRequest "POST" "$baseURL/api/search" @{ query = "What invoice and approval are needed for expense reimbursement?"; mode = "vector"; topK = 3; baseIds = @($baseID) }
    if (@($offlineSearch.value.hits).Count -lt 1 -or $offlineSearch.value.hits[0].documentTitle -ne "expense-reimbursement.md") {
        throw "package offline restart retrieval failed"
    }
    $packageHash = (Get-FileHash -LiteralPath $PackageZip -Algorithm SHA256).Hash.ToLowerInvariant()
    [pscustomobject]@{
        Result = "PASS"
        Package = (Resolve-Path -LiteralPath $PackageZip).Path
        PackageSHA256 = $packageHash
        PackageSizeBytes = (Get-Item -LiteralPath $PackageZip).Length
        ImportedFixtures = $fixtures.Count
        OfflineRestart = "PASS"
        DataHome = $dataHome
    } | Format-List
} finally {
    Stop-PackageServer $server
}
