#!/usr/bin/env bash
# 本地试用安装：不需要 root。模块→用户 addon 目录；守护进程→用户 libexec；图标→用户图标主题
set -euo pipefail
cd "$(dirname "$0")/.."
so=fcitx5-module/build/libxiaoyu-ime.so
[ -f "$so" ] || { echo "先运行 scripts/build.sh" >&2; exit 1; }
( cd daemon && go build -o xiaoyu-ime-daemon . )
mkdir -p ~/.local/share/fcitx5/addon ~/.local/share/fcitx5/inputmethod ~/.config/fcitx5/addon \
         ~/.local/libexec ~/.local/share/icons/hicolor/scalable/status
cp "$so" ~/.local/share/fcitx5/addon/
cp fcitx5-module/data/xiaoyu.conf ~/.config/fcitx5/addon/xiaoyu.conf
cp fcitx5-module/data/inputmethod.xiaoyu.conf ~/.local/share/fcitx5/inputmethod/xiaoyu.conf
cp fcitx5-module/data/xiaoyu-input-method.svg ~/.local/share/icons/hicolor/scalable/status/
cp daemon/xiaoyu-ime-daemon ~/.local/libexec/
# 本地开发时模块从 /usr/libexec 找守护进程；如需本地路径，改模块里的 candidates 或建软链
echo "已安装。重启 fcitx5：fcitx5 -r -d，然后在托盘「添加输入法」里选 小鱼。"
