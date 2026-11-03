// 小鱼输入法 IBus 引擎（Go，无 cgo）。
// 架构：ibus-daemon →(私有 D-Bus)→ 本进程（Engine 实现）→(unix socket)→ xiaoyu-ime-daemon（librime）。
// 协议依据 ibus 1.5.34 源码（src/ibusengine.c introspection XML、src/ibuscomponent.c、
// src/ibusenginedesc.c、src/ibuslookuptable.c、src/ibustext.c 的 serialize 字段顺序）。
package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/godbus/dbus/v5"
)

const (
	ibusService   = "org.freedesktop.IBus"
	ibusPath      = "/org/freedesktop/IBus"
	factoryPath   = "/org/freedesktop/IBus/Factory"
	factoryIface  = "org.freedesktop.IBus.Factory"
	engineIface   = "org.freedesktop.IBus.Engine"
	engineVersion = "0.1.0"
)

// ---------- daemon socket 客户端（与 fcitx5 模块同一协议） ----------

type keyResponse struct {
	Session     uint32 `json:"session"`
	Handled     bool   `json:"handled"`
	Commit      string `json:"commit"`
	Preedit     string `json:"preedit"`
	Candidates  []struct {
		Text    string `json:"text"`
		Comment string `json:"comment"`
	} `json:"candidates"`
	Highlight   int  `json:"highlight"`
	PageSize    int  `json:"page_size"`
	UIConnected bool `json:"ui_connected"`
	Error       string `json:"error"`
}

type client struct {
	mu sync.Mutex
	c  net.Conn
	enc *json.Encoder
	dec *json.Decoder
	sess uint32
}

func daemonSocket() string {
	dir := os.Getenv("XDG_RUNTIME_DIR")
	if dir == "" {
		dir = os.TempDir()
	}
	return filepath.Join(dir, "xiaoyu-ime.sock")
}

func spawnDaemon() {
	for _, p := range []string{"/usr/libexec/xiaoyu-ime-daemon", "/usr/lib/xiaoyu-ime/xiaoyu-ime-daemon"} {
		if _, err := os.Stat(p); err == nil {
			cmd := exec.Command(p)
			cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
			_ = cmd.Start()
			return
		}
	}
}

// ensure 连接 daemon（必要时拉起），失败重试。
func (cl *client) ensure() error {
	cl.mu.Lock()
	defer cl.mu.Unlock()
	if cl.c != nil {
		return nil
	}
	path := daemonSocket()
	for i := 0; i < 8; i++ {
		if i == 3 {
			spawnDaemon()
		}
		c, err := net.Dial("unix", path)
		if err == nil {
			cl.c = c
			cl.enc = json.NewEncoder(c)
			cl.dec = json.NewDecoder(c)
			return nil
		}
		time.Sleep(150 * time.Millisecond)
	}
	return fmt.Errorf("cannot reach xiaoyu-ime daemon at %s", path)
}

func (cl *client) call(req map[string]any) (keyResponse, error) {
	cl.mu.Lock()
	defer cl.mu.Unlock()
	if cl.c == nil {
		return keyResponse{}, fmt.Errorf("not connected")
	}
	if err := cl.enc.Encode(req); err != nil {
		cl.c.Close()
		cl.c = nil
		cl.sess = 0 // 连接断了：下次 open 重建会话
		return keyResponse{}, err
	}
	var resp keyResponse
	if err := cl.dec.Decode(&resp); err != nil {
		cl.c.Close()
		cl.c = nil
		cl.sess = 0
		return keyResponse{}, err
	}
	return resp, nil
}

// open 懒创建会话（daemon 未就绪时下次按键再试；连接断开后自动重建）。
func (cl *client) open() error {
	if cl.sess != 0 && cl.c != nil {
		return nil
	}
	if err := cl.ensure(); err != nil {
		return err
	}
	resp, err := cl.call(map[string]any{"op": "open"})
	if err != nil {
		return err
	}
	if resp.Error != "" {
		return errors.New(resp.Error)
	}
	cl.sess = resp.Session
	return nil
}

// ---------- IBus 序列化（字段顺序 = ibus 1.5.34 serialize 源码） ----------
// godbus 把 Go struct 按字段顺序编码为 D-Bus 元组（[]interface{} 会编成 av，不可用）。

type ibusProps = map[string]dbus.Variant

type attrListT struct {
	Type  string
	Props ibusProps
	Attrs []dbus.Variant
}

type textT struct {
	Type  string
	Props ibusProps
	Text  string
	Attrs dbus.Variant
}

type lookupTableT struct {
	Type          string
	Props         ibusProps
	PageSize      uint32
	CursorPos     uint32
	CursorVisible bool
	Round         bool
	Orientation   int32
	Candidates    []dbus.Variant
	Labels        []dbus.Variant
}

type engineDescT struct {
	Type           string
	Props          ibusProps
	Name           string
	LongName       string
	Description    string
	Language       string
	License        string
	Author         string
	Icon           string
	Layout         string
	Rank           uint32
	Hotkeys        string
	Symbol         string
	Setup          string
	LayoutVariant  string
	LayoutOption   string
	Version        string
	Textdomain     string
	IconPropKey    string
}

type componentT struct {
	Type     string
	Props    ibusProps
	Name     string
	Description string
	Version  string
	License  string
	Author   string
	Homepage string
	Exec     string
	Textdomain string
	Observed []dbus.Variant
	Engines  []dbus.Variant
}

func props() ibusProps { return ibusProps{} }

func attrList() dbus.Variant {
	return dbus.MakeVariant(attrListT{"IBusAttrList", props(), []dbus.Variant{}})
}

func ibusText(s string) dbus.Variant {
	return dbus.MakeVariant(textT{"IBusText", props(), s, attrList()})
}

func lookupTable(pageSize, cursorPos int, cands, labels []string) dbus.Variant {
	cv := make([]dbus.Variant, 0, len(cands))
	for _, c := range cands {
		cv = append(cv, ibusText(c))
	}
	lv := make([]dbus.Variant, 0, len(labels))
	for _, l := range labels {
		lv = append(lv, ibusText(l))
	}
	return dbus.MakeVariant(lookupTableT{
		"IBusLookupTable", props(),
		uint32(pageSize), uint32(cursorPos), true, false, int32(0),
		cv, lv,
	})
}

func engineDesc() dbus.Variant {
	return dbus.MakeVariant(engineDescT{
		"IBusEngineDesc", props(),
		"xiaoyu",              // name
		"小鱼输入法",            // longname
		"小鱼输入法（Rime：全拼/小鹤双拼/五笔）", // description
		"zh",                  // language
		"GPL-3.0-or-later",    // license
		"Waitfish",            // author
		"xiaoyu-input-method", // icon
		"",                    // layout
		uint32(999),           // rank
		"",                    // hotkeys
		"xiaoyu-input-method", // symbol
		"",                    // setup
		"",                    // layout_variant
		"",                    // layout_option
		engineVersion,         // version
		"",                    // textdomain
		"",                    // icon_prop_key
	})
}

func component() dbus.Variant {
	return dbus.MakeVariant(componentT{
		"IBusComponent", props(),
		"xiaoyu", "小鱼输入法（Rime 引擎）", engineVersion,
		"GPL-3.0-or-later", "Waitfish",
		"https://github.com/Waitfish/xiaoyu-ime",
		"/usr/libexec/xiaoyu-ime-ibus", // exec
		"",                             // textdomain
		[]dbus.Variant{},               // observed paths
		[]dbus.Variant{engineDesc()},   // engines
	})
}

// ---------- 引擎对象（每个输入上下文一个） ----------

type engineObj struct {
	conn *dbus.Conn
	path dbus.ObjectPath
	cl   client
	mu   sync.Mutex
}

func (e *engineObj) emit(name string, args ...interface{}) {
	if err := e.conn.Emit(e.path, engineIface+"."+name, args...); err != nil {
		log.Println("emit", name, ":", err)
	}
}

// update 把 daemon 响应映射为 IBus 的 preedit + lookup table 更新。
func (e *engineObj) update(resp keyResponse) {
	runes := []rune(resp.Preedit)
	e.emit("UpdatePreeditText", ibusText(resp.Preedit), uint32(len(runes)), resp.Preedit != "", uint32(0))

	texts := make([]string, 0, len(resp.Candidates))
	comments := make([]string, 0, len(resp.Candidates))
	for _, c := range resp.Candidates {
		t := c.Text
		if c.Comment != "" {
			t += " " + c.Comment
		}
		texts = append(texts, t)
		comments = append(comments, t)
	}
	labels := make([]string, 0, len(texts))
	for i := range texts {
		if i < 9 {
			labels = append(labels, strconv.Itoa(i+1))
		} else {
			labels = append(labels, "")
		}
	}
	pageSize := resp.PageSize
	if pageSize == 0 {
		pageSize = 5
	}
	// MyGo 候选窗在线时（非 GNOME 会话）由它显示候选，IBus 面板不重复显示
	visible := len(texts) > 0 && !resp.UIConnected
	e.emit("UpdateLookupTable", lookupTable(pageSize, resp.Highlight, comments, labels), visible)
}

func (e *engineObj) ProcessKeyEvent(keyval, keycode, state uint32) (bool, *dbus.Error) {
	if err := e.cl.open(); err != nil {
		log.Println("open:", err)
		return false, nil // 引擎未就绪：直通英文，下次按键再试
	}
	resp, err := e.cl.call(map[string]any{
		"op": "key", "session": e.cl.sess,
		"keysym": int(keyval), "mods": uint(state),
	})
	if err != nil {
		log.Println("key:", err)
		return false, nil
	}
	if resp.Commit != "" {
		e.emit("CommitText", ibusText(resp.Commit))
	}
	e.update(resp)
	return resp.Handled, nil
}

// sendKey 把 IBus 的翻页/光标方法翻译成 rime 按键。
func (e *engineObj) sendKey(keyval uint32) {
	if e.cl.sess == 0 {
		return
	}
	resp, err := e.cl.call(map[string]any{
		"op": "key", "session": e.cl.sess, "keysym": int(keyval), "mods": uint(0),
	})
	if err != nil {
		return
	}
	if resp.Commit != "" {
		e.emit("CommitText", ibusText(resp.Commit))
	}
	e.update(resp)
}

func (e *engineObj) PageUp() *dbus.Error            { e.sendKey(0xff55); return nil }
func (e *engineObj) PageDown() *dbus.Error           { e.sendKey(0xff56); return nil }
func (e *engineObj) CursorUp() *dbus.Error           { e.sendKey(0xff52); return nil }
func (e *engineObj) CursorDown() *dbus.Error         { e.sendKey(0xff54); return nil }
func (e *engineObj) SetCapabilities(caps uint32) *dbus.Error { return nil }
func (e *engineObj) PropertyActivate(name string, state uint32) *dbus.Error { return nil }
func (e *engineObj) PropertyShow(name string) *dbus.Error    { return nil }
func (e *engineObj) PropertyHide(name string) *dbus.Error    { return nil }
func (e *engineObj) ProcessHandWritingEvent(coords []float64) *dbus.Error   { return nil }
func (e *engineObj) CancelHandWriting(n uint32) *dbus.Error  { return nil }
func (e *engineObj) SetSurroundingText(text dbus.Variant, cursorPos, anchorPos uint32) *dbus.Error {
	return nil
}

func (e *engineObj) SetCursorLocation(x, y, w, h int32) *dbus.Error {
	if e.cl.sess == 0 {
		return nil
	}
	_, _ = e.cl.call(map[string]any{
		"op": "ui", "session": e.cl.sess,
		"x": int(x), "y": int(y), "w": int(w), "h": int(h), "scale": 1.0,
	})
	return nil
}

func (e *engineObj) CandidateClicked(index, button, state uint32) *dbus.Error {
	if e.cl.sess == 0 {
		return nil
	}
	resp, err := e.cl.call(map[string]any{
		"op": "select", "session": e.cl.sess, "index": int(index),
	})
	if err != nil {
		return nil
	}
	if resp.Commit != "" {
		e.emit("CommitText", ibusText(resp.Commit))
	}
	e.update(resp)
	return nil
}

func (e *engineObj) FocusIn() *dbus.Error {
	if err := e.cl.open(); err != nil {
		log.Println("focusin open:", err)
	}
	return nil
}

func (e *engineObj) FocusOut() *dbus.Error {
	e.Reset()
	return nil
}

func (e *engineObj) Reset() *dbus.Error {
	if e.cl.sess != 0 {
		_, _ = e.cl.call(map[string]any{"op": "reset", "session": e.cl.sess})
	}
	e.emit("UpdatePreeditText", ibusText(""), uint32(0), false, uint32(0))
	e.emit("UpdateLookupTable", lookupTable(5, 0, nil, nil), false)
	return nil
}

func (e *engineObj) Enable() *dbus.Error  { return nil }
func (e *engineObj) Disable() *dbus.Error { return nil }

// D-Bus 属性（ContentType 写、FocusId/ActiveSurroundingText 读）
func (e *engineObj) Get(iface, name string) (dbus.Variant, *dbus.Error) {
	switch name {
	case "ContentType":
		return dbus.MakeVariant([2]uint32{0, 0}), nil
	case "FocusId", "ActiveSurroundingText":
		return dbus.MakeVariant(false), nil
	}
	return dbus.Variant{}, dbus.NewError("org.freedesktop.DBus.Error.InvalidArgs", nil)
}

func (e *engineObj) Set(iface, name string, value dbus.Variant) *dbus.Error {
	return nil // ContentType 暂不需要（密码框等仍走 rime 默认）
}

func (e *engineObj) GetAll(iface string) (map[string]dbus.Variant, *dbus.Error) {
	return map[string]dbus.Variant{
		"ContentType":            dbus.MakeVariant([2]uint32{0, 0}),
		"FocusId":                dbus.MakeVariant(false),
		"ActiveSurroundingText":  dbus.MakeVariant(false),
	}, nil
}

// ---------- 工厂与注册 ----------

type factory struct{ conn *dbus.Conn }

var engineSeq int

func (f *factory) CreateEngine(name string) (dbus.ObjectPath, *dbus.Error) {
	if name != "xiaoyu" {
		return "", dbus.NewError("org.freedesktop.IBus.Error.NotFound", nil)
	}
	engineSeq++
	path := dbus.ObjectPath(fmt.Sprintf("/org/freedesktop/IBus/Engine/%d", engineSeq))
	e := &engineObj{conn: f.conn, path: path}
	if err := f.conn.Export(e, path, engineIface); err != nil {
		log.Println("export engine:", err)
		return "", dbus.NewError("org.freedesktop.DBus.Error.NoMemory", nil)
	}
	return path, nil
}

// ibusBusAddress 找 ibus 私有总线地址：IBUS_ADDRESS 环境变量优先，
// 否则取 ~/.config/ibus/bus/ 里最新且 daemon 进程存活的文件。
func ibusBusAddress() (string, error) {
	if a := os.Getenv("IBUS_ADDRESS"); a != "" {
		return a, nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	dir := filepath.Join(home, ".config", "ibus", "bus")
	entries, err := os.ReadDir(dir)
	if err != nil {
		return "", err
	}
	var names []string
	for _, en := range entries {
		names = append(names, en.Name())
	}
	sort.Strings(names) // <hash>-unix-0 < <hash>-unix-wayland-0；再看 PID
	for i := len(names) - 1; i >= 0; i-- {
		b, err := os.ReadFile(filepath.Join(dir, names[i]))
		if err != nil {
			continue
		}
		addr, pid := "", ""
		for _, line := range strings.Split(string(b), "\n") {
			if v, ok := strings.CutPrefix(line, "IBUS_ADDRESS="); ok {
				addr = v
			}
			if v, ok := strings.CutPrefix(line, "IBUS_DAEMON_PID="); ok {
				pid = v
			}
		}
		if addr == "" {
			continue
		}
		if p, err := strconv.Atoi(pid); err == nil && p > 0 {
			if _, err := os.Stat(fmt.Sprintf("/proc/%d", p)); err != nil {
				continue // daemon 不在了
			}
		}
		return addr, nil
	}
	return "", fmt.Errorf("no live ibus bus file in %s", dir)
}

func main() {
	addr, err := ibusBusAddress()
	if err != nil {
		log.Fatalf("ibus bus: %v", err)
	}
	conn, err := dbus.Connect(addr)
	if err != nil {
		log.Fatalf("connect ibus bus: %v", err)
	}
	defer conn.Close()

	f := &factory{conn: conn}
	if err := conn.Export(f, factoryPath, factoryIface); err != nil {
		log.Fatalf("export factory: %v", err)
	}

	call := conn.Object(ibusService, ibusPath).Call(
		ibusService+".RegisterComponent", 0, component())
	if call.Err != nil {
		log.Fatalf("RegisterComponent: %v", call.Err)
	}
	fmt.Println("xiaoyu-ime-ibus registered")

	// ibus-daemon 退出 → 本引擎退出（ibus 下次会重新拉起）
	const matchRule = "type='signal',sender='org.freedesktop.DBus',path='/org/freedesktop/DBus',interface='org.freedesktop.DBus',member='NameOwnerChanged'"
	if err := conn.SignalAdd(matchRule); err != nil {
		log.Println("addmatch:", err)
	}
	ch := make(chan *dbus.Signal, 16)
	conn.Signal(ch)
	for sig := range ch {
		if len(sig.Body) == 3 {
			if name, ok := sig.Body[0].(string); ok && name == ibusService {
				if newOwner, ok := sig.Body[2].(string); ok && newOwner == "" {
					fmt.Println("ibus-daemon gone, exiting")
					return
				}
			}
		}
	}
}
