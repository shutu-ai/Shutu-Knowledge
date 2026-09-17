param(
    [Parameter(Mandatory = $true)]
    [string] $PackageZip,
    [Parameter(Mandatory = $true)]
    [string] $OCRImage,
    [string] $ModelCache = "",
    [string] $Doc = "",
    [string] $Ppt = "",
    [string] $Xls = "",
    [string] $CodecPDF = "",
    [string] $WorkRoot = ""
)

$ErrorActionPreference = "Stop"

$PackageZip = [IO.Path]::GetFullPath($PackageZip)
$OCRImage = [IO.Path]::GetFullPath($OCRImage)
if ($ModelCache) { $ModelCache = [IO.Path]::GetFullPath($ModelCache) }
if ($Doc) { $Doc = [IO.Path]::GetFullPath($Doc) }
if ($Ppt) { $Ppt = [IO.Path]::GetFullPath($Ppt) }
if ($Xls) { $Xls = [IO.Path]::GetFullPath($Xls) }
if ($CodecPDF) { $CodecPDF = [IO.Path]::GetFullPath($CodecPDF) }

function Invoke-JsonRequest([string] $Method, [string] $Uri, $Body = $null) {
    if ($null -eq $Body) {
        return Invoke-RestMethod -Method $Method -Uri $Uri -TimeoutSec 900
    }
    $json = $Body | ConvertTo-Json -Depth 30 -Compress
    return Invoke-RestMethod -Method $Method -Uri $Uri -ContentType "application/json" -Body $json -TimeoutSec 900
}

function Wait-Operation([string] $BaseURL, [string] $OperationID) {
    if ([string]::IsNullOrWhiteSpace($OperationID)) { throw "operation id is empty" }
    for ($attempt = 0; $attempt -lt 1800; $attempt++) {
        try {
            $operation = (Invoke-JsonRequest "GET" "$BaseURL/api/operations/$OperationID").value
        } catch {
            Start-Sleep -Milliseconds 500
            continue
        }
        $state = [string]$operation.state
        if ($state -in @("succeeded", "failed", "cancelled")) {
            if ($state -ne "succeeded") {
                throw "operation $OperationID ended in ${state}: $($operation.errorMessage)"
            }
            return $operation
        }
        Start-Sleep -Milliseconds 500
    }
    throw "operation $OperationID did not finish"
}

function Wait-DocumentReady([string] $BaseURL, [string] $DocumentID) {
    if ([string]::IsNullOrWhiteSpace($DocumentID)) { throw "document id is empty" }
    for ($attempt = 0; $attempt -lt 1800; $attempt++) {
        try {
            $document = (Invoke-JsonRequest "GET" "$BaseURL/api/documents/$DocumentID?includeChunks=false").value
        } catch {
            Start-Sleep -Milliseconds 500
            continue
        }
        if ([string]$document.status -eq "ready") {
            if ([int]$document.chunkCount -lt 1) { throw "document $DocumentID became ready without chunks" }
            return $document
        }
        if ([string]$document.status -in @("failed", "error")) {
            throw "document $DocumentID ended in $($document.status): $($document.errorMessage)"
        }
        Start-Sleep -Milliseconds 500
    }
    throw "document $DocumentID did not become ready"
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
$WorkRoot = [IO.Path]::GetFullPath($WorkRoot)
$packageRoot = Join-Path $WorkRoot "package"
$dataHome = Join-Path $WorkRoot "data"
$modelCache = if ($ModelCache) { $ModelCache } else { Join-Path $dataHome "models" }
New-Item -ItemType Directory -Force -Path $packageRoot, $dataHome | Out-Null
if ($ModelCache) {
    if (-not (Test-Path -LiteralPath $modelCache -PathType Container)) { throw "model cache not found: $modelCache" }
} else {
    New-Item -ItemType Directory -Force -Path $modelCache | Out-Null
}
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
  enabled: false
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
    Write-Host "package smoke: initial packaged server ready"

    $baseResponse = Invoke-JsonRequest "POST" "$baseURL/api/bases" @{ name = "formal-package-smoke"; description = "package-only runtime validation"; group = "runtime"; config = @{} }
    $baseID = $baseResponse.value.id
    if ([string]::IsNullOrWhiteSpace($baseID)) { throw "package API did not create a base" }

    $textResponse = Invoke-JsonRequest "POST" "$baseURL/api/bases/$baseID/documents" @{ title = "expense-reimbursement.md"; content = "An expense report requires the original invoice and manager approval before reimbursement." }
    Write-Host "package smoke: text import accepted ($($textResponse.value.operationId))"
    $textOperation = Wait-Operation $baseURL ([string]$textResponse.value.operationId)
    $textResult = @($textOperation.result.documents)
    if ($textResult.Count -ne 1 -or $textResult[0].status -ne "ready" -or [int]$textResult[0].chunkCount -lt 1) {
        throw "package text import did not return a ready document result"
    }
    Write-Host "package smoke: text import ready"

    $fixtures = @(@{ Path = $OCRImage; Kind = "ocr" })
    foreach ($candidate in @(@{ Path = $Doc; Kind = "doc" }, @{ Path = $Ppt; Kind = "ppt" }, @{ Path = $Xls; Kind = "xls" })) {
        if (-not [string]::IsNullOrWhiteSpace($candidate.Path)) { $fixtures += $candidate }
    }
    $codecFixturePresent = "not-provided"
    if (-not [string]::IsNullOrWhiteSpace($CodecPDF)) {
        if (-not (Test-Path -LiteralPath $CodecPDF -PathType Leaf)) { throw "codec PDF fixture not found: $CodecPDF" }
        # A codec-only page intentionally has no text layer. Its full-page
        # PDF.js decode is covered by the direct managed-runtime smoke; the
        # package API import path must retain the parser's visible failure.
        $codecFixturePresent = "provided; direct PDF.js fixture gate is recorded separately"
    }
    $imported = @()
    foreach ($fixture in $fixtures) {
        if (-not (Test-Path -LiteralPath $fixture.Path -PathType Leaf)) { throw "fixture not found: $($fixture.Path)" }
        $data = [Convert]::ToBase64String([IO.File]::ReadAllBytes($fixture.Path))
        $response = Invoke-JsonRequest "POST" "$baseURL/api/bases/$baseID/files" @{ conflict = "rename"; files = @(@{ fileName = [IO.Path]::GetFileName($fixture.Path); contentBase64 = $data }) }
        Write-Host "package smoke: $($fixture.Kind) import accepted ($($response.value.operationId))"
        Wait-Operation $baseURL ([string]$response.value.operationId) | Out-Null
        $documents = @((Invoke-JsonRequest "GET" "$baseURL/api/bases/$baseID/documents").value)
        $document = $documents | Where-Object { $_.fileName -eq [IO.Path]::GetFileName($fixture.Path) -or $_.title -eq [IO.Path]::GetFileName($fixture.Path) } | Select-Object -First 1
        if ($null -eq $document) { throw "package $($fixture.Kind) import did not create a document" }
        if ([string]$document.status -ne "ready" -or [int]$document.chunkCount -lt 1) {
            throw "package $($fixture.Kind) import was not ready: status=$($document.status) chunks=$($document.chunkCount)"
        }
        $imported += $document.id
        Write-Host "package smoke: $($fixture.Kind) import ready"
    }

    $packageStats = Invoke-JsonRequest "GET" "$baseURL/api/bases/$baseID/stats"
    if (-not $packageStats.value.embedded -or $packageStats.value.embeddingDimensions -lt 1) {
        throw "package imports did not materialize embedding vectors: $($packageStats.value | ConvertTo-Json -Compress)"
    }
    $semantic = Invoke-JsonRequest "POST" "$baseURL/api/search" @{ query = "What invoice and approval are needed for expense reimbursement?"; mode = "vector"; topK = 3; baseIds = @($baseID) }
    if (@($semantic.value.hits).Count -lt 1 -or $semantic.value.hits[0].documentTitle -ne "expense-reimbursement.md") {
        throw "package semantic retrieval returned the wrong result"
    }
    $ocrSearch = Invoke-JsonRequest "POST" "$baseURL/api/search" @{ query = "Recall Test"; mode = "vector"; topK = 10; baseIds = @($baseID) }
    if (@($ocrSearch.value.hits).Count -lt 1) { throw "package OCR vector retrieval returned no hits" }

    $status = Invoke-JsonRequest "GET" "$baseURL/api/runtime-status"
    $statusText = ($status.value | ConvertTo-Json -Depth 20 -Compress)
    if ($statusText -notmatch '"ready"\s*:\s*true') { throw "package runtime status did not expose a ready capability" }
    Write-Host "package smoke: online retrieval and runtime status passed"
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
    Write-Host "package smoke: offline restart retrieval passed"
    $packageHash = (Get-FileHash -LiteralPath $PackageZip -Algorithm SHA256).Hash.ToLowerInvariant()
    [pscustomobject]@{
        Result = "PASS"
        Package = (Resolve-Path -LiteralPath $PackageZip).Path
        PackageSHA256 = $packageHash
        PackageSizeBytes = (Get-Item -LiteralPath $PackageZip).Length
        ImportedFixtures = $fixtures.Count
        CodecFixture = $codecFixturePresent
        OfflineRestart = "PASS"
        DataHome = $dataHome
    } | Format-List
} finally {
    Stop-PackageServer $server
}
