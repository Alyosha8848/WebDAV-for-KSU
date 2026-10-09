package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
)

// ---------------------------------------------------------------------------
// Paths
// ---------------------------------------------------------------------------

// Paths resolves every on-device location the daemon uses.
//
// All of them can be overridden through environment variables so the exact same
// binary can be exercised end-to-end on a development machine (the Windows test
// harness points DUFSBOX_STATE at a temp dir and DUFSBOX_DUFS at a native dufs
// build).
type Paths struct {
	StateDir         string
	ModuleDir        string
	BinDir           string
	ConfigFile       string
	ACLFile          string
	RunDir           string
	LogDir           string
	SockPath         string
	PidFile          string
	ControlCacheFile string
	DufsBinary       string
	TailscaleBinary  string
	TailscaledBinary string
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if strings.TrimSpace(v) != "" {
			return v
		}
	}
	return ""
}

// DefaultPaths builds the path set from the environment, falling back to the
// real on-device KernelSU module layout.
func DefaultPaths() Paths {
	state := firstNonEmpty(os.Getenv("DUFSBOX_STATE"), "/data/adb/dufsbox")
	mod := firstNonEmpty(os.Getenv("DUFSBOX_MODDIR"), "/data/adb/modules/dufsbox")
	bin := firstNonEmpty(os.Getenv("DUFSBOX_BINDIR"), filepath.Join(mod, "bin", "arm64"))
	p := Paths{
		StateDir:         state,
		ModuleDir:        mod,
		BinDir:           bin,
		ConfigFile:       filepath.Join(state, "config.json"),
		ACLFile:          filepath.Join(state, "acl.json"),
		RunDir:           filepath.Join(state, "run"),
		LogDir:           filepath.Join(state, "logs"),
		SockPath:         filepath.Join(state, "run", "dufsboxd.sock"),
		PidFile:          filepath.Join(state, "run", "dufsboxd.pid"),
		ControlCacheFile: filepath.Join(state, "run", "control.addr"),
		DufsBinary:       firstNonEmpty(os.Getenv("DUFSBOX_DUFS"), filepath.Join(bin, "dufs")),
		TailscaleBinary:  firstNonEmpty(os.Getenv("DUFSBOX_TAILSCALE"), filepath.Join(bin, "tailscale")),
		TailscaledBinary: firstNonEmpty(os.Getenv("DUFSBOX_TAILSCALED"), filepath.Join(bin, "tailscaled")),
	}
	return p
}

// EnsureDirs creates the state directories with owner-only permissions.
func (p Paths) EnsureDirs() error {
	for _, d := range []string{p.StateDir, p.RunDir, p.LogDir, p.TmpDir()} {
		if err := os.MkdirAll(d, 0o700); err != nil {
			return err
		}
		_ = os.Chmod(d, 0o700)
	}
	return nil
}

// TmpDir is the private scratch directory handed to dufs/tailscaled as TMPDIR.
// dufs materialises archives here, so it must be on writable storage and must
// not be inside the shared directory.
func (p Paths) TmpDir() string { return filepath.Join(p.StateDir, "tmp") }

// TailscaleStateFile is tailscaled's persisted node state (node key + login).
func (p Paths) TailscaleStateFile() string { return filepath.Join(p.StateDir, "tailscaled.state") }

// TailscaleSocket is the control socket tailscaled exposes (separate from ours).
func (p Paths) TailscaleSocket() string { return filepath.Join(p.RunDir, "tailscaled.sock") }

// EnabledFile persists the operator's desired service state.
func (p Paths) EnabledFile() string { return filepath.Join(p.StateDir, "enabled") }

// LogFile is the daemon's own log.
func (p Paths) LogFile() string { return filepath.Join(p.LogDir, "dufsboxd.log") }

// StdoutFile captures boot-time output (including panics) for the module's diagnostics.
func (p Paths) StdoutFile() string { return filepath.Join(p.LogDir, "dufsboxd.stdout.log") }

// ---------------------------------------------------------------------------
// Config
// ---------------------------------------------------------------------------

// TailscaleConfig holds the optional Tailscale userspace settings.
type TailscaleConfig struct {
	Hostname    string `json:"hostname"`
	HTTPSServe  bool   `json:"https_serve"`
	AcceptDNS   bool   `json:"accept_dns"`
	LoginServer string `json:"login_server"`
	AuthKey     string `json:"auth_key"`
}

// Config is the persisted daemon configuration (config.json).
type Config struct {
	SharePath      string          `json:"share_path"`
	ShareName      string          `json:"share_name"`
	Port           int             `json:"port"`
	Mode           string          `json:"mode"`
	AuthMode       string          `json:"auth_mode"`
	Username       string          `json:"username"`
	Password       string          `json:"password"`
	DefaultPolicy  string          `json:"default_policy"`
	ReadonlyGlobal bool            `json:"readonly_global"`
	Autostart      bool            `json:"autostart"`
	LogLevel       string          `json:"log_level"`
	Tailscale      TailscaleConfig `json:"tailscale"`
}

// DefaultConfig returns the shipped defaults: LAN only, anonymous, writable,
// filesystem root of the shared storage, auto-start on boot.
func DefaultConfig() Config {
	return Config{
		SharePath:     "/sdcard",
		ShareName:     "DufsBox",
		Port:          8080,
		Mode:          "lan",
		AuthMode:      "anonymous",
		Username:      "dufsbox",
		Password:      "",
		DefaultPolicy: "rw",
		Autostart:     true,
		LogLevel:      "info",
		Tailscale: TailscaleConfig{
			Hostname:  "dufsbox",
			AcceptDNS: false,
		},
	}
}

// ValidModes / ValidPolicies / ValidAuthModes are the accepted enum values.
var (
	ValidModes      = []string{"lan", "tailscale", "both"}
	ValidPolicies   = []string{"rw", "ro", "deny"}
	ValidAuthModes  = []string{"anonymous", "password"}
	ValidLogLevels  = []string{"trace", "debug", "info", "warn", "error"}
	defaultShareDir = "/sdcard"
)

func contains(list []string, v string) bool {
	for _, s := range list {
		if s == v {
			return true
		}
	}
	return false
}

// Normalize fills in defaults and clamps out-of-range values so a hand-edited
// config.json can never leave the daemon in an unrunnable state.
func (c *Config) Normalize() {
	if strings.TrimSpace(c.SharePath) == "" {
		c.SharePath = defaultShareDir
	}
	c.SharePath = cleanPath(c.SharePath)
	if strings.TrimSpace(c.ShareName) == "" {
		c.ShareName = "DufsBox"
	}
	if c.Port < 1 || c.Port > 65000 {
		c.Port = 8080
	}
	if !contains(ValidModes, c.Mode) {
		c.Mode = "lan"
	}
	if !contains(ValidAuthModes, c.AuthMode) {
		c.AuthMode = "anonymous"
	}
	if strings.TrimSpace(c.Username) == "" {
		c.Username = "dufsbox"
	}
	if !contains(ValidPolicies, c.DefaultPolicy) {
		c.DefaultPolicy = "rw"
	}
	if !contains(ValidLogLevels, strings.ToLower(c.LogLevel)) {
		c.LogLevel = "info"
	}
	c.LogLevel = strings.ToLower(c.LogLevel)
	if strings.TrimSpace(c.Tailscale.Hostname) == "" {
		c.Tailscale.Hostname = "dufsbox"
	}
}

// InternalPort is the loopback-only port dufs listens on. It is derived from the
// public port so the two can never collide, and dufs is never reachable from
// outside the device.
func (c Config) InternalPort() int {
	p := c.Port + 10000
	if p > 65535 {
		p = c.Port - 1
	}
	if p < 1 {
		p = 18080
	}
	return p
}

// TailscaleEnabled reports whether any tailnet listener should be running.
func (c Config) TailscaleEnabled() bool {
	return c.Mode == "tailscale" || c.Mode == "both"
}

// PublicBindAddress is the address the ACL proxy listens on.
//
// In pure tailnet mode the proxy is bound to loopback only: tailscaled's
// userspace netstack dials 127.0.0.1, so the tailnet still reaches it while the
// campus LAN cannot.
func (c Config) PublicBindAddress() string {
	if c.Mode == "tailscale" {
		return fmt.Sprintf("127.0.0.1:%d", c.Port)
	}
	return fmt.Sprintf(":%d", c.Port)
}

// AuthRule is the dufs --auth rule for the configured credential.
//
// dufs parses a rule as `<user>:<pass>@<path>[:perm]` (see split_account_paths /
// AccessPaths::merge in src/auth.rs), where:
//   - the split point is the first literal "@/", so a password must not contain it;
//   - a path written without an explicit permission defaults to READ-ONLY.
//
// That last point is the trap: `user:pass@/` would silently make every
// authenticated client read-only, which would defeat the whole per-device policy
// model. The `:rw` suffix is mandatory here because dufs-level permission is a
// fallback and the proxy is what actually decides per device.
func (c Config) AuthRule() string {
	return fmt.Sprintf("%s:%s@/:rw", c.Username, c.Password)
}

// AuthRuleError reports why the configured credential cannot be handed to dufs.
//
// An unparseable rule makes dufs exit at startup, so it is checked before the
// process is spawned instead of being discovered as a dead share.
func (c Config) AuthRuleError() error {
	if c.AuthMode != "password" {
		return nil
	}
	if c.Password == "" {
		// Not an error: an empty password means "no credential requested", and
		// DufsArgs then serves anonymously. Refusing here would make a half
		// finished settings screen take the share offline.
		return nil
	}
	if strings.Contains(c.Username, ":") {
		return fmt.Errorf("用户名不能包含冒号")
	}
	if strings.Contains(c.Username, "@/") || strings.Contains(c.Password, "@/") {
		return fmt.Errorf("用户名或密码不能包含字符序列 @/（dufs 用它分隔账号与路径）")
	}
	if strings.Contains(c.Username, "|") || strings.Contains(c.Password, "|") {
		return fmt.Errorf("用户名或密码不能包含竖线 |")
	}
	return nil
}

// DufsArgs builds the argument vector for the upstream dufs binary.
//
// dufs always gets full write permission because per-device read-only and deny
// are enforced by the proxy in front of it; a dufs-level restriction would apply
// to every device and make the per-device policy impossible.
func (c Config) DufsArgs() []string {
	args := []string{
		c.SharePath,
		"--bind", "127.0.0.1",
		"--port", strconv.Itoa(c.InternalPort()),
		"--allow-all",
	}
	if c.AuthMode == "password" && c.Password != "" {
		args = append(args, "--auth", c.AuthRule())
	}
	return args
}

// ConfigStore owns the in-memory config and its on-disk representation.
type ConfigStore struct {
	mu       sync.RWMutex
	cfg      Config
	paths    Paths
	onChange func(Config, Config)
}

// NewConfigStore loads config.json, creating it with defaults when absent.
func NewConfigStore(paths Paths) (*ConfigStore, error) {
	s := &ConfigStore{paths: paths, cfg: DefaultConfig()}
	raw, err := os.ReadFile(paths.ConfigFile)
	switch {
	case err == nil:
		raw = stripBOM(raw)
		var c Config
		if err := json.Unmarshal(raw, &c); err != nil {
			// Keep the defaults but do not lose the operator's broken file.
			_ = os.WriteFile(paths.ConfigFile+".corrupt", raw, 0o600)
			return nil, fmt.Errorf("config.json is not valid JSON (kept a copy at config.json.corrupt): %w", err)
		}
		c.Normalize()
		s.cfg = c
	case os.IsNotExist(err):
		if err := s.persist(s.cfg); err != nil {
			return nil, err
		}
	default:
		return nil, err
	}
	return s, nil
}

// Get returns a copy of the current configuration.
func (s *ConfigStore) Get() Config {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.cfg
}

// SetOnChange registers a callback invoked with (old, new) after every update.
func (s *ConfigStore) SetOnChange(fn func(Config, Config)) {
	s.mu.Lock()
	s.onChange = fn
	s.mu.Unlock()
}

// Update applies fn to the config, normalizes, persists and notifies.
func (s *ConfigStore) Update(fn func(*Config)) (Config, error) {
	s.mu.Lock()
	old := s.cfg
	next := s.cfg
	fn(&next)
	next.Normalize()
	s.cfg = next
	cb := s.onChange
	s.mu.Unlock()

	if err := s.persist(next); err != nil {
		return next, err
	}
	if cb != nil && old != next {
		cb(old, next)
	}
	return next, nil
}

func (s *ConfigStore) persist(c Config) error {
	if err := s.paths.EnsureDirs(); err != nil {
		return err
	}
	raw, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return err
	}
	raw = append(raw, '\n')
	tmp := s.paths.ConfigFile + ".tmp"
	if err := os.WriteFile(tmp, raw, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, s.paths.ConfigFile)
}

// ApplyPatch merges a raw JSON object into the config, honouring "presence" so
// that an explicit empty password clears it while an omitted password keeps it.
// It returns the updated config and whether a service restart is required.
func (s *ConfigStore) ApplyPatch(patch map[string]json.RawMessage) (Config, bool, error) {
	needRestart := false
	updated, err := s.Update(func(c *Config) {
		for k, v := range patch {
			switch k {
			case "share_path":
				if json.Unmarshal(v, &c.SharePath) == nil {
					needRestart = true
				}
			case "share_name":
				_ = json.Unmarshal(v, &c.ShareName)
			case "port":
				if json.Unmarshal(v, &c.Port) == nil {
					needRestart = true
				}
			case "mode":
				if json.Unmarshal(v, &c.Mode) == nil {
					needRestart = true
				}
			case "auth_mode":
				if json.Unmarshal(v, &c.AuthMode) == nil {
					needRestart = true
				}
			case "username":
				if json.Unmarshal(v, &c.Username) == nil {
					needRestart = true
				}
			case "password":
				if json.Unmarshal(v, &c.Password) == nil {
					needRestart = true
				}
			case "default_policy":
				_ = json.Unmarshal(v, &c.DefaultPolicy)
			case "readonly_global":
				if json.Unmarshal(v, &c.ReadonlyGlobal) == nil {
					needRestart = true
				}
			case "autostart":
				_ = json.Unmarshal(v, &c.Autostart)
			case "log_level":
				_ = json.Unmarshal(v, &c.LogLevel)
			case "tailscale":
				var ts TailscaleConfig
				if json.Unmarshal(v, &ts) == nil {
					// Partial object: only overwrite provided fields.
					var m map[string]json.RawMessage
					if json.Unmarshal(v, &m) == nil {
						cur := c.Tailscale
						if _, ok := m["hostname"]; ok {
							cur.Hostname = ts.Hostname
						}
						if _, ok := m["https_serve"]; ok {
							cur.HTTPSServe = ts.HTTPSServe
						}
						if _, ok := m["accept_dns"]; ok {
							cur.AcceptDNS = ts.AcceptDNS
						}
						if _, ok := m["login_server"]; ok {
							cur.LoginServer = ts.LoginServer
						}
						if _, ok := m["auth_key"]; ok {
							cur.AuthKey = ts.AuthKey
						}
						c.Tailscale = cur
					} else {
						c.Tailscale = ts
					}
					needRestart = true
				}
			}
		}
	})
	return updated, needRestart, err
}

// ConfigView is the API-safe projection of Config: the password is never
// returned, only whether one is set.
type ConfigView struct {
	SharePath      string `json:"share_path"`
	ShareName      string `json:"share_name"`
	Port           int    `json:"port"`
	Mode           string `json:"mode"`
	AuthMode       string `json:"auth_mode"`
	Username       string `json:"username"`
	PasswordSet    bool   `json:"password_set"`
	DefaultPolicy  string `json:"default_policy"`
	ReadonlyGlobal bool   `json:"readonly_global"`
	Autostart      bool   `json:"autostart"`
	LogLevel       string `json:"log_level"`
}

// View projects the config for the API.
func (c Config) View() ConfigView {
	return ConfigView{
		SharePath:      c.SharePath,
		ShareName:      c.ShareName,
		Port:           c.Port,
		Mode:           c.Mode,
		AuthMode:       c.AuthMode,
		Username:       c.Username,
		PasswordSet:    c.Password != "",
		DefaultPolicy:  c.DefaultPolicy,
		ReadonlyGlobal: c.ReadonlyGlobal,
		Autostart:      c.Autostart,
		LogLevel:       c.LogLevel,
	}
}
