package com.dufsbox.app.ui.theme

import androidx.compose.ui.graphics.Color

/**
 * Theme selection. Persisted by id via [com.dufsbox.app.data.prefs.SettingsStore].
 */
enum class ThemeMode(val id: String, val label: String) {
    SYSTEM("system", "跟随系统"),
    LIGHT("light", "浅色"),
    DARK("dark", "深色"),
    ;

    companion object {
        fun fromId(value: String?): ThemeMode =
            entries.firstOrNull { it.id == value } ?: SYSTEM
    }
}

/**
 * Preset accent colours. The dark-mode colour is deliberately a lighter tone of the same
 * hue so that Material3 contrast stays reasonable when the dynamic-colour palette is off.
 */
enum class AccentOption(
    val id: String,
    val label: String,
    val light: Color,
    val dark: Color,
) {
    BLUE("blue", "海蓝", Color(0xFF1B5E8C), Color(0xFF8FCDF2)),
    GREEN("green", "松绿", Color(0xFF2E6B4F), Color(0xFF8FD9B6)),
    PURPLE("purple", "藤紫", Color(0xFF5B3E8F), Color(0xFFC5B0F0)),
    ORANGE("orange", "陶橙", Color(0xFF9A4B12), Color(0xFFFFB77C)),
    PINK("pink", "珊瑚", Color(0xFF9C3355), Color(0xFFFFAFC6)),
    TEAL("teal", "青碧", Color(0xFF0E6B6B), Color(0xFF7FD6D6)),
    ;

    companion object {
        fun fromId(value: String?): AccentOption =
            entries.firstOrNull { it.id == value } ?: BLUE
    }
}
