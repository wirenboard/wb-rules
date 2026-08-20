package wbrules

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"

	duktape "github.com/wirenboard/go-duktape"
)

// Reloading a rule file must not leak its realm. Every reload used to
// strand the file's callback entries in the heap-wide _esCallbacks stash
// (RemoveCallback skips invalidated contexts), and one stranded entry pins
// the dead realm - its scope chains, its realm-local builtins and their Go
// registry entries - until process restart. invalidate() now sweeps the
// context's keys through the long-lived heap context, so the stash size,
// the Go value registry and the JS heap must all stay flat under reload
// churn. Deterministic: everything is measured on the engine loop after a
// full JS GC; no timing or RSS involved.
func TestReloadReclaimsRealm(t *testing.T) {
	engine, done := bareCorpusEngine(t, nil)
	defer done()

	dir := t.TempDir()
	path := filepath.Join(dir, "reload_leak.js")
	load := func(i int) {
		// content differs per version so the tracker always reloads
		src := fmt.Sprintf(
			"defineRule(\"reload_leak_rule\", {whenChanged: \"a/b\", then: function () { log(\"v%d\"); }});\n", i)
		if err := os.WriteFile(path, []byte(src), 0o644); err != nil {
			t.Fatal(err)
		}
		if err := engine.LiveLoadFile(path); err != nil {
			t.Fatalf("load %d: %v", i, err)
		}
	}

	type snapshot struct {
		stashCallbacks int
		jsHeap         int64
	}
	measure := func() snapshot {
		var snap snapshot
		ch := make(chan struct{})
		engine.CallSync(func() {
			defer close(ch)
			ctx := engine.globalCtx
			// GC the dead realm, reap freed contexts (a Pop runs the
			// reaper), then GC again for anything the reap released
			ctx.RunGC()
			ctx.PushUndefined()
			ctx.Pop()
			ctx.RunGC()
			ctx.PushHeapStash()
			ctx.GetPropString(-1, ESCALLBACKS_OBJ_NAME)
			ctx.Enum(-1, duktape.DUK_ENUM_OWN_PROPERTIES_ONLY)
			for ctx.Next(-1, false) {
				snap.stashCallbacks++
				ctx.Pop()
			}
			ctx.Pop3()
			snap.jsHeap = ctx.MemoryUsed()
		})
		<-ch
		return snap
	}

	load(0)
	base := measure()
	baseReg := duktape.GoRegistrySize()

	const reloads = 20
	for i := 1; i <= reloads; i++ {
		load(i)
	}
	after := measure()
	afterReg := duktape.GoRegistrySize()

	t.Logf("stash callbacks %d -> %d, go registry %d -> %d, js heap %d -> %d",
		base.stashCallbacks, after.stashCallbacks, baseReg, afterReg, base.jsHeap, after.jsHeap)

	if after.stashCallbacks != base.stashCallbacks {
		t.Errorf("callback stash grew under reload churn: %d -> %d entries (dead files' callbacks not swept)",
			base.stashCallbacks, after.stashCallbacks)
	}
	// one live realm before and after; a few entries of slack for unrelated
	// engine bookkeeping, nowhere near the ~30 builtins a leaked realm pins
	if afterReg > baseReg+5 {
		t.Errorf("go value registry grew under reload churn: %d -> %d (leaked realms keep their builtins registered)",
			baseReg, afterReg)
	}
	// a leaked realm costs tens of KB; legitimate growth is near zero
	if growth := after.jsHeap - base.jsHeap; growth > 64*1024 {
		t.Errorf("js heap grew by %d bytes over %d reloads (realm leak?)", growth, reloads)
	}
}
