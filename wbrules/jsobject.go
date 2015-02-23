package wbrules

import (
	"log"
	"github.com/stretchr/objx"
	duktape "github.com/ivan4th/go-duktape"
)

func getObject(ctx *duktape.Context, objIndex int) map[string]interface{} {
	m := make(map[string]interface{})
	ctx.Enum(-1, duktape.DUK_ENUM_OWN_PROPERTIES_ONLY)
	for ctx.Next(-1, true) {
		key := ctx.SafeToString(-2)
		m[key] = getJSObject(ctx, -1, false)
		ctx.Pop2()
	}
	ctx.Pop()
	return m
}

func getArray(ctx *duktape.Context, objIndex int) []interface{} {
	// FIXME: this will not work for arrays with length >= 2^32
	r := make([]interface{}, ctx.GetLength(objIndex))
	ctx.Enum(-1, duktape.DUK_ENUM_ARRAY_INDICES_ONLY)
	for ctx.Next(-1, true) {
		n := ctx.ToInt(-2)
		r[n] = getJSObject(ctx, -1, false)
		ctx.Pop2()
	}
	ctx.Pop()
	return r
}

func getJSObject(ctx *duktape.Context, objIndex int, top bool) interface{} {
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
			return getArray(ctx, objIndex)
		}
		m := getObject(ctx, objIndex)
		if top {
			return objx.New(m)
		} else {
			return m
		}
	case t.IsBuffer():
		log.Println("WARNING: buffers aren't supported yet")
		return nil
	case t.IsPointer():
		return ctx.GetPointer(objIndex)
	default:
		log.Panicf("bad object type %d", t)
		return nil // avoid compiler warning
	}
}

func GetJSObject(ctx *duktape.Context, objIndex int) interface{} {
	return getJSObject(ctx, objIndex, true)
}

func PushJSObject(ctx *duktape.Context, m objx.Map) {
	// FIXME: should do proper conversion, not just rely on JSON
	ctx.PushString(m.MustJSON())
	ctx.JsonDecode(-1)
}

// TBD: proper PushJSObject
// TBD: handle loops
// TBD: handle Go objects
// TBD: handle buffers
