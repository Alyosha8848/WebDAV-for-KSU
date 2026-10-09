#!/system/bin/sh
# DufsBox boot service.
#
# Runs from KernelSU / Magisk at the late_start service stage. The daemon is
# exec'd so the module's boot process *is* the daemon: there is no second
# supervisor to keep in sync.

MODDIR=${0%/*}
STATE_DIR=/data/adb/dufsbox
BIN="$MODDIR/bin/arm64/dufsboxd"
BOOT_LOG="$STATE_DIR/logs/boot.log"

mkdir -p "$STATE_DIR/logs" "$STATE_DIR/tmp"
chmod 0700 "$STATE_DIR" "$STATE_DIR/logs" "$STATE_DIR/tmp" 2>/dev/null

# A new boot invalidates every pid file and socket: Android reuses pids, and a
# socket left behind by an unclean shutdown would make the CLI hang.
rm -rf "$STATE_DIR/run"
mkdir -p "$STATE_DIR/run"
chmod 0700 "$STATE_DIR/run"

log_boot() {
  printf '%s %s\n' "$(date '+%Y-%m-%d %H:%M:%S')" "$*" >> "$BOOT_LOG" 2>/dev/null
}

if [ ! -x "$BIN" ]; then
  log_boot "FATAL dufsboxd missing or not executable: $BIN"
  exit 1
fi

# Wait (bounded) for Android to finish booting. Starting too early means
# /storage/emulated/0 is not mounted yet, and dufs would fail to bind the share.
i=0
while [ "$(getprop sys.boot_completed 2>/dev/null)" != "1" ] && [ "$i" -lt 120 ]; do
  sleep 1
  i=$((i + 1))
done

# Honour the App's auto-start toggle.
if ! "$BIN" autostart-check >>"$BOOT_LOG" 2>&1; then
  log_boot "auto-start disabled by configuration, not starting"
  exit 0
fi

log_boot "starting dufsboxd (boot_completed=$(getprop sys.boot_completed 2>/dev/null))"
exec "$BIN" serve >>"$STATE_DIR/logs/dufsboxd.stdout.log" 2>&1
