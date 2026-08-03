<#
.SYNOPSIS
    Builds client/installer/Output/GoldenCloudSetup.exe with Inno Setup.

.DESCRIPTION
    Expects build.ps1 to have produced client/build/app, and thirdparty.ps1 to
    have produced client/thirdparty/rclone.exe and client/thirdparty/winfsp.msi.
    Both are run automatically if their outputs are missing, so `./package.ps1`
    on a clean checkout does the right thing.

    Inno Setup 6 is pre-installed on the GitHub windows-latest runner at
    "C:\Program Files (x86)\Inno Setup 6\ISCC.exe". If it is not present it is
    downloaded and installed silently.

    The output path is fixed, because CI uploads exactly that file.

.EXAMPLE
    ./package.ps1
#>
[CmdletBinding()]
param(
    [ValidateSet('Debug', 'Release')]
    [string]$Configuration = 'Release',

    [string]$Version = '0.1.0',

    # Where to fetch Inno Setup from if it is not already installed.
    [string]$InnoSetupInstallerUrl = 'https://jrsoftware.org/download.php/is.exe'
)

Set-StrictMode -Version Latest
$ErrorActionPreference = 'Stop'

. (Join-Path $PSScriptRoot 'thirdparty.ps1')

$paths = Get-GoldenCloudPaths
$issFile = Join-Path $paths.InstallerDir 'GoldenCloud.iss'
$expectedOutput = Join-Path $paths.InstallerOut 'GoldenCloudSetup.exe'

Write-Host "GoldenCloud installer"
Write-Host "====================="

if ($env:OS -ne 'Windows_NT') {
    throw "package.ps1 builds a Windows installer and must run on Windows."
}

# ---- 1. Make sure the inputs exist -----------------------------------------

$exe = Join-Path $paths.AppDir 'GoldenCloud.Tray.exe'
if (-not (Test-Path -LiteralPath $exe)) {
    Write-Host "No published app found; running build.ps1 first."
    & (Join-Path $PSScriptRoot 'build.ps1') -Configuration $Configuration -Version $Version
}

if (-not (Test-Path -LiteralPath $exe)) {
    throw "Expected $exe after build.ps1, but it is not there."
}

$thirdParty = Initialize-GoldenCloudThirdParty

# ---- 2. Locate or install Inno Setup ---------------------------------------

function Find-Iscc {
    $candidates = @(
        @(
            (Join-Path ${env:ProgramFiles(x86)} 'Inno Setup 6\ISCC.exe')
            (Join-Path $env:ProgramFiles 'Inno Setup 6\ISCC.exe')
            (Join-Path ${env:ProgramFiles(x86)} 'Inno Setup 5\ISCC.exe')
        ) | Where-Object { $_ -and (Test-Path -LiteralPath $_) }
    )

    if ($candidates.Count -gt 0) {
        return $candidates[0]
    }

    $onPath = Get-Command -Name 'iscc.exe' -ErrorAction SilentlyContinue
    if ($onPath) {
        return $onPath.Source
    }

    return $null
}

$iscc = Find-Iscc

if (-not $iscc) {
    Write-Host "Inno Setup was not found; installing it silently."
    $installer = Join-Path ([System.IO.Path]::GetTempPath()) 'innosetup-6.exe'
    Invoke-GcDownload -Url $InnoSetupInstallerUrl -Destination $installer

    $process = Start-Process -FilePath $installer `
        -ArgumentList '/VERYSILENT', '/SUPPRESSMSGBOXES', '/NORESTART', '/SP-', '/NOCANCEL' `
        -Wait -PassThru
    if ($process.ExitCode -ne 0) {
        throw "The Inno Setup installer exited with code $($process.ExitCode)."
    }

    $iscc = Find-Iscc
}

if (-not $iscc) {
    throw "Inno Setup (ISCC.exe) is still not available after installation."
}

Write-Host "ISCC:          $iscc"
Write-Host "App folder:    $($paths.AppDir)"
Write-Host "Third-party:   $($thirdParty.Directory)"
Write-Host "Output:        $expectedOutput"
Write-Host ""

# ---- 3. Compile the installer ----------------------------------------------

# Idempotent: a stale installer from a previous run must never be mistaken for
# the current one.
if (Test-Path -LiteralPath $expectedOutput) {
    Remove-Item -LiteralPath $expectedOutput -Force
}
if (-not (Test-Path -LiteralPath $paths.InstallerOut)) {
    New-Item -ItemType Directory -Path $paths.InstallerOut -Force | Out-Null
}

$isccArgs = @(
    "/DAppVersion=$Version"
    "/DSourceDir=$($paths.AppDir)"
    "/DThirdPartyDir=$($thirdParty.Directory)"
    $issFile
)

& $iscc @isccArgs
if ($LASTEXITCODE -ne 0) {
    throw "ISCC failed with exit code $LASTEXITCODE."
}

if (-not (Test-Path -LiteralPath $expectedOutput)) {
    throw "ISCC reported success but $expectedOutput does not exist."
}

$hash = (Get-FileHash -LiteralPath $expectedOutput -Algorithm SHA256).Hash.ToLowerInvariant()
$size = [math]::Round((Get-Item -LiteralPath $expectedOutput).Length / 1MB, 1)

Write-Host ""
Write-Host "Installer built"
Write-Host "---------------"
Write-Host "Path:   $expectedOutput"
Write-Host "Size:   $size MB"
Write-Host "SHA256: $hash"
