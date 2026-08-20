package wbrules

// Hardening tests for the tsgo integration that need no engine: framing
// abuse from the persistent api child, misbehaving transient --noEmit
// compilers (crashes, unpositioned errors, output floods), shutdown with
// checks in flight, and the batch bookkeeping of CheckAsync. Misbehaving
// compilers are played by shell scripts.

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// writeFakeTsgo writes an executable shell script playing the compiler.
func writeFakeTsgo(t *testing.T, dir, name, body string) string {
	t.Helper()
	path := filepath.Join(dir, name)
	require.NoError(t, os.WriteFile(path, []byte("#!/bin/sh\n"+body), 0o755))
	return path
}

// tsCheckFixture creates the files checkMany needs: a stand-in for the API
// declarations plus two rule files (checkMany stats every path).
func tsCheckFixture(t *testing.T, dir string) (typesPath, pathA, pathB string) {
	t.Helper()
	typesPath = filepath.Join(dir, "types.d.ts")
	pathA = filepath.Join(dir, "a.ts")
	pathB = filepath.Join(dir, "b.ts")
	for _, p := range []string{typesPath, pathA, pathB} {
		require.NoError(t, os.WriteFile(p, []byte("// stub\n"), 0o644))
	}
	return
}

// A frame size from the child is an allocation size: implausible values
// (huge, negative, garbage) must be transport errors, never allocations.
func TestReadMsgRejectsImplausibleFrames(t *testing.T) {
	read := func(stream string) error {
		c := NewTSCompiler("/nonexistent-tsgo", "")
		c.stdout = bufio.NewReader(strings.NewReader(stream))
		_, err := c.readMsg()
		return err
	}

	err := read(fmt.Sprintf("Content-Length: %d\r\n\r\n", maxFrameBytes+1))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "implausible frame size")

	err = read("Content-Length: -7\r\n\r\n")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "implausible frame size")

	require.Error(t, read("Content-Length: banana\r\n\r\n"))

	err = read("X-Whatever: 1\r\n\r\n")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "missing Content-Length")

	// a well-formed small frame still parses
	assert.NoError(t, read("Content-Length: 2\r\n\r\n{}"))
}

// A compiler that dies after printing a positional diagnostic: the file the
// diagnostic belongs to keeps it, but a co-batched file with none must be
// told the check did not run - never recorded clean.
func TestCheckManyAbnormalExitPoisonsDiaglessFiles(t *testing.T) {
	dir := t.TempDir()
	typesPath, pathA, pathB := tsCheckFixture(t, dir)
	fake := writeFakeTsgo(t, dir, "tsgo-crash",
		`printf '%s(3,1): error TS2322: boom\n' "$1"`+"\nexit 2\n")

	c := NewTSCompiler(fake, typesPath)
	results, err := c.checkMany([]string{pathA, pathB}, "")
	require.NoError(t, err)
	require.Len(t, results[pathA], 1)
	assert.Equal(t, 2322, results[pathA][0].Code)
	assert.Equal(t, "error", results[pathA][0].Severity)
	require.Len(t, results[pathB], 1)
	assert.Equal(t, "warning", results[pathB][0].Severity)
	assert.Contains(t, results[pathB][0].Message, "type check did not run")
	assert.Contains(t, results[pathB][0].Message, "exited abnormally")
}

// An error tsgo prints without a (line,col) prefix while still exiting
// normally (config trouble, a vanished file) is attributable to no one:
// files without their own diagnostic must not be recorded clean.
func TestCheckManyUnpositionedErrorPoisonsDiaglessFiles(t *testing.T) {
	dir := t.TempDir()
	typesPath, pathA, pathB := tsCheckFixture(t, dir)
	fake := writeFakeTsgo(t, dir, "tsgo-unpositioned",
		`printf '%s(3,1): error TS2322: boom\n' "$1"`+"\n"+
			`echo "error TS5023: Unknown compiler option 'wat'."`+"\nexit 0\n")

	c := NewTSCompiler(fake, typesPath)
	results, err := c.checkMany([]string{pathA, pathB}, "")
	require.NoError(t, err)
	require.Len(t, results[pathA], 1)
	assert.Equal(t, 2322, results[pathA][0].Code)
	require.Len(t, results[pathB], 1)
	assert.Contains(t, results[pathB][0].Message, "type check did not run")
	assert.Contains(t, results[pathB][0].Message, "TS5023")

	// with NO parsed diagnostics at all the whole run fails instead
	fakeOnly := writeFakeTsgo(t, dir, "tsgo-unpositioned-only",
		`echo "error TS5023: Unknown compiler option 'wat'."`+"\nexit 0\n")
	c2 := NewTSCompiler(fakeOnly, typesPath)
	_, err = c2.checkMany([]string{pathA, pathB}, "")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "TS5023")
}

// A compiler flooding its output: the engine retains only a bounded
// head+tail, and since the middle was dropped, diagnostic-less files are
// told the check did not run instead of being recorded clean.
func TestCheckManyBoundsFloodedOutput(t *testing.T) {
	dir := t.TempDir()
	typesPath, pathA, pathB := tsCheckFixture(t, dir)
	fake := writeFakeTsgo(t, dir, "tsgo-flood",
		`printf '%s(3,1): error TS2322: boom\n' "$1"`+"\n"+
			`head -c 2000000 /dev/zero | tr '\0' 'x'`+"\necho\nexit 1\n")

	c := NewTSCompiler(fake, typesPath)
	results, err := c.checkMany([]string{pathA, pathB}, "")
	require.NoError(t, err)
	require.Len(t, results[pathA], 1)
	assert.Equal(t, 2322, results[pathA][0].Code)
	require.Len(t, results[pathB], 1)
	assert.Contains(t, results[pathB][0].Message, "type check did not run")
	assert.Contains(t, results[pathB][0].Message, "retention limit")
}

func TestBoundedBufferHeadTail(t *testing.T) {
	var b boundedBuffer
	head := strings.Repeat("h", checkOutputHeadCap)
	tail := strings.Repeat("t", checkOutputTailCap)
	_, _ = b.Write([]byte(head))
	_, _ = b.Write([]byte(strings.Repeat("m", 100)))
	_, _ = b.Write([]byte(tail))
	assert.True(t, b.Truncated())
	out := b.String()
	assert.True(t, strings.HasPrefix(out, "h"))
	assert.True(t, strings.HasSuffix(out, "t"))
	assert.Contains(t, out, "100 bytes of compiler output dropped")
	assert.Less(t, len(out), checkOutputHeadCap+checkOutputTailCap+200)

	var small boundedBuffer
	_, _ = small.Write([]byte("hello"))
	assert.False(t, small.Truncated())
	assert.Equal(t, "hello", small.String())
}

// Stop must kill the transient check child, join the check goroutine within
// its bound, and still hand the queued request a terminal verdict.
func TestStopCancelsActiveChecks(t *testing.T) {
	dir := t.TempDir()
	typesPath, pathA, _ := tsCheckFixture(t, dir)
	pidFile := filepath.Join(dir, "pid")
	fake := writeFakeTsgo(t, dir, "tsgo-sleep",
		"echo $$ > "+pidFile+"\nexec sleep 60\n")

	c := NewTSCompiler(fake, typesPath)
	verdicts := make(chan []TSDiag, 1)
	c.CheckAsync(pathA, "", func(diags []TSDiag) { verdicts <- diags })

	// wait for the fake compiler to be running
	var pid int
	require.Eventually(t, func() bool {
		b, err := os.ReadFile(pidFile)
		if err != nil {
			return false
		}
		_, err = fmt.Sscanf(string(b), "%d", &pid)
		return err == nil && pid > 0
	}, 10*time.Second, 10*time.Millisecond, "the fake check process never started")

	start := time.Now()
	c.Stop()
	assert.Less(t, time.Since(start), tsStopJoinTimeout,
		"Stop must join the active check quickly, not wait out the child")

	// the goroutine was joined, so the verdict is already delivered...
	select {
	case diags := <-verdicts:
		require.Len(t, diags, 1)
		assert.Contains(t, diags[0].Message, "type check did not run")
	default:
		t.Fatal("no verdict delivered for the aborted check")
	}
	// ...and the transient child is dead (killed via the cancelled context)
	err := syscall.Kill(pid, 0)
	assert.ErrorIs(t, err, syscall.ESRCH, "the transient tsgo child must be killed on Stop")
}

// A request queued after Stop still gets its terminal verdict without any
// compiler being run.
func TestCheckAsyncAfterStopReportsWithoutRunning(t *testing.T) {
	dir := t.TempDir()
	typesPath, pathA, _ := tsCheckFixture(t, dir)
	fake := writeFakeTsgo(t, dir, "tsgo-nope", "exit 0\n")
	c := NewTSCompiler(fake, typesPath)
	c.Stop()

	verdicts := make(chan []TSDiag, 1)
	c.CheckAsync(pathA, "", func(diags []TSDiag) { verdicts <- diags })
	select {
	case diags := <-verdicts:
		require.Len(t, diags, 1)
		assert.Contains(t, diags[0].Message, "type check did not run")
	case <-time.After(10 * time.Second):
		t.Fatal("no verdict for a check queued after Stop")
	}
	assert.Equal(t, int64(0), c.CheckRuns(), "no compiler process may run after Stop")
}

// The persistent transpile child feeding garbage framing (an implausible
// Content-Length) is a transport error: the child is killed and respawned
// once, and the transpile succeeds on the healthy respawn.
func TestTranspileRecoversFromBrokenFraming(t *testing.T) {
	tsgo := tsgoForTests()
	if tsgo == "" {
		t.Skip("no tsgo: WB_RULES_TSGO not set and /usr/bin/tsgo absent")
	}
	dir := t.TempDir()
	marker := filepath.Join(dir, "poisoned-once")
	fake := writeFakeTsgo(t, dir, "tsgo-flaky",
		"if [ ! -e "+marker+" ]; then\n"+
			" : > "+marker+"\n"+
			` printf 'Content-Length: 99999999999\r\n\r\n'`+"\n"+
			" exit 0\n"+
			"fi\n"+
			"exec "+tsgo+" \"$@\"\n")

	c := NewTSCompiler(fake, "")
	out, err := c.Transpile("const n: number = 1;\n", "flaky.ts")
	require.NoError(t, err, "the transpile must succeed after one respawn")
	assert.Contains(t, out, "const n = 1;")
	c.Stop()
}

// A same-path request arriving within the batch window replaces the queued
// one AND moves to the end of the batch: flushBatch checks the whole batch
// against the LAST request's registry snapshot, so the newest snapshot must
// sit there - an in-place replacement would check the replacing request
// against a registry older than the one it was scheduled with.
func TestBatchSamePathReplacementUsesNewestRegistry(t *testing.T) {
	dir := t.TempDir()
	typesPath, pathA, pathB := tsCheckFixture(t, dir)
	regOut := filepath.Join(dir, "registry-used")
	// the registry temp file is the last argument; keep what it contained
	fake := writeFakeTsgo(t, dir, "tsgo-reg",
		`for a; do last=$a; done`+"\n"+`cat "$last" > `+regOut+"\nexit 0\n")

	c := NewTSCompiler(fake, typesPath)
	var mu sync.Mutex
	reported := map[string]int{}
	report := func(p string) func([]TSDiag) {
		return func([]TSDiag) { mu.Lock(); reported[p]++; mu.Unlock() }
	}
	c.CheckAsync(pathA, "interface WbControls { \"a/1\": \"value\" }", report(pathA))
	c.CheckAsync(pathB, "interface WbControls { \"a/1\": \"value\"; \"b/1\": \"switch\" }", report(pathB))
	newest := "interface WbControls { \"a/1\": \"value\"; \"b/1\": \"switch\"; \"c/1\": \"text\" }"
	c.CheckAsync(pathA, newest, report(pathA))

	require.Eventually(t, func() bool {
		mu.Lock()
		defer mu.Unlock()
		return reported[pathA] == 1 && reported[pathB] == 1
	}, 10*time.Second, 10*time.Millisecond, "both requests must get exactly one verdict")
	used, err := os.ReadFile(regOut)
	require.NoError(t, err)
	assert.Equal(t, newest, string(used),
		"the batch must be checked against the newest registry snapshot")
	assert.Equal(t, int64(1), c.CheckRuns(), "one coalesced run for the whole batch")
	c.Stop()
}
