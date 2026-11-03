// tomato-ime 引擎守护进程（M2）：独占 librime，fcitx5 模块经 unix socket 转发按键。
//
// 协议：$XDG_RUNTIME_DIR/tomato-ime.sock 上的换行分隔 JSON（请求→响应一问一答）。
//   请求  {"op":"open"} | {"op":"close","session":N} | {"op":"reset","session":N}
//         {"op":"key","session":N,"keysym":112,"mods":0}
//   响应  open: {"session":N}；key: {"handled":…,"commit":…,"preedit":…,"candidates":[…],…}
// 模块在连接失败时会拉起本进程（--daemon 由调用方负责，本进程只管前台跑）。
package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"log"
	"net"
	"os"
	"path/filepath"
	"sync"

	"tomato-ime/daemon/rime"
)

type request struct {
	Op      string `json:"op"`
	Session uint32 `json:"session"`
	Keysym  int    `json:"keysym"`
	Mods    uint   `json:"mods"`
}

type response struct {
	Session    uint32          `json:"session,omitempty"`
	Handled    bool            `json:"handled"`
	Commit     string          `json:"commit,omitempty"`
	Preedit    string          `json:"preedit,omitempty"`
	Candidates []rime.Candidate `json:"candidates,omitempty"`
	Page       int             `json:"page"`
	PageSize   int             `json:"page_size"`
	IsLastPage bool            `json:"is_last_page"`
	Highlight  int             `json:"highlight"`
	Error      string          `json:"error,omitempty"`
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
	return os.WriteFile(p, []byte("config_version: \"1.0\"\nschema_list:\n  - schema: luna_pinyin_simp\n"), 0o644)
}

// engine 串行化全部 librime 调用（librime 全局状态非线程安全）。
type engine struct {
	mu       sync.Mutex
	api      *rime.Api
	sessions map[uint32]uint32 // proto id -> rime session
	nextID   uint32
}

func (e *engine) handle(req *request) response {
	e.mu.Lock()
	defer e.mu.Unlock()

	switch req.Op {
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
		}
		return response{}
	case "reset":
		if sid, ok := e.sessions[req.Session]; ok {
			e.api.ClearComposition(sid)
		}
		return response{}
	case "key":
		sid, ok := e.sessions[req.Session]
		if !ok {
			return response{Error: "no such session"}
		}
		handled := e.api.ProcessKey(sid, req.Keysym, req.Mods)
		ctx := e.api.GetContext(sid)
		return response{
			Handled:    handled,
			Commit:     e.api.CommitText(sid),
			Preedit:    ctx.Preedit,
			Candidates: ctx.Candidates,
			Page:       ctx.Page,
			PageSize:   ctx.PageSize,
			IsLastPage: ctx.IsLastPage,
			Highlight:  ctx.Highlight,
		}
	default:
		return response{Error: "unknown op: " + req.Op}
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

	eng := &engine{api: api, sessions: map[uint32]uint32{}}

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
	defer conn.Close()
	sc := bufio.NewScanner(conn)
	sc.Buffer(make([]byte, 64*1024), 1024*1024)
	enc := json.NewEncoder(conn)
	for sc.Scan() {
		var req request
		if err := json.Unmarshal(sc.Bytes(), &req); err != nil {
			enc.Encode(response{Error: "bad json"})
			continue
		}
		if err := enc.Encode(eng.handle(&req)); err != nil {
			return
		}
	}
}
