param(
    [ValidateSet('sm_75','sm_80','sm_86','sm_87','sm_88','sm_89','sm_90','sm_100','sm_103','sm_110','sm_120','sm_121','all')]
    [string]$Arch = 'all',
    [string]$Output = 'internal/cudagen/assets/wallettools-cuda.exe'
)
$ErrorActionPreference = 'Stop'
$repoDir = Split-Path $PSScriptRoot -Parent
$sourceDir = Join-Path $repoDir 'third_party/provanity'
$backendScript = Join-Path $sourceDir 'scripts/build-cuda-backend.ps1'
$target = if ([IO.Path]::IsPathRooted($Output)) { $Output } else { Join-Path $repoDir $Output }
if (-not (Test-Path -LiteralPath $backendScript)) {
    throw "Vendored ProVanity build script was not found: $backendScript"
}

# vcvars64.bat fails when the inherited PATH exceeds cmd.exe's legacy limit.
$previousPath = $env:Path
try {
    $env:Path = "$env:SystemRoot\System32;$env:SystemRoot;$env:SystemRoot\System32\Wbem"
    & $backendScript -Arch $Arch
    if ($LASTEXITCODE -ne 0) { throw 'CUDA backend build failed' }
} finally {
    $env:Path = $previousPath
}

$previousCache = $env:GOCACHE
try {
    $env:GOCACHE = Join-Path $repoDir '.codex-gocache-cuda-helper'
    New-Item -ItemType Directory -Force -Path (Split-Path $target -Parent) | Out-Null
    Push-Location $sourceDir
    try {
        go build -buildvcs=false -trimpath -tags cudaembed -ldflags '-s -w' -o $target ./cmd/provanity-worker
        if ($LASTEXITCODE -ne 0) { throw 'CUDA worker build failed' }
    } finally {
        Pop-Location
    }
} finally {
    $env:GOCACHE = $previousCache
}
Write-Output "Embedded CUDA helper: $target"
