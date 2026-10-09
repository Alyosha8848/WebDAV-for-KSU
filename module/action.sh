#!/system/bin/sh
# DufsBox action button (KernelSU / Magisk "action" tap): toggles the share and
# prints where it can be reached.

MODDIR=${0%/*}
CLI="$MODDIR/bin/arm64/dufsboxd"
STATE_DIR=/data/adb/dufsbox

if [ ! -x "$CLI" ]; then
  echo "错误：找不到 dufsboxd"
  exit 1
fi

if "$CLI" ctl status 2>/dev/null | grep -q '"state":"running"'; then
  "$CLI" ctl service.set --json '{"action":"stop"}' >/dev/null 2>&1
  echo "DufsBox 已停止"
  echo "在 App 或模块 WebUI 中可以重新启动。"
  exit 0
fi

# Starting from a cold boot: make sure the daemon itself is up first.
"$CLI" launch >/dev/null 2>&1
"$CLI" ctl service.set --json '{"action":"start"}' >/dev/null 2>&1
sleep 1

echo "DufsBox 已启动"
STATUS="$("$CLI" ctl status 2>/dev/null)"
URL="$(printf '%s' "$STATUS" | tr ',' '\n' | grep -o 'http://[0-9][0-9.]*:[0-9]*/' | head -n 1)"
if [ -n "$URL" ]; then
  echo "访问地址：$URL"
else
  echo "尚未获得网络地址，请检查 Wi-Fi 连接。"
fi

SHARE="$("$CLI" ctl config.get 2>/dev/null | tr ',' '\n' | grep -o '"share_path":"[^"]*"' | head -n 1)"
[ -n "$SHARE" ] && echo "共享目录：${SHARE#*:}" | tr -d '"'
