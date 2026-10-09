package com.dufsbox.app.data

import com.topjohnwu.superuser.Shell
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.withContext

/**
 * Result of one root-shell command.
 *
 * [stderr] is only populated for commands that explicitly redirect `2>&1`; libsu returns
 * stdout and stderr as separate lists and the daemon only ever writes its JSON contract to
 * stdout, so the `ctl` path intentionally does not mix them (merging would corrupt JSON
 * parsing with unrelated warnings).
 */
data class ExecResult(
    val code: Int,
    val stdout: String,
    val stderr: String,
) {
    val ok: Boolean get() = code == 0

    val combined: String get() = buildString {
        append(stdout)
        if (stderr.isNotBlank()) {
            if (isNotEmpty()) append('\n')
            append(stderr)
        }
    }
}

/** Base class for every shell/daemon failure surfaced to the UI. */
open class ShellException(message: String) : Exception(message)

/** The device has no working root shell. */
class NoRootException(message: String = "未获取 root 权限") : ShellException(message)

/** Root works, but the DufsBox KernelSU module is not installed / its binary is absent. */
class ModuleMissingException(
    // Qualified because a default argument cannot see another declaration's scope.
    // RootShell.MOD_PATH is a compile-time constant, so this is inlined safely.
    message: String = "未检测到 DufsBox 模块（${RootShell.MOD_PATH}）",
) : ShellException(message)

/** The daemon answered, but with `{"ok":false,...}`, or its stdout was not valid JSON. */
class CtlException(message: String) : ShellException(message)

/**
 * One persistent root shell for the whole process.
 *
 * `Shell.getShell()` is intentionally called off the main thread; libsu blocks while the
 * `su` request is being resolved and would trip `NetworkOnMainThread`-style strict-mode
 * violations (and an ANR) if it ran on the UI thread.
 */
object RootShell {

    const val MOD_DIR = "/data/adb/modules/dufsbox"
    const val MOD_PATH = "$MOD_DIR/bin/arm64/dufsboxd"

    @Volatile
    private var cached: Shell? = null

    @Volatile
    private var cachedError: String? = null

    /** True once a shell has been obtained and verified to be root. */
    val isRoot: Boolean get() = cached?.isRoot == true

    /** Non-null when the last [requireShell] attempt failed. */
    val lastError: String? get() = cachedError

    val modPath: String get() = MOD_PATH

    /**
     * Returns the persistent root shell, or throws [NoRootException].
     *
     * Must be called from a background dispatcher.
     */
    fun requireShell(): Shell {
        cached?.let { if (it.isRoot) return it }

        val existing = cached
        if (existing != null && !existing.isRoot) {
            cachedError = "未获取 root 权限"
            throw NoRootException()
        }

        return try {
            val shell = Shell.getShell()
            if (!shell.isRoot) {
                cached = shell
                cachedError = "未获取 root 权限"
                throw NoRootException()
            }
            cached = shell
            cachedError = null
            shell
        } catch (e: NoRootException) {
            throw e
        } catch (e: Exception) {
            cachedError = e.message ?: "无法获取 root shell"
            throw NoRootException("无法获取 root shell：${e.message ?: e.javaClass.simpleName}")
        }
    }

    /** Drops the cached shell so the next call re-requests `su` (used after a "no root" state). */
    fun invalidate() {
        cached = null
    }

    /**
     * Runs one command line in the persistent root shell.
     *
     * Uses `Shell.cmd(...)`, which is documented as equivalent to
     * `getShell().newJob().add(...).to(new ArrayList<>())` — i.e. it *does* collect STDOUT and
     * STDERR. `newJob()` alone collects nothing unless `.to(...)` is set, so it must not be
     * used here.
     *
     * @param cmd a complete shell command. Use [quote] for every argument that may contain
     *   spaces, quotes or `$`.
     */
    suspend fun exec(cmd: String): ExecResult = withContext(Dispatchers.IO) {
        requireShell()
        try {
            val result = Shell.cmd(cmd).exec()
            ExecResult(
                code = result.code,
                stdout = result.out.orEmpty().joinToString("\n").trim(),
                stderr = result.err.orEmpty().joinToString("\n").trim(),
            )
        } catch (e: ShellException) {
            throw e
        } catch (e: Exception) {
            throw ShellException(e.message ?: "root shell 执行失败")
        }
    }

    /** True when [path] exists (does not require the module). */
    suspend fun exists(path: String): Boolean = try {
        exec("test -e ${quote(path)} && echo yes || echo no").stdout.trim() == "yes"
    } catch (e: Exception) {
        false
    }

    /**
     * POSIX single-quote escaping: `it's` becomes `'it'\''s'`. Safe for spaces, quotes,
     * `$`, backticks and newlines inside single quotes.
     */
    fun quote(value: String): String = "'" + value.replace("'", "'\\''") + "'"
}
