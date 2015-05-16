package wbrules

import (
	"bytes"
	"fmt"
	wbgo "github.com/contactless/wbgo"
	duktape "github.com/ivan4th/go-duktape"
	"github.com/stretchr/objx"
	"runtime"
	"strconv"
	"strings"
)

type ESCallback uint64
type ESCallbackFunc func(args objx.Map) interface{}

type ESContext struct {
	*duktape.Context
	callbackIndex ESCallback
}

func newESContext() *ESContext {
	ctx := &ESContext{
		duktape.NewContext(),
		1,
	}
	ctx.initGlobalObject("_esCallbacks")
	return ctx
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
		wbgo.Error.Println("buffers aren't supported yet")
		return nil
	case t.IsPointer():
		return ctx.GetPointer(objIndex)
	default:
		wbgo.Error.Panicf("bad object type %d", t)
		return nil // avoid compiler warning
	}
}

func (ctx *ESContext) GetJSObject(objIndex int) interface{} {
	return ctx.getJSObject(objIndex, true)
}

func (ctx *ESContext) PushJSObject(m objx.Map) {
	// FIXME: should do proper conversion, not just rely on JSON
	ctx.PushString(m.MustJSON())
	ctx.JsonDecode(-1)
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

func (ctx *ESContext) initGlobalObject(propName string) {
	// callback list stash property holds callback functions referenced by ids
	ctx.PushGlobalStash()
	ctx.PushObject()
	ctx.PutPropString(-2, propName)
	ctx.Pop()
}

func (ctx *ESContext) callbackKey(key ESCallback) string {
	return strconv.FormatUint(uint64(key), 16)
}

func (ctx *ESContext) invokeCallback(key ESCallback, args objx.Map) interface{} {
	ctx.PushGlobalStash()
	ctx.GetPropString(-1, "_esCallbacks")
	ctx.PushString(ctx.callbackKey(key))
	argCount := 0
	if args != nil {
		ctx.PushJSObject(args)
		argCount++
	}
	defer ctx.Pop3() // pop: result, callback list object, global stash
	if s := ctx.PcallProp(-2-argCount, argCount); s != 0 {
		wbgo.Error.Printf("failed to invoke callback %v: %s", key, ctx.SafeToString(-1))
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
	key := ctx.callbackIndex
	ctx.callbackIndex++

	ctx.PushGlobalStash()
	ctx.GetPropString(-1, "_esCallbacks")
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
	holder.ctx.RemoveCallback(holder.callback)
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

func (ctx *ESContext) RemoveCallback(key ESCallback) {
	ctx.PushGlobalStash()
	ctx.GetPropString(-1, "_esCallbacks")
	ctx.DelPropString(-1, ctx.callbackKey(key))
	ctx.Pop()
}

func (ctx *ESContext) LoadScript(path string) error {
	defer ctx.Pop()
	if r := ctx.PevalFile(path); r != 0 {
		ctx.GetPropString(-1, "stack")
		message := ctx.SafeToString(-1)
		ctx.Pop()
		if message == "" {
			message = ctx.SafeToString(-1)
		}
		return fmt.Errorf("failed to load %s: %s", path, message)
	}
	return nil
}

func (ctx *ESContext) LoadEmbeddedScript(filename, content string) error {
	ctx.PushString(filename)
	// we use PcompileStringFilename here to get readable stacktraces
	if r := ctx.PcompileStringFilename(0, content); r != 0 {
		defer ctx.Pop()
		return fmt.Errorf("failed to compile lib.js: %s", ctx.SafeToString(-1))
	}
	defer ctx.Pop()
	if r := ctx.Pcall(0); r != 0 {
		return fmt.Errorf("failed to run lib.js: %s", ctx.SafeToString(-1))
	}
	return nil
}

func (ctx *ESContext) DefineFunctions(fns map[string]func() int) {
	for name, fn := range fns {
		f := fn
		ctx.PushGoFunc(func(*duktape.Context) int {
			return f()
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

// TBD: proper PushJSObject
// TBD: handle loops
// TBD: handle Go objects
// TBD: handle buffers
