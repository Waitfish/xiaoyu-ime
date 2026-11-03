// xiaoyu-ime-ui：MyGo 候选窗（M3）。
//
// 订阅守护进程的状态推送，在光标处显示 preedit + 候选；点击候选回 select。
// X11 / wlroots 系 Wayland（走 gtk-layer-shell）下是原生覆盖窗：置顶、免任务栏、任意定位。
// GNOME Wayland 不支持 layer-shell（MyGo 后端注释明确），会退化为普通窗——
// 该场景建议用 fcitx5 内置候选窗（daemon 无订阅者时模块自动用内置面板）。
package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"sync"
	"time"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
)

type candidate struct {
	Text    string `json:"text"`
	Comment string `json:"comment,omitempty"`
}

type cursor struct {
	X, Y, W, H int
	Scale      float64
}

type stateMsg struct {
	Type       string      `json:"type"`
	Session    uint32      `json:"session"`
	Visible    bool        `json:"visible"`
	Preedit    string      `json:"preedit"`
	Candidates []candidate `json:"candidates"`
	Highlight  int         `json:"highlight"`
	Cursor     cursor      `json:"cursor"`
}

type imeUI struct {
	mu     sync.Mutex
	st     stateMsg
	ap     appearanceCfg
	conn   net.Conn
	connMu sync.Mutex
	win    *mygo.Window
}

func (u *imeUI) apSnapshot() appearanceCfg {
	u.mu.Lock()
	defer u.mu.Unlock()
	return u.ap
}

// reloadAppearance 收到 daemon 的 config 广播后重取外观并即时重排候选窗
// （窗口尺寸每次 show 时按 estimate 重设，无需重启进程）。
func (u *imeUI) reloadAppearance() {
	m, err := daemonCall(map[string]any{"op": "get_config"})
	if err != nil {
		return
	}
	u.mu.Lock()
	changed := m.Ap != u.ap
	u.ap = m.Ap
	u.mu.Unlock()
	if changed {
		u.win.Invalidate()
		st := u.snapshot()
		if st.Visible {
			u.apply(st)
		}
	}
}

type state = stateMsg

func socketPath() string {
	dir := os.Getenv("XDG_RUNTIME_DIR")
	if dir == "" {
		dir = os.TempDir()
	}
	return dir + "/xiaoyu-ime.sock"
}

func (u *imeUI) snapshot() stateMsg {
	u.mu.Lock()
	defer u.mu.Unlock()
	return u.st
}

// readLoop 保持与 daemon 的连接：重连、订阅、把每条 state 推给窗口。
func (u *imeUI) readLoop() {
	for {
		conn, err := net.Dial("unix", socketPath())
		if err != nil {
			time.Sleep(500 * time.Millisecond)
			continue
		}
		u.connMu.Lock()
		u.conn = conn
		u.connMu.Unlock()
		fmt.Fprintln(conn, `{"op":"subscribe"}`)

		sc := bufio.NewScanner(conn)
		sc.Buffer(make([]byte, 64*1024), 1024*1024)
		for sc.Scan() {
			var m stateMsg
			if json.Unmarshal(sc.Bytes(), &m) != nil {
				continue
			}
			switch m.Type {
			case "state":
				u.apply(m)
			case "config":
				u.reloadAppearance()
			}
		}
		u.connMu.Lock()
		u.conn = nil
		u.connMu.Unlock()
		conn.Close()
		u.win.Hide()
		time.Sleep(300 * time.Millisecond)
	}
}

func (u *imeUI) apply(m stateMsg) {
	u.mu.Lock()
	u.st = m
	u.mu.Unlock()

	if !m.Visible {
		u.win.Hide()
		return
	}
	w, h := u.estimate(m)
	u.win.SetSize(w, h)
	scale := m.Cursor.Scale
	if scale <= 0 {
		scale = 1
	}
	x := int(float64(m.Cursor.X) * scale)
	y := int(float64(m.Cursor.Y+m.Cursor.H+8) * scale)
	u.win.SetPosition(x, y)
	u.win.Show()
	u.win.Invalidate()
}

// estimate 按外观估算窗口尺寸：CJK 字形宽≈字号。
func (u *imeUI) estimate(m stateMsg) (int, int) {
	ap := u.apSnapshot().normalized()
	fs := ap.FontSize
	gap, pad := 10, 20
	if ap.Style == "scale" {
		gap = ap.FontSize / 3
		pad = ap.FontSize
	}
	cands := m.Candidates
	if len(cands) > ap.PageSize {
		cands = cands[:ap.PageSize]
	}
	if ap.Orientation == "vertical" {
		w := 0
		for _, c := range cands {
			cw := fs + len([]rune(c.Text))*fs + 12
			if c.Comment != "" {
				cw += len([]rune(c.Comment)) * (fs * 2 / 3)
			}
			if cw > w {
				w = cw
			}
		}
		h := len(cands)*(fs+12) + pad
		if m.Preedit != "" {
			h += fs + 8
		}
		if w < 80 {
			w = 80
		}
		return w + pad, h
	}
	w := pad
	if m.Preedit != "" {
		w += len([]rune(m.Preedit))*(fs*4/5) + gap
	}
	for _, c := range m.Candidates {
		w += fs + len([]rune(c.Text))*fs + gap
		if c.Comment != "" {
			w += len([]rune(c.Comment)) * (fs * 2 / 3)
		}
	}
	if w > 900 {
		w = 900
	}
	return w, fs+34
}

func (u *imeUI) pick(index int) {
	u.mu.Lock()
	sess := u.st.Session
	u.mu.Unlock()
	u.connMu.Lock()
	conn := u.conn
	u.connMu.Unlock()
	if conn == nil {
		return
	}
	b, _ := json.Marshal(map[string]any{"op": "select", "session": sess, "index": index})
	conn.Write(append(b, '\n'))
}

func (u *imeUI) view(c *ui.Context) {
	st := u.snapshot()
	if !st.Visible {
		return
	}
	candidatesView(c, st, u.apSnapshot(), u.pick)
}

func main() {
	if len(os.Args) > 1 && os.Args[1] == "--settings" {
		runSettings()
		return
	}
	u := &imeUI{ap: defaultAp()}
	if m, err := daemonCall(map[string]any{"op": "get_config"}); err == nil {
		u.ap = m.Ap
	}
	// MyGo 要求窗口在 App 就绪后创建（NewWindow 早于 WhenReady 会 panic）
	mygo.App.WhenReady(func() {
		u.win = mygo.NewWindow(mygo.WindowOptions{
			Title:         "小鱼候选窗",
			Frameless:     true,
			AlwaysOnTop:   true,
			SkipTaskbar:   true,
			Hidden:        true,
			DisableMove:   true,
			DisableResize: true,
			Width:         300,
			Height:        56,
			Content:       ui.View(u.view),
		})
		go u.readLoop()
	})
	mygo.App.OnWindowAllClosed(func() { os.Exit(0) })
	if err := mygo.App.Run(); err != nil {
		fmt.Fprintln(os.Stderr, "xiaoyu-ime-ui:", err)
		os.Exit(1)
	}
}
