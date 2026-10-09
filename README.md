# DufsBox
> 中文 [README_ZH.md](./README_ZH.md) 

Turn **any directory** on your Android phone into a LAN web‑based file manager plus WebDAV share.
You can set access policy **per‑device**: `Deny` / `Read‑Only` / `Read‑Write`.
Tailscale is optional, giving you a stable private address independent of LAN or DHCP changes.

- Target Platform: **Android 16 (API 36) / arm64‑v8a / KernelSU**
- File Server: **Unmodified upstream [Dufs](https://github.com/sigoden/dufs) 0.46.0** (static musl binary)
- Control Panel: **Native Jetpack Compose + Material3 App**
- Extra Web Panel: **KernelSU Module WebUI** (pure HTML; no Android App required for management)

---

## 1. Architecture
```
       LAN Client ──┐
                    ├──▶  dufsboxd  :8080        ← The only public entry point
   Tailscale Client ──┘     (ACL proxy / device statistics / leveled logging / control API)
                                 │
                                 │ Loopback‑only traffic
                                 ▼
                          127.0.0.1:18080  dufs --allow-all   (listen on loopback interface only)
                                 │
                                 ▼
                   Arbitrary directories (e.g. /sdcard, /data/local/tmp, /)
```

### Why an intermediate `dufsboxd` layer exists
This is the core design decision which reference projects do not implement:

1. **Dufs lacks per‑client permission model.**
   `--auth` authorizes by `user + path`. Flags like `--readonly` / `--allow‑*` apply globally.
   To achieve granular rules for same shared folder: Device‑A read‑write, Device‑B read‑only, Device‑C denied;
   access validation **must happen before requests reach Dufs**.

2. **Therefore Dufs always starts with `--allow-all` and binds only to `127.0.0.1`.**
   All externally visible permission logic is enforced by `dufsboxd` based on source client device.
   Dufs cannot be reached from outside because it never opens any public network port.

3. Read‑only logic mirrors Dufs internal definition.
   Read‑only HTTP / WebDAV methods: `GET / HEAD / OPTIONS / PROPFIND / CHECKAUTH / LOGOUT`.
   Write methods: `PUT / DELETE / MKCOL / MOVE / COPY / PATCH / POST / PROPPATCH / LOCK …`.
   Implementation matches upstream `src/auth.rs` `is_readonly_method()`.
   Read‑only devices behave exactly same as Dufs global read‑only mode, including allowing WebDAV login (`CHECKAUTH`).

4. Control channel is not exposed over network.
   Android App invokes `dufsboxd ctl …` via root shell over a root‑owned Unix socket with file mode `0600`.
   This bypasses Android SELinux restrictions for `untrusted_app → root`.
   No LAN host can access control interfaces.

### Directory Layout
```
dufsbox/
├─ module/                  # KernelSU module content for flashing ZIP
│  ├─ module.prop  customize.sh  service.sh  action.sh  uninstall.sh
│  ├─ checksums.sha256      # SHA‑256 checksums for binaries, enforced on installation
│  ├─ bin/arm64/            # dufs, tailscale, tailscaled, dufsboxd binaries
│  └─ webroot/              # KernelSU Module WebUI static HTML assets
├─ daemon/                  # Go daemon source (dufsboxd) + unit tests
├─ app/                     # Android Application (Compose + Material3)
├─ tools/
│  ├─ build‑module.ps1      # One‑click build module ZIP
│  └─ verify‑local.ps1      # 96+ end‑to‑end local validation suite against real dufs
└─ docs/API.md              # App ↔ daemon API contract, single source of truth
```

---

## 2. Feature Matrix
| Requirement | Implementation Details |
|---|---|
| Target Android 16 / arm64‑v8a / KernelSU | `customize.sh` validates ABI & API level; `service.sh` leverages KernelSU boot service |
| Web file manager & WebDAV powered by Dufs | Upstream Dufs 0.46.0 used without patches |
| Share arbitrary filesystem paths | Daemon runs as root; App includes root‑aware directory browser for absolute path selection |
| Optional auto‑start on boot | `config.autostart` toggle; `service.sh` invokes `autostart‑check` to decide startup behaviour |
| ① Manage connected devices & permissions (Deny / Read‑Only / Read‑Write) | `dufsboxd` enforces policy by source IP / MAC. Device list shows online state, active connections, request counters, traffic stats, reject counters |
| ② Default LAN mode, optional Tailscale fixed private address | Default `mode=lan`. Select `tailscale` / `both`. Uses `tailscaled --tun=userspace‑networking`; no system VPN TUN slot consumed |
| ③ Compose Material3 UI with theming | Material You dynamic color, light / dark / system theme, accent color presets |
| ④ Bottom navigation tabs | Home / Logs / Status / Settings |
| WebUI Backend | Native Android App and KernelSU WebUI share identical `dufsboxd ctl` control interface |

---

## 3. Build
Windows PowerShell environment with pre‑installed toolchain: Go, Android SDK, Gradle.

```powershell
# 1. Fetch runtime binaries (dufs / tailscale) and verify official SHA‑256
powershell -ExecutionPolicy Bypass -File ..\.tools\fetch-runtime.ps1

# 2. Build full module ZIP (runs go vet / unit‑tests / arm64 cross‑compile / generate checksums / package & self‑test)
powershell -ExecutionPolicy Bypass -File tools\build-module.ps1

# Re‑compile daemon only, skip building Android App
powershell -ExecutionPolicy Bypass -File tools\build-module.ps1 -SkipApp
```

Build output: `dist/DufsBox‑v1.0.0‑arm64.zip` (~56 MiB, contains embedded APK).

### Build Toolchain Versions
| Component | Version | Notes |
|---|---|---|
| Go | 1.26.9 | `GOOS=android GOARCH=arm64 CGO_ENABLED=0`, **NDK not required** |
| Gradle | 9.6.1 | |
| Android Gradle Plugin | 9.4.1 | AGP9 ships Kotlin support; remove `org.jetbrains.kotlin.android` plugin |
| Kotlin | 2.2.10 | Brought by AGP 9.4.1 POM metadata; Compose compiler version must match |
| Compose BOM | 2026.09.00 | material3 / ui / material‑icons‑extended |
| JDK | 25 | For Android app compilation |
| Android SDK | platform **37.0** + build‑tools 37.0.0 | |

> Important compileSdk note: `compileSdk = 37`, `targetSdk = 36`.
> AndroidX / Compose libraries declare metadata requiring API‑37 compile environment.
> `targetSdk=36` enforces Android 16 runtime behaviour. Higher compileSdk value **does not change runtime behaviour**.

Generated APK uses debug keystore signature for direct installation.
For release build: configure `signingConfigs` inside `app/build.gradle.kts` and run `:app:assembleRelease`.

### Local End‑to‑end Validation (No Android device needed)
```powershell
powershell -ExecutionPolicy Bypass -File tools\verify-local.ps1
```
Spawn real `dufsboxd` and genuine `dufs` inside temporary directory.
Validates HTTP & WebDAV behaviour with 97 test cases.

---

## 4. Installation
1. Flash `dist/DufsBox‑v1.0.0‑arm64.zip` inside KernelSU manager then reboot device.
    - `customize.sh` validates SHA‑256 for four binaries. Installation aborts on checksum mismatch.
    - If ZIP includes embedded `DufsBox.apk`, installer attempts `pm install`. Manual APK install prompt appears upon failure.
2. Open KernelSU → Modules → DufsBox → WebUI; or launch DufsBox Android App.
3. Default runtime configuration: LAN anonymous read‑write access, `/sdcard` shared, port `8080`, auto‑start enabled.
4. To enable stable Tailscale private address: Settings → Sharing Mode → select Tailscale / Both. Navigate to Status tab and tap "Connect Tailscale". Open authorization link and login your Tailscale account. You will obtain `100.x.y.z` tailnet IP.
> Suggestion: visit [Tailscale Admin Machines](https://login.tailscale.com/admin/machines), set **Disable Key Expiry**, otherwise re‑authorization required roughly every 180 days.

### Client Access Methods
- **Web Browser**: Open displayed network address, you will see Dufs web file manager.
- **WebDAV**:
    - Windows: `rclone` + WinFsp map network drive or built‑in "Map Network Drive".
    - macOS: Finder → Connect to Server.
    - Media clients: Kodi / NPlayer / Solid‑Explorer input same WebDAV URL.
- After enabling username‑password authentication: clients receive HTTP 401, supply configured credentials.

---

## 5. Security Notes
- Dufs service **only listens on `127.0.0.1`**. Dufs port is never exposed on physical network interfaces. All external traffic must pass `dufsboxd` ACL policy layer.
- Control API is bound to root‑only Unix socket (file permission `0600`). Only root context can connect.
- Password values are never returned in API responses. `status` / `config.get` only report boolean `password_set: true`. Password shown in App UI comes from local application cache; it clearly reports "Not cached in App" when unavailable and will not invent fake credentials.
> ⚠️ Module holds full root privileges. Shared filesystem operations modify real device files. Always back‑up important data.
> Default setting: anonymous read‑write access. When connected to public Wi‑Fi any LAN participant can modify your files.
> Recommended hardening: enable account password, set global default policy to read‑only, or assign per‑device permissions.

---

## 6. Verified & Unverified Items
Please read carefully to understand project real‑world maturity.

### Verified locally on Windows host
Same daemon source code, ACL proxy and logging logic compiled for host OS, using real upstream dufs 0.46.0 backend.
`tools/verify‑local.ps1`: **97 test cases passed**.

| Group | Test Coverage |
|---|---|
| Version & Health | Version probing, dufs loopback‑only binding, shared path, protocol metadata, health‑check, redact plaintext passwords |
| Basic request passthrough | `GET 200`, `PROPFIND 207`, `OPTIONS`, `MKCOL` |
| Large‑file integrity | 20 MiB upload‑download round‑trip, full SHA‑256 match; confirm proxy preserves streaming |
| Device statistics | Device entries appear, default policy enforcement, request counter, inbound/outbound byte metrics |
| **Per‑device Read‑Only** | Read methods (`GET`/`PROPFIND`) succeed; write methods (`PUT`/`DELETE`/`MKCOL`/`COPY`/`MOVE`) return HTTP 403. No partial file writes on disk. Response header `X‑DufsBox‑Policy: ro` injected. |
| **Per‑device Deny** | All incoming requests (`GET` / `PROPFIND` / `OPTIONS`) get 403 Forbidden |
| Global read‑only toggle | Block write operations without blocking read operations; state toggle restores write permission |
| Authentication flows | Anonymous 401 challenge, valid credentials pass, wrong credentials reject, authenticated users respect write permissions, reject `@/` malformed auth input |
| Directory listing | Sort directories before files, block relative path traversal |
| Leveled logging | Five verbosity levels, warn level records access‑denied events, no application panic, trace log captures per‑request details |
| NDJSON event stream | Initial state snapshot with connected device list |
| Hot‑reload config | Changing share path / listening port takes effect immediately; old port closed, dufs restarts with new parameters |
| Service lifecycle | Stop: processes terminate and ports close, daemon keeps running; Start / Restart restore service |
| Boot autostart | `autostart` toggle correctly changes exit‑code for `autostart‑check` helper |
| Persisted log files | Timestamp + severity level, capture dufs subprocess output |

Additional pure‑logic unit‑tests via `go test`:
Read‑only method set parity with upstream Dufs, policy precedence (`MAC > IP > CIDR longest‑prefix > default`), CIDR sorting, configuration normalization, auth rule parsing, password validation, BOM handling, path sanitization.

Binary build artefact sanity‑check:
- `dufsboxd`: AArch64 PIE executable, interpreter `/system/bin/linker64` (Go `android/arm64` target, **not generic linux/arm64**).
- `dufs` / `tailscale` / `tailscaled`: static AArch64 standalone binaries.

Build‑time safeguards preventing hard‑to‑debug phone‑side bugs:
1. Android App compiles to valid `app‑debug.apk` (18.8 MiB), only deprecation warnings remain.
2. Module ZIP enforces forward‑slash `/` path separators. Build fails if backslash entries detected, avoids Android unzip misinterpretation bug introduced by old PowerShell `ZipFile.CreateFromDirectory`.
3. Port‑conflict diagnostic path validated: daemon logs expose real OS error code when backend cannot bind port, instead of ambiguous downstream HTTP failures.

### Important items NOT yet physically verified
No real Android hardware / emulator connected during current development (`adb devices` empty). These require acceptance testing on your physical device:
1. Real‑device runtime behaviour: arm64 binaries execution on Android 16, SELinux contexts, file execute permissions under `/data/adb`. Dufs documentation confirms static musl aarch64 binaries work on Android, but no hands‑on test done by author.
2. Boot‑time auto‑start: real execution timing of `service.sh` inside KernelSU boot service hook.
3. Android App UI rendering: Compose source compiles successfully, layout components never rendered on device/emulator. UI tweaks may be required.
4. Tailscale inbound connectivity: `tailscaled --tun=userspace‑networking` works on Android according community projects. However direct access to `100.x.y.z:8080` from other tailnet peers has not been validated. There is fallback toggle **Use HTTPS (tailscale serve)** inside settings, which uses tailscale built‑in reverse‑proxy (proven working in PhoneBridge). Enable fallback if direct IP connection fails.
5. MAC address collection: relies on `/proc/net/arp` with fallback `ip neigh`. Normally accessible under root. If kernel restricts these files, device identification falls back purely to IP address; rules will not survive DHCP lease renewal.

Suggested first‑time manual verification shell commands over adb root:
```sh
adb shell su -c '/data/adb/modules/dufsbox/bin/arm64/dufsboxd version'
adb shell su -c '/data/adb/modules/dufsbox/bin/arm64/dufsboxd ctl status'
adb shell su -c '/data/adb/modules/dufsbox/bin/arm64/dufsboxd ctl logs --json \'{"level":"trace","limit":20}\''
```

---

## 7. Relationship to Reference Projects
- **[ewiro/PhoneBridge](https://github.com/ewiro/PhoneBridge)** — Main structural reference.
  Adopted: KernelSU module layout conventions (`module.prop` / `customize.sh` / `service.sh`), binary SHA‑256 hardening, `tailscaled --tun=userspace‑networking` parameters, `SSL_CERT_DIR=/system/etc/security/cacerts` environment variable, loopback‑only backend pattern.
  Features **not present in PhoneBridge**: native LAN access, arbitrary custom shared directories, per‑device permission control, multi‑level logging, native Compose Android App.

- **[YAWAsau/Android‑_SMB_For_KSU](https://github.com/YAWAsau/Android‑_SMB_For_KSU)**
  References boot‑sequence practical knowledge: wait for `sys.boot_completed`, autostart toggle, module action buttons. It only implements SMB protocol, DufsBox focuses purely on WebDAV via dufs. It lacks per‑device granular permissions.

- **[sigoden/dufs](https://github.com/sigoden/dufs)** — File & WebDAV engine, zero source‑code modifications.
- **[tailscale](https://pkgs.tailscale.com/stable/#static)** — Optional tailnet private networking, official static arm64 release builds.

---

## 8. Third‑party Licenses
| Component | Version | License |
|---|---|---|
| Dufs | 0.46.0 | MIT OR Apache‑2.0 |
| Tailscale | 1.102.2 | BSD‑3‑Clause |

Official SHA‑256 hashes are hard‑coded within `.tools/fetch‑runtime.ps1` and validated during download.
`module/checksums.sha256` re‑validates binaries at KernelSU module flashing‑time.
Original DufsBox source code is available under **MIT License**.