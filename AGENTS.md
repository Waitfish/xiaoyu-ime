# xiaoyu-ime 项目说明

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
- librime 走系统包。Ubuntu 真实包名：`librime-dev` + 数据三件套
  `rime-data-luna-pinyin`（方案）+ `rime-prelude`（default.yaml 等）+ `rime-essay`（词库）；
  `librime-data` 只有文档，`rime-data`/`fcitx5-dev` 在 Ubuntu 不存在。shared_data_dir=/usr/share/rime-data。
- 目标 API 是 fcitx5 **5.1.7**（24.04/26.04 一致）：引擎类=InputMethodEngineV2（本身即 AddonInstance，
  工厂 `create(AddonManager*)`）；activate/deactivate/reset 收 `InputContextEvent&`（`inputContext()` 是指针）；
  UI 走 `inputPanel().setPreedit/setCandidateList(DisplayOnlyCandidateList)` + `updateUserInterface`；
  提交 `commitString()`；会话按 `InputContext::uuid()`；IM 条目靠 `/usr/share/fcitx5/inputmethod/xiaoyu.conf`。
  （jammy 5.0.x 是旧 API，如要支持需单独兼容分支，暂不做。）
- librime ≥1.9：`process_key(session, keycode, mask)` 3 参；无 `clear_session`（用 `clear_composition`）；
  头文件不再导出修饰掩码枚举（X11 值本地定义：Shift=1, Ctrl=4, Mod1=8）。
- **librime traits 大坑**：`api->setup()` 不填 `data_size`（只声明模块依赖+日志）；
  `data_size` 为 0/垃圾时 librime 按旧版结构**忽略全部字段**（用户目录会退化成 cwd）。
  必须手动 `traits.data_size = sizeof(traits)`（C++ 和 Go 都要）。
- **librime 会把缺 `config_version` 字段的用户配置移进 `trash/`**——写 default.yaml 必须带 `config_version: "1.0"`。
- **fcitx5 addon conf 正确写法**（5.1.19 实测）：`Type=SharedLibrary` + `Library=libxiaoyu-ime`
  （`Type=IMEngine`/`Library=export:libXXX` 是错误写法，会 "Could not load addon"）。
- 首次运行模块自动写 `~/.local/share/xiaoyu-ime/rime/default.yaml` 默认简体 `luna_pinyin_simp`
  （luna_pinyin 默认输出繁体）。
- C++ 必须 `-std=c++20`（fcitx5 5.1 头文件用 std::span/starts_with）。

## GNOME Wayland 事实（26.04 / Shell 50.1 实测）

- GNOME 的 Wayland 输入法通道是 **IBus 专属**（gnome-shell `js/misc/inputMethod.js` 直连 IBus.Bus），
  第三方 IME 拿不到 native protocol（fcitx5 自诊断 `Using Wayland native input method protocol: 0`）。
- 正确路径 = **immodule 前端直连 fcitx5**：`~/.config/environment.d/fcitx5.conf` 写
  `GTK_IM_MODULE=fcitx / QT_IM_MODULE=fcitx / XMODIFIERS=@im=fcitx`（需重新登录生效一次）。
  生效后重启 fcitx5 应用自动重连（GTK immodule 监听 NameOwnerChanged），不必注销；
  不设置时会话默认 ibus（走 fcitx5 ibusfrontend 冒充 IBus，重启即断，这就是"必须注销"的根因）。
- 「建议安装 GNOME 扩展」通知 = fcitx5 wayland 插件诊断提示（ID `wayland-diagnose-gnome`，推荐 kimpanel），
  隐藏：`~/.config/fcitx5/conf/notifications.conf` → `HiddenNotifications=wayland-diagnose-gnome`。
- waylandim 插件禁不掉（classicui 硬依赖它），留着无害。
- 重启用 `scripts/restart-fcitx5.sh`（SSH 下必须带 set -a eval 会话环境）。

## 里程碑状态

M1 fcitx5 模块（真机打字验证通过，26.04 arm64）
M2 Go 守护进程：已上线（见上方协议）
M3 MyGo 候选窗：**核心已上线**——daemon 中枢（subscribe/ui光标/select/状态广播/ui_connected 协商）；
   模块检测到候选窗在线即清空内置面板；uiapp（GTK3，vendor 离线构建，进 deb /usr/libexec/xiaoyu-ime-ui）；
   daemon 启动时自动拉起 uiapp，但 **GNOME Wayland 无 layer-shell → 跳过并回退内置面板**（已实测）；
   视觉验证走 `ui.Render` 离屏渲染 + 像素断言（模型不能读图时的替代）。
   待做：设置程序（方案切换/词库同步）。
→ M4 PPA。
坑：测试脚本里 `eval "$(systemctl --user show-environment)"` 必须 `set -a`（否则变量不 export，
   子进程看不到 XDG_SESSION_TYPE 等，行为与真实 fcitx5 拉起的进程不一致）。
注意：进程名 xiaoyu-ime-daemon 超 15 字符，pkill -x 匹配不到，用 `pkill -f "^/usr/libexec/xiaoyu-ime-daemon$"`。
