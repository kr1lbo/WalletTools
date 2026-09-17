param(
    [string]$SourceDir = (Join-Path $PSScriptRoot "..\internal\cuda\native"),
    [string]$BuildDir = (Join-Path $PSScriptRoot "..\.tmp\cuda-backend"),
    [string]$EmbedDir = (Join-Path $PSScriptRoot "..\internal\cuda\assets"),
    [string]$CudaRoot = $env:CUDA_PATH,
    [string]$VcVars = "",
    [string]$Arch = "",
    [string[]]$Archs = @(),
    [string]$PtxArch = "",
    [string]$OutputName = "provanity_cuda_standard.dll",
    [int]$MaxRegisters = 0,
    [switch]$PtxasVerbose,
    [switch]$LineInfo,
    [switch]$PtxasDlcmCa,
    [string]$ExtraNvccFlags = ""
)

$ErrorActionPreference = "Stop"

function Find-LatestCudaRoot {
    param([string]$Major)

    $Base = "C:\Program Files\NVIDIA GPU Computing Toolkit\CUDA"
    if (-not (Test-Path -LiteralPath $Base)) {
        return ""
    }
    $Candidate = Get-ChildItem -LiteralPath $Base -Directory -Filter "v$Major.*" |
        Sort-Object { [version]($_.Name.TrimStart("v")) } -Descending |
        Select-Object -First 1
    if ($null -eq $Candidate) {
        return ""
    }
    return $Candidate.FullName
}

if ([string]::IsNullOrWhiteSpace($CudaRoot)) {
    $CudaRoot = Find-LatestCudaRoot "13"
    if ([string]::IsNullOrWhiteSpace($CudaRoot)) {
        $CudaRoot = "C:\Program Files\NVIDIA GPU Computing Toolkit\CUDA\v13.2"
    }
}

$Nvcc = Join-Path $CudaRoot "bin\nvcc.exe"
$Source = Join-Path $SourceDir "backend.cu"
$Output = Join-Path $BuildDir $OutputName

if (-not (Test-Path -LiteralPath $Source)) {
    throw "Missing CUDA source at $Source"
}
if (-not (Test-Path -LiteralPath $Nvcc)) {
    throw "Missing nvcc at $Nvcc. Install CUDA Toolkit or pass -CudaRoot."
}
if ($MaxRegisters -lt 0) {
    throw "MaxRegisters must be 0 or greater."
}

function Resolve-VcVarsPath {
    param([string]$Requested)

    if (-not [string]::IsNullOrWhiteSpace($Requested)) {
        if (Test-Path -LiteralPath $Requested) {
            return $Requested
        }
        throw "Missing MSVC vcvars64.bat at $Requested. Install Visual Studio Build Tools or pass -VcVars."
    }

    $Candidates = New-Object System.Collections.Generic.List[string]
    $Candidates.Add("C:\Program Files (x86)\Microsoft Visual Studio\2022\BuildTools\VC\Auxiliary\Build\vcvars64.bat")
    $Candidates.Add("C:\Program Files\Microsoft Visual Studio\2022\Enterprise\VC\Auxiliary\Build\vcvars64.bat")
    $Candidates.Add("C:\Program Files\Microsoft Visual Studio\2022\Professional\VC\Auxiliary\Build\vcvars64.bat")
    $Candidates.Add("C:\Program Files\Microsoft Visual Studio\2022\Community\VC\Auxiliary\Build\vcvars64.bat")

    $VsWhere = Join-Path ${env:ProgramFiles(x86)} "Microsoft Visual Studio\Installer\vswhere.exe"
    if (Test-Path -LiteralPath $VsWhere) {
        $Found = & $VsWhere -latest -products * -requires Microsoft.VisualStudio.Component.VC.Tools.x86.x64 -find "VC\Auxiliary\Build\vcvars64.bat" 2>$null
        foreach ($Path in $Found) {
            if (-not [string]::IsNullOrWhiteSpace($Path)) {
                $Candidates.Add($Path)
            }
        }
    }

    foreach ($Candidate in $Candidates) {
        if (Test-Path -LiteralPath $Candidate) {
            return $Candidate
        }
    }
    throw "Missing MSVC vcvars64.bat. Install Visual Studio 2022 Build Tools or pass -VcVars."
}

function Assert-SupportedArch {
    param([string]$Value)
    if ($Value -eq "all") {
        return
    }
    $Unsupported = @("sm_50", "sm_52", "sm_53", "sm_60", "sm_61", "sm_62", "sm_70", "sm_72")
    if ($Value -in $Unsupported) {
        throw "$Value is below provanity's sm_75 minimum. Pascal/Volta and older are not supported."
    }
}

$VcVars = Resolve-VcVarsPath $VcVars
if ($Archs.Count -eq 0) {
    $Archs = @("sm_75", "sm_80", "sm_86", "sm_87", "sm_88", "sm_89", "sm_90", "sm_100", "sm_103", "sm_110", "sm_120", "sm_121")
}
if ([string]::IsNullOrWhiteSpace($PtxArch)) {
    $PtxArch = "compute_120"
}

# Single -Arch overrides the fat-binary list for fast dev iteration on one GPU.
$ArchFlags = ""
if ($Arch -eq "all") {
    $Arch = ""
}
if (-not [string]::IsNullOrWhiteSpace($Arch)) {
    Assert-SupportedArch $Arch
    $ArchFlags = "-arch=$Arch"
} else {
    if ($Archs.Count -eq 0) {
        throw "No GPU arches selected. Pass -Arch for a single arch or -Archs for a fat binary."
    }
    $Parts = New-Object System.Collections.Generic.List[string]
    foreach ($SmArch in $Archs) {
        Assert-SupportedArch $SmArch
        $Compute = $SmArch -replace '^sm_', 'compute_'
        $Parts.Add("-gencode arch=$Compute,code=$SmArch")
    }
    if (-not [string]::IsNullOrWhiteSpace($PtxArch)) {
        $Parts.Add("-gencode arch=$PtxArch,code=$PtxArch")
    }
    $ArchFlags = ($Parts -join ' ')
}

New-Item -ItemType Directory -Force -Path $BuildDir | Out-Null
$RegisterFlag = ""
if ($MaxRegisters -gt 0) {
    $RegisterFlag = " --maxrregcount=$MaxRegisters"
}
$ExtraFlags = New-Object System.Collections.Generic.List[string]
if ($PtxasVerbose) {
    $ExtraFlags.Add("-Xptxas -v")
}
if ($LineInfo) {
    $ExtraFlags.Add("-lineinfo")
}
if ($PtxasDlcmCa) {
    $ExtraFlags.Add("-Xptxas -O3,-dlcm=ca")
}
if (-not [string]::IsNullOrWhiteSpace($ExtraNvccFlags)) {
    $ExtraFlags.Add($ExtraNvccFlags)
}
$ExtraFlagPart = ""
if ($ExtraFlags.Count -gt 0) {
    $ExtraFlagPart = " " + ($ExtraFlags -join " ")
}
$Command = ('"{0}" && "{1}" -std=c++17 -O3 -cudart=static {2}{3}{4} -Xcompiler /MD -shared -o "{5}" "{6}"' -f $VcVars, $Nvcc, $ArchFlags, $RegisterFlag, $ExtraFlagPart, $Output, $Source)
& cmd.exe /d /s /c $Command
if ($LASTEXITCODE -ne 0) {
    exit $LASTEXITCODE
}

New-Item -ItemType Directory -Force -Path $EmbedDir | Out-Null
Copy-Item -LiteralPath $Output -Destination (Join-Path $EmbedDir $OutputName) -Force

Write-Host "Built $Output"
Write-Host "Updated embedded CUDA DLL asset in $EmbedDir"
