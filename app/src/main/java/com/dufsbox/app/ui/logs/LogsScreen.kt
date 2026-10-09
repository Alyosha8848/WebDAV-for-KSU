package com.dufsbox.app.ui.logs

import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.Spacer
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.width
import androidx.compose.foundation.lazy.LazyColumn
import androidx.compose.foundation.lazy.items
import androidx.compose.foundation.lazy.rememberLazyListState
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.material3.ExperimentalMaterial3Api
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Surface
import androidx.compose.material3.Text
import androidx.compose.material3.TextButton
import androidx.compose.material3.TopAppBar
import androidx.compose.material3.TopAppBarDefaults
import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.derivedStateOf
import androidx.compose.runtime.getValue
import androidx.compose.runtime.remember
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.platform.LocalClipboardManager
import androidx.compose.ui.text.AnnotatedString
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.unit.dp
import com.dufsbox.app.data.model.LogEntry
import com.dufsbox.app.ui.LogLevelFilter
import com.dufsbox.app.ui.MainUiState
import com.dufsbox.app.ui.MessageSink
import com.dufsbox.app.ui.components.ChipRow
import com.dufsbox.app.ui.components.EmptyState
import com.dufsbox.app.ui.components.LogLevelStyle
import com.dufsbox.app.ui.components.MonoText
import com.dufsbox.app.ui.components.color
import kotlinx.coroutines.launch

/** Tab 2 — 日志: level chips + monospace auto-following list. */
@OptIn(ExperimentalMaterial3Api::class)
@Composable
fun LogsScreen(
    state: MainUiState,
    onSetFilter: (LogLevelFilter) -> Unit,
    onClear: () -> Unit,
    onRefresh: () -> Unit,
    onMessage: MessageSink,
) {
    val entries = state.filteredLogs
    val listState = rememberLazyListState()
    val clipboard = LocalClipboardManager.current

    // Follow the tail only while the user has not scrolled up.
    val atBottom by remember {
        derivedStateOf {
            val lastVisible = listState.layoutInfo.visibleItemsInfo.lastOrNull()?.index ?: 0
            val total = listState.layoutInfo.totalItemsCount
            total == 0 || lastVisible >= total - 2
        }
    }

    LaunchedEffect(entries.size) {
        val total = listState.layoutInfo.totalItemsCount
        if (total > 0 && atBottom) {
            listState.scrollToItem(total - 1)
        }
    }

    Column(modifier = Modifier.fillMaxSize()) {
        TopAppBar(
            title = { Text("日志") },
            actions = {
                TextButton(onClick = onRefresh) { Text("刷新") }
                TextButton(onClick = onClear) { Text("清空") }
                TextButton(
                    onClick = {
                        val text = entries.joinToString("\n") { entry ->
                            "${entry.ts} [${entry.normalizedLevel.uppercase()}] ${entry.src} ${entry.msg}"
                        }
                        clipboard.setText(AnnotatedString(text))
                        onMessage("已复制 ${entries.size} 条日志")
                    },
                    enabled = entries.isNotEmpty(),
                ) { Text("复制全部") }
            },
            colors = TopAppBarDefaults.topAppBarColors(
                containerColor = MaterialTheme.colorScheme.background,
            ),
        )

        ChipRow(
            options = LogLevelFilter.chips,
            selected = state.logFilter,
            label = { filter -> if (filter == LogLevelFilter.ALL) "全部(TRACE)" else filter.label },
            onSelect = onSetFilter,
            chipColor = { filter -> logStyleOf(filter).color() },
            modifier = Modifier.padding(horizontal = 16.dp, vertical = 4.dp),
        )

        Row(
            modifier = Modifier
                .fillMaxWidth()
                .padding(horizontal = 16.dp, vertical = 4.dp),
            horizontalArrangement = Arrangement.SpaceBetween,
            verticalAlignment = Alignment.CenterVertically,
        ) {
            Text(
                text = "共 ${entries.size} 条（本地缓存上限 ${com.dufsbox.app.ui.LOG_BUFFER_LIMIT} 条）",
                style = MaterialTheme.typography.labelSmall,
                color = MaterialTheme.colorScheme.onSurfaceVariant,
            )
            Text(
                text = "级别：${state.logFilter.label}",
                style = MaterialTheme.typography.labelSmall,
                color = MaterialTheme.colorScheme.onSurfaceVariant,
            )
        }

        if (entries.isEmpty()) {
            EmptyState("暂无日志")
        } else {
            LazyColumn(
                state = listState,
                modifier = Modifier
                    .fillMaxSize()
                    .padding(horizontal = 12.dp),
            ) {
                items(items = entries, key = { it.key }) { entry ->
                    LogLine(entry = entry)
                }
            }
        }
    }
}

@Composable
private fun LogLine(entry: LogEntry) {
    val style = LogLevelStyle.from(entry.normalizedLevel)
    val color = style.color()

    Row(
        modifier = Modifier
            .fillMaxWidth()
            .padding(vertical = 3.dp),
        verticalAlignment = Alignment.Top,
    ) {
        MonoText(
            text = entry.timeText,
            color = MaterialTheme.colorScheme.onSurfaceVariant,
            fontSize = 11,
        )
        Spacer(Modifier.width(6.dp))
        Surface(
            color = color.copy(alpha = 0.18f),
            shape = RoundedCornerShape(4.dp),
        ) {
            Text(
                text = style.badge,
                modifier = Modifier.padding(horizontal = 4.dp, vertical = 1.dp),
                style = MaterialTheme.typography.labelSmall,
                color = color,
                fontWeight = FontWeight.SemiBold,
            )
        }
        Spacer(Modifier.width(6.dp))
        Column(modifier = Modifier.weight(1f)) {
            if (entry.src.isNotBlank()) {
                Text(
                    text = entry.src,
                    style = MaterialTheme.typography.labelSmall,
                    color = MaterialTheme.colorScheme.onSurfaceVariant,
                )
            }
            MonoText(text = entry.msg, fontSize = 12)
        }
    }
}

/** [LogLevelFilter] ordinal == [LogLevelStyle] ordinal by construction. */
internal fun logStyleOf(filter: LogLevelFilter): LogLevelStyle =
    LogLevelStyle.options.getOrElse(filter.ordinal) { LogLevelStyle.TRACE }
