package wbrules

import (
	"strings"
	"testing"
	"time"
)

// A js-timeout abort must point at the loop it interrupted - the last call
// site inside it - not at the statement before the loop. QuickJS builds the
// abort's backtrace from a pc it has already moved to the jump target of the
// back-edge it polled on, so without the shim's relocation a rule file's
// runaway loop was reported at the line above it (and the editor marked
// that line). The file runs inside the async top-level wrapper, the shape
// users hit.
func TestRunawayLoopLocatedAtTheLoop(t *testing.T) {
	engine, cleanup := bareCorpusEngine(t, func(o *ESEngineOptions) {
		o.JsExecutionLimit = 300 * time.Millisecond
	})
	defer cleanup()

	bad := writeScript(t, "runaway_loc.js", "var a = 1;\nvar b = a + 1;\nwhile (true) { Math.abs(b); }")
	loadErr := make(chan error, 1)
	go func() { loadErr <- engine.LiveLoadFile(bad) }()
	var err error
	select {
	case err = <-loadErr:
	case <-time.After(15 * time.Second):
		t.Fatal("interrupt did not fire: load still blocked after 15s")
	}
	msg := ""
	if err != nil {
		msg = err.Error()
	} else {
		msg = loadedEntryError(t, engine, bad)
	}
	if !strings.Contains(msg, "execution timed out") {
		t.Fatalf("expected the watchdog abort, got: %q", msg)
	}
	if !strings.Contains(msg, "runaway_loc.js:3:") || strings.Contains(msg, "runaway_loc.js:2:") {
		t.Fatalf("abort not located in the loop (line 3): %q", msg)
	}
}
