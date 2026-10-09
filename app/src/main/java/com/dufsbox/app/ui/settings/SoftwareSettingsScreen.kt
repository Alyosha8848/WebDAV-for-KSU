package com.dufsbox.app.ui.settings

import android.content.Intent
import android.net.Uri
import android.os.Build
import android.provider.Settings
import androidx.compose.foundation.background
import androidx.compose.foundation.clickable
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.Spacer
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.rememberScrollState
import androidx.compose.foundation.shape.CircleShape
import androidx.compose.foundation.verticalScroll
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.automirrored.filled.ArrowBack
import androidx.compose.material3.ExperimentalMaterial3Api
import androidx.compose.material3.Icon
import androidx.compose.material3.IconButton
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Text
import androidx.compose.material3.TopAppBar
import androidx.compose.material3.TopAppBarDefaults
import androidx.compose.runtime.Composable
import androidx.compose.runtime.getValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.draw.clip
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.graphics.luminance
import androidx.compose.ui.platform.LocalContext
import androidx.compose.ui.unit.dp
import androidx.core.net.toUri
import androidx.lifecycle.compose.collectAsStateWithLifecycle
import com.dufsbox.app.data.RootShell
import com.dufsbox.app.data.model.VersionInfo
import com.dufsbox.app.settings.SoftwareSettingsViewModel
import com.dufsbox.app.ui.ScreenContentPadding
import com.dufsbox.app.ui.components.ActionRow
import com.dufsbox.app.ui.components.HintCard
import com.dufsbox.app.ui.components.InfoRow
import com.dufsbox.app.ui.components.SectionCard
import com.dufsbox.app.ui.components.SelectorRow
import com.dufsbox.app.ui.components.SwitchRow
import com.dufsbox.app.ui.components.swatchOutline
import com.dufsbox.app.ui.theme.AccentOption
import com.dufsbox.app.ui.theme.ThemeMode

/** 软件设置 sub-page: theme, accent colour, about. */
@OptIn(ExperimentalMaterial3Api::class)
@Composable
fun SettingsSubScreen(
    softwareVm: SoftwareSettingsViewModel,
    version: VersionInfo?,
    onBack: () -> Unit,
) {
    val theme by softwareVm.theme.collectAsStateWithLifecycle()
    val context = LocalContext.current

    Column(modifier = Modifier.fillMaxWidth()) {
        TopAppBar(
            title = { Text("软件设置") },
            navigationIcon = {
                IconButton(onClick = onBack) {
                    Icon(Icons.AutoMirrored.Filled.ArrowBack, contentDescription = "返回")
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
            SectionCard(title = "外观") {
                SelectorRow(
                    label = "主题",
                    options = enumValues<ThemeMode>().toList(),
                    selected = theme.themeMode,
                    optionLabel = { it.label },
                    onSelect = softwareVm::setThemeMode,
                )

                Spacer(Modifier.height(8.dp))
                SwitchRow(
                    title = "动态取色 (Material You)",
                    subtitle = if (softwareVm.dynamicColorSupported) {
                        "跟随系统壁纸生成配色"
                    } else {
                        "需要 Android 12 (API 31) 及以上，当前设备不支持"
                    },
                    checked = theme.dynamicColor && softwareVm.dynamicColorSupported,
                    enabled = softwareVm.dynamicColorSupported,
                    onCheckedChange = softwareVm::setDynamicColor,
                )
                if (!softwareVm.dynamicColorSupported) {
                    Spacer(Modifier.height(6.dp))
                    HintCard(
                        text = "当前系统版本为 Android ${Build.VERSION.RELEASE}（API ${Build.VERSION.SDK_INT}），" +
                            "低于 Android 12，无法使用动态取色。关闭后可手动选择下面的强调色。",
                    )
                }

                Spacer(Modifier.height(10.dp))
                Text(
                    text = "强调色",
                    style = MaterialTheme.typography.bodyLarge,
                    color = if (theme.dynamicColor && softwareVm.dynamicColorSupported) {
                        MaterialTheme.colorScheme.onSurfaceVariant
                    } else {
                        MaterialTheme.colorScheme.onSurface
                    },
                )
                Spacer(Modifier.height(8.dp))
                Row(
                    horizontalArrangement = Arrangement.spacedBy(12.dp),
                    verticalAlignment = Alignment.CenterVertically,
                ) {
                    enumValues<AccentOption>().forEach { accent ->
                        AccentSwatch(
                            accent = accent,
                            selected = theme.accent == accent,
                            onClick = {
                                softwareVm.setAccent(accent)
                                if (theme.dynamicColor && softwareVm.dynamicColorSupported) {
                                    softwareVm.setDynamicColor(false)
                                }
                            },
                        )
                    }
                }
            }

            SectionCard(title = "关于") {
                InfoRow("应用版本", softwareVm.appVersion, monospace = true)
                InfoRow("应用包名", softwareVm.packageName, monospace = true)
                InfoRow("模块版本", version?.version ?: "未知", monospace = true)
                InfoRow("后端版本", version?.dufs ?: "未知", monospace = true)
                InfoRow("接口协议", "v${version?.protocol ?: 1}")
                InfoRow("模块路径", RootShell.MOD_PATH, monospace = true)
                Spacer(Modifier.height(6.dp))
                HintCard(
                    text = "本应用不联网检查更新。请通过 KernelSU 管理器更新 DufsBox 模块，" +
                        "或在模块仓库中查看最新版本后再覆盖安装。",
                )
            }

            SectionCard(title = "系统") {
                ActionRow(
                    title = "打开系统应用设置",
                    subtitle = "权限、通知与存储",
                    onClick = {
                        val intent = Intent(
                            Settings.ACTION_APPLICATION_DETAILS_SETTINGS,
                            Uri.fromParts("package", softwareVm.packageName, null),
                        )
                        runCatching { context.startActivity(intent) }
                            .onFailure {
                                // Fall back to the generic app-settings screen.
                                runCatching {
                                    context.startActivity(
                                        Intent(Settings.ACTION_APPLICATION_DETAILS_SETTINGS)
                                            .setData("package:${softwareVm.packageName}".toUri()),
                                    )
                                }
                            }
                    },
                )
            }

            Spacer(Modifier.height(4.dp))
        }
    }
}

@Composable
private fun AccentSwatch(
    accent: AccentOption,
    selected: Boolean,
    onClick: () -> Unit,
) {
    val isDark = MaterialTheme.colorScheme.background.luminance() < 0.5f
    val color = if (isDark) accent.dark else accent.light
    val ring = MaterialTheme.colorScheme.onSurface

    Box(
        modifier = Modifier
            .size(38.dp)
            .clip(CircleShape)
            .background(color)
            .then(if (selected) Modifier.swatchOutline(ring) else Modifier)
            .clickable(onClick = onClick),
        contentAlignment = Alignment.Center,
    ) {
        if (selected) {
            Box(
                modifier = Modifier
                    .size(10.dp)
                    .clip(CircleShape)
                    .background(MaterialTheme.colorScheme.surface),
            )
        }
    }
}
