package com.dufsbox.app.ui.components

import androidx.compose.material3.MaterialTheme
import androidx.compose.runtime.Composable
import androidx.compose.ui.graphics.Color

/**
 * Wire level → Chinese label + colour. Order is the canonical `trace … error` ranking used
 * by the daemon (§4.8).
 */
enum class LogLevelStyle(val wire: String, val short: String, val zh: String) {
    TRACE("trace", "TRACE", "跟踪"),
    DEBUG("debug", "DEBUG", "调试"),
    INFO("info", "INFO", "信息"),
    WARN("warn", "WARN", "警告"),
    ERROR("error", "ERROR", "错误"),
    ;

    /** 3-letter badge shown in the log list. */
    val badge: String get() = short.take(3)

    companion object {
        val options: List<LogLevelStyle> = entries.toList()

        fun from(level: String): LogLevelStyle = when (level.trim().lowercase()) {
            "trace" -> TRACE
            "debug" -> DEBUG
            "info" -> INFO
            "warn", "warning" -> WARN
            "error", "fatal", "panic" -> ERROR
            else -> INFO
        }
    }
}

/** Fixed colour per level: trace=灰, debug=蓝, info=绿, warn=琥珀, error=红. */
fun LogLevelStyle.color(): Color = when (this) {
    LogLevelStyle.TRACE -> Color(0xFF7A7A7A)
    LogLevelStyle.DEBUG -> Color(0xFF2F6FD0)
    LogLevelStyle.INFO -> Color(0xFF2E9E5B)
    LogLevelStyle.WARN -> Color(0xFFD08A0E)
    LogLevelStyle.ERROR -> Color(0xFFC62828)
}

/** Colour of the `全部` pseudo-level (the theme accent). */
@Composable
fun allLevelsColor(): Color = MaterialTheme.colorScheme.primary
