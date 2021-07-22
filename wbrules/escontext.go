package wbrules

import (
	"bytes"
	"fmt"
	"io/ioutil"
	"log"
	"reflect"
	"regexp"
	"runtime"
	"strconv"
	"strings"

	"github.com/contactless/wbgong"
	"github.com/stretchr/objx"
	duktape "github.com/wirenboard/go-duktape"
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
type ESCallbackFunc func(args objx.Map) interface{}
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
	ctx := &ESContext{
		dctx,     // *duktape.Context
		syncFunc, // syncFunc
		nil,      // callbackErrorHandler
		f,        // factory
		make(map[string]*Rule),
		true, // validation flag
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

	ctx.Context = nil
	ctx.valid = false
}

func (ctx *ESContext) assertStackClean(stack_top int) {
	if ctx.GetTop() != stack_top {
		wbgong.Error.Panicf("stack top assertion failed: expected %d, got %d", stack_top, ctx.GetTop())
	}
}

func (ctx *ESContext) IsValid() bool {
	return ctx.valid
}

func (ctx *ESContext) mustBeValid() {
	if !ctx.valid {
		panic("operation on invalid context")
	}
}

func (ctx *ESContext) DefaultCallbackErrorHandler(err ESError) {
	wbgong.Error.Printf("failed to invoke callback in context %p: %s", ctx, err)
}

func (ctx *ESContext) SetCallbackErrorHandler(handler ESCallbackErrorHandler) {
	ctx.callbackErrorHandler = handler
}

func (ctx *ESContext) getObject(objIndex int) map[string]interface{} {
	m := make(map[string]interface{})
	ctx.Enum(-1, duktape.DUK_ENUM_OWN_PROPERTIES_ONLY)
	for ctx.Next(-1, true) {
		key := ctx.SafeToString(-2)
		m[key] = ctx.getJSObject(-1, false)
		ctx.Pop2()
	}
	ctx.Pop()
	return m
}

func (ctx *ESContext) getArray(objIndex int) []interface{} {
	// FIXME: this will not work for arrays with length >= 2^32
	r := make([]interface{}, ctx.GetLength(objIndex))
	ctx.Enum(-1, duktape.DUK_ENUM_ARRAY_INDICES_ONLY)
	for ctx.Next(-1, true) {
		n := ctx.ToInt(-2)
		r[n] = ctx.getJSObject(-1, false)
		ctx.Pop2()
	}
	ctx.Pop()
	return r
}

func (ctx *ESContext) getJSObject(objIndex int, top bool) interface{} {
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
		} else {
			return m
		}
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

func (ctx *ESContext) GetJSObject(objIndex int) interface{} {
	return ctx.getJSObject(objIndex, true)
}

func (ctx *ESContext) PushJSObject(obj interface{}) {
	if obj == nil {
		ctx.PushNull()
		return
	}
	switch obj.(type) {
	case string:
		ctx.PushString(obj.(string))
	case objx.Map:
		ctx.PushJSObject(map[string]interface{}(obj.(objx.Map)))
	case map[string]interface{}:
		ctx.PushObject()
		for k, v := range obj.(map[string]interface{}) {
			ctx.PushJSObject(v)
			ctx.PutPropString(-2, k)
		}
	case bool:
		ctx.PushBoolean(obj.(bool))
	case float32:
		ctx.PushNumber(float64(obj.(float32)))
	case float64:
		ctx.PushNumber(obj.(float64))
	case int:
		ctx.PushNumber(float64(obj.(int)))
	case uint8:
		ctx.PushNumber(float64(obj.(uint8)))
	case uint16:
		ctx.PushNumber(float64(obj.(uint16)))
	case uint32:
		ctx.PushNumber(float64(obj.(uint32)))
	case uint64:
		ctx.PushNumber(float64(obj.(uint64)))
	case int8:
		ctx.PushNumber(float64(obj.(int8)))
	case int16:
		ctx.PushNumber(float64(obj.(int16)))
	case int32:
		ctx.PushNumber(float64(obj.(int32)))
	case int64:
		ctx.PushNumber(float64(obj.(int64)))
	default:
		ctx.pushJSObjectUsingReflection(obj)
	}
}

func (ctx *ESContext) pushJSObjectUsingReflection(obj interface{}) {
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

func (ctx *ESContext) invokeCallback(key ESCallback, args objx.Map) interface{} {
	ctx.mustBeValid()
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
	return func(args objx.Map) interface{} {
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
	if !ctx.IsValid() {
		return
	}

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

var syntaxErrorRx = regexp.MustCompile(`^SyntaxError:.*?\(line\s+(\d+)\)\s*(\n|$)`)

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
	srcRaw, err := ioutil.ReadFile(path)

	if err != nil {
		return err
	}

	// wrap source code
	src := "function(module){" + string(srcRaw) + "}"

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

	// call function
	defer ctx.Pop()
	if r := ctx.Pcall(1); r != 0 {
		return ctx.GetESErrorAugmentingSyntaxErrors(path)
	}

	return nil
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
			} else {
				wbgong.Error.Panicf("No known conversion for duktape context to ESContext from %v", dctx)
				panic("")
			}
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

var fileRx = regexp.MustCompile(`^\s*\S+\s+(.*):(\d+)(?:\s+[^:]*)?$`)

func (ctx *ESContext) GetESError() (r ESError) {
	r.Traceback = ESTraceback{}
	if !ctx.GetPropString(-1, "stack") {
		r.Message = ctx.SafeToString(-1)
		ctx.Pop()
		return
	}
	stackLines := strings.Split(ctx.SafeToString(-1), "\n")
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
	r.Message = ctx.SafeToString(-1)
	ctx.Pop()
	return
}

func (ctx *ESContext) GetESErrorAugmentingSyntaxErrors(path string) (r ESError) {
	// SyntaxError have no script files in their stack trace,
	// but provide line number info in the message
	// FIXME: need to use ctx.GetErrorCode() to check
	// for SyntaxError (requires newer duktape)
	r = ctx.GetESError()
	if len(r.Traceback) != 0 {
		return
	}

	groups := syntaxErrorRx.FindStringSubmatch(r.Message)
	if groups == nil {
		return
	}

	lineNumber, err := strconv.Atoi(groups[1])
	if err != nil {
		wbgong.Warn.Printf("bad js line number: %d", lineNumber)
		return
	}

	r = ESError{
		r.Message,
		ESTraceback{
			{filename: path, line: lineNumber},
		},
	}
	return
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
