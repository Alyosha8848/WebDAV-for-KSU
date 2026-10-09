package main

import (
	"context"
	"net"
	"sort"
	"strings"
	"sync"
	"time"
)

// ---------------------------------------------------------------------------
// Connected-device tracker
//
// The App's home tab is driven by this: who is connected, how much they have
// transferred, and what they are allowed to do. Devices are keyed internally by
// IP (the only identity an inbound request carries) and enriched with a MAC from
// the neighbour table; the MAC becomes the public id because it is stable across
// DHCP lease changes.
// ---------------------------------------------------------------------------

// OnlineWindow is how long after the last request a device still counts as online.
const OnlineWindow = 90 * time.Second

// maxTrackedDevices caps memory on a busy network; the least recently seen
// offline devices are evicted first.
const maxTrackedDevices = 300

// DeviceView is the API projection of a tracked device.
type DeviceView struct {
	ID           string            `json:"id"`
	IP           string            `json:"ip"`
	MAC          string            `json:"mac"`
	Hostname     string            `json:"hostname"`
	Kind         string            `json:"kind"`
	Policy       string            `json:"policy"`
	PolicySource string            `json:"policy_source"`
	Online       bool              `json:"online"`
	ActiveConns  int               `json:"active_conns"`
	Requests     int64             `json:"requests"`
	BytesIn      int64             `json:"bytes_in"`
	BytesOut     int64             `json:"bytes_out"`
	Denied       int64             `json:"denied"`
	FirstSeen    string            `json:"first_seen"`
	LastSeen     string            `json:"last_seen"`
	UserAgent    string            `json:"user_agent"`
	CurrentPath  string            `json:"current_path"`
	LastMethod   string            `json:"last_method"`
	LastStatus   int               `json:"last_status"`
	Rules        map[string]Policy `json:"rules,omitempty"`
}

type device struct {
	ip          string
	mac         string
	hostname    string
	kind        string
	activeConns int
	requests    int64
	bytesIn     int64
	bytesOut    int64
	denied      int64
	firstSeen   time.Time
	lastSeen    time.Time
	userAgent   string
	currentPath string
	lastMethod  string
	lastStatus  int
	dnsTriedAt  time.Time
}

// Tracker owns all device state.
type Tracker struct {
	mu        sync.Mutex
	devs      map[string]*device
	arp       *arpCache
	acl       *ACL
	cfg       *ConfigStore
	log       *Logger
	notify    chan struct{}
	dnsActive map[string]bool
}

// NewTracker builds a tracker.
func NewTracker(acl *ACL, cfg *ConfigStore, log *Logger) *Tracker {
	return &Tracker{
		devs:      map[string]*device{},
		arp:       newARPCache(15 * time.Second),
		acl:       acl,
		cfg:       cfg,
		log:       log,
		notify:    make(chan struct{}, 1),
		dnsActive: map[string]bool{},
	}
}

// Changes returns a channel that receives a value whenever the device list
// structurally changes (new device, policy change). Used by `subscribe`.
func (t *Tracker) Changes() <-chan struct{} { return t.notify }

func (t *Tracker) signal() {
	select {
	case t.notify <- struct{}{}:
	default:
	}
}

// deviceID is the public identity of a device.
func deviceID(ip, mac string) string {
	if mac != "" {
		return mac
	}
	return ip
}

// observe fetches (creating if needed) the device record for ip.
// Caller must hold t.mu.
func (t *Tracker) observeLocked(ip, kind string) *device {
	d, ok := t.devs[ip]
	if !ok {
		now := time.Now()
		d = &device{ip: ip, kind: kind, firstSeen: now, lastSeen: now}
		t.devs[ip] = d
		t.evictLocked()
		t.signal() // non-blocking: structural change
	}
	if kind != "" {
		d.kind = kind
	}
	if d.mac == "" {
		if mac := t.arp.LookupMAC(ip); mac != "" {
			d.mac = mac
		}
	}
	d.lastSeen = time.Now()
	return d
}

// evictLocked trims the oldest offline devices when the map grows too large.
// Caller must hold t.mu.
func (t *Tracker) evictLocked() {
	if len(t.devs) <= maxTrackedDevices {
		return
	}
	type pair struct {
		ip string
		at time.Time
	}
	offline := make([]pair, 0, len(t.devs))
	now := time.Now()
	for ip, d := range t.devs {
		if d.activeConns == 0 && now.Sub(d.lastSeen) > OnlineWindow {
			offline = append(offline, pair{ip, d.lastSeen})
		}
	}
	sort.Slice(offline, func(i, j int) bool { return offline[i].at.Before(offline[j].at) })
	for i := 0; i < len(offline) && len(t.devs) > maxTrackedDevices; i++ {
		delete(t.devs, offline[i].ip)
	}
}

// OpenConn records a new TCP connection from ip.
func (t *Tracker) OpenConn(ip, kind string) {
	t.mu.Lock()
	d := t.observeLocked(ip, kind)
	d.activeConns++
	t.mu.Unlock()
	t.maybeResolveName(ip)
}

// CloseConn records a closed TCP connection from ip.
func (t *Tracker) CloseConn(ip string) {
	t.mu.Lock()
	if d, ok := t.devs[ip]; ok {
		if d.activeConns > 0 {
			d.activeConns--
		}
		d.lastSeen = time.Now()
	}
	t.mu.Unlock()
}

// Touch marks activity without a full request record (used by the health loop).
func (t *Tracker) Touch(ip, kind string) {
	t.mu.Lock()
	t.observeLocked(ip, kind)
	t.mu.Unlock()
}

// Record updates per-device statistics after a request completes.
func (t *Tracker) Record(ip, kind, method, path, userAgent string, status int, bytesIn, bytesOut int64, allowed bool) {
	t.mu.Lock()
	d := t.observeLocked(ip, kind)
	d.requests++
	d.bytesIn += bytesIn
	d.bytesOut += bytesOut
	d.lastMethod = method
	d.currentPath = path
	d.lastStatus = status
	if userAgent != "" {
		d.userAgent = userAgent
	}
	if !allowed {
		d.denied++
	}
	t.mu.Unlock()
	t.maybeResolveName(ip)
}

// maybeResolveName performs a one-shot best-effort reverse DNS lookup.
//
// It is deliberately asynchronous and rate limited: on a LAN without a
// registered reverse zone it simply returns nothing, and a blocking lookup in
// the request path would stall every request.
func (t *Tracker) maybeResolveName(ip string) {
	if ip == "" || ip == "127.0.0.1" || strings.HasPrefix(ip, "127.") {
		return
	}
	t.mu.Lock()
	d, ok := t.devs[ip]
	if !ok || d.hostname != "" {
		t.mu.Unlock()
		return
	}
	if t.dnsActive[ip] || time.Since(d.dnsTriedAt) < 5*time.Minute {
		t.mu.Unlock()
		return
	}
	t.dnsActive[ip] = true
	d.dnsTriedAt = time.Now()
	t.mu.Unlock()

	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		var name string
		if names, err := net.DefaultResolver.LookupAddr(ctx, ip); err == nil && len(names) > 0 {
			name = strings.TrimSuffix(names[0], ".")
		}
		t.mu.Lock()
		if d, ok := t.devs[ip]; ok {
			d.hostname = name
		}
		delete(t.dnsActive, ip)
		t.mu.Unlock()
		t.signal()
	}()
}

// List returns the current device views, online first then most recently seen.
func (t *Tracker) List() []DeviceView {
	cfg := t.cfg.Get()

	t.mu.Lock()
	type row struct {
		view  DeviceView
		order int
	}
	rows := make([]row, 0, len(t.devs))
	now := time.Now()
	for _, d := range t.devs {
		policy, source := t.acl.Resolve(d.ip, d.mac, Policy(cfg.DefaultPolicy))
		if cfg.ReadonlyGlobal && policy == PolicyReadWrite {
			policy = PolicyReadOnly
			source = "global"
		}
		online := d.activeConns > 0 || now.Sub(d.lastSeen) < OnlineWindow
		v := DeviceView{
			ID:           deviceID(d.ip, d.mac),
			IP:           d.ip,
			MAC:          d.mac,
			Hostname:     d.hostname,
			Kind:         d.kind,
			Policy:       string(policy),
			PolicySource: source,
			Online:       online,
			ActiveConns:  d.activeConns,
			Requests:     d.requests,
			BytesIn:      d.bytesIn,
			BytesOut:     d.bytesOut,
			Denied:       d.denied,
			FirstSeen:    d.firstSeen.Format(time.RFC3339),
			LastSeen:     d.lastSeen.Format(time.RFC3339),
			UserAgent:    d.userAgent,
			CurrentPath:  d.currentPath,
			LastMethod:   d.lastMethod,
			LastStatus:   d.lastStatus,
			Rules:        t.acl.Rules(d.ip, d.mac),
		}
		order := 0
		if !online {
			order = 1
		}
		rows = append(rows, row{view: v, order: order})
	}
	t.mu.Unlock()

	sort.SliceStable(rows, func(i, j int) bool {
		if rows[i].order != rows[j].order {
			return rows[i].order < rows[j].order
		}
		return rows[i].view.LastSeen > rows[j].view.LastSeen
	})

	out := make([]DeviceView, 0, len(rows))
	for _, r := range rows {
		out = append(out, r.view)
	}
	return out
}

// Find resolves a device by its public id (MAC or IP) or by raw IP.
func (t *Tracker) Find(id string) (ip, mac string, ok bool) {
	t.mu.Lock()
	defer t.mu.Unlock()
	for _, d := range t.devs {
		if d.ip == id || (d.mac != "" && strings.EqualFold(d.mac, id)) {
			return d.ip, d.mac, true
		}
	}
	// A device that has never made a request can still be given a rule by IP.
	if net.ParseIP(id) != nil {
		return id, "", true
	}
	return "", "", false
}

// Forget removes a device from the list and its statistics. Explicit ACL rules
// are kept: forgetting a device must not silently grant it the default policy.
func (t *Tracker) Forget(id string) bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	for ip, d := range t.devs {
		if d.ip == id || (d.mac != "" && strings.EqualFold(d.mac, id)) {
			delete(t.devs, ip)
			t.signal()
			return true
		}
	}
	return false
}

// MAC returns the known MAC for an IP, consulting the neighbour table on a miss.
//
// The proxy needs this before it can evaluate a policy, because rules keyed by
// MAC must win over rules keyed by IP.
func (t *Tracker) MAC(ip string) string {
	t.mu.Lock()
	if d, ok := t.devs[ip]; ok && d.mac != "" {
		mac := d.mac
		t.mu.Unlock()
		return mac
	}
	t.mu.Unlock()
	return t.arp.LookupMAC(ip)
}

// Totals aggregates statistics across all tracked devices.
type Totals struct {
	Devices     int   `json:"devices"`
	Online      int   `json:"online"`
	ActiveConns int   `json:"active_conns"`
	Requests    int64 `json:"requests"`
	Denied      int64 `json:"denied"`
	BytesIn     int64 `json:"bytes_in"`
	BytesOut    int64 `json:"bytes_out"`
}

// Totals computes aggregate statistics.
func (t *Tracker) Totals() Totals {
	t.mu.Lock()
	defer t.mu.Unlock()
	var tot Totals
	now := time.Now()
	for _, d := range t.devs {
		tot.Devices++
		tot.ActiveConns += d.activeConns
		tot.Requests += d.requests
		tot.Denied += d.denied
		tot.BytesIn += d.bytesIn
		tot.BytesOut += d.bytesOut
		if d.activeConns > 0 || now.Sub(d.lastSeen) < OnlineWindow {
			tot.Online++
		}
	}
	return tot
}

// Snapshot returns the device list and totals together, for the subscribe stream.
func (t *Tracker) Snapshot() ([]DeviceView, Totals) {
	return t.List(), t.Totals()
}
