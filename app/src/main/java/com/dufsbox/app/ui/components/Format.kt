package com.dufsbox.app.ui.components

import java.time.Instant
import java.time.OffsetDateTime
import java.time.ZoneId
import java.util.Locale

/** Human readable byte count (binary units, matching what the daemon reports). */
fun formatBytes(bytes: Long): String {
    if (bytes < 0) return "-" + formatBytes(-bytes)
    if (bytes < 1024L) return "$bytes B"
    val units = arrayOf("KB", "MB", "GB", "TB", "PB")
    var value = bytes.toDouble() / 1024.0
    var index = 0
    while (value >= 1024.0 && index < units.lastIndex) {
        value /= 1024.0
        index++
    }
    return if (value >= 100.0) {
        String.format(Locale.US, "%.0f %s", value, units[index])
    } else {
        String.format(Locale.US, "%.1f %s", value, units[index])
    }
}

/** `321` → `5 分 21 秒`, `90061` → `1 天 1 小时 1 分 1 秒`. */
fun formatUptime(seconds: Long): String {
    if (seconds <= 0L) return "—"
    var rest = seconds
    val days = rest / 86_400
    rest %= 86_400
    val hours = rest / 3_600
    rest %= 3_600
    val minutes = rest / 60
    val secs = rest % 60

    val parts = ArrayList<String>(4)
    if (days > 0) parts += "$days 天"
    if (hours > 0) parts += "$hours 小时"
    if (minutes > 0) parts += "$minutes 分"
    if (secs > 0 && days == 0L) parts += "$secs 秒"
    return if (parts.isEmpty()) "—" else parts.joinToString(" ")
}

/** `2026-01-01T10:05:00+08:00` → `10:05:00`. */
fun formatClock(iso: String): String {
    if (iso.isBlank()) return "—"
    val t = iso.indexOf('T')
    if (t < 0) return iso
    val rest = iso.substring(t + 1)
    val cut = rest.indexOfFirst { it == '+' || it == 'Z' }
    val body = if (cut >= 0) rest.substring(0, cut) else rest
    return if (body.length >= 8) body.substring(0, 8) else body
}

/** `2026-01-01T10:05:00+08:00` → `2026-01-01 10:05:00`. */
fun formatDateTime(iso: String): String {
    if (iso.isBlank()) return "—"
    val t = iso.indexOf('T')
    if (t < 0) return iso
    return iso.substring(0, t) + " " + formatClock(iso)
}

private fun parseIso(iso: String): Instant? {
    if (iso.isBlank()) return null
    runCatching { return OffsetDateTime.parse(iso).toInstant() }
    runCatching { return Instant.parse(iso) }
    runCatching {
        return java.time.LocalDateTime.parse(iso)
            .atZone(ZoneId.systemDefault())
            .toInstant()
    }
    return null
}

/** `设备最近在线` style relative time: 刚刚 / N 分钟前 / N 小时前 / N 天前. */
fun relativeTime(iso: String): String {
    val instant = parseIso(iso) ?: return if (iso.isBlank()) "—" else iso
    val deltaSec = java.time.Duration.between(instant, Instant.now()).toSeconds()
    return when {
        deltaSec < 5 -> "刚刚"
        deltaSec < 60 -> "$deltaSec 秒前"
        deltaSec < 3_600 -> "${deltaSec / 60} 分钟前"
        deltaSec < 86_400 -> "${deltaSec / 3_600} 小时前"
        deltaSec < 2_592_000 -> "${deltaSec / 86_400} 天前"
        else -> formatDateTime(iso)
    }
}

/** `true`/`false`/`null` as 是 / 否 / —. */
fun formatBool(value: Boolean?): String = when (value) {
    true -> "是"
    false -> "否"
    null -> "—"
}

fun truncate(value: String, max: Int = 120): String =
    if (value.length <= max) value else value.take(max - 1) + "…"
