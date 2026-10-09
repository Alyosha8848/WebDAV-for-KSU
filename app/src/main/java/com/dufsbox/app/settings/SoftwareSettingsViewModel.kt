package com.dufsbox.app.settings

import android.content.Context
import android.os.Build
import androidx.lifecycle.ViewModel
import com.dufsbox.app.DufsBoxApp
import com.dufsbox.app.data.prefs.SettingsStore
import com.dufsbox.app.ui.theme.AccentOption
import com.dufsbox.app.ui.theme.ThemeMode
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.asStateFlow
import kotlinx.coroutines.flow.update

/** Persisted software preferences (the 软件设置 sub-screen). */
data class SoftwareSettings(
    val themeMode: ThemeMode = ThemeMode.SYSTEM,
    val dynamicColor: Boolean = true,
    val accent: AccentOption = AccentOption.BLUE,
)

/**
 * Owns the 软件设置 state. Kept separate from the backend ViewModel so that theme changes
 * recompose the whole tree without touching the polling loop.
 */
class SoftwareSettingsViewModel : ViewModel() {

    private val store: SettingsStore = DufsBoxApp.settings

    private val mutableTheme = MutableStateFlow(
        SoftwareSettings(
            themeMode = store.themeMode,
            dynamicColor = store.dynamicColor,
            accent = store.accent,
        ),
    )
    val theme: StateFlow<SoftwareSettings> = mutableTheme.asStateFlow()

    /** Material You needs Android 12 (API 31). */
    val dynamicColorSupported: Boolean = Build.VERSION.SDK_INT >= Build.VERSION_CODES.S

    /** App version read from the manifest — `buildConfig` stays disabled. */
    val appVersion: String = runCatching {
        val context: Context = DufsBoxApp.appContext
        val info = context.packageManager.getPackageInfo(context.packageName, 0)
        @Suppress("DEPRECATION")
        val raw: String? = info.versionName
        raw?.takeIf { it.isNotBlank() } ?: "1.0.0"
    }.getOrDefault("1.0.0")

    val appVersionCode: Long = runCatching {
        val context: Context = DufsBoxApp.appContext
        val info = context.packageManager.getPackageInfo(context.packageName, 0)
        if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.P) {
            info.longVersionCode
        } else {
            @Suppress("DEPRECATION")
            info.versionCode.toLong()
        }
    }.getOrDefault(1L)

    val packageName: String get() = DufsBoxApp.appContext.packageName

    fun setThemeMode(mode: ThemeMode) {
        store.themeMode = mode
        mutableTheme.update { it.copy(themeMode = mode) }
    }

    fun setDynamicColor(enabled: Boolean) {
        store.dynamicColor = enabled
        mutableTheme.update { it.copy(dynamicColor = enabled) }
    }

    fun setAccent(accent: AccentOption) {
        store.accent = accent
        mutableTheme.update { it.copy(accent = accent) }
    }
}
