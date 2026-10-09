package main

import (
	"bufio"
	"net"
	"os"
	"os/exec"
	"strings"
	"sync"
	"time"
)

// ---------------------------------------------------------------------------
// Network facts: local addresses and the IP -> MAC mapping.
//
// Per-device rules are keyed by MAC where possible because a DHCP lease change
// would otherwise silently move a policy onto a different machine. The MAC comes
// from the kernel's ARP/neighbour table, which is only populated for addresses
// the device has actually exchanged packets with - exactly the set of devices
// that are talking to the share.
// ---------------------------------------------------------------------------

// LocalIPv4s returns the device's non-loopback, non-link-local IPv4 addresses.
// These become the "connection address" shown in the App's status tab.
func LocalIPv4s() []string {
	ifaces, err := net.Interfaces()
	if err != nil {
		return nil
	}
	var out []string
	for _, ifc := range ifaces {
		if ifc.Flags&net.FlagUp == 0 || ifc.Flags&net.FlagLoopback != 0 {
			continue
		}
		addrs, err := ifc.Addrs()
		if err != nil {
			continue
		}
		for _, a := range addrs {
			ipnet, ok := a.(*net.IPNet)
			if !ok {
				continue
			}
			ip4 := ipnet.IP.To4()
			if ip4 == nil || ip4.IsLoopback() || ip4.IsLinkLocalUnicast() {
				continue
			}
			out = append(out, ip4.String())
		}
	}
	return out
}

// IsLoopbackHost reports whether the host part of an address is loopback.
func IsLoopbackHost(remoteAddr string) bool {
	host, _, err := net.SplitHostPort(remoteAddr)
	if err != nil {
		host = remoteAddr
	}
	ip := net.ParseIP(strings.Trim(host, "[]"))
	return ip != nil && ip.IsLoopback()
}

// RemoteIP extracts the IP from a "host:port" remote address.
func RemoteIP(remoteAddr string) string {
	host, _, err := net.SplitHostPort(remoteAddr)
	if err != nil {
		host = remoteAddr
	}
	return strings.Trim(host, "[]")
}

// arpCache memoises MAC lookups: the tracker asks for the MAC of every request,
// and re-reading /proc/net/arp on each one would be wasteful.
type arpCache struct {
	mu      sync.Mutex
	entries map[string]macEntry
	ttl     time.Duration
	full    map[string]string // last full table snapshot
	fullAt  time.Time
}

type macEntry struct {
	mac string
	at  time.Time
}

func newARPCache(ttl time.Duration) *arpCache {
	if ttl <= 0 {
		ttl = 15 * time.Second
	}
	return &arpCache{entries: map[string]macEntry{}, ttl: ttl, full: map[string]string{}}
}

// LookupMAC returns the MAC for ip, or "" when the neighbour table has no
// complete entry. A failed lookup is cached for a short window so a device that
// is not in the ARP table yet does not cause a table scan on every request.
func (c *arpCache) LookupMAC(ip string) string {
	if ip == "" {
		return ""
	}
	c.mu.Lock()
	if e, ok := c.entries[ip]; ok && time.Since(e.at) < c.ttl {
		c.mu.Unlock()
		return e.mac
	}
	c.mu.Unlock()

	table := c.table()
	mac := table[ip]

	c.mu.Lock()
	c.entries[ip] = macEntry{mac: mac, at: time.Now()}
	c.mu.Unlock()
	return mac
}

// table returns the kernel neighbour table, preferring /proc/net/arp and
// falling back to `ip neigh` for devices where procfs is restricted.
func (c *arpCache) table() map[string]string {
	c.mu.Lock()
	if c.full != nil && time.Since(c.fullAt) < 3*time.Second {
		defer c.mu.Unlock()
		out := make(map[string]string, len(c.full))
		for k, v := range c.full {
			out[k] = v
		}
		return out
	}
	c.mu.Unlock()

	out := readProcNetARP()
	if len(out) == 0 {
		out = readIPNeigh()
	}
	if len(out) == 0 {
		out = map[string]string{}
	}

	c.mu.Lock()
	c.full = out
	c.fullAt = time.Now()
	c.mu.Unlock()

	// Return a copy so callers cannot mutate the cache.
	cp := make(map[string]string, len(out))
	for k, v := range out {
		cp[k] = v
	}
	return cp
}

// waitForPort waits until a TCP address accepts a connection.
//
// Used after (re)starting dufs so that a configuration change does not leave a
// window in which the proxy is listening but its backend is not yet bound; the
// caller's next request would otherwise get a 502 for no visible reason.
func waitForPort(addr string, timeout time.Duration) bool {
	deadline := time.Now().Add(timeout)
	for {
		conn, err := net.DialTimeout("tcp", addr, 300*time.Millisecond)
		if err == nil {
			_ = conn.Close()
			return true
		}
		if time.Now().After(deadline) {
			return false
		}
		time.Sleep(100 * time.Millisecond)
	}
}

// readProcNetARP parses /proc/net/arp.
//
// Format: IPaddress HWtype Flags HWaddress Mask Device
// Only entries with the complete flag (0x2) carry a usable MAC.
func readProcNetARP() map[string]string {
	f, err := os.Open("/proc/net/arp")
	if err != nil {
		return nil
	}
	defer f.Close()

	out := map[string]string{}
	scanner := bufio.NewScanner(f)
	first := true
	for scanner.Scan() {
		if first { // header
			first = false
			continue
		}
		fields := strings.Fields(scanner.Text())
		if len(fields) < 4 {
			continue
		}
		ip, flags, mac := fields[0], fields[2], fields[3]
		if flags != "0x2" {
			continue
		}
		if mac == "00:00:00:00:00:00" || !strings.Contains(mac, ":") {
			continue
		}
		out[ip] = strings.ToLower(mac)
	}
	return out
}

// readIPNeigh parses `ip neigh show` as a fallback.
func readIPNeigh() map[string]string {
	cmd := exec.Command("ip", "neigh", "show")
	cmd.Env = append(os.Environ(), "LC_ALL=C")
	raw, err := cmd.Output()
	if err != nil {
		return nil
	}
	out := map[string]string{}
	for _, line := range strings.Split(string(raw), "\n") {
		fields := strings.Fields(line)
		if len(fields) < 5 {
			continue
		}
		// e.g. 192.168.1.7 dev wlan0 lladdr aa:bb:cc:dd:ee:ff REACHABLE
		ip := fields[0]
		for i := 0; i+1 < len(fields); i++ {
			if fields[i] == "lladdr" {
				mac := strings.ToLower(fields[i+1])
				if mac != "00:00:00:00:00:00" {
					out[ip] = mac
				}
				break
			}
		}
	}
	return out
}
