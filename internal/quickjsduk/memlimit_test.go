package duktape

import "testing"

// A script that allocates without bound must hit the heap cap and throw
// out-of-memory as a normal JS error - not grow until the whole process is
// OOM-killed - and the context must stay usable afterwards.
func TestMemoryLimit(t *testing.T) {
	ctx := NewContext()
	defer ctx.DestroyHeap()

	ctx.SetMemoryLimit(8 * 1024 * 1024) // 8 MiB
	rc := ctx.PevalString(`var a = []; for (;;) { a.push(new Array(20000).fill(7)); }`)
	if rc == 0 {
		t.Fatal("unbounded allocation finished without hitting the memory limit")
	}
	ctx.Pop() // discard the thrown error

	// lifting the limit, the context remains usable
	ctx.SetMemoryLimit(0)
	if r := ctx.PevalString("6 * 7"); r != 0 {
		t.Fatal("eval after a memory-limit error failed")
	}
	if v := ctx.GetNumber(-1); v != 42 {
		t.Fatalf("got %v", v)
	}
	ctx.Pop()
}
