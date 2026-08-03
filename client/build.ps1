<#
.SYNOPSIS
    Publishes the GoldenCloud tray app as a self-contained single-file win-x64
    executable, with the server address baked in.

.DESCRIPTION
    The server address comes from -ServerUrl, or from the GOLDENCLOUD_SERVER_URL
    environment variable (which CI populates from the repository variable of the
    same name — see PROGRESS.md, human gate 2).

    It is injected as an MSBuild property, -p:GoldenCloudServerUrl=..., which the
    project turns into a generated source file under obj/. No checked-in source
    file is ever edited, so the working tree stays clean and the build is both
    deterministic and idempotent.

    If no address is supplied, the build still succeeds but produces an
    "unconfigured" executable that shows a banner and refuses to store
    credentials (D-007).

    Output: client/build/app/  — consumed verbatim by package.ps1.

.EXAMPLE
    ./build.ps1 -Configuration Release -ServerUrl https://cloud.example.org
#>
[CmdletBinding()]
param(
    [ValidateSet('Debug', 'Release')]
    [string]$Configuration = 'Release',

    [string]$ServerUrl = $env:GOLDENCLOUD_SERVER_URL,

    [string]$Runtime = 'win-x64',

    [string]$Version = '0.1.0',

    # Skip the rclone / WinFsp download. The installer cannot be built without
    # them, but `./build.ps1 -SkipThirdParty` is handy for a quick compile.
    [switch]$SkipThirdParty
)

Set-StrictMode -Version Latest
$ErrorActionPreference = 'Stop'

. (Join-Path $PSScriptRoot 'thirdparty.ps1')

$paths = Get-GoldenCloudPaths
$unconfigured = 'https://cloud.example.com'

Write-Host "GoldenCloud client build"
Write-Host "========================"

# ---- 1. Resolve and sanity-check the server address ------------------------

if ([string]::IsNullOrWhiteSpace($ServerUrl)) {
    $ServerUrl = $unconfigured
    Write-GcWarning ("GOLDENCLOUD_SERVER_URL is not set. Building an UNCONFIGURED client: " +
                     "it will show a banner and refuse to store credentials. " +
                     "See PROGRESS.md, human gate 2.")
}

$ServerUrl = $ServerUrl.Trim()

$parsed = $null
if (-not [System.Uri]::TryCreate($ServerUrl, [System.UriKind]::Absolute, [ref]$parsed)) {
    throw "GOLDENCLOUD_SERVER_URL is not a valid absolute URL: '$ServerUrl'"
}

if ($parsed.Scheme -eq 'http') {
    if (-not $parsed.IsLoopback) {
        throw "Refusing to bake in a plain http:// address for a non-loopback host: '$ServerUrl'. Use https://."
    }
}
elseif ($parsed.Scheme -ne 'https') {
    throw "GOLDENCLOUD_SERVER_URL must be https:// (or http:// to localhost). Got '$($parsed.Scheme)://'."
}

if ($ServerUrl -eq $unconfigured) {
    Write-Host "Server URL:    $ServerUrl  (UNCONFIGURED BUILD)"
}
else {
    Write-Host "Server URL:    $ServerUrl"
}

Write-Host "Configuration: $Configuration"
Write-Host "Runtime:       $Runtime"
Write-Host "Version:       $Version"
Write-Host ""

# ---- 2. Third-party binaries ------------------------------------------------

if ($SkipThirdParty) {
    Write-Host "Skipping the third-party download (-SkipThirdParty)."
}
else {
    Write-Host "Third-party binaries"
    Write-Host "--------------------"
    Initialize-GoldenCloudThirdParty | Out-Null
    Write-Host ""
}

# ---- 3. Publish -------------------------------------------------------------

# Idempotent: the output folder is rebuilt from scratch every time, so a stale
# file from a previous run can never end up in the installer.
if (Test-Path -LiteralPath $paths.AppDir) {
    Remove-Item -LiteralPath $paths.AppDir -Recurse -Force
}
New-Item -ItemType Directory -Path $paths.AppDir -Force | Out-Null

Write-Host "Publishing"
Write-Host "----------"

$publishArgs = @(
    'publish'
    $paths.TrayProject
    '--configuration', $Configuration
    '--runtime', $Runtime
    '--self-contained', 'true'
    '--output', $paths.AppDir
    '-p:PublishSingleFile=true'
    '-p:IncludeNativeLibrariesForSelfExtract=true'
    '-p:DebugType=none'
    '-p:DebugSymbols=false'
    "-p:GoldenCloudServerUrl=$ServerUrl"
    "-p:Version=$Version"
    "-p:AssemblyVersion=$Version.0"
    "-p:FileVersion=$Version.0"
    '-p:ContinuousIntegrationBuild=true'
    '--nologo'
)

& dotnet @publishArgs
if ($LASTEXITCODE -ne 0) {
    throw "dotnet publish failed with exit code $LASTEXITCODE."
}

$exe = Join-Path $paths.AppDir 'GoldenCloud.Tray.exe'
if (-not (Test-Path -LiteralPath $exe)) {
    throw "Publish succeeded but $exe is missing."
}

# Never ship symbols or a leftover settings file.
Get-ChildItem -LiteralPath $paths.AppDir -Filter '*.pdb' -Recurse -ErrorAction SilentlyContinue |
    Remove-Item -Force -ErrorAction SilentlyContinue

Write-Host ""
Write-Host "Output"
Write-Host "------"
Get-ChildItem -LiteralPath $paths.AppDir |
    Sort-Object -Property Length -Descending |
    Select-Object -First 10 Name, Length |
    Format-Table -AutoSize |
    Out-String |
    Write-Host

Write-Host "Published to: $($paths.AppDir)"
Write-Host "Next: ./package.ps1"
