# DufsBox
> English version: [README.md](./README.md)

把 Android 手机上的**任意目录**变成局域网里的网页文件管理器 + WebDAV 共享，
并且可以**按设备**分别设置「拒绝 / 只读 / 可写」。可选启用 Tailscale，
用一个不随局域网或 DHCP 变化的私有地址访问。

- 目标平台：**Android 16 (API 36) / arm64-v8a / KernelSU**
- 文件服务：**上游未修改的 [Dufs](https://github.com/sigoden/dufs) 0.46.0**（静态 musl 二进制）
- 控制面板：**原生 Jetpack Compose + Material3 App**
- 附加面板：**KernelSU 模块 WebUI**（纯 HTML，不用装 App 也能管理）

---

## 1. 架构

```
       局域网客户端 ──┐
                     ├──▶  dufsboxd  :8080        ← 唯一对外入口
   Tailscale 客户端 ──┘     （ACL 代理 / 设备统计 / 分级日志 / 控制接口）
                                 │
                                 │  仅回环
                                 ▼
                          127.0.0.1:18080  dufs --allow-all   （只监听环回）
                                 │
                                 ▼
                          任意目录（例如 /sdcard、/data/local/tmp、/）
```

### 为什么中间要加一层 `dufsboxd`

这是整个方案的关键决定，也是两个参考项目都没做的事：

1. **Dufs 没有「按客户端」的权限概念。**
   `--auth` 是按「用户 + 路径」授权的，`--readonly` / `--allow-*` 是全局的。
   要做到「同一个共享目录，A 电脑可写、B 手机只读、C 设备拒绝」，
   必须在请求到达 Dufs 之前做判断。

2. **所以 Dufs 永远以 `--allow-all` 启动，只绑定 `127.0.0.1`。**
   对外可见的权限差异全部由 `dufsboxd` 按来源设备实施；
   `dufs` 自己不可能被绕过访问，因为它根本没有对外监听。

3. **只读的判定完全照抄 Dufs 的内部定义。**
   `GET / HEAD / OPTIONS / PROPFIND / CHECKAUTH / LOGOUT` 视为读操作，
   其余方法（`PUT / DELETE / MKCOL / MOVE / COPY / PATCH / POST / PROPPATCH / LOCK …`）视为写操作。
   与上游 `src/auth.rs` 的 `is_readonly_method()` 一一对应，
   所以「只读设备」看到的行为和 Dufs 自己的全局只读是一致的 ——
   包括不会把 WebDAV 登录（`CHECKAUTH`）也一起挡掉。

4. **控制通道不在网络上。** App 通过 root shell 调用 `dufsboxd ctl …`，
   走一个 `0600`、属主为 root 的 Unix socket，
   既避开了 Android 对 `untrusted_app → root` 的 SELinux 限制，
   也不存在「局域网里有人能调控制接口」的问题。

### 目录结构

```
dufsbox/
├─ module/                  KernelSU 模块（刷入手机的 ZIP 内容）
│  ├─ module.prop  customize.sh  service.sh  action.sh  uninstall.sh
│  ├─ checksums.sha256      四个二进制的 SHA-256，安装时强校验
│  ├─ bin/arm64/            dufs、tailscale、tailscaled、dufsboxd
│  └─ webroot/              KernelSU 模块 WebUI
├─ daemon/                  Go 控制守护进程（dufsboxd）源码 + 单元测试
├─ app/                     Android App（Compose + Material3）
├─ tools/
│  ├─ build-module.ps1      一条命令出模块 ZIP
│  └─ verify-local.ps1      96 项端到端验证（真 dufs，本机跑）
└─ docs/API.md              App ↔ 守护进程 接口契约（唯一事实来源）
```

---

## 2. 功能对照

| 你的要求 | 实现方式 |
| --- | --- |
| 面向 Android 16 / armv8a / KernelSU | `customize.sh` 校验 ABI 与 API，`service.sh` 走 KernelSU 的 boot service |
| 用 Dufs 提供网页文件管理器 + WebDAV | 上游 Dufs 0.46.0 原样使用，未打补丁 |
| 任意目录 | 守护进程以 root 运行；App 内置 **root 目录浏览器**，可选任意绝对路径 |
| 开机自启动**可选** | `config.autostart`；`service.sh` 调 `autostart-check` 决定是否拉起 |
| ① 实时管理连接设备、连接权限、拒绝 / 只读 / 可写 | `dufsboxd` 按来源 IP/MAC 实施策略；设备列表含在线状态、活跃连接、请求数、上下行流量、被拒绝次数 |
| ② 默认局域网，可选 Tailscale 固定私有地址 | 默认 `mode=lan`；可选 `tailscale` / `both`，跑 `tailscaled --tun=userspace-networking`（不需要 TUN，也不占用系统唯一的 VPN 槽位） |
| ③ Compose + Material3 + 美化选项 | 动态取色（Material You）、浅色/深色/跟随系统、多组预设强调色 |
| ④ 底栏四栏 | 首页 / 日志 / 状态 / 设置，见下节 |
| WebUI 后端 | 原生 App + KernelSU 模块 WebUI 两种前端共用同一个 `dufsboxd ctl` 接口 |
---

## 3. 构建

需要 Windows PowerShell 与已下载的工具链（Go、Android SDK、Gradle）。

```powershell
# 1. 取运行期二进制（dufs / tailscale），并校验官方 SHA-256
powershell -ExecutionPolicy Bypass -File ..\.tools\fetch-runtime.ps1

# 2. 一条命令出模块 ZIP（内部会跑 go vet / go test / arm64 交叉编译 / 生成校验和 / 打包并自检）
powershell -ExecutionPolicy Bypass -File tools\build-module.ps1

# 只重新编译守护进程、不打包 App
powershell -ExecutionPolicy Bypass -File tools\build-module.ps1 -SkipApp
```

产物：`dist/DufsBox-v1.0.0-arm64.zip`（约 56 MB，含 App）

### 实际使用的构建工具链

| 组件 | 版本 | 说明 |
| --- | --- | --- |
| Go | 1.26.9 | `GOOS=android GOARCH=arm64 CGO_ENABLED=0`，**不需要 NDK** |
| Gradle | 9.6.1 | |
| Android Gradle Plugin | 9.4.1 | AGP 9 自带 Kotlin 支持，**必须去掉** `org.jetbrains.kotlin.android` 插件 |
| Kotlin | 2.2.10 | 由 AGP 9.4.1 带入（见其 POM）；Compose 编译器插件版本必须与之相同 |
| Compose BOM | 2026.09.00 | material3 / ui / material-icons-extended |
| JDK | 25 | 构建 App 用 |
| Android SDK | platform **37.0** + build-tools 37.0.0 | |

关于 `compileSdk`：**`compileSdk = 37` 而 `targetSdk = 36`**。
这是刻意分开的两件事 —— 当前的 AndroidX / Compose（core 1.19.1、compose 1.12.1）
在自己的 AAR 元数据里声明「需要以 API 37 或更高编译」，所以编译期必须用 37；
而 `targetSdk = 36` 才是「按 Android 16 的运行时行为运行」，
也就是本项目面向的平台。提高 `compileSdk` 不改变任何运行时行为。

产物是一个 **debug 签名**的 APK（用构建机上的 debug keystore 签名，可直接安装）。
要做正式发布版，请在 `app/build.gradle.kts` 里加 `signingConfigs` 并跑 `:app:assembleRelease`。

### 本机端到端验证（不需要手机）

```powershell
powershell -ExecutionPolicy Bypass -File tools\verify-local.ps1
```

它会在临时目录里跑**真实的 `dufsboxd` + 真实的 `dufs`**，
用真实的 HTTP/WebDAV 请求验证 96 项行为（见第 6 节）。

---

## 4. 安装

1. 把 `dist/DufsBox-v1.0.0-arm64.zip` 在 KernelSU 管理器里刷入并重启。
   - `customize.sh` 会校验四个二进制的 SHA-256，任一不符直接中止安装。
   - ZIP 里如果带了 `DufsBox.apk`，安装时会尝试 `pm install`，失败则提示手动安装。
2. 打开 KernelSU → 模块 → DufsBox → WebUI，或打开 DufsBox App。
3. 默认已按「局域网 + 匿名 + 可写 + `/sdcard` + 端口 8080 + 开机自启」运行。
4. 想用固定私有地址：设置 → 共享方式 → Tailscale（或两者）→ 回到状态页点「连接 Tailscale」，
   打开授权链接登录同一账号。之后会显示 `100.x.y.z` 地址。
   建议到 [Tailscale 设备管理](https://login.tailscale.com/admin/machines) 给这台设备
   **Disable Key Expiry**，否则默认约 180 天后要重新授权。

### 客户端怎么连

- **浏览器**：直接打开状态页显示的地址，就是 Dufs 的网页文件管理器。
- **WebDAV**：Windows 用 `rclone` + WinFsp 映射盘符，或系统自带「映射网络驱动器」；
  macOS 用 Finder「连接服务器」；Kodi / Nplayer / Solid Explorer 等填同一个地址即可。
- 开了账号密码后，客户端会收到 401，填设置页里的用户名与密码即可。

---

## 5. 安全性

- Dufs **只监听 `127.0.0.1`**，物理网卡上永远没有 Dufs 的端口；
  局域网里的其他人只能通过 `dufsboxd`，也就必然经过设备策略。
- 控制接口是 `0600` 的 Unix socket，只有 root（也就是 KernelSU 的 `su`）能连。
- 密码在接口里**永不回显**，`status` / `config.get` 只返回 `password_set: true`。
  状态页显示的密码取自 App 本地缓存；没缓存时它老实说「未在 App 中缓存」，不会编一个值出来。
- 模块拥有 root 权限。虽然共享目录由你指定，删除/移动仍然作用在真实文件上，**先备份**。
- 默认匿名可写，等于「同一个 Wi-Fi 下谁都能改你的文件」。
  公用网络下建议：改成账号密码，或把 `default_policy` 改成只读，或按设备授权。

---

## 6. 已经验证了什么 / 还没验证什么

这一节请务必读完，它决定了你现在能信多少。

### 已经在本机（Windows）跑通并有证据的

守护进程是**同一份源码**、同样的 ACL 代理与日志逻辑，只是编译到 host 上运行，
用真实的 `dufs 0.46.0` 作为后端。`tools/verify-local.ps1` **97 项全部通过**：

| 组 | 覆盖内容 |
| --- | --- |
| 版本与状态 | 版本探测、`dufs` 只绑回环、共享路径、协议信息、健康检查、口令不回显 |
| 基础透传 | `GET /` 200、`GET` 200、`PROPFIND` 207、`OPTIONS`、`MKCOL` |
| 大文件完整性 | 20 MiB 上传后回流，**SHA-256 完全一致**（代理不破坏流式传输） |
| 设备与统计 | 设备出现在列表、默认策略、请求数、上下行字节数 |
| **按设备只读** | `GET`/`PROPFIND` 仍 200/207；`PUT`/`DELETE`/`MKCOL`/`COPY`/`MOVE` **全部 403**；被拒的文件确实没落盘、原文件确实没被删；响应带 `X-DufsBox-Policy: ro` |
| **拒绝** | `GET`/`PROPFIND`/`OPTIONS` 全部 403 |
| 全局只读 | 只挡写、不挡读；关闭后恢复可写 |
| 认证 | 匿名 401、正确凭据 200/207、错误凭据 401、**认证用户仍可写**、`@/` 口令被拒 |
| 目录浏览 | 列出内容、目录排在文件前、拒绝相对路径 |
| 分级日志 | 五档级别都可用、warn 级含拦截记录、无 panic/fatal、trace 有逐请求记录 |
| NDJSON 事件流 | 首帧 snapshot、含 devices |
| 配置热更新 | 改共享路径立即生效（旧路径 404）、改端口立即生效（旧端口关闭）、`dufs` 以新路径重启 |
| 启停控制 | stop 后进程退出且端口关闭但守护进程仍在、start 恢复、restart 恢复 |
| 开机自启 | `autostart` 开关正确影响 `autostart-check` 退出码 |
| 日志落盘 | 时间戳+级别、包含 dufs 子进程输出 |

另有 `go test` 单元测试覆盖纯逻辑：只读方法集合与 Dufs 一致、策略优先级
（MAC > IP > 最长前缀 CIDR > 默认）、CIDR 排序、配置归一化、`--auth` 规则格式、
口令校验、BOM 容错、路径处理等。

二进制架构也已核对：`dufsboxd` 是 **AArch64 PIE，解释器 `/system/bin/linker64`**
（即 Go 的 `android/arm64` 目标，而不是 linux/arm64）；
`dufs` / `tailscale` / `tailscaled` 都是 **AArch64 静态可执行**。

另外两件在构建期就验证掉、否则会以「莫名其妙的现象」出现在手机上的事：

- **App 能编译出 APK**：`app-debug.apk`（18.8 MB）编译通过，只剩弃用告警。
- **模块 ZIP 的路径分隔符是 `/`**：PowerShell 5.1（.NET Framework）的
  `ZipFile.CreateFromDirectory` 会写入 `bin\arm64\dufs` 这种**反斜杠**条目，
  Android 的 `unzip` 会把它解成一个名字里带反斜杠的文件而不是目录树，模块装上去就是坏的。
  `build-module.ps1` 现在手写 ZIP 条目，并且**直接读原始中央目录名**做校验
  （而不是解压后再比较 —— 解压会把分隔符归一化，正好掩盖这个问题），
  发现反斜杠就直接让构建失败。
- **守护进程能正确报告端口被占用**：验证脚本会在启动后主动确认 `dufs` 后端真的起来了，
  起不来就打印守护进程日志并立刻失败。这条诊断路径本身也被实测过 ——
  它抓到过一次「`dufs` 绑定 `127.0.0.1:18370` 失败、`os error 10013`」，
  原因是 Windows 为 Hyper-V/WSL 保留了 `18293-18392` 整段端口
  （`netsh int ipv4 show excludedportrange protocol=tcp`），
  而验证脚本当时只探测了对外端口、没探测推导出来的内部端口。
  Android 上没有这种端口保留；这条记录的价值在于：**后端起不来时，你会在一行里看到真正的原因**，
  而不是几十个看起来互不相关的请求失败。

### 还没有验证的（重要）

**本次没有连接任何 Android 设备**（`adb devices` 为空），所以以下只能在你的手机上验收：

1. **真机运行**：arm64 二进制在 Android 16 上的实际执行、SELinux 上下文、`/data/adb` 下的执行权限。
   Dufs 官方明确说过 `aarch64-unknown-linux-musl` 可用于 Android/arm64，参考项目也这么做，但**我没有真机跑过**。
2. **开机自启动**：`service.sh` 在 KernelSU boot service 阶段的实际行为与等待时机。
3. **App 界面**：Compose 代码已写完并通过编译，但**没有在设备/模拟器上渲染过**，
   布局细节（分段按钮、图标可用性）可能需要微调。
4. **Tailscale 入站可达性**：`tailscaled --tun=userspace-networking` 本身在 Android 上可用
   （参考项目与社区实践都验证过），但「外部 tailnet 设备直连 `100.x.y.z:8080` 是否被 netstack 转发到本机回环」
   我**没能实测**。因此设置里同时提供了 **「使用 HTTPS (tailscale serve)」** 开关：
   它让 tailscaled 自己做 HTTPS 反代，这条路是确定可行的（PhoneBridge 就是这么做的）。
   如果直连 IP 不通，打开这个开关即可。
5. **MAC 地址显示**：依赖 `/proc/net/arp`（回退 `ip neigh`）。root 下通常可读；
   若你的内核受限，设备列表会退化为按 IP 识别（功能不受影响，只是规则不能跨 DHCP 续约保持）。

建议的首次真机验收顺序：

```sh
adb shell su -c '/data/adb/modules/dufsbox/bin/arm64/dufsboxd version'
adb shell su -c '/data/adb/modules/dufsbox/bin/arm64/dufsboxd ctl status'
adb shell su -c '/data/adb/modules/dufsbox/bin/arm64/dufsboxd ctl logs --json \'{"level":"trace","limit":20}\''
```

---

## 7. 与参考项目的关系

- **[ewiro/PhoneBridge](https://github.com/ewiro/PhoneBridge)** —— 本项目的骨架参考。
  沿用了它的：KernelSU 模块布局与 `module.prop`/`customize.sh`/`service.sh`/`action.sh` 约定、
  二进制 SHA-256 锁定与安装期校验、`tailscaled --tun=userspace-networking` 的参数与
  `SSL_CERT_DIR=/system/etc/security/cacerts`（Android 的 CA 目录，不设会导致控制面 TLS 失败）、
  以及「只监听 127.0.0.1、由网关上收」的思路。
  **它没有的**：局域网访问（它只走 Tailscale）、任意目录（写死 `/sdcard`）、
  按设备权限、分级日志、原生 App。
- **[YAWAsau/Android-_SMB_For_KSU](https://github.com/YAWAsau/Android-_SMB_For_KSU)** ——
  参考了它的开机流程经验（等待 `sys.boot_completed` 再启动、自启开关、Action 按钮、
  「只有全局只读、没有按设备权限」的现状也正好说明了本项目的差异点）。
  它只做 SMB，本项目只做 WebDAV（由 Dufs 提供）。
- **[sigoden/dufs](https://github.com/sigoden/dufs)** —— 文件服务与 WebDAV 引擎，**未做任何修改**。
- **[tailscale](https://pkgs.tailscale.com/stable/#static)** —— 可选私有地址，官方静态 arm64 包。

## 8. 第三方许可

| 组件 | 版本 | 许可 |
| --- | --- | --- |
| Dufs | 0.46.0 | MIT OR Apache-2.0 |
| Tailscale | 1.102.2 | BSD-3-Clause |

两个归档的官方 SHA-256 记录在 `.tools/fetch-runtime.ps1` 中，下载时会强校验；
`module/checksums.sha256` 会在刷入时再次校验四个二进制。
DufsBox 自有代码使用 MIT License。