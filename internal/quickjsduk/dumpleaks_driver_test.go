package duktape

import (
	"context"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"
)

// TestQuickJSDumpLeaks recompiles the shim with QuickJS's DUMP_LEAKS
// instrumentation (build tag dumpleaks -> -DDUMP_LEAKS=1, see duktape.go)
// and runs TestDumpLeaksHeapExercise (dumpleaks_test.go) as a subprocess: a
// full pass over the shim API surface followed by DestroyHeap. With
// DUMP_LEAKS, JS_FreeRuntime prints a table of every object, atom or string
// still referenced when the runtime dies; a clean heap prints nothing, so
// any leak marker in the captured output fails the test.
//
// Gated by QJS_DUMP_LEAKS=1: the instrumented build is a separate compile
// of the whole embedded QuickJS (minutes when cold), so the default suite
// must not pay for it.
func TestQuickJSDumpLeaks(t *testing.T) {
	if os.Getenv("QJS_DUMP_LEAKS") == "" {
		t.Skip("set QJS_DUMP_LEAKS=1 to run the DUMP_LEAKS-instrumented heap check")
	}

	ctxT, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()
	cmd := exec.CommandContext(ctxT, "go", "test", "-tags", "dumpleaks",
		"-count=1", "-run", "^TestDumpLeaksHeapExercise$", "-v", ".")
	cmd.Env = append(os.Environ(), "CGO_ENABLED=1")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("instrumented run failed: %v\n%s", err, out)
	}

	// the exact strings QuickJS's DUMP_LEAKS blocks print (JS_FreeRuntime);
	// "leakage" covers the rt_info variants of the atom/string tables
	for _, marker := range []string{
		"Object leaks:",
		"Secondary object leaks:",
		"Atom leaks:",
		"String leaks:",
		"leakage:",
	} {
		if strings.Contains(string(out), marker) {
			t.Fatalf("QuickJS reported leaks (%s) after DestroyHeap:\n%s", marker, out)
		}
	}
	if !strings.Contains(string(out), "PASS") {
		t.Fatalf("instrumented test did not pass:\n%s", out)
	}
}
