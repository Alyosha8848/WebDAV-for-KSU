package com.dufsbox.app.data.model

import com.dufsbox.app.data.arr
import com.dufsbox.app.data.bool
import com.dufsbox.app.data.int
import com.dufsbox.app.data.long
import com.dufsbox.app.data.obj
import com.dufsbox.app.data.str
import org.json.JSONObject

/* ------------------------------------------------------------------------------------------- */
/* Enumerations from docs/API.md §3.1 / §3.2                                                    */
/* ------------------------------------------------------------------------------------------- */

/** ACL policy values (§3.1). */
enum class Policy(val wire: String, val label: String) {
    DENY("deny", "拒绝"),
    RO("ro", "只读"),
    RW("rw", "可写"),
    DEFAULT("default", "默认"),
    ;

    companion object {
        fun from(v: String?): Policy =
            entries.firstOrNull { it.wire.equals(v?.trim(), ignoreCase = true) } ?: DEFAULT

        /** The three states the home-screen per-device selector offers. */
        val selectable: List<Policy> = listOf(DENY, RO, RW)
    }
}

/** Device origin (§3.2). */
enum class DeviceKind(val wire: String, val label: String) {
    LAN("lan", "局域网"),
    TAILNET("tailnet", "Tailscale"),
    ;

    companion object {
        fun from(v: String?): DeviceKind =
            entries.firstOrNull { it.wire.equals(v?.trim(), ignoreCase = true) } ?: LAN
    }
}

/* ------------------------------------------------------------------------------------------- */
/* §3.2 Device                                                                                  */
/* ------------------------------------------------------------------------------------------- */

data class Device(
    val id: String = "",
    val ip: String = "",
    val mac: String = "",
    val hostname: String = "",
    val kind: DeviceKind = DeviceKind.LAN,
    val policy: Policy = Policy.DEFAULT,
    val policySource: String = "default",
    val online: Boolean = false,
    val activeConns: Int = 0,
    val requests: Long = 0L,
    val bytesIn: Long = 0L,
    val bytesOut: Long = 0L,
    val denied: Long = 0L,
    val firstSeen: String = "",
    val lastSeen: String = "",
    val userAgent: String = "",
    val currentPath: String = "",
    val lastMethod: String = "",
    val lastStatus: Int = 0,
) {
    /** Primary label: hostname when known, otherwise the IP. */
    val label: String get() = hostname.ifBlank { ip.ifBlank { id } }

    val isDefaultPolicy: Boolean get() = policySource == "default"

    companion object {
        fun from(json: JSONObject?): Device {
            if (json == null) return Device()
            return Device(
                id = json.str("id"),
                ip = json.str("ip"),
                mac = json.str("mac"),
                hostname = json.str("hostname"),
                kind = DeviceKind.from(json.str("kind")),
                policy = Policy.from(json.str("policy")),
                policySource = json.str("policy_source", "default"),
                online = json.bool("online"),
                activeConns = json.int("active_conns"),
                requests = json.long("requests"),
                bytesIn = json.long("bytes_in"),
                bytesOut = json.long("bytes_out"),
                denied = json.long("denied"),
                firstSeen = json.str("first_seen"),
                lastSeen = json.str("last_seen"),
                userAgent = json.str("user_agent"),
                currentPath = json.str("current_path"),
                lastMethod = json.str("last_method"),
                lastStatus = json.int("last_status"),
            )
        }

        fun listFrom(json: JSONObject?): List<Device> {
            val array = json.arr("devices")
            val out = ArrayList<Device>(array.length())
            for (i in 0 until array.length()) out += from(array.optJSONObject(i))
            return out
        }
    }
}

/* ------------------------------------------------------------------------------------------- */
/* §4.1 status → nested objects                                                                 */
/* ------------------------------------------------------------------------------------------- */

data class ServiceInfo(
    val state: String = "unknown",
    val uptimeSec: Long = 0L,
    val pid: Int = 0,
    val version: String = "",
) {
    val running: Boolean get() = state.equals("running", ignoreCase = true)

    companion object {
        fun from(json: JSONObject?): ServiceInfo {
            if (json == null) return ServiceInfo()
            return ServiceInfo(
                state = json.str("state", "unknown"),
                uptimeSec = json.long("uptime_sec"),
                pid = json.int("pid"),
                version = json.str("version"),
            )
        }
    }
}

data class DufsInfo(
    val running: Boolean = false,
    val pid: Int = 0,
    val version: String = "",
    val bind: String = "",
) {
    companion object {
        fun from(json: JSONObject?): DufsInfo {
            if (json == null) return DufsInfo()
            return DufsInfo(
                running = json.bool("running"),
                pid = json.int("pid"),
                version = json.str("version"),
                bind = json.str("bind"),
            )
        }
    }
}

data class TailscaleStatus(
    val enabled: Boolean = false,
    val state: String = "disabled",
    val ip: String = "",
    val dns: String = "",
    val authUrl: String = "",
    val version: String = "",
    val httpsServe: Boolean = false,
) {
    val needsLogin: Boolean get() = state.equals("needs_login", ignoreCase = true)
    val online: Boolean get() = state.equals("online", ignoreCase = true)

    val stateLabel: String
        get() = when (state.lowercase()) {
            "disabled" -> "未启用"
            "stopped" -> "已停止"
            "needs_login" -> "需要登录"
            "online" -> "在线"
            "offline" -> "离线"
            "error" -> "错误"
            else -> state.ifBlank { "未知" }
        }

    companion object {
        fun from(json: JSONObject?): TailscaleStatus {
            if (json == null) return TailscaleStatus()
            return TailscaleStatus(
                enabled = json.bool("enabled"),
                state = json.str("state", "disabled"),
                ip = json.str("ip"),
                dns = json.str("dns"),
                authUrl = json.str("auth_url"),
                version = json.str("version"),
                httpsServe = json.bool("https_serve"),
            )
        }
    }
}

/**
 * `status.config` — the non-secret projection of `config.json`. `password` is never
 * echoed by the daemon; only `password_set` is reported (§3.3).
 */
data class ConfigInfo(
    val sharePath: String = "",
    val shareName: String = "DufsBox",
    val port: Int = 0,
    val mode: String = "lan",
    val authMode: String = "anonymous",
    val username: String = "",
    val passwordSet: Boolean = false,
    val defaultPolicy: Policy = Policy.RW,
    val readonlyGlobal: Boolean = false,
    val autostart: Boolean = false,
    val logLevel: String = "info",
    val tailscaleHostname: String = "",
    val tailscaleHttpsServe: Boolean = false,
    val tailscaleAcceptDns: Boolean = false,
    val tailscaleLoginServer: String = "",
    val tailscaleAuthKeySet: Boolean = false,
) {
    val isPasswordAuth: Boolean get() = authMode.equals("password", ignoreCase = true)
    val isAnonymous: Boolean get() = !isPasswordAuth

    val modeLabel: String
        get() = when (mode.lowercase()) {
            "lan" -> "仅局域网"
            "tailscale" -> "仅 Tailscale"
            "both" -> "局域网 + Tailscale"
            else -> mode.ifBlank { "未知" }
        }

    companion object {
        fun from(json: JSONObject?): ConfigInfo {
            if (json == null) return ConfigInfo()
            val ts = json.obj("tailscale")
            return ConfigInfo(
                sharePath = json.str("share_path"),
                shareName = json.str("share_name", "DufsBox"),
                port = json.int("port"),
                mode = json.str("mode", "lan"),
                authMode = json.str("auth_mode", "anonymous"),
                username = json.str("username"),
                passwordSet = json.bool("password_set"),
                defaultPolicy = Policy.from(json.str("default_policy")),
                readonlyGlobal = json.bool("readonly_global"),
                autostart = json.bool("autostart"),
                logLevel = json.str("log_level", "info"),
                tailscaleHostname = ts.str("hostname"),
                tailscaleHttpsServe = ts.bool("https_serve"),
                tailscaleAcceptDns = ts.bool("accept_dns"),
                tailscaleLoginServer = ts.str("login_server"),
                tailscaleAuthKeySet = ts.str("auth_key").isNotBlank(),
            )
        }
    }
}

data class Addresses(
    val lan: List<String> = emptyList(),
    val tailnet: List<String> = emptyList(),
    val loopback: List<String> = emptyList(),
) {
    /** Everything a client on the LAN could type into a browser. */
    val all: List<String> get() = lan + tailnet

    companion object {
        fun from(json: JSONObject?): Addresses {
            if (json == null) return Addresses()
            val lan = json.arr("lan").toStringList()
            val tailnet = json.arr("tailnet").toStringList()
            val loopback = json.arr("loopback").toStringList()
            return Addresses(lan = lan, tailnet = tailnet, loopback = loopback)
        }
    }
}

data class ProtocolInfo(
    val http: String = "",
    val webdav: String = "",
    val dufs: String = "",
) {
    companion object {
        fun from(json: JSONObject?): ProtocolInfo {
            if (json == null) return ProtocolInfo()
            return ProtocolInfo(
                http = json.str("http"),
                webdav = json.str("webdav"),
                dufs = json.str("dufs"),
            )
        }
    }
}

data class StatsInfo(
    val devices: Long = 0L,
    val online: Long = 0L,
    val activeConns: Long = 0L,
    val requests: Long = 0L,
    val denied: Long = 0L,
    val bytesIn: Long = 0L,
    val bytesOut: Long = 0L,
) {
    companion object {
        fun from(json: JSONObject?): StatsInfo {
            if (json == null) return StatsInfo()
            return StatsInfo(
                devices = json.long("devices"),
                online = json.long("online"),
                activeConns = json.long("active_conns"),
                requests = json.long("requests"),
                denied = json.long("denied"),
                bytesIn = json.long("bytes_in"),
                bytesOut = json.long("bytes_out"),
            )
        }
    }
}

data class HealthInfo(
    val sharePathExists: Boolean = false,
    val sharePathWritable: Boolean = false,
    val portBindable: Boolean = false,
) {
    companion object {
        fun from(json: JSONObject?): HealthInfo {
            if (json == null) return HealthInfo()
            return HealthInfo(
                sharePathExists = json.bool("share_path_exists"),
                sharePathWritable = json.bool("share_path_writable"),
                portBindable = json.bool("port_bindable"),
            )
        }
    }
}

data class Status(
    val service: ServiceInfo = ServiceInfo(),
    val dufs: DufsInfo = DufsInfo(),
    val tailscale: TailscaleStatus = TailscaleStatus(),
    val config: ConfigInfo = ConfigInfo(),
    val addresses: Addresses = Addresses(),
    val protocol: ProtocolInfo = ProtocolInfo(),
    val stats: StatsInfo = StatsInfo(),
    val health: HealthInfo = HealthInfo(),
    val lastError: String = "",
) {
    companion object {
        fun from(json: JSONObject?): Status {
            if (json == null) return Status()
            return Status(
                service = ServiceInfo.from(json.obj("service")),
                dufs = DufsInfo.from(json.obj("dufs")),
                tailscale = TailscaleStatus.from(json.obj("tailscale")),
                config = ConfigInfo.from(json.obj("config")),
                addresses = Addresses.from(json.obj("addresses")),
                protocol = ProtocolInfo.from(json.obj("protocol")),
                stats = StatsInfo.from(json.obj("stats")),
                health = HealthInfo.from(json.obj("health")),
                lastError = json.str("last_error"),
            )
        }
    }
}

/* ------------------------------------------------------------------------------------------- */
/* §4.8 logs                                                                                    */
/* ------------------------------------------------------------------------------------------- */

data class LogEntry(
    val ts: String = "",
    val level: String = "info",
    val src: String = "",
    val msg: String = "",
) {
    val normalizedLevel: String get() = level.trim().lowercase()

    /** Stable identity for [androidx.compose.foundation.lazy.LazyColumn] keys. */
    val key: String get() = "$ts|$normalizedLevel|$src|$msg"

    /** `2026-01-01T10:00:00.000+08:00` → `10:00:00.000`. */
    val timeText: String
        get() {
            val t = ts.indexOf('T')
            if (t < 0) return ts
            val rest = ts.substring(t + 1)
            val cut = rest.indexOfFirst { it == '+' || it == '-' || it == 'Z' }
            return if (cut > 0) rest.substring(0, cut) else rest
        }

    companion object {
        fun from(json: JSONObject?): LogEntry {
            if (json == null) return LogEntry()
            return LogEntry(
                ts = json.str("ts"),
                level = json.str("level", "info"),
                src = json.str("src"),
                msg = json.str("msg"),
            )
        }

        fun listFrom(json: JSONObject?): List<LogEntry> {
            val array = json.arr("entries")
            val out = ArrayList<LogEntry>(array.length())
            for (i in 0 until array.length()) out += from(array.optJSONObject(i))
            return out
        }
    }
}

/* ------------------------------------------------------------------------------------------- */
/* §4.10 browse                                                                                 */
/* ------------------------------------------------------------------------------------------- */

data class BrowseEntry(
    val name: String = "",
    val path: String = "",
    val isDir: Boolean = false,
    val size: Long = 0L,
    val readable: Boolean = true,
) {
    companion object {
        fun from(json: JSONObject?): BrowseEntry {
            if (json == null) return BrowseEntry()
            return BrowseEntry(
                name = json.str("name"),
                path = json.str("path"),
                isDir = json.bool("is_dir"),
                size = json.long("size"),
                readable = json.bool("readable", true),
            )
        }
    }
}

data class BrowseResult(
    val path: String = "",
    val parent: String = "",
    val exists: Boolean = false,
    val entries: List<BrowseEntry> = emptyList(),
) {
    companion object {
        fun from(json: JSONObject?): BrowseResult {
            if (json == null) return BrowseResult()
            val array = json.arr("entries")
            val out = ArrayList<BrowseEntry>(array.length())
            for (i in 0 until array.length()) out += BrowseEntry.from(array.optJSONObject(i))
            return BrowseResult(
                path = json.str("path"),
                parent = json.str("parent"),
                exists = json.bool("exists"),
                entries = out,
            )
        }
    }
}

/* ------------------------------------------------------------------------------------------- */
/* §4.12 version                                                                                */
/* ------------------------------------------------------------------------------------------- */

data class VersionInfo(
    val version: String = "",
    val dufs: String = "",
    val go: String = "",
    val protocol: Int = 1,
) {
    companion object {
        fun from(json: JSONObject?): VersionInfo {
            if (json == null) return VersionInfo()
            return VersionInfo(
                version = json.str("version"),
                dufs = json.str("dufs"),
                go = json.str("go"),
                protocol = json.int("protocol", 1),
            )
        }
    }
}

/* ------------------------------------------------------------------------------------------- */
/* Helpers                                                                                      */
/* ------------------------------------------------------------------------------------------- */

private fun org.json.JSONArray.toStringList(): List<String> {
    if (length() == 0) return emptyList()
    val out = ArrayList<String>(length())
    for (i in 0 until length()) {
        val value = opt(i) ?: continue
        if (value is JSONObject || value is org.json.JSONArray) continue
        val text = value.toString()
        if (text.isNotBlank() && text != "null") out += text
    }
    return out
}
