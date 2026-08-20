package duktape

import (
	"strings"
	"testing"
	"time"
)

// A watchdog abort must be reported with a reason (execution time limit
// exceeded), not QuickJS's opaque "interrupted"; an ordinary error must not.
func TestExecTimeoutAbortMessage(t *testing.T) {
	ctx := NewContext()
	defer ctx.DestroyHeap()

	// an ordinary throw is not a watchdog abort
	if r := ctx.PevalString(`throw new Error("boom")`); r == 0 {
		t.Fatal("expected the throw to error")
	}
	if _, aborted := ctx.ExecTimeoutAbort(ctx.SafeToString(-1)); aborted {
		t.Fatal("an ordinary error must not be reported as a watchdog abort")
	}
	ctx.Pop()

	// a runaway loop is
	ctx.SetExecutionTimeLimit(150 * time.Millisecond)
	rc := make(chan int, 1)
	go func() { rc <- ctx.PevalString("while (true) {}") }()
	select {
	case r := <-rc:
		if r == 0 {
			t.Fatal("runaway loop finished without error")
		}
	case <-time.After(10 * time.Second):
		t.Fatal("interrupt did not fire within 10s")
	}
	msg, aborted := ctx.ExecTimeoutAbort(ctx.SafeToString(-1))
	if !aborted {
		t.Fatal("watchdog abort was not reported")
	}
	if !strings.Contains(msg, "js-timeout") || !strings.Contains(msg, "150ms") {
		t.Fatalf("abort message is not clear: %q", msg)
	}
	ctx.Pop()
}

// A failing microtask that runs during the same pump as a synchronous runaway
// must not steal the runaway's timeout attribution: the runaway keeps the
// timeout message, the microtask keeps its own error.
func TestExecTimeoutNotStolenByFailingJob(t *testing.T) {
	ctx := NewContext()
	defer ctx.DestroyHeap()

	var jobErr string
	ctx.SetJobErrorHandler(func(m string) { jobErr = m })
	ctx.SetExecutionTimeLimit(150 * time.Millisecond)

	rc := make(chan int, 1)
	go func() {
		// schedule a microtask that throws, then run away synchronously
		rc <- ctx.PevalString(`Promise.resolve().then(function(){ throw new Error("bg"); }); while (true) {}`)
	}()
	select {
	case r := <-rc:
		if r == 0 {
			t.Fatal("runaway loop finished without error")
		}
	case <-time.After(10 * time.Second):
		t.Fatal("interrupt did not fire within 10s")
	}

	// the synchronous runaway (read via GetESError in production) keeps the
	// timeout attribution - the microtask must not have consumed it
	msg, aborted := ctx.ExecTimeoutAbort(ctx.SafeToString(-1))
	if !aborted {
		t.Fatal("the runaway lost its timeout attribution to the failing microtask")
	}
	if !strings.Contains(msg, "js-timeout") {
		t.Fatalf("abort message is not clear: %q", msg)
	}
	// the microtask keeps its own error, not the timeout text
	if !strings.Contains(jobErr, "bg") {
		t.Fatalf("microtask error was mislabeled: %q", jobErr)
	}
	if strings.Contains(jobErr, "js-timeout") {
		t.Fatalf("microtask error wrongly relabeled as the timeout: %q", jobErr)
	}
	ctx.Pop()
}

// JS run from Go at depth 0 - here a user toString invoked by SafeToString
// on a thrown object - is covered by the watchdog too; it used to be
// invisible to the interrupt handler (execStart armed only by Pcall & co.).
func TestExecTimeoutCoversDepthZeroConversions(t *testing.T) {
	ctx := NewContext()
	defer ctx.DestroyHeap()
	ctx.SetExecutionTimeLimit(200 * time.Millisecond)
	if r := ctx.PevalString(`throw { toString: function () { while (true) {} } }`); r == 0 {
		t.Fatal("expected the throw to error")
	}
	done := make(chan string, 1)
	go func() { done <- ctx.SafeToString(-1) }()
	select {
	case msg := <-done:
		if !strings.Contains(msg, "interrupted") && !strings.Contains(msg, "js-timeout") {
			// SafeToString falls back to another representation once the
			// toString is aborted; any non-hanging answer is the point
			t.Logf("SafeToString returned %q", msg)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("a looping toString at depth 0 hung SafeToString: the watchdog did not cover it")
	}
	ctx.Pop()
}

// A script's own InternalError("interrupted") is not the watchdog and must
// not be relabelled as a timeout.
func TestScriptOwnInterruptedErrorIsNotRelabelled(t *testing.T) {
	ctx := NewContext()
	defer ctx.DestroyHeap()
	ctx.SetExecutionTimeLimit(5 * time.Second)
	if r := ctx.PevalString(`throw new InternalError("interrupted by me")`); r == 0 {
		t.Fatal("expected the throw to error")
	}
	msg := ctx.SafeToString(-1)
	if _, aborted := ctx.ExecTimeoutAbort(msg); aborted {
		t.Fatalf("a script-thrown InternalError was relabelled as a watchdog abort: %q", msg)
	}
	ctx.Pop()
}
