param(
    [Parameter(Mandatory = $true)]
    [string] $PackageRoot
)

$ErrorActionPreference = "Stop"
$PackageRoot = [IO.Path]::GetFullPath($PackageRoot)
if (-not (Test-Path -LiteralPath $PackageRoot -PathType Container)) {
    throw "package root does not exist: $PackageRoot"
}

$textFiles = @(Get-ChildItem -LiteralPath $PackageRoot -File -Recurse |
    Where-Object { $_.Extension.ToLowerInvariant() -in @('.md', '.yaml', '.yml', '.json', '.txt') })

$findings = [System.Collections.Generic.List[string]]::new()
$structuredCredentialPattern = '^(?:apiKey|api_key|access_token|client_secret|password|secret|token)$'
$emptyCredentialPattern = '^(?:""|''''|null|none|<set>|<redacted>|\$\{[A-Za-z_][A-Za-z0-9_]*\}|\$env:[A-Za-z_][A-Za-z0-9_]*)$'
$tokenPatterns = @(
    '-----BEGIN [A-Z ]*PRIVATE KEY-----',
    '(?i)\b(?:sk|rk)-[A-Za-z0-9]{20,}\b',
    '(?i)\bgh[pousr]_[A-Za-z0-9]{20,}\b',
    '(?i)\bgithub_pat_[A-Za-z0-9_]{20,}\b',
    '(?i)\bglpat-[A-Za-z0-9_-]{20,}\b',
    '\bAKIA[0-9A-Z]{16}\b',
    '(?i)\bxox[baprs]-[A-Za-z0-9-]{20,}\b'
)

foreach ($file in $textFiles) {
    $text = Get-Content -LiteralPath $file.FullName -Raw
    foreach ($pattern in $tokenPatterns) {
        if ($text -match $pattern) {
            $relative = $file.FullName.Substring($PackageRoot.Length).TrimStart('\', '/')
            $findings.Add("$relative matches high-confidence secret pattern")
            break
        }
    }

    foreach ($line in ($text -split "`r?`n")) {
        if ($line -notmatch '^\s*"?(?<key>apiKey|api_key|access_token|client_secret|password|secret|token)"?\s*:\s*(?<value>.*?)(?:\s*[,#].*)?\s*$') {
            continue
        }
        $value = $Matches['value'].Trim()
        if ($value.Length -ge 2 -and (($value.StartsWith('"') -and $value.EndsWith('"')) -or ($value.StartsWith("'") -and $value.EndsWith("'")))) {
            $value = $value.Substring(1, $value.Length - 2).Trim()
        }
        if ($value -and $value -notmatch $emptyCredentialPattern) {
            $relative = $file.FullName.Substring($PackageRoot.Length).TrimStart('\', '/')
            $findings.Add("$relative contains non-empty credential field $($Matches['key'])")
        }
    }
}

if ($findings.Count -gt 0) {
    throw "static release-package secret audit failed: $($findings -join '; ')"
}

Write-Output "static release-package secret audit: passed ($($textFiles.Count) text files scanned)"
