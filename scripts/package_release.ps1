param(
    [string] $RepoRoot = (Split-Path -Parent $PSScriptRoot),
    [string] $OutputRoot = "",
    [ValidateSet("windows-amd64", "linux-amd64")]
    [string] $Platform = "windows-amd64"
)

$ErrorActionPreference = "Stop"

if (-not $OutputRoot) {
    $OutputRoot = Join-Path $RepoRoot ".tmp\formal-release"
}

$versionSource = Get-Content -LiteralPath (Join-Path $RepoRoot "internal\version\version.go") -Raw
$versionMatch = [regex]::Match($versionSource, 'Version\s*=\s*"([^"]+)"')
if (-not $versionMatch.Success) {
    throw "could not determine Knowledge version"
}
$version = $versionMatch.Groups[1].Value
$commit = (& git -C $RepoRoot rev-parse HEAD).Trim()
if ($LASTEXITCODE -ne 0 -or $commit -notmatch '^[0-9a-f]{40}$') {
    throw "could not determine candidate commit"
}
$dirtyFiles = @(& git -C $RepoRoot status --porcelain --untracked-files=no)
if ($dirtyFiles.Count -gt 0) {
    throw "formal packaging requires a clean candidate checkout; uncommitted files: $($dirtyFiles -join '; ')"
}

$goos = "windows"
$goarch = "amd64"
$binaryName = "shutu-knowledge.exe"
if ($Platform -eq "linux-amd64") {
    $goos = "linux"
    $binaryName = "shutu-knowledge"
}

$packageName = "shutu-knowledge-$version-$Platform"
$stage = Join-Path $OutputRoot $packageName
$archive = Join-Path $OutputRoot "$packageName.zip"
$verifyRoot = Join-Path $OutputRoot ".verify-$Platform"

New-Item -ItemType Directory -Force -Path $OutputRoot | Out-Null
if (Test-Path -LiteralPath $stage) { Remove-Item -LiteralPath $stage -Recurse -Force }
if (Test-Path -LiteralPath $archive) { Remove-Item -LiteralPath $archive -Force }
if (Test-Path -LiteralPath $verifyRoot) { Remove-Item -LiteralPath $verifyRoot -Recurse -Force }
New-Item -ItemType Directory -Force -Path (Join-Path $stage "bin") | Out-Null

$binaryPath = Join-Path $stage "bin\$binaryName"
$previousGOOS = $env:GOOS
$previousGOARCH = $env:GOARCH
try {
    $env:GOOS = $goos
    $env:GOARCH = $goarch
    & go build -trimpath -o $binaryPath (Join-Path $RepoRoot "./cmd/shutu-knowledge")
    if ($LASTEXITCODE -ne 0) { throw "Knowledge binary build failed" }
} finally {
    if ($null -eq $previousGOOS) { Remove-Item Env:GOOS -ErrorAction SilentlyContinue } else { $env:GOOS = $previousGOOS }
    if ($null -eq $previousGOARCH) { Remove-Item Env:GOARCH -ErrorAction SilentlyContinue } else { $env:GOARCH = $previousGOARCH }
}

foreach ($fileMapping in @(
    @{ Source = "LICENSE"; Target = "LICENSE" },
    @{ Source = "README.md"; Target = "README.md" },
    @{ Source = "THIRD_PARTY_NOTICES.md"; Target = "THIRD_PARTY_NOTICES.md" },
    @{ Source = "extension.yaml"; Target = "extension.yaml" },
    @{ Source = "docs\deployment.md"; Target = "docs\deployment.md" },
    @{ Source = "docs\runtime_dependencies.md"; Target = "docs\runtime_dependencies.md" },
    @{ Source = "docs\runtime_license_inventory.md"; Target = "docs\runtime_license_inventory.md" },
    @{ Source = "runtime_implementation_report.md"; Target = "runtime_implementation_report.md" },
    @{ Source = "docs\out_of_box_parity_matrix.md"; Target = "docs\out_of_box_parity_matrix.md" },
    @{ Source = "internal\runtime\assets\runtime-manifest.json"; Target = "runtime-manifest.json" }
)) {
    $sourcePath = Join-Path $RepoRoot $fileMapping.Source
    if (-not (Test-Path -LiteralPath $sourcePath -PathType Leaf)) {
        throw "required release file is missing: $($fileMapping.Source)"
    }
    $targetPath = Join-Path $stage $fileMapping.Target
    New-Item -ItemType Directory -Force -Path (Split-Path -Parent $targetPath) | Out-Null
    Copy-Item -LiteralPath $sourcePath -Destination $targetPath
}

$binaryVersion = (& $binaryPath version).Trim()
if ($LASTEXITCODE -ne 0 -or $binaryVersion -ne $version) {
    throw "packaged binary version mismatch: got '$binaryVersion', want '$version'"
}

$binaryHash = (Get-FileHash -LiteralPath $binaryPath -Algorithm SHA256).Hash.ToLowerInvariant()
$metadata = [ordered]@{
    schema = 1
    product = "shutu-knowledge"
    version = $version
    platform = $Platform
    git_sha = $commit
    packaging = "formal-release-zip"
    runtime = "embedded managed runtime assets; Node/model/OCR artifacts are auto-managed in the Knowledge data home"
    binary_sha256 = $binaryHash
}
$metadata | ConvertTo-Json -Depth 5 | Set-Content -LiteralPath (Join-Path $stage "BUILD-METADATA.json") -Encoding UTF8

$checksumLines = foreach ($file in @(Get-ChildItem -LiteralPath $stage -File -Recurse | Sort-Object FullName)) {
    $relative = [IO.Path]::GetRelativePath($stage, $file.FullName).Replace("\", "/")
    $hash = (Get-FileHash -LiteralPath $file.FullName -Algorithm SHA256).Hash.ToLowerInvariant()
    "$hash  $relative"
}
$checksumLines | Set-Content -LiteralPath (Join-Path $stage "checksums.sha256") -Encoding ASCII

Compress-Archive -Path (Join-Path $stage "*") -DestinationPath $archive -CompressionLevel Optimal
Expand-Archive -LiteralPath $archive -DestinationPath $verifyRoot -Force
$verifiedRoot = Join-Path $verifyRoot $packageName
if (-not (Test-Path -LiteralPath $verifiedRoot)) {
    # Compress-Archive with a wildcard creates files at the archive root.
    $verifiedRoot = $verifyRoot
}
foreach ($required in @("bin\$binaryName", "runtime-manifest.json", "LICENSE", "THIRD_PARTY_NOTICES.md", "checksums.sha256", "BUILD-METADATA.json")) {
    if (-not (Test-Path -LiteralPath (Join-Path $verifiedRoot $required) -PathType Leaf)) {
        throw "formal package verification missing $required"
    }
}
$forbidden = @(Get-ChildItem -LiteralPath $verifiedRoot -Recurse -Force -ErrorAction SilentlyContinue |
    Where-Object { $_.FullName -match '(node_modules|\.git|model-cache|runtime-cache|\.npm-cache|\.tmp-home)' })
if ($forbidden.Count -gt 0) {
    throw "formal package contains forbidden development artifacts: $($forbidden.FullName -join ', ')"
}
$textFiles = @(Get-ChildItem -LiteralPath $verifiedRoot -File -Recurse |
    Where-Object { $_.Extension -in @('.md', '.yaml', '.json', '.txt') })
foreach ($textFile in $textFiles) {
    $text = Get-Content -LiteralPath $textFile.FullName -Raw
    if ($text -match 'C:\\Users\\|C:\\dev-projects\\|/home/[^/]+/|/Users/[^/]+/') {
        throw "formal package contains a developer-specific absolute path: $($textFile.FullName)"
    }
}

$archiveHash = (Get-FileHash -LiteralPath $archive -Algorithm SHA256).Hash.ToLowerInvariant()
[pscustomobject]@{
    Package = $archive
    SizeBytes = (Get-Item -LiteralPath $archive).Length
    SHA256 = $archiveHash
    GitSHA = $commit
    Binary = $binaryName
    BinarySHA256 = $binaryHash
    Inventory = (Get-ChildItem -LiteralPath $verifiedRoot -File -Recurse | ForEach-Object { [IO.Path]::GetRelativePath($verifiedRoot, $_.FullName).Replace("\", "/") }) -join ","
} | Format-List
