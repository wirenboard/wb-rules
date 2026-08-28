package duktape

import (
	"strings"
	"testing"
	"time"
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

func TestRequireModuleHost(t *testing.T) {
	ctx := NewContext()
	defer ctx.DestroyHeap()
	ctx.SetModuleHost(&testModuleHost{files: map[string]string{
		"/mods/testmod.js": "exports.answer = 42; exports.name = module.id;",
	}})

	if r := ctx.PevalString("require('testmod').answer + ':' + require('testmod').name"); r != 0 {
		t.Fatalf("require failed: %s", ctx.SafeToString(-1))
	}
	if got := ctx.SafeToString(-1); got != "42:testmod" {
		t.Fatalf("bad require result: %q", got)
	}
	ctx.Pop()

	// missing module → catchable error carrying a code
	if r := ctx.PevalString("try { require('nope'); 'no-error' } catch(e) { e.code + ':' + e.message }"); r != 0 {
		t.Fatalf("eval failed: %s", ctx.SafeToString(-1))
	}
	if got := ctx.GetString(-1); got != `MODULE_NOT_FOUND:cannot find module "nope"` {
		t.Fatalf("unexpected error: %q", got)
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

func TestExecutionTimeLimit(t *testing.T) {
	ctx := NewContext()
	defer ctx.DestroyHeap()
	ctx.SetExecutionTimeLimit(200 * time.Millisecond)
	rc := make(chan int, 1)
	go func() { rc <- ctx.PevalString("while (true) {}") }()
	select {
	case r := <-rc:
		if r == 0 {
			t.Fatal("runaway loop finished without error")
		}
	case <-time.After(10 * time.Second):
		// fail cleanly instead of hanging the binary if the interrupt dies
		t.Fatal("interrupt did not fire within 10s")
	}
	ctx.Pop()
	// the context must remain usable after an interrupt
	ctx.SetExecutionTimeLimit(0)
	if r := ctx.PevalString("6 * 7"); r != 0 {
		t.Fatal("eval after interrupt failed")
	}
	if v := ctx.GetNumber(-1); v != 42 {
		t.Fatalf("got %v", v)
	}
	ctx.Pop()
}

func TestExecutionTimeLimitInPromiseJob(t *testing.T) {
	ctx := NewContext()
	defer ctx.DestroyHeap()
	ctx.SetExecutionTimeLimit(200 * time.Millisecond)
	done := make(chan int, 1)
	go func() {
		// the spinning .then reaction runs during the post-eval job pump
		done <- ctx.PevalString(`Promise.resolve().then(function(){ for(;;){} }); 1`)
	}()
	select {
	case r := <-done:
		if r != 0 {
			t.Fatalf("top-level eval failed: rc=%d", r)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("promise job was not interrupted; engine would hang forever")
	}
	ctx.Pop()
}

// A Go callback that re-enters JS from inside a promise job must not
// disarm the watchdog for the rest of the job, and must not drain other
// pending jobs mid-job (run-to-completion).
func TestExecutionTimeLimitSurvivesNestedEntryInJob(t *testing.T) {
	ctx := NewContext()
	defer ctx.DestroyHeap()
	ctx.SetExecutionTimeLimit(200 * time.Millisecond)

	ran := []string{}
	ctx.PushGlobalObject()
	ctx.PushGoFunc(func(d *Context) int {
		// nested JS entry: depth 0 -> 1 -> 0 while the job still runs
		d.PevalString("1 + 1")
		d.Pop()
		ran = append(ran, "nested")
		return 0
	})
	ctx.PutPropString(-2, "nested")
	ctx.PushGoFunc(func(d *Context) int {
		ran = append(ran, "other-job")
		return 0
	})
	ctx.PutPropString(-2, "mark")
	ctx.Pop()

	done := make(chan int, 1)
	go func() {
		done <- ctx.PevalString(`
Promise.resolve().then(function () { mark(); });
Promise.resolve().then(function () { nested(); for (;;) {} });
1`)
	}()
	select {
	case r := <-done:
		if r != 0 {
			t.Fatalf("top-level eval failed: rc=%d", r)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("nested entry disarmed the watchdog; job ran forever")
	}
	// the first job ran to completion before the second (FIFO), and the
	// nested call did not recursively pump it mid-job
	if len(ran) < 2 || ran[0] != "other-job" || ran[1] != "nested" {
		t.Fatalf("unexpected job interleaving: %v", ran)
	}
	ctx.Pop()
}

// Async/promise errors in rules are only observable through the host
// promise rejection tracker: QuickJS's reaction jobs "succeed" by
// rejecting the derived promise, so JS_ExecutePendingJob never reports
// them. These used to vanish silently.
func TestUnhandledRejectionReported(t *testing.T) {
	ctx := NewContext()
	defer ctx.DestroyHeap()
	var msgs []string
	ctx.SetJobErrorHandler(func(m string) { msgs = append(msgs, m) })

	cases := []struct{ name, src, want string }{
		{"throw after await",
			"(async function () { await Promise.resolve(); throw new Error('boom-after'); })();",
			"boom-after"},
		{"throw before first await",
			"(async function () { throw new Error('boom-before'); await Promise.resolve(); })();",
			"boom-before"},
		{"throwing then reaction",
			"Promise.resolve(1).then(function () { throw new Error('boom-then'); });",
			"boom-then"},
		{"bare rejection",
			"Promise.reject(new Error('boom-reject'));",
			"boom-reject"},
	}
	for _, c := range cases {
		if r := ctx.PevalString(c.src); r != 0 {
			t.Fatalf("%s: eval failed: %s", c.name, ctx.SafeToString(-1))
		}
		ctx.Pop()
		found := false
		for _, m := range msgs {
			if strings.Contains(m, c.want) {
				found = true
			}
		}
		if !found {
			t.Errorf("%s: no async error containing %q reported; got %v", c.name, c.want, msgs)
		}
	}
}

func TestHandledRejectionNotReported(t *testing.T) {
	ctx := NewContext()
	defer ctx.DestroyHeap()
	var msgs []string
	ctx.SetJobErrorHandler(func(m string) { msgs = append(msgs, m) })
	// the catch attaches in the same turn: the tracker's is_handled
	// notification must retract the pending report
	srcs := []string{
		"(async function () { throw new Error('handled-async'); })().catch(function () {});",
		"Promise.reject(new Error('handled-reject')).catch(function () {});",
		"(async function () { try { await Promise.reject(new Error('handled-await')); } catch (e) {} })();",
	}
	for _, src := range srcs {
		if r := ctx.PevalString(src); r != 0 {
			t.Fatalf("eval failed: %s", ctx.SafeToString(-1))
		}
		ctx.Pop()
	}
	for _, m := range msgs {
		if strings.Contains(m, "handled") {
			t.Errorf("handled rejection reported as unhandled: %q", m)
		}
	}
}

// Duktape's duk_is_* / duk_get_* treat an invalid stack index as "not that
// type" / default value. wb-rules' builtins rely on it when a rule calls them
// with fewer arguments than they inspect (trackMqtt(topic) without a
// callback): the answer must be false/zero, never a process-killing panic.
func TestInvalidIndexIsNotAPanic(t *testing.T) {
	ctx := NewContext()
	defer ctx.DestroyHeap()
	ctx.PushString("only one value")
	if ctx.GetTop() != 1 {
		t.Fatalf("top %d", ctx.GetTop())
	}
	if ctx.IsFunction(1) || ctx.IsArray(1) || ctx.IsString(1) || ctx.IsObject(5) {
		t.Fatal("Is* on an invalid index must be false")
	}
	if ctx.GetBoolean(1) || ctx.ToBoolean(1) || ctx.GetNumber(1) != 0 || ctx.GetString(1) != "" || ctx.GetLength(1) != 0 {
		t.Fatal("Get*/To* on an invalid index must yield the default value")
	}
	if got := ctx.SafeToString(1); got != "undefined" {
		t.Fatalf("SafeToString on an invalid index: %q", got)
	}
	if ctx.GetType(1) != int(DUK_TYPE_NONE) {
		t.Fatal("GetType on an invalid index must be NONE")
	}
	ctx.Pop()
}

// A Go builtin captured in a closure keeps working after the realm whose
// code created the closure is gone - Duktape ran native functions on the
// calling thread, so a rule file could publish helpers (global.__proto__)
// that other files keep using across its reloads. QuickJS hands the shim
// the caller's realm (the dead one); the call must be redirected to a live
// realm, not fail with "stale go function".
func TestGoFuncSurvivesCreatorRealmRelease(t *testing.T) {
	ctx := NewContext()
	defer ctx.DestroyHeap()
	calls := 0
	ctx.PushGlobalObject()
	ctx.PushGoFunc(func(d *Context) int {
		calls++
		d.PushString("ok")
		return 1
	})
	ctx.PutPropString(-2, "hostFn")
	ctx.Pop()

	// the primary global is the shared prototype of every realm's global
	// (the wb-rules pattern, see TestRealmGlobalPrototypeChain)
	ctx.PushHeapStash()
	ctx.PushGlobalObject()
	ctx.PutPropString(-2, "proto")
	ctx.Pop()
	newRealm := func() *Context {
		ctx.PushThreadNewGlobalenv()
		r := ctx.GetContext(-1)
		r.PushGlobalObject()
		r.PushHeapStash()
		r.GetPropString(-1, "proto")
		r.Remove(-2)
		r.SetPrototype(-2)
		r.Pop()
		return r
	}

	// realm A: creates a closure over the host function and publishes it on
	// the shared prototype
	a := newRealm()
	if r := a.PevalString("Object.getPrototypeOf(this).makeThing = function () { return { poke: function () { return hostFn(); } }; }"); r != 0 {
		t.Fatalf("realm A setup: %s", a.SafeToString(-1))
	}
	a.Pop()

	// realm B takes an object from A's closure
	b := newRealm()
	if r := b.PevalString("var thing = makeThing(); thing.poke()"); r != 0 {
		t.Fatalf("realm B first call: %s", b.SafeToString(-1))
	}
	b.Pop()

	// release realm A (its thread handle is below B's on the primary stack)
	ctx.Remove(-2)
	ctx.RunGC()

	if r := b.PevalString("thing.poke()"); r != 0 {
		t.Fatalf("closure from a released realm must still call into Go: %s", b.SafeToString(-1))
	}
	if got := b.GetString(-1); got != "ok" {
		t.Fatalf("got %q", got)
	}
	b.Pop()
	if calls != 2 {
		t.Fatalf("host function called %d times, want 2", calls)
	}
	ctx.Pop() // realm B handle
}

// The watchdog interrupt inside an async function is swallowed by QuickJS
// into a rejection of the function's promise (not a job error). It must
// still be reported with the clear timeout message, not the opaque
// "InternalError: interrupted".
func TestExecutionTimeLimitAfterAwaitHasClearMessage(t *testing.T) {
	ctx := NewContext()
	defer ctx.DestroyHeap()
	ctx.SetExecutionTimeLimit(200 * time.Millisecond)
	var msgs []string
	ctx.SetJobErrorHandler(func(m string) { msgs = append(msgs, m) })
	done := make(chan int, 1)
	go func() {
		done <- ctx.PevalString(`(async function () { await Promise.resolve(); for(;;){} })(); 1`)
	}()
	select {
	case r := <-done:
		if r != 0 {
			t.Fatalf("top-level eval failed: rc=%d", r)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("async function was not interrupted")
	}
	ctx.Pop()
	if len(msgs) != 1 {
		t.Fatalf("want one async error, got %v", msgs)
	}
	if !strings.Contains(msgs[0], "execution timed out") || strings.Contains(msgs[0], "InternalError: interrupted") {
		t.Fatalf("opaque interrupt message reported for a post-await runaway: %q", msgs[0])
	}
}

// JS -> Go builtin -> JS -> ... recursion (a rule calling itself through
// runRule, a toString that calls format) must end in a JavaScript error,
// not in the thread running off its C stack: the stack-top anchor QuickJS
// measures against is set at the outermost entry only, and the shim caps
// the nesting depth explicitly.
func TestNestedHostRecursionIsAnError(t *testing.T) {
	ctx := NewContext()
	defer ctx.DestroyHeap()
	calls := 0
	ctx.PushGlobalObject()
	ctx.PushGoFunc(func(d *Context) int {
		calls++
		// re-enter JS from the builtin, which calls the builtin again
		if r := d.PevalString("recurse()"); r != 0 {
			return DUK_RET_INSTACK_ERROR // propagate the error up the chain
		}
		return 1
	})
	ctx.PutPropString(-2, "hostFn")
	ctx.Pop()
	done := make(chan int, 1)
	go func() {
		done <- ctx.PevalString("function recurse() { return hostFn(); } recurse()")
	}()
	select {
	case r := <-done:
		if r == 0 {
			t.Fatal("unbounded host recursion finished without an error")
		}
	case <-time.After(30 * time.Second):
		t.Fatal("recursion did not terminate")
	}
	msg := ctx.SafeToString(-1)
	if !strings.Contains(msg, "recursion limit") && !strings.Contains(msg, "stack overflow") {
		t.Fatalf("unexpected error for host recursion: %q", msg)
	}
	ctx.Pop()
	if calls < 10 {
		t.Fatalf("recursion stopped suspiciously early (%d calls)", calls)
	}
	// the context is still usable
	if r := ctx.PevalString("1 + 1"); r != 0 {
		t.Fatalf("context unusable after the recursion error: %s", ctx.SafeToString(-1))
	}
	ctx.Pop()
}

// A getter that throws during enumeration value fetches is captured for
// TakeConversionError (whole-object conversion propagates it) and stands in
// as undefined; the capture is cleared by the take.
func TestEnumGetterThrowCaptured(t *testing.T) {
	ctx := NewContext()
	defer ctx.DestroyHeap()

	if r := ctx.PevalString(`({a: 1, get b() { throw new TypeError("nope"); }, c: 3})`); r != 0 {
		t.Fatalf("eval failed: %s", ctx.SafeToString(-1))
	}
	ctx.Enum(-1, DUK_ENUM_OWN_PROPERTIES_ONLY)
	keys := 0
	for ctx.Next(-1, true) {
		keys++
		ctx.Pop2()
	}
	ctx.Pop() // enumerator
	if keys != 3 {
		t.Fatalf("expected 3 keys enumerated, got %d", keys)
	}
	msg, ok := ctx.TakeConversionError()
	if !ok {
		t.Fatal("throwing getter not captured as a conversion error")
	}
	if want := "nope"; !strings.Contains(msg, want) {
		t.Fatalf("conversion error %q does not mention %q", msg, want)
	}
	if _, again := ctx.TakeConversionError(); again {
		t.Fatal("conversion error not cleared by TakeConversionError")
	}
	ctx.Pop() // the object
}

// Under JS_NAN_BOXING (32-bit builds) a float64 JSValue carries no raw
// JS_TAG_FLOAT64; only the normalized tag identifies it. With the raw tag
// every fractional number was typed as an object on armhf, sending float
// arguments down object-conversion paths ("TypeError: not an object" from
// stock system rules on WB6/7 was the field symptom).
func TestFloatTagIsNumber(t *testing.T) {
	ctx := NewContext()
	defer ctx.DestroyHeap()
	if rc := ctx.PevalString("42.5"); rc != 0 {
		t.Fatalf("eval rc %d", rc)
	}
	if !ctx.IsNumber(-1) {
		t.Fatalf("42.5 must be a number, got type %v", ctx.GetType(-1))
	}
	if ctx.IsObject(-1) {
		t.Fatal("42.5 must not be an object")
	}
	if v := ctx.GetNumber(-1); v != 42.5 {
		t.Fatalf("GetNumber: %v", v)
	}
	ctx.Pop()
	if rc := ctx.PevalString("1"); rc != 0 {
		t.Fatalf("eval rc %d", rc)
	}
	if !ctx.IsNumber(-1) {
		t.Fatal("1 must be a number")
	}
	ctx.Pop()
}
