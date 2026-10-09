package com.dufsbox.app.ui.status

import android.content.Intent
import androidx.compose.foundation.layout.Arrangement
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
import androidx.compose.material3.ExperimentalMaterial3Api
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.OutlinedButton
import androidx.compose.material3.Text
import androidx.compose.material3.TextButton
import androidx.compose.material3.TopAppBar
import androidx.compose.material3.TopAppBarDefaults
import androidx.compose.runtime.Composable
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.rememberCoroutineScope
import androidx.compose.runtime.saveable.rememberSaveable
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.platform.LocalContext
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.unit.dp
import androidx.core.net.toUri
import com.dufsbox.app.data.RootShell
import com.dufsbox.app.data.model.Status
import com.dufsbox.app.data.model.TailscaleStatus
import com.dufsbox.app.ui.ConnectionState
import com.dufsbox.app.ui.MainUiState
import com.dufsbox.app.ui.MessageSink
import com.dufsbox.app.ui.ScreenContentPadding
import com.dufsbox.app.ui.components.ColoredDot
import com.dufsbox.app.ui.components.ConfirmDialog
import com.dufsbox.app.ui.components.HintCard
import com.dufsbox.app.ui.components.InfoRow
import com.dufsbox.app.ui.components.SectionCard
import com.dufsbox.app.ui.components.SwitchRow
import com.dufsbox.app.ui.components.TagBadge
import com.dufsbox.app.ui.components.formatBool
import com.dufsbox.app.ui.components.formatBytes
import com.dufsbox.app.ui.components.formatUptime
import kotlinx.coroutines.launch

/** Tab 3 — 状态: everything the daemon reports about itself. */
@OptIn(ExperimentalMaterial3Api::class)
@Composable
fun StatusScreen(
    state: MainUiState,
    cachedPassword: String,
    onMessage: MessageSink,
    onTailscaleAction: suspend (String) -> Unit,
    onRetry: () -> Unit,
    modulePath: String = RootShell.MOD_PATH,
) {
    val status = state.status

    Column(modifier = Modifier.fillMaxWidth()) {
        TopAppBar(
            title = { Text("状态") },
            actions = { TextButton(onClick = onRetry) { Text("刷新") } },
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
            if (status == null) {
                val message = when (val connection = state.connection) {
                    is ConnectionState.NoRoot -> "未获取 root 权限，无法读取服务状态。"
                    is ConnectionState.ModuleMissing -> "未检测到 DufsBox 模块。"
                    is ConnectionState.Error -> "无法读取状态：${connection.message}"
                    ConnectionState.Connecting -> "正在读取状态…"
                    ConnectionState.Ok -> "正在读取状态…"
                }
                HintCard(text = message)
                return@Column
            }

            ServiceCard(status = status)
            ShareCard(status = status, onMessage = onMessage)
            AddressCard(status = status, onMessage = onMessage)
            AuthCard(status = status, cachedPassword = cachedPassword, onMessage = onMessage)
            DufsCard(status = status)
            TailscaleCard(
                tailscale = status.tailscale,
                onTailscaleAction = onTailscaleAction,
                onMessage = onMessage,
            )
            StatsCard(status = status)
            HealthCard(status = status)
            AboutBackendCard(state = state, modulePath = modulePath, onMessage = onMessage)
            Spacer(Modifier.height(4.dp))
        }
    }
}

/* ------------------------------------------------------------------------------------------- */

@Composable
private fun ServiceCard(status: Status) {
    val service = status.service
    SectionCard(title = "服务状态") {
        Row(verticalAlignment = Alignment.CenterVertically) {
            ColoredDot(
                color = if (service.running) Color(0xFF2E9E5B) else Color(0xFFC62828),
            )
            Spacer(Modifier.width(8.dp))
            Text(
                text = if (service.running) "运行中" else "已停止",
                style = MaterialTheme.typography.titleMedium,
                fontWeight = FontWeight.SemiBold,
            )
            if (service.state.isNotBlank() && service.state != "unknown") {
                Spacer(Modifier.width(8.dp))
                TagBadge(text = service.state)
            }
        }
        Spacer(Modifier.height(8.dp))
        InfoRow("运行时长", formatUptime(service.uptimeSec))
        InfoRow("守护进程 PID", if (service.pid > 0) service.pid.toString() else "—", monospace = true)
        InfoRow("后端版本", service.version.ifBlank { "—" }, monospace = true)
        InfoRow("端口绑定", formatBool(status.health.portBindable))
    }
}

@Composable
private fun ShareCard(status: Status, onMessage: MessageSink) {
    val config = status.config
    SectionCard(title = "共享") {
        InfoRow(
            label = "共享路径",
            value = config.sharePath.ifBlank { "—" },
            monospace = true,
            copyable = true,
            onCopied = { onMessage("已复制路径：$it") },
        )
        InfoRow(
            label = "共享名称",
            value = config.shareName.ifBlank { "—" },
            copyable = true,
            onCopied = { onMessage("已复制名称：$it") },
        )
        InfoRow(label = "监听端口", value = if (config.port > 0) config.port.toString() else "—")
        InfoRow(label = "共享方式", value = config.modeLabel)
        InfoRow(label = "全局只读", value = formatBool(config.readonlyGlobal))
        InfoRow(label = "默认设备权限", value = config.defaultPolicy.label)
        InfoRow(label = "开机自启动", value = formatBool(config.autostart))
    }
}

@Composable
private fun AddressCard(status: Status, onMessage: MessageSink) {
    val addresses = status.addresses
    SectionCard(title = "连接地址", subtitle = "点击任意地址即可复制") {
        val lan = addresses.lan
        val tailnet = addresses.tailnet

        Row(verticalAlignment = Alignment.CenterVertically) {
            TagBadge(text = "局域网")
        }
        if (lan.isEmpty()) {
            Text(
                text = "局域网未连接",
                style = MaterialTheme.typography.bodySmall,
                color = MaterialTheme.colorScheme.onSurfaceVariant,
            )
        } else {
            lan.forEach { address ->
                InfoRow(
                    label = "LAN",
                    value = address,
                    monospace = true,
                    copyable = true,
                    onCopied = { onMessage("已复制 $it") },
                )
            }
        }

        Spacer(Modifier.height(8.dp))
        Row(verticalAlignment = Alignment.CenterVertically) {
            TagBadge(
                text = "Tailscale",
                container = MaterialTheme.colorScheme.tertiaryContainer,
                contentColor = MaterialTheme.colorScheme.onTertiaryContainer,
            )
        }
        if (tailnet.isEmpty()) {
            Text(
                text = "Tailscale 未连接",
                style = MaterialTheme.typography.bodySmall,
                color = MaterialTheme.colorScheme.onSurfaceVariant,
            )
        } else {
            tailnet.forEach { address ->
                InfoRow(
                    label = "TS",
                    value = address,
                    monospace = true,
                    copyable = true,
                    onCopied = { onMessage("已复制 $it") },
                )
            }
        }

        if (addresses.loopback.isNotEmpty()) {
            Spacer(Modifier.height(8.dp))
            addresses.loopback.forEach { address ->
                InfoRow(
                    label = "本机",
                    value = address,
                    monospace = true,
                    copyable = true,
                    onCopied = { onMessage("已复制 $it") },
                )
            }
        }
    }
}

@Composable
private fun AuthCard(
    status: Status,
    cachedPassword: String,
    onMessage: MessageSink,
) {
    val config = status.config
    var revealed by rememberSaveable { mutableStateOf(false) }

    SectionCard(
        title = "认证",
        subtitle = if (config.isAnonymous) "匿名访问" else "账号密码",
    ) {
        InfoRow(label = "认证模式", value = if (config.isAnonymous) "匿名访问" else "账号密码")
        InfoRow(
            label = "账号",
            value = if (config.isAnonymous) "—" else config.username.ifBlank { "—" },
            copyable = !config.isAnonymous && config.username.isNotBlank(),
            onCopied = { onMessage("已复制账号：$it") },
        )

        // The daemon never returns the password (§3.3) — it only reports `password_set`.
        // So we can only reveal what the user typed into 设置 on this device.
        val hasBackendPassword = config.passwordSet
        val showReal = revealed && cachedPassword.isNotBlank()

        Row(
            modifier = Modifier.fillMaxWidth(),
            verticalAlignment = Alignment.CenterVertically,
        ) {
            Text(
                text = "密码",
                style = MaterialTheme.typography.bodyMedium,
                color = MaterialTheme.colorScheme.onSurfaceVariant,
            )
            Spacer(Modifier.width(12.dp))
            Text(
                text = when {
                    config.isAnonymous -> "未启用密码认证"
                    showReal -> cachedPassword
                    cachedPassword.isNotBlank() -> "••••••••"
                    else -> "未在 App 中缓存，请在设置中重新填写"
                },
                modifier = Modifier.weight(1f),
                style = MaterialTheme.typography.bodyMedium,
                fontWeight = FontWeight.Medium,
                color = if (!config.isAnonymous && cachedPassword.isBlank()) {
                    MaterialTheme.colorScheme.error
                } else {
                    MaterialTheme.colorScheme.onSurface
                },
            )
            if (!config.isAnonymous && cachedPassword.isNotBlank()) {
                TextButton(onClick = { revealed = !revealed }) {
                    Text(if (revealed) "隐藏" else "显示")
                }
            }
        }

        if (!config.isAnonymous) {
            Spacer(Modifier.height(6.dp))
            HintCard(
                text = if (hasBackendPassword) {
                    "后台已设置密码。出于安全考虑，dufsboxd 不会回传密码明文；此处展示的是你在「设置 → 认证」中填写的本地缓存值。"
                } else {
                    "后台尚未设置密码，建议在「设置 → 认证」中开启账号密码并保存。"
                },
            )
        }
    }
}

@Composable
private fun DufsCard(status: Status) {
    val dufs = status.dufs
    SectionCard(title = "dufs 进程") {
        Row(verticalAlignment = Alignment.CenterVertically) {
            ColoredDot(color = if (dufs.running) Color(0xFF2E9E5B) else Color(0xFFC62828))
            Spacer(Modifier.width(8.dp))
            Text(
                text = if (dufs.running) "运行中" else "未运行",
                style = MaterialTheme.typography.bodyLarge,
            )
        }
        Spacer(Modifier.height(8.dp))
        InfoRow("PID", if (dufs.pid > 0) dufs.pid.toString() else "—", monospace = true)
        InfoRow("监听地址", dufs.bind.ifBlank { "—" }, monospace = true)
        InfoRow("dufs 版本", dufs.version.ifBlank { status.protocol.dufs.ifBlank { "—" } }, monospace = true)
        InfoRow("HTTP 版本", status.protocol.http.ifBlank { "—" }, monospace = true)
        InfoRow("WebDAV", status.protocol.webdav.ifBlank { "—" }, monospace = true)
    }
}

@Composable
private fun TailscaleCard(
    tailscale: TailscaleStatus,
    onTailscaleAction: suspend (String) -> Unit,
    onMessage: MessageSink,
) {
    val context = LocalContext.current
    var confirmLogout by remember { mutableStateOf(false) }
    // The tailnet actions are suspend (they run a CLI round-trip through the root
    // shell), while Compose click handlers are not, so they are launched here.
    val scope = rememberCoroutineScope()

    val stateColor = when (tailscale.state.lowercase()) {
        "online" -> Color(0xFF2E9E5B)
        "needs_login" -> Color(0xFFD08A0E)
        "error" -> Color(0xFFC62828)
        else -> MaterialTheme.colorScheme.outline
    }

    SectionCard(title = "Tailscale 状态") {
        Row(verticalAlignment = Alignment.CenterVertically) {
            ColoredDot(color = stateColor)
            Spacer(Modifier.width(8.dp))
            Text(
                text = tailscale.stateLabel,
                style = MaterialTheme.typography.titleSmall,
                fontWeight = FontWeight.SemiBold,
            )
            if (tailscale.version.isNotBlank()) {
                Spacer(Modifier.width(8.dp))
                TagBadge(text = tailscale.version)
            }
        }
        Spacer(Modifier.height(8.dp))
        InfoRow(
            label = "Tailscale IP",
            value = tailscale.ip.ifBlank { "—" },
            monospace = true,
            copyable = tailscale.ip.isNotBlank(),
            onCopied = { onMessage("已复制 $it") },
        )
        InfoRow(
            label = "MagicDNS",
            value = tailscale.dns.ifBlank { "—" },
            copyable = tailscale.dns.isNotBlank(),
            onCopied = { onMessage("已复制 $it") },
        )
        InfoRow(label = "已启用", value = formatBool(tailscale.enabled))

        SwitchRow(
            title = "使用 HTTPS (tailscale serve)",
            subtitle = "通过 tailscale serve 暴露 HTTPS 端口",
            checked = tailscale.httpsServe,
            enabled = tailscale.online,
            onCheckedChange = { checked ->
                scope.launch { onTailscaleAction(if (checked) "serve_on" else "serve_off") }
            },
        )

        Spacer(Modifier.height(8.dp))

        Row(horizontalArrangement = Arrangement.spacedBy(8.dp)) {
            if (tailscale.needsLogin) {
                Button(
                    onClick = {
                        if (tailscale.authUrl.isBlank()) {
                            scope.launch { onTailscaleAction("up") }
                        } else {
                            val intent = Intent(Intent.ACTION_VIEW, tailscale.authUrl.toUri())
                            runCatching { context.startActivity(intent) }
                                .onFailure { onMessage("无法打开登录链接") }
                        }
                    },
                    modifier = Modifier.weight(1f),
                ) {
                    Text("连接 / 登录")
                }
            } else {
                Button(
                    onClick = { scope.launch { onTailscaleAction("up") } },
                    modifier = Modifier.weight(1f),
                ) {
                    Text("连接")
                }
            }
            OutlinedButton(
                onClick = { confirmLogout = true },
                modifier = Modifier.weight(1f),
            ) {
                Text("退出登录")
            }
        }

        if (tailscale.needsLogin && tailscale.authUrl.isNotBlank()) {
            Spacer(Modifier.height(8.dp))
            InfoRow(
                label = "登录链接",
                value = tailscale.authUrl,
                monospace = true,
                copyable = true,
                onCopied = { onMessage("已复制登录链接") },
            )
        }
    }

    if (confirmLogout) {
        ConfirmDialog(
            title = "退出 Tailscale 登录？",
            text = "将执行 tailscale logout，本机需要重新登录 Tailscale 才能继续使用 tailnet 共享。",
            confirmLabel = "退出登录",
            destructive = true,
            onConfirm = {
                confirmLogout = false
                scope.launch { onTailscaleAction("logout") }
            },
            onDismiss = { confirmLogout = false },
        )
    }
}

@Composable
private fun StatsCard(status: Status) {
    val stats = status.stats
    SectionCard(title = "统计") {
        InfoRow("设备数", "${stats.devices}")
        InfoRow("在线设备", "${stats.online}")
        InfoRow("活跃连接", "${stats.activeConns}")
        InfoRow("累计请求", "${stats.requests}")
        InfoRow("被拒绝", "${stats.denied}")
        InfoRow("上行流量", formatBytes(stats.bytesIn))
        InfoRow("下行流量", formatBytes(stats.bytesOut))
    }
}

@Composable
private fun HealthCard(status: Status) {
    val health = status.health
    SectionCard(title = "健康检查") {
        InfoRow(
            label = "共享路径存在",
            value = formatBool(health.sharePathExists),
            valueColor = if (health.sharePathExists) {
                MaterialTheme.colorScheme.onSurface
            } else {
                MaterialTheme.colorScheme.error
            },
        )
        InfoRow(
            label = "共享路径可写",
            value = formatBool(health.sharePathWritable),
            valueColor = if (health.sharePathWritable) {
                MaterialTheme.colorScheme.onSurface
            } else {
                MaterialTheme.colorScheme.onSurfaceVariant
            },
        )
        InfoRow("端口可绑定", formatBool(health.portBindable))
        if (status.lastError.isNotBlank()) {
            Spacer(Modifier.height(8.dp))
            HintCard(
                text = "最近错误：${status.lastError}",
                tone = MaterialTheme.colorScheme.errorContainer,
                contentColor = MaterialTheme.colorScheme.onErrorContainer,
            )
        }
    }
}

@Composable
private fun AboutBackendCard(
    state: MainUiState,
    modulePath: String,
    onMessage: MessageSink,
) {
    val version = state.version
    val updated = state.lastUpdated
    SectionCard(title = "后端") {
        InfoRow("后端版本", version?.version ?: "未知", monospace = true)
        InfoRow("dufs 版本", version?.dufs ?: "未知", monospace = true)
        InfoRow("Go 版本", version?.go ?: "未知", monospace = true)
        InfoRow("接口协议", "v${version?.protocol ?: 1}")
        InfoRow(
            label = "最后刷新",
            value = if (updated > 0) "刚刚" else "—",
        )
        InfoRow(
            label = "模块路径",
            value = modulePath,
            monospace = true,
            copyable = true,
            onCopied = { onMessage("已复制路径") },
        )
        if (state.connection is ConnectionState.Error) {
            Spacer(Modifier.height(6.dp))
            Text(
                text = "连接异常：${(state.connection as ConnectionState.Error).message}",
                style = MaterialTheme.typography.bodySmall,
                color = MaterialTheme.colorScheme.error,
            )
        }
    }
}
