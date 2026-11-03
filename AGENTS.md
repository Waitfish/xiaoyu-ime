# tomato-ime 项目说明

跨平台输入法（路线 C）。中文沟通。目标平台优先 Ubuntu 26.04（fcitx5）。

## 构建命令

```sh
./scripts/build.sh                      # fcitx5 模块（Linux）
cd daemon && go vet ./... && go build   # Go 守护进程（Linux；macOS 需 brew librime + PKG_CONFIG_PATH）
```

macOS 本地编译 daemon：`PKG_CONFIG_PATH=/opt/homebrew/opt/librime/lib/pkgconfig go build ./...`

## 铁律

- fcitx5 模块里**不能**嵌入 Go 运行时（模块加载进 fcitx5 进程）；引擎必须在独立守护进程。
- 键值统一 X keysym + Rime 修饰掩码。
- librime 走系统包（Ubuntu: librime-dev + rime-data；shared_data_dir=/usr/share/rime-data）。
- fcitx5 API 版本兼容：jammy 是 5.0.x，26.04 是 5.1+；候选列表用 V1 接口（两边都在），
  CI 矩阵会暴露 API 漂移。
- 发布 PPA 时直接用 launchpad-ppa-release skill（本仓库已有完整经验沉淀）。

## 里程碑状态

M1 fcitx5 模块内嵌 librime（进行中）→ M2 Go 守护进程 + socket 转发 → M3 MyGo 候选窗/设置 → M4 PPA。
