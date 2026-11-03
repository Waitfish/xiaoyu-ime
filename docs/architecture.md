# 架构（路线 C）

番茄输入法 = **平台薄壳 × N + 引擎守护进程 × 1 + MyGo UI × 1**，与 Weasel（TSF DLL + UI 进程）、Squirrel 同构。

```
┌───────────────────────────────────────────────────────┐
│ 候选窗 / 设置 / 托盘        MyGo（Go，一份代码）        │  ← M3
├───────────────────────────────────────────────────────┤
│ 引擎守护进程               Go + cgo + librime（一份）   │  ← M2
│   $XDG_RUNTIME_DIR/tomato-ime.sock，换行 JSON          │
├───────────────────────────────────────────────────────┤
│ 平台接入层（每平台一个原生薄壳，永久存在）              │
│   Linux   fcitx5-module/  C++ addon（M1，当前）        │
│   macOS   IMK XPC 服务（ObjC，未来）                   │
│   Windows TSF COM DLL（C++，未来）                     │
└───────────────────────────────────────────────────────┘
```

## 为什么 M1 先内嵌 librime

M1 目标是"Ubuntu 26.04 能打字"：fcitx5 模块直接调 librime C API（约 200 行），
候选窗用 fcitx5 自带 UI（Wayland/X11 定位都正确，零额外工作）。
跑通后 M2 把模块里的 rime 调用换成 socket 转发，模块只剩按键转发 + UI 同步，
引擎迁到 Go 守护进程；M3 上 MyGo 候选窗（X11 精确定位；Wayland 下继续用
fcitx5 内置 UI 或 layer-shell）。

## 关键约束

- fcitx5 模块是 C++，加载进 fcitx5 进程；Go 运行时不能进 fcitx5 进程，
  所以引擎必须在独立进程（守护进程），这也是三平台复用引擎的唯一方式。
- librime 的 C API（rime_api.h）是稳定 ABI；三平台同一份引擎代码。
- 键值统一用 X keysym + Rime 修饰掩码，Linux/macOS 原生就是这个体系，
  Windows 侧做一张 VK→keysym 映射表即可。
- Ubuntu 26.04 默认 GNOME+IBus；使用本项目需切到 fcitx5（im-config -n fcitx5）。
  未来可加 IBus 壳（第二块 Linux 拼图），复用同一个守护进程。

## 里程碑

| | 内容 | 状态 |
| --- | --- | --- |
| M1 | fcitx5 模块内嵌 librime，Ubuntu 26.04 可打字 | 当前 |
| M2 | Go 守护进程（daemon/），模块改 socket 转发 | 骨架已编译 |
| M3 | MyGo 候选窗 + 设置界面 + 托盘 | - |
| M4 | 打包进 PPA（launchpad-ppa-release skill） | - |
