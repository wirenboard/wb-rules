package wbrules

import (
	"github.com/stretchr/objx"
	"github.com/stretchr/testify/assert"
	"math"
	"testing"
)

var objTests = []string{
	`{}`,
	`{
           "x": 3,
           "y": "abc",
           "z": { "rr": 42 },
           "arrKey": [ 1, 2, "x", { "y": "zz" }, null, false, true ]
         }`,
}

func TestJSToObjxAndBack(t *testing.T) {
	ctx := newESContext(nil)
	for _, jsonStr := range objTests {
		if r := ctx.PevalString("(" + jsonStr + ")"); r != 0 {
			t.Fatal("failed to evaluate the script")
		}
		object := ctx.GetJSObject(-1)
		ctx.Pop()
		json := objx.MustFromJSON(jsonStr)
		assert.Equal(t, json, object)

		ctx.PushGlobalObject()
		ctx.PushJSObject(object.(objx.Map))
		ctx.PutPropString(-2, "jso")
		if r := ctx.PevalString("JSON.stringify(jso)"); r != 0 {
			t.Fatal("failed to evaluate the script")
		}
		jsonStr1 := ctx.SafeToString(-1)
		ctx.Pop()
		json1 := objx.MustFromJSON(jsonStr1)
		assert.Equal(t, json, json1)
	}
}

func TestNumConversions(t *testing.T) {
	ctx := newESContext(nil)
	ctx.PushJSObject(objx.Map{
		"v_uint8":   uint8(0xf0),
		"v_uint16":  uint16(0xf001),
		"v_uint32":  uint32(0xf0000001),
		"v_uint64":  uint64(0xf00000001),
		"v_int8":    int8(-1),
		"v_int16":   int16(-2),
		"v_int32":   int32(-3),
		"v_int64":   int64(-4),
		"v_int":     int(-5),
		"v_float32": float32(-1.5),
		"v_float64": float64(-2.5),
		"nan_32":    float32(math.NaN()),
		"nan_64":    float32(math.NaN()),
	})
	expected := objx.Map{
		"v_uint8":   float64(0xf0),
		"v_uint16":  float64(0xf001),
		"v_uint32":  float64(0xf0000001),
		"v_uint64":  float64(0xf00000001),
		"v_int8":    float64(-1),
		"v_int16":   float64(-2),
		"v_int32":   float64(-3),
		"v_int64":   float64(-4),
		"v_int":     float64(-5),
		"v_float32": float64(-1.5),
		"v_float64": float64(-2.5),
		"nan_32":    math.NaN(),
		"nan_64":    math.NaN(),
	}
	actual := ctx.GetJSObject(-1).(objx.Map)
	for k, v := range expected {
		assert.Equal(t, v, actual[k])
	}
	assert.Equal(t, len(expected), len(actual))
}
