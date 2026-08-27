//go:build dumpleaks

package duktape

// Shim-level leak check against QuickJS's own DUMP_LEAKS instrumentation.
// Built only with -tags dumpleaks, which adds -DDUMP_LEAKS=1 to the QuickJS
// build (see the #cgo directive in duktape.go): every object, atom and
// string still alive when JS_FreeRuntime runs is then dumped to stdout as
// "Object leaks:" / "Atom leaks:" / "String leaks:" tables.
//
// The test exercises the shim API surface end to end and destroys the heap;
// a clean run prints nothing. The dump cannot be read from inside this
// process (C stdio, flushed at exit), so the assertion lives in
// TestQuickJSDumpLeaks (dumpleaks_driver_test.go), which runs this test as
// a subprocess and fails on any leak table in the captured output. Direct
// use:
//
//	QJS_DUMP_LEAKS=1 go test -run TestQuickJSDumpLeaks -v .
//
// or manually: go test -tags dumpleaks -run TestDumpLeaksHeapExercise -v .

import (
	"strings"
	"testing"
	"time"
)

func TestDumpLeaksHeapExercise(t *testing.T) {
	ctx := NewContext()
	jobErrors := 0
	ctx.SetJobErrorHandler(func(string) { jobErrors++ })

	// --- eval, values, strings ---
	if r := ctx.PevalString("'abc' + 1 + true"); r != 0 {
		t.Fatalf("eval: %s", ctx.SafeToString(-1))
	}
	if got := ctx.SafeToString(-1); got != "abc1true" {
		t.Fatalf("eval result: %q", got)
	}
	ctx.Pop()

	// eval error path: exception caught, stringified, stack read
	if r := ctx.PevalString("(function boom(){ throw new Error('x') })()"); r == 0 {
		t.Fatal("expected eval error")
	}
	ctx.GetPropString(-1, "stack")
	if s := ctx.SafeToString(-1); !strings.Contains(s, "boom") {
		t.Fatalf("stack: %q", s)
	}
	ctx.Pop2()

	// syntax error path
	if r := ctx.PevalString("this is not js"); r == 0 {
		t.Fatal("expected syntax error")
	}
	ctx.Pop()

	// --- Go functions, Go objects, duk-style rc errors ---
	ctx.PushGoFunc(func(d *Context) int {
		if d.GetString(0) == "fail" {
			return DUK_RET_TYPE_ERROR
		}
		if d.GetString(0) == "throw" {
			d.PushErrorObject(DUK_ERR_ERROR, "from-go")
			return DUK_RET_INSTACK_ERROR
		}
		d.PushString("go:" + d.SafeToString(0))
		return 1
	})
	ctx.PutGlobalString("gofn")
	for _, src := range []string{
		"gofn('ok')",
		"try { gofn('fail') } catch (e) { '' + e }",
		"try { gofn('throw') } catch (e) { '' + e }",
	} {
		if r := ctx.PevalString(src); r != 0 {
			t.Fatalf("gofn eval: %s", ctx.SafeToString(-1))
		}
		ctx.Pop()
	}

	type payload struct{ n int }
	ctx.PushGoObject(&payload{42})
	if p, ok := ctx.GetGoObject(-1).(*payload); !ok || p.n != 42 {
		t.Fatal("GetGoObject roundtrip failed")
	}
	ctx.Pop()

	// --- heap stash: store / invoke / delete a callback (wb-rules protocol) ---
	ctx.PushHeapStash()
	ctx.PevalString("(function (x) { return x * 2 })")
	ctx.PutPropString(-2, "cb")
	ctx.GetPropString(-1, "cb")
	ctx.PushNumber(21)
	if r := ctx.Pcall(1); r != 0 {
		t.Fatalf("stash callback: %s", ctx.SafeToString(-1))
	}
	if ctx.GetNumber(-1) != 42 {
		t.Fatal("stash callback result")
	}
	ctx.Pop()
	ctx.DelPropString(-1, "cb")
	ctx.Pop()

	// --- JSON: encode, decode, checked-encode failure (cycle) ---
	ctx.PevalString("({a: [1, 2, {b: 'c'}]})")
	if s := ctx.JsonEncode(-1); s != `{"a":[1,2,{"b":"c"}]}` {
		t.Fatalf("JsonEncode: %q", s)
	}
	ctx.JsonDecode(-1)
	ctx.Pop()
	ctx.PevalString("(function () { var o = {}; o.self = o; return o })()")
	if _, err := ctx.JsonEncodeChecked(-1); err == nil {
		t.Fatal("expected cycle error from JsonEncodeChecked")
	}
	ctx.Pop()

	// --- enumeration protocol ---
	ctx.PevalString("({x: 1, y: 2, z: 3})")
	ctx.Enum(-1, DUK_ENUM_OWN_PROPERTIES_ONLY)
	seen := 0
	for ctx.Next(-1, true) {
		seen++
		ctx.Pop2()
	}
	if seen != 3 {
		t.Fatalf("enum saw %d keys", seen)
	}
	ctx.Pop2()

	// --- compile protocols ---
	ctx.PushString("fake.js")
	if r := ctx.PcompileStringFilename(DUK_COMPILE_FUNCTION, "function (a) { return a + 1 }"); r != 0 {
		t.Fatalf("compile function: %s", ctx.SafeToString(-1))
	}
	ctx.PushNumber(1)
	if r := ctx.Pcall(1); r != 0 {
		t.Fatalf("call compiled: %s", ctx.SafeToString(-1))
	}
	ctx.Pop()
	if r := ctx.PcompileString(0, "1 + 2"); r != 0 {
		t.Fatalf("compile program: %s", ctx.SafeToString(-1))
	}
	if r := ctx.Pcall(0); r != 0 {
		t.Fatalf("run program: %s", ctx.SafeToString(-1))
	}
	ctx.Pop()

	// --- constructor + method-call protocols ---
	ctx.PevalString("(function Thing(v) { this.v = v })")
	ctx.PushNumber(7)
	ctx.New(1)
	ctx.PushString("hasOwnProperty")
	ctx.PushString("v")
	if r := ctx.PcallProp(-3, 1); r != 0 {
		t.Fatalf("PcallProp: %s", ctx.SafeToString(-1))
	}
	ctx.Pop2()

	// --- require / modSearch (hit + miss) ---
	ctx.GetGlobalString("Duktape")
	ctx.PushGoFunc(func(d *Context) int {
		if d.GetString(0) != "mod" {
			d.PushErrorObject(DUK_ERR_ERROR, "module not found")
			return DUK_RET_INSTACK_ERROR
		}
		d.PushString("exports.n = 6 * 7;")
		return 1
	})
	ctx.PutPropString(-2, "modSearch")
	ctx.Pop()
	if r := ctx.PevalString("require('mod').n + (function () { try { require('nope') } catch (e) { return 0 } })()"); r != 0 {
		t.Fatalf("require: %s", ctx.SafeToString(-1))
	}
	ctx.Pop()

	// --- promises: settled chains, rejections (handled + reported), retraction ---
	for _, src := range []string{
		"(async function () { await Promise.resolve(1); return 2 })()",
		"(async function () { try { await Promise.reject(new Error('h')) } catch (e) {} })()",
		"(async function () { throw new Error('unhandled') })()",
		"(function () { var w = Promise.withResolvers(); w.promise.then(function () {}); w.resolve(1); return w.promise })()",
	} {
		if r := ctx.PevalString(src); r != 0 {
			t.Fatalf("promise eval: %s", ctx.SafeToString(-1))
		}
		ctx.Pop()
	}
	if jobErrors == 0 {
		t.Fatal("unhandled rejection was not reported")
	}
	// rejected promise inspected and retracted (the LoadScenario protocol)
	if r := ctx.PevalString("(async function () { throw new Error('load') })()"); r != 0 {
		t.Fatalf("tla eval: %s", ctx.SafeToString(-1))
	}
	if ctx.PromiseStateTop() != PromiseRejected {
		t.Fatal("expected rejected promise")
	}
	ctx.RetractTopPromiseRejection()
	ctx.PushPromiseResultTop()
	ctx.Pop2()
	ctx.PumpJobs()

	// --- realms: parked promises, cross-realm stash, handle GC + reap ---
	for i := 0; i < 3; i++ {
		ctx.PushThreadNewGlobalenv()
		sub := ctx.GetContext(-1)
		if sub == nil {
			t.Fatal("realm creation failed")
		}
		if r := sub.PevalString(
			"var w = Promise.withResolvers();" +
				"(async function () { await w.promise })();" +
				"(async function () { await new Promise(function () {}) })();" +
				"require === require"); r != 0 {
			t.Fatalf("realm eval: %s", sub.SafeToString(-1))
		}
		sub.Pop()
		ctx.Pop() // handle becomes garbage; realm reaped at a safe point
	}
	ctx.RunGC()
	ctx.PushUndefined()
	ctx.Pop() // reap point
	ctx.RunGC()

	// --- watchdog abort path ---
	ctx.SetExecutionTimeLimit(50 * time.Millisecond)
	if r := ctx.PevalString("while (true) {}"); r == 0 {
		t.Fatal("runaway was not interrupted")
	}
	if _, aborted := ctx.ExecTimeoutAbort(ctx.SafeToString(-1)); !aborted {
		t.Fatalf("expected watchdog abort, got: %s", ctx.SafeToString(-1))
	}
	ctx.Pop()
	ctx.SetExecutionTimeLimit(0)

	// --- memory limit setter (exercise both directions) ---
	ctx.SetMemoryLimit(64 * 1024 * 1024)
	ctx.SetMemoryLimit(0)

	if n := ctx.GetTop(); n != 0 {
		t.Fatalf("stack not clean before destroy: %d values", n)
	}

	ctx.DestroyHeap()

	// every Go value referenced from JS must have been finalized with the heap
	if n := GoRegistrySize(); n != 0 {
		t.Fatalf("go registry holds %d entries after DestroyHeap", n)
	}
}
