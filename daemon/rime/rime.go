// Package rime 是 librime C API 的最小 cgo 绑定（M2：引擎守护进程用）。
// librime 的入口是 rime_get_api() 返回的函数指针表；cgo 不能直接调用
// 结构体函数指针，因此在前导区写 C 转发包装。
package rime

/*
#cgo pkg-config: rime
#include <rime_api.h>
#include <stdlib.h>

static void tr_setup(RimeApi* a, RimeTraits* t) { a->setup(t); }
static void tr_initialize(RimeApi* a, RimeTraits* t) { a->initialize(t); }
static void tr_finalize(RimeApi* a) { a->finalize(); }
static void tr_start_maintenance(RimeApi* a, Bool foreground) { a->start_maintenance(foreground); }
static RimeSessionId tr_create_session(RimeApi* a) { return a->create_session(); }
static void tr_destroy_session(RimeApi* a, RimeSessionId s) { a->destroy_session(s); }
static void tr_clear_composition(RimeApi* a, RimeSessionId s) { a->clear_composition(s); }
static Bool tr_select_candidate(RimeApi* a, RimeSessionId s, size_t i) {
	return a->select_candidate(s, i);
}
static Bool tr_process_key(RimeApi* a, RimeSessionId s, int k, int m) {
	return a->process_key(s, k, m);
}
static Bool tr_select_schema(RimeApi* a, RimeSessionId s, const char* id) {
	return a->select_schema(s, id);
}
static void tr_set_option(RimeApi* a, RimeSessionId s, const char* n, Bool v) {
	a->set_option(s, n, v);
}
static Bool tr_get_commit(RimeApi* a, RimeSessionId s, RimeCommit* c) { return a->get_commit(s, c); }
static void tr_free_commit(RimeApi* a, RimeCommit* c) { a->free_commit(c); }
static void tr_get_context(RimeApi* a, RimeSessionId s, RimeContext* c) { a->get_context(s, c); }
static void tr_free_context(RimeApi* a, RimeContext* c) { a->free_context(c); }
*/
import "C"

import (
	"errors"
	"unsafe"
)

type Api struct{ p *C.RimeApi }

type Context struct {
	Preedit    string
	Candidates []Candidate
	Page       int
	PageSize   int
	IsLastPage bool
	Highlight  int
}

type Candidate struct {
	Text    string `json:"text"`
	Comment string `json:"comment,omitempty"`
}

func Setup(sharedDataDir, userDataDir, appName string) (*Api, error) {
	api := C.rime_get_api()
	if api == nil {
		return nil, errors.New("rime_get_api returned nil")
	}
	var traits C.RimeTraits
	// setup() 不填 data_size；为 0 时 librime 忽略全部 traits 字段（必须手动置）
	traits.data_size = C.int(unsafe.Sizeof(traits))
	C.tr_setup(api, &traits)
	cShared := C.CString(sharedDataDir)
	cUser := C.CString(userDataDir)
	cApp := C.CString(appName)
	defer C.free(unsafe.Pointer(cShared))
	defer C.free(unsafe.Pointer(cUser))
	defer C.free(unsafe.Pointer(cApp))
	traits.shared_data_dir = cShared
	traits.user_data_dir = cUser
	traits.app_name = cApp
	C.tr_initialize(api, &traits)
	C.tr_start_maintenance(api, 1)
	return &Api{p: api}, nil
}

func (a *Api) Finalize() { C.tr_finalize(a.p) }

func (a *Api) CreateSession() uint32 {
	return uint32(C.tr_create_session(a.p))
}

func (a *Api) DestroySession(sid uint32) {
	C.tr_destroy_session(a.p, C.RimeSessionId(sid))
}

func (a *Api) ClearComposition(sid uint32) {
	C.tr_clear_composition(a.p, C.RimeSessionId(sid))
}

// SelectCandidate 按当前页内 0 基下标选中候选（候选窗鼠标点击用）。
func (a *Api) SelectCandidate(sid uint32, index int) bool {
	return C.tr_select_candidate(a.p, C.RimeSessionId(sid), C.size_t(index)) == C.True
}

// ProcessKey 送入一个 X keysym + Rime 修饰掩码，返回是否被引擎消费。
func (a *Api) ProcessKey(sid uint32, keysym int, mods uint) bool {
	return C.tr_process_key(a.p, C.RimeSessionId(sid), C.int(keysym), C.int(mods)) == C.True
}

// SelectSchema 切换输入方案（须已在 schema_list 中）。
func (a *Api) SelectSchema(sid uint32, id string) bool {
	c := C.CString(id)
	defer C.free(unsafe.Pointer(c))
	return C.tr_select_schema(a.p, C.RimeSessionId(sid), c) == C.True
}

// SetOption 设置会话选项（ascii_mode / full_shape / simplification 等）。
func (a *Api) SetOption(sid uint32, name string, value bool) {
	c := C.CString(name)
	defer C.free(unsafe.Pointer(c))
	var v C.Bool = C.False
	if value {
		v = C.True
	}
	C.tr_set_option(a.p, C.RimeSessionId(sid), c, v)
}

func (a *Api) CommitText(sid uint32) string {
	var commit C.RimeCommit
	commit.data_size = C.int(unsafe.Sizeof(commit))
	if C.tr_get_commit(a.p, C.RimeSessionId(sid), &commit) != C.True {
		return ""
	}
	text := C.GoString(commit.text)
	C.tr_free_commit(a.p, &commit)
	return text
}

func (a *Api) GetContext(sid uint32) Context {
	var ctx C.RimeContext
	ctx.data_size = C.int(unsafe.Sizeof(ctx))
	C.tr_get_context(a.p, C.RimeSessionId(sid), &ctx)
	defer C.tr_free_context(a.p, &ctx)

	out := Context{
		Page:       int(ctx.menu.page_no),
		PageSize:   int(ctx.menu.page_size),
		IsLastPage: ctx.menu.is_last_page == C.True,
		Highlight:  int(ctx.menu.highlighted_candidate_index),
	}
	if ctx.composition.preedit != nil {
		out.Preedit = C.GoString(ctx.composition.preedit)
	}
	if ctx.menu.candidates != nil {
		slice := unsafe.Slice(ctx.menu.candidates, int(ctx.menu.num_candidates))
		for _, c := range slice {
			cand := Candidate{Text: C.GoString(c.text)}
			if c.comment != nil {
				cand.Comment = C.GoString(c.comment)
			}
			out.Candidates = append(out.Candidates, cand)
		}
	}
	return out
}
