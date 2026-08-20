package wbrules

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"

	duktape "github.com/wirenboard/go-duktape"
	"github.com/wirenboard/wbgong"
	"github.com/wirenboard/wbgong/testutils"
)

// Churn leak hunts for the engine loop: every scenario runs a few warmup
// cycles (shapes, atom tables, module caches and other one-time QuickJS
// allocator retention), snapshots the heap and the Go-side registries, runs
// ~20 measured cycles of the same churn and snapshots again. Live JS
// objects, JS heap bytes, the Go value registry, the heap-wide callback
// stash and the goroutine count must all be FLAT: the tolerances below are
// small constants, far under one-object-per-cycle, so any per-cycle slope
// fails. Everything JS is measured on the engine loop after full GC passes
// on both sides (Go finalizers drive the stash sweep; QuickJS GC frees the
// realms), so the numbers are deterministic - no RSS, no timing.
//
// A byte-level slope of tens of bytes per cycle (a reallocated table
// recording a growing high-water mark) clears the byte tolerance only over
// hundreds of cycles: WBRULES_CHURN_CYCLES=300 runs the same scenarios long
// enough - that is how the timer-callback stash pile-up was caught (see the
// eager sweep in esWbStartTimer/esWbSpawn).
//
// Companion tests: rule_reload_leak_test.go pins the reload/realm sweep,
// rule_async_leak_test.go pins waiter/tracker/timer registry bounds, and
// internal/quickjsduk/leak_test.go churns the bare shim.

const (
	churnWarmupCycles   = 3
	churnMeasuredCycles = 20

	// a leak of even one small object per cycle exceeds every one of these
	churnObjTolerance     = 10       // live JS objects
	churnMemTolerance     = 8 * 1024 // bytes of live JS heap (allocator bucketing noise)
	churnGoRegTolerance   = 5        // Go values referenced from JS
	churnStashTolerance   = 3        // _esCallbacks entries (finalizer-driven)
	churnGoroutineSlack   = 4        // transient helpers may still be parked
	churnEvalWaitDeadline = 10 * time.Second
)

type churnSnapshot struct {
	full           duktape.MemoryUsage
	goRegistry     int
	stashCallbacks int
	goroutines     int
	rules          int
	timers         int
	trackers       int
	localCtxs      int
}

// churnCycles returns the measured cycle count; WBRULES_CHURN_CYCLES
// overrides it for leak investigation (a longer run separates a linear
// slope from one-time allocator retention).
func churnCycles() int {
	if s := os.Getenv("WBRULES_CHURN_CYCLES"); s != "" {
		if n, err := strconv.Atoi(s); err == nil && n > 0 {
			return n
		}
	}
	return churnMeasuredCycles
}

type churnHarness struct {
	t      *testing.T
	engine *ESEngine
	broker *testutils.FakeMQTTBroker
	dir    string
	seq    int
}

// newChurnHarness builds a bare engine like bareCorpusEngine does, plus the
// pieces churn needs: the broker (to drain its recorder), a source root (for
// the editor-save path) and full teardown including the JS heap.
func newChurnHarness(t *testing.T, tweak func(*ESEngineOptions)) *churnHarness {
	broker := testutils.NewFakeMQTTBroker(t, nil)
	broker.SetWaitForRetained(false)
	driverClient := broker.MakeClient("driver")
	dargs := wbgong.NewDriverArgs().
		SetId("churn-driver").
		SetMqtt(driverClient).
		SetTesting()
	dargs.SetUseStorage(false)

	driver, err := wbgong.NewDriverBase(dargs)
	if err != nil {
		t.Fatalf("driver: %v", err)
	}
	if err := driver.StartLoop(); err != nil {
		t.Fatalf("driver loop: %v", err)
	}
	driver.WaitForReady()
	driver.SetFilter(&wbgong.AllDevicesFilter{})

	logClient := broker.MakeClient("churn-log")
	options := NewESEngineOptions()
	options.SetModulesDirs([]string{testModulesDir()})
	if tweak != nil {
		tweak(options)
	}
	engine, err := NewESEngine(driver, logClient, options)
	if err != nil {
		t.Fatalf("engine: %v", err)
	}
	dir := t.TempDir()
	if err := engine.SetSourceRoot(dir); err != nil {
		t.Fatalf("source root: %v", err)
	}
	engine.Start()

	h := &churnHarness{t: t, engine: engine, broker: broker, dir: dir}
	t.Cleanup(func() {
		h.drainRecorder() // teardown publishes too; keep the recorder unblocked
		engine.Close()    // stops the loops (if still running) and frees the JS heap
		driver.StopLoop()
		driver.Close()
		// the engine starts its log client lazily on the first Log publish;
		// a scenario that never logged leaves it unstarted, and Stop on an
		// unstarted fake client panics - Start is idempotent
		logClient.Start()
		logClient.Stop()
	})
	return h
}

// drainRecorder consumes everything the fake broker recorded so far: the
// recorder's channel holds 1000 items and blocks publishers beyond that,
// which would deadlock a churn loop. A sentinel publish marks "so far".
func (h *churnHarness) drainRecorder() {
	h.seq++
	payload := fmt.Sprintf("%d", h.seq)
	h.broker.Publish("churn-drain", wbgong.MQTTMessage{Topic: "/churn/drain", Payload: payload})
	h.broker.SkipTill(fmt.Sprintf("churn-drain -> /churn/drain: [%s] (QoS 0)", payload))
}

// sync runs a full round trip through the engine loop, so everything queued
// before it (LiveRemoveFile cleanups, finalizer-driven RemoveCallback
// thunks) has executed when it returns.
func (h *churnHarness) sync() {
	done := make(chan struct{})
	h.engine.CallSync(func() { close(done) })
	<-done
}

// evalStr evaluates src on the engine loop in the global context and
// returns the result as a string; a JS error fails the test.
func (h *churnHarness) evalStr(src string) string {
	var out string
	var evalErr string
	done := make(chan struct{})
	h.engine.CallSync(func() {
		defer close(done)
		ctx := h.engine.globalCtx
		if r := ctx.PevalString(src); r != 0 {
			evalErr = ctx.SafeToString(-1)
		} else {
			out = ctx.SafeToString(-1)
		}
		ctx.Pop()
	})
	<-done
	if evalErr != "" {
		h.t.Fatalf("eval %q failed: %s", src, evalErr)
	}
	return out
}

// waitEval polls src on the engine loop until it yields want - the way the
// scenarios synchronize with rule runs, timers and promise jobs without
// sleeping for fixed amounts.
func (h *churnHarness) waitEval(src, want string) {
	h.t.Helper()
	deadline := time.Now().Add(churnEvalWaitDeadline)
	var last string
	for time.Now().Before(deadline) {
		last = h.evalStr(src)
		if last == want {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	h.t.Fatalf("timed out waiting for %q == %q (last value %q)", src, want, last)
}

func (h *churnHarness) writeFile(name, content string) string {
	path := filepath.Join(h.dir, name)
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		h.t.Fatal(err)
	}
	return path
}

func (h *churnHarness) mustLoad(name, content string) string {
	h.t.Helper()
	path := h.writeFile(name, content)
	if err := h.engine.LiveLoadFile(path); err != nil {
		h.t.Fatalf("load %s: %v", name, err)
	}
	if msg := loadedEntryError(h.t, h.engine, path); msg != "" {
		h.t.Fatalf("script error in %s: %s", name, msg)
	}
	return path
}

// stableGoroutines waits for the goroutine count to stop moving (timer
// loops winding down, spawn helpers exiting) and returns it.
func stableGoroutines() int {
	last := runtime.NumGoroutine()
	stable := 0
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		time.Sleep(25 * time.Millisecond)
		n := runtime.NumGoroutine()
		if n == last {
			if stable++; stable >= 3 {
				return n
			}
		} else {
			stable = 0
			last = n
		}
	}
	return last
}

// snapshot settles both garbage collectors and reads every leak canary.
// Go GC first: callback-holder finalizers queue RemoveCallback thunks on
// the engine loop (sync() lands them), which drops the last stash
// references; the JS GC (with a reap point between two passes for dead
// realms) then actually frees what they pinned. Three rounds cover the
// finalizer -> stash -> JS-object chains.
func (h *churnHarness) snapshot() churnSnapshot {
	for i := 0; i < 3; i++ {
		runtime.GC()
		time.Sleep(5 * time.Millisecond)
		h.sync()
	}
	var snap churnSnapshot
	done := make(chan struct{})
	h.engine.CallSync(func() {
		defer close(done)
		ctx := h.engine.globalCtx
		ctx.RunGC()
		ctx.PushUndefined()
		ctx.Pop() // API entry: reap point for dead realms
		ctx.RunGC()
		snap.full = ctx.MemoryUsage()
		ctx.PushHeapStash()
		ctx.GetPropString(-1, ESCALLBACKS_OBJ_NAME)
		ctx.Enum(-1, duktape.DUK_ENUM_OWN_PROPERTIES_ONLY)
		for ctx.Next(-1, false) {
			snap.stashCallbacks++
			ctx.Pop()
		}
		ctx.Pop3()
		snap.rules = len(h.engine.ruleMap)
		snap.timers = len(h.engine.timers)
		for _, m := range h.engine.tracks {
			snap.trackers += len(m)
		}
		snap.localCtxs = len(h.engine.localCtxs)
	})
	<-done
	snap.goRegistry = duktape.GoRegistrySize()
	snap.goroutines = stableGoroutines()
	return snap
}

// runChurn is the shared scenario driver: warmup, baseline, measured
// cycles, flatness assertions. cycle(i) gets a monotonically growing i so
// per-cycle content always differs (the content tracker must reload).
func runChurn(t *testing.T, h *churnHarness, cycle func(i int)) {
	cycles := churnCycles()
	for i := 0; i < churnWarmupCycles; i++ {
		cycle(i)
		h.drainRecorder()
	}
	base := h.snapshot()
	for i := 0; i < cycles; i++ {
		cycle(churnWarmupCycles + i)
		h.drainRecorder()
	}
	after := h.snapshot()

	t.Logf("churn over %d cycles: obj %d -> %d, mem %d -> %d, goreg %d -> %d, stash %d -> %d, "+
		"goroutines %d -> %d, rules %d -> %d, timers %d -> %d, trackers %d -> %d, ctxs %d -> %d",
		cycles,
		base.full.ObjCount, after.full.ObjCount, base.full.MemoryUsedSize, after.full.MemoryUsedSize,
		base.goRegistry, after.goRegistry, base.stashCallbacks, after.stashCallbacks,
		base.goroutines, after.goroutines, base.rules, after.rules,
		base.timers, after.timers, base.trackers, after.trackers,
		base.localCtxs, after.localCtxs)
	t.Logf("churn detail: atoms %d -> %d (%+d bytes), strings %d -> %d, shapes %d -> %d, "+
		"props %d -> %d, blocks %d -> %d, jsfuncs %d -> %d",
		base.full.AtomCount, after.full.AtomCount, after.full.AtomSize-base.full.AtomSize,
		base.full.StrCount, after.full.StrCount,
		base.full.ShapeCount, after.full.ShapeCount,
		base.full.PropCount, after.full.PropCount,
		base.full.MallocCount, after.full.MallocCount,
		base.full.JSFuncCount, after.full.JSFuncCount)

	if grown := after.full.ObjCount - base.full.ObjCount; grown > churnObjTolerance {
		t.Errorf("live JS objects grew by %d over %d cycles (%.1f/cycle)",
			grown, cycles, float64(grown)/float64(cycles))
	}
	if grown := after.full.MemoryUsedSize - base.full.MemoryUsedSize; grown > churnMemTolerance {
		t.Errorf("JS heap grew by %d bytes over %d cycles (%.0f/cycle)",
			grown, cycles, float64(grown)/float64(cycles))
	}
	if grown := after.goRegistry - base.goRegistry; grown > churnGoRegTolerance {
		t.Errorf("Go value registry grew by %d over %d cycles", grown, cycles)
	}
	if grown := after.stashCallbacks - base.stashCallbacks; grown > churnStashTolerance {
		t.Errorf("callback stash grew by %d entries over %d cycles", grown, cycles)
	}
	if grown := after.goroutines - base.goroutines; grown > churnGoroutineSlack {
		t.Errorf("goroutines grew by %d over %d cycles", grown, cycles)
	}
	if after.rules != base.rules {
		t.Errorf("rule registry changed under churn: %d -> %d", base.rules, after.rules)
	}
	if after.timers != base.timers {
		t.Errorf("timer registry changed under churn: %d -> %d", base.timers, after.timers)
	}
	if after.trackers != base.trackers {
		t.Errorf("MQTT tracker registry changed under churn: %d -> %d", base.trackers, after.trackers)
	}
	if after.localCtxs != base.localCtxs {
		t.Errorf("per-file context registry changed under churn: %d -> %d", base.localCtxs, after.localCtxs)
	}
}

// (a) Editor-save cycle: a file defining a virtual device and rules is
// saved over and over (the remove+recreate reload path the Editor RPC
// takes). rule_reload_leak_test.go pins the stash sweep for a plain rule
// file; this adds the vdev and the anonymous rule to the churn.
func TestLeakChurnEditorSave(t *testing.T) {
	h := newChurnHarness(t, nil)
	runChurn(t, h, func(i int) {
		content := fmt.Sprintf(`
defineVirtualDevice("edsave", {cells: {
  c1: {type: "value", value: %d},
  sw: {type: "switch", value: false}
}});
defineRule("edsave_rule", {whenChanged: "edsave/sw", then: function (v) {
  dev["edsave/c1"] = %d;
}});
defineRule({whenChanged: "edsave/c1", then: function () {}});
`, i, i)
		if err := h.engine.LiveWriteScript("edsave.js", content); err != nil {
			t.Fatalf("editor save %d: %v", i, err)
		}
		if msg := loadedEntryError(t, h.engine, filepath.Join(h.dir, "edsave.js")); msg != "" {
			t.Fatalf("script error after save %d: %s", i, msg)
		}
	})
}

// (b) File removed, then recreated: the full LiveRemoveFile cleanup
// (cleanup scopes, thread storage, context invalidation) followed by a
// fresh load, every cycle.
func TestLeakChurnRemoveRecreate(t *testing.T) {
	h := newChurnHarness(t, nil)
	path := filepath.Join(h.dir, "recreate.js")
	runChurn(t, h, func(i int) {
		h.mustLoad("recreate.js", fmt.Sprintf(`
defineVirtualDevice("recdev", {cells: {c: {type: "value", value: %d}}});
defineRule("recreate_rule", {whenChanged: "recdev/c", then: function () { log("rec %d"); }});
`, i, i))
		if err := os.Remove(path); err != nil {
			t.Fatal(err)
		}
		if err := h.engine.LiveRemoveFile(path); err != nil {
			t.Fatalf("remove %d: %v", i, err)
		}
		h.sync() // LiveRemoveFile only schedules; wait for the cleanups
	})
}

// (c) defineRule redefinition of one name, across reloads and inside a
// single load: the in-file duplicate is refused ("named rule redefinition")
// after its condition and then callbacks were already wrapped and stashed -
// the refused rule's callbacks must be reclaimed, not stranded.
func TestLeakChurnNamedRuleRedefinition(t *testing.T) {
	h := newChurnHarness(t, nil)
	runChurn(t, h, func(i int) {
		h.mustLoad("redef.js", fmt.Sprintf(`
defineRule("redef_rule", {whenChanged: "rdev/a", then: function () { log("v%d"); }});
try {
  defineRule("redef_rule", {whenChanged: "rdev/b", then: function () { log("dup%d"); }});
} catch (e) {
  log("redefinition refused");
}
`, i, i))
	})
}

// (d) setTimeout/setInterval churn: a batch created and cleared unfired, a
// one-shot that fires, and an interval that ticks then clears itself -
// every cycle, with the timer registry and the callback stash flat.
func TestLeakChurnTimers(t *testing.T) {
	h := newChurnHarness(t, nil)
	h.evalStr("var __fired = 0, __ticks = 0, __idone = 0; 'ready'")
	runChurn(t, h, func(i int) {
		h.evalStr(fmt.Sprintf(`
(function () {
  var ids = [];
  for (var j = 0; j < 8; j++) ids.push(setTimeout(function () {}, 1));
  for (var j = 0; j < ids.length; j++) clearTimeout(ids[j]);
  setTimeout(function () { __fired = %d; }, 1);
  var start = __ticks;
  var iv = setInterval(function () {
    if (++__ticks - start >= 2) { clearInterval(iv); __idone = %d; }
  }, 1);
  return 'ok';
})()`, i+1, i+1))
		h.waitEval("'' + __fired + ':' + __idone", fmt.Sprintf("%d:%d", i+1, i+1))
	})
}

// (e) defineAlias redefinition churn: the same alias redefined to rotating
// targets from the global context, and a per-file alias redefined across
// reloads. Aliases live on the shared global object; redefinition must
// replace, not accumulate.
func TestLeakChurnDefineAlias(t *testing.T) {
	h := newChurnHarness(t, nil)
	runChurn(t, h, func(i int) {
		h.evalStr(fmt.Sprintf(`defineAlias("churn_alias", "adev/c%d"); 'ok'`, i%3))
		h.mustLoad("alias.js", fmt.Sprintf(`
defineAlias("churn_file_alias", "adev/f%d");
defineRule({whenChanged: "churn_file_alias", then: function () {}});
`, i%3))
	})
}

// (f) An await on changed()/nextMqtt() that then fires, every cycle. The
// kick rules park a waiter and flag it via a control, so the test fires
// the wake-up only once the waiter is parked - no timing assumptions.
// rule_async_leak_test.go bounds the waiter registries themselves; this
// measures the heap under park/wake churn.
func TestLeakChurnAsyncAwait(t *testing.T) {
	h := newChurnHarness(t, nil)
	h.mustLoad("await.js", `
defineVirtualDevice("chdev", {cells: {
  kick: {type: "value", value: 0},
  tick: {type: "value", value: 0},
  mkick: {type: "value", value: 0},
  parked: {type: "value", value: 0},
  mparked: {type: "value", value: 0},
  fired: {type: "value", value: 0},
  mfired: {type: "value", value: 0}
}});
defineRule({whenChanged: "chdev/kick", then: function (v) {
  (async function () {
    var p = changed("chdev/tick");
    dev["chdev/parked"] = v;
    dev["chdev/fired"] = await p;
  })();
}});
defineRule({whenChanged: "chdev/mkick", then: function (v) {
  (async function () {
    var p = nextMqtt("/churn/next");
    dev["chdev/mparked"] = v;
    var m = await p;
    dev["chdev/mfired"] = Number(m.value);
  })();
}});
`)
	runChurn(t, h, func(i int) {
		v := fmt.Sprintf("%d", i+1)
		h.evalStr(fmt.Sprintf(`dev["chdev/kick"] = %s; 'ok'`, v))
		h.waitEval(`'' + dev["chdev/parked"]`, v)
		h.evalStr(fmt.Sprintf(`dev["chdev/tick"] = %s; 'ok'`, v))
		h.waitEval(`'' + dev["chdev/fired"]`, v)

		h.evalStr(fmt.Sprintf(`dev["chdev/mkick"] = %s; 'ok'`, v))
		h.waitEval(`'' + dev["chdev/mparked"]`, v)
		h.evalStr(fmt.Sprintf(`publish("/churn/next", "%s"); 'ok'`, v))
		h.waitEval(`'' + dev["chdev/mfired"]`, v)
	})
}

// (g) spawn churn through the stubbed runner (the corpus sandbox pattern):
// an awaited spawn, a legacy exitCallback runShellCommand, and a failing
// exec whose rejection is caught - each cycle.
func TestLeakChurnSpawn(t *testing.T) {
	h := newChurnHarness(t, func(o *ESEngineOptions) {
		o.SetSpawnFunc(func(name string, args []string, _, _ bool, _ *string) (*CommandResult, error) {
			if name == "fail" {
				return nil, fmt.Errorf("churn stub: refusing %q", name)
			}
			return &CommandResult{ExitStatus: 0, CapturedOutput: "out", CapturedErrorOutput: ""}, nil
		})
	})
	h.evalStr("var __spawned = 0, __called = 0, __failed = 0; 'ready'")
	runChurn(t, h, func(i int) {
		v := i + 1
		h.evalStr(fmt.Sprintf(`
(function () {
  spawn("noop", ["a"], {captureOutput: true}).then(function (r) {
    if (r.capturedOutput === "out") __spawned = %d;
  });
  runShellCommand("x", {exitCallback: function () { __called = %d; }});
  spawn("fail", []).catch(function () { __failed = %d; });
  return 'ok';
})()`, v, v, v))
		h.waitEval("'' + __spawned + ':' + __called + ':' + __failed",
			fmt.Sprintf("%d:%d:%d", v, v, v))
	})
}

// (h) PersistentStorage open/write churn: the global storage exercised from
// the engine context (writes, reads, StorableObject round trip, delete) and
// a per-file local storage re-opened across reloads.
func TestLeakChurnPersistentStorage(t *testing.T) {
	h := newChurnHarness(t, func(o *ESEngineOptions) {
		o.SetPersistentDBFile(filepath.Join(t.TempDir(), "churn-pdb.db"))
	})
	runChurn(t, h, func(i int) {
		got := h.evalStr(fmt.Sprintf(`
(function (i) {
  var ps = PersistentStorage("churn_ps", {global: true});
  ps["k" + (i %% 4)] = "v" + i;
  var x = ps["k" + (i %% 4)];
  ps.obj = new StorableObject({n: i});
  var y = ps.obj.n;
  ps.gone = "there";
  ps.gone = null; // null deletes the key
  return x + ":" + y;
})(%d)`, i))
		if want := fmt.Sprintf("v%d:%d", i, i); got != want {
			t.Fatalf("storage cycle %d: got %q, want %q", i, got, want)
		}
		h.mustLoad("pstore.js", fmt.Sprintf(`
var ps = PersistentStorage("churn_local");
ps.k%d = %d;
log("psv " + ps.k%d);
`, i%3, i, i%3))
	})
}

// (i) A rule that throws plus an async rejection, every cycle: the
// CallbackErrorHandler path (traceback included) and the unhandled
// rejection report must not retain the error chain.
func TestLeakChurnRuleErrors(t *testing.T) {
	h := newChurnHarness(t, nil)
	h.mustLoad("errors.js", `
defineVirtualDevice("edev", {cells: {
  kick: {type: "value", value: 0},
  count: {type: "value", value: 0}
}});
defineRule({whenChanged: "edev/kick", then: function (v) {
  (async function () {
    await Promise.resolve();
    throw new Error("async boom " + v);
  })();
  dev["edev/count"] = v;
  throw new Error("sync boom " + v);
}});
`)
	runChurn(t, h, func(i int) {
		v := fmt.Sprintf("%d", i+1)
		h.evalStr(fmt.Sprintf(`dev["edev/kick"] = %s; 'ok'`, v))
		h.waitEval(`'' + dev["edev/count"]`, v)
	})
}

// (j) Watchdog-abort churn: a runaway file is loaded and interrupted by the
// js-timeout over and over (the JsExecutionLimit knob the runaway corpus
// test uses); each abort must free the aborted run's state. The engine must
// still load a healthy file afterwards.
func TestLeakChurnWatchdogAbort(t *testing.T) {
	h := newChurnHarness(t, func(o *ESEngineOptions) {
		o.JsExecutionLimit = 150 * time.Millisecond
	})
	path := filepath.Join(h.dir, "runaway.js")
	runChurn(t, h, func(i int) {
		h.writeFile("runaway.js", fmt.Sprintf("// v%d\nwhile (true) {}\n", i))
		err := h.engine.LiveLoadFile(path)
		msg := ""
		if err != nil {
			msg = err.Error()
		} else {
			msg = loadedEntryError(t, h.engine, path)
		}
		if !strings.Contains(msg, "execution timed out") {
			t.Fatalf("cycle %d: expected watchdog abort, got %q", i, msg)
		}
	})
	h.mustLoad("fine.js", `log("still alive");`)
}
