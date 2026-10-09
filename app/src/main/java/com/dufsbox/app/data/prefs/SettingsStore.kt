package com.dufsbox.app.data.prefs

import android.content.Context
import android.content.SharedPreferences
import com.dufsbox.app.ui.theme.AccentOption
import com.dufsbox.app.ui.theme.ThemeMode

/**
 * Tiny SharedPreferences facade. Deliberately **not** DataStore (see the project
 * constraints): theme choices and the locally cached share password only.
 *
 * NOTE on the password: `dufsboxd` never echoes `password` back (see docs/API.md §3.3),
 * so the only way the 状态 tab can reveal it is if the user typed it here. When nothing
 * is cached the UI says so instead of inventing a value.
 */
class SettingsStore private constructor(private val prefs: SharedPreferences) {

    var password: String
        get() = prefs.getString(KEY_PASSWORD, "").orEmpty()
        set(value) = prefs.edit().putString(KEY_PASSWORD, value).apply()

    var themeMode: ThemeMode
        get() = ThemeMode.fromId(prefs.getString(KEY_THEME_MODE, null))
        set(value) = prefs.edit().putString(KEY_THEME_MODE, value.id).apply()

    var dynamicColor: Boolean
        get() = prefs.getBoolean(KEY_DYNAMIC_COLOR, true)
        set(value) = prefs.edit().putBoolean(KEY_DYNAMIC_COLOR, value).apply()

    var accent: AccentOption
        get() = AccentOption.fromId(prefs.getString(KEY_ACCENT, null))
        set(value) = prefs.edit().putString(KEY_ACCENT, value.id).apply()

    /** True once the user has stored *something* for the password field. */
    val hasCachedPassword: Boolean
        get() = prefs.contains(KEY_PASSWORD)

    fun clearPassword() {
        prefs.edit().remove(KEY_PASSWORD).apply()
    }

    companion object {
        private const val PREFS_NAME = "dufsbox_prefs"
        private const val KEY_PASSWORD = "share_password"
        private const val KEY_THEME_MODE = "theme_mode"
        private const val KEY_DYNAMIC_COLOR = "dynamic_color"
        private const val KEY_ACCENT = "accent"

        @Volatile
        private var instance: SettingsStore? = null

        fun get(context: Context): SettingsStore =
            instance ?: synchronized(this) {
                instance ?: SettingsStore(
                    context.applicationContext.getSharedPreferences(
                        PREFS_NAME,
                        Context.MODE_PRIVATE,
                    ),
                ).also { instance = it }
            }
    }
}
