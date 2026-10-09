package com.dufsbox.app.data

import com.dufsbox.app.data.model.BrowseResult
import com.dufsbox.app.data.model.ConfigInfo
import com.dufsbox.app.data.model.Device
import com.dufsbox.app.data.model.LogEntry
import com.dufsbox.app.data.model.Policy
import com.dufsbox.app.data.model.Status
import com.dufsbox.app.data.model.TailscaleStatus
import com.dufsbox.app.data.model.VersionInfo
import org.json.JSONObject

/** Result of `config.set`: the (partial) config plus whether a restart is needed (§4.7). */
data class ConfigSetResult(
    val config: JSONObject?,
    val restartRequired: Boolean,
)

/** Result of `service.set` (§4.6). */
data class ServiceActionResult(val state: String)

/**
 * Typed façade over `dufsboxd ctl ...` (docs/API.md §4). Every function is a plain suspend
 * call; the ViewModel owns polling, error surfacing and caching.
 */
object Backend {

    /* ------------------------------------------------------------------------------------- */
    /* Core plumbing                                                                          */
    /* ------------------------------------------------------------------------------------- */

    /**
     * Runs `dufsboxd ctl <verb> [--json <payload>]` and returns the parsed JSON object.
     *
     * Contract (§2): exactly one JSON object on stdout, newline terminated. On failure the
     * daemon prints `{"ok":false,"error":"..."}` and exits non-zero — both are turned into a
     * [CtlException] carrying the daemon's own message.
     */
    suspend fun ctl(verb: String, jsonPayload: String? = null): JSONObject {
        val cmd = buildString {
            append(RootShell.quote(RootShell.MOD_PATH))
            append(" ctl ")
            append(RootShell.quote(verb))
            if (!jsonPayload.isNullOrBlank()) {
                append(" --json ")
                append(RootShell.quote(jsonPayload))
            }
        }

        val result = RootShell.exec(cmd)
        val parsed = parseSingleJsonObject(result.stdout)
            ?: throw CtlException(
                "无法解析后台响应：${firstLine(result.stdout.ifBlank { result.stderr }).ifBlank { "空输出" }}",
            )

        val okFlag = parsed.opt("ok")
        val isOk = when (okFlag) {
            is Boolean -> okFlag
            is String -> okFlag.equals("true", ignoreCase = true)
            null -> true // tolerant: an object without `ok` is assumed to be a success payload
            else -> false
        }

        if (!isOk) {
            throw CtlException(parsed.str("error", "命令 $verb 执行失败"))
        }

        // `ok:true` with a non-zero exit code would be a daemon bug; do not mask it.
        if (result.code != 0 && parsed.data() == null) {
            throw CtlException(
                parsed.str("error", "命令 $verb 退出码 ${result.code}"),
            )
        }
        return parsed
    }

    /** True when the KernelSU module binary is present (§6: 模块缺失 must be reported clearly). */
    suspend fun moduleInstalled(): Boolean = RootShell.exists(RootShell.MOD_PATH)

    /* ------------------------------------------------------------------------------------- */
    /* §4.1 status                                                                            */
    /* ------------------------------------------------------------------------------------- */

    suspend fun status(): Status = Status.from(ctl("status").data())

    /* ------------------------------------------------------------------------------------- */
    /* §4.2 devices                                                                           */
    /* ------------------------------------------------------------------------------------- */

    suspend fun devices(): List<Device> = Device.listFrom(ctl("devices").data())

    /* ------------------------------------------------------------------------------------- */
    /* §4.3 acl.set / §4.4 acl.remove                                                          */
    /* ------------------------------------------------------------------------------------- */

    /** `expectedDevices == null` means "leave the current policy alone" (used for `default`). */
    suspend fun setPolicy(id: String, policy: Policy): List<Device>? {
        val payload = JSONObject()
            .put("id", id)
            .put("policy", policy.wire)
        val data = ctl("acl.set", payload.toString()).data()
        val devices = Device.listFrom(data)
        return devices.ifEmpty { null }
    }

    suspend fun aclRemove(id: String) {
        val payload = JSONObject().put("id", id)
        ctl("acl.remove", payload.toString())
    }

    /* ------------------------------------------------------------------------------------- */
    /* §4.5 device.forget                                                                     */
    /* ------------------------------------------------------------------------------------- */

    suspend fun forgetDevice(id: String) {
        val payload = JSONObject().put("id", id)
        ctl("device.forget", payload.toString())
    }

    /* ------------------------------------------------------------------------------------- */
    /* §4.6 service.set                                                                       */
    /* ------------------------------------------------------------------------------------- */

    suspend fun serviceAction(action: String): ServiceActionResult {
        val payload = JSONObject().put("action", action)
        val data = ctl("service.set", payload.toString()).data()
        return ServiceActionResult(state = data.str("state", "unknown"))
    }

    /* ------------------------------------------------------------------------------------- */
    /* §4.7 config.get / config.set                                                           */
    /* ------------------------------------------------------------------------------------- */

    /** Returns the raw `data.config` object so the settings screen can render a draft. */
    suspend fun configGet(): JSONObject? = ctl("config.get").data().obj("config")

    /** Typed convenience wrapper around [configGet]. */
    suspend fun config(): ConfigInfo = ConfigInfo.from(configGet())

    /**
     * Sends a partial config. Callers pass a [JSONObject] because values are heterogeneous
     * (strings, booleans, nested `tailscale` object).
     */
    suspend fun configSet(patch: JSONObject): ConfigSetResult {
        val data = ctl("config.set", patch.toString()).data()
        return ConfigSetResult(
            config = data.obj("config"),
            restartRequired = data.bool("restart_required"),
        )
    }

    /* ------------------------------------------------------------------------------------- */
    /* §4.8 logs                                                                              */
    /* ------------------------------------------------------------------------------------- */

    /**
     * @param level minimum level (`trace|debug|info|warn|error`); the app always asks for
     *   `trace` and filters locally so switching the chip never loses buffered lines.
     * @param since epoch millis of the last line already received, or `null` for a full fetch.
     */
    suspend fun logs(
        level: String = "trace",
        limit: Int = 500,
        since: Long? = null,
    ): List<LogEntry> {
        val payload = JSONObject()
            .put("level", level)
            .put("limit", limit)
        if (since != null && since > 0L) payload.put("since", since)
        return LogEntry.listFrom(ctl("logs", payload.toString()).data())
    }

    /* ------------------------------------------------------------------------------------- */
    /* §4.10 browse                                                                           */
    /* ------------------------------------------------------------------------------------- */

    suspend fun browse(path: String): BrowseResult {
        val payload = JSONObject().put("path", path)
        val data = ctl("browse", payload.toString()).data()
        return BrowseResult.from(data).let { result ->
            // Defensive: some builds may omit `path`; fall back to what we asked for.
            if (result.path.isBlank()) result.copy(path = path) else result
        }
    }

    /* ------------------------------------------------------------------------------------- */
    /* §4.11 tailscale.set                                                                    */
    /* ------------------------------------------------------------------------------------- */

    suspend fun tailscaleSet(action: String): TailscaleStatus {
        val payload = JSONObject().put("action", action)
        val data = ctl("tailscale.set", payload.toString()).data()
        return TailscaleStatus.from(data)
    }

    /* ------------------------------------------------------------------------------------- */
    /* §4.12 version                                                                          */
    /* ------------------------------------------------------------------------------------- */

    suspend fun version(): VersionInfo = VersionInfo.from(ctl("version").data())

    /* ------------------------------------------------------------------------------------- */
    /* Parsing helpers                                                                        */
    /* ------------------------------------------------------------------------------------- */

    /**
     * Extracts exactly one JSON object from a stdout blob. Tolerates leading shell noise
     * (e.g. an `su` banner) by taking the span from the first `{` to the last `}`.
     */
    fun parseSingleJsonObject(raw: String): JSONObject? {
        val trimmed = raw.trim()
        if (trimmed.isEmpty()) return null

        // Fast path: pure JSON (the contract).
        runCatching { JSONObject(trimmed) }.getOrNull()?.let { return it }

        // Slow path: strip non-JSON framing around the object.
        val start = trimmed.indexOf('{')
        val end = trimmed.lastIndexOf('}')
        if (start >= 0 && end > start) {
            val slice = trimmed.substring(start, end + 1)
            runCatching { JSONObject(slice) }.getOrNull()?.let { return it }
        }

        // Last resort: first line that looks like JSON.
        trimmed.lineSequence()
            .map { it.trim() }
            .filter { it.startsWith("{") && it.endsWith("}") }
            .forEach { line ->
                runCatching { JSONObject(line) }.getOrNull()?.let { return it }
            }
        return null
    }

    private fun firstLine(text: String): String =
        text.lineSequence().firstOrNull { it.isNotBlank() }?.take(200).orEmpty()

    /** `strOrNull` is used by the settings draft builder; keep it referenced here. */
    internal fun JSONObject?.optional(key: String): String? = this.strOrNull(key)
}
