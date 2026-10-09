package com.dufsbox.app.ui

import com.dufsbox.app.data.model.ConfigInfo
import com.dufsbox.app.data.model.Device
import com.dufsbox.app.data.model.LogEntry
import com.dufsbox.app.data.model.Status
import com.dufsbox.app.data.model.VersionInfo

/** App-level connection state (§6: 非 root / 模块缺失 must never crash, only explain). */
sealed interface ConnectionState {
    /** Not resolved yet — the first probe is still running. */
    data object Connecting : ConnectionState

    /** Root shell rejected the request. */
    data object NoRoot : ConnectionState

    /** Root granted, but `/data/adb/modules/dufsbox/bin/arm64/dufsboxd` is absent. */
    data object ModuleMissing : ConnectionState

    /** The daemon answered the last `ctl` call. */
    data object Ok : ConnectionState

    /** Recoverable failure (daemon not serving, parse error, ...). */
    data class Error(val message: String) : ConnectionState
}

/** Which log levels the 日志 tab shows. `ALL` maps to the `trace` floor. */
enum class LogLevelFilter(val wire: String, val label: String) {
    ALL("trace", "全部"),
    DEBUG("debug", "DEBUG"),
    INFO("info", "INFO"),
    WARN("warn", "WARN"),
    ERROR("error", "ERROR"),
    ;

    companion object {
        val chips: List<LogLevelFilter> = entries.toList()
    }
}

/** One-shot message for the Snackbar (kept out of the polling state on purpose). */
data class UiMessage(val text: String, val id: Long = System.nanoTime())

/**
 * Everything the UI renders. Published as a single StateFlow so that the polling loop is
 * driven by exactly one `WhileSubscribed` subscription.
 */
data class MainUiState(
    val connection: ConnectionState = ConnectionState.Connecting,
    val status: Status? = null,
    val version: VersionInfo? = null,
    val devices: List<Device> = emptyList(),
    val logs: List<LogEntry> = emptyList(),
    val logFilter: LogLevelFilter = LogLevelFilter.ALL,
    val actionInFlight: String? = null,
    val initialLoadDone: Boolean = false,
    val lastUpdated: Long = 0L,
) {
    val config: ConfigInfo get() = status?.config ?: ConfigInfo()

    val deviceCount: Int get() = devices.size
    val onlineCount: Int get() = devices.count { it.online }

    val isRootReady: Boolean
        get() = connection is ConnectionState.Ok ||
            connection is ConnectionState.Error

    val filteredLogs: List<LogEntry>
        get() {
            val floor = logFilter.ordinal // ALL == 0 == trace
            return logs.filter { entry -> levelRank(entry.normalizedLevel) >= floor }
        }

    companion object {
        /** trace=0 … error=4, so `>= filter.ordinal` implements "this level and above". */
        fun levelRank(level: String): Int = when (level.trim().lowercase()) {
            "trace" -> 0
            "debug" -> 1
            "info" -> 2
            "warn", "warning" -> 3
            "error", "fatal", "panic" -> 4
            else -> 2
        }
    }
}

/** Max number of log lines kept in memory (§ App side requirement). */
const val LOG_BUFFER_LIMIT = 500
