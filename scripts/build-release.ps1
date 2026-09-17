param(
    [Parameter(Mandatory = $true)]
    [ValidatePattern('^v[0-9]+\.[0-9]+\.[0-9]+$')]
    [string]$Version
)
$ErrorActionPreference = 'Stop'
$repoDir = Split-Path $PSScriptRoot -Parent
Push-Location $repoDir
$previousCGO = $env:CGO_ENABLED
$previousOS = $env:GOOS
$previousArch = $env:GOARCH
try {
    $env:CGO_ENABLED = '0'
    $env:GOOS = 'windows'
    $env:GOARCH = 'amd64'
    $outDir = Join-Path $repoDir "dist/$Version"
    New-Item -ItemType Directory -Force -Path $outDir | Out-Null
    $exe = Join-Path $outDir 'wallettools-windows-amd64.exe'
    go build -trimpath -buildvcs=false -ldflags "-s -w -X main.version=$Version" -o $exe ./cmd/wallettools
    if ($LASTEXITCODE -ne 0) { throw 'Go build failed' }
    $zip = Join-Path $outDir 'wallettools-windows-amd64.zip'
    $archiveFiles = @($exe, (Join-Path $repoDir 'README.md'), (Join-Path $repoDir 'LICENSE'), (Join-Path $repoDir 'THIRD_PARTY_NOTICES.md'))
    Compress-Archive -LiteralPath $archiveFiles -DestinationPath $zip -Force
    $checksums = foreach ($file in @($exe, $zip)) {
        $hash = (Get-FileHash -LiteralPath $file -Algorithm SHA256).Hash.ToLowerInvariant()
        "$hash  $(Split-Path $file -Leaf)"
    }
    $checksums | Set-Content -LiteralPath (Join-Path $outDir 'checksums.txt') -Encoding ascii
    Write-Output "Release files: $outDir"
} finally {
    $env:CGO_ENABLED = $previousCGO
    $env:GOOS = $previousOS
    $env:GOARCH = $previousArch
    Pop-Location
}
