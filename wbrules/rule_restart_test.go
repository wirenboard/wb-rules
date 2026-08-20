package wbrules

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	duktape "github.com/wirenboard/go-duktape"
	"github.com/wirenboard/wbgong"
	"github.com/wirenboard/wbgong/testutils"
)

// Same-instance engine lifecycle: Start/Stop/Start restarts, deterministic
// fail-fast of the synchronous entry points on a stopped engine, the eager
// callback sweeps on Stop (none of the assertions here force a Go GC - a
// regression back to finalizer-driven cleanup fails them), and the terminal
// Close.

// stashCallbackCount counts the heap-wide _esCallbacks entries via direct
// heap access. The caller must guarantee single-threaded heap access: either
// the engine is stopped (its loops are gone) or the call runs on the engine
// loop (CallSyncWait).
func stashCallbackCount(ctx *ESContext) int {
	n := 0
	ctx.PushHeapStash()
	ctx.GetPropString(-1, ESCALLBACKS_OBJ_NAME)
	ctx.Enum(-1, duktape.DUK_ENUM_OWN_PROPERTIES_ONLY)
	for ctx.Next(-1, false) {
		n++
		ctx.Pop()
	}
	ctx.Pop3()
	return n
}

// stashCountOnLoop reads the stash count on the engine loop of a running
// engine.
func stashCountOnLoop(t *testing.T, engine *ESEngine) int {
	t.Helper()
	var n int
	if err := engine.CallSyncWait(func() { n = stashCallbackCount(engine.globalCtx) }); err != nil {
		t.Fatalf("stash count: %v", err)
	}
	return n
}

// TestEngineRestartNoDuplicateDelivery restarts the same engine instance and
// checks that a control change still triggers each rule exactly once: Start
// used to register another persistent driver handler per call, so every
// restart doubled event delivery.
func TestEngineRestartNoDuplicateDelivery(t *testing.T) {
	h := newChurnHarness(t, nil)
	h.mustLoad("restart.js", `
defineVirtualDevice("rst", {cells: {
  src: {type: "value", value: 0},
  count: {type: "value", value: 0}
}});
defineRule({whenChanged: "rst/src", then: function () {
  dev["rst/count"] = Number(dev["rst/count"]) + 1;
}});
`)
	kick := func(i int, wantCount string) {
		t.Helper()
		h.evalStr(fmt.Sprintf(`dev["rst/src"] = %d; 'ok'`, i))
		h.waitEval(`'' + dev["rst/count"]`, wantCount)
	}
	kick(1, "1")

	for round := 0; round < 2; round++ {
		h.engine.Stop()
		h.engine.Start()
		want := round + 2
		kick(want, fmt.Sprintf("%d", want))
		// settle the loop twice so any duplicate delivery has run, then
		// confirm the count did not move past the expected value
		h.sync()
		h.sync()
		if got := h.evalStr(`'' + dev["rst/count"]`); got != fmt.Sprintf("%d", want) {
			t.Fatalf("after restart %d the rule ran a duplicated number of times: count=%s, want %d",
				round+1, got, want)
		}
	}
}

// TestRestartSweepsPendingTimerCallbacks stops an engine with pending timers
// and checks their callback stash entries are swept by Stop itself:
// handleStop used to discard the timer entries without running their removal
// hooks, pinning each callback (and the realm it references) forever. No Go
// GC is forced anywhere here - the sweep must be eager, not finalizer-driven.
func TestRestartSweepsPendingTimerCallbacks(t *testing.T) {
	h := newChurnHarness(t, nil)
	base := stashCountOnLoop(t, h.engine)

	for i := 0; i < 3; i++ {
		h.mustLoad("timers.js", fmt.Sprintf(`// v%d
for (var j = 0; j < 6; j++) setTimeout(function () {}, 3600000);
`, i))
		if got := stashCountOnLoop(t, h.engine); got < base+6 {
			t.Fatalf("cycle %d: expected >= %d pending timer callbacks in the stash, got %d",
				i, base+6, got)
		}
		h.engine.Stop()
		// the engine is stopped: this goroutine is the only heap toucher
		if got := stashCallbackCount(h.engine.globalCtx); got != base {
			t.Fatalf("cycle %d: Stop left %d callback stash entries (baseline %d) - timer removal hooks did not run",
				i, got, base)
		}
		h.engine.Start()
	}
}

// TestStoppedEngineFailsFast checks the deterministic behavior of every
// synchronous entry point on a stopped engine: each must return
// ErrEngineStopped promptly instead of waiting forever for a thunk that the
// stopped engine silently dropped.
func TestStoppedEngineFailsFast(t *testing.T) {
	h := newChurnHarness(t, nil)
	h.mustLoad("ok.js", `log("loaded");`)
	h.engine.Stop()

	assertStopped := func(name string, f func() error) {
		t.Helper()
		done := make(chan error, 1)
		go func() { done <- f() }()
		select {
		case err := <-done:
			if !errors.Is(err, ErrEngineStopped) {
				t.Errorf("%s on a stopped engine: got %v, want ErrEngineStopped", name, err)
			}
		case <-time.After(5 * time.Second):
			t.Errorf("%s still blocked 5s after the engine stopped", name)
		}
	}
	assertStopped("EvalScript", func() error { return h.engine.EvalScript("1+1") })
	assertStopped("LiveLoadFile", func() error {
		return h.engine.LiveLoadFile(h.writeFile("late.js", `log("late");`))
	})
	assertStopped("LiveWriteScript", func() error {
		return h.engine.LiveWriteScript("late2.js", `log("late2");`)
	})
	assertStopped("CallSync", func() error { return h.engine.CallSync(func() {}) })
	assertStopped("CallSyncWait", func() error { return h.engine.CallSyncWait(func() {}) })
	assertStopped("MaybeCallSync", func() error { return h.engine.MaybeCallSync(func() {}) })

	// and the same instance still restarts cleanly afterwards
	h.engine.Start()
	h.mustLoad("again.js", `log("again");`)
}

// TestStopSweepsInFlightSpawnCallback lets a shell command complete after
// the engine stopped: the completion thunk is dropped, and the callback's
// stash entry - which only that thunk would have swept - must be reclaimed
// at the next single-threaded boundary (here: Start). No Go GC involved.
func TestStopSweepsInFlightSpawnCallback(t *testing.T) {
	release := make(chan struct{})
	h := newChurnHarness(t, func(o *ESEngineOptions) {
		o.SetSpawnFunc(func(name string, args []string, _, _ bool, _ *string) (*CommandResult, error) {
			<-release
			return &CommandResult{ExitStatus: 0}, nil
		})
	})
	base := stashCountOnLoop(t, h.engine)
	h.mustLoad("spawn.js", `runShellCommand("blocked", {exitCallback: function () { log("done"); }});`)
	during := stashCountOnLoop(t, h.engine)
	if during <= base {
		t.Fatalf("expected the in-flight spawn callback in the stash: %d <= %d", during, base)
	}

	h.engine.Stop()
	close(release) // the command now completes into a stopped engine

	// wait until the dropped completion was recorded for the sweep
	deadline := time.Now().Add(5 * time.Second)
	for {
		h.engine.orphanedCallbacksMtx.Lock()
		n := len(h.engine.orphanedCallbacks)
		h.engine.orphanedCallbacksMtx.Unlock()
		if n > 0 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("the dropped spawn completion was never recorded as orphaned")
		}
		time.Sleep(5 * time.Millisecond)
	}
	// the producer goroutine must NOT have touched the heap itself
	if got := stashCallbackCount(h.engine.globalCtx); got != during {
		t.Fatalf("stash changed while the engine was stopped: %d, want %d", got, during)
	}
	// the next Start sweeps the orphaned key
	h.engine.Start()
	if got := stashCountOnLoop(t, h.engine); got != base {
		t.Fatalf("orphaned spawn callback not swept on Start: stash %d, want %d", got, base)
	}
}

// TestTimerCallbacksSweptEagerlyNoGC pins the eager stash sweep for timer
// callbacks without EVER forcing a Go GC: cleared and fired timers must drop
// their _esCallbacks entries deterministically through the timer removal
// hook. The churn suite settles both garbage collectors before its
// snapshots, which would mask a regression back to finalizer-driven
// cleanup; this test fails on one.
func TestTimerCallbacksSweptEagerlyNoGC(t *testing.T) {
	h := newChurnHarness(t, nil)
	h.evalStr("var __fired = 0; 'ready'")
	base := stashCountOnLoop(t, h.engine)
	for i := 0; i < 10; i++ {
		h.evalStr(fmt.Sprintf(`
(function () {
  var ids = [];
  for (var j = 0; j < 8; j++) ids.push(setTimeout(function () {}, 3600000));
  for (var j = 0; j < ids.length; j++) clearTimeout(ids[j]);
  setTimeout(function () { __fired = %d; }, 1);
  return 'ok';
})()`, i+1))
		h.waitEval("'' + __fired", fmt.Sprintf("%d", i+1))
		h.sync()
		if got := stashCountOnLoop(t, h.engine); got != base {
			t.Fatalf("cycle %d: stash %d != baseline %d with no GC forced - timer callback sweep regressed to finalizer-driven cleanup", i, got, base)
		}
	}
}

// TestSpawnCallbackSweptEagerlyNoGC is the spawn counterpart: the one-shot
// shell-command callback's stash entry must be swept right after its single
// invocation, with no Go GC involved.
func TestSpawnCallbackSweptEagerlyNoGC(t *testing.T) {
	h := newChurnHarness(t, func(o *ESEngineOptions) {
		o.SetSpawnFunc(func(name string, args []string, _, _ bool, _ *string) (*CommandResult, error) {
			return &CommandResult{ExitStatus: 0}, nil
		})
	})
	h.evalStr("var __done = 0; 'ready'")
	base := stashCountOnLoop(t, h.engine)
	for i := 0; i < 10; i++ {
		h.evalStr(fmt.Sprintf(
			`runShellCommand("noop", {exitCallback: function () { __done = %d; }}); 'ok'`, i+1))
		h.waitEval("'' + __done", fmt.Sprintf("%d", i+1))
		h.sync()
		if got := stashCountOnLoop(t, h.engine); got != base {
			t.Fatalf("cycle %d: stash %d != baseline %d with no GC forced - spawn callback sweep regressed to finalizer-driven cleanup", i, got, base)
		}
	}
}

// TestEngineCloseSweepsRegistries checks the terminal Close: the JS heap is
// destroyed and every process-global Go registry entry that referenced it
// (go functions, go objects, realm states) is released - synchronously, no
// GC required. Also checks idempotency.
func TestEngineCloseSweepsRegistries(t *testing.T) {
	baseReg := duktape.GoRegistrySize()

	broker := testutils.NewFakeMQTTBroker(t, nil)
	broker.SetWaitForRetained(false)
	driverClient := broker.MakeClient("driver")
	dargs := wbgong.NewDriverArgs().
		SetId("close-driver").
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
	defer driver.Close()
	defer driver.StopLoop()
	driver.WaitForReady()
	driver.SetFilter(&wbgong.AllDevicesFilter{})

	logClient := broker.MakeClient("close-log")
	defer logClient.Stop()
	logClient.Start()
	options := NewESEngineOptions()
	options.SetModulesDirs([]string{testModulesDir()})
	options.SetPersistentDBFile(filepath.Join(t.TempDir(), "close-pdb.db"))
	engine, err := NewESEngine(driver, logClient, options)
	if err != nil {
		t.Fatalf("engine: %v", err)
	}
	engine.Start()

	dir := t.TempDir()
	path := filepath.Join(dir, "close.js")
	script := `
defineVirtualDevice("closedev", {cells: {c: {type: "value", value: 1}}});
defineRule("close_rule", {whenChanged: "closedev/c", then: function () {}});
setTimeout(function () {}, 3600000);
var ps = PersistentStorage("close_ps", {global: true});
ps.k = "v";
`
	if err := os.WriteFile(path, []byte(script), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := engine.LiveLoadFile(path); err != nil {
		t.Fatalf("load: %v", err)
	}
	if grown := duktape.GoRegistrySize(); grown <= baseReg {
		t.Fatalf("expected the engine to grow the Go registry: %d <= %d", grown, baseReg)
	}

	engine.Close()
	engine.Close() // idempotent: a second Close must be a no-op

	if got := duktape.GoRegistrySize(); got != baseReg {
		t.Errorf("Close left %d Go registry entries (baseline %d)", got, baseReg)
	}

	// a straggler entry point on a closed engine fails fast, not crashes
	if err := engine.EvalScript("1+1"); !errors.Is(err, ErrEngineStopped) {
		t.Errorf("EvalScript on a closed engine: got %v, want ErrEngineStopped", err)
	}
}

// TestEngineSkipsQuarantinedFile covers the engine-level loadguard path: a
// file recorded as having crashed the process during its previous loads is
// skipped with a visible load error, and editing it releases the quarantine.
func TestEngineSkipsQuarantinedFile(t *testing.T) {
	guardDir := t.TempDir()
	scriptDir := t.TempDir()
	path := filepath.Join(scriptDir, "poison.js")
	script := `defineVirtualDevice("poison", {cells: {c: {type: "value", value: 1}}});`
	if err := os.WriteFile(path, []byte(script), 0o644); err != nil {
		t.Fatal(err)
	}
	// state a previous run would have left after LOAD_CRASH_QUARANTINE_THRESHOLD crashes
	state := fmt.Sprintf(`{%q: {"crashes": %d, "quarantinedMtime": %d}}`,
		path, LOAD_CRASH_QUARANTINE_THRESHOLD, fileMtimeNs(path))
	if err := os.WriteFile(filepath.Join(guardDir, loadGuardStateFile), []byte(state), 0o640); err != nil {
		t.Fatal(err)
	}

	h := newChurnHarness(t, func(o *ESEngineOptions) { o.SetLoadGuardDir(guardDir) })

	err := h.engine.LiveLoadFile(path)
	if err == nil || !strings.Contains(err.Error(), "[loadguard]") {
		t.Fatalf("expected a loadguard quarantine load error, got %v", err)
	}
	// the quarantined script must not have executed
	if got := h.evalStr(`'' + (getDevice("poison") !== undefined)`); got != "false" {
		t.Fatal("quarantined file was executed")
	}

	// editing the file (content change moves mtime) releases the quarantine
	time.Sleep(10 * time.Millisecond) // ensure a distinct mtime
	if err := os.WriteFile(path, []byte(script+"\n// edited\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := h.engine.LiveLoadFile(path); err != nil {
		t.Fatalf("edited file still refused: %v", err)
	}
	h.waitEval(`'' + (getDevice("poison") !== undefined)`, "true")
}

// TestRealmCreationOOMYieldsLoadError drives the JS heap to its memory limit
// and checks that a file load whose realm cannot be created reports a load
// error instead of crashing, and that the engine recovers once memory is
// available again.
func TestRealmCreationOOMYieldsLoadError(t *testing.T) {
	h := newChurnHarness(t, func(o *ESEngineOptions) {
		o.JsMemoryLimit = 24 * 1024 * 1024
	})
	// fill the heap to the cap, in ever smaller chunks so almost no slack
	// remains for the next realm
	h.mustLoad("filler.js", `
globalThis.__ballast = [];
try { for (;;) { __ballast.push(new Array(4096).fill(7)); } } catch (e) {}
try { for (;;) { __ballast.push(new Array(64).fill(1)); } } catch (e) {}
`)

	p2 := h.writeFile("second.js", `log("second");`)
	err := h.engine.LiveLoadFile(p2)
	if err == nil || !strings.Contains(err.Error(), "cannot create a JS context") {
		t.Fatalf("expected a realm-creation load error at the heap limit, got %v", err)
	}

	// lift the limit: the engine must recover and load files again
	if err := h.engine.CallSyncWait(func() { h.engine.globalCtx.SetMemoryLimit(0) }); err != nil {
		t.Fatalf("lift limit: %v", err)
	}
	h.mustLoad("third.js", `log("third");`)
}
