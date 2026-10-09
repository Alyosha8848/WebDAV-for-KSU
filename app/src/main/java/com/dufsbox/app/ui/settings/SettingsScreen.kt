package com.dufsbox.app.ui.settings

import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.Spacer
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.width
import androidx.compose.foundation.rememberScrollState
import androidx.compose.foundation.text.KeyboardOptions
import androidx.compose.foundation.verticalScroll
import androidx.compose.material3.Button
import androidx.compose.material3.ExperimentalMaterial3Api
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.OutlinedButton
import androidx.compose.material3.OutlinedTextField
import androidx.compose.material3.Text
import androidx.compose.material3.TopAppBar
import androidx.compose.material3.TopAppBarDefaults
import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.rememberCoroutineScope
import androidx.compose.runtime.saveable.rememberSaveable
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.text.input.KeyboardType
import androidx.compose.ui.unit.dp
import com.dufsbox.app.ui.MainUiState
import com.dufsbox.app.ui.MainViewModel
import com.dufsbox.app.ui.MessageSink
import com.dufsbox.app.ui.ScreenContentPadding
import com.dufsbox.app.ui.components.ActionRow
import com.dufsbox.app.ui.components.HintCard
import com.dufsbox.app.ui.components.SectionCard
import com.dufsbox.app.ui.components.SelectorRow
import com.dufsbox.app.ui.components.SwitchRow
import com.dufsbox.app.ui.components.ThinDivider
import com.dufsbox.app.ui.home.PermissionSelector
import kotlinx.coroutines.launch
import org.json.JSONObject

/** Settings sub-pages. Kept as plain ints so the shell can use `rememberSaveable`. */
object SubScreen {
    const val NONE = 0
    const val SOFTWARE = 1
}

/** `log_level` accepts the daemon's five wire values (§3.3 / §4.8). */
enum class LogLevelOption(val wire: String, val label: String) {
    TRACE("trace", "TRACE（跟踪）"),
    DEBUG("debug", "DEBUG（调试）"),
    INFO("info", "INFO（信息）"),
    WARN("warn", "WARN（警告）"),
    ERROR("error", "ERROR（错误）"),
    ;

    companion object {
        fun from(wire: String?): LogLevelOption =
            entries.firstOrNull { it.wire.equals(wire?.trim(), ignoreCase = true) } ?: INFO
    }
}

private enum class ShareMode(val wire: String, val label: String) {
    LAN("lan", "局域网"),
    TAILSCALE("tailscale", "Tailscale"),
    BOTH("both", "两者"),
    ;

    companion object {
        fun from(wire: String?): ShareMode =
            entries.firstOrNull { it.wire.equals(wire?.trim(), ignoreCase = true) } ?: LAN
    }
}

/**
 * Tab 4 — 设置 (基本设置).
 *
 * Every control writes through `config.set` (§4.7). Fields that the daemon flags with
 * `restart_required` trigger the 立即重启服务 prompt through [onOfferRestart].
 */
@OptIn(ExperimentalMaterial3Api::class)
@Composable
fun SettingsScreen(
    vm: MainViewModel,
    state: MainUiState,
    onOpenSoftwareSettings: () -> Unit,
    onMessage: MessageSink,
    onOfferRestart: suspend (Boolean) -> Unit,
) {
    val config = state.config
    val scope = rememberCoroutineScope()

    // Draft state for the free-text fields; re-seeded whenever the daemon reports new values.
    var shareNameDraft by remember { mutableStateOf(config.shareName) }
    var usernameDraft by remember { mutableStateOf(config.username) }
    var passwordDraft by remember { mutableStateOf(vm.cachedPassword()) }
    var tailscaleHostnameDraft by remember { mutableStateOf(config.tailscaleHostname) }
    var passwordDirty by remember { mutableStateOf(false) }
    var browserOpen by rememberSaveable { mutableStateOf(false) }
    var saving by remember { mutableStateOf(false) }

    LaunchedEffect(config.shareName) {
        if (config.shareName.isNotBlank() && shareNameDraft != config.shareName) {
            shareNameDraft = config.shareName
        }
    }
    LaunchedEffect(config.username) {
        if (usernameDraft != config.username) usernameDraft = config.username
    }
    LaunchedEffect(config.tailscaleHostname) {
        if (tailscaleHostnameDraft != config.tailscaleHostname) {
            tailscaleHostnameDraft = config.tailscaleHostname
        }
    }
    LaunchedEffect(config.passwordSet) {
        if (vm.cachedPassword().isBlank() && config.passwordSet) {
            // We know a password exists but the daemon never returns it; leave the field empty.
            passwordDraft = ""
        }
    }

    /** Shared `config.set` runner: reports failures + restart requirements. */
    fun apply(patch: JSONObject, onDone: () -> Unit = {}) {
        if (patch.length() == 0) {
            onDone()
            return
        }
        scope.launch {
            saving = true
            vm.configSet(patch)
                .onSuccess { restartRequired ->
                    onDone()
                    onOfferRestart(restartRequired)
                }
                .onFailure { failure ->
                    onMessage(failure.message ?: "保存失败")
                }
            saving = false
        }
    }

    Column(modifier = Modifier.fillMaxWidth()) {
        TopAppBar(
            title = { Text("设置") },
            actions = {
                if (saving) {
                    Text("保存中…", style = MaterialTheme.typography.labelSmall)
                }
            },
            colors = TopAppBarDefaults.topAppBarColors(
                containerColor = MaterialTheme.colorScheme.background,
            ),
        )

        Column(
            modifier = Modifier
                .fillMaxWidth()
                .verticalScroll(rememberScrollState())
                .padding(ScreenContentPadding),
            verticalArrangement = Arrangement.spacedBy(12.dp),
        ) {
            SectionCard(
                title = "基本设置",
                subtitle = "修改共享相关的核心参数",
            ) {
                ActionRow(
                    title = "共享路径",
                    subtitle = config.sharePath.ifBlank { "未设置" },
                    onClick = { browserOpen = true },
                )
                ThinDivider()

                OutlinedTextField(
                    value = shareNameDraft,
                    onValueChange = { shareNameDraft = it },
                    label = { Text("共享名称") },
                    singleLine = true,
                    modifier = Modifier.fillMaxWidth(),
                )
                Spacer(Modifier.height(6.dp))
                OutlinedButton(
                    onClick = {
                        apply(JSONObject().put("share_name", shareNameDraft))
                    },
                    enabled = shareNameDraft != config.shareName && !saving,
                ) {
                    Text("保存共享名称")
                }
            }

            SectionCard(
                title = "认证",
                subtitle = if (config.isAnonymous) "当前：匿名访问" else "当前：账号密码",
            ) {
                SwitchRow(
                    title = "账号密码认证",
                    subtitle = "关闭后任何人可匿名访问共享目录",
                    checked = config.isPasswordAuth,
                    onCheckedChange = { usePassword ->
                        if (!usePassword) {
                            vm.cachePassword("")
                            passwordDraft = ""
                            passwordDirty = false
                            apply(JSONObject().put("auth_mode", "anonymous"))
                        } else {
                            apply(
                                JSONObject()
                                    .put("auth_mode", "password")
                                    .put("username", usernameDraft.ifBlank { "dufsbox" }),
                            )
                        }
                    },
                )

                if (config.isPasswordAuth) {
                    Spacer(Modifier.height(8.dp))
                    OutlinedTextField(
                        value = usernameDraft,
                        onValueChange = { usernameDraft = it },
                        label = { Text("用户名") },
                        singleLine = true,
                        keyboardOptions = KeyboardOptions(keyboardType = KeyboardType.Text),
                        modifier = Modifier.fillMaxWidth(),
                    )
                    Spacer(Modifier.height(8.dp))
                    PasswordField(
                        value = passwordDraft,
                        onValueChange = {
                            passwordDraft = it
                            passwordDirty = true
                        },
                        supportingText = if (config.passwordSet && passwordDraft.isBlank()) {
                            "后台已设置密码；如需修改请在此输入新密码"
                        } else {
                            null
                        },
                    )
                    Spacer(Modifier.height(6.dp))
                    Button(
                        onClick = {
                            val patch = JSONObject()
                                .put("auth_mode", "password")
                                .put("username", usernameDraft)
                                .put("password", passwordDraft)
                            apply(patch) {
                                // Keep a local copy so 状态 can reveal it (§3.3: the daemon
                                // never echoes `password`, so this is the only source).
                                vm.cachePassword(passwordDraft)
                                passwordDirty = false
                            }
                        },
                        enabled = !saving && (passwordDirty || usernameDraft != config.username),
                    ) {
                        Text("保存认证设置")
                    }
                }
            }

            SectionCard(
                title = "默认设备权限",
                subtitle = "未设置显式规则的设备使用该策略",
            ) {
                PermissionSelector(
                    selected = config.defaultPolicy,
                    isDefaultSource = false,
                    onSelect = { policy ->
                        apply(JSONObject().put("default_policy", policy.wire))
                    },
                )
                Spacer(Modifier.height(4.dp))
                Text(
                    text = "当前默认：${config.defaultPolicy.label}",
                    style = MaterialTheme.typography.labelSmall,
                    color = MaterialTheme.colorScheme.onSurfaceVariant,
                )
            }

            SectionCard(title = "共享方式与安全") {
                SelectorRow(
                    label = "共享方式",
                    options = enumValues<ShareMode>().toList(),
                    selected = ShareMode.from(config.mode),
                    optionLabel = { it.label },
                    onSelect = { mode -> apply(JSONObject().put("mode", mode.wire)) },
                )

                Spacer(Modifier.height(8.dp))
                SwitchRow(
                    title = "全局只读",
                    subtitle = "开启后所有设备只能读取，优先级高于单设备策略",
                    checked = config.readonlyGlobal,
                    onCheckedChange = { value ->
                        apply(JSONObject().put("readonly_global", value))
                    },
                )

                Spacer(Modifier.height(8.dp))
                SwitchRow(
                    title = "开机自启动",
                    subtitle = "设备重启后自动拉起 dufsboxd",
                    checked = config.autostart,
                    onCheckedChange = { value ->
                        apply(JSONObject().put("autostart", value))
                    },
                )

                Spacer(Modifier.height(8.dp))
                SelectorRow(
                    label = "默认日志级别",
                    options = enumValues<LogLevelOption>().toList(),
                    selected = LogLevelOption.from(config.logLevel),
                    optionLabel = { it.label },
                    onSelect = { level -> apply(JSONObject().put("log_level", level.wire)) },
                )
            }

            if (config.mode != "lan") {
                SectionCard(
                    title = "Tailscale",
                    subtitle = "仅在共享方式包含 Tailscale 时生效",
                ) {
                    OutlinedTextField(
                        value = tailscaleHostnameDraft,
                        onValueChange = { tailscaleHostnameDraft = it },
                        label = { Text("Tailscale 主机名") },
                        singleLine = true,
                        supportingText = { Text("用于 MagicDNS，例如 dufsbox") },
                        modifier = Modifier.fillMaxWidth(),
                    )
                    Spacer(Modifier.height(6.dp))
                    SwitchRow(
                        title = "使用 HTTPS (tailscale serve)",
                        subtitle = "通过 tailscale serve 暴露 HTTPS",
                        checked = config.tailscaleHttpsServe,
                        onCheckedChange = { value ->
                            apply(tailscalePatch(httpsServe = value))
                        },
                    )
                    Spacer(Modifier.height(6.dp))
                    Row(horizontalArrangement = Arrangement.spacedBy(8.dp)) {
                        Button(
                            onClick = {
                                apply(tailscalePatch(hostname = tailscaleHostnameDraft))
                            },
                            enabled = tailscaleHostnameDraft != config.tailscaleHostname && !saving,
                            modifier = Modifier.weight(1f),
                        ) {
                            Text("保存主机名")
                        }
                        OutlinedButton(
                            onClick = {
                                scope.launch {
                                    vm.tailscaleSet("up").fold(
                                        onSuccess = { s -> onMessage("Tailscale：$s") },
                                        onFailure = { e ->
                                            onMessage(e.message ?: "Tailscale 操作失败")
                                        },
                                    )
                                }
                            },
                            modifier = Modifier.weight(1f),
                        ) {
                            Text("连接 Tailscale")
                        }
                    }
                }
            }

            SectionCard(title = "其他") {
                ActionRow(
                    title = "软件设置",
                    subtitle = "主题、强调色、关于",
                    onClick = onOpenSoftwareSettings,
                )
            }

            HintCard(
                text = "共享路径、端口、共享方式、认证方式与全局只读的修改需要重启服务后生效；" +
                    "共享名称、默认权限、开机自启动、日志级别与 Tailscale 设置会立即生效。",
            )

            Spacer(Modifier.height(4.dp))
        }
    }

    if (browserOpen) {
        RootBrowserDialog(
            initialPath = config.sharePath.ifBlank { "/sdcard" },
            onDismiss = { browserOpen = false },
            onConfirm = { path ->
                browserOpen = false
                apply(JSONObject().put("share_path", path))
            },
            onMessage = onMessage,
        )
    }
}
