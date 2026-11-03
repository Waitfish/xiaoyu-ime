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
- **librime traits 大坑**：`api->setup()` 不填 `data_size`（只声明模块依赖+日志）；
  `data_size` 为 0/垃圾时 librime 按旧版结构**忽略全部字段**（用户目录会退化成 cwd）。
  必须手动 `traits.data_size = sizeof(traits)`（C++ 和 Go 都要）。
- **librime 会把缺 `config_version` 字段的用户配置移进 `trash/`**——写 default.yaml 必须带 `config_version: "1.0"`。
- **fcitx5 addon conf 正确写法**（5.1.19 实测）：`Type=SharedLibrary` + `Library=libtomato-ime`
  （`Type=IMEngine`/`Library=export:libXXX` 是错误写法，会 "Could not load addon"）。
- 首次运行模块自动写 `~/.local/share/tomato-ime/rime/default.yaml` 默认简体 `luna_pinyin_simp`
  （luna_pinyin 默认输出繁体）。
- C++ 必须 `-std=c++20`（fcitx5 5.1 头文件用 std::span/starts_with）。

## 里程碑状态

M1 fcitx5 模块（真机打字验证通过，26.04 arm64）
M2 Go 守护进程：**已上线**——模块纯转发（不再链接 librime），socket `$XDG_RUNTIME_DIR/tomato-ime.sock`，
   协议换行 JSON：`{"op":"open|close|reset|key",...}`；模块连接失败自动 fork/exec `/usr/libexec/tomato-ime-daemon`；
   daemon 独占 librime，全局 mutex 串行（librime 非线程安全）；协议字段小写（Go 侧必须加 json tag）。
→ M3 MyGo 候选窗/设置程序 → M4 PPA。
注意：进程名 tomato-ime-daemon 超 15 字符，pkill -x 匹配不到，用 `pkill -f "^/usr/libexec/tomato-ime-daemon$"`。
