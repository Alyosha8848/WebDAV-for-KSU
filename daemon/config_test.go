package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestNormalizeFillsDefaultsAndClamps(t *testing.T) {
	c := Config{} // a hand-edited or truncated config.json
	c.Normalize()

	if c.SharePath != "/sdcard" {
		t.Errorf("SharePath = %q, want /sdcard", c.SharePath)
	}
	if c.Port != 8080 {
		t.Errorf("Port = %d, want 8080", c.Port)
	}
	if c.Mode != "lan" {
		t.Errorf("Mode = %q, want lan", c.Mode)
	}
	if c.AuthMode != "anonymous" {
		t.Errorf("AuthMode = %q, want anonymous", c.AuthMode)
	}
	if c.DefaultPolicy != "rw" {
		t.Errorf("DefaultPolicy = %q, want rw", c.DefaultPolicy)
	}
	if c.LogLevel != "info" {
		t.Errorf("LogLevel = %q, want info", c.LogLevel)
	}
	if c.Username == "" {
		t.Error("Username should have a default")
	}
	if c.Tailscale.Hostname == "" {
		t.Error("Tailscale.Hostname should have a default")
	}

	bad := Config{Port: 99999, Mode: "wan", AuthMode: "ldap", DefaultPolicy: "sudo", LogLevel: "LOUD"}
	bad.Normalize()
	if bad.Port != 8080 || bad.Mode != "lan" || bad.AuthMode != "anonymous" || bad.DefaultPolicy != "rw" || bad.LogLevel != "info" {
		t.Errorf("out-of-range values were not clamped: %+v", bad)
	}
}

func TestNormalizeCleansSharePath(t *testing.T) {
	c := Config{SharePath: "/sdcard/Download/../DCIM/"}
	c.Normalize()
	if c.SharePath != "/sdcard/DCIM" {
		t.Errorf("SharePath = %q, want /sdcard/DCIM", c.SharePath)
	}
}

func TestInternalPortIsDerivedAndInRange(t *testing.T) {
	if got := (Config{Port: 8080}).InternalPort(); got != 18080 {
		t.Errorf("InternalPort(8080) = %d, want 18080", got)
	}
	// Must stay inside the valid TCP range even for a high public port.
	if got := (Config{Port: 60000}).InternalPort(); got < 1 || got > 65535 {
		t.Errorf("InternalPort(60000) = %d, out of range", got)
	}
	// And must never collide with the public port.
	for _, p := range []int{1, 80, 443, 8080, 50000, 65500} {
		c := Config{Port: p}
		if c.InternalPort() == p {
			t.Errorf("InternalPort(%d) collides with the public port", p)
		}
	}
}

func TestPublicBindAddress(t *testing.T) {
	// Tailnet-only mode must bind loopback: tailscaled's userspace netstack dials
	// 127.0.0.1, so the tailnet still reaches it while the LAN cannot.
	if got := (Config{Port: 8080, Mode: "tailscale"}).PublicBindAddress(); got != "127.0.0.1:8080" {
		t.Errorf("tailscale mode bind = %q, want 127.0.0.1:8080", got)
	}
	for _, mode := range []string{"lan", "both"} {
		if got := (Config{Port: 9000, Mode: mode}).PublicBindAddress(); got != ":9000" {
			t.Errorf("%s mode bind = %q, want :9000", mode, got)
		}
	}
}

func TestDufsArgs(t *testing.T) {
	c := Config{SharePath: "/sdcard/DCIM", Port: 8080, AuthMode: "anonymous"}
	c.Normalize()
	args := strings.Join(c.DufsArgs(), " ")

	// dufs must never be reachable from the network.
	if !strings.Contains(args, "--bind 127.0.0.1") {
		t.Errorf("dufs must bind loopback, got: %s", args)
	}
	if !strings.Contains(args, "--port 18080") {
		t.Errorf("dufs must use the internal port, got: %s", args)
	}
	// Full write permission is mandatory: per-device policy is enforced by the
	// proxy, so a dufs-level restriction would apply to everyone.
	if !strings.Contains(args, "--allow-all") {
		t.Errorf("dufs must be started with --allow-all, got: %s", args)
	}
	if strings.Contains(args, "--auth") {
		t.Errorf("anonymous mode must not pass --auth, got: %s", args)
	}
	if !strings.HasPrefix(args, "/sdcard/DCIM") {
		t.Errorf("share path must be the first argument, got: %s", args)
	}

	// Password mode adds a single global credential that the proxy passes through.
	c2 := Config{SharePath: "/sdcard", Port: 8080, AuthMode: "password", Username: "dufsbox", Password: "s3cret"}
	c2.Normalize()
	args2 := strings.Join(c2.DufsArgs(), " ")
	if !strings.Contains(args2, "--auth dufsbox:s3cret@/:rw") {
		t.Errorf("password mode must pass --auth in dufs's <user>:<pass>@<path>:<perm> form, got: %s", args2)
	}

	// A password auth mode with an empty password is not a valid credential and
	// must not produce an entry that locks everyone out.
	c3 := Config{SharePath: "/sdcard", Port: 8080, AuthMode: "password", Password: ""}
	c3.Normalize()
	if strings.Contains(strings.Join(c3.DufsArgs(), " "), "--auth") {
		t.Error("empty password must not emit --auth")
	}
}

func TestAuthRuleMustGrantReadWrite(t *testing.T) {
	// Regression guard. dufs's AccessPaths::merge defaults a path written without
	// an explicit permission to READ-ONLY, so `user:pass@/` would silently make
	// every authenticated client read-only and break per-device read-write.
	// Verified against dufs 0.46.0: with `@/` a PUT returns 403, with `@/:rw` 201.
	c := Config{AuthMode: "password", Username: "u", Password: "p"}
	rule := c.AuthRule()
	if rule != "u:p@/:rw" {
		t.Fatalf("AuthRule() = %q, want %q", rule, "u:p@/:rw")
	}
	if !strings.HasSuffix(rule, ":rw") {
		t.Fatal("the rule must carry an explicit :rw suffix")
	}
	// The delimiter dufs actually splits on is the first literal "@/".
	if !strings.Contains(rule, "@/") {
		t.Fatal("the rule must contain the @/ delimiter dufs splits on")
	}
}

func TestAuthRuleErrorRejectsDelimiterInCredential(t *testing.T) {
	// A credential containing "@/", ":" (in the username) or "|" cannot be
	// expressed as a dufs auth rule; it must be rejected before dufs is spawned,
	// because dufs exits at startup on an unparseable rule.
	bad := []Config{
		{AuthMode: "password", Username: "u", Password: "p@/x"},
		{AuthMode: "password", Username: "u@/x", Password: "p"},
		{AuthMode: "password", Username: "u:v", Password: "p"},
		{AuthMode: "password", Username: "u", Password: "p|x"},
	}
	for _, c := range bad {
		if err := c.AuthRuleError(); err == nil {
			t.Errorf("AuthRuleError(%+v) = nil, want an error", c)
		}
	}
	good := []Config{
		{AuthMode: "password", Username: "u", Password: "p"},
		{AuthMode: "password", Username: "u", Password: "p:with:colons"},
		{AuthMode: "anonymous", Username: "u", Password: "p@/x"},
		{AuthMode: "password", Username: "u", Password: ""},
	}
	for _, c := range good {
		if err := c.AuthRuleError(); err != nil {
			t.Errorf("AuthRuleError(%+v) = %v, want nil", c, err)
		}
	}
}

func TestConfigStoreRoundTrip(t *testing.T) {
	paths := testPaths(t)
	store, err := NewConfigStore(paths)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(paths.ConfigFile); err != nil {
		t.Fatalf("config.json was not created: %v", err)
	}
	if _, err := store.Update(func(c *Config) {
		c.SharePath = "/sdcard/Download"
		c.Port = 9100
	}); err != nil {
		t.Fatal(err)
	}

	reloaded, err := NewConfigStore(paths)
	if err != nil {
		t.Fatal(err)
	}
	got := reloaded.Get()
	if got.SharePath != "/sdcard/Download" || got.Port != 9100 {
		t.Fatalf("reload lost values: %+v", got)
	}
}

func TestConfigStoreSurvivesCorruptFile(t *testing.T) {
	paths := testPaths(t)
	if err := paths.EnsureDirs(); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(paths.ConfigFile, []byte("{ this is not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := NewConfigStore(paths); err == nil {
		t.Fatal("expected an error for a corrupt config")
	}
	// The original file must be preserved for the operator to inspect.
	if _, err := os.Stat(paths.ConfigFile + ".corrupt"); err != nil {
		t.Errorf("corrupt config was not preserved: %v", err)
	}
}

func TestApplyPatchPresenceSemantics(t *testing.T) {
	paths := testPaths(t)
	store, err := NewConfigStore(paths)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.Update(func(c *Config) {
		c.Password = "original"
		c.AuthMode = "password"
	}); err != nil {
		t.Fatal(err)
	}

	// An omitted password keeps the stored one, and must not force a restart.
	patch := map[string]json.RawMessage{"share_name": json.RawMessage(`"新名称"`)}
	_, restart, err := store.ApplyPatch(patch)
	if err != nil {
		t.Fatal(err)
	}
	if restart {
		t.Error("renaming the share must not require a restart")
	}
	if store.Get().Password != "original" {
		t.Errorf("password was changed by an unrelated patch: %q", store.Get().Password)
	}
	if store.Get().ShareName != "新名称" {
		t.Errorf("ShareName = %q", store.Get().ShareName)
	}

	// An explicit empty password clears it and does require a restart.
	patch = map[string]json.RawMessage{"password": json.RawMessage(`""`)}
	_, restart, err = store.ApplyPatch(patch)
	if err != nil {
		t.Fatal(err)
	}
	if !restart {
		t.Error("changing the password must require a restart")
	}
	if store.Get().Password != "" {
		t.Errorf("password should be cleared, got %q", store.Get().Password)
	}
}

func TestApplyPatchRestartFlags(t *testing.T) {
	paths := testPaths(t)
	store, err := NewConfigStore(paths)
	if err != nil {
		t.Fatal(err)
	}
	restartFields := []string{"share_path", "port", "mode", "auth_mode", "username", "password", "readonly_global"}
	noRestartFields := map[string]string{
		"share_name":     `"x"`,
		"default_policy": `"ro"`,
		"autostart":      `false`,
		"log_level":      `"debug"`,
	}
	for _, f := range restartFields {
		var value string
		switch f {
		case "share_path":
			value = `"/sdcard/DCIM"`
		case "port":
			value = `9000`
		case "mode":
			value = `"both"`
		case "auth_mode":
			value = `"password"`
		case "username":
			value = `"someone"`
		case "password":
			value = `"pw"`
		case "readonly_global":
			value = `true`
		}
		patch := map[string]json.RawMessage{f: json.RawMessage(value)}
		_, restart, err := store.ApplyPatch(patch)
		if err != nil {
			t.Fatalf("%s: %v", f, err)
		}
		if !restart {
			t.Errorf("field %q should require a restart", f)
		}
	}
	for f, value := range noRestartFields {
		patch := map[string]json.RawMessage{f: json.RawMessage(value)}
		_, restart, err := store.ApplyPatch(patch)
		if err != nil {
			t.Fatalf("%s: %v", f, err)
		}
		if restart {
			t.Errorf("field %q should not require a restart", f)
		}
	}
}

func TestApplyPatchPartialTailscale(t *testing.T) {
	paths := testPaths(t)
	store, err := NewConfigStore(paths)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.Update(func(c *Config) {
		c.Tailscale.HTTPSServe = true
		c.Tailscale.Hostname = "keepme"
	}); err != nil {
		t.Fatal(err)
	}
	// Patching only the hostname must not silently reset https_serve.
	patch := map[string]json.RawMessage{"tailscale": json.RawMessage(`{"hostname":"newname"}`)}
	if _, _, err := store.ApplyPatch(patch); err != nil {
		t.Fatal(err)
	}
	ts := store.Get().Tailscale
	if ts.Hostname != "newname" {
		t.Errorf("Hostname = %q, want newname", ts.Hostname)
	}
	if !ts.HTTPSServe {
		t.Error("https_serve was reset by a partial patch")
	}
}

func TestConfigViewHidesPassword(t *testing.T) {
	c := Config{Password: "topsecret", AuthMode: "password"}
	c.Normalize()
	view := c.View()
	if !view.PasswordSet {
		t.Error("PasswordSet should be true")
	}
	encoded, err := json.Marshal(view)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(encoded), "topsecret") {
		t.Fatalf("the password leaked into the API view: %s", encoded)
	}
}

func TestConfigStoreToleratesBOM(t *testing.T) {
	paths := testPaths(t)
	if err := paths.EnsureDirs(); err != nil {
		t.Fatal(err)
	}
	// PowerShell's `Set-Content -Encoding UTF8` adds a BOM; a config written that
	// way must not silently reset the operator's settings.
	body := "\uFEFF" + `{"share_path":"/sdcard/DCIM","port":9123,"mode":"both"}`
	if err := os.WriteFile(paths.ConfigFile, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	store, err := NewConfigStore(paths)
	if err != nil {
		t.Fatalf("a BOM must not be treated as a corrupt config: %v", err)
	}
	got := store.Get()
	if got.SharePath != "/sdcard/DCIM" || got.Port != 9123 || got.Mode != "both" {
		t.Fatalf("BOM-prefixed config was not loaded correctly: %+v", got)
	}
}

func TestLoadACLToleratesBOM(t *testing.T) {
	paths := testPaths(t)
	if err := paths.EnsureDirs(); err != nil {
		t.Fatal(err)
	}
	body := "\uFEFF" + `{"rules":{"ip:192.168.1.7":"ro"}}`
	if err := os.WriteFile(paths.ACLFile, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	a, err := LoadACL(paths.ACLFile)
	if err != nil {
		t.Fatalf("a BOM must not be treated as a corrupt ACL: %v", err)
	}
	if p, _ := a.Resolve("192.168.1.7", "", PolicyReadWrite); p != PolicyReadOnly {
		t.Fatalf("BOM-prefixed ACL was not loaded: got %v", p)
	}
}

func TestDefaultPathsHonourEnvOverrides(t *testing.T) {
	t.Setenv("DUFSBOX_STATE", "/tmp/dufsbox-test")
	t.Setenv("DUFSBOX_MODDIR", "/tmp/dufsbox-mod")
	t.Setenv("DUFSBOX_DUFS", "/tmp/custom-dufs")
	p := DefaultPaths()
	if p.StateDir != "/tmp/dufsbox-test" {
		t.Errorf("StateDir = %q", p.StateDir)
	}
	if p.DufsBinary != "/tmp/custom-dufs" {
		t.Errorf("DufsBinary = %q", p.DufsBinary)
	}
	if p.SockPath != filepath.Join("/tmp/dufsbox-test", "run", "dufsboxd.sock") {
		t.Errorf("SockPath = %q", p.SockPath)
	}
	if p.TailscaledBinary != filepath.Join("/tmp/dufsbox-mod", "bin", "arm64", "tailscaled") {
		t.Errorf("TailscaledBinary = %q", p.TailscaledBinary)
	}
}
