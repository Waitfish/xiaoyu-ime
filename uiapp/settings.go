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
	Ap             appearanceCfg
}

type settingsUI struct {
	m      cfgModel
	status string
	tab    int
	fs     float64 // 字号滑杆
	ps     float64 // 候选个数滑杆
	fontSel string // 字体下拉选择（"默认" 或字体族名）
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
			Schema     string `json:"schema"`
			AsciiMode  *bool  `json:"ascii_mode"`
			Simplification *bool `json:"simplification"`
			FullShape  *bool  `json:"full_shape"`
			Appearance struct {
				Orientation string `json:"orientation"`
				Font        string `json:"font"`
				FontSize    int    `json:"font_size"`
				PageSize    int    `json:"page_size"`
				Style       string `json:"style"`
			} `json:"appearance"`
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
	m := &cfgModel{Schema: resp.Config.Schema, Simplification: true,
		Ap: appearanceCfg{
			Orientation: resp.Config.Appearance.Orientation,
			Font:        resp.Config.Appearance.Font,
			FontSize:    resp.Config.Appearance.FontSize,
			PageSize:    resp.Config.Appearance.PageSize,
			Style:       resp.Config.Appearance.Style,
		}.normalized()}
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
	ap := s.m.Ap
	ap.FontSize = int(s.fs)
	ap.PageSize = int(s.ps)
	if s.fontSel == fontDefaultLabel {
		ap.Font = ""
	} else {
		ap.Font = s.fontSel
	}
	req := map[string]any{"op": "set_config", "config": map[string]any{
		"schema":         s.m.Schema,
		"ascii_mode":     s.m.AsciiMode,
		"simplification": s.m.Simplification,
		"full_shape":     s.m.FullShape,
		"appearance":     ap,
	}}
	if _, err := daemonCall(req); err != nil {
		s.status = "应用失败：" + err.Error()
		return
	}
	s.status = "已应用（对当前所有输入窗口即时生效）"
}

func (s *settingsUI) view(c *ui.Context) {
	t := c.Theme()
	ui.Column(c).Fill().Gap(12).Padding(18).Children(func() {
		ui.Text(c, "小鱼输入法设置").FontSize(18).TextColor(t.Accent)
		ui.Tabs(c, &s.tab, "输入方案", "外观")
		if s.tab == 1 {
			s.appearanceView(c)
			ui.Divider(c)
			ui.Row(c).Gap(10).AlignItems(ui.Center).Children(func() {
				ui.PrimaryButton(c, "应用").OnClick(s.apply)
				if s.status != "" {
					ui.Text(c, s.status).FontSize(12).TextColor(t.TextMuted)
				}
			})
			return
		}

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

const fontDefaultLabel = "默认"

func fontOptions() []string {
	opts := []string{fontDefaultLabel}
	for _, f := range ui.FontFamilies() {
		if f.Name != "" {
			opts = append(opts, f.Name)
		}
	}
	return opts
}

// appearanceView 外观页：顶部实时预览，下方参数变化即时反映到预览。
func (s *settingsUI) appearanceView(c *ui.Context) {
	t := c.Theme()
	prev := s.m.Ap.normalized()
	prev.FontSize = int(s.fs)
	prev.PageSize = int(s.ps)
	if s.fontSel == fontDefaultLabel {
		prev.Font = ""
	} else {
		prev.Font = s.fontSel
	}
	pst := stateMsg{
		Visible: true, Preedit: "pomo", Highlight: 0,
		Candidates: []candidate{
			{Text: "泼墨"}, {Text: "泼魔"}, {Text: "破墨"},
			{Text: "迫"}, {Text: "魄"}, {Text: "蒲"},
		},
	}
	ui.Row(c).Fill().Radius(8).Background(t.Surface).Padding(4).Children(func() {
		candidatesView(c, pst, prev, nil)
	})
	ui.Text(c, "以上为候选窗实际效果预览（候选窗为鼠标点击选择）").FontSize(11).TextColor(t.TextMuted)

	ui.Divider(c)
	ui.Text(c, "候选排列").FontSize(13).TextColor(t.TextMuted)
	ui.Row(c).Gap(16).AlignItems(ui.Center).Children(func() {
		ui.Radio(c, &s.m.Ap.Orientation, "horizontal", "横排")
		ui.Radio(c, &s.m.Ap.Orientation, "vertical", "竖排")
	})

	ui.Text(c, "候选字体").FontSize(13).TextColor(t.TextMuted)
	ui.Combobox(c, &s.fontSel, fontOptions())
	ui.Row(c).Gap(10).AlignItems(ui.Center).Children(func() {
		ui.Text(c, "字号").FontSize(13)
		ui.Slider(c, &s.fs, 12, 32)
		ui.Textf(c, "%d", int(s.fs)).FontSize(12).TextColor(t.TextMuted)
	})

	ui.Text(c, "候选个数（每页；对 IBus 面板同样生效）").FontSize(13).TextColor(t.TextMuted)
	ui.Row(c).Gap(10).AlignItems(ui.Center).Children(func() {
		ui.Slider(c, &s.ps, 2, 9)
		ui.Textf(c, "%d", int(s.ps)).FontSize(12).TextColor(t.TextMuted)
	})

	ui.Text(c, "候选样式").FontSize(13).TextColor(t.TextMuted)
	ui.Row(c).Gap(12).AlignItems(ui.Center).Children(func() {
		ui.Radio(c, &s.m.Ap.Style, "scroll", "卷轴模式")
		ui.Radio(c, &s.m.Ap.Style, "single", "单行模式")
		ui.Radio(c, &s.m.Ap.Style, "scale", "跟随字体缩放")
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
	s := &settingsUI{m: cfgModel{Schema: "luna_pinyin_simp", Simplification: true, Ap: defaultAp()}}
	if m, err := daemonCall(map[string]any{"op": "get_config"}); err == nil {
		s.m = *m
	} else {
		s.status = "未连接守护进程（" + err.Error() + "），保存仍会写入配置文件"
	}
	s.fs = float64(s.m.Ap.FontSize)
	s.ps = float64(s.m.Ap.PageSize)
	s.fontSel = s.m.Ap.Font
	if s.fontSel == "" {
		s.fontSel = fontDefaultLabel
	}
	mygo.App.WhenReady(func() {
		uiLog("app ready in %v", time.Since(logStart))
		mygo.NewWindow(mygo.WindowOptions{
			Title:   "小鱼输入法设置",
			Width:   460,
			Height:  620,
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
