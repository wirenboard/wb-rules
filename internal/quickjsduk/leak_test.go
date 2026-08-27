package duktape

import (
	"runtime"
	"strings"
	"testing"
	"time"
)

// Leak-hunting tests for the async machinery: promise jobs, the
// rejection tracker, realms with parked promises. The canary is
// memory_used_size after a full GC - it must stay flat under churn.

func gcStableMemory(ctx *Context) int64 {
	// two passes: the first can queue finalizers whose effects only
	// show after the second
	ctx.RunGC()
	ctx.RunGC()
	return ctx.MemoryUsed()
}

func churn(t *testing.T, ctx *Context, n int, src string) {
	for i := 0; i < n; i++ {
		if r := ctx.PevalString(src); r != 0 {
			t.Fatalf("churn eval failed: %s", ctx.SafeToString(-1))
		}
		ctx.Pop()
	}
}

func TestAsyncChurnMemoryStable(t *testing.T) {
	ctx := NewContext()
	defer ctx.DestroyHeap()
	ctx.SetJobErrorHandler(func(string) {}) // rejections are part of the churn

	cases := []struct{ name, src string }{
		{"resolved chain", "(async function () { await Promise.resolve(1); await 2; return 3; })();"},
		{"handled rejection", "(async function () { try { await Promise.reject(new Error('x')); } catch (e) {} })();"},
		{"unhandled rejection", "(async function () { await Promise.resolve(); throw new Error('boom'); })();"},
		{"withResolvers settled", "(function () { var w = Promise.withResolvers(); w.promise.then(function () {}); w.resolve(1); })();"},
		{"race with loser", "Promise.race([Promise.resolve(1), new Promise(function () {})]);"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			churn(t, ctx, 500, c.src) // warmup: shapes, caches
			base := gcStableMemory(ctx)
			churn(t, ctx, 5000, c.src)
			grown := gcStableMemory(ctx) - base
			// a real per-iteration leak of even one small object would
			// exceed this by an order of magnitude over 5000 iterations
			if grown > 256*1024 {
				t.Fatalf("heap grew by %d bytes over 5000 iterations", grown)
			}
		})
	}
}

func TestPendingRejectionsMapDrained(t *testing.T) {
	ctx := NewContext()
	defer ctx.DestroyHeap()
	var reported []string
	ctx.SetJobErrorHandler(func(m string) { reported = append(reported, m) })

	srcs := []string{
		"(async function () { throw new Error('will-report'); })();",
		"(async function () { throw new Error('will-retract'); })().catch(function () {});",
	}
	for _, src := range srcs {
		if r := ctx.PevalString(src); r != 0 {
			t.Fatalf("eval failed: %s", ctx.SafeToString(-1))
		}
		ctx.Pop()
	}
	found := false
	for _, m := range reported {
		if strings.Contains(m, "will-report") {
			found = true
		}
		if strings.Contains(m, "will-retract") {
			t.Errorf("retracted rejection was reported: %q", m)
		}
	}
	if !found {
		t.Errorf("unhandled rejection not reported; got %v", reported)
	}
	// the tracker's bookkeeping must be empty once the pump flushed -
	// entries neither stuck (leak) nor lost
	s := stateFor(ctx.c())
	regMu.Lock()
	pending := len(s.rts.pendingRejections)
	regMu.Unlock()
	if pending != 0 {
		t.Fatalf("pendingRejections holds %d stale entries", pending)
	}
}

func TestRealmWithParkedPromisesReleased(t *testing.T) {
	ctx := NewContext()
	defer ctx.DestroyHeap()

	regMu.Lock()
	base := len(ctxReg)
	regMu.Unlock()

	for i := 0; i < 20; i++ {
		ctx.PushThreadNewGlobalenv()
		sub := ctx.GetContext(-1)
		// realm-internal parked promises: an await that never resolves
		// and a pending withResolvers - they must die with the realm
		if r := sub.PevalString(
			"var w = Promise.withResolvers();" +
				"(async function () { await w.promise; })();" +
				"(async function () { await new Promise(function () {}); })();"); r != 0 {
			t.Fatalf("realm eval failed: %s", sub.SafeToString(-1))
		}
		sub.Pop()
		ctx.Pop() // realm handle becomes garbage
	}
	// the handle was popped (QuickJS refcount drops it); the realm itself is
	// reaped at the next safe point, which the GC loop below gives it
	for i := 0; i < 10; i++ {
		runtime.GC()
		time.Sleep(10 * time.Millisecond)
		ctx.PevalString("0") // API entry: reap point for dead realms
		ctx.Pop()
	}
	regMu.Lock()
	live := len(ctxReg)
	regMu.Unlock()
	if live > base+2 { // allow reap-in-flight slack
		t.Fatalf("realms leaked: %d live contexts (baseline %d)", live, base)
	}
}
