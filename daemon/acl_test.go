package main

import (
	"net"
	"os"
	"path/filepath"
	"testing"
)

// testPaths builds a fully populated Paths rooted at a temp dir, so that
// ConfigStore.persist (which calls EnsureDirs) works in tests.
func testPaths(t *testing.T) Paths {
	t.Helper()
	dir := t.TempDir()
	p := Paths{
		StateDir:   dir,
		ModuleDir:  filepath.Join(dir, "mod"),
		BinDir:     filepath.Join(dir, "mod", "bin"),
		ConfigFile: filepath.Join(dir, "config.json"),
		ACLFile:    filepath.Join(dir, "acl.json"),
		RunDir:     filepath.Join(dir, "run"),
		LogDir:     filepath.Join(dir, "logs"),
		SockPath:   filepath.Join(dir, "run", "dufsboxd.sock"),
		PidFile:    filepath.Join(dir, "run", "dufsboxd.pid"),
	}
	return p
}

// ---------------------------------------------------------------------------
// ACL
// ---------------------------------------------------------------------------

func TestIsReadonlyMethodMirrorsDufs(t *testing.T) {
	// This set must stay identical to dufs's is_readonly_method() (src/auth.rs),
	// otherwise a read-only device would be blocked from logging in (CHECKAUTH)
	// or from browsing WebDAV (PROPFIND).
	readonly := []string{"GET", "HEAD", "OPTIONS", "PROPFIND", "CHECKAUTH", "LOGOUT", "get", "propfind"}
	for _, m := range readonly {
		if !IsReadonlyMethod(m) {
			t.Errorf("IsReadonlyMethod(%q) = false, want true", m)
		}
	}
	writes := []string{"PUT", "DELETE", "MKCOL", "MOVE", "COPY", "PATCH", "POST", "PROPPATCH", "LOCK", "UNLOCK"}
	for _, m := range writes {
		if IsReadonlyMethod(m) {
			t.Errorf("IsReadonlyMethod(%q) = true, want false", m)
		}
	}
}

func TestPolicyAllows(t *testing.T) {
	cases := []struct {
		policy Policy
		method string
		want   bool
	}{
		{PolicyReadWrite, "PUT", true},
		{PolicyReadWrite, "GET", true},
		{PolicyReadOnly, "GET", true},
		{PolicyReadOnly, "PROPFIND", true},
		{PolicyReadOnly, "PUT", false},
		{PolicyReadOnly, "DELETE", false},
		{PolicyReadOnly, "MKCOL", false},
		{PolicyDeny, "GET", false},
		{PolicyDeny, "PROPFIND", false},
		// An unknown policy must fail closed for writes and open for reads,
		// because that is what "read-write" means and the caller normalises first.
		{Policy("bogus"), "PUT", true},
	}
	for _, c := range cases {
		if got := c.policy.Allows(c.method); got != c.want {
			t.Errorf("Policy(%q).Allows(%q) = %v, want %v", c.policy, c.method, got, c.want)
		}
	}
}

func TestACLResolvePrecedence(t *testing.T) {
	a := NewEphemeralACL()
	mac := "aa:bb:cc:dd:ee:ff"
	ip := "192.168.1.7"

	// 1. Nothing configured -> default.
	if p, src := a.Resolve(ip, mac, PolicyReadWrite); p != PolicyReadWrite || src != "default" {
		t.Fatalf("empty ACL: got (%v,%v), want (rw,default)", p, src)
	}

	// 2. A CIDR rule applies.
	if err := a.Set(cidrRuleKey("192.168.1.0/24"), PolicyDeny); err != nil {
		t.Fatal(err)
	}
	if p, src := a.Resolve(ip, mac, PolicyReadWrite); p != PolicyDeny || src != "cidr" {
		t.Fatalf("cidr rule: got (%v,%v), want (deny,cidr)", p, src)
	}

	// 3. An exact IP beats the CIDR.
	if err := a.Set(ipRuleKey(ip), PolicyReadOnly); err != nil {
		t.Fatal(err)
	}
	if p, src := a.Resolve(ip, mac, PolicyReadWrite); p != PolicyReadOnly || src != "ip" {
		t.Fatalf("ip rule: got (%v,%v), want (ro,ip)", p, src)
	}

	// 4. The MAC beats the IP: this is what makes a rule survive a DHCP change.
	if err := a.Set(macRuleKey(mac), PolicyReadWrite); err != nil {
		t.Fatal(err)
	}
	if p, src := a.Resolve(ip, mac, PolicyReadWrite); p != PolicyReadWrite || src != "mac" {
		t.Fatalf("mac rule: got (%v,%v), want (rw,mac)", p, src)
	}

	// 5. With no MAC, the IP rule still wins.
	if p, src := a.Resolve(ip, "", PolicyReadWrite); p != PolicyReadOnly || src != "ip" {
		t.Fatalf("no mac: got (%v,%v), want (ro,ip)", p, src)
	}
}

func TestACLResolveLongestPrefixWins(t *testing.T) {
	a := NewEphemeralACL()
	mustSet(t, a, cidrRuleKey("10.0.0.0/8"), PolicyDeny)
	mustSet(t, a, cidrRuleKey("10.1.0.0/16"), PolicyReadOnly)
	mustSet(t, a, cidrRuleKey("10.1.2.0/24"), PolicyReadWrite)

	cases := []struct {
		ip   string
		want Policy
	}{
		{"10.9.9.9", PolicyDeny},
		{"10.1.9.9", PolicyReadOnly},
		{"10.1.2.9", PolicyReadWrite},
		{"11.0.0.1", PolicyReadWrite}, // outside every rule -> default
	}
	for _, c := range cases {
		if p, _ := a.Resolve(c.ip, "", PolicyReadWrite); p != c.want {
			t.Errorf("Resolve(%s) = %v, want %v", c.ip, p, c.want)
		}
	}
}

func TestACLSetInheritRemovesRule(t *testing.T) {
	a := NewEphemeralACL()
	ip := "192.168.0.5"
	mustSet(t, a, ipRuleKey(ip), PolicyDeny)
	if p, _ := a.Resolve(ip, "", PolicyReadWrite); p != PolicyDeny {
		t.Fatal("rule was not applied")
	}
	mustSet(t, a, ipRuleKey(ip), PolicyInherit)
	if p, src := a.Resolve(ip, "", PolicyReadWrite); p != PolicyReadWrite || src != "default" {
		t.Fatalf("after inherit: got (%v,%v), want (rw,default)", p, src)
	}
	if len(a.All()) != 0 {
		t.Fatalf("rule map should be empty, got %v", a.All())
	}
}

func TestACLSaveLoadRoundTrip(t *testing.T) {
	paths := testPaths(t)
	a, err := LoadACL(paths.ACLFile)
	if err != nil {
		t.Fatal(err)
	}
	mustSet(t, a, macRuleKey("AA:BB:CC:DD:EE:FF"), PolicyReadOnly)
	mustSet(t, a, ipRuleKey("192.168.1.7"), PolicyDeny)
	mustSet(t, a, cidrRuleKey("172.16.0.0/12"), PolicyReadWrite)

	reloaded, err := LoadACL(paths.ACLFile)
	if err != nil {
		t.Fatal(err)
	}
	if len(reloaded.All()) != 3 {
		t.Fatalf("reloaded %d rules, want 3: %v", len(reloaded.All()), reloaded.All())
	}
	if p, src := reloaded.Resolve("192.168.1.7", "aa:bb:cc:dd:ee:ff", PolicyReadWrite); p != PolicyReadOnly || src != "mac" {
		t.Fatalf("mac rule lost: got (%v,%v)", p, src)
	}
	if p, src := reloaded.Resolve("172.20.1.1", "", PolicyDeny); p != PolicyReadWrite || src != "cidr" {
		t.Fatalf("cidr rule lost: got (%v,%v)", p, src)
	}
}

func TestLoadACLRejectsInvalidEntries(t *testing.T) {
	paths := testPaths(t)
	if err := paths.EnsureDirs(); err != nil {
		t.Fatal(err)
	}
	// A hand-edited file with junk values must not make the daemon refuse to run.
	body := `{"rules":{"ip:1.2.3.4":"ro","ip:5.6.7.8":"nonsense","cidr:bad/net":"deny"}}`
	if err := os.WriteFile(paths.ACLFile, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	a, err := LoadACL(paths.ACLFile)
	if err != nil {
		t.Fatalf("LoadACL should tolerate bad entries, got %v", err)
	}
	if len(a.All()) != 1 {
		t.Fatalf("kept %v, want only the valid rule", a.All())
	}
	// A bad CIDR must not match anything.
	if p, src := a.Resolve("1.2.3.4", "", PolicyReadWrite); p != PolicyReadOnly || src != "ip" {
		t.Fatalf("valid rule not applied: (%v,%v)", p, src)
	}
}

func TestLooksLikeMAC(t *testing.T) {
	valid := []string{"aa:bb:cc:dd:ee:ff", "AA:BB:CC:DD:EE:FF", "00:11:22:33:44:55"}
	for _, m := range valid {
		if !looksLikeMAC(m) {
			t.Errorf("looksLikeMAC(%q) = false, want true", m)
		}
	}
	invalid := []string{"192.168.1.1", "aa:bb:cc:dd:ee", "zz:bb:cc:dd:ee:ff", "10.0.0.0/8", ""}
	for _, m := range invalid {
		if looksLikeMAC(m) {
			t.Errorf("looksLikeMAC(%q) = true, want false", m)
		}
	}
}

func TestRemoteIPAndLoopback(t *testing.T) {
	cases := []struct {
		addr string
		ip   string
		loop bool
	}{
		{"192.168.1.7:51234", "192.168.1.7", false},
		{"127.0.0.1:9999", "127.0.0.1", true},
		{"[::1]:9999", "::1", true},
	}
	for _, c := range cases {
		if got := RemoteIP(c.addr); got != c.ip {
			t.Errorf("RemoteIP(%q) = %q, want %q", c.addr, got, c.ip)
		}
		if got := IsLoopbackHost(c.addr); got != c.loop {
			t.Errorf("IsLoopbackHost(%q) = %v, want %v", c.addr, got, c.loop)
		}
	}
}

func TestCIDRContains(t *testing.T) {
	_, n, err := net.ParseCIDR("192.168.1.0/24")
	if err != nil {
		t.Fatal(err)
	}
	if !n.Contains(net.ParseIP("192.168.1.255")) {
		t.Error("expected /24 to contain .255")
	}
	if n.Contains(net.ParseIP("192.168.2.1")) {
		t.Error("expected /24 not to contain a different subnet")
	}
}

func mustSet(t *testing.T, a *ACL, key string, p Policy) {
	t.Helper()
	if err := a.Set(key, p); err != nil {
		t.Fatalf("Set(%s,%s): %v", key, p, err)
	}
}
