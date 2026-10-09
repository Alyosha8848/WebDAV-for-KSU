package com.dufsbox.app.ui

import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.PaddingValues
import androidx.compose.foundation.layout.WindowInsets
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.padding
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.filled.Home
import androidx.compose.material.icons.filled.List
import androidx.compose.material.icons.filled.Settings
import androidx.compose.material.icons.filled.Star
import androidx.compose.material3.ExperimentalMaterial3Api
import androidx.compose.material3.Icon
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.NavigationBar
import androidx.compose.material3.NavigationBarItem
import androidx.compose.material3.Scaffold
import androidx.compose.material3.SnackbarHost
import androidx.compose.material3.SnackbarHostState
import androidx.compose.material3.SnackbarResult
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableIntStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.rememberCoroutineScope
import androidx.compose.runtime.saveable.rememberSaveable
import androidx.compose.runtime.setValue
import androidx.compose.ui.Modifier
import androidx.compose.ui.graphics.vector.ImageVector
import androidx.compose.ui.unit.dp
import androidx.lifecycle.compose.collectAsStateWithLifecycle
import com.dufsbox.app.settings.SoftwareSettingsViewModel
import com.dufsbox.app.ui.home.HomeScreen
import com.dufsbox.app.ui.logs.LogsScreen
import com.dufsbox.app.ui.settings.SettingsScreen
import com.dufsbox.app.ui.settings.SettingsSubScreen
import com.dufsbox.app.ui.settings.SubScreen
import com.dufsbox.app.ui.status.StatusScreen
import kotlinx.coroutines.launch

/**
 * Screens report copy confirmations / notices through this sink.
 *
 * Deliberately **not** suspend: it is called from `onClick` / `onCopied` lambdas, which are not
 * suspend contexts. The shell hands each screen a callback that launches the Snackbar on the
 * composition scope and dedupes rapid repeats.
 */
typealias MessageSink = (String) -> Unit

/** Bottom navigation destinations (§6: 首页 / 日志 / 状态 / 设置). */
enum class AppTab(val title: String, val icon: ImageVector) {
    HOME("首页", Icons.Filled.Home),
    LOGS("日志", Icons.Filled.List),
    STATUS("状态", Icons.Filled.Star),
    SETTINGS("设置", Icons.Filled.Settings),
}

/**
 * Material3 shell. No navigation library: the tab is a `rememberSaveable` int and the body is
 * a `when`. Every screen draws its own `TopAppBar` so the 设置 sub-page can show a back arrow
 * while the bottom bar stays put.
 */
@OptIn(ExperimentalMaterial3Api::class)
@Composable
fun DufsBoxApp(
    vm: MainViewModel,
    softwareVm: SoftwareSettingsViewModel,
) {
    val state by vm.state.collectAsStateWithLifecycle()
    val snackbarHostState = remember { SnackbarHostState() }
    val scope = rememberCoroutineScope()
    var selectedTab by rememberSaveable { mutableIntStateOf(0) }
    var subScreen by rememberSaveable { mutableIntStateOf(SubScreen.NONE) }

    // One-shot backend errors / confirmations, drained into the Snackbar.
    LaunchedEffect(vm) {
        vm.messages.collect { message ->
            snackbarHostState.showSnackbar(message.text)
        }
    }

    // Non-suspend sink for the screens: launches on the composition scope and swallows rapid
    // repeats so copying several rows does not queue up an endless Snackbar backlog.
    val messageSink: MessageSink = remember(scope) {
        var lastText: String? = null
        var lastAt = 0L
        { text: String ->
            val now = android.os.SystemClock.elapsedRealtime()
            if (text.isNotBlank() && (text != lastText || now - lastAt > 1_200L)) {
                lastText = text
                lastAt = now
                scope.launch { snackbarHostState.showSnackbar(text) }
            }
        }
    }

    // Load the daemon version once, for the 关于 card.
    LaunchedEffect(state.connection, state.version) {
        if (state.connection is ConnectionState.Ok && state.version == null) {
            vm.loadVersion()
        }
    }

    Scaffold(
        modifier = Modifier.fillMaxSize(),
        containerColor = MaterialTheme.colorScheme.background,
        // Each screen owns its own top bar / status-bar inset handling.
        contentWindowInsets = WindowInsets(0, 0, 0, 0),
        snackbarHost = { SnackbarHost(hostState = snackbarHostState) },
        bottomBar = {
            NavigationBar {
                AppTab.entries.forEachIndexed { index, tab ->
                    NavigationBarItem(
                        selected = selectedTab == index && subScreen == SubScreen.NONE,
                        onClick = {
                            selectedTab = index
                            subScreen = SubScreen.NONE
                        },
                        icon = { Icon(tab.icon, contentDescription = tab.title) },
                        label = { Text(tab.title) },
                    )
                }
            }
        },
    ) { innerPadding ->
        Box(
            modifier = Modifier
                .fillMaxSize()
                .padding(innerPadding),
        ) {
            if (subScreen == SubScreen.SOFTWARE) {
                SettingsSubScreen(
                    softwareVm = softwareVm,
                    version = state.version,
                    onBack = { subScreen = SubScreen.NONE },
                )
                return@Box
            }

            when (AppTab.entries[selectedTab]) {
                AppTab.HOME -> HomeScreen(
                    state = state,
                    onServiceAction = vm::serviceAction,
                    onSetPolicy = vm::setPolicy,
                    onForgetDevice = vm::forgetDevice,
                    onRetry = vm::retryConnection,
                    onMessage = messageSink,
                )

                AppTab.LOGS -> LogsScreen(
                    state = state,
                    onSetFilter = vm::setLogFilter,
                    onClear = vm::clearLogs,
                    onRefresh = vm::refreshLogs,
                    onMessage = messageSink,
                )

                AppTab.STATUS -> StatusScreen(
                    state = state,
                    cachedPassword = vm.cachedPassword(),
                    onMessage = messageSink,
                    onTailscaleAction = { action ->
                        vm.tailscaleSet(action).fold(
                            onSuccess = { newState -> messageSink("Tailscale：$newState") },
                            onFailure = { error ->
                                messageSink(error.message ?: "Tailscale 操作失败")
                            },
                        )
                    },
                    onRetry = vm::retryConnection,
                    modulePath = vm.modulePath,
                )

                AppTab.SETTINGS -> SettingsScreen(
                    vm = vm,
                    state = state,
                    onOpenSoftwareSettings = { subScreen = SubScreen.SOFTWARE },
                    onMessage = messageSink,
                    onOfferRestart = { restartRequired ->
                        if (!restartRequired) {
                            messageSink("设置已保存")
                        } else {
                            val result = snackbarHostState.showSnackbar(
                                message = "该修改需要重启服务才能生效",
                                actionLabel = "立即重启",
                                withDismissAction = true,
                            )
                            if (result == SnackbarResult.ActionPerformed) {
                                vm.serviceAction("restart")
                            }
                        }
                    },
                )
            }
        }
    }
}

/** Padding used by the scrollable body of every screen. */
val ScreenContentPadding = PaddingValues(horizontal = 16.dp, vertical = 12.dp)
