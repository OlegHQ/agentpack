param(
    [switch]$NoModifyPath,
    [switch]$Help
)

$ErrorActionPreference = 'Stop'
$Version = if ($env:AGENTPACK_VERSION) { $env:AGENTPACK_VERSION } else { '0.3.14' }
$Repository = if ($env:AGENTPACK_REPOSITORY) { $env:AGENTPACK_REPOSITORY } else { 'OlegHQ/agentpack' }

if ($Help) {
    Write-Output @"
agentpack-installer.ps1

Download, verify, and install agentpack $Version.

Options:
  -NoModifyPath   Do not add the install directory to the user PATH.
  -Help           Show this help.

Environment:
  AGENTPACK_VERSION, AGENTPACK_DOWNLOAD_URL, AGENTPACK_INSTALL_DIR,
  AGENTPACK_NO_MODIFY_PATH, AGENTPACK_GITHUB_TOKEN
"@
    exit 0
}

if ($env:AGENTPACK_DOWNLOAD_URL) {
    $BaseUrl = $env:AGENTPACK_DOWNLOAD_URL.TrimEnd('/')
} elseif ($env:INSTALLER_DOWNLOAD_URL) {
    $BaseUrl = $env:INSTALLER_DOWNLOAD_URL.TrimEnd('/')
} elseif ($env:AGENTPACK_INSTALLER_GHE_BASE_URL) {
    $BaseUrl = "$($env:AGENTPACK_INSTALLER_GHE_BASE_URL.TrimEnd('/'))/$Repository/releases/download/v$Version"
} elseif ($env:AGENTPACK_INSTALLER_GITHUB_BASE_URL) {
    $BaseUrl = "$($env:AGENTPACK_INSTALLER_GITHUB_BASE_URL.TrimEnd('/'))/$Repository/releases/download/v$Version"
} else {
    $BaseUrl = "https://github.com/$Repository/releases/download/v$Version"
}

if ($env:AGENTPACK_INSTALL_DIR) {
    $InstallDir = $env:AGENTPACK_INSTALL_DIR
} elseif ($env:AGENTPACK_UNMANAGED_INSTALL) {
    $InstallDir = $env:AGENTPACK_UNMANAGED_INSTALL
    $NoModifyPath = $true
} elseif ($env:CARGO_HOME) {
    $InstallDir = Join-Path $env:CARGO_HOME 'bin'
} else {
    $InstallDir = Join-Path $HOME '.cargo\bin'
}

$Archive = "agentpack_${Version}_windows_amd64.zip"
$Temporary = Join-Path ([IO.Path]::GetTempPath()) ("agentpack-install-" + [guid]::NewGuid())
$Headers = @{}
if ($env:AGENTPACK_GITHUB_TOKEN) { $Headers.Authorization = "Bearer $($env:AGENTPACK_GITHUB_TOKEN)" }

try {
    New-Item -ItemType Directory -Path $Temporary | Out-Null
    $ArchivePath = Join-Path $Temporary $Archive
    $ChecksumsPath = Join-Path $Temporary 'checksums.txt'
    Write-Output "Downloading agentpack $Version (windows/amd64)"
    Invoke-WebRequest -UseBasicParsing -Headers $Headers -Uri "$BaseUrl/$Archive" -OutFile $ArchivePath
    Invoke-WebRequest -UseBasicParsing -Headers $Headers -Uri "$BaseUrl/checksums.txt" -OutFile $ChecksumsPath
    $ChecksumLine = Get-Content $ChecksumsPath | Where-Object { $_ -match "\s\*?$([regex]::Escape($Archive))$" } | Select-Object -First 1
    if (-not $ChecksumLine) { throw "checksum for $Archive is missing" }
    $Expected = ($ChecksumLine -split '\s+')[0].ToLowerInvariant()
    $Actual = (Get-FileHash -Algorithm SHA256 $ArchivePath).Hash.ToLowerInvariant()
    if ($Actual -ne $Expected) { throw "checksum mismatch for $Archive" }

    $Unpacked = Join-Path $Temporary 'unpacked'
    Expand-Archive -Path $ArchivePath -DestinationPath $Unpacked
    $Binary = Join-Path $Unpacked 'agentpack.exe'
    if (-not (Test-Path -LiteralPath $Binary -PathType Leaf)) { throw "agentpack.exe is missing from $Archive" }
    New-Item -ItemType Directory -Force -Path $InstallDir | Out-Null
    $Destination = Join-Path $InstallDir 'agentpack.exe'
    $Pending = "$Destination.new"
    Copy-Item -Force $Binary $Pending
    Move-Item -Force $Pending $Destination

    $DisablePath = $NoModifyPath -or $env:AGENTPACK_NO_MODIFY_PATH -eq '1' -or $env:INSTALLER_NO_MODIFY_PATH -eq '1'
    if (-not $DisablePath) {
        $Current = [Environment]::GetEnvironmentVariable('Path', 'User')
        $Entries = @($Current -split ';' | Where-Object { $_ })
        if ($InstallDir -notin $Entries) {
            [Environment]::SetEnvironmentVariable('Path', (($InstallDir + ';' + $Current).TrimEnd(';')), 'User')
            Write-Output "Added $InstallDir to the user PATH"
        }
    }
    Write-Output "Installed agentpack $Version to $Destination"
} finally {
    if (Test-Path $Temporary) { Remove-Item -Recurse -Force $Temporary }
}
