// 设置窗口：输入方案（全拼/小鹤双拼/五笔）+ 默认输入状态（中英文/简繁/全半角）。
// 通过 daemon 的 get_config/set_config 读写；set_config 即时作用到所有在线会话
// 并持久化到 ~/.config/xiaoyu-ime/config.json，无需重启 daemon。
package main

import (
	"bufio"
	"runtime"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"strings"
	"time"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
)

// 与 daemon 的 config 对齐（指针区分"未设置"，UI 里统一展开为具体值）。
type cfgModel struct {
	Schema         string
	AsciiMode      bool // true = 英文模式
	Simplification bool // true = 简体
	FullShape      bool // true = 全角
}

type settingsUI struct {
	m  cfgModel
	status string
}

var schemas = []struct{ id, label string }{
	{"luna_pinyin_simp", "全拼"},
	{"double_pinyin_flypy", "双拼（小鹤）"},
	{"wubi86", "五笔"},
}

// uiLog 追加时间戳日志到 ~/.local/state/xiaoyu-ime/ui.log（菜单启动无 stderr 可看）。
func uiLog(format string, args ...any) {
	home, err := os.UserHomeDir()
	if err != nil {
		return
	}
	dir := home + "/.local/state/xiaoyu-ime"
	_ = os.MkdirAll(dir, 0o755)
	f, err := os.OpenFile(dir+"/ui.log", os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return
	}
	defer f.Close()
	fmt.Fprintf(f, "%s %s\n", time.Now().Format("15:04:05.000"), fmt.Sprintf(format, args...))
}

func dialSocket() (net.Conn, error) {
	return net.Dial("unix", socketPath())
}

// daemonCall 一次性连接 daemon 收发一条请求。
func daemonCall(req map[string]any) (*cfgModel, error) {
	conn, err := dialSocket()
	if err != nil {
		return nil, err
	}
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(3 * time.Second))
	b, _ := json.Marshal(req)
	if _, err := conn.Write(append(b, '\n')); err != nil {
		return nil, err
	}
	var resp struct {
		Config *struct {
			Schema         string `json:"schema"`
			AsciiMode      *bool  `json:"ascii_mode"`
			Simplification *bool  `json:"simplification"`
			FullShape      *bool  `json:"full_shape"`
		} `json:"config"`
		Error string `json:"error"`
	}
	sc := bufio.NewScanner(conn)
	if !sc.Scan() {
		return nil, fmt.Errorf("no response")
	}
	if err := json.Unmarshal(sc.Bytes(), &resp); err != nil {
		return nil, err
	}
	if resp.Error != "" {
		return nil, errors.New(resp.Error)
	}
	if resp.Config == nil {
		return nil, fmt.Errorf("no config in response")
	}
	m := &cfgModel{Schema: resp.Config.Schema, Simplification: true}
	if resp.Config.AsciiMode != nil {
		m.AsciiMode = *resp.Config.AsciiMode
	}
	if resp.Config.Simplification != nil {
		m.Simplification = *resp.Config.Simplification
	}
	if resp.Config.FullShape != nil {
		m.FullShape = *resp.Config.FullShape
	}
	return m, nil
}

func (s *settingsUI) apply() {
	req := map[string]any{"op": "set_config", "config": map[string]any{
		"schema":         s.m.Schema,
		"ascii_mode":     s.m.AsciiMode,
		"simplification": s.m.Simplification,
		"full_shape":     s.m.FullShape,
	}}
	if _, err := daemonCall(req); err != nil {
		s.status = "应用失败：" + err.Error()
		return
	}
	s.status = "已应用（对当前所有输入窗口即时生效）"
}

func (s *settingsUI) view(c *ui.Context) {
	t := c.Theme()
	ui.Column(c).Fill().Gap(14).Padding(18).Children(func() {
		ui.Text(c, "小鱼输入法设置").FontSize(18).TextColor(t.Accent)

		ui.Text(c, "输入方案").FontSize(13).TextColor(t.TextMuted)
		for _, sc := range schemas {
			ui.Radio(c, &s.m.Schema, sc.id, sc.label)
		}

		ui.Divider(c)
		ui.Text(c, "默认输入状态（新输入窗口生效）").FontSize(13).TextColor(t.TextMuted)

		s.switchRow(c, "英文模式", &s.m.AsciiMode, "开 = 直接英文输入", "关 = 中文输入")
		s.switchRow(c, "简体输出", &s.m.Simplification, "开 = 简体", "关 = 繁体")
		s.switchRow(c, "全角模式", &s.m.FullShape, "开 = 全角标点/字母", "关 = 半角")

		ui.Divider(c)
		ui.Row(c).Gap(10).AlignItems(ui.Center).Children(func() {
			ui.PrimaryButton(c, "应用").OnClick(s.apply)
			if s.status != "" {
				ui.Text(c, s.status).FontSize(12).TextColor(t.TextMuted)
			}
		})
	})
}

func (s *settingsUI) switchRow(c *ui.Context, label string, on *bool, onHint, offHint string) {
	hint := offHint
	if *on {
		hint = onHint
	}
	ui.Row(c).Gap(10).AlignItems(ui.Center).Children(func() {
		ui.Switch(c, on)
		ui.Text(c, label).FontSize(14)
		ui.Text(c, hint).FontSize(12).TextColor(c.Theme().TextMuted)
	})
}

// runSettings 打开设置窗口（xiaoyu-ime-ui --settings）。
func runSettings() {
	// GNOME Wayland 上 MyGo 的 GTK/Wayland 路径会让 mutter 主循环停摆数秒
	// （实测 DBus 探针超时；GDK_BACKEND=x11 走 XWayland 后零卡顿）。
	if strings.Contains(strings.ToLower(os.Getenv("XDG_CURRENT_DESKTOP")), "gnome") {
		os.Setenv("GDK_BACKEND", "x11")
	}
	logStart := time.Now()
	uiLog("start desktop=%q backend=%q DISPLAY=%q WAYLAND=%q GOMAXPROCS=%d",
		os.Getenv("XDG_CURRENT_DESKTOP"), os.Getenv("GDK_BACKEND"),
		os.Getenv("DISPLAY"), os.Getenv("WAYLAND_DISPLAY"), runtime.GOMAXPROCS(0))
	s := &settingsUI{m: cfgModel{Schema: "luna_pinyin_simp", Simplification: true}}
	if m, err := daemonCall(map[string]any{"op": "get_config"}); err == nil {
		s.m = *m
	} else {
		s.status = "未连接守护进程（" + err.Error() + "），保存仍会写入配置文件"
	}
	mygo.App.WhenReady(func() {
		uiLog("app ready in %v", time.Since(logStart))
		mygo.NewWindow(mygo.WindowOptions{
			Title:   "小鱼输入法设置",
			Width:   430,
			Height:  470,
			Content: ui.View(s.view),
		}).Show()
		uiLog("window shown in %v", time.Since(logStart))
	})
	mygo.App.OnWindowAllClosed(func() { os.Exit(0) })
	if err := mygo.App.Run(); err != nil {
		fmt.Fprintln(os.Stderr, "xiaoyu-ime-ui settings:", err)
		os.Exit(1)
	}
}
