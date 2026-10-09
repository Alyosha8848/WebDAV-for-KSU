// Command dufsboxd is the DufsBox control daemon: it supervises the upstream
// dufs file server, enforces per-device access policy in front of it, and serves
// a small JSON control API that the Android app drives over a root shell.
package main

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"runtime"
	"strconv"
	"syscall"
	"time"
)

func main() {
	os.Exit(run(os.Args[1:]))
}

func run(argv []string) int {
	if len(argv) == 0 {
		usage()
		return 2
	}
	switch argv[0] {
	case "serve":
		return runServe()
	case "launch":
		return runLaunch()
	case "ctl":
		return runCtl(argv[1:])
	case "autostart-check":
		return runAutostartCheck()
	case "version", "--version", "-V":
		printJSON(map[string]any{
			"ok": true,
			"data": map[string]any{
				"version":  DaemonVersion,
				"protocol": ProtocolVersion,
				"go":       runtime.Version(),
				"dufs":     PinnedDufsVersion,
			},
		})
		return 0
	case "help", "--help", "-h":
		usage()
		return 0
	}
	fmt.Fprintf(os.Stderr, "未知命令: %s\n", argv[0])
	usage()
	return 2
}

func usage() {
	fmt.Fprint(os.Stderr, `DufsBox 控制守护进程

用法:
  dufsboxd serve              以守护进程方式运行（由模块 service.sh 调用）
  dufsboxd launch             后台拉起守护进程（App 中“启动”使用）
  dufsboxd autostart-check    读取 config.json，开机自启开启时返回 0
  dufsboxd ctl <verb> [--json <payload>]
  dufsboxd version

常用 ctl 命令:
  status | devices | config.get | logs | browse | version
  service.set   --json '{"action":"start|stop|restart"}'
  acl.set       --json '{"id":"<mac 或 ip>","policy":"rw|ro|deny|default"}'
  acl.remove    --json '{"id":"<mac 或 ip>"}'
  device.forget --json '{"id":"<mac 或 ip>"}'
  config.set    --json '{"share_path":"/sdcard/Download"}'
  tailscale.set --json '{"action":"up|down|logout|serve_on|serve_off"}'
  subscribe     （NDJSON 事件流，供 CLI / KernelSU WebUI 使用）
`)
}

// openLogger creates the daemon logger and mirrors it into the module log file.
func openLogger(paths Paths) *Logger {
	log := NewLogger(2000)
	if err := paths.EnsureDirs(); err != nil {
		fmt.Fprintf(os.Stderr, "无法创建状态目录: %v\n", err)
	}
	if err := log.OpenFile(paths.LogFile()); err != nil {
		fmt.Fprintf(os.Stderr, "无法写入日志文件: %v\n", err)
	}
	return log
}

// loadConfigStore loads config.json, recovering from a corrupt file instead of
// refusing to boot (a broken config must never leave the share dead).
func loadConfigStore(paths Paths, log *Logger) *ConfigStore {
	store, err := NewConfigStore(paths)
	if err != nil {
		log.Errorf("config", "%v", err)
		_ = os.Remove(paths.ConfigFile)
		store, err = NewConfigStore(paths)
		if err != nil {
			log.Errorf("config", "使用默认配置: %v", err)
			fallback := DefaultConfig()
			fallback.Normalize()
			// Last resort: keep running in memory only.
			if store == nil {
				store = &ConfigStore{cfg: fallback, paths: paths}
			}
		}
	}
	return store
}

func runServe() int {
	paths := DefaultPaths()
	if err := paths.EnsureDirs(); err != nil {
		fmt.Fprintf(os.Stderr, "无法创建状态目录: %v\n", err)
		return 1
	}

	// Only one daemon may own the control socket.
	if daemonReachable() {
		fmt.Fprintln(os.Stderr, "dufsboxd 已在运行")
		return 0
	}

	log := openLogger(paths)
	log.Infof("daemon", "dufsboxd %s 启动 (go %s, protocol %d)", DaemonVersion, runtime.Version(), ProtocolVersion)

	if err := os.WriteFile(paths.PidFile, []byte(strconv.Itoa(os.Getpid())+"\n"), 0o600); err != nil {
		log.Warnf("daemon", "写入 pid 文件失败: %v", err)
	}
	defer func() {
		_ = os.Remove(paths.PidFile)
		log.Infof("daemon", "dufsboxd 退出")
	}()

	cfg := loadConfigStore(paths, log)
	log.SetLevel(ParseLevel(cfg.Get().LogLevel))

	acl, err := LoadACL(paths.ACLFile)
	if err != nil {
		log.Errorf("acl", "读取 acl.json 失败，已忽略: %v", err)
		acl = NewEphemeralACL()
	}
	log.Infof("acl", "已加载 %d 条设备规则", len(acl.All()))

	dev := NewTracker(acl, cfg, log)
	sup := NewSupervisor(paths, cfg, acl, dev, log)

	control, err := StartControl(paths, cfg, acl, dev, sup, log)
	if err != nil {
		log.Errorf("control", "%v", err)
		return 1
	}
	defer control.Close()

	// A live config change is applied through the supervisor directly (see the
	// config.set verb); the store callback only keeps the log level in sync.
	cfg.SetOnChange(func(old, next Config) {
		if old.LogLevel != next.LogLevel {
			log.SetLevel(ParseLevel(next.LogLevel))
		}
	})

	done := make(chan struct{})
	go func() {
		sup.Run()
		close(done)
	}()

	sig := make(chan os.Signal, 1)
	signal.Notify(sig, os.Interrupt, syscall.SIGTERM)

	select {
	case <-sig:
		log.Infof("daemon", "收到退出信号，正在停止子进程")
	case <-done:
	}

	sup.Close()
	// Give the flush-on-exit log line a moment to land in the file.
	time.Sleep(100 * time.Millisecond)
	return 0
}

// runLaunch starts the daemon detached and waits for it to answer.
//
// The App calls this for "启动" so that pressing start works even when the module
// did not auto-start at boot. The child is orphaned on purpose: it must outlive
// the root shell that libsu opened.
func runLaunch() int {
	paths := DefaultPaths()
	if daemonReachable() {
		printJSON(map[string]any{"ok": true, "data": map[string]any{"already_running": true}})
		return 0
	}
	if err := paths.EnsureDirs(); err != nil {
		printJSON(map[string]any{"ok": false, "error": "无法创建状态目录: " + err.Error()})
		return 1
	}

	exe, err := os.Executable()
	if err != nil {
		printJSON(map[string]any{"ok": false, "error": "无法定位自身可执行文件: " + err.Error()})
		return 1
	}
	out, err := os.OpenFile(paths.StdoutFile(), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		printJSON(map[string]any{"ok": false, "error": "无法写入启动日志: " + err.Error()})
		return 1
	}
	defer out.Close()

	cmd := exec.Command(exe, "serve")
	cmd.Env = os.Environ()
	cmd.Stdin = nil
	cmd.Stdout = out
	cmd.Stderr = out
	cmd.SysProcAttr = detachSysProcAttr()
	if err := cmd.Start(); err != nil {
		printJSON(map[string]any{"ok": false, "error": "启动 dufsboxd 失败: " + err.Error()})
		return 1
	}
	pid := cmd.Process.Pid
	_ = cmd.Process.Release()

	deadline := time.Now().Add(12 * time.Second)
	for time.Now().Before(deadline) {
		if daemonReachable() {
			printJSON(map[string]any{"ok": true, "data": map[string]any{"pid": pid, "already_running": false}})
			return 0
		}
		time.Sleep(200 * time.Millisecond)
	}
	printJSON(map[string]any{"ok": false, "error": "dufsboxd 启动后未在 12 秒内就绪，请查看日志"})
	return 1
}

// runAutostartCheck is called by the module's service.sh: it decides whether the
// daemon should be started at boot, without needing the daemon itself running.
func runAutostartCheck() int {
	paths := DefaultPaths()
	store, err := NewConfigStore(paths)
	if err != nil {
		// A corrupt config must not silently disable boot auto-start.
		fmt.Fprintf(os.Stderr, "读取配置失败，按自启处理: %v\n", err)
		return 0
	}
	cfg := store.Get()
	if cfg.Autostart {
		return 0
	}
	stdoutJSON(map[string]any{"autostart": false, "share_path": cfg.SharePath})
	return 1
}

func stdoutJSON(v any) {
	buf, err := json.Marshal(v)
	if err != nil {
		return
	}
	os.Stdout.Write(append(buf, '\n'))
}
