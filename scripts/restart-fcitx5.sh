#!/usr/bin/env bash
# 重启 fcitx5（含小鱼 daemon），带全会话环境——从 SSH 终端执行也必须可用。
# 注意：GTK/Qt 应用经 immodule 直连 fcitx5（GTK_IM_MODULE=fcitx，见 environment.d），
# fcitx5 重启后应用会自动重连，无需注销。
set -euo pipefail
set -a
eval "$(systemctl --user show-environment | grep -E '^(DBUS|DISPLAY|WAYLAND|XDG_CURRENT|XDG_SESSION)')"
set +a
export XDG_RUNTIME_DIR="${XDG_RUNTIME_DIR:?}"
pkill -x fcitx5 2>/dev/null || true
pkill -f "^/usr/libexec/xiaoyu-ime-daemon$" 2>/dev/null || true
sleep 2
nohup fcitx5 >/dev/null 2>&1 &
sleep 2
echo "fcitx5 已重启：$(fcitx5-remote -n 2>/dev/null || echo '(无聚焦窗口)')"
