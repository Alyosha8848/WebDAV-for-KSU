# DufsBox — 后端 ↔ App 接口契约 (v1)

本文件是 **唯一事实来源**。Go 守护进程 `dufsboxd` 与 Android Compose App 必须同时遵守本契约。

## 1. 组件与角色

| 组件 | 路径 | 说明 |
| --- | --- | --- |
| `dufsboxd` | `/data/adb/modules/dufsbox/bin/arm64/dufsboxd` | Go 单文件静态二进制。控制平面 + ACL 反向代理 + 进程守护 |
| `dufs` | `/data/adb/modules/dufsbox/bin/arm64/dufs` | 上游未修改的静态 musl 二进制，只监听 `127.0.0.1:<内部端口>` |
| `tailscaled` / `tailscale` | `/data/adb/modules/dufsbox/bin/arm64/` | 上游静态二进制，`--tun=userspace-networking` |
| 状态目录 | `/data/adb/dufsbox/` | `config.json`、`acl.json`、`run/dufsboxd.sock`、`logs/` |

数据流：

```
局域网客户端 ─┐
              ├─▶ dufsboxd :<port>  (ACL / 统计 / 分级日志)
Tailscale 客户端┘        │
                         └─▶ 127.0.0.1:<port+10000> dufs (--allow-all)
```

`dufs` 永远只绑定回环地址；所有对外可见的权限差异由 `dufsboxd` 按来源设备实施。

## 2. 命令行

```
dufsboxd serve                       # 守护进程（由模块 service.sh 调用）
dufsboxd launch                      # 后台拉起守护进程（App/WebUI 中“启动”使用）
dufsboxd ctl <verb> [--json <payload> | --json-stdin]
dufsboxd autostart-check             # 读 config.json，autostart=true 时 exit 0
dufsboxd version                     # 打印 {"version":..., "dufs":...}
```

`--json-stdin` 从标准输入读取 payload。**推荐在 App / WebUI / 脚本中使用它**：
把 JSON 塞进命令行很容易出问题 —— POSIX shell 需要单引号包裹，
而 Windows PowerShell 5.1 会直接把参数里的双引号吃掉，把合法 JSON 变成非法 JSON。
在 Android 上单引号是安全的（JSON 只用双引号），但 stdin 更省心。

`ctl` 的**每一条**命令都必须向 stdout 输出**恰好一个 JSON 对象**（换行结尾），
成功 `{"ok":true,"data":{...}}`，失败 `{"ok":false,"error":"人类可读信息"}`，
失败时退出码非 0。`subscribe` 例外：输出 NDJSON 流（每个事件一行一个 JSON 对象）。

App 通过 libsu 常驻 root shell 执行：

```
/data/adb/modules/dufsbox/bin/arm64/dufsboxd ctl status
```

## 3. 数据模型

### 3.1 权限策略 `policy`

| 值 | 语义 |
| --- | --- |
| `rw` | 可写：全部方法透传 |
| `ro` | 只读：仅允许 `GET HEAD OPTIONS PROPFIND CHECKAUTH LOGOUT`，其余方法返回 `403` |
| `deny` | 拒绝：任何方法立即 `403`（连接仍会被记录，便于在列表里看到"被拒绝的设备"） |
| `default` | 无显式规则，使用 `config.default_policy` |

只读方法集合与上游 dufs `is_readonly_method()` 完全一致。

### 3.2 设备 `Device`

```json
{
  "id": "aa:bb:cc:dd:ee:ff",
  "ip": "192.168.1.7",
  "mac": "aa:bb:cc:dd:ee:ff",
  "hostname": "laptop",
  "kind": "lan",
  "policy": "ro",
  "policy_source": "mac",
  "online": true,
  "active_conns": 2,
  "requests": 42,
  "bytes_in": 1048576,
  "bytes_out": 20971520,
  "denied": 3,
  "first_seen": "2026-01-01T10:00:00+08:00",
  "last_seen": "2026-01-01T10:05:00+08:00",
  "user_agent": "Mozilla/5.0 ...",
  "current_path": "/DCIM",
  "last_method": "PROPFIND",
  "last_status": 207
}
```

- `id`：有 MAC 时用 MAC，否则用 IP。ACL 与"忘记设备"都以它为主键。
- `kind`：`lan`（真实来源 IP）或 `tailnet`（经 Tailscale netstack 转发，来源恒为 `127.0.0.1`）。
- `policy_source`：`mac` / `ip` / `cidr` / `default`。
- `online`：最近 90 秒内有过连接，或有活跃连接。
- `mac` / `hostname` 可能为空字符串（ARP 表未命中 / 反解失败）。

### 3.3 配置 `Config`

```json
{
  "share_path": "/sdcard",
  "share_name": "DufsBox",
  "port": 8080,
  "mode": "lan",
  "auth_mode": "anonymous",
  "username": "dufsbox",
  "password": "",
  "default_policy": "rw",
  "readonly_global": false,
  "autostart": true,
  "log_level": "info",
  "tailscale": {
    "hostname": "dufsbox",
    "https_serve": false,
    "accept_dns": false,
    "login_server": "",
    "auth_key": ""
  }
}
```

- `mode`：`lan` | `tailscale` | `both`。
- `auth_mode`：`anonymous` | `password`。`password` 时给 dufs 传
  `--auth <user>:<pass>@/:rw`（`dufs` 的规则格式是 `<账号>@<路径>[:权限]`，
  按第一个字面量 `@/` 切分；**路径不带权限时默认只读**，所以 `:rw` 是必须的）。
  用户名或密码包含 `@/`、用户名包含 `:` 、任一方包含 `|` 时 `config.set` 会报错拒绝，
  因为这些字符无法在 dufs 规则里表达（而这会让 dufs 启动即退出）。
- `readonly_global`：全局只读开关（在代理层拦截，优先级高于设备策略）。
- `share_path`：**任意**目录，因为进程以 root 运行。
- `default_policy`：未知设备的默认策略。
- `password` 在 `status`/`config.get` 中**不回显**，只回 `password_set: true`。

## 4. 命令清单

### 4.1 `status`

```json
{"ok":true,"data":{
  "service":{"state":"running","uptime_sec":321,"pid":1234,"version":"1.0.0"},
  "dufs":{"running":true,"pid":1235,"version":"0.46.0","bind":"127.0.0.1:18080"},
  "tailscale":{"enabled":false,"state":"disabled","ip":"","dns":"","auth_url":"","version":"1.102.2","https_serve":false},
  "config":{"share_path":"/sdcard","share_name":"DufsBox","port":8080,"mode":"lan",
            "auth_mode":"anonymous","username":"dufsbox","password_set":false,
            "default_policy":"rw","readonly_global":false,"autostart":true,"log_level":"info"},
  "addresses":{"lan":["http://192.168.1.5:8080/"],"tailnet":[],"loopback":["http://127.0.0.1:8080/"]},
  "protocol":{"http":"1.1","webdav":"RFC 4918 (class 1)","dufs":"0.46.0"},
  "stats":{"devices":3,"online":1,"active_conns":2,"requests":120,"denied":4,"bytes_in":123456,"bytes_out":9876543},
  "health":{"share_path_exists":true,"share_path_writable":true,"port_bindable":true},
  "last_error":""
}}
```

`tailscale.state` ∈ `disabled` | `stopped` | `needs_login` | `online` | `offline` | `error`。
`needs_login` 时必须给出 `auth_url`（从 tailscaled 日志里抓 `https://login.tailscale.com/...`）。

### 4.2 `devices`

```json
{"ok":true,"data":{"devices":[ <Device>, ... ]}}
```

排序：在线优先，其次 `last_seen` 倒序。

### 4.3 `acl.set`

入参：`{"id":"<device id 或 ip>","policy":"rw|ro|deny|default"}`；可选 `"scope":"device|ip|cidr"` 与 `"cidr":"192.168.1.0/24"`。
`policy:"default"` 等于删除该规则。返回 `{"ok":true,"data":{"devices":[...]}}`（更新后的列表）。

### 4.4 `acl.remove`

入参：`{"id":"<device id>"}` → `{"ok":true}`

### 4.5 `device.forget`

入参：`{"id":"<device id>"}` → 从列表与统计中移除（不删除 ACL 规则）。

### 4.6 `service.set`

入参：`{"action":"start|stop|restart"}` → `{"ok":true,"data":{"state":"running"}}`
`stop` 会杀死 dufs/tailscaled 并让守护进程进入 disabled 状态（重启后由 `autostart` 决定）。

### 4.7 `config.get` / `config.set`

`config.set` 入参为**部分** Config 字段，返回 `{"ok":true,"data":{"config":{...},"restart_required":true}}`。
需要重启才生效的字段：`share_path`、`port`、`mode`、`auth_mode`、`username`、`password`、`readonly_global`。
不需要重启：`share_name`、`default_policy`、`autostart`、`log_level`、`tailscale.*`。

### 4.8 `logs`

入参：`{"level":"trace|debug|info|warn|error","limit":200,"since":0}`
`level` 表示**最低**级别（返回该级别及以上）。返回：

```json
{"ok":true,"data":{"entries":[{"ts":"2026-01-01T10:00:00.000+08:00","level":"info","src":"proxy","msg":"..."}]}}
```

### 4.9 `subscribe`

NDJSON 流，每行一个事件：

```json
{"type":"snapshot","data":{"status":{...},"devices":[...],"logs":[...]}}
{"type":"devices","data":{"devices":[...]}}
{"type":"status","data":{...}}
{"type":"log","data":{"ts":"...","level":"info","src":"proxy","msg":"..."}}
```

连接后立即推送一次 `snapshot`，随后在状态/设备变化时推送（设备统计类变化最多 1 秒一次），日志事件实时推送。

### 4.10 `browse`

入参：`{"path":"/sdcard"}`，返回：

```json
{"ok":true,"data":{"path":"/sdcard","parent":"/","exists":true,
  "entries":[{"name":"DCIM","path":"/sdcard/DCIM","is_dir":true,"size":0,"readable":true}]}}
```

只返回目录与文件的基本信息，目录在前、按名称排序。用于 App 内的 root 目录选择器。
`parent` 为 `""` 表示已到根。

### 4.11 `tailscale.set`

入参：`{"action":"up|down|logout|serve_on|serve_off"}` → `{"ok":true,"data":{"state":"..."}}`

### 4.12 `version`

`{"ok":true,"data":{"version":"1.0.0","dufs":"0.46.0","go":"go1.26.9","protocol":1}}`

## 5. 事件与刷新

`subscribe` 由 CLI / KernelSU WebUI 使用。**App 采用轮询**，实现更简单且足够实时：

- 首页设备列表 + 顶部开关：每 **2 秒** `devices`。
- 状态页：每 **2 秒** `status`。
- 日志页：切换级别时 `logs` 拉取一次，之后每 **2 秒**用 `since=<最后一条时间戳>` 增量拉取。

## 6. App 侧硬性要求

- Kotlin + Jetpack Compose + Material3，`minSdk 26`、`targetSdk 36`、`compileSdk 36`。
- 底部导航 `NavigationBar` 四栏：**首页 / 日志 / 状态 / 设置**。
- root 访问统一走 libsu `com.github.topjohnwu.libsu:core`，常驻一个 root shell 执行 `ctl`；
  **不要**使用 `:service` 模块，**不要**使用 `subscribe` 流。
- 非 root 或模块缺失时给出明确引导文案，不崩溃。
- 主题：Material You 动态取色（Android 12+）、浅色/深色/跟随系统、若干预设强调色。
- 所有界面文案使用简体中文。
