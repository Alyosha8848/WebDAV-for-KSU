/*
 * DufsBox KernelSU module WebUI.
 *
 * Every action goes through the daemon's own CLI, so this page has exactly the
 * same capabilities (and the same permission model) as the Android app: the
 * manager runs ksu.exec as root, and the root-only control socket does the rest.
 *
 * The daemon is NOT required to be running: `launch` starts it on demand.
 */
(function () {
  "use strict";

  var MODDIR = "/data/adb/modules/dufsbox";
  var CLI = MODDIR + "/bin/arm64/dufsboxd";
  var REFRESH_MS = 2000;

  var busy = false;
  var current = { status: null, devices: [], logs: [] };

  var el = function (id) { return document.getElementById(id); };

  // --- KernelSU bridge ------------------------------------------------------

  function ksuExec(command) {
    return new Promise(function (resolve, reject) {
      if (typeof ksu === "undefined" || !ksu || typeof ksu.exec !== "function") {
        reject(new Error("请在 KernelSU 管理器的模块 WebUI 中打开本页面"));
        return;
      }
      var cb = "__dufsbox_cb_" + Date.now() + "_" + Math.floor(Math.random() * 1e6);
      var done = false;
      window[cb] = function (errno, stdout, stderr) {
        if (done) return;
        done = true;
        delete window[cb];
        if (errno === 0) resolve(stdout || "");
        else reject(new Error((stderr || stdout || ("命令失败，退出码 " + errno)).trim()));
      };
      try {
        ksu.exec(command, "{}", cb);
      } catch (e) {
        delete window[cb];
        reject(e);
      }
    });
  }

  // Single-quote a JSON string for /system/bin/sh. JSON.stringify uses double
  // quotes, so the only character that needs escaping is the single quote itself.
  function shQuote(str) {
    return "'" + String(str).replace(/'/g, "'\\''") + "'";
  }

  function ctl(verb, args) {
    var payload = JSON.stringify(args || {});
    return ksuExec(CLI + " ctl " + verb + " --json " + shQuote(payload)).then(function (out) {
      var text = (out || "").trim();
      if (!text) throw new Error("dufsboxd 没有返回内容");
      var parsed;
      try {
        parsed = JSON.parse(text);
      } catch (e) {
        throw new Error("无法解析守护进程响应：" + text.slice(0, 200));
      }
      if (!parsed.ok) throw new Error(parsed.error || "命令失败");
      return parsed.data || {};
    });
  }

  // Start the daemon if it is not up yet, then wait for the control socket.
  function ensureDaemon() {
    return ctl("version").catch(function () {
      return ksuExec(CLI + " launch").then(function () {
        return new Promise(function (resolve, reject) {
          var tries = 0;
          (function poll() {
            ctl("version").then(resolve, function (err) {
              if (++tries >= 20) { reject(err); return; }
              setTimeout(poll, 400);
            });
          })();
        });
      });
    });
  }

  function toast(msg) {
    try {
      if (typeof ksu !== "undefined" && ksu && typeof ksu.toast === "function") ksu.toast(String(msg));
    } catch (e) { /* ignore */ }
  }

  // --- rendering -----------------------------------------------------------

  function fmtBytes(n) {
    n = Number(n) || 0;
    if (n < 1024) return n + " B";
    var units = ["KB", "MB", "GB", "TB"];
    var i = -1;
    do { n /= 1024; i++; } while (n >= 1024 && i < units.length - 1);
    return n.toFixed(n < 10 ? 1 : 0) + " " + units[i];
  }

  function fmtAgo(iso) {
    if (!iso) return "—";
    var t = Date.parse(iso);
    if (isNaN(t)) return "—";
    var s = Math.max(0, Math.floor((Date.now() - t) / 1000));
    if (s < 60) return s + " 秒前";
    if (s < 3600) return Math.floor(s / 60) + " 分钟前";
    if (s < 86400) return Math.floor(s / 3600) + " 小时前";
    return Math.floor(s / 86400) + " 天前";
  }

  function setNotice(text) {
    var node = el("notice");
    if (!text) { node.classList.add("hidden"); node.textContent = ""; return; }
    node.textContent = text;
    node.classList.remove("hidden");
  }

  function setBusy(state) {
    busy = state;
    ["startBtn", "stopBtn", "restartBtn", "saveBtn", "tsLogin", "tsLogout"].forEach(function (id) {
      var b = el(id);
      if (b) b.disabled = state;
    });
  }

  function renderStatus(s) {
    current.status = s;
    var running = s.service && s.service.state === "running";
    var dufsUp = s.dufs && s.dufs.running;

    el("stateBadge").textContent = running ? (dufsUp ? "运行中" : "启动中") : "已停止";
    el("stateBadge").className = "badge " + (running && dufsUp ? "good" : running ? "warn" : "muted");

    var urls = []
      .concat((s.addresses && s.addresses.lan) || [])
      .concat((s.addresses && s.addresses.tailnet) || []);
    var primary = urls[0] || "";
    el("primaryUrl").textContent = primary || "尚未就绪";
    el("primaryUrl").href = primary || "#";
    el("allUrls").textContent = urls.length > 1 ? urls.slice(1).join("   ") : "";
    el("copyUrl").disabled = !primary;

    var notices = [];
    if (!running) notices.push("服务已停止，点击「启动」恢复。");
    if (running && !dufsUp) notices.push("文件服务正在启动，请稍候。");
    if (s.health && !s.health.share_path_exists) notices.push("共享路径不存在：" + s.config.share_path);
    else if (s.health && !s.health.share_path_writable) notices.push("共享路径当前不可写，客户端只能读取。");
    if (s.tailscale && s.tailscale.state === "needs_login") notices.push("Tailscale 需要登录，请点击「连接 Tailscale」。");
    if (s.last_error) notices.push(s.last_error);
    setNotice(notices.join(" "));

    el("deviceCount").textContent = "· " + (s.stats ? s.stats.online + "/" + s.stats.devices : "0");

    var cells = [
      ["共享路径", s.config.share_path],
      ["共享名称", s.config.share_name],
      ["端口", String(s.config.port)],
      ["共享方式", { lan: "仅局域网", tailscale: "仅 Tailscale", both: "局域网 + Tailscale" }[s.config.mode] || s.config.mode],
      ["认证模式", s.config.auth_mode === "password" ? "账号密码（" + s.config.username + "）" : "匿名访问"],
      ["协议版本", "HTTP " + s.protocol.http + " · WebDAV " + s.protocol.webdav],
      ["Dufs", s.dufs.version + (s.dufs.running ? "（pid " + s.dufs.pid + "）" : "（未运行）")],
      ["默认权限", { rw: "可写", ro: "只读", deny: "拒绝" }[s.config.default_policy] || s.config.default_policy],
      ["全局只读", s.config.readonly_global ? "已开启" : "关闭"],
      ["开机自启", s.config.autostart ? "已开启" : "关闭"],
      ["Tailscale", s.tailscale.state + (s.tailscale.ip ? " · " + s.tailscale.ip : "")],
      ["运行时长", Math.floor((s.service.uptime_sec || 0) / 60) + " 分钟"],
      ["流量", "↑ " + fmtBytes(s.stats.bytes_in) + "  ↓ " + fmtBytes(s.stats.bytes_out)],
      ["请求 / 拒绝", (s.stats.requests || 0) + " / " + (s.stats.denied || 0)]
    ];
    el("statusGrid").innerHTML = cells.map(function (pair) {
      return '<div class="cell"><div class="k">' + escapeHtml(pair[0]) + '</div><div class="v">' + escapeHtml(pair[1]) + "</div></div>";
    }).join("");

    // Settings form: only fill when the user is not editing (avoid clobbering).
    var form = el("sharePath");
    if (document.activeElement !== form) {
      el("sharePath").value = s.config.share_path;
      el("shareName").value = s.config.share_name;
      el("port").value = s.config.port;
      el("mode").value = s.config.mode;
      el("authMode").value = s.config.auth_mode;
      el("username").value = s.config.username;
      el("defaultPolicy").value = s.config.default_policy;
      el("readonlyGlobal").checked = !!s.config.readonly_global;
      el("autostart").checked = !!s.config.autostart;
      el("logLevel").value = s.config.log_level;
    }
  }

  function escapeHtml(v) {
    return String(v == null ? "" : v).replace(/[&<>"']/g, function (c) {
      return { "&": "&amp;", "<": "&lt;", ">": "&gt;", '"': "&quot;", "'": "&#39;" }[c];
    });
  }

  function renderDevices(list) {
    current.devices = list || [];
    var host = el("devices");
    if (!list || !list.length) {
      host.innerHTML = '<p class="hint">暂无连接设备。有人访问共享后会自动出现在这里。</p>';
      return;
    }
    host.innerHTML = list.map(function (d) {
      var name = d.hostname || d.ip;
      var meta = [d.ip, d.mac || "MAC 未知", d.kind === "tailnet" ? "Tailscale" : "局域网"].join(" · ");
      var stats = "请求 " + d.requests + " · 活跃 " + d.active_conns + " · ↑" + fmtBytes(d.bytes_in) +
        " ↓" + fmtBytes(d.bytes_out) + (d.denied ? " · 已拒绝 " + d.denied : "") +
        " · 最近 " + fmtAgo(d.last_seen);
      var perms = [["deny", "拒绝"], ["ro", "只读"], ["rw", "可写"]].map(function (p) {
        var active = d.policy === p[0] ? " active" : "";
        return '<button type="button" class="' + active.trim() + '" data-policy="' + p[0] +
          '" data-device="' + escapeHtml(d.id) + '">' + p[1] + "</button>";
      }).join("");
      return '<div class="device' + (d.online ? "" : " offline") + '">' +
        '<div class="device-head"><span class="dot' + (d.online ? " on" : "") + '"></span>' +
        '<span class="device-name">' + escapeHtml(name) + "</span>" +
        '<span class="tag">' + escapeHtml(d.policy) + "</span>" +
        '<span class="tag">' + (d.online ? "在线" : "离线") + "</span></div>" +
        '<div class="device-meta">' + escapeHtml(meta) + "</div>" +
        '<div class="device-stats">' + escapeHtml(stats) + "</div>" +
        '<div class="perm">' + perms + "</div></div>";
    }).join("");
  }

  function renderLogs(entries) {
    var host = el("logs");
    if (!entries || !entries.length) { host.textContent = "该级别暂无日志。"; return; }
    host.innerHTML = entries.map(function (e) {
      var cls = "l-" + (e.level || "info");
      return '<span class="' + cls + '">' + escapeHtml(e.ts) + " [" + escapeHtml((e.level || "").toUpperCase()) +
        "] " + escapeHtml(e.src) + " " + escapeHtml(e.msg) + "</span>";
    }).join("\n");
    host.scrollTop = host.scrollHeight;
  }

  // --- data loading --------------------------------------------------------

  function loadStatus() {
    return ctl("status").then(renderStatus);
  }

  function loadDevices() {
    return ctl("devices").then(function (d) { renderDevices(d.devices); });
  }

  function loadLogs() {
    var level = el("logLevel").value;
    return ctl("logs", { level: level, limit: 300 }).then(function (d) { renderLogs(d.entries); });
  }

  function refresh() {
    if (busy) return;
    ensureDaemon()
      .then(function () { return Promise.all([loadStatus(), loadDevices()]); })
      .then(function () { setNotice(""); })
      .catch(function (err) { setNotice(String(err.message || err)); });
  }

  // --- actions -------------------------------------------------------------

  function run(promise, successMessage) {
    if (busy) return Promise.resolve();
    setBusy(true);
    return promise
      .then(function () {
        if (successMessage) toast(successMessage);
        return Promise.all([loadStatus(), loadDevices()]);
      })
      .catch(function (err) { toast(String(err.message || err)); setNotice(String(err.message || err)); })
      .then(function () { setBusy(false); });
  }

  function serviceAction(action, message) {
    return run(ensureDaemon().then(function () { return ctl("service.set", { action: action }); }), message);
  }

  function bind() {
    el("startBtn").addEventListener("click", function () { serviceAction("start", "已启动"); });
    el("stopBtn").addEventListener("click", function () { serviceAction("stop", "已停止"); });
    el("restartBtn").addEventListener("click", function () { serviceAction("restart", "已重启"); });

    el("copyUrl").addEventListener("click", function () {
      var url = el("primaryUrl").textContent;
      if (!url || url === "尚未就绪") return;
      try {
        navigator.clipboard.writeText(url);
        toast("地址已复制");
      } catch (e) {
        toast(url);
      }
    });

    el("devices").addEventListener("click", function (ev) {
      var btn = ev.target.closest ? ev.target.closest("button[data-policy]") : null;
      if (!btn) return;
      var id = btn.getAttribute("data-device");
      var policy = btn.getAttribute("data-policy");
      run(ctl("acl.set", { id: id, policy: policy }), policy === "rw" ? "已设为可写" : policy === "ro" ? "已设为只读" : "已拒绝");
    });

    el("logRefresh").addEventListener("click", function () { loadLogs().catch(function () {}); });
    el("logLevel").addEventListener("change", function () { loadLogs().catch(function () {}); });

    el("saveBtn").addEventListener("click", function () {
      var patch = {
        share_path: el("sharePath").value.trim(),
        share_name: el("shareName").value.trim(),
        port: parseInt(el("port").value, 10) || 8080,
        mode: el("mode").value,
        auth_mode: el("authMode").value,
        username: el("username").value.trim(),
        default_policy: el("defaultPolicy").value,
        readonly_global: el("readonlyGlobal").checked,
        autostart: el("autostart").checked,
        log_level: el("logLevel").value
      };
      var pw = el("password").value;
      if (pw) patch.password = pw;
      run(ctl("config.set", patch), "设置已保存").then(function () { el("password").value = ""; });
    });

    el("tsLogin").addEventListener("click", function () {
      run(ctl("tailscale.set", { action: "up" }), "请在状态中打开授权链接");
    });
    el("tsLogout").addEventListener("click", function () {
      run(ctl("tailscale.set", { action: "logout" }), "已退出 Tailscale");
    });
  }

  bind();
  refresh();
  loadLogs().catch(function () {});
  setInterval(refresh, REFRESH_MS);
})();
