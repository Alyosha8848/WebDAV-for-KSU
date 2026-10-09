package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"
)

// ---------------------------------------------------------------------------
// ACL-enforcing reverse proxy
//
// Layout: the public listener terminates here, the request is attributed to a
// device, the device's policy is evaluated, and only then is the request handed
// to dufs on 127.0.0.1. dufs itself always runs with --allow-all, because a
// dufs-level restriction would be global; per-device read-only has to be decided
// before the request reaches it.
// ---------------------------------------------------------------------------

// countingWriter records the response size and status while staying transparent
// enough for streaming (dufs uses Server-Sent Events for its file watcher, so
// Flush must be forwarded).
type countingWriter struct {
	http.ResponseWriter
	status  int
	written int64
}

func (w *countingWriter) WriteHeader(code int) {
	if w.status == 0 {
		w.status = code
	}
	w.ResponseWriter.WriteHeader(code)
}

func (w *countingWriter) Write(p []byte) (int, error) {
	if w.status == 0 {
		w.status = http.StatusOK
	}
	n, err := w.ResponseWriter.Write(p)
	w.written += int64(n)
	return n, err
}

// Flush forwards to the underlying writer so SSE responses are not buffered.
func (w *countingWriter) Flush() {
	if f, ok := w.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

// Unwrap lets http.ResponseController reach the real writer (Go 1.20+).
func (w *countingWriter) Unwrap() http.ResponseWriter { return w.ResponseWriter }

// countingReader counts request body bytes (uploads).
type countingReader struct {
	r io.ReadCloser
	n int64
}

func (c *countingReader) Read(p []byte) (int, error) {
	n, err := c.r.Read(p)
	c.n += int64(n)
	return n, err
}

func (c *countingReader) Close() error { return c.r.Close() }

// Proxy is the public HTTP handler.
type Proxy struct {
	cfg    *ConfigStore
	acl    *ACL
	dev    *Tracker
	log    *Logger
	rp     *httputil.ReverseProxy
	target *url.URL
}

// NewProxy builds the reverse proxy towards dufs on the given loopback port.
func NewProxy(cfg *ConfigStore, acl *ACL, dev *Tracker, log *Logger, internalPort int) (*Proxy, error) {
	target, err := url.Parse(fmt.Sprintf("http://127.0.0.1:%d", internalPort))
	if err != nil {
		return nil, err
	}
	p := &Proxy{cfg: cfg, acl: acl, dev: dev, log: log, target: target}

	transport := &http.Transport{
		Proxy:                 nil, // never route the loopback hop through a proxy
		MaxIdleConns:          64,
		MaxIdleConnsPerHost:   32,
		IdleConnTimeout:       90 * time.Second,
		ExpectContinueTimeout: 2 * time.Second,
		// No ResponseHeaderTimeout: dufs can take a while to start answering a
		// large archive request, and killing it would break big downloads.
		DisableCompression: true,
	}

	p.rp = &httputil.ReverseProxy{
		Transport: transport,
		Rewrite: func(pr *httputil.ProxyRequest) {
			// Rewrite the URL without touching the Host header, then restore it so
			// dufs sees the address the client actually used.
			pr.SetURL(target)
			pr.Out.Host = pr.In.Host
			pr.SetXForwarded()
		},
		// Flush immediately: dufs streams SSE for its file watcher and large
		// downloads should not be buffered in the proxy.
		FlushInterval: -1,
		ErrorHandler: func(w http.ResponseWriter, r *http.Request, err error) {
			ip := RemoteIP(r.RemoteAddr)
			if errors.Is(err, context.Canceled) {
				p.log.Debugf("proxy", "%s %s from %s cancelled", r.Method, r.URL.Path, ip)
				return
			}
			p.log.Errorf("proxy", "%s %s from %s failed: %v", r.Method, r.URL.Path, ip, err)
			w.Header().Set("Content-Type", "text/plain; charset=utf-8")
			w.WriteHeader(http.StatusBadGateway)
			_, _ = io.WriteString(w, "DufsBox: 后端 dufs 未响应，请检查服务状态。\n")
		},
		ModifyResponse: func(resp *http.Response) error {
			resp.Header.Set("Server", "DufsBox")
			return nil
		},
	}
	return p, nil
}

// kindFor classifies a request source.
//
// In tailnet mode tailscaled's userspace netstack dials 127.0.0.1, so a loopback
// peer is the tailnet; everything else is a real LAN address.
func (p *Proxy) kindFor(ip string) string {
	if ip == "127.0.0.1" || ip == "::1" || strings.HasPrefix(ip, "127.") {
		if p.cfg.Get().TailscaleEnabled() {
			return "tailnet"
		}
		return "lan"
	}
	return "lan"
}

// ServeHTTP applies the device policy and forwards allowed requests.
func (p *Proxy) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	cfg := p.cfg.Get()
	ip := RemoteIP(r.RemoteAddr)
	kind := p.kindFor(ip)

	mac := ""
	if kind == "lan" && ip != "127.0.0.1" {
		mac = p.dev.MAC(ip)
	}
	policy, source := p.acl.Resolve(ip, mac, Policy(cfg.DefaultPolicy))
	if cfg.ReadonlyGlobal && policy == PolicyReadWrite {
		policy = PolicyReadOnly
		source = "global"
	}

	allowed := policy.Allows(r.Method)
	userAgent := r.UserAgent()

	if !allowed {
		p.dev.Record(ip, kind, r.Method, r.URL.Path, userAgent, http.StatusForbidden, 0, 0, false)
		p.log.Warnf("acl", "拒绝 %s %s 来源 %s (策略=%s 依据=%s)", r.Method, r.URL.Path, ip, policy, source)
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		w.Header().Set("X-DufsBox-Policy", string(policy))
		w.WriteHeader(http.StatusForbidden)
		if policy == PolicyDeny {
			_, _ = io.WriteString(w, "DufsBox: 该设备已被拒绝访问。\n")
		} else {
			_, _ = io.WriteString(w, "DufsBox: 该设备为只读权限，无法执行写操作。\n")
		}
		return
	}

	p.log.Tracef("proxy", "%s %s 来源 %s 策略=%s(%s)", r.Method, r.URL.Path, ip, policy, source)

	var in *countingReader
	if r.Body != nil {
		in = &countingReader{r: r.Body}
		r.Body = in
	}
	cw := &countingWriter{ResponseWriter: w}

	start := time.Now()
	p.rp.ServeHTTP(cw, r)

	var bytesIn int64
	if in != nil {
		bytesIn = in.n
	}
	status := cw.status
	if status == 0 {
		status = http.StatusOK
	}
	p.dev.Record(ip, kind, r.Method, r.URL.Path, userAgent, status, bytesIn, cw.written, true)
	p.log.Debugf("proxy", "%s %s 来源 %s %d 上传=%d 下载=%d 耗时=%s",
		r.Method, r.URL.Path, ip, status, bytesIn, cw.written, time.Since(start).Round(time.Millisecond))
}

// ---------------------------------------------------------------------------
// Public listener
// ---------------------------------------------------------------------------

// Listener is the public-facing HTTP server (LAN and/or tailnet).
type Listener struct {
	srv  *http.Server
	ln   net.Listener
	addr string
	log  *Logger
}

// StartListener binds addr and serves the proxy handler.
func StartListener(addr string, handler http.Handler, dev *Tracker, log *Logger) (*Listener, error) {
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return nil, err
	}
	l := &Listener{ln: ln, addr: ln.Addr().String(), log: log}

	l.srv = &http.Server{
		Handler:           handler,
		ReadHeaderTimeout: 30 * time.Second,
		// Deliberately no ReadTimeout/WriteTimeout: a 20 GiB download over Wi-Fi is
		// legitimate traffic and must not be cut off mid-transfer.
		IdleTimeout: 120 * time.Second,
		ErrorLog:    nil,
		ConnState: func(c net.Conn, st http.ConnState) {
			ip := RemoteIP(c.RemoteAddr().String())
			kind := "lan"
			if ip == "127.0.0.1" || ip == "::1" {
				kind = "tailnet"
			}
			switch st {
			case http.StateNew:
				dev.OpenConn(ip, kind)
			case http.StateClosed, http.StateHijacked:
				dev.CloseConn(ip)
			}
		},
	}
	go func() {
		if err := l.srv.Serve(ln); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Errorf("proxy", "监听 %s 结束: %v", l.addr, err)
		}
	}()
	return l, nil
}

// Addr is the actual bound address (useful when the port was chosen by the OS).
func (l *Listener) Addr() string { return l.addr }

// Port is the bound TCP port.
func (l *Listener) Port() int {
	_, portStr, err := net.SplitHostPort(l.addr)
	if err != nil {
		return 0
	}
	n, _ := strconv.Atoi(portStr)
	return n
}

// Close stops the listener.
func (l *Listener) Close() error {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	return l.srv.Shutdown(ctx)
}

// bindablePort reports whether a TCP port can currently be bound. Used by the
// health section of the status reply so the App can warn about a conflict before
// the operator wonders why nothing is reachable.
func bindablePort(addr string) bool {
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return false
	}
	_ = ln.Close()
	return true
}

// healthProbe caches the filesystem probe: the App polls status every two
// seconds and writing a probe file that often would be wasteful on flash storage.
type healthProbe struct {
	mu       sync.Mutex
	at       time.Time
	exists   bool
	writable bool
}

// Probe checks that the share path exists and is writable by the daemon.
func (h *healthProbe) Probe(path string) (bool, bool) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if time.Since(h.at) < 30*time.Second {
		return h.exists, h.writable
	}
	h.at = time.Now()
	st, err := os.Stat(path)
	h.exists = err == nil && st.IsDir()
	h.writable = false
	if h.exists {
		f, err := os.CreateTemp(path, ".dufsbox-probe-*")
		if err == nil {
			h.writable = true
			name := f.Name()
			_ = f.Close()
			_ = os.Remove(name)
		}
	}
	return h.exists, h.writable
}
