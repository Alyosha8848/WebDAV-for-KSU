package com.dufsbox.app

import android.app.Application
import android.content.Context
import com.dufsbox.app.data.prefs.SettingsStore

/**
 * Minimal Application subclass. It exists only so that the (single, no-factory)
 * [com.dufsbox.app.ui.MainViewModel] and the theme helpers can reach a Context and the
 * SharedPreferences-backed [SettingsStore] without pulling in a DI framework.
 */
class DufsBoxApp : Application() {

    override fun onCreate() {
        super.onCreate()
        appContext = applicationContext
    }

    companion object {
        /**
         * Set in [onCreate]. `lateinit` is safe: every consumer runs after Application
         * creation (Activity / ViewModel / Compose content).
         */
        lateinit var appContext: Context
            private set

        val settings: SettingsStore
            get() = SettingsStore.get(appContext)
    }
}
