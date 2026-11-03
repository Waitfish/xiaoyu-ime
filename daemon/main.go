// tomato-ime 引擎守护进程（M3）：独占 librime；fcitx5 模块转发按键，
// MyGo 候选窗（tomato-ime-ui）订阅状态推送。中枢模型：
//
//   fcitx5 模块 ──key/ui/select──► daemon ──state 广播──► 候选窗（订阅者）
//
// 协议：$XDG_RUNTIME_DIR/tomato-ime.sock 换行分隔 JSON。
//   模块请求 {"op":"open|close|reset|key|ui|select",...}
//   订阅   {"op":"subscribe"} 之后该连接会收到 {"type":"state",...} 推送
//   ui_connected=true 时模块应跳过 fcitx5 内置面板（由候选窗接管显示）。
package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"log"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"syscall"

	"tomato-ime/daemon/rime"
)

type request struct {
	Op      string  `json:"op"`
	Session uint32  `json:"session"`
	Keysym  int     `json:"keysym"`
	Mods    uint    `json:"mods"`
	Index   int     `json:"index"`
	X       int     `json:"x"`
	Y       int     `json:"y"`
	W       int     `json:"w"`
	H       int     `json:"h"`
	Scale   float64 `json:"scale"`
}

type response struct {
	Session     uint32          `json:"session,omitempty"`
	Handled     bool            `json:"handled"`
	Commit      string          `json:"commit,omitempty"`
	Preedit     string          `json:"preedit,omitempty"`
	Candidates  []rime.Candidate `json:"candidates,omitempty"`
	Page        int             `json:"page"`
	PageSize    int             `json:"page_size"`
	IsLastPage  bool            `json:"is_last_page"`
	Highlight   int             `json:"highlight"`
	UIConnected bool            `json:"ui_connected"`
	Error       string          `json:"error,omitempty"`
}

type cursor struct {
	X, Y, W, H int
	Scale      float64
}

type stateMsg struct {
	Type       string           `json:"type"`
	Session    uint32           `json:"session"`
	Visible    bool             `json:"visible"`
	Preedit    string           `json:"preedit,omitempty"`
	Candidates []rime.Candidate `json:"candidates,omitempty"`
	Highlight  int              `json:"highlight"`
	Page       int              `json:"page"`
	PageSize   int              `json:"page_size"`
	IsLastPage bool             `json:"is_last_page"`
	Cursor     cursor           `json:"cursor"`
}

func socketPath() string {
	dir := os.Getenv("XDG_RUNTIME_DIR")
	if dir == "" {
		dir = os.TempDir()
	}
	return filepath.Join(dir, "tomato-ime.sock")
}

// ensureUserConfig 首次运行写入默认简体方案。
// 注意 librime 会把缺 config_version 的用户配置移进 trash/。
func ensureUserConfig(dir string) error {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	p := filepath.Join(dir, "default.yaml")
	if _, err := os.Stat(p); err == nil {
		return nil
	}
	return os.WriteFile(p, []byte("config_version: \"1.0\"\nschema_list:\n  - schema: luna_pinyin_simp\n"), 0o600)
}

// engine 串行化全部 librime 调用（librime 全局状态非线程安全）。
type engine struct {
	mu       sync.Mutex
	api      *rime.Api
	sessions map[uint32]uint32 // proto id -> rime session
	cursors  map[uint32]cursor
	nextID   uint32
	subs     map[net.Conn]*json.Encoder
	subMu    sync.Mutex
}

func (e *engine) uiConnected() bool {
	e.subMu.Lock()
	defer e.subMu.Unlock()
	return len(e.subs) > 0
}

// broadcast 必须在持有 e.mu 时调用（读 rime 状态）。
func (e *engine) broadcast(sess uint32, ctx rime.Context) {
	e.subMu.Lock()
	defer e.subMu.Unlock()
	if len(e.subs) == 0 {
		return
	}
	msg := stateMsg{
		Type: "state", Session: sess,
		Preedit: ctx.Preedit, Candidates: ctx.Candidates,
		Highlight: ctx.Highlight, Page: ctx.Page, PageSize: ctx.PageSize,
		IsLastPage: ctx.IsLastPage,
		Visible:    ctx.Preedit != "" || len(ctx.Candidates) > 0,
		Cursor:     e.cursors[sess],
	}
	for _, enc := range e.subs {
		_ = enc.Encode(msg) // 写失败由订阅连接自己的读循环清理
	}
}

func (e *engine) handle(req *request, conn net.Conn) response {
	e.mu.Lock()
	defer e.mu.Unlock()

	switch req.Op {
	case "subscribe":
		e.subMu.Lock()
		e.subs[conn] = json.NewEncoder(conn)
		e.subMu.Unlock()
		return response{}
	case "unsubscribe":
		e.subMu.Lock()
		delete(e.subs, conn)
		e.subMu.Unlock()
		return response{}
	case "open":
		sid := e.api.CreateSession()
		if sid == 0 {
			return response{Error: "create_session failed"}
		}
		e.nextID++
		e.sessions[e.nextID] = sid
		return response{Session: e.nextID}
	case "close":
		if sid, ok := e.sessions[req.Session]; ok {
			e.api.DestroySession(sid)
			delete(e.sessions, req.Session)
			delete(e.cursors, req.Session)
		}
		return response{}
	case "reset":
		if sid, ok := e.sessions[req.Session]; ok {
			e.api.ClearComposition(sid)
			e.broadcast(req.Session, e.api.GetContext(sid))
		}
		return response{}
	case "ui": // 模块上报光标位置
		e.cursors[req.Session] = cursor{req.X, req.Y, req.W, req.H, req.Scale}
		if sid, ok := e.sessions[req.Session]; ok {
			e.broadcast(req.Session, e.api.GetContext(sid))
		}
		return response{}
	case "select":
		sid, ok := e.sessions[req.Session]
		if !ok {
			return response{Error: "no such session"}
		}
		handled := e.api.SelectCandidate(sid, req.Index)
		commit := e.api.CommitText(sid)
		ctx := e.api.GetContext(sid)
		e.broadcast(req.Session, ctx)
		return response{Handled: handled, Commit: commit, UIConnected: e.uiConnected()}
	case "key":
		sid, ok := e.sessions[req.Session]
		if !ok {
			return response{Error: "no such session"}
		}
		handled := e.api.ProcessKey(sid, req.Keysym, req.Mods)
		ctx := e.api.GetContext(sid)
		e.broadcast(req.Session, ctx)
		return response{
			Handled: handled, Commit: e.api.CommitText(sid),
			Preedit: ctx.Preedit, Candidates: ctx.Candidates,
			Page: ctx.Page, PageSize: ctx.PageSize,
			IsLastPage: ctx.IsLastPage, Highlight: ctx.Highlight,
			UIConnected: e.uiConnected(),
		}
	default:
		return response{Error: "unknown op: " + req.Op}
	}
}

// spawnUI 拉起 MyGo 候选窗（继承本进程会话环境）。
// GNOME Wayland 不支持 layer-shell，候选窗会退化成普通窗——跳过，
// 让模块回退 fcitx5 内置面板（无订阅者时自动如此）。
func spawnUI() {
	if os.Getenv("XDG_SESSION_TYPE") == "wayland" &&
		strings.Contains(os.Getenv("XDG_CURRENT_DESKTOP"), "GNOME") {
		fmt.Fprintln(os.Stderr, "GNOME Wayland: 候选窗退化为普通窗，跳过 uiapp（用内置面板）")
		return
	}
	for _, path := range []string{"/usr/libexec/tomato-ime-ui", "/usr/lib/tomato-ime/tomato-ime-ui"} {
		if _, err := os.Stat(path); err == nil {
			cmd := exec.Command(path)
			cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true} // 脱离 daemon 进程组
			cmd.Stdout, cmd.Stderr = nil, nil
			if err := cmd.Start(); err == nil {
				fmt.Fprintln(os.Stderr, "spawned uiapp", path)
			}
			return
		}
	}
}

func main() {
	home, err := os.UserHomeDir()
	if err != nil {
		log.Fatalf("home: %v", err)
	}
	userDir := filepath.Join(home, ".local", "share", "tomato-ime", "rime")
	if err := ensureUserConfig(userDir); err != nil {
		log.Fatalf("bootstrap user config: %v", err)
	}
	api, err := rime.Setup("/usr/share/rime-data", userDir, "tomato-ime")
	if err != nil {
		log.Fatalf("rime setup: %v", err)
	}
	defer api.Finalize()

	path := socketPath()
	// 已有实例在跑则直接退出（模块会连上旧实例）
	if _, err := os.Stat(path); err == nil {
		if c, derr := net.Dial("unix", path); derr == nil {
			c.Close()
			fmt.Fprintln(os.Stderr, "another daemon already owns", path)
			return
		}
	}
	_ = os.Remove(path)
	ln, err := net.Listen("unix", path)
	if err != nil {
		log.Fatalf("listen %s: %v", path, err)
	}
	defer ln.Close()
	_ = os.Chmod(path, 0o600)
	fmt.Fprintln(os.Stderr, "tomato-ime daemon listening on", path)

	eng := &engine{api: api, sessions: map[uint32]uint32{}, cursors: map[uint32]cursor{}, subs: map[net.Conn]*json.Encoder{}}

	spawnUI()

	for {
		conn, err := ln.Accept()
		if err != nil {
			log.Printf("accept: %v", err)
			continue
		}
		go serve(eng, conn)
	}
}

func serve(eng *engine, conn net.Conn) {
	defer func() {
		eng.subMu.Lock()
		delete(eng.subs, conn)
		eng.subMu.Unlock()
		conn.Close()
	}()
	sc := bufio.NewScanner(conn)
	sc.Buffer(make([]byte, 64*1024), 1024*1024)
	enc := json.NewEncoder(conn)
	for sc.Scan() {
		var req request
		if err := json.Unmarshal(sc.Bytes(), &req); err != nil {
			enc.Encode(response{Error: "bad json"})
			continue
		}
		// 订阅连接会收到广播推送，写锁避免交叠
		if req.Op == "subscribe" || req.Op == "unsubscribe" {
			eng.handle(&req, conn)
			enc.Encode(response{})
			continue
		}
		if err := enc.Encode(eng.handle(&req, conn)); err != nil {
			return
		}
	}
}
