package wbrules

import (
	"github.com/stretchr/objx"
	"github.com/stretchr/testify/assert"
	"testing"
)

var objTests = []string{
	`{}`,
	`{
           "x": 3,
           "y": "abc",
           "z": { "rr": 42 },
           "arrKey": [ 1, 2, "x", { "y": "zz" } ]
         }`,
}

func TestJSToObjx(t *testing.T) { // TBD: -> TestJSToObjxAndBack
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
