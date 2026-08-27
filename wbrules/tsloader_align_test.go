package wbrules

import "testing"

// The check counter is read and bumped with 64-bit atomics from several
// goroutines. On 32-bit builds (armhf, WB6/WB7) sync/atomic panics with
// "unaligned 64-bit atomic operation" unless the field is 8-byte aligned -
// a bare int64 member of TSCompiler was not, and every background check on
// a WB6 died with that panic. atomic.Int64 carries its own alignment
// guarantee; this test simply exercises the counter so a regression to a
// bare field fails the 32-bit run instead of the field's controller.
func TestCheckRunsCounterIsAtomicSafe(t *testing.T) {
	c := NewTSCompiler("", "")
	if got := c.CheckRuns(); got != 0 {
		t.Fatalf("fresh compiler reports %d runs", got)
	}
	c.checkRuns.Add(1)
	if got := c.CheckRuns(); got != 1 {
		t.Fatalf("after one increment: %d", got)
	}
}
