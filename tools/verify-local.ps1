# DufsBox end-to-end verification harness
#
# Runs the real control daemon against a real dufs binary on this machine and
# asserts the behaviour the Android module depends on: per-device ACLs, streaming
# integrity, auth passthrough, config hot-reload and the control API.
#
# The daemon is compiled for the host OS; only the arm64 cross-compiled binary
# ships on device. The ACL proxy, logging and control plane are identical.
#
# Usage:
#   pwsh -File tools\verify-local.ps1 [-Keep] [-Verbose]

[CmdletBinding()]
param(
    [switch]$Keep,
    # Directory holding the downloaded build toolchain (.tools/). It is the parent
    # of the project root, because the toolchain is a build-time artefact and is
    # deliberately kept out of the module tree.
    [string]$Workspace = (Split-Path -Parent (Split-Path -Parent $PSScriptRoot))
)

$ErrorActionPreference = 'Stop'
$ProgressPreference = 'SilentlyContinue'

$Failures = 0
$Checks = 0
function Check {
    param([string]$Name, [bool]$Condition, [string]$Detail = '')
    $script:Checks++
    if ($Condition) {
        Write-Host ("  [PASS] {0}" -f $Name) -ForegroundColor Green
    } else {
        Write-Host ("  [FAIL] {0}  {1}" -f $Name, $Detail) -ForegroundColor Red
        $script:Failures++
    }
}
function Section { param([string]$Title) Write-Host "`n=== $Title ===" -ForegroundColor Cyan }

function HttpCode {
    param([string]$Method, [string]$Url, [string[]]$Extra = @())
    $a = @('-s', '-o', 'NUL', '-w', '%{http_code}', '--max-time', '120', '-X', $Method) + $Extra + @($Url)
    $code = (& curl.exe @a 2>$null)
    if ($LASTEXITCODE -ne 0 -and [string]::IsNullOrWhiteSpace($code)) { return '000' }
    return ($code | Select-Object -Last 1).Trim()
}
function HttpBody {
    param([string]$Method, [string]$Url, [string[]]$Extra = @())
    $a = @('-s', '--max-time', '120', '-X', $Method) + $Extra + @($Url)
    return (& curl.exe @a 2>$null | Out-String)
}
function Ctl {
    param([string]$Verb, [string]$Json = '{}')
    # The payload goes in over stdin. Windows PowerShell 5.1 strips embedded double
    # quotes when passing an argument to a native executable, which would silently
    # corrupt every JSON payload; piping avoids command-line quoting entirely.
    $raw = $Json | & $script:Daemon ctl $Verb --json-stdin 2>$null | Out-String
    if ([string]::IsNullOrWhiteSpace($raw)) { return $null }
    try { return ($raw.Trim() | ConvertFrom-Json) } catch { return $null }
}
function Sha256File { param([string]$Path) return (Get-FileHash -Algorithm SHA256 -LiteralPath $Path).Hash }

# ---------------------------------------------------------------------------
# Environment
# ---------------------------------------------------------------------------
$T = Join-Path $env:TEMP ("dufsbox-e2e-" + [guid]::NewGuid().ToString('N').Substring(0, 8))
$State = Join-Path $T 'state'
$Share = Join-Path $T 'share'
$Share2 = Join-Path $T 'share-b'
$Mod = Join-Path $T 'mod'
$BinDir = Join-Path $Mod 'bin\arm64'
$HostExe = Join-Path $Workspace '.tools\out\dufsboxd.exe'
$HostDufs = Join-Path $Workspace '.tools\dufs-win\dufs.exe'
$Daemon = $HostExe

Write-Host "DufsBox local verification" -ForegroundColor White
Write-Host "  workspace : $T"

if (-not (Test-Path $HostExe)) { throw "Missing $HostExe - build the daemon first: go build -o .tools\out\dufsboxd.exe ." }
if (-not (Test-Path $HostDufs)) { throw "Missing $HostDufs - fetch the native test binary first (tools\fetch-runtime.ps1)" }

New-Item -ItemType Directory -Force -Path $State, $Share, "$Share\sub", $Share2, $BinDir | Out-Null
Set-Content -LiteralPath (Join-Path $Share 'hello.txt') -Value 'hello dufsbox' -NoNewline
Set-Content -LiteralPath (Join-Path $Share2 'other.txt') -Value 'second share' -NoNewline
Copy-Item -LiteralPath $HostDufs -Destination (Join-Path $BinDir 'dufs.exe') -Force

# The daemon derives its internal (loopback-only) dufs port as public+10000, so
# BOTH ports have to be usable, not just the public one.
#
# This is not just tidiness: Windows reserves whole blocks of TCP ports for
# Hyper-V/WSL (see `netsh int ipv4 show excludedportrange protocol=tcp`), and a
# bind inside a reserved block fails with WSAEACCES (10013) instead of "address
# already in use". An earlier version of this harness probed only the public port,
# picked public 8370, and the derived 18370 fell inside the reserved 18293-18392
# block, so dufs could not start at all. On Android no such reservations exist.
function Test-FreePort {
    param([int]$Port)
    $l = New-Object System.Net.Sockets.TcpListener([System.Net.IPAddress]::Loopback, $Port)
    try { $l.Start(); $l.Stop(); return $true } catch { try { $l.Stop() } catch { }; return $false }
}

function Get-FreePublicPort {
    param([int]$Start)
    for ($p = $Start; $p -lt ($Start + 3000); $p++) {
        if ((Test-FreePort $p) -and (Test-FreePort ($p + 10000))) { return $p }
    }
    throw "no usable public/internal port pair near $Start"
}

$PublicPort = Get-FreePublicPort (8100 + (Get-Random -Maximum 400))
$PublicPort2 = Get-FreePublicPort ($PublicPort + 400)
$taken = @($PublicPort, $PublicPort + 10000, $PublicPort2, $PublicPort2 + 10000)
$ControlPort = $null
foreach ($candidate in 13300..16000) {
    if ($taken -contains $candidate) { continue }
    if (Test-FreePort $candidate) { $ControlPort = $candidate; break }
}
if (-not $ControlPort) { throw 'no free control port found' }
Write-Host "  ports     : public=$PublicPort (dufs internal=$($PublicPort + 10000))  control=$ControlPort"

$env:DUFSBOX_STATE = $State
$env:DUFSBOX_MODDIR = $Mod
$env:DUFSBOX_BINDIR = $BinDir
$env:DUFSBOX_DUFS = (Join-Path $BinDir 'dufs.exe')
$env:DUFSBOX_CONTROL_TCP = "127.0.0.1:$ControlPort"

# Pre-seed the configuration so the daemon does not try to share /sdcard first.
$config = [ordered]@{
    share_path      = $Share
    share_name      = 'DufsBoxTest'
    port            = $PublicPort
    mode            = 'lan'
    auth_mode       = 'anonymous'
    username        = 'dufsbox'
    password        = ''
    default_policy  = 'rw'
    readonly_global = $false
    autostart       = $true
    log_level       = 'trace'
    tailscale       = [ordered]@{ hostname = 'dufsbox'; https_serve = $false; accept_dns = $false; login_server = ''; auth_key = '' }
}
New-Item -ItemType Directory -Force -Path $State | Out-Null
# Write without a BOM. PowerShell 5.1's `Set-Content -Encoding UTF8` prepends one,
# and a BOM is not valid at the start of a JSON document. The daemon tolerates a
# BOM too, but the harness should not depend on that.
$configJson = $config | ConvertTo-Json -Depth 5
[System.IO.File]::WriteAllText((Join-Path $State 'config.json'), $configJson, (New-Object System.Text.UTF8Encoding($false)))

$Base = "http://127.0.0.1:$PublicPort"
$CtlBase = "http://127.0.0.1:$ControlPort"

# ---------------------------------------------------------------------------
# Start the daemon
# ---------------------------------------------------------------------------
Section '启动守护进程'
$outLog = Join-Path $T 'daemon.out'
$errLog = Join-Path $T 'daemon.err'
$proc = Start-Process -FilePath $Daemon -ArgumentList @('serve') -PassThru -NoNewWindow `
    -RedirectStandardOutput $outLog -RedirectStandardError $errLog

$ready = $false
foreach ($i in 1..60) {
    Start-Sleep -Milliseconds 300
    try {
        $r = Invoke-WebRequest -Uri "$CtlBase/ping" -TimeoutSec 2 -UseBasicParsing
        if ($r.StatusCode -eq 200) { $ready = $true; break }
    } catch { }
}
Check '控制接口就绪 (/ping)' $ready
if (-not $ready) {
    Write-Host '--- daemon stderr ---' -ForegroundColor Yellow
    Get-Content -LiteralPath $errLog -ErrorAction SilentlyContinue | Select-Object -Last 40 | ForEach-Object { Write-Host "  $_" }
    if (-not $Keep) { Remove-Item -Recurse -Force $T -ErrorAction SilentlyContinue }
    exit 1
}

# Fail fast, with the real reason, if the dufs backend cannot come up. Without
# this a single startup problem surfaces as dozens of unrelated-looking request
# failures and hides its own cause.
$dufsUp = $false
foreach ($i in 1..30) {
    $probe = Ctl 'status'
    if ($probe -and $probe.data.dufs.running -eq $true) { $dufsUp = $true; break }
    Start-Sleep -Milliseconds 500
}
Check 'dufs 后端已就绪' $dufsUp
if (-not $dufsUp) {
    Write-Host '--- daemon stderr ---' -ForegroundColor Yellow
    Get-Content -LiteralPath $errLog -ErrorAction SilentlyContinue | Select-Object -Last 30 | ForEach-Object { Write-Host "  $_" }
    Write-Host '--- dufsboxd.log (tail) ---' -ForegroundColor Yellow
    Get-Content -LiteralPath (Join-Path $State 'logs\dufsboxd.log') -ErrorAction SilentlyContinue |
        Select-Object -Last 40 | ForEach-Object { Write-Host "  $_" }
    $diag = Ctl 'status'
    if ($diag) { Write-Host ("  last_error = " + $diag.data.last_error) -ForegroundColor Yellow }
    $dufsCopy = Join-Path $BinDir 'dufs.exe'
    Write-Host ("  dufs binary: $dufsCopy exists=" + (Test-Path $dufsCopy) + " port=" + $PublicPort) -ForegroundColor Yellow
    Write-Host "  temp dir kept for inspection: $T" -ForegroundColor Yellow
    if ($proc -and -not $proc.HasExited) { Stop-Process -Id $proc.Id -Force -ErrorAction SilentlyContinue }
    Get-Process -Name 'dufs' -ErrorAction SilentlyContinue | Where-Object { $_.Path -like "$T*" } | Stop-Process -Force -ErrorAction SilentlyContinue
    exit 1
}

try {
    # -----------------------------------------------------------------------
    Section '版本与状态'
    $v = Ctl 'version'
    Check 'version 返回 ok' ($v.ok -eq $true)
    Check 'dufs 版本探测为 0.46.0' ($v.data.dufs -eq '0.46.0') "got=$($v.data.dufs)"

    $st = Ctl 'status'
    Check 'status 返回 ok' ($st.ok -eq $true)
    # If the pre-seeded config was not picked up, every later check fails for the
    # same reason, so surface the cause immediately instead of a wall of noise.
    if ($st.data.config.share_path -ne $Share) {
        Write-Host '--- daemon stderr ---' -ForegroundColor Yellow
        Get-Content -LiteralPath $errLog -ErrorAction SilentlyContinue | Select-Object -Last 25 | ForEach-Object { Write-Host "  $_" }
        Write-Host ("  config on disk: " + ([System.IO.File]::ReadAllText((Join-Path $State 'config.json')))) -ForegroundColor Yellow
    }
    Check 'dufs 子进程在运行' ($st.data.dufs.running -eq $true)
    Check 'dufs 仅监听回环地址' ($st.data.dufs.bind -eq "127.0.0.1:$($PublicPort + 10000)") "got=$($st.data.dufs.bind)"
    Check '共享路径正确' ($st.data.config.share_path -eq $Share) "got=$($st.data.config.share_path)"
    Check '协议信息包含 WebDAV RFC 4918' ($st.data.protocol.webdav -like '*4918*')
    Check '健康检查: 路径存在' ($st.data.health.share_path_exists -eq $true)
    Check '健康检查: 路径可写' ($st.data.health.share_path_writable -eq $true)
    Check '口令未回显' ($st.data.config.password_set -eq $false)
    Check '局域网地址已列出' ($st.data.addresses.lan.Count -ge 1) "got=$($st.data.addresses.lan -join ',')"

    # -----------------------------------------------------------------------
    Section '基础 HTTP / WebDAV 透传 (默认策略 rw)'
    Check 'GET / -> 200' ((HttpCode GET "$Base/") -eq '200')
    Check 'GET /hello.txt -> 200' ((HttpCode GET "$Base/hello.txt") -eq '200')
    Check 'GET 内容正确' ((HttpBody GET "$Base/hello.txt").Trim() -eq 'hello dufsbox')
    Check 'PROPFIND / -> 207' ((HttpCode PROPFIND "$Base/") -eq '207')
    Check 'OPTIONS / -> 200/204' ((HttpCode OPTIONS "$Base/") -in @('200', '204'))
    Check 'MKCOL /newdir -> 2xx' ((HttpCode MKCOL "$Base/newdir") -match '^2')

    # -----------------------------------------------------------------------
    Section '大文件流式完整性 (20 MiB 往返)'
    $big = Join-Path $T 'big.bin'
    $fs = [System.IO.File]::Create($big)
    $buf = New-Object byte[] (1MB)
    $rnd = [System.Random]::new(1234)
    foreach ($i in 1..20) { $rnd.NextBytes($buf); $fs.Write($buf, 0, $buf.Length) }
    $fs.Close()
    $bigHash = Sha256File $big
    $code = HttpCode PUT "$Base/big.bin" @('--data-binary', "@$big", '-H', 'Content-Type: application/octet-stream')
    Check 'PUT 20MiB -> 2xx' ($code -match '^2') "code=$code"
    $back = Join-Path $T 'big-back.bin'
    if (Test-Path $back) { Remove-Item -Force $back }
    & curl.exe -s --max-time 300 -o $back "$Base/big.bin" | Out-Null
    if (Test-Path $back) {
        Check '下载后 SHA-256 一致' ((Sha256File $back) -eq $bigHash) "expected=$bigHash got=$(Sha256File $back)"
        Check '下载大小一致' ((Get-Item $back).Length -eq (Get-Item $big).Length)
    } else {
        Check '下载后 SHA-256 一致' $false 'nothing was downloaded'
        Check '下载大小一致' $false 'nothing was downloaded'
    }

    # -----------------------------------------------------------------------
    Section '设备列表与统计'
    $d = Ctl 'devices'
    Check 'devices 返回 ok' ($d.ok -eq $true)
    $dev = $d.data.devices | Where-Object { $_.ip -eq '127.0.0.1' } | Select-Object -First 1
    Check '本机客户端出现在设备列表' ($null -ne $dev)
    Check '设备默认策略为 rw' ($dev.policy -eq 'rw') "got=$($dev.policy)"
    Check '策略来源为 default' ($dev.policy_source -eq 'default') "got=$($dev.policy_source)"
    Check '请求计数已累加' ([int]$dev.requests -gt 0) "got=$($dev.requests)"
    Check '下载字节数已累加' ([int64]$dev.bytes_out -ge 20MB) "got=$($dev.bytes_out)"
    Check '上传字节数已累加' ([int64]$dev.bytes_in -ge 20MB) "got=$($dev.bytes_in)"
    Check '设备 id 回退为 IP' ($dev.id -eq '127.0.0.1') "got=$($dev.id)"

    # -----------------------------------------------------------------------
    Section '按设备只读 (ro) 真正拦截写操作'
    $set = Ctl 'acl.set' (@{ id = '127.0.0.1'; policy = 'ro' } | ConvertTo-Json -Compress)
    Check 'acl.set ro 成功' ($set.ok -eq $true)
    Check 'GET 仍可读 -> 200' ((HttpCode GET "$Base/hello.txt") -eq '200')
    Check 'PROPFIND 仍可读 -> 207' ((HttpCode PROPFIND "$Base/") -eq '207')
    Check 'PUT 被拒绝 -> 403' ((HttpCode PUT "$Base/should-fail.bin" @('--data-binary', 'x')) -eq '403')
    Check 'DELETE 被拒绝 -> 403' ((HttpCode DELETE "$Base/hello.txt") -eq '403')
    Check 'MKCOL 被拒绝 -> 403' ((HttpCode MKCOL "$Base/nope") -eq '403')
    Check 'COPY 被拒绝 -> 403' ((HttpCode COPY "$Base/hello.txt" @('-H', "Destination: $Base/copy.txt")) -eq '403')
    Check 'MOVE 被拒绝 -> 403' ((HttpCode MOVE "$Base/hello.txt" @('-H', "Destination: $Base/moved.txt")) -eq '403')
    Check '被拒绝的文件确实没有写入' (-not (Test-Path (Join-Path $Share 'should-fail.bin')))
    Check '原文件确实没有被删除' (Test-Path (Join-Path $Share 'hello.txt'))
    Check '拒绝响应带 X-DufsBox-Policy 头' (
        (& curl.exe -s -D - -o NUL -X PUT --data-binary 'x' "$Base/hdr.bin" 2>$null | Out-String) -match 'X-DufsBox-Policy:\s*ro')

    $d2 = Ctl 'devices'
    $dev2 = $d2.data.devices | Where-Object { $_.ip -eq '127.0.0.1' } | Select-Object -First 1
    Check '设备策略显示为 ro' ($dev2.policy -eq 'ro') "got=$($dev2.policy)"
    Check '策略来源显示为 ip' ($dev2.policy_source -eq 'ip') "got=$($dev2.policy_source)"
    Check '拒绝计数已累加' ([int]$dev2.denied -ge 6) "got=$($dev2.denied)"

    # -----------------------------------------------------------------------
    Section '拒绝 (deny) 拦截一切'
    $null = Ctl 'acl.set' (@{ id = '127.0.0.1'; policy = 'deny' } | ConvertTo-Json -Compress)
    Check 'deny 时 GET -> 403' ((HttpCode GET "$Base/hello.txt") -eq '403')
    Check 'deny 时 PROPFIND -> 403' ((HttpCode PROPFIND "$Base/") -eq '403')
    Check 'deny 时 OPTIONS -> 403' ((HttpCode OPTIONS "$Base/") -eq '403')

    Section '恢复默认权限'
    $null = Ctl 'acl.set' (@{ id = '127.0.0.1'; policy = 'default' } | ConvertTo-Json -Compress)
    Check 'default 后 GET -> 200' ((HttpCode GET "$Base/hello.txt") -eq '200')
    Check 'default 后 PUT -> 2xx' ((HttpCode PUT "$Base/again.txt" @('--data-binary', 'ok')) -match '^2')
    $aclFile = Get-Content -Raw (Join-Path $State 'acl.json')
    Check 'default 后规则已落盘为空' ($aclFile -notmatch '127\.0\.0\.1')
    Check 'acl.json 存在且可解析' ($aclFile -match '"rules"')

    # -----------------------------------------------------------------------
    Section '全局只读开关'
    $null = Ctl 'config.set' (@{ readonly_global = $true } | ConvertTo-Json -Compress)
    Check '全局只读: GET -> 200' ((HttpCode GET "$Base/hello.txt") -eq '200')
    Check '全局只读: PUT -> 403' ((HttpCode PUT "$Base/global.bin" @('--data-binary', 'x')) -eq '403')
    Check '全局只读: DELETE -> 403' ((HttpCode DELETE "$Base/hello.txt") -eq '403')
    $null = Ctl 'config.set' (@{ readonly_global = $false } | ConvertTo-Json -Compress)
    Check '关闭全局只读后 PUT -> 2xx' ((HttpCode PUT "$Base/global.bin" @('--data-binary', 'x')) -match '^2')

    # -----------------------------------------------------------------------
    Section '账号密码认证透传'
    $null = Ctl 'config.set' (@{ auth_mode = 'password'; username = 'dufsbox'; password = 'secret123' } | ConvertTo-Json -Compress)
    Check '启用认证后匿名 GET -> 401' ((HttpCode GET "$Base/hello.txt") -eq '401')
    Check '正确凭据 GET -> 200' ((HttpCode GET "$Base/hello.txt" @('--anyauth', '-u', 'dufsbox:secret123')) -eq '200')
    Check '错误凭据 GET -> 401' ((HttpCode GET "$Base/hello.txt" @('--anyauth', '-u', 'dufsbox:wrong')) -eq '401')
    Check '正确凭据 PROPFIND -> 207' ((HttpCode PROPFIND "$Base/" @('--anyauth', '-u', 'dufsbox:secret123')) -eq '207')
    # The credential must not accidentally downgrade an authenticated client to
    # read-only: dufs's AccessPaths::merge defaults a path with no explicit
    # permission to read-only.
    Check '正确凭据 PUT -> 2xx (口令带 :rw)' (
        (HttpCode PUT "$Base/auth-write.txt" @('--data-binary', 'x', '--anyauth', '-u', 'dufsbox:secret123')) -match '^2')
    $stAuth = Ctl 'status'
    Check 'password_set 为 true' ($stAuth.data.config.password_set -eq $true)
    Check 'status 不回显口令' (($stAuth | ConvertTo-Json -Depth 8) -notmatch 'secret123')
    $badPw = Ctl 'config.set' (@{ password = 'has@/inside' } | ConvertTo-Json -Compress)
    Check '包含 @/ 的口令被拒绝' ($badPw.ok -eq $false)

    Section '恢复匿名访问 (供后续用例使用)'
    $null = Ctl 'config.set' (@{ auth_mode = 'anonymous'; password = '' } | ConvertTo-Json -Compress)
    Check '恢复匿名单后 GET -> 200' ((HttpCode GET "$Base/hello.txt") -eq '200')

    # -----------------------------------------------------------------------
    Section 'root 目录浏览 (browse)'
    $br = Ctl 'browse' (@{ path = $Share } | ConvertTo-Json -Compress)
    Check 'browse 返回 ok' ($br.ok -eq $true)
    # @() matters: PowerShell 5.1 gives a *single* match no usable .Count.
    $names = @($br.data.entries | ForEach-Object { $_.name })
    Check 'browse 列出 hello.txt' ($names -contains 'hello.txt') ("names=" + ($names -join ','))
    Check 'browse 把目录排在文件前面' ($br.data.entries[0].is_dir -eq $true) "first=$($br.data.entries[0].name)"
    $brBad = Ctl 'browse' (@{ path = 'relative/path' } | ConvertTo-Json -Compress)
    Check 'browse 拒绝相对路径' ($brBad.ok -eq $false)

    # -----------------------------------------------------------------------
    Section '分级日志'
    foreach ($lvl in @('trace', 'debug', 'info', 'warn', 'error')) {
        $lg = Ctl 'logs' (@{ level = $lvl; limit = 50 } | ConvertTo-Json -Compress)
        Check "logs level=$lvl 可用" ($lg.ok -eq $true)
    }
    $warnLogs = Ctl 'logs' (@{ level = 'warn'; limit = 200 } | ConvertTo-Json -Compress)
    $denials = @($warnLogs.data.entries | Where-Object { $_.msg -match '拒绝' })
    Check 'warn 及以上包含 ACL 拦截记录' ($denials.Count -ge 1)
    $errLogs = Ctl 'logs' (@{ level = 'error'; limit = 50 } | ConvertTo-Json -Compress)
    # dufs reports request outcomes as plain lines, so the meaningful assertion is
    # that nothing ever logged a panic/fatal.
    $fatal = @($errLogs.data.entries | Where-Object { $_.msg -match '(?i)panic|fatal' })
    Check '没有 panic/fatal 记录' ($fatal.Count -eq 0) "got=$($fatal -join ' | ')"
    $traceLogs = Ctl 'logs' (@{ level = 'trace'; limit = 5 } | ConvertTo-Json -Compress)
    Check 'trace 级别有逐请求记录' (@($traceLogs.data.entries).Count -ge 1)
    Check '日志条目字段完整' (
        @($traceLogs.data.entries | Where-Object { $_.ts -and $_.level -and $_.src -and $_.msg }).Count -ge 1)

    # -----------------------------------------------------------------------
    Section 'NDJSON 事件流 (subscribe)'
    # The body goes through a file: PowerShell 5.1 strips quotes from native-command
    # arguments, which would corrupt the JSON.
    $subBody = Join-Path $T 'subscribe.json'
    [System.IO.File]::WriteAllText($subBody, '{"verb":"subscribe"}', (New-Object System.Text.UTF8Encoding($false)))
    $sub = & curl.exe -s --max-time 3 "$CtlBase/rpc" -H 'Content-Type: application/json' --data-binary "@$subBody" 2>$null | Out-String
    Check 'subscribe 首帧为 snapshot' ($sub -match '"type":"snapshot"') ("head=" + $sub.Substring(0, [Math]::Min(120, $sub.Length)))
    Check 'subscribe 快照包含 devices' ($sub -match '"devices"')

    # -----------------------------------------------------------------------
    Section '配置热更新: 共享路径'
    $null = Ctl 'config.set' (@{ share_path = $Share2 } | ConvertTo-Json -Compress)
    $st2 = Ctl 'status'
    Check '共享路径已切换' ($st2.data.config.share_path -eq $Share2) "got=$($st2.data.config.share_path)"
    Check '新目录内容可访问' ((HttpCode GET "$Base/other.txt") -eq '200')
    Check '旧目录文件已不可见' ((HttpCode GET "$Base/hello.txt") -eq '404')
    $dufsPid = [int]$st2.data.dufs.pid
    $dufsCmd = ''
    if ($dufsPid -gt 0) {
        $dproc = Get-CimInstance Win32_Process -Filter "ProcessId = $dufsPid" -ErrorAction SilentlyContinue
        if ($dproc) { $dufsCmd = $dproc.CommandLine }
    }
    Check 'dufs 以新路径重启' ($dufsCmd -like "*$Share2*") "pid=$dufsPid cmd=$dufsCmd"

    # -----------------------------------------------------------------------
    Section '配置热更新: 端口'
    $null = Ctl 'config.set' (@{ port = $PublicPort2 } | ConvertTo-Json -Compress)
    Start-Sleep -Seconds 2
    $Base2 = "http://127.0.0.1:$PublicPort2"
    Check '新端口可访问' ((HttpCode GET "$Base2/other.txt") -eq '200')
    Check '旧端口已关闭' ((HttpCode GET "$Base/other.txt") -eq '000')

    # -----------------------------------------------------------------------
    Section '启停控制'
    $null = Ctl 'service.set' (@{ action = 'stop' } | ConvertTo-Json -Compress)
    Start-Sleep -Seconds 2
    $stopped = Ctl 'status'
    Check 'stop 后服务状态为 stopped' ($stopped.data.service.state -eq 'stopped') "got=$($stopped.data.service.state)"
    Check 'stop 后 dufs 已退出' ($stopped.data.dufs.running -eq $false)
    Check 'stop 后端口不再监听' ((HttpCode GET "$Base2/other.txt") -eq '000')
    Check 'stop 后守护进程仍在运行(可查询)' ($stopped.ok -eq $true)

    $null = Ctl 'service.set' (@{ action = 'start' } | ConvertTo-Json -Compress)
    Start-Sleep -Seconds 2
    $started = Ctl 'status'
    Check 'start 后服务状态为 running' ($started.data.service.state -eq 'running') "got=$($started.data.service.state)"
    Check 'start 后端口恢复' ((HttpCode GET "$Base2/other.txt") -eq '200')

    $null = Ctl 'service.set' (@{ action = 'restart' } | ConvertTo-Json -Compress)
    Start-Sleep -Seconds 2
    Check 'restart 后仍可访问' ((HttpCode GET "$Base2/other.txt") -eq '200')

    # -----------------------------------------------------------------------
    Section '开机自启开关 (autostart-check)'
    $rcOn = (Start-Process -FilePath $Daemon -ArgumentList @('autostart-check') -NoNewWindow -PassThru -Wait).ExitCode
    Check 'autostart=true 时退出码为 0' ($rcOn -eq 0) "got=$rcOn"
    $null = Ctl 'config.set' (@{ autostart = $false } | ConvertTo-Json -Compress)
    $rcOff = (Start-Process -FilePath $Daemon -ArgumentList @('autostart-check') -NoNewWindow -PassThru -Wait `
            -RedirectStandardOutput (Join-Path $T 'as.out') -RedirectStandardError (Join-Path $T 'as.err')).ExitCode
    Check 'autostart=false 时退出码非 0' ($rcOff -ne 0) "got=$rcOff"
    $null = Ctl 'config.set' (@{ autostart = $true } | ConvertTo-Json -Compress)

    # -----------------------------------------------------------------------
    Section '日志文件落盘'
    $daemonLog = Join-Path $State 'logs\dufsboxd.log'
    Check 'dufsboxd.log 已写入' (Test-Path $daemonLog)
    if (Test-Path $daemonLog) {
        $lines = Get-Content -LiteralPath $daemonLog
        Check '日志文件包含时间戳与级别' (($lines | Where-Object { $_ -match '^\d{4}-\d{2}-\d{2}T.*(INFO|WARN|DEBUG|TRACE|ERROR)' }).Count -ge 1)
        Check '日志文件包含 dufs 子进程输出' (($lines | Where-Object { $_ -match '\[dufs\]' }).Count -ge 1)
    }
}
finally {
    Section '清理'
    try { $null = Ctl 'service.set' (@{ action = 'stop' } | ConvertTo-Json -Compress) } catch { }
    if ($proc -and -not $proc.HasExited) { Stop-Process -Id $proc.Id -Force -ErrorAction SilentlyContinue }
    Start-Sleep -Milliseconds 500
    # dufs is a child of the daemon; make sure nothing survives the test.
    Get-Process -Name 'dufs' -ErrorAction SilentlyContinue | Where-Object { $_.Path -like "$T*" } | Stop-Process -Force -ErrorAction SilentlyContinue
    if ($Keep) {
        Write-Host "  kept: $T"
    } else {
        Remove-Item -Recurse -Force $T -ErrorAction SilentlyContinue
    }
}

Write-Host ''
if ($Failures -eq 0) {
    Write-Host ("ALL {0} CHECKS PASSED" -f $Checks) -ForegroundColor Green
    exit 0
}
Write-Host ("{0} of {1} CHECKS FAILED" -f $Failures, $Checks) -ForegroundColor Red
exit 1
