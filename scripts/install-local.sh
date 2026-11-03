#!/usr/bin/env bash
# 本地试用安装：不需要 root，fcitx5 会加载 ~/.local/share/fcitx5/addon/ 下的模块
set -euo pipefail
cd "$(dirname "$0")/.."
so=fcitx5-module/build/libtomato-ime.so
[ -f "$so" ] || { echo "先运行 scripts/build.sh" >&2; exit 1; }
mkdir -p ~/.local/share/fcitx5/addon
cp "$so" ~/.local/share/fcitx5/addon/
echo "已安装。重启 fcitx5：fcitx5 -r -d，然后在托盘里添加「番茄拼音」。"
