#!/system/bin/sh
# DufsBox uninstaller.
#
# Runs while the module directory still exists, so this is the last chance to
# stop the daemon and the three processes it supervises.

MODDIR=${0%/*}
CLI="$MODDIR/bin/arm64/dufsboxd"
STATE_DIR=/data/adb/dufsbox

if [ -x "$CLI" ]; then
  "$CLI" ctl service.set --json '{"action":"stop"}' >/dev/null 2>&1
  sleep 1
fi

# The daemon may be mid-restart, so make sure nothing is left behind. Match on the
# module path so unrelated processes are never touched.
pkill -f "$MODDIR/bin/arm64/dufsboxd" 2>/dev/null
pkill -f "$MODDIR/bin/arm64/dufs " 2>/dev/null
pkill -f "$MODDIR/bin/arm64/tailscaled" 2>/dev/null
pkill -f "$MODDIR/bin/arm64/tailscale " 2>/dev/null

# Configuration and logs are kept on purpose so that reinstalling the module
# preserves the operator's share path, credentials and per-device rules.
# Remove them manually with:  rm -rf /data/adb/dufsbox
rm -f "$STATE_DIR/run/dufsboxd.sock" "$STATE_DIR/run/dufsboxd.pid" 2>/dev/null

ui_print "- DufsBox 已卸载，进程已停止"
ui_print "- 配置与日志保留在 /data/adb/dufsbox（如需彻底清除请手动删除）"
