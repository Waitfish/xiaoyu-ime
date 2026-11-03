// xiaoyu-ime 引擎守护进程（M3）：独占 librime；fcitx5 模块转发按键，
// MyGo 候选窗（xiaoyu-ime-ui）订阅状态推送。中枢模型：
//
//   fcitx5 模块 ──key/ui/select──► daemon ──state 广播──► 候选窗（订阅者）
//
// 协议：$XDG_RUNTIME_DIR/xiaoyu-ime.sock 换行分隔 JSON。
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

	"xiaoyu-ime/daemon/rime"
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
	return filepath.Join(dir, "xiaoyu-ime.sock")
}

// ensureUserConfig 首次运行写入方案列表补丁（default.custom.yaml）。
// 不能用精简 default.yaml：方案会 __include default:/key_binder 等节，
// 精简版会让这些 config 构建失败（wubi86/pinyin_simp 报 error building config）。
// 注意 librime 会把缺 config_version 的用户配置移进 trash/。
func ensureUserConfig(dir string) error {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	p := filepath.Join(dir, "default.custom.yaml")
	if _, err := os.Stat(p); err == nil {
		return nil
	}
	return os.WriteFile(p, []byte("patch:\n  schema_list:\n    - schema: luna_pinyin_simp\n    - schema: double_pinyin_flypy\n    - schema: wubi86\n"), 0o600)
}

// config 应用级配置（~/.config/xiaoyu-ime/config.json）：
// 输入方案 + 默认输入状态。设置程序（后续）写它，daemon 每次 open 会话时应用。
type config struct {
	Schema         string `json:"schema"`          // luna_pinyin_simp | double_pinyin_flypy | wubi86
	AsciiMode      *bool  `json:"ascii_mode"`      // 默认中文(false)
	Simplification *bool  `json:"simplification"`  // 默认简体(true)
	FullShape      *bool  `json:"full_shape"`      // 默认半角(false)
}

var allowedSchemas = map[string]bool{
	"luna_pinyin_simp":   true, // 全拼（简体）
	"double_pinyin_flypy": true, // 双拼-小鹤
	"wubi86":             true, // 五笔 86
}

func loadConfig() config {
	cfg := config{Schema: "luna_pinyin_simp"}
	home, err := os.UserHomeDir()
	if err == nil {
		if b, err := os.ReadFile(filepath.Join(home, ".config", "xiaoyu-ime", "config.json")); err == nil {
			_ = json.Unmarshal(b, &cfg)
		}
	}
	if !allowedSchemas[cfg.Schema] {
		cfg.Schema = "luna_pinyin_simp"
	}
	return cfg
}

// applyConfig 在新会话上应用方案与默认状态。必须在持有 e.mu 时调用。
// 注意：librime 的 select_schema 对"当前默认方案"返回 false 且会把会话置成
// 无活动方案（按键直通失效）——默认方案（schema_list 第一项）跳过 select。
const defaultSchema = "luna_pinyin_simp"

func (e *engine) applyConfig(sid uint32) {
	if e.cfg.Schema != defaultSchema && !e.api.SelectSchema(sid, e.cfg.Schema) {
		fmt.Fprintln(os.Stderr, "select_schema failed:", e.cfg.Schema)
	}
	ascii := false
	if e.cfg.AsciiMode != nil {
		ascii = *e.cfg.AsciiMode
	}
	e.api.SetOption(sid, "ascii_mode", ascii)
	simp := true
	if e.cfg.Simplification != nil {
		simp = *e.cfg.Simplification
	}
	// 简繁开关的 rime 选项名因方案而异：luna_pinyin_simp 用 zh_simp，
	// luna/double_pinyin 用 simplification——两个都设，无副作用。
	e.api.SetOption(sid, "simplification", simp)
	e.api.SetOption(sid, "zh_simp", simp)
	full := false
	if e.cfg.FullShape != nil {
		full = *e.cfg.FullShape
	}
	e.api.SetOption(sid, "full_shape", full)
}

// engine 串行化全部 librime 调用（librime 全局状态非线程安全）。
type engine struct {
	mu       sync.Mutex
	api      *rime.Api
	ready    bool // rime 初始化完成（listen 先于 setup，open 在就绪前返回错误）
	cfg      config
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
		if !e.ready {
			return response{Error: "rime not ready"}
		}
		sid := e.api.CreateSession()
		if sid == 0 {
			return response{Error: "create_session failed"}
		}
		e.nextID++
		e.sessions[e.nextID] = sid
		e.applyConfig(sid)
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
	for _, path := range []string{"/usr/libexec/xiaoyu-ime-ui", "/usr/lib/xiaoyu-ime/xiaoyu-ime-ui"} {
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
	userDir := filepath.Join(home, ".local", "share", "xiaoyu-ime", "rime")
	if err := ensureUserConfig(userDir); err != nil {
		log.Fatalf("bootstrap user config: %v", err)
	}

	// 先 listen 再做 rime 初始化：模块 activate 时能连上；
	// 就绪前 open 返回错误，模块会在下一次按键时懒重试（见 xiaoyu_engine.cpp）。
	path := socketPath()
	// 单例用 lock 文件上的 flock（原子，无竞态）。
	// 旧的 "stat+dial" 检查在两个实例同时启动时会互相抢 socket，
	// 出现两个 librime 实例共用用户目录 → 部署状态损坏 → 所有会话按键直通。
	lockF, err := os.OpenFile(path+".lock", os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		log.Fatalf("lock file: %v", err)
	}
	if err := syscall.Flock(int(lockF.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		fmt.Fprintln(os.Stderr, "another daemon already owns", path)
		return
	}
	_ = os.Remove(path)
	ln, err := net.Listen("unix", path)
	if err != nil {
		log.Fatalf("listen %s: %v", path, err)
	}
	defer ln.Close()
	_ = os.Chmod(path, 0o600)
	fmt.Fprintln(os.Stderr, "xiaoyu-ime daemon listening on", path)

	eng := &engine{sessions: map[uint32]uint32{}, cursors: map[uint32]cursor{}, subs: map[net.Conn]*json.Encoder{}, cfg: loadConfig()}

	go func() {
		api, err := rime.Setup("/usr/share/rime-data", userDir, "xiaoyu-ime")
		if err != nil {
			log.Fatalf("rime setup: %v", err)
		}
		eng.mu.Lock()
		eng.api = api
		eng.ready = true
		eng.mu.Unlock()
		fmt.Fprintln(os.Stderr, "rime ready")
	}()

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
