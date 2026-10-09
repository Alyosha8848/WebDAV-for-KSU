# Build the DufsBox KernelSU module zip.
#
# Steps:
#   1. cross-compile the control daemon for android/arm64
#   2. place the four vendored arm64 binaries and verify their checksums
#   3. optionally build the Android app and bundle the APK into the module
#   4. emit dist/DufsBox-<version>-arm64.zip
#
# Usage:
#   powershell -ExecutionPolicy Bypass -File tools\build-module.ps1
#   powershell -ExecutionPolicy Bypass -File tools\build-module.ps1 -SkipApp

[CmdletBinding()]
param(
    [string]$Workspace = (Split-Path -Parent (Split-Path -Parent $PSScriptRoot)),
    [switch]$SkipApp,
    [switch]$SkipTests
)

$ErrorActionPreference = 'Stop'
$ProgressPreference = 'SilentlyContinue'

$ProjectRoot = Split-Path -Parent $PSScriptRoot
$ModuleDir = Join-Path $ProjectRoot 'module'
$BinDir = Join-Path $ModuleDir 'bin\arm64'
$AppDir = Join-Path $ProjectRoot 'app'
$DistDir = Join-Path $ProjectRoot 'dist'
$ToolsDir = Join-Path $Workspace '.tools'

function Step { param([string]$Text) Write-Host "`n== $Text ==" -ForegroundColor Cyan }
function Ok { param([string]$Text) Write-Host "   $Text" -ForegroundColor Green }
function Warn { param([string]$Text) Write-Host "   $Text" -ForegroundColor Yellow }
function Die { param([string]$Text) Write-Host "   $Text" -ForegroundColor Red; exit 1 }

# ---------------------------------------------------------------------------
# Version
# ---------------------------------------------------------------------------
$Version = '1.0.0'
$PropFile = Join-Path $ModuleDir 'module.prop'
if (Test-Path $PropFile) {
    $m = Select-String -LiteralPath $PropFile -Pattern '^version=(.+)$'
    if ($m) { $Version = $m.Matches[0].Groups[1].Value.Trim() }
}
Write-Host "DufsBox module build $Version" -ForegroundColor White
New-Item -ItemType Directory -Force -Path $DistDir | Out-Null

# ---------------------------------------------------------------------------
# 0. Toolchain
# ---------------------------------------------------------------------------
Step 'Toolchain'
$GoExe = Join-Path $ToolsDir 'go\bin\go.exe'
if (-not (Test-Path $GoExe)) { Die "Go toolchain not found at $GoExe (run the environment setup first)" }
$env:GOROOT = Join-Path $ToolsDir 'go'
$env:GOCACHE = Join-Path $ToolsDir 'gocache'
Ok "go: $(& $GoExe version)"

# ---------------------------------------------------------------------------
# 1. Runtime binaries
# ---------------------------------------------------------------------------
Step 'Runtime binaries (dufs / tailscale / tailscaled)'
$required = @{
    'dufs'       = 'dufs          (upstream file server + WebDAV, static musl)'
    'tailscale'  = 'tailscale     (CLI for the userspace tailnet)'
    'tailscaled' = 'tailscaled    (userspace-networking daemon)'
}
foreach ($name in $required.Keys) {
    $p = Join-Path $BinDir $name
    if (-not (Test-Path $p)) {
        Warn "missing $name - fetching runtime dependencies"
        & (Join-Path $ToolsDir 'fetch-runtime.ps1') -Root $Workspace | Out-Null
        break
    }
}
foreach ($name in $required.Keys) {
    $p = Join-Path $BinDir $name
    if (-not (Test-Path $p)) { Die "missing $($required[$name])" }
    Ok ("{0,-12} {1,12:N0} bytes" -f $name, (Get-Item $p).Length)
}

# ---------------------------------------------------------------------------
# 2. Build the daemon for android/arm64
# ---------------------------------------------------------------------------
Step 'Build dufsboxd (android/arm64)'
Push-Location (Join-Path $ProjectRoot 'daemon')
try {
    if (-not $SkipTests) {
        & $GoExe vet ./... 2>&1 | Out-Host
        if ($LASTEXITCODE -ne 0) { Die 'go vet failed' }
        & $GoExe test ./... 2>&1 | Out-Host
        if ($LASTEXITCODE -ne 0) { Die 'go test failed' }
        Ok 'vet + unit tests passed'
    }
    $env:GOOS = 'android'
    $env:GOARCH = 'arm64'
    $env:CGO_ENABLED = '0'
    & $GoExe build -trimpath -ldflags '-s -w' -o (Join-Path $BinDir 'dufsboxd') .
    if ($LASTEXITCODE -ne 0) { Die 'go build failed' }
    Ok ("dufsboxd     {0,12:N0} bytes" -f (Get-Item (Join-Path $BinDir 'dufsboxd')).Length)
} finally {
    Remove-Item Env:GOOS, Env:GOARCH, Env:CGO_ENABLED -ErrorAction SilentlyContinue
    Pop-Location
}

# ---------------------------------------------------------------------------
# 3. Checksums
# ---------------------------------------------------------------------------
Step 'Checksums'
$checksumFile = Join-Path $ModuleDir 'checksums.sha256'
$lines = @()
foreach ($name in @('dufs', 'tailscale', 'tailscaled', 'dufsboxd')) {
    $rel = "bin/arm64/$name"
    $hash = (Get-FileHash -Algorithm SHA256 -LiteralPath (Join-Path $ModuleDir ($rel -replace '/', '\'))).Hash.ToLowerInvariant()
    $lines += "$hash  $rel"
    Ok "$($hash.Substring(0,16))...  $rel"
}
# LF endings: the installer runs in /system/bin/sh on Android.
[System.IO.File]::WriteAllText($checksumFile, (($lines -join "`n") + "`n"), (New-Object System.Text.UTF8Encoding($false)))

# ---------------------------------------------------------------------------
# 4. Android app
# ---------------------------------------------------------------------------
$apkOut = Join-Path $ModuleDir 'DufsBox.apk'
if ($SkipApp) {
    Warn 'skipping app build (-SkipApp)'
} else {
    Step 'Build Android app'
    $sdk = Join-Path $ToolsDir 'android-sdk'
    $jdk = if (Test-Path (Join-Path $ToolsDir 'jdk21\bin\java.exe')) { Join-Path $ToolsDir 'jdk21' } else { 'D:\Software\#DevTools\Java\25' }
    $env:JAVA_HOME = $jdk
    $env:ANDROID_HOME = $sdk
    $env:ANDROID_SDK_ROOT = $sdk
    Ok "JAVA_HOME=$jdk"

    $gradleBat = Get-ChildItem -Path (Join-Path $env:USERPROFILE '.gradle\wrapper\dists') -Filter 'gradle.bat' -Recurse -ErrorAction SilentlyContinue |
        Where-Object { $_.FullName -match 'gradle-\d' } | Select-Object -First 1
    $gradleExe = if ($gradleBat) { $gradleBat.FullName } else { $null }
    if (-not $gradleExe) {
        $gradleHome = Get-ChildItem -Path (Join-Path $ToolsDir 'gradle-*\bin\gradle.bat') -ErrorAction SilentlyContinue | Select-Object -First 1
        if ($gradleHome) { $gradleExe = $gradleHome.FullName }
    }
    if (-not $gradleExe) { Die 'no Gradle distribution found (expected one under ~/.gradle/wrapper/dists or .tools/gradle-*)' }
    Ok "gradle: $gradleExe"

    $lp = Join-Path $ProjectRoot 'local.properties'
    [System.IO.File]::WriteAllText($lp, "sdk.dir=$($sdk -replace '\\','\\')`n", (New-Object System.Text.UTF8Encoding($false)))

    Push-Location $ProjectRoot
    try {
        & $gradleExe --no-daemon :app:assembleDebug 2>&1 | Out-Host
        if ($LASTEXITCODE -ne 0) { Die 'gradle assembleDebug failed' }
    } finally {
        Pop-Location
    }

    $apk = Get-ChildItem -Path (Join-Path $AppDir 'build\outputs\apk') -Filter '*.apk' -Recurse -ErrorAction SilentlyContinue |
        Sort-Object LastWriteTime -Descending | Select-Object -First 1
    if (-not $apk) { Die 'the app build produced no APK' }
    Copy-Item -LiteralPath $apk.FullName -Destination $apkOut -Force
    Ok ("app apk      {0,12:N0} bytes  ({1})" -f (Get-Item $apkOut).Length, $apk.Name)
}

# ---------------------------------------------------------------------------
# 5. Package
# ---------------------------------------------------------------------------
Step 'Package'
# On .NET Framework ZipFile/ZipFileExtensions live in System.IO.Compression.FileSystem
# while ZipArchive/ZipArchiveMode live in System.IO.Compression, so both are needed.
Add-Type -AssemblyName System.IO.Compression
Add-Type -AssemblyName System.IO.Compression.FileSystem
$zipPath = Join-Path $DistDir "DufsBox-v$Version-arm64.zip"
if (Test-Path $zipPath) { Remove-Item -Force $zipPath }

# The zip root must contain module.prop: that is what the KernelSU installer expects.
$staging = Join-Path $env:TEMP ("dufsbox-stage-" + [guid]::NewGuid().ToString('N').Substring(0, 8))
New-Item -ItemType Directory -Force -Path $staging | Out-Null
Copy-Item -Recurse -Force (Join-Path $ModuleDir '*') $staging

# Entries are written by hand rather than with ZipFile.CreateFromDirectory.
# On Windows PowerShell (.NET Framework) that helper stores the *OS* directory
# separator, producing entries literally named "bin\arm64\dufs". Android's unzip
# then creates a single file whose name contains backslashes instead of the
# directory tree, and the module installs broken. The ZIP spec mandates "/".
$base = (Resolve-Path -LiteralPath $staging).Path.TrimEnd('\')
$stream = [System.IO.File]::Open($zipPath, [System.IO.FileMode]::Create)
try {
    $archive = New-Object System.IO.Compression.ZipArchive($stream, [System.IO.Compression.ZipArchiveMode]::Create)
    try {
        foreach ($file in (Get-ChildItem -Recurse -File -LiteralPath $staging | Sort-Object FullName)) {
            $rel = $file.FullName.Substring($base.Length + 1).Replace('\', '/')
            $entry = $archive.CreateEntry($rel, [System.IO.Compression.CompressionLevel]::Optimal)
            $es = $entry.Open()
            try {
                $fs = [System.IO.File]::OpenRead($file.FullName)
                try { $fs.CopyTo($es) } finally { $fs.Dispose() }
            } finally { $es.Dispose() }
        }
    } finally { $archive.Dispose() }
} finally { $stream.Dispose() }
Remove-Item -Recurse -Force $staging

$size = (Get-Item $zipPath).Length
Ok ("{0}  ({1:N1} MB)" -f $zipPath, ($size / 1MB))

Step 'Verify the packaged module'
# Inspect the RAW entry names. Extracting with .NET would rewrite the separators
# and hide exactly the defect this check exists to catch.
$zip = [System.IO.Compression.ZipFile]::OpenRead($zipPath)
try {
    $names = @($zip.Entries | ForEach-Object { $_.FullName })
} finally { $zip.Dispose() }

$badSeparators = @($names | Where-Object { $_ -like '*\*' })
if ($badSeparators.Count -gt 0) {
    Die ("zip entries must use '/' as the separator, found: " + ($badSeparators -join ', '))
}
$needed = @('module.prop', 'customize.sh', 'service.sh', 'action.sh', 'uninstall.sh', 'checksums.sha256',
            'bin/arm64/dufs', 'bin/arm64/tailscale', 'bin/arm64/tailscaled', 'bin/arm64/dufsboxd',
            'webroot/index.html')
$missing = @($needed | Where-Object { $names -notcontains $_ })
if ($missing.Count -gt 0) { Die ("module is missing: " + ($missing -join ', ')) }
Ok "all $($needed.Count) required files present ($($names.Count) entries), separators are '/'"

Write-Host ''
Write-Host "Module ready: $zipPath" -ForegroundColor Green
