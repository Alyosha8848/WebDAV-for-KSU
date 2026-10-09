package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"
)

// ---------------------------------------------------------------------------
// Control plane
//
// The App never talks HTTP to the daemon: it runs `dufsboxd ctl <verb>` through
// a root shell. That keeps the control channel off the network entirely and
// sidesteps Android's SELinux rules for untrusted_app -> root TCP sockets.
//
// The transport is a JSON POST over a 0600 unix socket owned by root, so only
// root (and therefore KernelSU's `su`) can drive the daemon.
// ---------------------------------------------------------------------------

// RPCRequest is one control request.
type RPCRequest struct {
	Verb string          `json:"verb"`
	Args json.RawMessage `json:"args,omitempty"`
}

func rpcOK(data any) map[string]any { return map[string]any{"ok": true, "data": data} }
func rpcErr(msg string) map[string]any {
	return map[string]any{"ok": false, "error": msg}
}

// Control serves the RPC API.
type Control struct {
	paths Paths
	cfg   *ConfigStore
	acl   *ACL
	dev   *Tracker
	sup   *Supervisor
	log   *Logger

	srv  *http.Server
	ln   net.Listener
	addr string
}

// StartControl binds the control socket and serves the API.
func StartControl(paths Paths, cfg *ConfigStore, acl *ACL, dev *Tracker, sup *Supervisor, log *Logger) (*Control, error) {
	c := &Control{paths: paths, cfg: cfg, acl: acl, dev: dev, sup: sup, log: log}

	mux := http.NewServeMux()
	mux.HandleFunc("/rpc", c.handleRPC)
	mux.HandleFunc("/ping", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"ok":true}`+"\n")
	})

	// A dev/test escape hatch: on a Windows host there is no root and no
	// meaningful socket permissions, so the harness can pin the control plane to
	// a loopback TCP port instead.
	var ln net.Listener
	var err error
	if tcp := os.Getenv("DUFSBOX_CONTROL_TCP"); tcp != "" {
		ln, err = net.Listen("tcp", tcp)
		if err != nil {
			return nil, fmt.Errorf("控制端口监听失败: %w", err)
		}
	} else {
		if err := os.MkdirAll(filepath.Dir(paths.SockPath), 0o700); err != nil {
			return nil, err
		}
		// A socket left behind by a crash makes Listen fail, so clear it first.
		_ = os.Remove(paths.SockPath)
		ln, err = net.Listen("unix", paths.SockPath)
		if err != nil {
			return nil, fmt.Errorf("控制套接字监听失败: %w", err)
		}
		_ = os.Chmod(paths.SockPath, 0o600)
	}
	c.ln = ln
	c.addr = ln.Addr().String()
	c.srv = &http.Server{
		Handler:           mux,
		ReadHeaderTimeout: 10 * time.Second,
		ErrorLog:          nil,
	}

	if err := os.WriteFile(paths.ControlCacheFile, []byte(c.addr+"\n"), 0o600); err != nil {
		log.Debugf("control", "写入控制地址缓存失败: %v", err)
	}

	go func() {
		if err := c.srv.Serve(ln); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Errorf("control", "控制服务结束: %v", err)
		}
	}()
	log.Infof("control", "控制接口已就绪 (%s)", c.addr)
	return c, nil
}

// Close stops the control server and removes the socket.
func (c *Control) Close() {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	_ = c.srv.Shutdown(ctx)
	if os.Getenv("DUFSBOX_CONTROL_TCP") == "" {
		_ = os.Remove(c.paths.SockPath)
	}
}

// handleRPC dispatches one request.
func (c *Control) handleRPC(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "POST only", http.StatusMethodNotAllowed)
		return
	}
	raw, err := io.ReadAll(io.LimitReader(r.Body, 1<<20))
	if err != nil {
		writeRPC(w, rpcErr("读取请求失败: "+err.Error()), true)
		return
	}
	var req RPCRequest
	if err := json.Unmarshal(raw, &req); err != nil {
		writeRPC(w, rpcErr("请求不是合法 JSON: "+err.Error()), true)
		return
	}

	if req.Verb == "subscribe" {
		c.handleSubscribe(w, r)
		return
	}

	resp, failed := c.dispatch(req)
	writeRPC(w, resp, failed)
}

func writeRPC(w http.ResponseWriter, payload map[string]any, failed bool) {
	buf, err := json.Marshal(payload)
	if err != nil {
		buf = []byte(`{"ok":false,"error":"响应序列化失败"}`)
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("X-DufsBox-Ok", strconv.FormatBool(!failed))
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(append(buf, '\n'))
}

// dispatch runs one verb and reports whether it failed.
func (c *Control) dispatch(req RPCRequest) (map[string]any, bool) {
	args := req.Args
	if len(args) == 0 {
		args = json.RawMessage("{}")
	}

	switch req.Verb {
	case "status":
		return rpcOK(c.sup.Status()), false

	case "devices":
		return rpcOK(map[string]any{"devices": c.dev.List()}), false

	case "acl.set":
		return c.rpcACLSet(args)

	case "acl.remove":
		return c.rpcACLRemove(args)

	case "device.forget":
		var a struct {
			ID string `json:"id"`
		}
		if err := json.Unmarshal(args, &a); err != nil || a.ID == "" {
			return rpcErr("缺少设备 id"), true
		}
		if !c.dev.Forget(a.ID) {
			return rpcErr("未找到该设备"), true
		}
		c.log.Infof("acl", "已忘记设备 %s", a.ID)
		return rpcOK(map[string]any{"devices": c.dev.List()}), false

	case "service.set":
		var a struct {
			Action string `json:"action"`
		}
		if err := json.Unmarshal(args, &a); err != nil {
			return rpcErr("参数无效"), true
		}
		var err error
		switch a.Action {
		case "start":
			err = c.sup.Start()
		case "stop":
			err = c.sup.Stop()
		case "restart":
			err = c.sup.Restart()
		default:
			return rpcErr("未知动作: " + a.Action), true
		}
		if err != nil {
			return rpcErr(err.Error()), true
		}
		c.log.Infof("service", "服务动作: %s", a.Action)
		return rpcOK(map[string]any{"state": c.sup.Status().Service.State}), false

	case "config.get":
		return rpcOK(map[string]any{"config": c.cfg.Get().View()}), false

	case "config.set":
		var patch map[string]json.RawMessage
		if err := json.Unmarshal(args, &patch); err != nil {
			return rpcErr("参数无效: " + err.Error()), true
		}
		// Validate the credential before it is persisted: dufs refuses to start
		// with an unparseable --auth rule.
		if raw, ok := patch["password"]; ok {
			var pw string
			if json.Unmarshal(raw, &pw) == nil && pw != "" {
				var un string
				if rawUser, ok := patch["username"]; ok {
					_ = json.Unmarshal(rawUser, &un)
				}
				if un == "" {
					un = c.cfg.Get().Username
				}
				err := Config{AuthMode: "password", Username: un, Password: pw}.AuthRuleError()
				if err != nil {
					return rpcErr(err.Error()), true
				}
			}
		}
		updated, restartRequired, err := c.cfg.ApplyPatch(patch)
		if err != nil {
			return rpcErr("保存配置失败: " + err.Error()), true
		}
		c.log.SetLevel(ParseLevel(updated.LogLevel))
		if _, ok := patch["log_level"]; ok {
			c.log.Infof("config", "日志级别已切换为 %s", updated.LogLevel)
		}
		if restartRequired {
			if err := c.sup.Restart(); err != nil {
				return rpcErr("配置已保存，但重启服务失败: " + err.Error()), true
			}
			c.log.Infof("config", "配置已更新并重启服务")
		}
		return rpcOK(map[string]any{"config": updated.View(), "restart_required": restartRequired}), false

	case "logs":
		var a struct {
			Level string          `json:"level"`
			Limit int             `json:"limit"`
			Since json.RawMessage `json:"since"`
		}
		_ = json.Unmarshal(args, &a)
		if a.Limit <= 0 || a.Limit > 2000 {
			a.Limit = 200
		}
		since := parseSince(a.Since)
		entries := c.log.Entries(ParseLevel(a.Level), a.Limit, since)
		return rpcOK(map[string]any{"entries": entries}), false

	case "browse":
		var a struct {
			Path string `json:"path"`
		}
		_ = json.Unmarshal(args, &a)
		res, err := browsePath(a.Path)
		if err != nil {
			return rpcErr(err.Error()), true
		}
		return rpcOK(res), false

	case "tailscale.set":
		var a struct {
			Action string `json:"action"`
		}
		if err := json.Unmarshal(args, &a); err != nil {
			return rpcErr("参数无效"), true
		}
		data, err := c.sup.TailscaleAction(a.Action)
		if err != nil {
			return rpcErr(err.Error()), true
		}
		return rpcOK(data), false

	case "version":
		return rpcOK(map[string]any{
			"version":  DaemonVersion,
			"dufs":     c.sup.dufsVersion(),
			"go":       runtime.Version(),
			"protocol": ProtocolVersion,
		}), false
	}
	return rpcErr("未知命令: " + req.Verb), true
}

// parseSince accepts either a unix-millisecond number or an RFC3339 string.
func parseSince(raw json.RawMessage) time.Time {
	s := strings.TrimSpace(string(raw))
	if s == "" || s == "null" || s == "0" {
		return time.Time{}
	}
	if n, err := strconv.ParseInt(s, 10, 64); err == nil {
		if n <= 0 {
			return time.Time{}
		}
		return time.UnixMilli(n)
	}
	var str string
	if json.Unmarshal(raw, &str) == nil && str != "" {
		if ts, err := time.Parse(time.RFC3339, str); err == nil {
			return ts
		}
		if ts, err := time.Parse(msTime, str); err == nil {
			return ts
		}
	}
	return time.Time{}
}

func (c *Control) rpcACLSet(args json.RawMessage) (map[string]any, bool) {
	var a struct {
		ID     string `json:"id"`
		Policy string `json:"policy"`
		Scope  string `json:"scope"`
		CIDR   string `json:"cidr"`
	}
	if err := json.Unmarshal(args, &a); err != nil {
		return rpcErr("参数无效: " + err.Error()), true
	}
	if !ValidPolicyName(a.Policy) {
		return rpcErr("未知权限: " + a.Policy), true
	}
	key, err := c.aclKeyFor(a.ID, a.Scope, a.CIDR)
	if err != nil {
		return rpcErr(err.Error()), true
	}
	if err := c.acl.Set(key, Policy(a.Policy)); err != nil {
		return rpcErr("保存规则失败: " + err.Error()), true
	}
	if a.Policy == string(PolicyInherit) {
		c.log.Infof("acl", "已清除规则 %s（回到默认权限）", key)
	} else {
		c.log.Infof("acl", "已设置规则 %s = %s", key, a.Policy)
	}
	return rpcOK(map[string]any{"key": key, "devices": c.dev.List()}), false
}

func (c *Control) rpcACLRemove(args json.RawMessage) (map[string]any, bool) {
	var a struct {
		ID    string `json:"id"`
		Key   string `json:"key"`
		Scope string `json:"scope"`
	}
	if err := json.Unmarshal(args, &a); err != nil {
		return rpcErr("参数无效: " + err.Error()), true
	}
	key := a.Key
	if key == "" {
		var err error
		key, err = c.aclKeyFor(a.ID, a.Scope, "")
		if err != nil {
			return rpcErr(err.Error()), true
		}
	}
	existed, err := c.acl.Remove(key)
	if err != nil {
		return rpcErr("删除规则失败: " + err.Error()), true
	}
	return rpcOK(map[string]any{"removed": existed, "devices": c.dev.List()}), false
}

// aclKeyFor picks the most specific stable identity for a rule.
//
// Preference order: an explicit CIDR, then the device's MAC (stable across DHCP
// lease changes), then its IP, then a MAC-shaped or CIDR-shaped literal id.
func (c *Control) aclKeyFor(id, scope, cidr string) (string, error) {
	if cidr != "" {
		if _, _, err := net.ParseCIDR(cidr); err != nil {
			return "", fmt.Errorf("无效的网段: %s", cidr)
		}
		return cidrRuleKey(cidr), nil
	}
	if id == "" {
		return "", errors.New("缺少设备 id")
	}
	ip, mac, found := c.dev.Find(id)

	switch scope {
	case "cidr":
		if _, _, err := net.ParseCIDR(id); err != nil {
			return "", fmt.Errorf("无效的网段: %s", id)
		}
		return cidrRuleKey(id), nil
	case "ip":
		if found && ip != "" {
			return ipRuleKey(ip), nil
		}
		if net.ParseIP(id) != nil {
			return ipRuleKey(id), nil
		}
		return "", fmt.Errorf("无效的 IP: %s", id)
	}

	if found && mac != "" {
		return macRuleKey(mac), nil
	}
	if net.ParseIP(id) != nil {
		return ipRuleKey(id), nil
	}
	if strings.Contains(id, "/") {
		if _, _, err := net.ParseCIDR(id); err != nil {
			return "", fmt.Errorf("无效的网段: %s", id)
		}
		return cidrRuleKey(id), nil
	}
	if looksLikeMAC(id) {
		return macRuleKey(id), nil
	}
	if found {
		return ipRuleKey(ip), nil
	}
	return "", fmt.Errorf("无法识别的设备: %s", id)
}

func looksLikeMAC(s string) bool {
	parts := strings.Split(s, ":")
	if len(parts) != 6 {
		return false
	}
	for _, p := range parts {
		if len(p) != 2 {
			return false
		}
		if _, err := strconv.ParseUint(p, 16, 8); err != nil {
			return false
		}
	}
	return true
}

// ---------------------------------------------------------------------------
// subscribe: NDJSON stream of state changes
// ---------------------------------------------------------------------------

type ndjsonEvent struct {
	Type string `json:"type"`
	Data any    `json:"data"`
}

func (c *Control) handleSubscribe(w http.ResponseWriter, r *http.Request) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		writeRPC(w, rpcErr("当前连接不支持流式输出"), true)
		return
	}
	w.Header().Set("Content-Type", "application/x-ndjson")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusOK)

	enc := json.NewEncoder(w)
	send := func(ev ndjsonEvent) bool {
		if err := enc.Encode(ev); err != nil {
			return false
		}
		flusher.Flush()
		return true
	}

	logCh, unsub := c.log.Subscribe()
	defer unsub()

	status := c.sup.Status()
	devices := c.dev.List()
	if !send(ndjsonEvent{Type: "snapshot", Data: map[string]any{
		"status":  status,
		"devices": devices,
		"logs":    c.log.Entries(LevelTrace, 200, time.Time{}),
	}}) {
		return
	}

	lastDevices := mustJSON(devices)
	tick := time.NewTicker(time.Second)
	defer tick.Stop()
	statusEvery := 0

	for {
		select {
		case <-r.Context().Done():
			return
		case entry, ok := <-logCh:
			if !ok {
				return
			}
			if !send(ndjsonEvent{Type: "log", Data: entry}) {
				return
			}
		case <-tick.C:
			devices := c.dev.List()
			encoded := mustJSON(devices)
			if encoded != lastDevices {
				lastDevices = encoded
				if !send(ndjsonEvent{Type: "devices", Data: map[string]any{"devices": devices}}) {
					return
				}
			}
			statusEvery++
			if statusEvery >= 5 {
				statusEvery = 0
				if !send(ndjsonEvent{Type: "status", Data: c.sup.Status()}) {
					return
				}
			}
		}
	}
}

func mustJSON(v any) string {
	buf, err := json.Marshal(v)
	if err != nil {
		return ""
	}
	return string(buf)
}

// ---------------------------------------------------------------------------
// browse: root-side directory picker for the App
// ---------------------------------------------------------------------------

// BrowseEntry is one entry in a directory listing.
type BrowseEntry struct {
	Name     string `json:"name"`
	Path     string `json:"path"`
	IsDir    bool   `json:"is_dir"`
	Size     int64  `json:"size"`
	Readable bool   `json:"readable"`
}

// BrowseResult is a single directory listing.
type BrowseResult struct {
	Path    string        `json:"path"`
	Parent  string        `json:"parent"`
	Exists  bool          `json:"exists"`
	Entries []BrowseEntry `json:"entries"`
}

const maxBrowseEntries = 2000

// browsePath lists a directory. The daemon runs as root, so this is how the App
// can offer an arbitrary share path instead of only /sdcard.
func browsePath(pathArg string) (BrowseResult, error) {
	if strings.TrimSpace(pathArg) == "" {
		pathArg = "/"
	}
	if !isAbsPath(pathArg) {
		return BrowseResult{}, fmt.Errorf("必须是绝对路径: %s", pathArg)
	}
	clean := cleanPath(pathArg)

	res := BrowseResult{Path: clean, Parent: ""}
	if clean != "/" {
		parent := dirPath(clean)
		if parent != clean {
			res.Parent = parent
		}
	}
	st, err := os.Stat(clean)
	if err != nil {
		res.Exists = false
		return res, nil
	}
	if !st.IsDir() {
		return BrowseResult{}, fmt.Errorf("不是目录: %s", clean)
	}
	res.Exists = true

	entries, err := os.ReadDir(clean)
	if err != nil {
		return BrowseResult{}, fmt.Errorf("无法读取目录: %v", err)
	}
	dirs := make([]BrowseEntry, 0, len(entries))
	files := make([]BrowseEntry, 0, len(entries))
	for _, e := range entries {
		if len(dirs)+len(files) >= maxBrowseEntries {
			break
		}
		full := joinPath(clean, e.Name())
		item := BrowseEntry{Name: e.Name(), Path: full, IsDir: e.IsDir(), Readable: true}
		if info, err := e.Info(); err == nil {
			item.Size = info.Size()
		} else {
			item.Readable = false
		}
		if item.IsDir {
			dirs = append(dirs, item)
		} else {
			files = append(files, item)
		}
	}
	sortBrowse(dirs)
	sortBrowse(files)
	res.Entries = append(dirs, files...)
	return res, nil
}

func sortBrowse(items []BrowseEntry) {
	// Case-insensitive name order: what a file manager shows.
	for i := 1; i < len(items); i++ {
		for j := i; j > 0 && strings.ToLower(items[j].Name) < strings.ToLower(items[j-1].Name); j-- {
			items[j], items[j-1] = items[j-1], items[j]
		}
	}
}

// ---------------------------------------------------------------------------
// CLI client
// ---------------------------------------------------------------------------

// controlHTTPClient builds a client that reaches the control plane.
func controlHTTPClient() *http.Client {
	if tcp := os.Getenv("DUFSBOX_CONTROL_TCP"); tcp != "" {
		return &http.Client{Timeout: 30 * time.Second}
	}
	sock := DefaultPaths().SockPath
	return &http.Client{
		Timeout: 30 * time.Second,
		Transport: &http.Transport{
			DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
				var d net.Dialer
				return d.DialContext(ctx, "unix", sock)
			},
		},
	}
}

func controlEndpoint() string {
	if tcp := os.Getenv("DUFSBOX_CONTROL_TCP"); tcp != "" {
		return "http://" + tcp + "/rpc"
	}
	return "http://unix/rpc"
}

// runCtl implements `dufsboxd ctl <verb> [--json <payload> | --json-stdin]`.
//
// It always prints exactly one JSON object to stdout so the App can parse the
// result without special-casing transport failures.
//
// --json-stdin exists because embedding JSON in a command line is fragile: POSIX
// shells need single quotes, and Windows PowerShell strips embedded double quotes
// outright, which silently turns a valid payload into invalid JSON.
func runCtl(argv []string) int {
	if len(argv) == 0 {
		printJSON(rpcErr("用法: dufsboxd ctl <verb> [--json <payload> | --json-stdin]"))
		return 2
	}
	verb := argv[0]
	payload := json.RawMessage("{}")
	if len(argv) >= 2 {
		switch {
		case argv[1] == "--json" && len(argv) >= 3:
			payload = json.RawMessage(argv[2])
		case argv[1] == "--json-stdin" || argv[1] == "-":
			raw, err := io.ReadAll(io.LimitReader(os.Stdin, 1<<20))
			if err != nil {
				printJSON(rpcErr("读取标准输入失败: " + err.Error()))
				return 2
			}
			raw = bytes.TrimSpace(raw)
			if len(raw) == 0 {
				raw = []byte("{}")
			}
			payload = json.RawMessage(raw)
		default:
			payload = json.RawMessage(argv[1])
		}
	}
	if !json.Valid(payload) {
		printJSON(rpcErr("--json 的内容不是合法 JSON"))
		return 2
	}

	body, err := json.Marshal(RPCRequest{Verb: verb, Args: payload})
	if err != nil {
		printJSON(rpcErr(err.Error()))
		return 2
	}

	client := controlHTTPClient()
	resp, err := client.Post(controlEndpoint(), "application/json", bytes.NewReader(body))
	if err != nil {
		printJSON(rpcErr("无法连接 dufsboxd: " + err.Error()))
		return 1
	}
	defer resp.Body.Close()

	if verb == "subscribe" {
		_, err := io.Copy(os.Stdout, resp.Body)
		if err != nil {
			return 1
		}
		return 0
	}

	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		printJSON(rpcErr("读取响应失败: " + err.Error()))
		return 1
	}
	os.Stdout.Write(raw)
	if !bytes.HasSuffix(raw, []byte("\n")) {
		os.Stdout.Write([]byte("\n"))
	}

	var parsed struct {
		OK bool `json:"ok"`
	}
	if json.Unmarshal(raw, &parsed) != nil || !parsed.OK {
		return 1
	}
	return 0
}

func printJSON(v any) {
	buf, err := json.Marshal(v)
	if err != nil {
		buf = []byte(`{"ok":false,"error":"内部序列化错误"}`)
	}
	os.Stdout.Write(append(buf, '\n'))
}

// daemonReachable reports whether a daemon is already serving the control socket.
func daemonReachable() bool {
	client := controlHTTPClient()
	client.Timeout = 3 * time.Second
	resp, err := client.Get(strings.TrimSuffix(controlEndpoint(), "/rpc") + "/ping")
	if err != nil {
		return false
	}
	defer resp.Body.Close()
	return resp.StatusCode == http.StatusOK
}
