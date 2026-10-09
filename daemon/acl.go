package main

import (
	"encoding/json"
	"fmt"
	"net"
	"os"
	"sort"
	"strings"
	"sync"
)

// ---------------------------------------------------------------------------
// Per-device access policy
//
// dufs has no concept of a per-client rule: --auth is keyed by user and path,
// and --readonly/--allow-* are global. Per-device control therefore lives here,
// in front of dufs, and uses exactly the same definition of "read-only" that
// dufs uses internally (src/auth.rs: is_readonly_method) so that a device marked
// read-only sees the same behaviour dufs would have applied globally.
// ---------------------------------------------------------------------------

// Policy is the access level granted to one device.
type Policy string

const (
	// PolicyReadWrite passes every method through to dufs.
	PolicyReadWrite Policy = "rw"
	// PolicyReadOnly allows only the read-only method set.
	PolicyReadOnly Policy = "ro"
	// PolicyDeny rejects every request (the device still shows up in the list so
	// the operator can see who was turned away).
	PolicyDeny Policy = "deny"
	// PolicyInherit removes an explicit rule and falls back to the default.
	PolicyInherit Policy = "default"
)

// IsReadonlyMethod mirrors dufs's is_readonly_method().
//
// CHECKAUTH and LOGOUT are dufs-specific methods used by its web UI for digest
// authentication; treating them as writes would break login for read-only users.
func IsReadonlyMethod(method string) bool {
	switch strings.ToUpper(method) {
	case "GET", "HEAD", "OPTIONS", "PROPFIND", "CHECKAUTH", "LOGOUT":
		return true
	}
	return false
}

// Allows reports whether a policy permits the given HTTP method.
func (p Policy) Allows(method string) bool {
	switch p {
	case PolicyDeny:
		return false
	case PolicyReadOnly:
		return IsReadonlyMethod(method)
	default:
		return true
	}
}

// ValidPolicyName reports whether s is an accepted policy string.
func ValidPolicyName(s string) bool {
	return contains([]string{string(PolicyReadWrite), string(PolicyReadOnly), string(PolicyDeny), string(PolicyInherit)}, s)
}

// Rule key prefixes. A rule is always keyed by the most specific identity we
// have: a MAC address survives DHCP lease changes, an IP does not.
const (
	keyPrefixMAC  = "mac:"
	keyPrefixIP   = "ip:"
	keyPrefixCIDR = "cidr:"
)

func macRuleKey(mac string) string   { return keyPrefixMAC + strings.ToLower(strings.TrimSpace(mac)) }
func ipRuleKey(ip string) string     { return keyPrefixIP + strings.TrimSpace(ip) }
func cidrRuleKey(cidr string) string { return keyPrefixCIDR + strings.TrimSpace(cidr) }

type cidrRule struct {
	net    *net.IPNet
	policy Policy
}

// ACL is the persisted rule set (acl.json).
type ACL struct {
	mu      sync.RWMutex
	path    string
	rules   map[string]Policy
	parsed  []cidrRule // CIDR rules pre-parsed, longest prefix first
	persist bool
}

// LoadACL reads acl.json (missing file yields an empty rule set).
func LoadACL(path string) (*ACL, error) {
	a := &ACL{path: path, rules: map[string]Policy{}, persist: true}
	raw, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return a, nil
		}
		return nil, err
	}
	raw = stripBOM(raw)
	if len(strings.TrimSpace(string(raw))) == 0 {
		return a, nil
	}
	var stored struct {
		Rules map[string]string `json:"rules"`
	}
	if err := json.Unmarshal(raw, &stored); err != nil {
		return nil, fmt.Errorf("acl.json is not valid JSON: %w", err)
	}
	for k, v := range stored.Rules {
		p := Policy(v)
		if !ValidPolicyName(string(p)) || p == PolicyInherit {
			continue
		}
		// Drop a malformed CIDR rather than storing a rule that can never match.
		if strings.HasPrefix(k, keyPrefixCIDR) {
			if _, _, err := net.ParseCIDR(strings.TrimPrefix(k, keyPrefixCIDR)); err != nil {
				continue
			}
		}
		a.rules[k] = p
	}
	a.rebuild()
	return a, nil
}

// NewEphemeralACL returns an in-memory ACL (used by tests).
func NewEphemeralACL() *ACL {
	return &ACL{rules: map[string]Policy{}}
}

// rebuild recompiles the CIDR list, longest prefix first so that a /32 beats a /24.
// Caller must hold the write lock (or be in a constructor).
func (a *ACL) rebuild() {
	a.parsed = a.parsed[:0]
	for k, p := range a.rules {
		if !strings.HasPrefix(k, keyPrefixCIDR) {
			continue
		}
		_, n, err := net.ParseCIDR(strings.TrimPrefix(k, keyPrefixCIDR))
		if err != nil {
			continue
		}
		a.parsed = append(a.parsed, cidrRule{net: n, policy: p})
	}
	sort.Slice(a.parsed, func(i, j int) bool {
		oi, _ := a.parsed[i].net.Mask.Size()
		oj, _ := a.parsed[j].net.Mask.Size()
		return oi > oj
	})
}

// Save writes the rule set atomically.
func (a *ACL) Save() error {
	a.mu.RLock()
	snapshot := make(map[string]string, len(a.rules))
	for k, v := range a.rules {
		snapshot[k] = string(v)
	}
	a.mu.RUnlock()

	if !a.persist || a.path == "" {
		return nil
	}
	raw, err := json.MarshalIndent(struct {
		Rules map[string]string `json:"rules"`
	}{Rules: snapshot}, "", "  ")
	if err != nil {
		return err
	}
	raw = append(raw, '\n')
	tmp := a.path + ".tmp"
	if err := os.WriteFile(tmp, raw, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, a.path)
}

// Set stores (or, for PolicyInherit, deletes) a rule.
func (a *ACL) Set(key string, p Policy) error {
	a.mu.Lock()
	if p == PolicyInherit {
		delete(a.rules, key)
	} else {
		a.rules[key] = p
	}
	a.rebuild()
	a.mu.Unlock()
	return a.Save()
}

// Remove deletes a rule by key. It reports whether a rule existed.
func (a *ACL) Remove(key string) (bool, error) {
	a.mu.Lock()
	_, existed := a.rules[key]
	delete(a.rules, key)
	a.rebuild()
	a.mu.Unlock()
	if err := a.Save(); err != nil {
		return existed, err
	}
	return existed, nil
}

// All returns a copy of the raw rule map.
func (a *ACL) All() map[string]Policy {
	a.mu.RLock()
	defer a.mu.RUnlock()
	out := make(map[string]Policy, len(a.rules))
	for k, v := range a.rules {
		out[k] = v
	}
	return out
}

// Rules returns the explicit rules affecting one device, so the UI can show why
// a device has the policy it has.
func (a *ACL) Rules(ip, mac string) map[string]Policy {
	all := a.All()
	out := map[string]Policy{}
	if mac != "" {
		if p, ok := all[macRuleKey(mac)]; ok {
			out[macRuleKey(mac)] = p
		}
	}
	if p, ok := all[ipRuleKey(ip)]; ok {
		out[ipRuleKey(ip)] = p
	}
	if i := net.ParseIP(ip); i != nil {
		for _, r := range a.snapshotCIDR() {
			if r.net.Contains(i) {
				out[cidrRuleKey(r.net.String())] = r.policy
			}
		}
	}
	return out
}

func (a *ACL) snapshotCIDR() []cidrRule {
	a.mu.RLock()
	defer a.mu.RUnlock()
	out := make([]cidrRule, len(a.parsed))
	copy(out, a.parsed)
	return out
}

// Resolve returns the effective policy for a request source.
//
// Precedence: exact MAC -> exact IP -> longest matching CIDR -> configured
// default. source is one of "mac", "ip", "cidr", "default" and is surfaced in
// the API so the App can explain the decision.
func (a *ACL) Resolve(ip, mac string, def Policy) (Policy, string) {
	a.mu.RLock()
	defer a.mu.RUnlock()
	if mac != "" {
		if p, ok := a.rules[macRuleKey(mac)]; ok {
			return p, "mac"
		}
	}
	if p, ok := a.rules[ipRuleKey(ip)]; ok {
		return p, "ip"
	}
	if parsed := net.ParseIP(ip); parsed != nil {
		for _, r := range a.parsed {
			if r.net.Contains(parsed) {
				return r.policy, "cidr"
			}
		}
	}
	if def == "" {
		def = PolicyReadWrite
	}
	return def, "default"
}
