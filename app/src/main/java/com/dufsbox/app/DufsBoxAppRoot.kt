package com.dufsbox.app

import androidx.compose.runtime.Composable
import androidx.compose.runtime.getValue
import androidx.lifecycle.compose.collectAsStateWithLifecycle
import androidx.lifecycle.viewmodel.compose.viewModel
import com.dufsbox.app.settings.SoftwareSettingsViewModel
import com.dufsbox.app.ui.DufsBoxApp
import com.dufsbox.app.ui.MainViewModel
import com.dufsbox.app.ui.theme.DufsBoxTheme

/**
 * Application root. Theme state lives in a small dedicated ViewModel so it survives
 * configuration changes and is shared between the app shell and the 软件设置 sub-screen.
 * The backend ViewModel is created here so exactly one polling loop exists per Activity.
 */
@Composable
fun DufsBoxAppRoot() {
    val softwareVm: SoftwareSettingsViewModel = viewModel()
    val mainVm: MainViewModel = viewModel()
    val theme by softwareVm.theme.collectAsStateWithLifecycle()

    DufsBoxTheme(
        settings = theme,
        dynamicColorSupported = softwareVm.dynamicColorSupported,
    ) {
        DufsBoxApp(
            vm = mainVm,
            softwareVm = softwareVm,
        )
    }
}
