#!/usr/bin/env bash
set -euo pipefail
cd "$(dirname "$0")/.."
cmake -B fcitx5-module/build -S fcitx5-module
cmake --build fcitx5-module/build -j
echo "built: fcitx5-module/build/libxiaoyu-ime.so"
