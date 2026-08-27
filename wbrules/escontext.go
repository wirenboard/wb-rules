package wbrules

import (
	"bytes"
	"errors"
	"fmt"
	"log"
	"os"
	"reflect"
	"regexp"
	"runtime"
	"strconv"
	"strings"

	"github.com/stretchr/objx"
	duktape "github.com/wirenboard/go-duktape"
	"github.com/wirenboard/wbgong"
)

const (
	ESCALLBACKS_OBJ_NAME = "_esCallbacks"
	FILENAME_PROP_NAME   = "__filename"
)

type ESLocation struct {
	filename string
	line     int
}

type ESTraceback []ESLocation
type ESCallback uint64
type ESCallbackFunc func(args objx.Map) any
type ESCallbackErrorHandler func(err ESError)

// ESSyncFunc denotes a function that executes the specified
// thunk in the context of the goroutine which utilizes the context
type ESSyncFunc func(thunk func())

type ESContext struct {
	*duktape.Context
	syncFunc             ESSyncFunc
	callbackErrorHandler ESCallbackErrorHandler
	factory              *ESContextFactory

	ruleNames map[string]*Rule

	// storedCallbacks are the _esCallbacks stash keys this context created.
	// The stash is heap-wide, so these entries do not die with the context:
	// invalidate() must sweep them, or each one pins the dead context's
	// whole realm (its scope chains, its realm-local builtins) until the
	// process restarts. Entries removed the normal way (RemoveCallback) are
	// pruned here as well, so the set stays bounded for long-lived contexts.
	storedCallbacks map[ESCallback]struct{}

	valid bool
}

type ESError struct {
	Message   string
	Traceback ESTraceback
}

// ESContextFactory creates ESContexts and  stores properties which are
// common for related ESContexts (in one application).
// ESContextFactory is logically binded to Duktape heap.
type ESContextFactory struct {
	duktapeToESContextMap map[duktape.Context]*ESContext
	callbackIndex         ESCallback

	// heapCtx is the first (engine-global) context created on this heap. It
	// lives as long as the engine and gives invalidate() a context that can
	// still reach the heap-wide stash after a file context's own realm is
	// gone.
	heapCtx *ESContext

	// preprocessor transforms rule-file source before evaluation (used for
	// source transpilation); nil means load sources as-is.
	preprocessor func(path string, src []byte) ([]byte, error)

	// lineTranslator maps generated (transpiled) line numbers back to
	// source lines for preprocessed files; nil means identity.
	lineTranslator func(file string, line int) (int, bool)

	// wrapPrologue returns extra same-line source injected into the rule
	// file wrapper (e.g. "use strict" for transpiled files); nil = none.
	wrapPrologue func(path string) string
}

func newESContextFactory() *ESContextFactory {
	return &ESContextFactory{
		duktapeToESContextMap: make(map[duktape.Context]*ESContext),
		callbackIndex:         1,
	}
}

func (err ESError) Error() string {
	return err.Message
}

func (f *ESContextFactory) newESContext(syncFunc ESSyncFunc, filename string) *ESContext {
	return f.newESContextFromDuktape(syncFunc, filename, duktape.NewContext())
}

func (f *ESContextFactory) newESContextFromDuktape(syncFunc ESSyncFunc, filename string, dctx *duktape.Context) *ESContext {
	if dctx == nil {
		// realm creation failed (the JS heap hit its memory limit)
		return nil
	}
	ctx := &ESContext{
		Context:              dctx,
		syncFunc:             syncFunc,
		callbackErrorHandler: nil,
		factory:              f,
		ruleNames:            make(map[string]*Rule),
		storedCallbacks:      make(map[ESCallback]struct{}),
		valid:                true,
	}
	if f.heapCtx == nil {
		f.heapCtx = ctx
	}
	ctx.callbackErrorHandler = ctx.DefaultCallbackErrorHandler
	ctx.initGlobalObject()
	ctx.initFilename(filename)
	ctx.initHeapPropertyObjectIfNotExist(ESCALLBACKS_OBJ_NAME)

	wbgong.Debug.Printf("create context %p\n", ctx)

	// save context for conversions
	f.duktapeToESContextMap[*dctx] = ctx

	return ctx
}

func (ctx *ESContext) invalidate() {
	// remove context from factory, just in case
	delete(ctx.factory.duktapeToESContextMap, *ctx.Context)

	// Sweep this context's callback entries out of the heap-wide stash: the
	// stash outlives the context, and a surviving entry would pin the dead
	// realm (with everything its scope chains reference) until process
	// restart. The context's own realm may already be gone here, so the
	// sweep goes through the long-lived heap context. The finalizer-driven
	// RemoveCallback path skips invalid contexts and the keys are already
	// gone, so nothing is deleted twice.
	if heap := ctx.factory.heapCtx; heap != nil && heap != ctx && heap.IsValid() {
		for key := range ctx.storedCallbacks {
			heap.RemoveCallback(key)
		}
	}
	ctx.storedCallbacks = nil

	ctx.Context = nil
	ctx.valid = false
}

// markClosed invalidates the context WITHOUT touching the JS heap - for
// ESEngine.Close, which has already destroyed the whole heap. Stragglers
// (Go finalizers, producers whose thunks were dropped) then see
// IsValid()==false instead of reaching into freed memory.
func (ctx *ESContext) markClosed() {
	ctx.storedCallbacks = nil
	ctx.Context = nil
	ctx.valid = false
}

func (ctx *ESContext) assertStackClean(stackTop int) {
	if ctx.GetTop() != stackTop {
		wbgong.Error.Panicf("stack top assertion failed: expected %d, got %d", stackTop, ctx.GetTop())
	}
}

func (ctx *ESContext) IsValid() bool {
	return ctx.valid
}

func (ctx *ESContext) DefaultCallbackErrorHandler(err ESError) {
	wbgong.Error.Printf("failed to invoke callback in context %p: %s", ctx, err)
}

func (ctx *ESContext) SetCallbackErrorHandler(handler ESCallbackErrorHandler) {
	ctx.callbackErrorHandler = handler
}

func (ctx *ESContext) getObject(objIndex int) map[string]any {
	m := make(map[string]any)
	ctx.Enum(-1, duktape.DUK_ENUM_OWN_PROPERTIES_ONLY)
	for ctx.Next(-1, true) {
		key := ctx.SafeToString(-2)
		m[key] = ctx.getJSObject(-1, false)
		ctx.Pop2()
	}
	ctx.Pop()
	return m
}

func (ctx *ESContext) getArray(objIndex int) []any {
	// FIXME: this will not work for arrays with length >= 2^32
	r := make([]any, ctx.GetLength(objIndex))
	ctx.Enum(-1, duktape.DUK_ENUM_ARRAY_INDICES_ONLY)
	for ctx.Next(-1, true) {
		n := ctx.ToInt(-2)
		r[n] = ctx.getJSObject(-1, false)
		ctx.Pop2()
	}
	ctx.Pop()
	return r
}

func (ctx *ESContext) getJSObject(objIndex int, top bool) any {
	t := duktape.Type(ctx.GetType(-1))
	switch {
	case t.IsNone() || t.IsUndefined() || t.IsNull(): // FIXME
		return nil // FIXME
	case t.IsBool():
		return ctx.GetBoolean(objIndex)
	case t.IsNumber():
		return ctx.GetNumber(objIndex)
	case t.IsString():
		return ctx.GetString(objIndex)
	case t.IsObject():
		if ctx.IsArray(objIndex) {
			return ctx.getArray(objIndex)
		}
		m := ctx.getObject(objIndex)
		if top {
			return objx.New(m)
		}
		return m
	case t.IsBuffer():
		wbgong.Error.Println("buffers aren't supported yet")
		return nil
	case t.IsPointer():
		return ctx.GetPointer(objIndex)
	default:
		wbgong.Error.Panicf("bad object type %d", t)
		return nil // avoid compiler warning
	}
}

// ConversionError marks a GetJSObject result whose conversion ran a getter
// or Proxy trap that threw. The partially converted value is discarded; a
// builtin receiving this sentinel rethrows the recorded error to the calling
// script (Duktape propagated such throws out of its property reads). The
// sentinel is intentionally NOT objx.Map/[]any, so an unaudited call site's
// type assertion fails closed instead of using a partial object.
type ConversionError struct {
	Message string
}

func (e ConversionError) Error() string { return e.Message }

func (ctx *ESContext) GetJSObject(objIndex int) any {
	ctx.TakeConversionError() // drop a stale record from a non-conversion Enum
	v := ctx.getJSObject(objIndex, true)
	if msg, failed := ctx.TakeConversionError(); failed {
		return ConversionError{Message: msg}
	}
	return v
}

func (ctx *ESContext) PushJSObject(obj any) {
	if obj == nil {
		ctx.PushNull()
		return
	}
	switch t := obj.(type) {
	case string:
		ctx.PushString(t)
	case objx.Map:
		ctx.PushJSObject(map[string]any(t))
	case wbgong.Title:
		ctx.PushObject()
		for k, v := range t {
			ctx.PushJSObject(v)
			ctx.PutPropString(-2, k)
		}
	case map[string]wbgong.Title:
		ctx.PushObject()
		for k, v := range t {
			ctx.PushJSObject(v)
			ctx.PutPropString(-2, k)
		}
	case map[string]any:
		ctx.PushObject()
		for k, v := range t {
			ctx.PushJSObject(v)
			ctx.PutPropString(-2, k)
		}
	case bool:
		ctx.PushBoolean(t)
	case float32:
		ctx.PushNumber(float64(t))
	case float64:
		ctx.PushNumber(t)
	case int:
		ctx.PushNumber(float64(t))
	case uint8:
		ctx.PushNumber(float64(t))
	case uint16:
		ctx.PushNumber(float64(t))
	case uint32:
		ctx.PushNumber(float64(t))
	case uint64:
		ctx.PushNumber(float64(t))
	case int8:
		ctx.PushNumber(float64(t))
	case int16:
		ctx.PushNumber(float64(t))
	case int32:
		ctx.PushNumber(float64(t))
	case int64:
		ctx.PushNumber(float64(t))
	default:
		ctx.pushJSObjectUsingReflection(obj)
	}
}

func (ctx *ESContext) pushJSObjectUsingReflection(obj any) {
	v := reflect.ValueOf(obj)
	if v.Kind() != reflect.Slice && v.Kind() != reflect.Array {
		log.Panicf("ESContext: unsupported object value: %v", obj)
	}
	if v.IsNil() {
		ctx.PushNull()
		return
	}
	vIndex := ctx.PushArray()
	n := v.Len()
	for i := 0; i < n; i++ {
		ctx.PushJSObject(v.Index(i).Interface())
		ctx.PutPropIndex(vIndex, uint(i))
	}
}

func (ctx *ESContext) StringArrayToGo(arrIndex int) []string {
	if !ctx.IsArray(arrIndex) {
		panic("string array expected")
	}

	n := ctx.GetLength(arrIndex)
	r := make([]string, n)
	for i := 0; i < n; i++ {
		ctx.GetPropIndex(arrIndex, uint(i))
		r[i] = ctx.SafeToString(-1)
		ctx.Pop()
	}
	return r
}

func (ctx *ESContext) initGlobalObject() {
	ctx.PushGlobalObject()
	ctx.PushGlobalObject()
	ctx.PutPropString(-2, "global")
	ctx.Pop()
}

func (ctx *ESContext) initFilename(filename string) {
	ctx.PushString(filename)
	ctx.PutGlobalString(FILENAME_PROP_NAME)
}

func (ctx *ESContext) initHeapPropertyObjectIfNotExist(propName string) {
	// callback list stash property holds callback functions referenced by ids
	ctx.PushHeapStash()
	defer ctx.Pop()

	// check if property exists
	if !ctx.HasPropString(-1, propName) {
		ctx.PushObject()
		ctx.PutPropString(-2, propName)
	}
}

func (ctx *ESContext) callbackKey(key ESCallback) string {
	return strconv.FormatUint(uint64(key), 16)
}

func (ctx *ESContext) invokeCallback(key ESCallback, args objx.Map) any {
	if !ctx.IsValid() {
		wbgong.Error.Printf("skipping callback %d: context %p is invalid\n", key, ctx)
		return nil
	}
	wbgong.Debug.Printf("trying to invoke callback %d in context %p\n", key, ctx)

	ctx.PushHeapStash()

	ctx.GetPropString(-1, ESCALLBACKS_OBJ_NAME)
	ctx.PushString(ctx.callbackKey(key))
	argCount := 0
	if args != nil {
		ctx.PushJSObject(args)
		argCount++
	}
	defer ctx.Pop3() // pop: result, callback list object, global stash
	if s := ctx.PcallProp(-2-argCount, argCount); s != 0 {
		ctx.callbackErrorHandler(ctx.GetESError())
		return nil
	} else if ctx.IsBoolean(-1) {
		return ctx.ToBoolean(-1)
	} else if ctx.IsString(-1) {
		return ctx.ToString(-1)
	} else if ctx.IsNumber(-1) {
		return ctx.ToNumber(-1)
	} else {
		return nil
	}
}

// storeCallback stores the callback from the specified stack index
// (which should be >= 0) at 'key' in the callback list specified as propName.
// If key is specified as nil, a new callback key is generated and returned
// as uint64. In this case the returned value is guaranteed to be
// greater than zero.
func (ctx *ESContext) storeCallback(callbackStackIndex int) ESCallback {
	// get previous callback index
	key := ctx.factory.callbackIndex
	ctx.factory.callbackIndex++
	if ctx.storedCallbacks != nil {
		ctx.storedCallbacks[key] = struct{}{}
	}

	wbgong.Debug.Printf("store callback %d at context %p\n", key, ctx)

	ctx.PushHeapStash()
	ctx.GetPropString(-1, ESCALLBACKS_OBJ_NAME)
	if callbackStackIndex < 0 {
		ctx.Dup(callbackStackIndex - 2)
	} else {
		ctx.Dup(callbackStackIndex)
	}
	ctx.PutPropString(-2, ctx.callbackKey(key))
	ctx.Pop2()
	return key
}

type callbackHolder struct {
	ctx      *ESContext
	callback ESCallback
}

func callbackFinalizer(holder *callbackHolder) {
	// this function already runs in a separate goroutine
	holder.ctx.removeCallbackSync(holder.callback)
}

func (ctx *ESContext) WrapCallback(callbackStackIndex int) ESCallbackFunc {
	holder := &callbackHolder{
		ctx,
		ctx.storeCallback(callbackStackIndex),
	}
	runtime.SetFinalizer(holder, callbackFinalizer)
	return func(args objx.Map) any {
		return ctx.invokeCallback(holder.callback, args)
	}
}

func (ctx *ESContext) removeCallbackSync(key ESCallback) {
	// if context is invalid, just ignore this
	if !ctx.valid {
		return
	}

	if ctx.syncFunc == nil {
		ctx.RemoveCallback(key)
	} else {
		ctx.syncFunc(func() {
			ctx.RemoveCallback(key)
		})
	}
}

func (ctx *ESContext) RemoveCallback(key ESCallback) {
	// invalid context: its keys were already swept by invalidate()
	if !ctx.IsValid() {
		return
	}

	delete(ctx.storedCallbacks, key)

	defer ctx.assertStackClean(ctx.GetTop())

	ctx.PushHeapStash()
	ctx.GetPropString(-1, ESCALLBACKS_OBJ_NAME)
	ctx.DelPropString(-1, ctx.callbackKey(key))
	ctx.Pop2()
}

func (ctx *ESContext) EvalScript(code string) error {
	defer ctx.Pop()
	if r := ctx.PevalString(code); r != 0 {
		return ctx.GetESError()
	}
	return nil
}

func (ctx *ESContext) LoadScript(path string) error {
	defer ctx.Pop()
	if r := ctx.PevalFile(path); r != 0 {
		return ctx.GetESErrorAugmentingSyntaxErrors(path)
	}
	return nil
}

// LoadScenario wraps loaded script into closure
// and gives extra global objects with additional information
// about environment
func (ctx *ESContext) LoadScenario(path string) error {
	// load script file
	srcRaw, err := os.ReadFile(path)

	if err != nil {
		return err
	}

	if pp := ctx.factory.preprocessor; pp != nil {
		srcRaw, err = pp(path, srcRaw)
		if err != nil {
			line := 1
			var lineErr interface{ SourceLine() int }
			if errors.As(err, &lineErr) {
				line = lineErr.SourceLine()
			}
			return ESError{Message: err.Error(), Traceback: ESTraceback{{filename: path, line: line}}}
		}
	}

	// wrap source code; exports is provided (aliasing module.exports) so
	// CommonJS-style module files also load as plain rule files. A
	// preprocessor may inject a same-line prologue via factory hooks.
	//
	// The wrapper is async so a rule file may use top-level await
	// (`val = await changed(...)`). Files that use no top-level await are
	// unaffected: an async function with no await runs synchronously to
	// completion, and a throw before any await settles its promise
	// synchronously - both are handled below exactly as the old synchronous
	// wrapper was. A file that does await at the top level leaves a pending
	// promise and finishes on the microtask queue, like an async rule body.
	prologue := ""
	if pl := ctx.factory.wrapPrologue; pl != nil {
		prologue = pl(path)
	}
	src := "async function F(module, exports){" + prologue + string(srcRaw) + "\n}"

	// Source code evaluation.
	// Checking if there are extra curly braces. Compile with the real path
	// so syntax-error tracebacks carry the script file name.
	ctx.PushString(path)
	if err := ctx.PcompileStringFilename(duktape.DUK_COMPILE_EVAL, src); err != 0 {
		defer ctx.Pop()
		return ctx.GetESErrorAugmentingSyntaxErrors(path)
	}
	ctx.Pop()

	// compile function
	if err = ctx.LoadFunctionFromString(path, src); err != nil {
		return err
	}

	// push 'module' argument
	ctx.PushObject()

	// set module prototype
	ctx.PushGlobalObject()
	ctx.GetPropString(-1, "__wbModulePrototype")
	ctx.SetPrototype(-3)
	ctx.Pop()

	// set 'filename' param
	ctx.PushString(path)
	ctx.PutPropString(-2, "filename")

	// push 'exports' argument, aliased as module.exports
	ctx.PushObject()
	ctx.Dup(-1)
	ctx.PutPropString(-3, "exports")

	// Call the wrapper. Use the no-pump variant so we can inspect the
	// returned promise before the microtask queue is drained: F is async, and
	// if the body threw before reaching any top-level await its promise is
	// already rejected, which we want to surface as a synchronous load error
	// (exactly as the old sync wrapper did) rather than let the pump report it
	// as a deferred "async rule error".
	defer ctx.Pop() // pop the promise (or the synchronous exception)
	if r := ctx.PcallNoPump(2); r != 0 {
		ctx.PumpJobs()
		return ctx.GetESErrorAugmentingSyntaxErrors(path)
	}

	var loadErr error
	if ctx.PromiseStateTop() == duktape.PromiseRejected {
		// retract it from the unhandled-rejection tracker before the pump
		// flushes, so it is reported once (here) as a load error
		ctx.RetractTopPromiseRejection()
		ctx.PushPromiseResultTop()
		loadErr = ctx.GetESErrorAugmentingSyntaxErrors(path)
		ctx.Pop() // pop the rejection reason
	}

	// Now run microtasks: a top-level await continues here, and any unrelated
	// pending rejection is still reported. A pending promise (genuine
	// top-level await) just keeps running on later pumps.
	ctx.PumpJobs()
	return loadErr
}

func (ctx *ESContext) LoadFunctionFromString(filename, content string) error {
	return ctx.loadScriptFromStringFlags(filename, content, duktape.DUK_COMPILE_FUNCTION)
}

func (ctx *ESContext) LoadScriptFromString(filename, content string) error {
	if err := ctx.loadScriptFromStringFlags(filename, content, 0); err != nil {
		return err
	}

	defer ctx.Pop()
	if r := ctx.Pcall(0); r != 0 {
		return ctx.GetESErrorAugmentingSyntaxErrors(filename)
	}

	return nil
}

func (ctx *ESContext) loadScriptFromStringFlags(filename, content string, flags uint) error {
	ctx.PushString(filename)
	// we use PcompileStringFilename here to get readable stacktraces
	if r := ctx.PcompileStringFilename(flags, content); r != 0 {
		defer ctx.Pop()
		return ctx.GetESErrorAugmentingSyntaxErrors(filename)
	}
	return nil
}

func (ctx *ESContext) DefineFunctions(fns map[string]func(*ESContext) int) {
	for name, fn := range fns {
		f := fn
		factory := ctx.factory
		ctx.PushGoFunc(func(dctx *duktape.Context) int {
			if ctx, ok := factory.duktapeToESContextMap[*dctx]; ok {
				return f(ctx)
			}
			wbgong.Error.Panicf("No known conversion for duktape context to ESContext from %v", dctx)
			panic("")
		})
		ctx.PutPropString(-2, name)
	}
}

func (ctx *ESContext) Format() string {
	top := ctx.GetTop()
	if top < 1 {
		return ""
	}
	s := ctx.SafeToString(0)
	p := 1
	parts := strings.Split(s, "{{")
	buf := new(bytes.Buffer)
	for i, part := range parts {
		if i > 0 {
			buf.WriteString("{")
		}
		for j, subpart := range strings.Split(part, "{}") {
			if j > 0 && p < top {
				buf.WriteString(ctx.SafeToString(p))
				p++
			}
			buf.WriteString(subpart)
		}
	}
	// write remaining parts
	for ; p < top; p++ {
		buf.WriteString(" ")
		buf.WriteString(ctx.SafeToString(p))
	}
	return buf.String()
}

// QuickJS stack lines: "    at func (file:line:col)" or "    at file:line:col"
// (the latter for syntax errors). Native frames ("at fn (native)") don't match.
var fileRx = regexp.MustCompile(`^\s*at\s+(?:[^(]*\()?([^():]*):(\d+)(?::\d+)?\)?$`)

func (ctx *ESContext) GetESError() (r ESError) {
	r.Traceback = ESTraceback{}
	// Unlike Duktape, QuickJS's .stack holds only frame lines (no leading
	// "Error: msg"), so take the message from the error value itself.
	r.Message = ctx.SafeToString(-1)
	// the execution watchdog surfaces only as "InternalError: interrupted";
	// replace it with a message that says why (the traceback is still appended
	// below, so the offending location is kept)
	aborted := false
	if msg, ok := ctx.ExecTimeoutAbort(r.Message); ok {
		r.Message = msg
		aborted = true
	}
	if !ctx.GetPropString(-1, "stack") {
		ctx.Pop()
		return
	}
	stackStr := ctx.SafeToString(-1)
	if aborted {
		// the interpreter's own frame position is the statement before the
		// interrupted loop; the shim recorded the real one
		stackStr = ctx.RelocateAbortStack(stackStr)
	}
	stackLines := strings.Split(stackStr, "\n")
	r.Traceback = make(ESTraceback, 0, len(stackLines))
	for _, line := range stackLines {
		groups := fileRx.FindStringSubmatch(line)
		if groups != nil {
			lineNumber, err := strconv.Atoi(groups[2])
			if err != nil {
				wbgong.Warn.Printf("bad js line number: %d", lineNumber)
				continue
			}
			r.Traceback = append(r.Traceback, ESLocation{groups[1], lineNumber})
		}
	}
	if tr := ctx.factory.lineTranslator; tr != nil {
		for i := range r.Traceback {
			if src, ok := tr(r.Traceback[i].filename, r.Traceback[i].line); ok {
				r.Traceback[i].line = src
			}
		}
		stackStr = translateStackLines(stackStr, tr)
	}
	// Duktape's .stack embeds the message and wb-rules logged it whole;
	// reproduce that shape (message first, frame lines after).
	if len(r.Traceback) > 0 {
		r.Message = r.Message + "\n" + strings.TrimRight(stackStr, "\n")
	}
	ctx.Pop()
	return
}

var stackLineRefRx = regexp.MustCompile(`([^\s():]+\.ts):(\d+)`)

// translateStackLines rewrites file.ts:NN references in a stack text using
// the transpiler's source maps, so logged tracebacks show .ts source lines.
func translateStackLines(stack string, tr func(string, int) (int, bool)) string {
	return stackLineRefRx.ReplaceAllStringFunc(stack, func(ref string) string {
		g := stackLineRefRx.FindStringSubmatch(ref)
		n, err := strconv.Atoi(g[2])
		if err != nil {
			return ref
		}
		if src, ok := tr(g[1], n); ok {
			return fmt.Sprintf("%s:%d", g[1], src)
		}
		return ref
	})
}

// GetESErrorAugmentingSyntaxErrors is kept for its call sites: with
// Duktape, syntax errors carried no stack frames and the line number had
// to be recovered from the "(line N)" suffix of the message. QuickJS
// syntax errors come with a regular "at file:line" stack frame that
// GetESError already parses, so no augmentation is needed.
func (ctx *ESContext) GetESErrorAugmentingSyntaxErrors(path string) ESError {
	return ctx.GetESError()
}

func (ctx *ESContext) GetTraceback() ESTraceback {
	ctx.PushErrorObject(duktape.DUK_ERR_ERROR, "fake")
	defer ctx.Pop()
	return ctx.GetESError().Traceback
}

// get current filename from globals
func (ctx *ESContext) GetCurrentFilename() string {
	ctx.GetGlobalString(FILENAME_PROP_NAME)
	defer ctx.Pop()

	return ctx.GetString(-1)
}

func (ctx *ESContext) AddRule(name string, rule *Rule) error {
	if name == "" {
		// TODO: empty rules storage
		return nil
	}

	if _, found := ctx.ruleNames[name]; !found {
		ctx.ruleNames[name] = rule
		return nil
	} else {
		return fmt.Errorf("named rule redefinition: %s", name)
	}
}

// TBD: handle loops in object graphs in PushJSObject
// TBD: handle Go objects
// TBD: handle buffers
