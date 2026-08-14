// Package duktape is a QuickJS-backed drop-in replacement for the subset of
// the github.com/wirenboard/go-duktape API that wb-rules uses. The public
// surface keeps Duktape's stack-machine semantics (value stack, heap stash,
// threads with fresh global environments, GoFunc callbacks, CommonJS
// require/modSearch) while executing on QuickJS (tested against Bellard's
// 2026-06-04 release, ES2023+).
//
// Semantics intentionally preserved:
//   - Context is a comparable one-pointer struct; wb-rules uses it as a map key.
//   - Native (Go) calls see a fresh stack frame containing only their args.
//   - PushThreadNewGlobalenv creates a realm (JSContext) in the shared runtime;
//     the pushed handle keeps it alive, GC of the handle frees the realm.
//   - The heap stash is one object per runtime, shared by all realms.
//   - require() caches modules per realm (per script file), like wb-rules'
//     Duktape setup; relative ids resolve against the requiring module.
package duktape

/*
#cgo CFLAGS: -D_GNU_SOURCE -I${SRCDIR}/../../third_party/quickjs -Wno-array-bounds -Wno-format-truncation
#cgo LDFLAGS: -lm -lpthread
#include <stdlib.h>
#include <string.h>
#include "shim.h"
*/
import "C"

import (
	"fmt"
	"os"
	"runtime/debug"
	"strings"
	"sync"
	"sync/atomic"
	"time"
	"unsafe"
)

// ---------------------------------------------------------------------------
// Constants (names and values mirror go-duktape)

type Type int

const (
	DUK_TYPE_NONE Type = iota
	DUK_TYPE_UNDEFINED
	DUK_TYPE_NULL
	DUK_TYPE_BOOLEAN
	DUK_TYPE_NUMBER
	DUK_TYPE_STRING
	DUK_TYPE_OBJECT
	DUK_TYPE_BUFFER
	DUK_TYPE_POINTER
)

func (t Type) IsNone() bool      { return t == DUK_TYPE_NONE }
func (t Type) IsUndefined() bool { return t == DUK_TYPE_UNDEFINED }
func (t Type) IsNull() bool      { return t == DUK_TYPE_NULL }
func (t Type) IsBool() bool      { return t == DUK_TYPE_BOOLEAN }
func (t Type) IsNumber() bool    { return t == DUK_TYPE_NUMBER }
func (t Type) IsString() bool    { return t == DUK_TYPE_STRING }
func (t Type) IsObject() bool    { return t == DUK_TYPE_OBJECT }
func (t Type) IsBuffer() bool    { return t == DUK_TYPE_BUFFER }
func (t Type) IsPointer() bool   { return t == DUK_TYPE_POINTER }

const (
	DUK_COMPILE_EVAL     uint = 1 << 3
	DUK_COMPILE_FUNCTION uint = 1 << 4

	DUK_ENUM_ARRAY_INDICES_ONLY  uint = 1 << 5
	DUK_ENUM_OWN_PROPERTIES_ONLY uint = 1 << 4

	DUK_ERR_ERROR int = 100

	DUK_RET_ERROR         = -100
	DUK_RET_TYPE_ERROR    = -105
	DUK_RET_INSTACK_ERROR = -128 // wirenboard fork extension: throw stack top
)

// ---------------------------------------------------------------------------
// Registries

type GoFunc func(d *Context) int

type enumState struct {
	obj  C.JSValue // dup'd reference to the enumerated object
	keys []string
	pos  int
	ctx  *C.JSContext
}

type ctxState struct {
	ctx    *C.JSContext
	rts    *runtimeState
	stack  []C.JSValue
	frames []int       // native call frame bases
	thisV  []C.JSValue // this-binding stack for nested native calls
}

type moduleKey struct {
	ctx *C.JSContext
	id  string
}

type runtimeState struct {
	rt         *C.JSRuntime
	primary    *C.JSContext
	execLimit  int64 // ns; 0 = no limit
	execStart  int64 // unix ns of the outermost JS entry (0 = idle)
	stash      C.JSValue
	modules    map[moduleKey]C.JSValue // require() cache per realm (Duktape: per global env)
	deadVals   []C.JSValue             // values owned by dead realms; freed at reap
	deadCtxs   []*C.JSContext          // realms whose handles were GC'd; freed at safe points
	activeCtxs []*C.JSContext          // realms currently executing JS (Duktape thread semantics)
	inJobPump  bool                    // a promise job is executing (counts as an active entry)
	jobErrFn   func(string)            // per-heap override of JobErrorHandler
}

var (
	regMu    sync.Mutex
	rtReg           = map[*C.JSRuntime]*runtimeState{}
	ctxReg          = map[*C.JSContext]*ctxState{}
	goVals          = map[uint64]interface{}{}
	enums           = map[uint64]*enumState{}
	nextID   uint64 = 1
	initOnce sync.Once
)

func registerGoVal(v interface{}) uint64 {
	regMu.Lock()
	defer regMu.Unlock()
	id := nextID
	nextID++
	goVals[id] = v
	return id
}

func stateFor(p *C.JSContext) *ctxState {
	regMu.Lock()
	defer regMu.Unlock()
	return ctxReg[p]
}

func stateForLocked(p *C.JSContext) *ctxState { return ctxReg[p] }

// ---------------------------------------------------------------------------
// Context

// Context must stay a comparable single-pointer struct: wb-rules dereferences
// it and uses the value as a map key (see ESContextFactory).
type Context struct {
	duk_context unsafe.Pointer
}

func (d *Context) c() *C.JSContext { return (*C.JSContext)(d.duk_context) }

func (d *Context) st() *ctxState {
	s := stateFor(d.c())
	if s == nil {
		panic("quickjsduk: unknown JSContext (context already destroyed?)")
	}
	// Go moves work between goroutines/OS threads; QuickJS records the C
	// stack top at JS_NewRuntime time on the creating thread and its
	// stack-overflow heuristic misfires on any other thread. Re-anchor it
	// to the current thread's stack on every API entry.
	C.JS_UpdateStackTop(s.rts.rt)
	return s
}

// NewContext creates a fresh runtime ("heap") with one primary realm.
func NewContext() *Context {
	initOnce.Do(func() { C.qjd_init_class_ids() })
	rt := C.JS_NewRuntime()
	C.qjd_register_classes(rt)
	ctx := C.qjd_new_context(rt)
	rts := &runtimeState{rt: rt, primary: ctx, modules: map[moduleKey]C.JSValue{}}
	rts.stash = C.JS_NewObject(ctx)
	C.qjd_install_require(ctx)
	regMu.Lock()
	ctxReg[ctx] = &ctxState{ctx: ctx, rts: rts}
	rtReg[rt] = rts
	regMu.Unlock()
	return &Context{unsafe.Pointer(ctx)}
}

// DestroyHeap frees the whole runtime. Not part of go-duktape's used API but
// handy for tests.
func (d *Context) DestroyHeap() {
	s := d.st()
	rts := s.rts

	// Collect everything under the lock, but free NOTHING while holding it:
	// freeing can run goobj/thread finalizers synchronously, and those
	// re-enter the registry lock.
	regMu.Lock()
	var vals []C.JSValue
	for id, es := range enums {
		if stateForLocked(es.ctx) != nil && stateForLocked(es.ctx).rts == rts {
			vals = append(vals, es.obj)
			delete(enums, id)
		}
	}
	var ctxs []*C.JSContext
	for p, cs := range ctxReg {
		if cs.rts == rts {
			vals = append(vals, cs.stack...)
			delete(ctxReg, p)
			if p != rts.primary {
				ctxs = append(ctxs, p)
			}
		}
	}
	delete(rtReg, rts.rt)
	regMu.Unlock()

	for _, v := range vals {
		C.JS_FreeValue(rts.primary, v)
	}
	for _, m := range rts.modules {
		C.JS_FreeValue(rts.primary, m)
	}
	C.JS_FreeValue(rts.primary, rts.stash)
	for _, p := range ctxs {
		C.JS_FreeContext(p)
	}
	// Frees above may have queued more realm handles / dead values.
	for {
		regMu.Lock()
		dead := rts.deadCtxs
		rts.deadCtxs = nil
		dvals := rts.deadVals
		rts.deadVals = nil
		regMu.Unlock()
		if len(dead) == 0 && len(dvals) == 0 {
			break
		}
		for _, v := range dvals {
			C.JS_FreeValue(rts.primary, v)
		}
		for _, p := range dead {
			C.JS_FreeContext(p)
		}
	}
	C.JS_FreeContext(rts.primary)
	C.JS_RunGC(rts.rt)
	C.JS_FreeRuntime(rts.rt)
}

func (rts *runtimeState) pushActive(ctx *C.JSContext) {
	regMu.Lock()
	rts.activeCtxs = append(rts.activeCtxs, ctx)
	// a nested entry from inside a promise job must not re-arm (and its pop
	// must not disarm) the watchdog: the job owns the execution window
	if len(rts.activeCtxs) == 1 && !rts.inJobPump {
		atomic.StoreInt64(&rts.execStart, time.Now().UnixNano())
	}
	regMu.Unlock()
}

func (rts *runtimeState) popActive() {
	regMu.Lock()
	rts.activeCtxs = rts.activeCtxs[:len(rts.activeCtxs)-1]
	if len(rts.activeCtxs) == 0 && !rts.inJobPump {
		atomic.StoreInt64(&rts.execStart, 0)
	}
	regMu.Unlock()
}

// SetExecutionTimeLimit bounds any single synchronous JS execution on this
// heap; exceeding it makes the engine interrupt the script with an
// InternalError instead of hanging the engine loop forever (something the
// old Duktape build could not do). Zero disables the limit.
func (d *Context) SetExecutionTimeLimit(limit time.Duration) {
	s := d.st()
	atomic.StoreInt64(&s.rts.execLimit, int64(limit))
}

//export goInterrupt
func goInterrupt(rt *C.JSRuntime) C.int {
	regMu.Lock()
	rts := rtReg[rt]
	regMu.Unlock()
	if rts == nil {
		return 0
	}
	limit := atomic.LoadInt64(&rts.execLimit)
	start := atomic.LoadInt64(&rts.execStart)
	if limit > 0 && start > 0 && time.Now().UnixNano()-start > limit {
		fmt.Fprintf(os.Stderr, "quickjsduk: interrupting JS execution after %v limit\n", time.Duration(limit))
		return 1
	}
	return 0
}

func (rts *runtimeState) currentActive(fallback *C.JSContext) *C.JSContext {
	regMu.Lock()
	defer regMu.Unlock()
	if n := len(rts.activeCtxs); n > 0 {
		return rts.activeCtxs[n-1]
	}
	return fallback
}

// JobErrorHandler receives stringified errors thrown by promise jobs
// (async rule callbacks) on heaps without their own handler; the default
// keeps them visible on stderr. Engines should install a per-heap
// handler via SetJobErrorHandler.
var JobErrorHandler = func(msg string) {
	fmt.Fprintf(os.Stderr, "quickjsduk: unhandled error in promise job: %s\n", msg)
}

// SetJobErrorHandler routes this heap's promise-job errors (async rule
// callbacks that throw or reject unhandled) to the engine's logger.
// Called on the engine goroutine; jobs pump on the same goroutine.
func (d *Context) SetJobErrorHandler(fn func(string)) {
	s := d.st()
	regMu.Lock()
	s.rts.jobErrFn = fn
	regMu.Unlock()
}

func (rts *runtimeState) reportJobError(msg string) {
	regMu.Lock()
	fn := rts.jobErrFn
	regMu.Unlock()
	if fn != nil {
		fn(msg)
		return
	}
	JobErrorHandler(msg)
}

// pumpJobs drains the QuickJS pending-job queue (promise reactions).
// Called when control returns to Go at the outermost JS entry — the moment
// an event-loop engine would run microtasks. Duktape 1.x had no promises,
// so wb-rules itself never schedules this. Callers must invoke it only
// AFTER capturing any pending exception of their own call (a throwing job
// replaces rt->current_exception).
//
// Known limitation (documented in PORT-QUICKJS.md): jobs execute with an
// empty active-context stack, so Go callbacks reached from async
// continuations dispatch to their creation realm rather than the awaiting
// file's realm — register rules/timers synchronously, not after await.
func (rts *runtimeState) pumpJobs() {
	regMu.Lock()
	depth := len(rts.activeCtxs)
	pumping := rts.inJobPump
	if depth == 0 && !pumping {
		rts.inJobPump = true
	}
	regMu.Unlock()
	if depth != 0 || pumping {
		// still inside a JS call or a job (a Go callback re-entering JS
		// returns through here at depth 0): jobs run when the true
		// outermost entry returns, preserving run-to-completion
		return
	}
	defer func() {
		regMu.Lock()
		rts.inJobPump = false
		regMu.Unlock()
	}()
	const maxJobs = 100000
	for i := 0; i < maxJobs; i++ {
		var jctx *C.JSContext
		// each job is an outermost JS entry for the execution watchdog
		atomic.StoreInt64(&rts.execStart, time.Now().UnixNano())
		r := C.qjd_execute_pending_job(rts.rt, &jctx)
		atomic.StoreInt64(&rts.execStart, 0)
		if r == 0 {
			return
		}
		if r < 0 && jctx != nil {
			exc := C.JS_GetException(jctx)
			msg := "[unstringifiable]"
			var n C.size_t
			if cs := C.JS_ToCStringLen(jctx, &n, exc); cs != nil {
				msg = C.GoStringN(cs, C.int(n))
				C.JS_FreeCString(jctx, cs)
			} else {
				C.JS_FreeValue(jctx, C.JS_GetException(jctx))
			}
			C.JS_FreeValue(jctx, exc)
			rts.reportJobError(msg)
			// keep draining: one failed job must not stall the queue
		}
	}
	rts.reportJobError(fmt.Sprintf("job queue still busy after %d jobs (self-rescheduling promise chain?)", maxJobs))
}

func (rts *runtimeState) reapDeadContexts() {
	regMu.Lock()
	dead := rts.deadCtxs
	rts.deadCtxs = nil
	regMu.Unlock()
	for _, p := range dead {
		C.JS_FreeContext(p)
	}
	regMu.Lock()
	vals := rts.deadVals
	rts.deadVals = nil
	regMu.Unlock()
	for _, v := range vals {
		C.JS_FreeValue(rts.primary, v)
	}
}

// ---------------------------------------------------------------------------
// Stack machinery

func (s *ctxState) base() int {
	if len(s.frames) == 0 {
		return 0
	}
	return s.frames[len(s.frames)-1]
}

func (s *ctxState) top() int { return len(s.stack) - s.base() }

func (s *ctxState) normalize(idx int) int {
	var n int
	if idx < 0 {
		n = len(s.stack) + idx
	} else {
		n = s.base() + idx
	}
	if n < s.base() || n >= len(s.stack) {
		panic(fmt.Sprintf("quickjsduk: stack index out of range: %d (top %d)", idx, s.top()))
	}
	return n
}

// push takes ownership of v.
func (s *ctxState) push(v C.JSValue) { s.stack = append(s.stack, v) }

// popTransfer removes the top value and transfers ownership to the caller.
func (s *ctxState) popTransfer() C.JSValue {
	n := len(s.stack) - 1
	if n < s.base() {
		panic("quickjsduk: pop from empty stack frame")
	}
	v := s.stack[n]
	s.stack = s.stack[:n]
	return v
}

func (s *ctxState) at(idx int) C.JSValue { return s.stack[s.normalize(idx)] }

func (d *Context) popN(n int) {
	s := d.st()
	for i := 0; i < n; i++ {
		C.JS_FreeValue(s.ctx, s.popTransfer())
	}
	s.rts.reapDeadContexts()
}

func (d *Context) Pop()  { d.popN(1) }
func (d *Context) Pop2() { d.popN(2) }
func (d *Context) Pop3() { d.popN(3) }

func (d *Context) GetTop() int { return d.st().top() }

func (d *Context) Dup(idx int) {
	s := d.st()
	s.push(C.JS_DupValue(s.ctx, s.at(idx)))
}

func (d *Context) Copy(from, to int) {
	s := d.st()
	nf, nt := s.normalize(from), s.normalize(to)
	v := C.JS_DupValue(s.ctx, s.stack[nf])
	C.JS_FreeValue(s.ctx, s.stack[nt])
	s.stack[nt] = v
}

func (d *Context) Replace(idx int) {
	s := d.st()
	n := s.normalize(idx)
	v := s.popTransfer()
	if n == len(s.stack) { // replaced the top itself
		s.push(v)
		return
	}
	C.JS_FreeValue(s.ctx, s.stack[n])
	s.stack[n] = v
}

func (d *Context) Remove(idx int) {
	s := d.st()
	n := s.normalize(idx)
	C.JS_FreeValue(s.ctx, s.stack[n])
	s.stack = append(s.stack[:n], s.stack[n+1:]...)
}

func (d *Context) Insert(toIdx int) {
	s := d.st()
	n := s.normalize(toIdx)
	v := s.popTransfer()
	s.stack = append(s.stack, v)
	copy(s.stack[n+1:], s.stack[n:])
	s.stack[n] = v
}

// ---------------------------------------------------------------------------
// Push operations

func (d *Context) PushUndefined() { d.st().push(C.qjd_undefined()) }
func (d *Context) PushNull()      { d.st().push(C.qjd_null()) }

func (d *Context) PushBoolean(b bool) {
	if b {
		d.st().push(C.qjd_true())
	} else {
		d.st().push(C.qjd_false())
	}
}

func (d *Context) PushNumber(n float64) {
	s := d.st()
	s.push(C.JS_NewFloat64(s.ctx, C.double(n)))
}

func (d *Context) PushInt(n int) { d.PushNumber(float64(n)) }

func (d *Context) PushString(str string) {
	s := d.st()
	cs := C.CString(str)
	defer C.free(unsafe.Pointer(cs))
	s.push(C.JS_NewStringLen(s.ctx, cs, C.size_t(len(str))))
}

func (d *Context) PushObject() int {
	s := d.st()
	s.push(C.JS_NewObject(s.ctx))
	return s.top() - 1
}

func (d *Context) PushArray() int {
	s := d.st()
	s.push(C.JS_NewArray(s.ctx))
	return s.top() - 1
}

func (d *Context) PushGlobalObject() {
	s := d.st()
	s.push(C.JS_GetGlobalObject(s.ctx))
}

func (d *Context) PushHeapStash() {
	s := d.st()
	s.push(C.JS_DupValue(s.ctx, s.rts.stash))
}

func (d *Context) PushThis() {
	s := d.st()
	if len(s.thisV) == 0 {
		s.push(C.qjd_undefined())
		return
	}
	s.push(C.JS_DupValue(s.ctx, s.thisV[len(s.thisV)-1]))
}

// PushErrorObject pushes a real Error created via the Error constructor so
// that it carries a .stack of the current JS call stack (wb-rules uses this
// for GetTraceback).
func (d *Context) PushErrorObject(errCode int, args ...interface{}) {
	s := d.st()
	msg := fmt.Sprint(args...)
	glob := C.JS_GetGlobalObject(s.ctx)
	ctor := getPropStr(s.ctx, glob, "Error")
	C.JS_FreeValue(s.ctx, glob)
	cm := C.CString(msg)
	arg := C.JS_NewStringLen(s.ctx, cm, C.size_t(len(msg)))
	C.free(unsafe.Pointer(cm))
	errv := C.qjd_call_ctor(s.ctx, ctor, 1, &arg)
	C.JS_FreeValue(s.ctx, ctor)
	C.JS_FreeValue(s.ctx, arg)
	if C.qjd_tag(errv) == C.JS_TAG_EXCEPTION {
		errv = C.JS_GetException(s.ctx)
	}
	s.push(errv)
}

func (d *Context) PushThreadNewGlobalenv() {
	s := d.st()
	nctx := C.qjd_new_context(s.rts.rt)
	C.qjd_install_require(nctx)
	regMu.Lock()
	ctxReg[nctx] = &ctxState{ctx: nctx, rts: s.rts}
	regMu.Unlock()
	handle := C.qjd_new_obj_with_opaque(s.ctx, C.qjd_thread_class_id, unsafe.Pointer(nctx))
	s.push(handle)
}

// GetContext returns a Context for the thread handle at idx. The returned
// pointer is stable per realm, so *Context values compare/map correctly.
func (d *Context) GetContext(idx int) *Context {
	s := d.st()
	p := C.qjd_get_opaque(s.at(idx), C.qjd_thread_class_id)
	if p == nil {
		return nil
	}
	return &Context{p}
}

// ---------------------------------------------------------------------------
// Go objects and functions

func (d *Context) PushGoObject(o interface{}) {
	s := d.st()
	id := registerGoVal(o)
	s.push(C.qjd_new_obj_with_opaque_id(s.ctx, C.qjd_goobj_class_id, C.uint64_t(id)))
}

func (d *Context) GetGoObject(idx int) interface{} {
	s := d.st()
	p := C.qjd_get_opaque(s.at(idx), C.qjd_goobj_class_id)
	if p == nil {
		return nil
	}
	regMu.Lock()
	defer regMu.Unlock()
	return goVals[uint64(uintptr(p))]
}

func (d *Context) PushGoFunc(f GoFunc) {
	s := d.st()
	id := registerGoVal(f)
	s.push(C.qjd_new_go_func(s.ctx, C.uint64_t(id)))
}

//export goObjFinalize
func goObjFinalize(id C.uint64_t) {
	regMu.Lock()
	es := enums[uint64(id)]
	delete(enums, uint64(id))
	delete(goVals, uint64(id))
	regMu.Unlock()
	if es != nil {
		C.JS_FreeValueRT(C.JS_GetRuntime(es.ctx), es.obj)
	}
}

//export goThreadFinalize
func goThreadFinalize(p unsafe.Pointer) {
	ctx := (*C.JSContext)(p)
	regMu.Lock()
	cs := ctxReg[ctx]
	if cs != nil {
		// The realm's shim stack should be empty by now; queue leftovers
		// for the reap path - freeing here (under regMu) could run
		// finalizers that re-enter the registry lock (see DestroyHeap)
		cs.rts.deadVals = append(cs.rts.deadVals, cs.stack...)
		for k, m := range cs.rts.modules {
			if k.ctx == ctx {
				cs.rts.deadVals = append(cs.rts.deadVals, m)
				delete(cs.rts.modules, k)
			}
		}
		cs.rts.deadCtxs = append(cs.rts.deadCtxs, ctx)
		delete(ctxReg, ctx)
	}
	regMu.Unlock()
}

//export goFuncCall
func goFuncCall(ctx *C.JSContext, thisVal C.JSValue, argc C.int, argv *C.JSValue, id C.uint64_t) C.JSValue {
	regMu.Lock()
	fv := goVals[uint64(id)]
	cs := ctxReg[ctx]
	regMu.Unlock()
	f, ok := fv.(GoFunc)
	if !ok || cs == nil {
		return throwTypeErr(ctx, "stale go function")
	}
	// Duktape runs native functions on the CALLING thread's context; QuickJS
	// hands us the function's creation realm. Substitute the realm that is
	// actually executing JS right now (wb-rules keys per-file state on it).
	if active := cs.rts.currentActive(ctx); active != ctx {
		ctx = active
		regMu.Lock()
		cs = ctxReg[ctx]
		regMu.Unlock()
		if cs == nil {
			return throwTypeErr(ctx, "stale calling context")
		}
	}

	// Fresh duktape-style frame containing only the args.
	base := len(cs.stack)
	cs.frames = append(cs.frames, base)
	cs.thisV = append(cs.thisV, C.JS_DupValue(ctx, thisVal))
	if argc > 0 {
		args := unsafe.Slice(argv, int(argc))
		for _, a := range args {
			cs.stack = append(cs.stack, C.JS_DupValue(ctx, a))
		}
	}

	// A panic inside a Go callback is an internal invariant violation.
	// wb-rules' contract (inherited from go-duktape) is fail-fast: log the
	// original stack, then crash the process so systemd restarts a clean
	// engine. We cannot let the panic unwind through the C frames (cgo
	// would abort with a less useful message), so re-raise on a fresh
	// goroutine after printing the real stack.
	rc := func() (rc int) {
		defer func() {
			if r := recover(); r != nil {
				fmt.Fprintf(os.Stderr, "quickjsduk: panic in go function: %v\n%s\n", r, debug.Stack())
				ch := make(chan struct{})
				go func() { close(ch); panic(r) }()
				<-ch
				select {} // unreachable: the goroutine panic kills the process
			}
		}()
		return f(&Context{unsafe.Pointer(ctx)})
	}()

	var res C.JSValue
	switch {
	case rc == 1:
		if len(cs.stack) > base {
			res = cs.popTransfer()
		} else {
			res = C.qjd_undefined()
		}
	case rc == 0:
		res = C.qjd_undefined()
	case rc == DUK_RET_INSTACK_ERROR && len(cs.stack) > base:
		errv := cs.popTransfer()
		res = C.JS_Throw(ctx, errv)
	case rc == DUK_RET_TYPE_ERROR:
		res = throwDukRcError(ctx, "TypeError", "type", rc)
	default:
		res = throwDukRcError(ctx, "Error", "error", rc)
	}

	// Unwind the frame. Defensive: a misbehaving callback may have popped
	// beyond its own frame; log loudly instead of panicking mid-unwind.
	if len(cs.stack) < base {
		fmt.Fprintf(os.Stderr,
			"quickjsduk: BUG: go callback under-popped its frame: len=%d base=%d frames=%v\n%s\n",
			len(cs.stack), base, cs.frames, debug.Stack())
		base = len(cs.stack)
	}
	for len(cs.stack) > base {
		C.JS_FreeValue(ctx, cs.popTransfer())
	}
	cs.frames = cs.frames[:len(cs.frames)-1]
	C.JS_FreeValue(ctx, cs.thisV[len(cs.thisV)-1])
	cs.thisV = cs.thisV[:len(cs.thisV)-1]
	return res
}

// DebugDump prints C-level facts about the value at idx (diagnostics only).
func (d *Context) DebugDump(idx int, label string) {
	s := d.st()
	v := s.at(idx)
	tag := int(C.qjd_tag(v))
	isErr := C.JS_IsError(s.ctx, v) == 1
	isFn := C.JS_IsFunction(s.ctx, v) == 1
	goObj := C.qjd_get_opaque(v, C.qjd_goobj_class_id) != nil
	keys := "-"
	if tag == int(C.JS_TAG_OBJECT) {
		var ptab *C.JSPropertyEnum
		var plen C.uint32_t
		if C.JS_GetOwnPropertyNames(s.ctx, &ptab, &plen, v, C.int(C.JS_GPN_STRING_MASK)) == 0 {
			tab := unsafe.Slice(ptab, int(plen))
			keys = ""
			for i, pe := range tab {
				if i < 10 {
					cs := C.JS_AtomToCString(s.ctx, pe.atom)
					if cs != nil {
						keys += C.GoString(cs) + ","
						C.JS_FreeCString(s.ctx, cs)
					}
				}
				C.JS_FreeAtom(s.ctx, pe.atom)
			}
			C.js_free(s.ctx, unsafe.Pointer(ptab))
		} else {
			C.JS_FreeValue(s.ctx, C.JS_GetException(s.ctx))
			keys = "[ownKeys threw]"
		}
	}
	fmt.Fprintf(os.Stderr, "QJD-DUMP %s: tag=%d err=%v fn=%v goobj=%v keys=%s\n",
		label, tag, isErr, isFn, goObj, keys)
}

// throwDukRcError mimics duktape's negative-rc throws, e.g.
// "Error: error error (rc -100)" — wb-rules tests assert these exact strings.
func throwDukRcError(ctx *C.JSContext, ctor, kind string, rc int) C.JSValue {
	msg := fmt.Sprintf("%s error (rc %d)", kind, rc)
	glob := C.JS_GetGlobalObject(ctx)
	ctorV := getPropStr(ctx, glob, ctor)
	C.JS_FreeValue(ctx, glob)
	cm := C.CString(msg)
	arg := C.JS_NewStringLen(ctx, cm, C.size_t(len(msg)))
	C.free(unsafe.Pointer(cm))
	errv := C.qjd_call_ctor(ctx, ctorV, 1, &arg)
	C.JS_FreeValue(ctx, ctorV)
	C.JS_FreeValue(ctx, arg)
	if C.qjd_tag(errv) == C.JS_TAG_EXCEPTION {
		errv = C.JS_GetException(ctx)
	}
	return C.JS_Throw(ctx, errv)
}

// safeCString stringifies a value without touching the shim stack.
func safeCString(ctx *C.JSContext, v C.JSValue) string {
	var n C.size_t
	if cs := C.JS_ToCStringLen(ctx, &n, v); cs != nil {
		out := C.GoStringN(cs, C.int(n))
		C.JS_FreeCString(ctx, cs)
		return out
	}
	C.JS_FreeValue(ctx, C.JS_GetException(ctx))
	return "[unstringifiable]"
}

func throwTypeErr(ctx *C.JSContext, msg string) C.JSValue {
	cm := C.CString(msg)
	defer C.free(unsafe.Pointer(cm))
	return C.qjd_throw_type_error(ctx, cm)
}

func throwErr(ctx *C.JSContext, msg string) C.JSValue {
	cm := C.CString(msg)
	defer C.free(unsafe.Pointer(cm))
	return C.qjd_throw_error(ctx, cm)
}

// ---------------------------------------------------------------------------
// Type checks and getters

func (d *Context) GetType(idx int) int {
	s := d.st()
	n := len(s.stack) + idx
	if idx >= 0 {
		n = s.base() + idx
	}
	if n < s.base() || n >= len(s.stack) {
		return int(DUK_TYPE_NONE)
	}
	v := s.stack[n]
	switch C.qjd_tag(v) {
	case C.JS_TAG_UNDEFINED, C.JS_TAG_UNINITIALIZED:
		return int(DUK_TYPE_UNDEFINED)
	case C.JS_TAG_NULL:
		return int(DUK_TYPE_NULL)
	case C.JS_TAG_BOOL:
		return int(DUK_TYPE_BOOLEAN)
	case C.JS_TAG_INT, C.JS_TAG_FLOAT64:
		return int(DUK_TYPE_NUMBER)
	case C.JS_TAG_STRING:
		return int(DUK_TYPE_STRING)
	case C.JS_TAG_OBJECT, C.JS_TAG_FUNCTION_BYTECODE:
		return int(DUK_TYPE_OBJECT)
	default:
		return int(DUK_TYPE_OBJECT)
	}
}

func (d *Context) typeAt(idx int) Type { return Type(d.GetType(idx)) }

func (d *Context) IsUndefined(idx int) bool { return d.typeAt(idx).IsUndefined() }
func (d *Context) IsNull(idx int) bool      { return d.typeAt(idx).IsNull() }
func (d *Context) IsNullOrUndefined(idx int) bool {
	t := d.typeAt(idx)
	return t.IsNull() || t.IsUndefined()
}
func (d *Context) IsBoolean(idx int) bool { return d.typeAt(idx).IsBool() }
func (d *Context) IsNumber(idx int) bool  { return d.typeAt(idx).IsNumber() }
func (d *Context) IsString(idx int) bool  { return d.typeAt(idx).IsString() }
func (d *Context) IsObject(idx int) bool  { return d.typeAt(idx).IsObject() }
func (d *Context) IsBuffer(idx int) bool  { return false }
func (d *Context) IsPointer(idx int) bool { return false }

func (d *Context) IsArray(idx int) bool {
	s := d.st()
	return C.JS_IsArray(s.ctx, s.at(idx)) == 1
}

func (d *Context) IsFunction(idx int) bool {
	s := d.st()
	v := s.at(idx)
	if C.qjd_tag(v) == C.JS_TAG_FUNCTION_BYTECODE {
		return true
	}
	return C.JS_IsFunction(s.ctx, v) == 1
}

func (d *Context) GetBoolean(idx int) bool {
	s := d.st()
	return C.JS_ToBool(s.ctx, s.at(idx)) == 1 && d.IsBoolean(idx)
}

func (d *Context) GetNumber(idx int) float64 {
	if !d.IsNumber(idx) {
		return 0
	}
	s := d.st()
	var out C.double
	C.JS_ToFloat64(s.ctx, &out, s.at(idx))
	return float64(out)
}

func (d *Context) GetInt(idx int) int { return int(d.GetNumber(idx)) }

func (d *Context) GetString(idx int) string {
	if !d.IsString(idx) {
		return ""
	}
	return d.SafeToString(idx)
}

func (d *Context) GetPointer(idx int) unsafe.Pointer { return nil }

func (d *Context) SafeToString(idx int) string {
	s := d.st()
	v := s.at(idx)
	var n C.size_t
	cs := C.JS_ToCStringLen(s.ctx, &n, v)
	if cs == nil {
		exc := C.JS_GetException(s.ctx)
		cs = C.JS_ToCStringLen(s.ctx, &n, exc)
		C.JS_FreeValue(s.ctx, exc)
		if cs == nil {
			jv := C.qjd_json_stringify(s.ctx, v)
			var jn C.size_t
			jcs := C.JS_ToCStringLen(s.ctx, &jn, jv)
			dump := "?"
			if jcs != nil {
				dump = C.GoStringN(jcs, C.int(jn))
				C.JS_FreeCString(s.ctx, jcs)
			} else {
				C.JS_FreeValue(s.ctx, C.JS_GetException(s.ctx))
			}
			C.JS_FreeValue(s.ctx, jv)
			fmt.Fprintf(os.Stderr, "quickjsduk: SafeToString failed: value tag=%d json=%s\n",
				int(C.qjd_tag(v)), dump)
			return "[unstringifiable]"
		}
	}
	out := C.GoStringN(cs, C.int(n))
	C.JS_FreeCString(s.ctx, cs)
	return out
}

// Coercing To* variants replace the stack value like duktape does.
func (d *Context) ToString(idx int) string {
	s := d.st()
	out := d.SafeToString(idx)
	n := s.normalize(idx)
	C.JS_FreeValue(s.ctx, s.stack[n])
	cs := C.CString(out)
	s.stack[n] = C.JS_NewStringLen(s.ctx, cs, C.size_t(len(out)))
	C.free(unsafe.Pointer(cs))
	return out
}

func (d *Context) ToNumber(idx int) float64 {
	s := d.st()
	var out C.double
	C.JS_ToFloat64(s.ctx, &out, s.at(idx))
	n := s.normalize(idx)
	C.JS_FreeValue(s.ctx, s.stack[n])
	s.stack[n] = C.JS_NewFloat64(s.ctx, out)
	return float64(out)
}

func (d *Context) ToInt(idx int) int { return int(d.ToNumber(idx)) }

func (d *Context) ToBoolean(idx int) bool {
	s := d.st()
	return C.JS_ToBool(s.ctx, s.at(idx)) == 1
}

func (d *Context) GetLength(idx int) int {
	s := d.st()
	v := s.at(idx)
	if C.qjd_tag(v) == C.JS_TAG_STRING {
		return len(d.SafeToString(idx)) // byte length is fine for wb-rules' use
	}
	lv := getPropStr(s.ctx, v, "length")
	defer C.JS_FreeValue(s.ctx, lv)
	var out C.double
	if C.JS_ToFloat64(s.ctx, &out, lv) != 0 {
		return 0
	}
	return int(out)
}

// ---------------------------------------------------------------------------
// Property operations

func getPropStr(ctx *C.JSContext, obj C.JSValue, key string) C.JSValue {
	ck := C.CString(key)
	defer C.free(unsafe.Pointer(ck))
	return C.JS_GetPropertyStr(ctx, obj, ck)
}

func setPropStr(ctx *C.JSContext, obj C.JSValue, key string, v C.JSValue) C.int {
	ck := C.CString(key)
	defer C.free(unsafe.Pointer(ck))
	return C.JS_SetPropertyStr(ctx, obj, ck, v)
}

func (d *Context) GetPropString(objIdx int, key string) bool {
	s := d.st()
	obj := s.at(objIdx)
	exists := d.hasProp(obj, key)
	v := getPropStr(s.ctx, obj, key)
	if C.qjd_tag(v) == C.JS_TAG_EXCEPTION {
		C.JS_FreeValue(s.ctx, C.JS_GetException(s.ctx))
		v = C.qjd_undefined()
		exists = false
	}
	s.push(v)
	return exists
}

func (d *Context) hasProp(obj C.JSValue, key string) bool {
	s := d.st()
	if C.qjd_tag(obj) != C.JS_TAG_OBJECT {
		return false
	}
	ck := C.CString(key)
	atom := C.JS_NewAtom(s.ctx, ck)
	C.free(unsafe.Pointer(ck))
	r := C.JS_HasProperty(s.ctx, obj, atom)
	C.JS_FreeAtom(s.ctx, atom)
	if r < 0 {
		C.JS_FreeValue(s.ctx, C.JS_GetException(s.ctx))
	}
	return r == 1
}

func (d *Context) HasPropString(objIdx int, key string) bool {
	return d.hasProp(d.st().at(objIdx), key)
}

func (d *Context) PutPropString(objIdx int, key string) bool {
	s := d.st()
	obj := s.at(objIdx) // normalize while value still on stack
	v := s.popTransfer()
	rc := setPropStr(s.ctx, obj, key, v)
	if rc < 0 {
		C.JS_FreeValue(s.ctx, C.JS_GetException(s.ctx))
		return false
	}
	return rc != 0
}

func (d *Context) DelPropString(objIdx int, key string) bool {
	s := d.st()
	obj := s.at(objIdx)
	ck := C.CString(key)
	atom := C.JS_NewAtom(s.ctx, ck)
	C.free(unsafe.Pointer(ck))
	r := C.JS_DeleteProperty(s.ctx, obj, atom, 0)
	C.JS_FreeAtom(s.ctx, atom)
	if r < 0 {
		C.JS_FreeValue(s.ctx, C.JS_GetException(s.ctx))
	}
	return r == 1
}

func (d *Context) GetPropIndex(objIdx int, i uint) bool {
	s := d.st()
	obj := s.at(objIdx)
	v := C.JS_GetPropertyUint32(s.ctx, obj, C.uint32_t(i))
	if C.qjd_tag(v) == C.JS_TAG_EXCEPTION {
		C.JS_FreeValue(s.ctx, C.JS_GetException(s.ctx))
		v = C.qjd_undefined()
	}
	s.push(v)
	return C.qjd_tag(v) != C.JS_TAG_UNDEFINED
}

func (d *Context) PutPropIndex(objIdx int, i uint) bool {
	s := d.st()
	obj := s.at(objIdx)
	v := s.popTransfer()
	rc := C.JS_SetPropertyUint32(s.ctx, obj, C.uint32_t(i), v)
	if rc < 0 {
		C.JS_FreeValue(s.ctx, C.JS_GetException(s.ctx))
		return false
	}
	return rc != 0
}

func (d *Context) GetGlobalString(key string) bool {
	s := d.st()
	glob := C.JS_GetGlobalObject(s.ctx)
	defer C.JS_FreeValue(s.ctx, glob)
	exists := d.hasProp(glob, key)
	s.push(getPropStr(s.ctx, glob, key))
	return exists
}

func (d *Context) PutGlobalString(key string) bool {
	s := d.st()
	glob := C.JS_GetGlobalObject(s.ctx)
	defer C.JS_FreeValue(s.ctx, glob)
	v := s.popTransfer()
	rc := setPropStr(s.ctx, glob, key, v)
	if rc < 0 {
		C.JS_FreeValue(s.ctx, C.JS_GetException(s.ctx))
		return false
	}
	return rc != 0
}

func (d *Context) SetPrototype(objIdx int) {
	s := d.st()
	obj := s.at(objIdx)
	proto := s.popTransfer()
	C.JS_SetPrototype(s.ctx, obj, proto)
	C.JS_FreeValue(s.ctx, proto)
}

// ---------------------------------------------------------------------------
// Enumeration (duktape enumerator protocol)

func (d *Context) Enum(objIdx int, flags uint) {
	s := d.st()
	obj := s.at(objIdx)
	var ptab *C.JSPropertyEnum
	var plen C.uint32_t
	keys := []string{}
	gpnFlags := C.int(C.JS_GPN_STRING_MASK | C.JS_GPN_ENUM_ONLY)
	if C.JS_GetOwnPropertyNames(s.ctx, &ptab, &plen, obj, gpnFlags) == 0 {
		tab := unsafe.Slice(ptab, int(plen))
		for _, pe := range tab {
			cs := C.JS_AtomToCString(s.ctx, pe.atom)
			if cs != nil {
				key := C.GoString(cs)
				C.JS_FreeCString(s.ctx, cs)
				if flags&DUK_ENUM_ARRAY_INDICES_ONLY != 0 {
					numeric := len(key) > 0
					for _, ch := range key {
						if ch < '0' || ch > '9' {
							numeric = false
							break
						}
					}
					if !numeric {
						continue
					}
				}
				keys = append(keys, key)
			}
			C.JS_FreeAtom(s.ctx, pe.atom)
		}
		C.js_free(s.ctx, unsafe.Pointer(ptab))
	}
	id := registerGoVal(nil) // reserve an id
	regMu.Lock()
	enums[id] = &enumState{obj: C.JS_DupValue(s.ctx, obj), keys: keys, ctx: s.ctx}
	regMu.Unlock()
	// The enumerator lives on the stack as a goobj-class handle; its
	// finalizer releases the registry entry (enum obj freed in Next/cleanup).
	s.push(C.qjd_new_obj_with_opaque_id(s.ctx, C.qjd_goobj_class_id, C.uint64_t(id)))
}

func (d *Context) Next(enumIdx int, getValue bool) bool {
	s := d.st()
	p := C.qjd_get_opaque(s.at(enumIdx), C.qjd_goobj_class_id)
	if p == nil {
		return false
	}
	regMu.Lock()
	es := enums[uint64(uintptr(p))]
	regMu.Unlock()
	if es == nil || es.pos >= len(es.keys) {
		return false
	}
	key := es.keys[es.pos]
	es.pos++
	ck := C.CString(key)
	s.push(C.JS_NewStringLen(s.ctx, ck, C.size_t(len(key))))
	C.free(unsafe.Pointer(ck))
	if getValue {
		s.push(getPropStr(s.ctx, es.obj, key))
	}
	return true
}

// ---------------------------------------------------------------------------
// Eval / compile / call

const evalGlobal = C.int(0) // JS_EVAL_TYPE_GLOBAL

func (d *Context) evalRaw(src, filename string, flags C.int) C.int {
	s := d.st()
	cs := C.CString(src)
	cf := C.CString(filename)
	defer C.free(unsafe.Pointer(cs))
	defer C.free(unsafe.Pointer(cf))
	s.rts.pushActive(s.ctx)
	v := C.qjd_eval(s.ctx, cs, C.size_t(len(src)), cf, flags)
	s.rts.popActive()
	if C.qjd_tag(v) == C.JS_TAG_EXCEPTION {
		s.push(C.JS_GetException(s.ctx))
		s.rts.pumpJobs()
		return 1
	}
	s.push(v)
	s.rts.pumpJobs()
	return 0
}

func (d *Context) PevalString(src string) int {
	return int(d.evalRaw(src, "input", evalGlobal))
}

func (d *Context) PevalFile(path string) int {
	data, err := os.ReadFile(path)
	if err != nil {
		d.PushErrorObject(DUK_ERR_ERROR, "cannot read file: "+err.Error())
		return 1
	}
	return int(d.evalRaw(string(data), path, evalGlobal))
}

func (d *Context) pcompile(flags uint, src, filename string) int {
	if flags&DUK_COMPILE_FUNCTION != 0 {
		// Duktape compiles a function expression; evaluating "(expr)" has no
		// side effects and yields the function value.
		return int(d.evalRaw("("+src+")", filename, evalGlobal))
	}
	// Program/eval compile: bytecode object, executable via Pcall(0).
	return int(d.evalRaw(src, filename, evalGlobal|C.JS_EVAL_FLAG_COMPILE_ONLY))
}

func (d *Context) PcompileString(flags uint, src string) int {
	return d.pcompile(flags, src, "input")
}

// PcompileStringFilename takes the filename from the stack top (duktape
// protocol: [ ... filename ] -> [ ... func ]).
func (d *Context) PcompileStringFilename(flags uint, src string) int {
	s := d.st()
	filename := d.SafeToString(-1)
	C.JS_FreeValue(s.ctx, s.popTransfer())
	return d.pcompile(flags, src, filename)
}

func (d *Context) Pcall(nargs int) int {
	s := d.st()
	fnPos := len(s.stack) - nargs - 1
	if fnPos < s.base() {
		panic("quickjsduk: Pcall: not enough stack values")
	}
	fn := s.stack[fnPos]

	var res C.JSValue
	s.rts.pushActive(s.ctx)
	if C.qjd_tag(fn) == C.JS_TAG_FUNCTION_BYTECODE {
		res = C.qjd_eval_function(s.ctx, C.JS_DupValue(s.ctx, fn))
	} else {
		var argv *C.JSValue
		if nargs > 0 {
			argv = &s.stack[fnPos+1]
		}
		res = C.qjd_call(s.ctx, fn, C.qjd_undefined(), C.int(nargs), argv)
	}
	s.rts.popActive()

	for i := 0; i <= nargs; i++ {
		C.JS_FreeValue(s.ctx, s.popTransfer())
	}
	if C.qjd_tag(res) == C.JS_TAG_EXCEPTION {
		s.push(C.JS_GetException(s.ctx))
		s.rts.pumpJobs()
		return 1
	}
	s.push(res)
	s.rts.pumpJobs()
	return 0
}

// PcallProp calls obj[key](args...): [ ... obj ... key a1..aN ] -> [ ... obj ... result ]
func (d *Context) PcallProp(objIdx int, nargs int) int {
	s := d.st()
	obj := s.at(objIdx)
	keyPos := len(s.stack) - nargs - 1
	if keyPos < s.base() {
		panic("quickjsduk: PcallProp: not enough stack values")
	}
	atom := C.JS_ValueToAtom(s.ctx, s.stack[keyPos])
	var argv *C.JSValue
	if nargs > 0 {
		argv = &s.stack[keyPos+1]
	}
	s.rts.pushActive(s.ctx)
	res := C.qjd_invoke(s.ctx, obj, atom, C.int(nargs), argv)
	s.rts.popActive()
	C.JS_FreeAtom(s.ctx, atom)
	for i := 0; i <= nargs; i++ {
		C.JS_FreeValue(s.ctx, s.popTransfer())
	}
	if C.qjd_tag(res) == C.JS_TAG_EXCEPTION {
		s.push(C.JS_GetException(s.ctx))
		s.rts.pumpJobs()
		return 1
	}
	s.push(res)
	s.rts.pumpJobs()
	return 0
}

func (d *Context) New(nargs int) {
	s := d.st()
	fnPos := len(s.stack) - nargs - 1
	fn := s.stack[fnPos]
	var argv *C.JSValue
	if nargs > 0 {
		argv = &s.stack[fnPos+1]
	}
	s.rts.pushActive(s.ctx)
	res := C.qjd_call_ctor(s.ctx, fn, C.int(nargs), argv)
	s.rts.popActive()
	for i := 0; i <= nargs; i++ {
		C.JS_FreeValue(s.ctx, s.popTransfer())
	}
	if C.qjd_tag(res) == C.JS_TAG_EXCEPTION {
		res = C.JS_GetException(s.ctx)
	}
	s.push(res)
	s.rts.pumpJobs()
}

// ---------------------------------------------------------------------------
// JSON

func (d *Context) JsonEncode(idx int) string {
	s := d.st()
	n := s.normalize(idx)
	v := C.qjd_json_stringify(s.ctx, s.stack[n])
	if C.qjd_tag(v) == C.JS_TAG_EXCEPTION {
		exc := C.JS_GetException(s.ctx)
		JobErrorHandler("JsonEncode failed: " + safeCString(s.ctx, exc))
		C.JS_FreeValue(s.ctx, exc)
		// "null" is valid JSON; never let an empty string reach storage
		cs := C.CString("null")
		v = C.JS_NewStringLen(s.ctx, cs, 4)
		C.free(unsafe.Pointer(cs))
	}
	C.JS_FreeValue(s.ctx, s.stack[n])
	s.stack[n] = v
	if C.qjd_tag(v) == C.JS_TAG_STRING {
		return d.SafeToString(idx)
	}
	return ""
}

func (d *Context) JsonDecode(idx int) {
	s := d.st()
	n := s.normalize(idx)
	src := d.SafeToString(idx)
	cs := C.CString(src)
	cf := C.CString("json")
	v := C.qjd_json_parse(s.ctx, cs, C.size_t(len(src)), cf)
	C.free(unsafe.Pointer(cs))
	C.free(unsafe.Pointer(cf))
	if C.qjd_tag(v) == C.JS_TAG_EXCEPTION {
		exc := C.JS_GetException(s.ctx)
		JobErrorHandler("JsonDecode failed: " + safeCString(s.ctx, exc))
		C.JS_FreeValue(s.ctx, exc)
		C.JS_FreeValue(s.ctx, s.stack[n])
		s.stack[n] = C.qjd_undefined()
		return
	}
	C.JS_FreeValue(s.ctx, s.stack[n])
	s.stack[n] = v
}

// ---------------------------------------------------------------------------
// CommonJS require (Duktape 1.x protocol with wb-rules extensions)

// resolveModuleID resolves "./x" / "../x" against the requiring module's id
// the way Duktape's CommonJS support does.
func resolveModuleID(baseID, id string) string {
	if !strings.HasPrefix(id, "./") && !strings.HasPrefix(id, "../") {
		return id
	}
	base := ""
	if i := strings.LastIndex(baseID, "/"); i >= 0 {
		base = baseID[:i]
	}
	parts := []string{}
	if base != "" {
		parts = strings.Split(base, "/")
	}
	for _, seg := range strings.Split(id, "/") {
		switch seg {
		case ".", "":
		case "..":
			if len(parts) > 0 {
				parts = parts[:len(parts)-1]
			}
		default:
			parts = append(parts, seg)
		}
	}
	return strings.Join(parts, "/")
}

//export goRequire
func goRequire(ctx *C.JSContext, thisVal C.JSValue, argc C.int, argv *C.JSValue, baseVal C.JSValue) C.JSValue {
	cs := stateFor(ctx)
	if cs == nil || argc < 1 {
		return throwTypeErr(ctx, "require: bad context or missing id")
	}
	rts := cs.rts
	args := unsafe.Slice(argv, int(argc))
	var n C.size_t
	cid := C.JS_ToCStringLen(ctx, &n, args[0])
	if cid == nil {
		return C.qjd_exception()
	}
	id := C.GoStringN(cid, C.int(n))
	C.JS_FreeCString(ctx, cid)

	var bn C.size_t
	if cb := C.JS_ToCStringLen(ctx, &bn, baseVal); cb != nil {
		id = resolveModuleID(C.GoStringN(cb, C.int(bn)), id)
		C.JS_FreeCString(ctx, cb)
	}

	if mod, ok := rts.modules[moduleKey{ctx, id}]; ok {
		return C.qjd_get_module_exports(ctx, mod)
	}

	// Fresh module + exports objects.
	module := C.JS_NewObject(ctx)
	exports := C.JS_NewObject(ctx)
	setPropStr(ctx, module, "exports", C.JS_DupValue(ctx, exports))
	{
		cidStr := C.CString(id)
		setPropStr(ctx, module, "id", C.JS_NewStringLen(ctx, cidStr, C.size_t(len(id))))
		C.free(unsafe.Pointer(cidStr))
	}

	// Look up this realm's Duktape.modSearch (assigned by wb-rules).
	glob := C.JS_GetGlobalObject(ctx)
	duk := getPropStr(ctx, glob, "Duktape")
	modSearch := getPropStr(ctx, duk, "modSearch")
	requireFn := getPropStr(ctx, glob, "require")
	C.JS_FreeValue(ctx, duk)
	C.JS_FreeValue(ctx, glob)

	fail := func(v C.JSValue) C.JSValue {
		C.JS_FreeValue(ctx, modSearch)
		C.JS_FreeValue(ctx, requireFn)
		C.JS_FreeValue(ctx, module)
		C.JS_FreeValue(ctx, exports)
		return v
	}

	if C.JS_IsFunction(ctx, modSearch) != 1 {
		return fail(throwTypeErr(ctx, "require: no Duktape.modSearch"))
	}

	cidr := C.CString(id)
	resolvedID := C.JS_NewStringLen(ctx, cidr, C.size_t(len(id)))
	C.free(unsafe.Pointer(cidr))
	msArgs := []C.JSValue{resolvedID, requireFn, exports, module}
	rts.pushActive(ctx)
	srcVal := C.qjd_call(ctx, modSearch, C.qjd_undefined(), 4, &msArgs[0])
	rts.popActive()
	C.JS_FreeValue(ctx, resolvedID)
	if C.qjd_tag(srcVal) == C.JS_TAG_EXCEPTION {
		return fail(C.qjd_exception())
	}

	// Pre-register for require-cycles: a module requiring its requirer sees
	// the partially-built exports instead of recursing forever.
	rts.modules[moduleKey{ctx, id}] = C.JS_DupValue(ctx, module)
	failCached := func(v C.JSValue) C.JSValue {
		if m, ok := rts.modules[moduleKey{ctx, id}]; ok {
			C.JS_FreeValue(ctx, m)
			delete(rts.modules, moduleKey{ctx, id})
		}
		return fail(v)
	}
	if C.qjd_tag(srcVal) == C.JS_TAG_STRING {
		var sn C.size_t
		csrc := C.JS_ToCStringLen(ctx, &sn, srcVal)
		src := C.GoStringN(csrc, C.int(sn))
		C.JS_FreeCString(ctx, csrc)
		C.JS_FreeValue(ctx, srcVal)

		filename := id
		fv := getPropStr(ctx, module, "filename")
		if C.qjd_tag(fv) == C.JS_TAG_STRING {
			var fn2 C.size_t
			cf := C.JS_ToCStringLen(ctx, &fn2, fv)
			filename = C.GoStringN(cf, C.int(fn2))
			C.JS_FreeCString(ctx, cf)
		}
		C.JS_FreeValue(ctx, fv)

		wrapped := "(function(require,exports,module){" + src + "\n})"
		cw := C.CString(wrapped)
		cf := C.CString(filename)
		fn := C.qjd_eval(ctx, cw, C.size_t(len(wrapped)), cf, evalGlobal)
		C.free(unsafe.Pointer(cw))
		C.free(unsafe.Pointer(cf))
		if C.qjd_tag(fn) == C.JS_TAG_EXCEPTION {
			return failCached(C.qjd_exception())
		}
		cbase := C.CString(id)
		boundRequire := C.qjd_new_require(ctx, cbase)
		C.free(unsafe.Pointer(cbase))
		callArgs := []C.JSValue{boundRequire, exports, module}
		rts.pushActive(ctx)
		res := C.qjd_call(ctx, fn, exports, 3, &callArgs[0])
		rts.popActive()
		C.JS_FreeValue(ctx, fn)
		C.JS_FreeValue(ctx, boundRequire)
		if C.qjd_tag(res) == C.JS_TAG_EXCEPTION {
			return failCached(C.qjd_exception())
		}
		C.JS_FreeValue(ctx, res)
	} else {
		C.JS_FreeValue(ctx, srcVal)
	}

	out := C.qjd_get_module_exports(ctx, module)
	C.JS_FreeValue(ctx, modSearch)
	C.JS_FreeValue(ctx, requireFn)
	C.JS_FreeValue(ctx, module)
	C.JS_FreeValue(ctx, exports)
	return out
}
