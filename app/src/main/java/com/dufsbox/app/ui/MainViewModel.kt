package com.dufsbox.app.ui

import android.os.SystemClock
import androidx.lifecycle.ViewModel
import androidx.lifecycle.viewModelScope
import com.dufsbox.app.DufsBoxApp
import com.dufsbox.app.data.Backend
import com.dufsbox.app.data.CtlException
import com.dufsbox.app.data.ModuleMissingException
import com.dufsbox.app.data.NoRootException
import com.dufsbox.app.data.RootShell
import com.dufsbox.app.data.ShellException
import com.dufsbox.app.data.model.LogEntry
import com.dufsbox.app.data.model.Policy
import kotlinx.coroutines.CancellationException
import kotlinx.coroutines.Job
import kotlinx.coroutines.delay
import kotlinx.coroutines.flow.MutableSharedFlow
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.SharedFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.asSharedFlow
import kotlinx.coroutines.flow.asStateFlow
import kotlinx.coroutines.flow.update
import kotlinx.coroutines.isActive
import kotlinx.coroutines.launch
import org.json.JSONObject
import java.time.OffsetDateTime
import java.time.format.DateTimeFormatter

/**
 * Single source of truth for the whole app.
 *
 * Nothing polls inside a composable: one coroutine in [viewModelScope] drives `status` +
 * `devices` every 2 s and `logs` incrementally, publishing into a single [StateFlow] that the
 * UI reads with `collectAsStateWithLifecycle()`, so no UI work happens while the app is
 * backgrounded.
 */
class MainViewModel : ViewModel() {

    private val settings = DufsBoxApp.settings

    private val mutableState = MutableStateFlow(MainUiState())
    val state: StateFlow<MainUiState> = mutableState.asStateFlow()

    /**
     * Alias kept for API clarity — identical to [state]. The polling loop is a single job in
     * [viewModelScope]; every UI collector uses `collectAsStateWithLifecycle()`, so collection
     * (and therefore UI updates) stops when the app is not visible.
     */
    val uiState: StateFlow<MainUiState> get() = state

    private val mutableMessages = MutableSharedFlow<UiMessage>(extraBufferCapacity = 8)
    val messages: SharedFlow<UiMessage> = mutableMessages.asSharedFlow()

    private val seenLogKeys = LinkedHashSet<String>()

    @Volatile
    private var lastLogMillis = 0L

    /** Epoch millis after which `logs --since` returns only new lines. */
    private var currentSince: Long? = null

    private var pollJob: Job? = null

    init {
        ensurePolling()
    }

    /* ------------------------------------------------------------------------------------- */
    /* Diagnostics for the settings screen                                                    */
    /* ------------------------------------------------------------------------------------- */

    val modulePath: String get() = RootShell.modPath

    /* ------------------------------------------------------------------------------------- */
    /* Polling                                                                                */
    /* ------------------------------------------------------------------------------------- */

    private fun ensurePolling() {
        if (pollJob?.isActive == true) return
        pollJob = viewModelScope.launch {
            // First pass: resolve root + module presence once.
            if (!probeConnection()) return@launch
            while (isActive) {
                refreshOnce()
                delay(POLL_INTERVAL_MS)
            }
        }
    }

    /** Root + module probe. Returns true when the daemon can be talked to. */
    private suspend fun probeConnection(): Boolean {
        return try {
            RootShell.requireShell()
            if (!Backend.moduleInstalled()) {
                mutableState.update {
                    it.copy(connection = ConnectionState.ModuleMissing, initialLoadDone = true)
                }
                return false
            }
            mutableState.update { it.copy(connection = ConnectionState.Connecting) }
            true
        } catch (e: CancellationException) {
            throw e
        } catch (e: NoRootException) {
            mutableState.update {
                it.copy(
                    connection = ConnectionState.NoRoot,
                    initialLoadDone = true,
                )
            }
            false
        } catch (e: Exception) {
            mutableState.update {
                it.copy(
                    connection = ConnectionState.Error(e.message ?: "无法访问 root shell"),
                    initialLoadDone = true,
                )
            }
            false
        }
    }

    private suspend fun refreshOnce() {
        val statusOk = pollStatus()
        val devicesOk = pollDevices()
        if (statusOk || devicesOk) {
            pollLogs()
        }
        mutableState.update {
            it.copy(
                initialLoadDone = true,
                lastUpdated = SystemClock.elapsedRealtime(),
            )
        }
    }

    private suspend fun pollStatus(): Boolean = try {
        val status = Backend.status()
        mutableState.update { it.copy(status = status, connection = ConnectionState.Ok) }
        true
    } catch (e: CancellationException) {
        throw e
    } catch (e: Exception) {
        reportFailure(e)
        false
    }

    private suspend fun pollDevices(): Boolean = try {
        val devices = Backend.devices()
        mutableState.update { it.copy(devices = devices, connection = ConnectionState.Ok) }
        true
    } catch (e: CancellationException) {
        throw e
    } catch (e: Exception) {
        reportFailure(e)
        false
    }

    /** Incremental fetch using `since` = newest timestamp we already hold (§5). */
    private suspend fun pollLogs() {
        try {
            val entries = Backend.logs(level = "trace", limit = LOG_BUFFER_LIMIT, since = currentSince)
            if (entries.isEmpty()) return
            appendLogs(entries)
        } catch (e: CancellationException) {
            throw e
        } catch (e: Exception) {
            // A log fetch failure must never mask a working device/status poll.
            reportFailure(e, onlyIfChanged = true)
        }
    }

    private fun appendLogs(entries: List<LogEntry>) {
        val fresh = ArrayList<LogEntry>(entries.size)
        for (entry in entries) {
            if (seenLogKeys.add(entry.key)) fresh += entry
            parseMillis(entry.ts)?.let { ms -> if (ms > lastLogMillis) lastLogMillis = ms }
        }
        if (lastLogMillis > 0L) currentSince = lastLogMillis

        val merged = if (fresh.isEmpty()) {
            mutableState.value.logs
        } else {
            (mutableState.value.logs + fresh).takeLast(LOG_BUFFER_LIMIT)
        }

        // Keep the dedup set bounded and consistent with the retained buffer.
        if (seenLogKeys.size > LOG_BUFFER_LIMIT * 2) {
            seenLogKeys.clear()
            merged.forEach { seenLogKeys += it.key }
        }

        mutableState.update { it.copy(logs = merged) }
    }

    private fun reportFailure(e: Exception, onlyIfChanged: Boolean = false) {
        val message = when (e) {
            is NoRootException -> e.message ?: "未获取 root 权限"
            is ModuleMissingException -> e.message ?: "未检测到 DufsBox 模块"
            is CtlException, is ShellException -> e.message ?: "后台调用失败"
            else -> e.message ?: e.javaClass.simpleName
        }
        val previous = mutableState.value.connection
        mutableState.update { it.copy(connection = ConnectionState.Error(message)) }
        val changed = previous !is ConnectionState.Error || previous.message != message
        if (changed && !onlyIfChanged) {
            emit(message)
        }
    }

    private fun emit(text: String) {
        if (text.isBlank()) return
        mutableMessages.tryEmit(UiMessage(text))
    }

    /* ------------------------------------------------------------------------------------- */
    /* Actions                                                                                */
    /* ------------------------------------------------------------------------------------- */

    /** Manually re-run the probe — used by "重试" affordances. */
    fun retryConnection() {
        RootShell.invalidate()
        lastLogMillis = 0L
        currentSince = null
        seenLogKeys.clear()
        mutableState.update { it.copy(connection = ConnectionState.Connecting) }
        if (pollJob?.isActive == true) {
            viewModelScope.launch { if (probeConnection()) refreshOnce() }
        } else {
            ensurePolling()
        }
    }

    /** `service.set` — start / stop / restart (§4.6). */
    fun serviceAction(action: String) {
        if (mutableState.value.actionInFlight != null) return
        mutableState.update { it.copy(actionInFlight = action) }
        viewModelScope.launch {
            try {
                val result = Backend.serviceAction(action)
                val label = when (action) {
                    "start" -> "已启动"
                    "stop" -> "已停止"
                    "restart" -> "已重启"
                    else -> "操作完成"
                }
                emit("$label（${result.state}）")
                pollStatus()
                pollDevices()
            } catch (e: CancellationException) {
                throw e
            } catch (e: Exception) {
                reportFailure(e, onlyIfChanged = false)
                emit(errorText(e))
            } finally {
                mutableState.update { it.copy(actionInFlight = null) }
            }
        }
    }

    /** `acl.set` — the per-device 3-way selector (§4.3). Applied optimistically. */
    fun setPolicy(deviceId: String, policy: Policy) {
        if (deviceId.isBlank()) return
        val previous = mutableState.value.devices
        mutableState.update { current ->
            current.copy(
                devices = current.devices.map { device ->
                    if (device.id == deviceId) {
                        device.copy(
                            policy = policy,
                            policySource = if (policy == Policy.DEFAULT) "default" else "mac",
                        )
                    } else {
                        device
                    }
                },
            )
        }
        viewModelScope.launch {
            try {
                val updated = Backend.setPolicy(deviceId, policy)
                if (updated != null) {
                    mutableState.update { it.copy(devices = updated) }
                }
            } catch (e: CancellationException) {
                throw e
            } catch (e: Exception) {
                mutableState.update { it.copy(devices = previous) }
                emit(errorText(e))
            }
        }
    }

    /** `device.forget` — removes it from the list and the statistics (§4.5). */
    fun forgetDevice(deviceId: String) {
        if (deviceId.isBlank()) return
        viewModelScope.launch {
            try {
                Backend.forgetDevice(deviceId)
                mutableState.update { current ->
                    current.copy(devices = current.devices.filterNot { it.id == deviceId })
                }
                emit("已忘记设备")
                pollDevices()
            } catch (e: CancellationException) {
                throw e
            } catch (e: Exception) {
                emit(errorText(e))
            }
        }
    }

    /** Force one `logs` fetch, e.g. the 刷新 button. */
    fun refreshLogs() {
        viewModelScope.launch { pollLogs() }
    }

    /** Clears only the local buffer; the daemon keeps its own log files. */
    fun clearLogs() {
        seenLogKeys.clear()
        lastLogMillis = 0L
        currentSince = null
        mutableState.update { it.copy(logs = emptyList()) }
        emit("已清空本地日志缓存")
    }

    fun setLogFilter(filter: LogLevelFilter) {
        mutableState.update { it.copy(logFilter = filter) }
    }

    /* ------------------------------------------------------------------------------------- */
    /* Settings-backed operations                                                             */
    /* ------------------------------------------------------------------------------------- */

    /**
     * `config.set` (§4.7). Returns `restartRequired` so the caller can offer 立即重启服务.
     */
    suspend fun configSet(patch: JSONObject): Result<Boolean> = runCatching {
        val result = Backend.configSet(patch)
        pollStatus()
        result.restartRequired
    }

    /**
     * `tailscale.set` (§4.11). `serve_on`/`serve_off` are config-driven, so refresh status
     * afterwards to pick up the new `https_serve` flag.
     */
    suspend fun tailscaleSet(action: String): Result<String> = runCatching {
        val status = Backend.tailscaleSet(action)
        pollStatus()
        status.state
    }

    /** Loads the daemon version for the 关于 card. */
    fun loadVersion() {
        if (mutableState.value.version != null) return
        viewModelScope.launch {
            runCatching { Backend.version() }
                .onSuccess { info -> mutableState.update { it.copy(version = info) } }
                .onFailure { /* non-fatal: the About card simply shows 未知 */ }
        }
    }

    /* ------------------------------------------------------------------------------------- */
    /* Password cache (app-local only — the daemon never echoes it, §3.3)                      */
    /* ------------------------------------------------------------------------------------- */

    fun cachedPassword(): String = settings.password

    fun cachePassword(value: String) {
        settings.password = value
    }

    /* ------------------------------------------------------------------------------------- */
    /* Helpers                                                                                */
    /* ------------------------------------------------------------------------------------- */

    private fun errorText(e: Exception): String = when (e) {
        is CtlException -> e.message ?: "后台调用失败"
        is NoRootException -> e.message ?: "未获取 root 权限"
        is ModuleMissingException -> e.message ?: "未检测到 DufsBox 模块"
        else -> e.message ?: e.javaClass.simpleName
    }

    override fun onCleared() {
        super.onCleared()
        pollJob = null
    }

    companion object {
        const val POLL_INTERVAL_MS = 2_000L

        /** Parses the daemon's RFC3339 timestamps into epoch millis. */
        fun parseMillis(ts: String): Long? {
            if (ts.isBlank()) return null
            return try {
                OffsetDateTime.parse(ts).toInstant().toEpochMilli()
            } catch (ignored: Exception) {
                try {
                    java.time.Instant.parse(ts).toEpochMilli()
                } catch (alsoIgnored: Exception) {
                    // Fallback for a naive local timestamp.
                    try {
                        java.time.LocalDateTime.parse(ts, DateTimeFormatter.ISO_LOCAL_DATE_TIME)
                            .atZone(java.time.ZoneId.systemDefault())
                            .toInstant()
                            .toEpochMilli()
                    } catch (stillIgnored: Exception) {
                        null
                    }
                }
            }
        }
    }
}
