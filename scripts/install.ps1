$ErrorActionPreference = "Stop"

$Repo = "robot-accomplice/ghola"
$ApiUrl = "https://api.github.com/repos/$Repo/releases/latest"
$InstallDir = if ($env:GHOLA_INSTALL_DIR) { $env:GHOLA_INSTALL_DIR } else { Join-Path $env:LOCALAPPDATA "Programs\ghola\bin" }

$release = Invoke-RestMethod -Uri $ApiUrl
$tag = $release.tag_name

if (-not $tag) {
    throw "Failed to determine latest release tag from GitHub API."
}

$archRaw = $env:PROCESSOR_ARCHITECTURE
$arch = switch ($archRaw) {
    "AMD64" { "amd64" }
    "ARM64" { "arm64" }
    default { throw "Unsupported architecture: $archRaw (expected AMD64 or ARM64)" }
}

$asset = "ghola_${tag}_windows_${arch}.zip"
$downloadUrl = "https://github.com/$Repo/releases/download/$tag/$asset"

$tmpRoot = Join-Path $env:TEMP ("ghola-" + [Guid]::NewGuid().ToString("N"))
New-Item -ItemType Directory -Path $tmpRoot | Out-Null

try {
    $zipPath = Join-Path $tmpRoot $asset
    Write-Host "Downloading $asset..."
    Invoke-WebRequest -Uri $downloadUrl -OutFile $zipPath

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
