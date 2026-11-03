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
- librime 走系统包。Ubuntu 真实包名：`librime-dev` + 数据三件套
  `rime-data-luna-pinyin`（方案）+ `rime-prelude`（default.yaml 等）+ `rime-essay`（词库）；
  `librime-data` 只有文档，`rime-data`/`fcitx5-dev` 在 Ubuntu 不存在。shared_data_dir=/usr/share/rime-data。
- 目标 API 是 fcitx5 **5.1.7**（24.04/26.04 一致）：引擎类=InputMethodEngineV2（本身即 AddonInstance，
  工厂 `create(AddonManager*)`）；activate/deactivate/reset 收 `InputContextEvent&`（`inputContext()` 是指针）；
  UI 走 `inputPanel().setPreedit/setCandidateList(DisplayOnlyCandidateList)` + `updateUserInterface`；
  提交 `commitString()`；会话按 `InputContext::uuid()`；IM 条目靠 `/usr/share/fcitx5/inputmethod/tomato.conf`。
  （jammy 5.0.x 是旧 API，如要支持需单独兼容分支，暂不做。）
- librime ≥1.9：`process_key(session, keycode, mask)` 3 参；无 `clear_session`（用 `clear_composition`）；
  头文件不再导出修饰掩码枚举（X11 值本地定义：Shift=1, Ctrl=4, Mod1=8）。
- C++ 必须 `-std=c++20`（fcitx5 5.1 头文件用 std::span/starts_with）。

## 里程碑状态

M1 fcitx5 模块内嵌 librime：**CI 编译通过**（24.04+26.04，run 41574303589）；真机运行验证待做
→ M2 Go 守护进程 + socket 转发 → M3 MyGo 候选窗/设置 → M4 PPA。
