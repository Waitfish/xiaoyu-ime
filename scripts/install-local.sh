#!/usr/bin/env bash
# 本地试用安装：不需要 root，fcitx5 会加载用户目录下的模块与配置
set -euo pipefail
cd "$(dirname "$0")/.."
so=fcitx5-module/build/libtomato-ime.so
[ -f "$so" ] || { echo "先运行 scripts/build.sh" >&2; exit 1; }
mkdir -p ~/.local/share/fcitx5/addon ~/.local/share/fcitx5/inputmethod ~/.config/fcitx5/addon
cp "$so" ~/.local/share/fcitx5/addon/
cp fcitx5-module/data/tomato.conf ~/.config/fcitx5/addon/tomato.conf
cp fcitx5-module/data/inputmethod.tomato.conf ~/.local/share/fcitx5/inputmethod/tomato.conf
echo "已安装。重启 fcitx5：fcitx5 -r -d，然后在托盘「添加输入法」里选 番茄。"
