package duktape

import (
	"strings"
	"testing"
)

func TestEvalBasics(t *testing.T) {
	ctx := NewContext()
	defer ctx.DestroyHeap()
	if r := ctx.PevalString("1 + 2"); r != 0 {
		t.Fatalf("eval failed: %s", ctx.SafeToString(-1))
	}
	if !ctx.IsNumber(-1) || ctx.GetNumber(-1) != 3 {
		t.Fatalf("bad result: %v", ctx.SafeToString(-1))
	}
	ctx.Pop()
	if ctx.GetTop() != 0 {
		t.Fatalf("stack not clean: %d", ctx.GetTop())
	}
	if r := ctx.PevalString("class Foo { #x = 1; get x() { return this.#x } }; new Foo().x"); r != 0 {
		t.Fatalf("ES2020+ eval failed: %s", ctx.SafeToString(-1))
	}
	ctx.Pop()
}

func TestGoFuncAndThis(t *testing.T) {
	ctx := NewContext()
	defer ctx.DestroyHeap()
	ctx.PushGlobalObject()
	ctx.PushGoFunc(func(d *Context) int {
		if d.GetTop() != 2 {
			t.Errorf("expected 2 args, got %d", d.GetTop())
		}
		sum := d.GetNumber(0) + d.GetNumber(1)
		d.PushThis()
		d.GetPropString(-1, "bonus")
		sum += d.GetNumber(-1)
		d.Pop2()
		d.PushNumber(sum)
		return 1
	})
	ctx.PutPropString(-2, "add")
	ctx.PushString("obj")
	ctx.Pop2()

	if r := ctx.PevalString("var o = {bonus: 10, add: add}; o.add(1, 2)"); r != 0 {
		t.Fatalf("eval failed: %s", ctx.SafeToString(-1))
	}
	if got := ctx.GetNumber(-1); got != 13 {
		t.Fatalf("expected 13, got %v", got)
	}
	ctx.Pop()
}

func TestHeapStashShared(t *testing.T) {
	ctx := NewContext()
	defer ctx.DestroyHeap()
	ctx.PushHeapStash()
	ctx.PushString("hello")
	ctx.PutPropString(-2, "greeting")
	ctx.Pop()

	// New realm sees the same stash.
	ctx.PushThreadNewGlobalenv()
	ctx2 := ctx.GetContext(-1)
	if ctx2 == nil {
		t.Fatal("GetContext returned nil")
	}
	ctx2.PushHeapStash()
	if !ctx2.GetPropString(-1, "greeting") || ctx2.GetString(-1) != "hello" {
		t.Fatalf("stash not shared: %q", ctx2.SafeToString(-1))
	}
	ctx2.Pop2()
	ctx.Pop() // thread handle
}

func TestRealmGlobalPrototypeChain(t *testing.T) {
	// The wb-rules pattern: build an API object in the primary realm, then
	// make it the prototype of a new realm's global object.
	ctx := NewContext()
	defer ctx.DestroyHeap()

	if r := ctx.PevalString("this.apiFunc = function() { return 'api-result' }; this"); r != 0 {
		t.Fatalf("setup eval: %s", ctx.SafeToString(-1))
	}
	ctx.PushHeapStash()
	ctx.Dup(-2)
	ctx.PutPropString(-2, "proto")
	ctx.Pop2()

	ctx.PushThreadNewGlobalenv()
	sub := ctx.GetContext(-1)

	// realm globals are fresh
	sub.PushGlobalObject()
	if sub.HasPropString(-1, "apiFunc") {
		t.Fatal("fresh realm should not see primary globals")
	}
	sub.Pop()

	// set prototype of the realm's global to the stashed API object
	sub.PushGlobalObject()
	sub.PushHeapStash()
	sub.GetPropString(-1, "proto")
	sub.Remove(-2) // drop stash: [ global proto ]
	sub.SetPrototype(-2)
	sub.Pop()

	if r := sub.PevalString("apiFunc()"); r != 0 {
		t.Fatalf("inherited global lookup failed: %s", sub.SafeToString(-1))
	}
	if got := sub.GetString(-1); got != "api-result" {
		t.Fatalf("expected api-result, got %q", got)
	}
	sub.Pop()

	// realm-local shadowing must not leak into other realms
	if r := sub.PevalString("var localVar = 42; localVar"); r != 0 {
		t.Fatalf("realm eval: %s", sub.SafeToString(-1))
	}
	sub.Pop()
	if r := ctx.PevalString("typeof localVar"); r != 0 {
		t.Fatal("typeof eval failed")
	}
	if got := ctx.GetString(-1); got != "undefined" {
		t.Fatalf("realm leak: localVar visible as %q in primary", got)
	}
	ctx.Pop()
	ctx.Pop() // thread handle
}

func TestEnumProtocol(t *testing.T) {
	ctx := NewContext()
	defer ctx.DestroyHeap()
	if r := ctx.PevalString("({a: 1, b: 'two', c: true})"); r != 0 {
		t.Fatal("eval failed")
	}
	m := map[string]string{}
	ctx.Enum(-1, DUK_ENUM_OWN_PROPERTIES_ONLY)
	for ctx.Next(-1, true) {
		m[ctx.SafeToString(-2)] = ctx.SafeToString(-1)
		ctx.Pop2()
	}
	ctx.Pop2() // enum + obj
	if len(m) != 3 || m["a"] != "1" || m["b"] != "two" || m["c"] != "true" {
		t.Fatalf("bad enum result: %v", m)
	}
}

func TestCallbackProtocol(t *testing.T) {
	// Mimics escontext.go: store a callback in a stash object, invoke via
	// PcallProp with a pushed key.
	ctx := NewContext()
	defer ctx.DestroyHeap()
	ctx.PushHeapStash()
	ctx.PushObject()
	ctx.PutPropString(-2, "_cbs")
	ctx.Pop()

	if r := ctx.PevalString("(function(arg) { return arg.x * 2 })"); r != 0 {
		t.Fatal("eval failed")
	}
	ctx.PushHeapStash()
	ctx.GetPropString(-1, "_cbs")
	ctx.Dup(-3)
	ctx.PutPropString(-2, "1")
	ctx.Pop3()

	ctx.PushHeapStash()
	ctx.GetPropString(-1, "_cbs")
	ctx.PushString("1")
	ctx.PushObject()
	ctx.PushNumber(21)
	ctx.PutPropString(-2, "x")
	if s := ctx.PcallProp(-3, 1); s != 0 {
		t.Fatalf("PcallProp failed: %s", ctx.SafeToString(-1))
	}
	if got := ctx.GetNumber(-1); got != 42 {
		t.Fatalf("expected 42, got %v", got)
	}
	ctx.Pop3()
}

func TestRequireModSearch(t *testing.T) {
	ctx := NewContext()
	defer ctx.DestroyHeap()

	// Install a modSearch like wb-rules does.
	ctx.GetGlobalString("Duktape")
	ctx.PushGoFunc(func(d *Context) int {
		id := d.GetString(0)
		if id != "testmod" {
			d.PushErrorObject(DUK_ERR_ERROR, "module not found: "+id)
			return DUK_RET_INSTACK_ERROR
		}
		// set module.filename like engine.ModSearch
		d.PushString("/fake/testmod.js")
		d.PutPropString(3, "filename")
		d.PushString("exports.answer = 42; exports.name = module.id;")
		return 1
	})
	ctx.PutPropString(-2, "modSearch")
	ctx.Pop()

	if r := ctx.PevalString("require('testmod').answer + ':' + require('testmod').name"); r != 0 {
		t.Fatalf("require failed: %s", ctx.SafeToString(-1))
	}
	if got := ctx.SafeToString(-1); got != "42:testmod" {
		t.Fatalf("bad require result: %q", got)
	}
	ctx.Pop()

	// missing module → catchable error
	if r := ctx.PevalString("try { require('nope'); 'no-error' } catch(e) { 'caught' }"); r != 0 {
		t.Fatalf("eval failed: %s", ctx.SafeToString(-1))
	}
	if got := ctx.GetString(-1); got != "caught" {
		t.Fatalf("expected caught, got %q", got)
	}
	ctx.Pop()
}

func TestErrorStackFormat(t *testing.T) {
	ctx := NewContext()
	defer ctx.DestroyHeap()
	if r := ctx.PevalString("function f(){throw new Error('boom')}; f()"); r == 0 {
		t.Fatal("expected error")
	}
	if !ctx.GetPropString(-1, "stack") {
		t.Fatal("no .stack on thrown error")
	}
	stack := ctx.SafeToString(-1)
	if !strings.Contains(stack, "at f") || !strings.Contains(stack, "input:1") {
		t.Fatalf("unexpected stack format: %q", stack)
	}
	ctx.Pop2()
}

func TestCompileFunctionAndProgram(t *testing.T) {
	ctx := NewContext()
	defer ctx.DestroyHeap()

	// DUK_COMPILE_FUNCTION with filename pushed (loadScriptFromStringFlags protocol)
	ctx.PushString("myfile.js")
	if r := ctx.PcompileStringFilename(DUK_COMPILE_FUNCTION, "function F(module){ return 'got:' + module.filename }"); r != 0 {
		t.Fatalf("compile function failed: %s", ctx.SafeToString(-1))
	}
	if !ctx.IsFunction(-1) {
		t.Fatal("expected function on stack")
	}
	ctx.PushObject()
	ctx.PushString("mod.js")
	ctx.PutPropString(-2, "filename")
	if r := ctx.Pcall(1); r != 0 {
		t.Fatalf("call failed: %s", ctx.SafeToString(-1))
	}
	if got := ctx.GetString(-1); got != "got:mod.js" {
		t.Fatalf("bad result %q", got)
	}
	ctx.Pop()

	// program compile + Pcall(0)
	ctx.PushString("prog.js")
	if r := ctx.PcompileStringFilename(0, "var progVar = 'set-by-program'; progVar"); r != 0 {
		t.Fatalf("program compile failed: %s", ctx.SafeToString(-1))
	}
	if r := ctx.Pcall(0); r != 0 {
		t.Fatalf("program run failed: %s", ctx.SafeToString(-1))
	}
	ctx.Pop()
	if r := ctx.PevalString("progVar"); r != 0 || ctx.GetString(-1) != "set-by-program" {
		t.Fatalf("program globals lost: %q", ctx.SafeToString(-1))
	}
	ctx.Pop()

	// syntax error path
	ctx.PushString("bad.js")
	if r := ctx.PcompileStringFilename(DUK_COMPILE_EVAL, "var x = ;"); r == 0 {
		t.Fatal("expected syntax error")
	}
	msg := ctx.SafeToString(-1)
	if !strings.Contains(msg, "SyntaxError") {
		t.Fatalf("expected SyntaxError, got %q", msg)
	}
	ctx.Pop()
}

func TestLibJSProxyPattern(t *testing.T) {
	ctx := NewContext()
	defer ctx.DestroyHeap()

	// _wbCellObject equivalent: a Go object with Go-func methods
	ctx.PushGlobalObject()
	ctx.PushGoFunc(func(d *Context) int {
		d.PushGoObject("cell-proxy-payload")
		fns := map[string]func(*Context) int{
			"isComplete": func(d *Context) int { d.PushBoolean(false); return 1 },
		}
		for name, f := range fns {
			d.PushGoFunc(f)
			d.PutPropString(-2, name)
		}
		return 1
	})
	ctx.PutPropString(-2, "_wbCellObject")
	ctx.Pop()

	script := `
var log = [];
var IncompleteCellCaught = (function () {
  function IncompleteCellCaught(cellName) {
    this.name = 'IncompleteCellCaught';
    this.message = 'incomplete cell encountered: ' + cellName;
  }
  IncompleteCellCaught.prototype = Object.create(Error.prototype);
  return IncompleteCellCaught;
})();
var requireCompleteCells = 0;
var dev = new Proxy({}, {
  get: function (o, name) {
    var cell = _wbCellObject(name);
    if (requireCompleteCells && !cell.isComplete())
      throw new IncompleteCellCaught(name);
    return 42;
  }
});
function wrap(f) {
  return function () {
    requireCompleteCells++;
    try { return f.apply(null, arguments); }
    catch (e) {
      if (e instanceof IncompleteCellCaught) { log.push('swallowed: ' + e.message); return undefined; }
      throw e;
    } finally { requireCompleteCells--; }
  };
}
var cond = wrap(function () { return dev.sw; });
var r = cond();
log.push('result: ' + r);
log.join(' | ')
`
	if r := ctx.PevalString(script); r != 0 {
		t.Fatalf("script failed: %s", ctx.SafeToString(-1))
	}
	got := ctx.GetString(-1)
	ctx.Pop()
	want := "swallowed: incomplete cell encountered: sw | result: undefined"
	if got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}
