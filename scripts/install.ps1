$ErrorActionPreference = "Stop"

$Repo = "robot-accomplice/ghola"
$ApiUrl = "https://api.github.com/repos/$Repo/releases/latest"
$InstallDir = if ($env:GHOLA_INSTALL_DIR) { $env:GHOLA_INSTALL_DIR } else { Join-Path $env:LOCALAPPDATA "Programs\ghola\bin" }

$release = Invoke-RestMethod -Uri $ApiUrl
$tag = $release.tag_name

if (-not $tag) {
    throw "Failed to determine latest release tag from GitHub API."
}

# A 32-bit PowerShell on 64-bit Windows reports x86 here; Windows keeps
# the real architecture in PROCESSOR_ARCHITEW6432.
$archRaw = if ($env:PROCESSOR_ARCHITEW6432) { $env:PROCESSOR_ARCHITEW6432 } else { $env:PROCESSOR_ARCHITECTURE }
$arch = switch ($archRaw) {
    "AMD64" { "amd64" }
    "ARM64" { "arm64" }
    default { throw "Unsupported architecture: $archRaw (expected AMD64 or ARM64)" }
}

# Release assets carry the version without the tag's leading "v".
$version = $tag.TrimStart("v")
$asset = "ghola_${version}_windows_${arch}.zip"
$baseUrl = "https://github.com/$Repo/releases/download/$tag"
$downloadUrl = "$baseUrl/$asset"

$tmpRoot = Join-Path $env:TEMP ("ghola-" + [Guid]::NewGuid().ToString("N"))
New-Item -ItemType Directory -Path $tmpRoot | Out-Null

try {
    $zipPath = Join-Path $tmpRoot $asset
    Write-Host "Downloading $asset..."
    Invoke-WebRequest -Uri $downloadUrl -OutFile $zipPath

    $sumsPath = Join-Path $tmpRoot "checksums.txt"
    Invoke-WebRequest -Uri "$baseUrl/checksums.txt" -OutFile $sumsPath
    $line = Get-Content $sumsPath | Where-Object { ($_ -split '\s+')[1] -eq $asset } | Select-Object -First 1
    if (-not $line) {
        throw "checksums.txt of $tag has no entry for $asset."
    }
    $want = ($line -split '\s+')[0].ToLower()
    $got = (Get-FileHash -Algorithm SHA256 -Path $zipPath).Hash.ToLower()
    if ($got -ne $want) {
        throw "Checksum mismatch for ${asset}: expected $want, got $got."
    }
    Write-Host "Checksum OK."

    Write-Host "Extracting..."
    Expand-Archive -Path $zipPath -DestinationPath $tmpRoot -Force

    New-Item -ItemType Directory -Path $InstallDir -Force | Out-Null
    Copy-Item -Path (Join-Path $tmpRoot "ghola.exe") -Destination (Join-Path $InstallDir "ghola.exe") -Force

    Write-Host "Installed ghola to $InstallDir\ghola.exe"

    $userPath = [Environment]::GetEnvironmentVariable("Path", "User")
    $parts = @()
    if ($userPath) {
        $parts = $userPath.Split(';') | Where-Object { $_ -ne "" }
    }
    if ($parts -notcontains $InstallDir) {
        $newPath = if ($userPath) { "$userPath;$InstallDir" } else { $InstallDir }
        [Environment]::SetEnvironmentVariable("Path", $newPath, "User")
        Write-Host "Added $InstallDir to your user PATH."
        Write-Host "Open a new PowerShell window, then run: ghola --version"
    } else {
        Write-Host "Install directory already exists in PATH."
        Write-Host "Verify with: ghola --version"
    }
}
finally {
    if (Test-Path $tmpRoot) {
        Remove-Item -Recurse -Force $tmpRoot
    }
}
