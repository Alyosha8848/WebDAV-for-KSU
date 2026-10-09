package main

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"regexp"
	"strings"
	"time"
)

// ---------------------------------------------------------------------------
// Tailscale (userspace networking)
//
// Kernel-mode Tailscale cannot work on Android without a TUN device and a
// compatible iptables stack, and the official app monopolises the single VPN
// slot. tailscaled in userspace-networking mode needs neither: it terminates the
// WireGuard tunnel inside the process and hands inbound connections to
// 127.0.0.1, which is where the ACL proxy is listening. The node therefore gets
// a stable 100.x.y.z address that survives campus-network re-authentication and
// DHCP changes.
// ---------------------------------------------------------------------------

// TailscaleInfo is the API projection of the tailnet side.
type TailscaleInfo struct {
	Enabled    bool   `json:"enabled"`
	State      string `json:"state"`
	IP         string `json:"ip"`
	DNS        string `json:"dns"`
	AuthURL    string `json:"auth_url"`
	Version    string `json:"version"`
	HTTPSServe bool   `json:"https_serve"`
}

// Tailscale states surfaced to the App.
const (
	TSDisabled   = "disabled"
	TSStopped    = "stopped"
	TSNeedsLogin = "needs_login"
	TSOnline     = "online"
	TSOffline    = "offline"
	TSError      = "error"
)

// authURLRe extracts the interactive login URL from tailscale/tailscaled output.
var authURLRe = regexp.MustCompile(`https://login\.tailscale\.com/[A-Za-z0-9_?=./&%-]+`)

// tailscaleEnv builds the environment tailscaled needs on Android.
//
// Android keeps its CA store in /system/etc/security/cacerts (not
// /etc/ssl/certs), and Go's crypto/x509 honours SSL_CERT_DIR, so without this the
// HTTPS control-plane connection fails with a certificate error.
func tailscaleEnv(paths Paths) []string {
	env := append([]string{}, os.Environ()...)
	env = append(env,
		"TMPDIR="+paths.TmpDir(),
		"HOME="+paths.StateDir,
		"SSL_CERT_DIR=/system/etc/security/cacerts",
	)
	return env
}

// ts runs `tailscale --socket=... <args>` with a timeout and returns its output.
func (s *Supervisor) ts(timeout time.Duration, args ...string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	full := append([]string{"--socket=" + s.paths.TailscaleSocket()}, args...)
	cmd := exec.CommandContext(ctx, s.paths.TailscaleBinary, full...)
	cmd.Env = tailscaleEnv(s.paths)
	out, err := cmd.CombinedOutput()
	return string(out), err
}

// tailscaleStatusJSON runs `tailscale status --json` and returns the raw object.
func (s *Supervisor) tailscaleStatusJSON() map[string]any {
	out, err := s.ts(8*time.Second, "status", "--json")
	if err != nil && strings.TrimSpace(out) == "" {
		return nil
	}
	var m map[string]any
	if json.Unmarshal([]byte(out), &m) != nil {
		return nil
	}
	return m
}

// applyTailscaleStatus refreshes the cached tailnet facts from tailscaled.
func (s *Supervisor) applyTailscaleStatus() {
	m := s.tailscaleStatusJSON()

	s.mu.Lock()
	defer s.mu.Unlock()
	if m == nil {
		// No control socket yet, or the daemon is not up.
		if s.tsChild != nil && s.tsChild.Alive() {
			s.tsState = TSOffline
		} else if s.cfg.Get().TailscaleEnabled() {
			s.tsState = TSStopped
		} else {
			s.tsState = TSDisabled
		}
		return
	}
	if v, ok := m["BackendState"].(string); ok {
		switch v {
		case "Running":
			s.tsState = TSOnline
		case "NeedsLogin", "NoState", "Stopped":
			if v == "NeedsLogin" || s.tsAuthURL != "" {
				s.tsState = TSNeedsLogin
			} else {
				s.tsState = TSStopped
			}
		case "Starting":
			s.tsState = TSOffline
		default:
			s.tsState = TSOffline
		}
	}
	if self, ok := m["Self"].(map[string]any); ok {
		if ips, ok := self["TailscaleIPs"].([]any); ok {
			s.tsIP = ""
			for _, v := range ips {
				str, _ := v.(string)
				if strings.Contains(str, ".") { // prefer IPv4 for the displayed URL
					s.tsIP = str
					break
				}
				if s.tsIP == "" {
					s.tsIP = str
				}
			}
		}
		if dns, ok := self["DNSName"].(string); ok {
			s.tsDNS = strings.TrimSuffix(dns, ".")
		}
	}
	if v, ok := m["Version"].(string); ok && v != "" {
		s.tsVersion = v
	}
}

// recordAuthURL scrapes an interactive login URL out of tailscale output.
func (s *Supervisor) recordAuthURL(text string) {
	if m := authURLRe.FindString(text); m != "" {
		s.mu.Lock()
		if s.tsAuthURL != m {
			s.tsAuthURL = m
			s.tsState = TSNeedsLogin
		}
		s.mu.Unlock()
	}
}
