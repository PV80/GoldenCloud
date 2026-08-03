<#
.SYNOPSIS
    Fetches the pinned third-party binaries GoldenCloud ships with.

.DESCRIPTION
    rclone.exe and the WinFsp redistributable MSI are downloaded at build time
    into client/thirdparty/ (gitignored) and verified against the SHA256 values
    in client/thirdparty.pins.json.

    Dot-source this file; it defines functions only and does nothing on import.

    Offline / air-gapped builds: set GOLDENCLOUD_RCLONE_EXE and
    GOLDENCLOUD_WINFSP_MSI to local paths and the download is skipped.
#>

Set-StrictMode -Version Latest

# Captured while this file is being dot-sourced, so it is this file's directory
# (client/) regardless of which script later calls the functions below.
$GoldenCloudClientRoot = $PSScriptRoot

function Get-GoldenCloudPaths {
    $clientRoot = $GoldenCloudClientRoot
    [pscustomobject]@{
        ClientRoot     = $clientRoot
        PinsFile       = Join-Path $clientRoot 'thirdparty.pins.json'
        ThirdPartyDir  = Join-Path $clientRoot 'thirdparty'
        BuildDir       = Join-Path $clientRoot 'build'
        AppDir         = Join-Path (Join-Path $clientRoot 'build') 'app'
        InstallerDir   = Join-Path $clientRoot 'installer'
        InstallerOut   = Join-Path (Join-Path $clientRoot 'installer') 'Output'
        SolutionFile   = Join-Path $clientRoot 'GoldenCloud.sln'
        TrayProject    = Join-Path (Join-Path $clientRoot 'GoldenCloud.Tray') 'GoldenCloud.Tray.csproj'
    }
}

function Write-GcWarning {
    param([Parameter(Mandatory)][string]$Message)

    if ($env:GITHUB_ACTIONS -eq 'true') {
        Write-Host "::warning::$Message"
    }
    Write-Warning $Message
}

function Get-GoldenCloudPins {
    $paths = Get-GoldenCloudPaths
    if (-not (Test-Path -LiteralPath $paths.PinsFile)) {
        throw "Pin file not found: $($paths.PinsFile)"
    }
    Get-Content -LiteralPath $paths.PinsFile -Raw | ConvertFrom-Json
}

function Test-GcHash {
    <#
        Returns $true when the file matches the pin. An empty pin is treated as
        "unpinned": a loud warning, the real hash printed, and the build carries
        on so an operator is never blocked by a value nobody has recorded yet.
    #>
    param(
        [Parameter(Mandatory)][string]$Path,
        [AllowEmptyString()][string]$Expected,
        [Parameter(Mandatory)][string]$Name
    )

    $actual = (Get-FileHash -LiteralPath $Path -Algorithm SHA256).Hash.ToLowerInvariant()

    if ([string]::IsNullOrWhiteSpace($Expected)) {
        Write-GcWarning ("Third-party component '$Name' is NOT pinned. Downloaded SHA256 is $actual. " +
                         "Record it in client/thirdparty.pins.json to turn on verification " +
                         "(see client/thirdparty/README.md).")
        return $true
    }

    $expectedNormalised = $Expected.Trim().ToLowerInvariant()
    if ($actual -ne $expectedNormalised) {
        throw "SHA256 mismatch for '$Name'.`n  expected: $expectedNormalised`n  actual:   $actual`nRefusing to build with an unexpected binary."
    }

    Write-Host "  sha256 verified: $Name"
    return $true
}

function Invoke-GcDownload {
    param(
        [Parameter(Mandatory)][string]$Url,
        [Parameter(Mandatory)][string]$Destination
    )

    Write-Host "  downloading $Url"
    $directory = Split-Path -Parent $Destination
    if (-not (Test-Path -LiteralPath $directory)) {
        New-Item -ItemType Directory -Path $directory -Force | Out-Null
    }

    $previousProgress = $ProgressPreference
    $ProgressPreference = 'SilentlyContinue'
    try {
        Invoke-WebRequest -Uri $Url -OutFile $Destination -UseBasicParsing -MaximumRedirection 5
    }
    finally {
        $ProgressPreference = $previousProgress
    }

    if (-not (Test-Path -LiteralPath $Destination)) {
        throw "Download produced no file: $Url"
    }
}

function Initialize-GoldenCloudThirdParty {
    <#
        Ensures client/thirdparty/rclone.exe and client/thirdparty/winfsp.msi
        exist and match their pins. Idempotent: an already-correct file is left
        alone, so repeated builds do not re-download.
    #>
    [CmdletBinding()]
    param([switch]$Force)

    $paths = Get-GoldenCloudPaths
    $pins = Get-GoldenCloudPins

    if (-not (Test-Path -LiteralPath $paths.ThirdPartyDir)) {
        New-Item -ItemType Directory -Path $paths.ThirdPartyDir -Force | Out-Null
    }

    # ---- rclone ----------------------------------------------------------
    $rclonePath = Join-Path $paths.ThirdPartyDir $pins.rclone.target

    if ($env:GOLDENCLOUD_RCLONE_EXE -and (Test-Path -LiteralPath $env:GOLDENCLOUD_RCLONE_EXE)) {
        Write-Host "rclone: using local override $($env:GOLDENCLOUD_RCLONE_EXE)"
        Copy-Item -LiteralPath $env:GOLDENCLOUD_RCLONE_EXE -Destination $rclonePath -Force
    }
    elseif ($Force -or -not (Test-Path -LiteralPath $rclonePath)) {
        Write-Host "rclone $($pins.rclone.version):"
        $zip = Join-Path $paths.ThirdPartyDir "rclone-$($pins.rclone.version).zip"
        Invoke-GcDownload -Url $pins.rclone.url -Destination $zip
        Test-GcHash -Path $zip -Expected $pins.rclone.sha256 -Name "rclone $($pins.rclone.version)" | Out-Null

        $extractDir = Join-Path $paths.ThirdPartyDir "rclone-extract"
        if (Test-Path -LiteralPath $extractDir) {
            Remove-Item -LiteralPath $extractDir -Recurse -Force
        }
        Expand-Archive -LiteralPath $zip -DestinationPath $extractDir -Force

        $found = Get-ChildItem -LiteralPath $extractDir -Recurse -Filter $pins.rclone.entry |
            Select-Object -First 1
        if (-not $found) {
            throw "Could not find $($pins.rclone.entry) inside $zip"
        }
        Copy-Item -LiteralPath $found.FullName -Destination $rclonePath -Force

        $licence = Get-ChildItem -LiteralPath $extractDir -Recurse -Filter $pins.rclone.licenseEntry |
            Select-Object -First 1
        if ($licence) {
            Copy-Item -LiteralPath $licence.FullName `
                      -Destination (Join-Path $paths.ThirdPartyDir $pins.rclone.licenseTarget) -Force
        }

        Remove-Item -LiteralPath $extractDir -Recurse -Force
        Remove-Item -LiteralPath $zip -Force
    }
    else {
        Write-Host "rclone: already present"
    }

    if (-not (Test-Path -LiteralPath $rclonePath)) {
        throw "rclone.exe is missing after the third-party step: $rclonePath"
    }

    # ---- WinFsp ----------------------------------------------------------
    $winfspPath = Join-Path $paths.ThirdPartyDir $pins.winfsp.target

    if ($env:GOLDENCLOUD_WINFSP_MSI -and (Test-Path -LiteralPath $env:GOLDENCLOUD_WINFSP_MSI)) {
        Write-Host "WinFsp: using local override $($env:GOLDENCLOUD_WINFSP_MSI)"
        Copy-Item -LiteralPath $env:GOLDENCLOUD_WINFSP_MSI -Destination $winfspPath -Force
    }
    elseif ($Force -or -not (Test-Path -LiteralPath $winfspPath)) {
        Write-Host "WinFsp $($pins.winfsp.version):"
        Invoke-GcDownload -Url $pins.winfsp.url -Destination $winfspPath
        Test-GcHash -Path $winfspPath -Expected $pins.winfsp.sha256 -Name "WinFsp $($pins.winfsp.version)" | Out-Null
    }
    else {
        Write-Host "WinFsp: already present"
    }

    if (-not (Test-Path -LiteralPath $winfspPath)) {
        throw "winfsp.msi is missing after the third-party step: $winfspPath"
    }

    [pscustomobject]@{
        RclonePath = $rclonePath
        WinFspPath = $winfspPath
        Directory  = $paths.ThirdPartyDir
    }
}
