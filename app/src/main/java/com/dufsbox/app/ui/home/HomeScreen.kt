package com.dufsbox.app.ui.home

import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.Spacer
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.width
import androidx.compose.foundation.rememberScrollState
import androidx.compose.foundation.verticalScroll
import androidx.compose.material3.Button
import androidx.compose.material3.ButtonDefaults
import androidx.compose.material3.DropdownMenu
import androidx.compose.material3.DropdownMenuItem
import androidx.compose.material3.ExperimentalMaterial3Api
import androidx.compose.material3.HorizontalDivider
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.OutlinedButton
import androidx.compose.material3.SegmentedButton
import androidx.compose.material3.SegmentedButtonDefaults
import androidx.compose.material3.SingleChoiceSegmentedButtonRow
import androidx.compose.material3.Text
import androidx.compose.material3.TextButton
import androidx.compose.material3.TopAppBar
import androidx.compose.material3.TopAppBarDefaults
import androidx.compose.runtime.Composable
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.saveable.rememberSaveable
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.platform.LocalClipboardManager
import androidx.compose.ui.text.AnnotatedString
import androidx.compose.ui.text.font.FontFamily
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.unit.dp
import com.dufsbox.app.data.model.ConfigInfo
import com.dufsbox.app.data.model.Device
import com.dufsbox.app.data.model.DeviceKind
import com.dufsbox.app.data.model.Policy
import com.dufsbox.app.ui.ConnectionState
import com.dufsbox.app.ui.MessageSink
import com.dufsbox.app.ui.MainUiState
import com.dufsbox.app.ui.ScreenContentPadding
import com.dufsbox.app.ui.components.ColoredDot
import com.dufsbox.app.ui.components.ConfirmDialog
import com.dufsbox.app.ui.components.EmptyState
import com.dufsbox.app.ui.components.HintCard
import com.dufsbox.app.ui.components.SectionCard
import com.dufsbox.app.ui.components.StatusDot
import com.dufsbox.app.ui.components.TagBadge
import com.dufsbox.app.ui.components.formatBytes
import com.dufsbox.app.ui.components.formatUptime
import com.dufsbox.app.ui.components.relativeTime

/** Tab 1 — 首页: master control + per-device permissions. */
@OptIn(ExperimentalMaterial3Api::class)
@Composable
fun HomeScreen(
    state: MainUiState,
    onServiceAction: (String) -> Unit,
    onSetPolicy: (String, Policy) -> Unit,
    onForgetDevice: (String) -> Unit,
    onRetry: () -> Unit,
    onMessage: MessageSink,
) {
    Column(modifier = Modifier.fillMaxWidth()) {
        TopAppBar(
            title = { Text("DufsBox 控制面板") },
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
            ConnectionBanner(connection = state.connection, onRetry = onRetry)

            MasterControlCard(
                state = state,
                config = state.config,
                busy = state.actionInFlight != null,
                onServiceAction = onServiceAction,
            )

            DevicesCard(
                devices = state.devices,
                initialLoadDone = state.initialLoadDone,
                online = state.onlineCount,
                onSetPolicy = onSetPolicy,
                onForgetDevice = onForgetDevice,
                onMessage = onMessage,
            )

            Spacer(Modifier.height(4.dp))
        }
    }
}

/* ------------------------------------------------------------------------------------------- */
/* Connection banner                                                                            */
/* ------------------------------------------------------------------------------------------- */

@Composable
private fun ConnectionBanner(connection: ConnectionState, onRetry: () -> Unit) {
    when (connection) {
        ConnectionState.Ok, ConnectionState.Connecting -> Unit

        ConnectionState.NoRoot -> HintCard(
            text = "未获取 root 权限。DufsBox 是 KernelSU 模块，请在 KernelSU 管理器中为本应用授予 root，然后点击「重试」。",
            tone = MaterialTheme.colorScheme.errorContainer,
            contentColor = MaterialTheme.colorScheme.onErrorContainer,
        )

        ConnectionState.ModuleMissing -> HintCard(
            text = "未检测到 DufsBox 模块（/data/adb/modules/dufsbox）。请先在 KernelSU 中安装模块并重启设备。",
            tone = MaterialTheme.colorScheme.errorContainer,
            contentColor = MaterialTheme.colorScheme.onErrorContainer,
        )

        is ConnectionState.Error -> Column(
            verticalArrangement = Arrangement.spacedBy(6.dp),
            modifier = Modifier.fillMaxWidth(),
        ) {
            HintCard(
                text = "无法与 dufsboxd 通信：${connection.message}",
                tone = MaterialTheme.colorScheme.errorContainer,
                contentColor = MaterialTheme.colorScheme.onErrorContainer,
            )
            TextButton(onClick = onRetry) { Text("重试") }
        }
    }
}

/* ------------------------------------------------------------------------------------------- */
/* Master control                                                                               */
/* ------------------------------------------------------------------------------------------- */

@Composable
private fun MasterControlCard(
    state: MainUiState,
    config: ConfigInfo,
    busy: Boolean,
    onServiceAction: (String) -> Unit,
) {
    var confirmStop by rememberSaveable { mutableStateOf(false) }

    val service = state.status?.service
    val running = service?.running == true
    val stateText = when {
        service == null -> "未知"
        running -> "运行中"
        service.state.equals("disabled", ignoreCase = true) -> "已停止"
        service.state.isBlank() || service.state == "unknown" -> "未知"
        else -> "已停止（${service.state}）"
    }

    SectionCard(
        title = "服务控制",
        subtitle = if (busy) "正在执行操作…" else "通过 root shell 调用 dufsboxd ctl service.set",
    ) {
        Row(verticalAlignment = Alignment.CenterVertically) {
            ColoredDot(
                color = when {
                    service == null -> MaterialTheme.colorScheme.outline
                    running -> Color(0xFF2E9E5B)
                    else -> Color(0xFFC62828)
                },
            )
            Spacer(Modifier.width(8.dp))
            Text(
                text = stateText,
                style = MaterialTheme.typography.titleMedium,
                fontWeight = FontWeight.SemiBold,
                color = if (running) Color(0xFF2E9E5B) else MaterialTheme.colorScheme.onSurface,
            )
        }

        Spacer(Modifier.height(8.dp))

        val address = state.status?.addresses?.all?.firstOrNull()
        Text(
            text = buildString {
                append("共享：")
                append(config.sharePath.ifBlank { "—" })
                if (config.port > 0) append("  ·  端口 ${config.port}")
                if (!address.isNullOrBlank()) append("  ·  $address")
            },
            style = MaterialTheme.typography.bodySmall,
            color = MaterialTheme.colorScheme.onSurfaceVariant,
        )
        if (running) {
            Text(
                text = "运行时长：" + formatUptime(service.uptimeSec),
                style = MaterialTheme.typography.bodySmall,
                color = MaterialTheme.colorScheme.onSurfaceVariant,
            )
        }
        if (!state.status?.lastError.isNullOrBlank()) {
            Spacer(Modifier.height(6.dp))
            HintCard(
                text = "最近错误：${state.status?.lastError}",
                tone = MaterialTheme.colorScheme.errorContainer,
                contentColor = MaterialTheme.colorScheme.onErrorContainer,
            )
        }

        Spacer(Modifier.height(12.dp))

        Row(horizontalArrangement = Arrangement.spacedBy(8.dp)) {
            Button(
                onClick = { onServiceAction("start") },
                enabled = !busy,
                modifier = Modifier.weight(1f),
            ) {
                Text("启动")
            }
            OutlinedButton(
                onClick = { confirmStop = true },
                enabled = !busy,
                modifier = Modifier.weight(1f),
                colors = ButtonDefaults.outlinedButtonColors(
                    contentColor = MaterialTheme.colorScheme.error,
                ),
            ) {
                Text("停止")
            }
            OutlinedButton(
                onClick = { onServiceAction("restart") },
                enabled = !busy,
                modifier = Modifier.weight(1f),
            ) {
                Text("重启")
            }
        }
    }

    if (confirmStop) {
        ConfirmDialog(
            title = "停止服务？",
            text = "停止后 dufs 与 tailscaled 会被结束，局域网内所有设备将立刻断开连接。重启后是否自动启动取决于「开机自启动」设置。",
            confirmLabel = "立即停止",
            destructive = true,
            onConfirm = {
                confirmStop = false
                onServiceAction("stop")
            },
            onDismiss = { confirmStop = false },
        )
    }
}

/* ------------------------------------------------------------------------------------------- */
/* Devices                                                                                      */
/* ------------------------------------------------------------------------------------------- */

@Composable
private fun DevicesCard(
    devices: List<Device>,
    initialLoadDone: Boolean,
    online: Int,
    onSetPolicy: (String, Policy) -> Unit,
    onForgetDevice: (String) -> Unit,
    onMessage: MessageSink,
) {
    SectionCard(
        title = "连接设备管理",
        subtitle = "在线 $online 台 / 共 ${devices.size} 台",
    ) {
        when {
            devices.isEmpty() && !initialLoadDone -> Text(
                text = "正在读取设备列表…",
                style = MaterialTheme.typography.bodyMedium,
                color = MaterialTheme.colorScheme.onSurfaceVariant,
            )

            devices.isEmpty() -> EmptyState("暂无连接设备")

            else -> Column(modifier = Modifier.fillMaxWidth()) {
                devices.forEachIndexed { index, device ->
                    if (index > 0) HorizontalDivider(
                        modifier = Modifier.padding(vertical = 8.dp),
                        color = MaterialTheme.colorScheme.outlineVariant,
                    )
                    DeviceRow(
                        device = device,
                        onSetPolicy = onSetPolicy,
                        onForgetDevice = onForgetDevice,
                        onMessage = onMessage,
                    )
                }
            }
        }
    }
}

@Composable
private fun DeviceRow(
    device: Device,
    onSetPolicy: (String, Policy) -> Unit,
    onForgetDevice: (String) -> Unit,
    onMessage: MessageSink,
) {
    var menuOpen by remember { mutableStateOf(false) }
    var confirmForget by remember { mutableStateOf(false) }
    val clipboard = LocalClipboardManager.current

    Column(modifier = Modifier.fillMaxWidth()) {
        Row(verticalAlignment = Alignment.CenterVertically) {
            StatusDot(online = device.online)
            Spacer(Modifier.width(8.dp))
            Text(
                text = device.label,
                style = MaterialTheme.typography.titleSmall,
                fontWeight = FontWeight.SemiBold,
                modifier = Modifier.weight(1f),
            )
            TagBadge(
                text = if (device.kind == DeviceKind.TAILNET) "Tailscale" else "局域网",
                container = if (device.kind == DeviceKind.TAILNET) {
                    MaterialTheme.colorScheme.tertiaryContainer
                } else {
                    MaterialTheme.colorScheme.secondaryContainer
                },
                contentColor = if (device.kind == DeviceKind.TAILNET) {
                    MaterialTheme.colorScheme.onTertiaryContainer
                } else {
                    MaterialTheme.colorScheme.onSecondaryContainer
                },
            )
            if (device.activeConns > 0) {
                Spacer(Modifier.width(6.dp))
                TagBadge(
                    text = "连接 ${device.activeConns}",
                    container = MaterialTheme.colorScheme.primaryContainer,
                    contentColor = MaterialTheme.colorScheme.onPrimaryContainer,
                )
            }
            Spacer(Modifier.width(4.dp))
            Box {
                TextButton(onClick = { menuOpen = true }) {
                    Text("⋯", style = MaterialTheme.typography.titleLarge)
                }
                DropdownMenu(expanded = menuOpen, onDismissRequest = { menuOpen = false }) {
                    DropdownMenuItem(
                        text = { Text("复制 IP") },
                        onClick = {
                            menuOpen = false
                            val value = device.ip.ifBlank { device.id }
                            clipboard.setText(AnnotatedString(value))
                            onMessage("已复制 $value")
                        },
                    )
                    DropdownMenuItem(
                        text = { Text("忘记设备") },
                        onClick = {
                            menuOpen = false
                            confirmForget = true
                        },
                    )
                }
            }
        }

        Text(
            text = buildString {
                append("IP ")
                append(device.ip.ifBlank { "—" })
                append("   ·   MAC ")
                append(device.mac.ifBlank { "—" })
            },
            style = MaterialTheme.typography.bodySmall.copy(fontFamily = FontFamily.Monospace),
            color = MaterialTheme.colorScheme.onSurfaceVariant,
        )
        Text(
            text = "最近在线：${relativeTime(device.lastSeen)}" +
                if (device.currentPath.isNotBlank()) "   ·   当前 ${device.currentPath}" else "",
            style = MaterialTheme.typography.bodySmall,
            color = MaterialTheme.colorScheme.onSurfaceVariant,
        )
        Text(
            text = "请求 ${device.requests}   ·   上行 ${formatBytes(device.bytesIn)}   ·   " +
                "下行 ${formatBytes(device.bytesOut)}   ·   拒绝 ${device.denied}",
            style = MaterialTheme.typography.bodySmall,
            color = MaterialTheme.colorScheme.onSurfaceVariant,
        )

        Spacer(Modifier.height(8.dp))

        PermissionSelector(
            selected = device.policy,
            isDefaultSource = device.isDefaultPolicy,
            onSelect = { policy -> onSetPolicy(device.id, policy) },
        )

        if (!device.isDefaultPolicy) {
            Spacer(Modifier.height(4.dp))
            Text(
                text = "已按 ${sourceLabel(device.policySource)} 应用显式规则",
                style = MaterialTheme.typography.labelSmall,
                color = MaterialTheme.colorScheme.onSurfaceVariant,
            )
        }
    }

    if (confirmForget) {
        ConfirmDialog(
            title = "忘记设备？",
            text = "将从设备列表与统计中移除「${device.label}」。已配置的权限规则不会被删除（如需清除请在下次该设备连接时改为「拒绝」或「默认」）。",
            confirmLabel = "忘记",
            destructive = true,
            onConfirm = {
                confirmForget = false
                onForgetDevice(device.id)
            },
            onDismiss = { confirmForget = false },
        )
    }
}

private fun sourceLabel(source: String): String = when (source) {
    "mac" -> "MAC 地址"
    "ip" -> "IP 地址"
    "cidr" -> "网段"
    else -> "默认策略"
}

/**
 * Compact 3-way permission control (拒绝 / 只读 / 可写) built on
 * [SingleChoiceSegmentedButtonRow].
 *
 * `Policy.DEFAULT` (the daemon's "no explicit rule") has no segment of its own, so it falls
 * back to highlighting 可写. [isDefaultSource] adds the explanatory caption underneath; any tap
 * writes a real rule through `acl.set`, which is exactly what the daemon expects.
 */
@OptIn(ExperimentalMaterial3Api::class)
@Composable
fun PermissionSelector(
    selected: Policy,
    isDefaultSource: Boolean,
    onSelect: (Policy) -> Unit,
    modifier: Modifier = Modifier,
    enabled: Boolean = true,
) {
    val options = Policy.selectable
    val selectedIndex = options.indexOf(selected).takeIf { it >= 0 } ?: 2

    SingleChoiceSegmentedButtonRow(modifier = modifier.fillMaxWidth()) {
        options.forEachIndexed { index, policy ->
            SegmentedButton(
                selected = index == selectedIndex,
                onClick = { onSelect(policy) },
                enabled = enabled,
                shape = SegmentedButtonDefaults.itemShape(index, options.size),
                label = {
                    Text(
                        text = policy.label,
                        style = MaterialTheme.typography.labelMedium,
                        fontWeight = if (index == selectedIndex) FontWeight.SemiBold else FontWeight.Normal,
                    )
                },
                modifier = Modifier.weight(1f),
            )
        }
    }

    if (isDefaultSource) {
        Spacer(Modifier.height(4.dp))
        Text(
            text = "当前未设置显式规则，正在使用「默认设备权限」",
            style = MaterialTheme.typography.labelSmall,
            color = MaterialTheme.colorScheme.onSurfaceVariant,
        )
    }
}
