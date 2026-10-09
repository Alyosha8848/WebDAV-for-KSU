package main

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"
)

// Service states surfaced to the App.
const (
	StateRunning  = "running"
	StateStopped  = "stopped"
	StateStarting = "starting"
	StateError    = "error"
)

// ---------------------------------------------------------------------------
// Status model (see docs/API.md section 4.1)
// ---------------------------------------------------------------------------

// ServiceInfo describes the daemon itself.
type ServiceInfo struct {
	State     string `json:"state"`
	UptimeSec int64  `json:"uptime_sec"`
	PID       int    `json:"pid"`
	Version   string `json:"version"`
}

// DufsInfo describes the upstream file server child process.
type DufsInfo struct {
	Running bool   `json:"running"`
	PID     int    `json:"pid"`
	Version string `json:"version"`
	Bind    string `json:"bind"`
}

// Addresses lists the URLs the share is reachable on.
type Addresses struct {
	LAN      []string `json:"lan"`
	Tailnet  []string `json:"tailnet"`
	Loopback []string `json:"loopback"`
}

// ProtocolInfo tells the client what it is actually talking to.
type ProtocolInfo struct {
	HTTP   string `json:"http"`
	WebDAV string `json:"webdav"`
	Dufs   string `json:"dufs"`
}

// HealthInfo is the pre-flight check shown on the status tab.
type HealthInfo struct {
	SharePathExists   bool `json:"share_path_exists"`
	SharePathWritable bool `json:"share_path_writable"`
	PortBindable      bool `json:"port_bindable"`
}

// Status is the full status reply.
type Status struct {
	Service   ServiceInfo   `json:"service"`
	Dufs      DufsInfo      `json:"dufs"`
	Tailscale TailscaleInfo `json:"tailscale"`
	Config    ConfigView    `json:"config"`
	Addresses Addresses     `json:"addresses"`
	Protocol  ProtocolInfo  `json:"protocol"`
	Stats     Totals        `json:"stats"`
	Health    HealthInfo    `json:"health"`
	LastError string        `json:"last_error"`
}

// ---------------------------------------------------------------------------
// child process wrapper
// ---------------------------------------------------------------------------

// child tracks one supervised process. Liveness is derived from Wait()
// returning, which works identically on Android and on a development host
// (signal-0 probing does not exist on Windows).
type child struct {
	name      string
	cmd       *exec.Cmd
	pid       int
	startedAt time.Time

	mu     sync.Mutex
	exited bool
	err    error
}

// Alive reports whether the process is still running.
func (c *child) Alive() bool {
	if c == nil {
		return false
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	return !c.exited
}

// ExitErr returns the error from Wait, if the process has exited.
func (c *child) ExitErr() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.err
}

// Stop asks the process to terminate, then kills it after grace elapses.
func (c *child) Stop(grace time.Duration) {
	if c == nil || c.cmd == nil || c.cmd.Process == nil {
		return
	}
	_ = c.cmd.Process.Signal(syscall.SIGTERM) // unsupported on Windows: falls through to Kill
	deadline := time.Now().Add(grace)
	for time.Now().Before(deadline) {
		if !c.Alive() {
			return
		}
		time.Sleep(100 * time.Millisecond)
	}
	_ = c.cmd.Process.Kill()
	for i := 0; i < 20 && c.Alive(); i++ {
		time.Sleep(50 * time.Millisecond)
	}
}

// lineWriter splits a child's output into lines and feeds them to the logger.
type lineWriter struct {
	log  *Logger
	src  string
	also func(string)

	mu  sync.Mutex
	buf []byte
}

func (w *lineWriter) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.buf = append(w.buf, p...)
	for {
		i := bytes.IndexByte(w.buf, '\n')
		if i < 0 {
			break
		}
		line := strings.TrimRight(string(w.buf[:i]), "\r")
		w.buf = w.buf[i+1:]
		w.emit(line)
	}
	// Guard against a binary/never-terminated stream growing without bound.
	if len(w.buf) > 8192 {
		w.emit(string(w.buf))
		w.buf = w.buf[:0]
	}
	return len(p), nil
}

func (w *lineWriter) emit(line string) {
	if strings.TrimSpace(line) == "" {
		return
	}
	if w.also != nil {
		w.also(line)
	}
	w.log.log(classifyExternalLevel(line), w.src, line)
}

// ---------------------------------------------------------------------------
// Supervisor
// ---------------------------------------------------------------------------

// Supervisor owns every child process and the public listener, and reconciles
// the running state with the configuration.
type Supervisor struct {
	paths Paths
	cfg   *ConfigStore
	acl   *ACL
	dev   *Tracker
	log   *Logger
	probe healthProbe

	mu                sync.Mutex
	startedAt         time.Time
	enabled           bool
	listener          *Listener
	dufsChild         *child
	dufsBind          string
	dufsLastAttempt   time.Time
	dufsAttempts      int
	tsChild           *child
	tsLoginChild      *child
	tsLastLoginAt     time.Time
	tsServeConfigured bool
	tsState           string
	tsIP              string
	tsDNS             string
	tsAuthURL         string
	tsVersion         string
	lastError         string
	dufsVersionCache  string
	stopCh            chan struct{}
}

// NewSupervisor builds a supervisor.
func NewSupervisor(paths Paths, cfg *ConfigStore, acl *ACL, dev *Tracker, log *Logger) *Supervisor {
	s := &Supervisor{
		paths:     paths,
		cfg:       cfg,
		acl:       acl,
		dev:       dev,
		log:       log,
		startedAt: time.Now(),
		enabled:   true,
		tsState:   TSDisabled,
		stopCh:    make(chan struct{}),
	}
	s.enabled = s.loadEnabled()
	return s
}

// loadEnabled reads the persisted desired state; the default is enabled.
func (s *Supervisor) loadEnabled() bool {
	raw, err := os.ReadFile(s.paths.EnabledFile())
	if err != nil {
		return true
	}
	return strings.TrimSpace(string(raw)) != "0"
}

// Enabled reports the desired service state.
func (s *Supervisor) Enabled() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.enabled
}

// SetEnabled persists and applies a desired service state.
func (s *Supervisor) SetEnabled(on bool) error {
	s.mu.Lock()
	s.enabled = on
	s.mu.Unlock()
	val := "0"
	if on {
		val = "1"
	}
	if err := os.WriteFile(s.paths.EnabledFile(), []byte(val+"\n"), 0o600); err != nil {
		return err
	}
	s.Reconcile()
	return nil
}

// Run starts the reconciliation loop and blocks until Close.
func (s *Supervisor) Run() {
	s.Reconcile()
	// Reconcile often enough that a crash is repaired quickly, but not so often
	// that tailscaled is polled needlessly.
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-s.stopCh:
			return
		case <-ticker.C:
			s.Reconcile()
		}
	}
}

// Close stops the loop and every child process.
func (s *Supervisor) Close() {
	select {
	case <-s.stopCh:
	default:
		close(s.stopCh)
	}
	s.Teardown()
}

// Start turns the service on.
func (s *Supervisor) Start() error { return s.SetEnabled(true) }

// Stop turns the service off (the daemon itself keeps running so the App can
// still read status and logs).
func (s *Supervisor) Stop() error { return s.SetEnabled(false) }

// Restart recreates dufs and the listener so that a changed share path, port,
// credential or global-read-only flag takes effect immediately.
//
// tailscaled is deliberately left alone: dropping the tunnel would make a
// cosmetic change take a minute instead of a second.
func (s *Supervisor) Restart() error {
	s.log.Infof("supervisor", "按当前配置重启 dufs 与监听")
	s.stopListener()
	s.stopDufs()
	s.mu.Lock()
	s.dufsVersionCache = ""
	// An explicit restart is not a crash loop: clear the backoff so the new
	// configuration is applied now rather than on some later health tick.
	s.dufsAttempts = 0
	s.dufsLastAttempt = time.Time{}
	s.mu.Unlock()
	return s.Reconcile()
}

// Reconcile makes the running processes match the configuration. It is
// idempotent and runs on every tick; it returns the first real failure so that
// an explicit restart can report it to the caller.
func (s *Supervisor) Reconcile() error {
	if !s.Enabled() {
		s.mu.Lock()
		running := s.dufsChild != nil || s.listener != nil || s.tsChild != nil
		s.mu.Unlock()
		if running {
			s.log.Infof("supervisor", "服务已按要求停止")
			s.Teardown()
		}
		s.mu.Lock()
		if s.tsState != TSDisabled {
			s.tsState = TSStopped
		}
		s.mu.Unlock()
		return nil
	}

	cfg := s.cfg.Get()
	var firstErr error

	if _, err := os.Stat(cfg.SharePath); err != nil {
		// Nothing to serve yet; the health loop will retry once the storage is
		// mounted. Reported as an error so the UI can explain the wait.
		msg := fmt.Sprintf("共享路径不可用: %s", cfg.SharePath)
		s.setError(msg)
		firstErr = errors.New(msg)
	} else if s.dufsChild == nil || !s.dufsChild.Alive() {
		if s.shouldAttemptDufsStart() {
			s.mu.Lock()
			s.dufsLastAttempt = time.Now()
			s.mu.Unlock()
			if err := s.startDufs(cfg); err != nil {
				s.mu.Lock()
				s.dufsAttempts++
				s.mu.Unlock()
				s.setError(err.Error())
				firstErr = err
			} else {
				s.mu.Lock()
				s.dufsAttempts = 0
				s.mu.Unlock()
			}
		}
	}

	if s.listener == nil {
		if err := s.startListener(cfg); err != nil {
			s.setError(err.Error())
			if firstErr == nil {
				firstErr = err
			}
		}
	}

	if cfg.TailscaleEnabled() {
		s.mu.Lock()
		alive := s.tsChild != nil && s.tsChild.Alive()
		s.mu.Unlock()
		if !alive {
			if err := s.startTailscaled(cfg); err != nil {
				s.setError(err.Error())
				if firstErr == nil {
					firstErr = err
				}
			}
		} else {
			s.ensureTailscaleLogin(cfg)
			s.ensureTailscaleServe(cfg)
			s.applyTailscaleStatus()
		}
	} else {
		s.mu.Lock()
		hasTS := s.tsChild != nil
		s.mu.Unlock()
		if hasTS {
			s.stopTailscaled()
		}
		s.mu.Lock()
		s.tsState = TSDisabled
		s.tsIP = ""
		s.tsDNS = ""
		s.tsAuthURL = ""
		s.mu.Unlock()
	}
	return firstErr
}

// shouldAttemptDufsStart rate-limits only *repeated failures*.
//
// The original implementation throttled every start attempt by a fixed window,
// which also delayed the restart that an explicit configuration change requires:
// after changing the share path, dufs stayed down until the next window elapsed.
// Backing off only after a failure keeps crash loops in check without making a
// deliberate restart wait.
func (s *Supervisor) shouldAttemptDufsStart() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.dufsAttempts == 0 {
		return true
	}
	return time.Since(s.dufsLastAttempt) >= 10*time.Second
}

// Teardown stops the listener, dufs and tailscaled.
func (s *Supervisor) Teardown() {
	s.stopListener()
	s.stopDufs()
	s.stopTailscaled()
}

// setError records and logs the current failure. Repeated identical messages are
// only stored once so the log does not fill up on every 5-second tick.
func (s *Supervisor) setError(msg string) {
	s.mu.Lock()
	if s.lastError != msg {
		s.lastError = msg
	}
	s.mu.Unlock()
	s.log.Errorf("supervisor", "%s", msg)
}

func (s *Supervisor) clearError() {
	s.mu.Lock()
	s.lastError = ""
	s.mu.Unlock()
}

// ---------------------------------------------------------------------------
// dufs
// ---------------------------------------------------------------------------

// spawn starts a supervised child with line-split logging.
func (s *Supervisor) spawn(name, src, binary string, args []string, env []string, also func(string)) (*child, error) {
	cmd := exec.Command(binary, args...)
	cmd.Env = env
	cmd.Stdout = &lineWriter{log: s.log, src: src, also: also}
	cmd.Stderr = &lineWriter{log: s.log, src: src, also: also}
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	c := &child{name: name, cmd: cmd, pid: cmd.Process.Pid, startedAt: time.Now()}
	go func() {
		err := cmd.Wait()
		c.mu.Lock()
		c.exited = true
		c.err = err
		c.mu.Unlock()
		s.log.Warnf(src, "%s (pid %d) 已退出: %v", name, c.pid, err)
	}()
	return c, nil
}

func (s *Supervisor) startDufs(cfg Config) error {
	if _, err := os.Stat(s.paths.DufsBinary); err != nil {
		return fmt.Errorf("缺少 dufs 二进制: %s", s.paths.DufsBinary)
	}
	// A malformed credential makes dufs exit immediately, so fail loudly here
	// rather than letting the share go dark with an obscure process error.
	if err := cfg.AuthRuleError(); err != nil {
		return err
	}
	_ = os.Chmod(s.paths.DufsBinary, 0o755)
	args := cfg.DufsArgs()
	env := append([]string{}, os.Environ()...)
	env = append(env, "TMPDIR="+s.paths.TmpDir())

	c, err := s.spawn("dufs", "dufs", s.paths.DufsBinary, args, env, nil)
	if err != nil {
		return fmt.Errorf("启动 dufs 失败: %w", err)
	}
	internal := fmt.Sprintf("127.0.0.1:%d", cfg.InternalPort())
	s.mu.Lock()
	s.dufsChild = c
	s.dufsBind = internal
	s.dufsVersionCache = ""
	s.mu.Unlock()

	if !waitForPort(internal, 6*time.Second) {
		s.log.Warnf("supervisor", "dufs 未在 6 秒内监听 %s（pid=%d 仍在运行=%v）", internal, c.pid, c.Alive())
		return fmt.Errorf("dufs 未能在 %s 上就绪", internal)
	}
	s.clearError()
	s.log.Infof("supervisor", "dufs 已启动 pid=%d 监听=%s 共享=%s", c.pid, internal, cfg.SharePath)
	return nil
}

func (s *Supervisor) stopDufs() {
	s.mu.Lock()
	c := s.dufsChild
	s.dufsChild = nil
	s.mu.Unlock()
	if c != nil {
		c.Stop(3 * time.Second)
		s.log.Infof("supervisor", "dufs 已停止")
	}
}

// ---------------------------------------------------------------------------
// Listener / proxy
// ---------------------------------------------------------------------------

func (s *Supervisor) startListener(cfg Config) error {
	proxy, err := NewProxy(s.cfg, s.acl, s.dev, s.log, cfg.InternalPort())
	if err != nil {
		return err
	}
	addr := cfg.PublicBindAddress()
	ln, err := StartListener(addr, proxy, s.dev, s.log)
	if err != nil {
		return fmt.Errorf("无法监听 %s: %w", addr, err)
	}
	s.mu.Lock()
	s.listener = ln
	s.mu.Unlock()
	s.log.Infof("supervisor", "对外监听已就绪 %s (模式 %s)", ln.Addr(), cfg.Mode)
	return nil
}

func (s *Supervisor) stopListener() {
	s.mu.Lock()
	ln := s.listener
	s.listener = nil
	s.mu.Unlock()
	if ln != nil {
		_ = ln.Close()
		s.log.Infof("supervisor", "对外监听已关闭")
	}
}

// ---------------------------------------------------------------------------
// tailscaled / tailscale
// ---------------------------------------------------------------------------

func (s *Supervisor) startTailscaled(cfg Config) error {
	if _, err := os.Stat(s.paths.TailscaledBinary); err != nil {
		return fmt.Errorf("缺少 tailscaled 二进制: %s", s.paths.TailscaledBinary)
	}
	if _, err := os.Stat(s.paths.TailscaleBinary); err != nil {
		return fmt.Errorf("缺少 tailscale 二进制: %s", s.paths.TailscaleBinary)
	}
	_ = os.Chmod(s.paths.TailscaledBinary, 0o755)
	_ = os.Chmod(s.paths.TailscaleBinary, 0o755)

	// A stale control socket makes the CLI hang, so remove it before starting.
	_ = os.Remove(s.paths.TailscaleSocket())

	args := []string{
		"--tun=userspace-networking",
		"--state=" + s.paths.TailscaleStateFile(),
		"--statedir=" + s.paths.StateDir,
		"--socket=" + s.paths.TailscaleSocket(),
	}
	c, err := s.spawn("tailscaled", "tailscale", s.paths.TailscaledBinary, args, tailscaleEnv(s.paths), nil)
	if err != nil {
		return fmt.Errorf("启动 tailscaled 失败: %w", err)
	}
	s.mu.Lock()
	s.tsChild = c
	s.tsState = TSOffline
	s.mu.Unlock()
	s.log.Infof("supervisor", "tailscaled 已启动 pid=%d (userspace-networking)", c.pid)
	return nil
}

func (s *Supervisor) stopTailscaled() {
	s.mu.Lock()
	login := s.tsLoginChild
	c := s.tsChild
	s.tsLoginChild = nil
	s.tsChild = nil
	s.tsIP = ""
	s.tsDNS = ""
	s.tsState = TSStopped
	s.tsServeConfigured = false
	s.mu.Unlock()
	if login != nil {
		login.Stop(2 * time.Second)
	}
	if c != nil {
		c.Stop(3 * time.Second)
		_ = os.Remove(s.paths.TailscaleSocket())
		s.log.Infof("supervisor", "tailscaled 已停止")
	}
}

// ensureTailscaleLogin keeps a `tailscale up` running until the node is
// authorised. It is retried at most every 30 seconds so a failing login does not
// spin.
func (s *Supervisor) ensureTailscaleLogin(cfg Config) {
	s.mu.Lock()
	running := s.tsLoginChild != nil && s.tsLoginChild.Alive()
	online := s.tsState == TSOnline
	since := time.Since(s.tsLastLoginAt)
	s.mu.Unlock()
	if running || online || since < 30*time.Second {
		return
	}

	args := []string{
		"--socket=" + s.paths.TailscaleSocket(),
		"up",
		"--hostname=" + cfg.Tailscale.Hostname,
		"--accept-dns=" + strconv.FormatBool(cfg.Tailscale.AcceptDNS),
	}
	if cfg.Tailscale.AuthKey != "" {
		args = append(args, "--auth-key="+cfg.Tailscale.AuthKey)
	}
	if cfg.Tailscale.LoginServer != "" {
		args = append(args, "--login-server="+cfg.Tailscale.LoginServer)
	}
	c, err := s.spawn("tailscale up", "tailscale", s.paths.TailscaleBinary, args, tailscaleEnv(s.paths), s.recordAuthURL)
	s.mu.Lock()
	s.tsLastLoginAt = time.Now()
	if err == nil {
		s.tsLoginChild = c
	}
	s.mu.Unlock()
	if err != nil {
		s.log.Warnf("tailscale", "tailscale up 启动失败: %v", err)
		return
	}
	s.log.Infof("tailscale", "已请求 Tailscale 登录 (hostname=%s)", cfg.Tailscale.Hostname)
}

// ensureTailscaleServe publishes the share over tailnet HTTPS when enabled.
//
// HTTPS Serve gives a stable MagicDNS name (https://<host>.<tailnet>.ts.net/)
// and, because it is an HTTP proxy inside tailscaled, it also propagates the real
// tailnet client address in X-Forwarded-For. Direct 100.x.y.z access keeps
// working regardless.
func (s *Supervisor) ensureTailscaleServe(cfg Config) {
	s.mu.Lock()
	online := s.tsState == TSOnline
	configured := s.tsServeConfigured
	s.mu.Unlock()

	if !cfg.Tailscale.HTTPSServe {
		if configured {
			if out, err := s.ts(25*time.Second, "serve", "reset"); err != nil {
				s.log.Warnf("tailscale", "关闭 HTTPS Serve 失败: %v %s", err, strings.TrimSpace(out))
			} else {
				s.log.Infof("tailscale", "HTTPS Serve 已关闭")
			}
			s.mu.Lock()
			s.tsServeConfigured = false
			s.mu.Unlock()
		}
		return
	}
	if !online || configured {
		return
	}
	target := fmt.Sprintf("http://127.0.0.1:%d", cfg.Port)
	out, err := s.ts(30*time.Second, "serve", "--bg", "--https=443", "--yes", target)
	if err != nil {
		s.log.Warnf("tailscale", "配置 HTTPS Serve 失败: %v %s", err, strings.TrimSpace(out))
		s.recordAuthURL(out)
		s.mu.Lock()
		s.tsLastLoginAt = time.Time{} // allow an immediate retry on the next tick
		s.mu.Unlock()
		return
	}
	s.mu.Lock()
	s.tsServeConfigured = true
	s.mu.Unlock()
	if dns := s.tailscaleDNS(); dns != "" {
		s.log.Infof("tailscale", "HTTPS Serve 已启用: https://%s/", dns)
	} else {
		s.log.Infof("tailscale", "HTTPS Serve 已启用")
	}
}

func (s *Supervisor) tailscaleDNS() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.tsDNS
}

// TailscaleAction implements the ctl tailscale.set verb.
func (s *Supervisor) TailscaleAction(action string) (map[string]any, error) {
	switch action {
	case "up":
		s.mu.Lock()
		s.tsLastLoginAt = time.Time{}
		s.mu.Unlock()
		s.Reconcile()
		return map[string]any{"state": s.tailscaleSnapshot().State}, nil
	case "down":
		out, err := s.ts(20*time.Second, "down")
		if err != nil {
			return nil, fmt.Errorf("tailscale down 失败: %v %s", err, strings.TrimSpace(out))
		}
		s.applyTailscaleStatus()
		return map[string]any{"state": s.tailscaleSnapshot().State}, nil
	case "logout":
		out, err := s.ts(20*time.Second, "logout")
		if err != nil {
			return nil, fmt.Errorf("tailscale logout 失败: %v %s", err, strings.TrimSpace(out))
		}
		s.mu.Lock()
		s.tsAuthURL = ""
		s.tsIP = ""
		s.tsDNS = ""
		s.tsServeConfigured = false
		s.mu.Unlock()
		s.stopTailscaled()
		return map[string]any{"state": TSStopped}, nil
	case "serve_on", "serve_off":
		on := action == "serve_on"
		if _, err := s.cfg.Update(func(c *Config) { c.Tailscale.HTTPSServe = on }); err != nil {
			return nil, err
		}
		s.Reconcile()
		return map[string]any{"https_serve": on}, nil
	}
	return nil, fmt.Errorf("未知的 Tailscale 动作: %s", action)
}

// ---------------------------------------------------------------------------
// status
// ---------------------------------------------------------------------------

// tailscaleSnapshot returns the cached tailnet facts for the API.
func (s *Supervisor) tailscaleSnapshot() TailscaleInfo {
	cfg := s.cfg.Get()
	s.mu.Lock()
	defer s.mu.Unlock()
	state := s.tsState
	if !cfg.TailscaleEnabled() {
		state = TSDisabled
	}
	ver := s.tsVersion
	if ver == "" {
		ver = PinnedTailscaleVersion
	}
	return TailscaleInfo{
		Enabled:    cfg.TailscaleEnabled(),
		State:      state,
		IP:         s.tsIP,
		DNS:        s.tsDNS,
		AuthURL:    s.tsAuthURL,
		Version:    ver,
		HTTPSServe: cfg.Tailscale.HTTPSServe && s.tsServeConfigured,
	}
}

// dufsVersion probes `dufs --version` once and caches the result.
func (s *Supervisor) dufsVersion() string {
	s.mu.Lock()
	if s.dufsVersionCache != "" {
		v := s.dufsVersionCache
		s.mu.Unlock()
		return v
	}
	s.mu.Unlock()

	out, err := exec.Command(s.paths.DufsBinary, "--version").CombinedOutput()
	v := PinnedDufsVersion
	if err == nil {
		if m := versionRe.FindString(string(out)); m != "" {
			v = m
		}
	}
	s.mu.Lock()
	s.dufsVersionCache = v
	s.mu.Unlock()
	return v
}

var versionRe = regexp.MustCompile(`\d+\.\d+\.\d+`)

// Status assembles the full status reply.
func (s *Supervisor) Status() Status {
	cfg := s.cfg.Get()
	exists, writable := s.probe.Probe(cfg.SharePath)

	s.mu.Lock()
	serviceState := StateRunning
	if !s.enabled {
		serviceState = StateStopped
	}
	if s.lastError != "" && (s.dufsChild == nil || !s.dufsChild.Alive()) {
		serviceState = StateError
	}
	dufsRunning := s.dufsChild != nil && s.dufsChild.Alive()
	dufsPID := 0
	if dufsRunning {
		dufsPID = s.dufsChild.pid
	}
	dufsBind := s.dufsBind
	listenerUp := s.listener != nil
	lastErr := s.lastError
	ts := TailscaleInfo{
		Enabled:    cfg.TailscaleEnabled(),
		State:      s.tsState,
		IP:         s.tsIP,
		DNS:        s.tsDNS,
		AuthURL:    s.tsAuthURL,
		Version:    s.tsVersion,
		HTTPSServe: cfg.Tailscale.HTTPSServe && s.tsServeConfigured,
	}
	if !cfg.TailscaleEnabled() {
		ts.State = TSDisabled
	}
	s.mu.Unlock()

	if ts.Version == "" {
		ts.Version = PinnedTailscaleVersion
	}

	var lan []string
	for _, ip := range LocalIPv4s() {
		lan = append(lan, fmt.Sprintf("http://%s:%d/", ip, cfg.Port))
	}
	var tailnet []string
	if ts.IP != "" {
		tailnet = append(tailnet, fmt.Sprintf("http://%s:%d/", ts.IP, cfg.Port))
		if ts.DNS != "" {
			tailnet = append(tailnet, fmt.Sprintf("http://%s/", ts.DNS))
		}
	}
	if cfg.Tailscale.HTTPSServe && ts.DNS != "" && s.isServeConfigured() {
		tailnet = append([]string{fmt.Sprintf("https://%s/", ts.DNS)}, tailnet...)
	}

	bindable := listenerUp
	if !listenerUp {
		bindable = bindablePort(cfg.PublicBindAddress())
	}

	return Status{
		Service: ServiceInfo{
			State:     serviceState,
			UptimeSec: int64(time.Since(s.startedAt).Seconds()),
			PID:       os.Getpid(),
			Version:   DaemonVersion,
		},
		Dufs: DufsInfo{
			Running: dufsRunning,
			PID:     dufsPID,
			Version: s.dufsVersion(),
			Bind:    dufsBind,
		},
		Tailscale: ts,
		Config:    cfg.View(),
		Addresses: Addresses{LAN: lan, Tailnet: tailnet, Loopback: []string{fmt.Sprintf("http://127.0.0.1:%d/", cfg.Port)}},
		Protocol: ProtocolInfo{
			HTTP:   "1.1",
			WebDAV: "RFC 4918 (class 1)",
			Dufs:   s.dufsVersion(),
		},
		Stats:     s.dev.Totals(),
		Health:    HealthInfo{SharePathExists: exists, SharePathWritable: writable, PortBindable: bindable},
		LastError: lastErr,
	}
}

func (s *Supervisor) isServeConfigured() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.tsServeConfigured
}
