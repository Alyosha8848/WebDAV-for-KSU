#!/system/bin/sh
# DufsBox installer. Runs inside the KernelSU / Magisk module installer, where
# $MODPATH, ui_print, abort, set_perm and set_perm_recursive are provided.

ui_print "- 安装 DufsBox 1.0.0"

ABI="$(getprop ro.product.cpu.abi)"
SDK="$(getprop ro.build.version.sdk)"

if [ "$ABI" != "arm64-v8a" ]; then
  abort "! DufsBox 仅支持 arm64-v8a 设备（当前 ABI: $ABI）"
fi
if [ -n "$SDK" ] && [ "$SDK" -lt 36 ]; then
  ui_print "! 目标平台为 Android 16 / API 36，当前 API $SDK，继续安装但可能不兼容"
fi

# The module ships four binaries; verifying them here means a corrupted download
# or a tampered zip is caught before anything runs as root.
verify_bundled_binaries() {
  verified=0
  while read -r expected relative || [ -n "$expected$relative" ]; do
    [ -n "$expected" ] || continue
    case "$relative" in
      bin/arm64/*) ;;
      *)
        ui_print "! 校验清单条目非法: $relative"
        return 1
        ;;
    esac

    target="$MODPATH/$relative"
    if [ ! -f "$target" ]; then
      ui_print "! 缺少文件: $relative"
      return 1
    fi

    result=$(sha256sum "$target" 2>&1)
    if [ $? -ne 0 ]; then
      ui_print "! sha256sum 执行失败: $relative"
      return 1
    fi
    actual=${result%% *}
    if [ "$actual" != "$expected" ]; then
      ui_print "! 校验失败: $relative"
      ui_print "! 期望: $expected"
      ui_print "! 实际: $actual"
      return 1
    fi
    verified=$((verified + 1))
  done < "$MODPATH/checksums.sha256"

  if [ "$verified" -ne 4 ]; then
    ui_print "! 应有 4 个二进制通过校验，实际通过 $verified 个"
    return 1
  fi
}

if ! verify_bundled_binaries; then
  abort "! 内置二进制校验失败，已中止安装"
fi
ui_print "- 已校验 4 个 arm64 二进制"

set_perm_recursive "$MODPATH" 0 0 0755 0644
set_perm "$MODPATH/service.sh" 0 0 0755
set_perm "$MODPATH/action.sh" 0 0 0755
set_perm "$MODPATH/uninstall.sh" 0 0 0755
set_perm_recursive "$MODPATH/bin/arm64" 0 0 0755 0755

mkdir -p /data/adb/dufsbox/logs /data/adb/dufsbox/tmp
chmod 0700 /data/adb/dufsbox /data/adb/dufsbox/logs /data/adb/dufsbox/tmp 2>/dev/null

# Install / update the control app when it is bundled next to the module files.
APK="$MODPATH/DufsBox.apk"
if [ -f "$APK" ] && command -v pm >/dev/null 2>&1; then
  ui_print "- 安装管理 App"
  if pm install -r -g "$APK" >/dev/null 2>&1; then
    ui_print "- 管理 App 安装完成"
  else
    ui_print "! 管理 App 自动安装失败，请手动安装：$APK"
  fi
fi

ui_print "- 默认共享目录: /sdcard（可在 App 或 WebUI 中改成任意目录）"
ui_print "- 默认局域网可访问，端口 8080；重启后自动启动"
ui_print "- 想使用固定的 Tailscale 私有地址，请在 App「设置」中把共享方式改为 Tailscale"
