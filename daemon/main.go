// tomato-ime 引擎守护进程（M2 骨架，M1 尚未接入）。
//
// 协议：$XDG_RUNTIME_DIR/tomato-ime.sock 上的换行分隔 JSON。
// 请求  {"session":1,"keysym":97,"mods":0}
// 响应  {"handled":true,"commit":"","preedit":"…","candidates":[…],"page":…}
package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"log"
	"net"
	"os"
	"path/filepath"

	"tomato-ime/daemon/rime"
)

type request struct {
	Session uint32 `json:"session"`
	Keysym  int    `json:"keysym"`
	Mods    uint   `json:"mods"`
}

type response struct {
	Handled    bool            `json:"handled"`
	Commit     string          `json:"commit,omitempty"`
	Preedit    string          `json:"preedit,omitempty"`
	Candidates []rime.Candidate `json:"candidates,omitempty"`
	Page       int             `json:"page"`
	PageSize   int             `json:"page_size"`
	IsLastPage bool            `json:"is_last_page"`
	Highlight  int             `json:"highlight"`
}

func socketPath() string {
	dir := os.Getenv("XDG_RUNTIME_DIR")
	if dir == "" {
		dir = os.TempDir()
	}
	return filepath.Join(dir, "tomato-ime.sock")
}

func main() {
	home, _ := os.UserHomeDir()
	api, err := rime.Setup("/usr/share/rime-data",
		filepath.Join(home, ".local/share/tomato-ime/rime"), "tomato-ime")
	if err != nil {
		log.Fatalf("rime setup: %v", err)
	}
	defer api.Finalize()

	path := socketPath()
	_ = os.Remove(path)
	ln, err := net.Listen("unix", path)
	if err != nil {
		log.Fatalf("listen %s: %v", path, err)
	}
	defer ln.Close()
	fmt.Fprintln(os.Stderr, "tomato-ime daemon listening on", path)

	for {
		conn, err := ln.Accept()
		if err != nil {
			log.Printf("accept: %v", err)
			continue
		}
		go serve(api, conn)
	}
}

func serve(api *rime.Api, conn net.Conn) {
	defer conn.Close()
	sc := bufio.NewScanner(conn)
	enc := json.NewEncoder(conn)
	sessions := map[uint32]uint32{} // proto id -> rime session
	for sc.Scan() {
		var req request
		if err := json.Unmarshal(sc.Bytes(), &req); err != nil {
			continue
		}
		sid, ok := sessions[req.Session]
		if !ok {
			sid = api.CreateSession()
			sessions[req.Session] = sid
		}
		handled := api.ProcessKey(sid, req.Keysym, req.Mods)
		ctx := api.GetContext(sid)
		_ = enc.Encode(response{
			Handled:    handled,
			Commit:     api.CommitText(sid),
			Preedit:    ctx.Preedit,
			Candidates: ctx.Candidates,
			Page:       ctx.Page,
			PageSize:   ctx.PageSize,
			IsLastPage: ctx.IsLastPage,
			Highlight:  ctx.Highlight,
		})
	}
	for _, sid := range sessions {
		api.DestroySession(sid)
	}
}
