package com.dufsbox.app.ui.settings

import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.FlowRow
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.Spacer
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.layout.width
import androidx.compose.foundation.lazy.LazyColumn
import androidx.compose.foundation.lazy.items
import androidx.compose.material3.AlertDialog
import androidx.compose.material3.AssistChip
import androidx.compose.material3.CircularProgressIndicator
import androidx.compose.material3.ExperimentalMaterial3Api
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.OutlinedButton
import androidx.compose.material3.OutlinedTextField
import androidx.compose.material3.Text
import androidx.compose.material3.TextButton
import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableIntStateOf
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.saveable.rememberSaveable
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.text.input.PasswordVisualTransformation
import androidx.compose.ui.text.input.VisualTransformation
import androidx.compose.ui.unit.dp
import com.dufsbox.app.data.Backend
import com.dufsbox.app.data.model.BrowseEntry
import com.dufsbox.app.ui.MessageSink
import com.dufsbox.app.ui.components.EmptyState
import com.dufsbox.app.ui.components.MonoText
import com.dufsbox.app.ui.components.formatBytes
import org.json.JSONObject

/** Quick-jump targets offered above the directory list. */
private val QUICK_JUMPS = listOf(
    "/sdcard",
    "/storage/emulated/0",
    "/data",
    "/",
)

/**
 * In-app root directory picker driven by `browse` (§4.10).
 *
 * It calls [Backend] directly (not through the ViewModel) so the dialog owns its own
 * loading/error state; every call is wrapped in `runCatching`, so an unreadable directory
 * renders an inline message instead of crashing.
 */
@OptIn(ExperimentalMaterial3Api::class)
@Composable
fun RootBrowserDialog(
    initialPath: String,
    onDismiss: () -> Unit,
    onConfirm: (String) -> Unit,
    onMessage: MessageSink,
) {
    var currentPath by rememberSaveable {
        mutableStateOf(initialPath.ifBlank { "/sdcard" })
    }
    var entries by remember { mutableStateOf<List<BrowseEntry>>(emptyList()) }
    var parent by remember { mutableStateOf("") }
    var loading by remember { mutableStateOf(true) }
    var error by remember { mutableStateOf<String?>(null) }
    var reloadToken by remember { mutableIntStateOf(0) }

    LaunchedEffect(currentPath, reloadToken) {
        loading = true
        error = null
        runCatching { Backend.browse(currentPath) }
            .onSuccess { result ->
                entries = result.entries
                parent = result.parent
                if (!result.exists) {
                    error = "目录不存在：${result.path.ifBlank { currentPath }}"
                }
                loading = false
            }
            .onFailure { failure ->
                entries = emptyList()
                parent = ""
                error = failure.message ?: "无法读取目录"
                loading = false
            }
    }

    AlertDialog(
        onDismissRequest = onDismiss,
        title = { Text("选择共享路径") },
        text = {
            Column(modifier = Modifier.fillMaxWidth()) {
                MonoText(
                    text = currentPath,
                    color = MaterialTheme.colorScheme.primary,
                    fontSize = 12,
                )
                Spacer(Modifier.height(8.dp))

                Row(horizontalArrangement = Arrangement.spacedBy(6.dp)) {
                    OutlinedButton(
                        onClick = { if (parent.isNotBlank()) currentPath = parent },
                        enabled = parent.isNotBlank(),
                        modifier = Modifier.weight(1f),
                    ) {
                        Text("上一级")
                    }
                    OutlinedButton(
                        onClick = { reloadToken += 1 },
                        modifier = Modifier.weight(1f),
                    ) {
                        Text("刷新")
                    }
                }

                Spacer(Modifier.height(8.dp))
                FlowRow(
                    horizontalArrangement = Arrangement.spacedBy(6.dp),
                    verticalArrangement = Arrangement.spacedBy(6.dp),
                ) {
                    QUICK_JUMPS.forEach { jump ->
                        AssistChip(
                            onClick = { currentPath = jump },
                            label = { Text(jump, style = MaterialTheme.typography.labelSmall) },
                        )
                    }
                }

                Spacer(Modifier.height(8.dp))

                when {
                    loading -> Row(
                        verticalAlignment = Alignment.CenterVertically,
                        modifier = Modifier.padding(vertical = 12.dp),
                    ) {
                        CircularProgressIndicator(modifier = Modifier.size(18.dp))
                        Spacer(Modifier.width(10.dp))
                        Text("正在读取…", style = MaterialTheme.typography.bodySmall)
                    }

                    error != null -> Text(
                        text = error.orEmpty(),
                        style = MaterialTheme.typography.bodySmall,
                        color = MaterialTheme.colorScheme.error,
                    )

                    entries.isEmpty() -> EmptyState("该目录下没有子目录")

                    else -> LazyColumn(
                        modifier = Modifier
                            .fillMaxWidth()
                            .height(280.dp),
                    ) {
                        items(entries) { entry ->
                            Row(
                                modifier = Modifier
                                    .fillMaxWidth()
                                    .padding(vertical = 8.dp),
                                verticalAlignment = Alignment.CenterVertically,
                            ) {
                                Text(
                                    text = if (entry.isDir) "📁" else "📄",
                                    style = MaterialTheme.typography.bodyLarge,
                                )
                                Spacer(Modifier.width(8.dp))
                                Column(modifier = Modifier.weight(1f)) {
                                    Text(
                                        text = entry.name,
                                        style = MaterialTheme.typography.bodyMedium,
                                        fontWeight = if (entry.isDir) {
                                            FontWeight.Medium
                                        } else {
                                            FontWeight.Normal
                                        },
                                    )
                                    if (!entry.isDir) {
                                        Text(
                                            text = formatBytes(entry.size),
                                            style = MaterialTheme.typography.labelSmall,
                                            color = MaterialTheme.colorScheme.onSurfaceVariant,
                                        )
                                    }
                                }
                                if (entry.isDir) {
                                    TextButton(onClick = { currentPath = entry.path }) {
                                        Text("进入")
                                    }
                                }
                            }
                        }
                    }
                }
            }
        },
        confirmButton = {
            TextButton(
                onClick = { onConfirm(currentPath) },
                enabled = !loading && error == null,
            ) {
                Text("确定", fontWeight = FontWeight.SemiBold)
            }
        },
        dismissButton = {
            TextButton(onClick = onDismiss) { Text("取消") }
        },
    )
}

/** Password field with an eye toggle, shared by 认证 settings and the 状态 tab. */
@Composable
internal fun PasswordField(
    value: String,
    onValueChange: (String) -> Unit,
    label: String = "密码",
    modifier: Modifier = Modifier,
    enabled: Boolean = true,
    supportingText: String? = null,
) {
    var visible by rememberSaveable { mutableStateOf(false) }
    OutlinedTextField(
        value = value,
        onValueChange = onValueChange,
        label = { Text(label) },
        singleLine = true,
        enabled = enabled,
        visualTransformation = if (visible) {
            VisualTransformation.None
        } else {
            PasswordVisualTransformation()
        },
        trailingIcon = {
            TextButton(onClick = { visible = !visible }) {
                Text(if (visible) "隐藏" else "显示", style = MaterialTheme.typography.labelSmall)
            }
        },
        supportingText = supportingText?.let { text -> { Text(text) } },
        modifier = modifier.fillMaxWidth(),
    )
}

/** Builds the `tailscale` sub-object patch for `config.set`. */
internal fun tailscalePatch(
    hostname: String? = null,
    httpsServe: Boolean? = null,
    acceptDns: Boolean? = null,
): JSONObject {
    val tailscale = JSONObject()
    if (hostname != null) tailscale.put("hostname", hostname)
    if (httpsServe != null) tailscale.put("https_serve", httpsServe)
    if (acceptDns != null) tailscale.put("accept_dns", acceptDns)
    return JSONObject().put("tailscale", tailscale)
}
