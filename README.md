# 小鱼输入法（xiaoyu-ime）

基于 **librime** 的跨平台输入法，路线 C：各平台原生薄壳 + Go/librime 引擎守护进程 + MyGo 界面。
当前进度 **M1：fcitx5 模块，Ubuntu 26.04 可用**。

## Ubuntu 26.04 构建与试用

```sh
sudo apt install build-essential cmake pkg-config fcitx5-modules-dev librime-dev rime-data-luna-pinyin rime-prelude rime-essay fcitx5
./scripts/build.sh
./scripts/install-local.sh   # 装到 ~/.local/share/fcitx5/addon/

# 切到 fcitx5（Ubuntu 默认 GNOME 用 IBus）：
im-config -n fcitx5
# 注销重新登录，fcitx5 托盘 → 添加输入法 → 勾选「小鱼拼音」
```

打字验证：切到小鱼拼音后输入 `nihao`，候选出现「你好」，空格上屏。

## 目录

| 路径 | 说明 |
| --- | --- |
| `fcitx5-module/` | Linux 平台薄壳（C++ fcitx5 addon，M1 内嵌 librime） |
| `daemon/` | Go + cgo librime 引擎守护进程（M2 骨架） |
| `docs/architecture.md` | 三平台架构与里程碑 |

## 开发

```sh
cd fcitx5-module && cmake -B build && cmake --build build   # 模块
cd daemon && go vet ./... && go build ./...                 # 守护进程（需 librime-dev）
```

架构与坑位见 [docs/architecture.md](docs/architecture.md)。
