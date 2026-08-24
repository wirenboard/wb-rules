package wbrules

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/wirenboard/wbgong"
)

// TSCompiler integrates Microsoft's typescript-go (tsgo).
//
// Load path ("transpile and run first"): a persistent `tsgo --api --async`
// child answers transpileModule requests over LSP-framed JSON-RPC in well
// under a millisecond — rule files start without waiting for type checking.
//
// Check path ("check later"): after a .ts file is loaded and running, a
// background `tsgo --noEmit` pass type-checks it (together with the
// wb-rules builtin declarations) and reports diagnostics asynchronously.
type TSCompiler struct {
	mu      sync.Mutex
	binPath string
	typesGl string // path to wb-rules.d.ts (checked into the program), "" if absent

	cmd     *exec.Cmd
	stdin   io.WriteCloser
	stdout  *bufio.Reader
	nextID  int64
	stopped bool // Stop() was called: the child's exit is expected, not logged

	mapsMu   sync.Mutex
	lineMaps map[string][]int // per transpiled file: generated line (1-based) -> source line

	checkSem chan struct{} // bounds concurrent background type-check processes
	// atomic.Int64: a bare int64 under sync/atomic panics on 32-bit builds
	// (armhf) unless 8-byte aligned, and this field was not
	checkRuns atomic.Int64 // tsgo --noEmit processes started so far (tests assert batching)

	// rootCtx parents every transient tsgo --noEmit run; Stop cancels it so
	// active check processes die with the engine instead of running out
	// their 60-second timeout on their own.
	rootCtx    context.Context
	rootCancel context.CancelFunc
	checkWG    sync.WaitGroup // active flushBatch goroutines; Stop joins them

	// pending background checks, coalesced into one tsgo run (see CheckAsync)
	batchMu    sync.Mutex
	batch      []checkRequest
	batchTimer *time.Timer
}

// CheckRuns reports how many type-check processes were started (tests).
func (c *TSCompiler) CheckRuns() int64 { return c.checkRuns.Load() }

// checkRequest is one queued CheckAsync call.
type checkRequest struct {
	path        string
	registryDts string
	report      func([]TSDiag)
}

// checkBatchDelay is how long CheckAsync waits for further requests before
// running one tsgo process for all of them: rule files load milliseconds
// apart at startup and on a mass reload, and one program with N files costs
// about as much as one with a single file (measured 0.27 s per file vs
// 0.34 s for 20 files together on a controller).
const checkBatchDelay = 300 * time.Millisecond

// sloppyJsCodes are semantic TypeScript diagnostics that reject constructs
// which are valid JavaScript in a (sloppy-mode) rule file and behave as the
// author intends at runtime; they are dropped for .js files (in .ts they stay).
//
//	2362/2363 arithmetic on a non-number operand (new Date() - t0)
//	2410 with statement, 2703 delete of an identifier
//
// Grammar-class diagnostics (TS1xxx: legacy octal 0755, top-level return,
// ...) are deliberately NOT dropped even though the runtime accepts them:
// a parse error (the octal literal, for one) makes TypeScript report no
// semantic diagnostics at all for the whole program, so such a construct
// silently disables the type check of its file - the user should see why
// (and 0o755 / a wrapping function fix it).
var sloppyJsCodes = map[int]bool{2362: true, 2363: true, 2410: true, 2703: true}

// isSyntacticCode reports whether a TypeScript diagnostic code is in the
// grammar range (TS1xxx). A parse error there makes tsgo skip semantic
// checking for the whole program, which is why checkMany re-runs without
// the files that produced one. The range is an over-approximation: a few
// TS1xxx codes are raised by the checker's grammar pass (top-level return,
// TS1108) and do not suppress anything - they only cost a needless second
// run, never a lost diagnostic.
func isSyntacticCode(code int) bool { return code >= 1000 && code < 2000 }

// TSSyntaxError is a fatal transpile-time (syntax) error.
type TSSyntaxError struct {
	FileName string
	Line     int
	Text     string
	Code     int // TypeScript diagnostic code (TSnnnn), 0 if unknown
}

func (e *TSSyntaxError) Error() string {
	return fmt.Sprintf("TypeScript: %s (line %d)", e.Text, e.Line)
}

// SourceLine anchors load errors to the .ts source line (see the
// preprocessor error handling in LoadScenario).
func (e *TSSyntaxError) SourceLine() int {
	return e.Line
}

// TSDiag is one type-checker diagnostic.
type TSDiag struct {
	File     string
	Line     int
	Column   int
	Severity string // "error" or "warning"
	Message  string
	Code     int // TypeScript diagnostic code (TSnnnn)
}

const tsScriptTargetESNext = 99 // core.ScriptTarget enum value

func NewTSCompiler(binPath, typesPath string) *TSCompiler {
	c := &TSCompiler{binPath: binPath, lineMaps: map[string][]int{}, checkSem: make(chan struct{}, 2)}
	c.rootCtx, c.rootCancel = context.WithCancel(context.Background())
	if typesPath != "" {
		if _, err := os.Stat(typesPath); err == nil {
			c.typesGl = typesPath
		} else {
			// without the declarations every check would report nothing but
			// "Cannot find name 'defineRule'" - say so once, loudly, and have
			// checkMany refuse to run instead (see below)
			wbgong.Error.Printf("ts check: API declarations %s are not readable (%s); type checks are disabled", typesPath, err)
		}
	}
	return c
}

// CheckSupported reports whether the background type check can run at all:
// it needs the API declarations (-ts-types). Transpilation does not.
func (c *TSCompiler) CheckSupported() bool { return c != nil && c.typesGl != "" }

// Available reports whether the tsgo binary exists.
func (c *TSCompiler) Available() bool {
	if c == nil || c.binPath == "" {
		return false
	}
	_, err := exec.LookPath(c.binPath)
	return err == nil
}

func (c *TSCompiler) ensureStartedLocked() error {
	if c.cmd != nil {
		return nil
	}
	// binPath is the -tsgo flag (default /usr/bin/tsgo), set by the operator
	// who runs the daemon, never by rule code or RPC; the arguments are fixed.
	cmd := exec.Command(c.binPath, "--api", "--async") // nosemgrep
	// die with the parent even if wb-rules crashes without calling Stop
	cmd.SysProcAttr = &syscall.SysProcAttr{Pdeathsig: syscall.SIGKILL}
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return err
	}
	// keep the tail of the child's stderr: a crashing compiler would
	// otherwise leave no trace at all
	var stderr tailBuffer
	cmd.Stderr = &stderr
	if err := cmd.Start(); err != nil {
		return err
	}
	c.cmd = cmd
	c.stdin = stdin
	c.stdout = bufio.NewReader(stdout)
	go func() {
		err := cmd.Wait() // reap
		c.mu.Lock()
		stopped := c.stopped
		if c.cmd == cmd {
			c.cmd = nil // next call respawns
		}
		c.mu.Unlock()
		if err != nil && !stopped {
			wbgong.Error.Printf("ts compiler process exited: %v%s", err, stderr.Tail())
		}
	}()
	return nil
}

// tailBuffer keeps the last few KB written to it (a child's stderr).
type tailBuffer struct {
	mu  sync.Mutex
	buf []byte
}

const tailBufferCap = 4096

func (b *tailBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	b.buf = append(b.buf, p...)
	if len(b.buf) > tailBufferCap {
		b.buf = b.buf[len(b.buf)-tailBufferCap:]
	}
	b.mu.Unlock()
	return len(p), nil
}

// Tail returns the kept output formatted for appending to a log line.
func (b *tailBuffer) Tail() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	t := strings.TrimSpace(string(b.buf))
	if t == "" {
		return ""
	}
	return "; stderr: " + t
}

// checkOutputHeadCap/checkOutputTailCap bound how much of a check run's
// combined output the engine retains: the parseable diagnostics come first
// (hundreds of bytes each - the head holds thousands of them), and the tail
// keeps whatever a crashing compiler printed last. Anything between is
// dropped; boundedBuffer.Truncated() reports that it happened, and checkMany
// then refuses to call diagnostic-less files clean.
const (
	checkOutputHeadCap = 512 << 10
	checkOutputTailCap = 64 << 10
)

// boundedBuffer is an io.Writer that retains only the head and tail of what
// is written, so a compiler flooding stdout/stderr cannot make the engine
// buffer the whole stream.
type boundedBuffer struct {
	mu    sync.Mutex
	head  []byte
	tail  []byte
	total int64
}

func (b *boundedBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	n := len(p)
	b.total += int64(n)
	if room := checkOutputHeadCap - len(b.head); room > 0 {
		take := room
		if take > len(p) {
			take = len(p)
		}
		b.head = append(b.head, p[:take]...)
		p = p[take:]
	}
	if len(p) > 0 {
		if len(p) >= checkOutputTailCap {
			b.tail = append(b.tail[:0], p[len(p)-checkOutputTailCap:]...)
		} else {
			b.tail = append(b.tail, p...)
			if len(b.tail) > checkOutputTailCap {
				b.tail = append(b.tail[:0], b.tail[len(b.tail)-checkOutputTailCap:]...)
			}
		}
	}
	return n, nil
}

// Truncated reports whether part of the output was dropped.
func (b *boundedBuffer) Truncated() bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.total > int64(len(b.head)+len(b.tail))
}

// String renders the retained output; a dropped middle is marked on a line
// of its own so the per-line diagnostic parser never sees a spliced line.
func (b *boundedBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	dropped := b.total - int64(len(b.head)+len(b.tail))
	if dropped <= 0 {
		return string(b.head) + string(b.tail)
	}
	return fmt.Sprintf("%s\n[... %d bytes of compiler output dropped ...]\n%s", b.head, dropped, b.tail)
}

func (c *TSCompiler) writeMsg(v any) error {
	b, err := json.Marshal(v)
	if err != nil {
		return err
	}
	_, err = fmt.Fprintf(c.stdin, "Content-Length: %d\r\n\r\n%s", len(b), b)
	return err
}

// maxFrameBytes bounds one tsgo api reply frame. The Content-Length header
// comes from the child process and directly sizes an allocation, so a corrupt
// (or garbage) value must not turn into an equally large allocation inside
// the service's memory cgroup. A legitimate transpile reply carries one rule
// file's output plus its source map - nowhere near this bound. An oversized
// frame is a transport error: the caller kills and respawns the child.
const maxFrameBytes = 16 << 20

func (c *TSCompiler) readMsg() (map[string]json.RawMessage, error) {
	contentLen := -1
	for {
		line, err := c.stdout.ReadString('\n')
		if err != nil {
			return nil, err
		}
		if strings.HasPrefix(line, "Content-Length:") {
			n, err := strconv.Atoi(strings.TrimSpace(strings.TrimPrefix(line, "Content-Length:")))
			if err != nil {
				return nil, err
			}
			if n < 0 || n > maxFrameBytes {
				return nil, fmt.Errorf("tsgo api: implausible frame size %d", n)
			}
			contentLen = n
		}
		if line == "\r\n" || line == "\n" {
			break
		}
	}
	if contentLen < 0 {
		return nil, fmt.Errorf("tsgo api: missing Content-Length")
	}
	buf := make([]byte, contentLen)
	if _, err := io.ReadFull(c.stdout, buf); err != nil {
		return nil, err
	}
	var m map[string]json.RawMessage
	if err := json.Unmarshal(buf, &m); err != nil {
		return nil, err
	}
	return m, nil
}

func (c *TSCompiler) kill() {
	if c.cmd != nil && c.cmd.Process != nil {
		_ = c.cmd.Process.Kill()
	}
	c.cmd = nil
}

// Transpile type-strips TypeScript source to JavaScript. Line numbers are
// preserved: the emitted "use strict" prologue line is removed so that
// source line N stays at output line N (rule files are wrapped in a
// single-line function header by LoadScenario, exactly like .js files).
func (c *TSCompiler) Transpile(src, fileName string) (string, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	out, err := c.transpileLocked(src, fileName)
	var syntaxErr *TSSyntaxError
	if err != nil && !errors.As(err, &syntaxErr) {
		// one respawn attempt for transport errors: the child may have died.
		// A syntax error is a healthy answer - keep the warm child.
		c.kill()
		out, err = c.transpileLocked(src, fileName)
		var stillSyntax *TSSyntaxError
		if err != nil && !errors.As(err, &stillSyntax) {
			// the raw transport error (EOF, broken pipe) says nothing to the
			// person reading the load error or the Editor.Check verdict
			err = fmt.Errorf("tsgo transpile failed (compiler child unresponsive or exited): %w", err)
		}
	}
	return out, err
}

func (c *TSCompiler) transpileLocked(src, fileName string) (string, error) {
	if err := c.ensureStartedLocked(); err != nil {
		return "", fmt.Errorf("cannot start tsgo: %w", err)
	}
	c.nextID++
	req := map[string]any{
		"jsonrpc": "2.0",
		"id":      c.nextID,
		"method":  "transpileModule",
		"params": map[string]any{
			"input": src,
			"options": map[string]any{
				"fileName": fileName,
				"compilerOptions": map[string]any{
					"target":    tsScriptTargetESNext,
					"sourceMap": true,
				},
				"reportDiagnostics": true,
			},
		},
	}
	// a wedged (not dead) child would block readMsg forever - and
	// Transpile runs on the engine loop; kill the process on deadline so
	// the read unblocks and the caller's respawn path takes over
	proc := c.cmd.Process
	watchdog := time.AfterFunc(15*time.Second, func() { proc.Kill() })
	defer watchdog.Stop()
	if err := c.writeMsg(req); err != nil {
		return "", err
	}
	resp, err := c.readMsg()
	if err != nil {
		return "", err
	}
	var respID int64
	if err := json.Unmarshal(resp["id"], &respID); err != nil || respID != c.nextID {
		// unsolicited or out-of-order message: the framing is no longer
		// trustworthy; treat as transport failure (kill + respawn)
		return "", fmt.Errorf("tsgo api: response id mismatch")
	}
	if e, ok := resp["error"]; ok {
		return "", fmt.Errorf("tsgo transpile: %s", string(e))
	}
	var res struct {
		OutputText    string `json:"outputText"`
		SourceMapText string `json:"sourceMapText"`
		Diagnostics   []struct {
			Pos      int    `json:"pos"`
			Category int    `json:"category"`
			Code     int    `json:"code"`
			Text     string `json:"text"`
		} `json:"diagnostics"`
	}
	if err := json.Unmarshal(resp["result"], &res); err != nil {
		return "", err
	}
	for _, d := range res.Diagnostics {
		// category: per typescript, 1 = Error; ignore warnings (0), suggestions
		// (2) and messages (3) - only a real error should fail an otherwise
		// runnable transpile.
		if d.Category != 1 {
			continue
		}
		return "", &TSSyntaxError{FileName: fileName, Line: lineForUTF16Pos(src, d.Pos), Text: d.Text, Code: d.Code}
	}
	prologueLines := 0
	out := res.OutputText
	// drop the prologue line to keep line numbers aligned with the source
	for _, prologue := range []string{"\"use strict\";\r\n", "\"use strict\";\n"} {
		if strings.HasPrefix(out, prologue) {
			out = out[len(prologue):]
			prologueLines = 1
			break
		}
	}
	// tsgo's emitter reformats the output (unlike classic tsc, line
	// positions are NOT preserved), so keep the source map's line table for
	// traceback translation.
	if table := sourceMapLineTable(res.SourceMapText, prologueLines); table != nil {
		c.mapsMu.Lock()
		c.lineMaps[fileName] = table
		c.mapsMu.Unlock()
	}
	return out, nil
}

// Forget drops the line table of a file that is no longer loaded.
func (c *TSCompiler) Forget(file string) {
	c.mapsMu.Lock()
	delete(c.lineMaps, file)
	c.mapsMu.Unlock()
}

// TranslateLine maps a generated (transpiled) line back to the .ts source
// line for a previously transpiled file.
func (c *TSCompiler) TranslateLine(file string, genLine int) (int, bool) {
	c.mapsMu.Lock()
	defer c.mapsMu.Unlock()
	table, ok := c.lineMaps[file]
	if !ok || genLine < 1 || genLine > len(table) {
		return 0, false
	}
	src := table[genLine-1]
	if src <= 0 {
		return 0, false
	}
	return src, true
}

// lineForUTF16Pos converts a UTF-16 code-unit offset (the unit tsgo's
// transpile diagnostics use) into a 1-based line number of the UTF-8
// source. Byte-based counting drifts one unit per non-ASCII character
// before the error - fatal for the Cyrillic comments common in rules.
func lineForUTF16Pos(src string, pos int) int {
	line, units := 1, 0
	for _, r := range src {
		if units >= pos {
			break
		}
		if r == '\n' {
			line++
		}
		if r > 0xFFFF {
			units += 2
		} else {
			units++
		}
	}
	return line
}

const b64chars = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789+/"

// sourceMapLineTable decodes a V3 source map's mappings into a table of
// source lines (1-based) indexed by generated line. prologueShift accounts
// for prologue lines stripped from the generated output after mapping.
func sourceMapLineTable(mapText string, prologueShift int) []int {
	if mapText == "" {
		return nil
	}
	var m struct {
		Mappings string `json:"mappings"`
	}
	if json.Unmarshal([]byte(mapText), &m) != nil || m.Mappings == "" {
		return nil
	}
	var table []int
	srcLine := 0 // deltas accumulate across the whole mappings string
	for _, lineStr := range strings.Split(m.Mappings, ";") {
		lineSrc := -1
		for _, seg := range strings.Split(lineStr, ",") {
			if seg == "" {
				continue
			}
			fields := decodeVLQ(seg)
			if len(fields) >= 4 {
				srcLine += fields[2]
				if lineSrc < 0 {
					lineSrc = srcLine + 1 // to 1-based
				}
			}
		}
		table = append(table, lineSrc)
	}
	if prologueShift > 0 && len(table) >= prologueShift {
		table = table[prologueShift:]
	}
	return table
}

func decodeVLQ(s string) []int {
	var out []int
	shift, value := 0, 0
	for _, ch := range s {
		d := strings.IndexRune(b64chars, ch)
		if d < 0 {
			return out
		}
		value |= (d & 31) << shift
		if d&32 != 0 {
			shift += 5
			continue
		}
		if value&1 != 0 {
			out = append(out, -(value >> 1))
		} else {
			out = append(out, value>>1)
		}
		shift, value = 0, 0
	}
	return out
}

// tsgo --noEmit output: "path(line,col): error TS1234: message"
var tsDiagRx = regexp.MustCompile(`^(.+?)\((\d+),(\d+)\): (error|warning) TS(\d+): (.*)$`)

// any TSxxxx error token, used to detect an error tsgo printed without a
// (line,col) prefix (which tsDiagRx would miss) while still exiting 0.
var tsErrorTokenRx = regexp.MustCompile(`error TS\d+`)

// pathsRefSameFile reports whether a tsgo-printed (cwd-relative) path
// and a local path refer to the same file, requiring a separator
// boundary so other/bar.ts never matches mother/bar.ts.
func pathsRefSameFile(a, b string) bool {
	if a == b {
		return true
	}
	// tsgo prints diagnostic paths relative to its cwd (possibly with "..").
	// Resolve both against the cwd and compare cleaned absolutes, so the match
	// holds regardless of where wb-rules runs (e.g. cwd not an ancestor of
	// /tmp or /usr/share). Keep the suffix check as a fallback.
	if wd, err := os.Getwd(); err == nil {
		ra, rb := a, b
		if !filepath.IsAbs(ra) {
			ra = filepath.Join(wd, ra)
		}
		if !filepath.IsAbs(rb) {
			rb = filepath.Join(wd, rb)
		}
		if filepath.Clean(ra) == filepath.Clean(rb) {
			return true
		}
	}
	return strings.HasSuffix(a, "/"+b) || strings.HasSuffix(b, "/"+a)
}

// checkMany type-checks the given rule files in ONE tsgo program and returns
// the diagnostics per requested path. A diagnostic in a file that is not one
// of the requested paths (a relatively required sibling) is attributed to the
// first path; it carries the other file's absolute path in File, which the
// journal shows and Editor.Check consumers skip. For a .js path the
// diagnostics are advisory: sloppy-JS-valid constructs (sloppyJsCodes) are
// dropped and the rest are reported as warnings.
func (c *TSCompiler) checkMany(paths []string, registryDts string) (map[string][]TSDiag, error) {
	// --lib esnext: no DOM globals, so rule-script names like 'history'
	// or 'name' don't collide with browser declarations.
	// --module esnext + --moduleDetection force: rule files may use
	// top-level await (the runtime wraps them in an async function), which
	// TypeScript only permits in a module.
	// --allowJs --checkJs: type-check .js rule files too (against the wb-rules
	// types + the live-device registry), so a wrong-typed write like
	// dev["buzzer/enabled"] = 123 is caught in legacy .js as well as .ts. No-op
	// for a .ts check. TypeScript parses each file per its extension, so a .js
	// file is still parsed as JavaScript (e.g. `a < b > (c)` stays comparisons).
	if c.typesGl == "" {
		return nil, fmt.Errorf("type check did not run: the API declarations (-ts-types) are missing")
	}
	// A file that vanished between the request and the run (saved then
	// deleted, renamed or disabled within the batch window) makes tsgo abort
	// the whole program with TS6053 and no diagnostics for anyone - drop it
	// here instead of failing every co-batched file.
	present := paths[:0:0]
	for _, p := range paths {
		if _, err := os.Stat(p); err == nil {
			present = append(present, p)
		}
	}
	results := make(map[string][]TSDiag, len(paths))
	if len(present) == 0 {
		return results, nil
	}
	paths = present
	// --pretty false: diagnostics in the plain "path(line,col): error TSnnnn"
	// form the parser below expects, whatever the default becomes
	args := []string{"--noEmit", "--pretty", "false", "--target", "esnext", "--lib", "esnext",
		"--strict", "false", "--module", "esnext", "--moduleDetection", "force",
		"--allowJs", "--checkJs"}
	args = append(args, paths...)
	args = append(args, c.typesGl)
	// The registry (a generated `interface WbControls { ... }` built from the
	// controller's live device table) declaration-merges into the empty
	// WbControls in wb-rules.d.ts, so this on-controller check flags wrong
	// types on the stringly-referenced APIs (dev["dev/ctrl"], getControl(...))
	// the same way the editor does. Passed as a temp file since tsgo takes
	// file paths.
	var registryPath string
	if registryDts != "" {
		if f, err := os.CreateTemp("", "wb-controls-*.d.ts"); err == nil {
			registryPath = f.Name()
			_, _ = f.WriteString(registryDts)
			_ = f.Close()
			defer os.Remove(registryPath)
			args = append(args, registryPath)
		} else {
			wbgong.Error.Printf("ts check: cannot write registry temp: %s", err)
		}
	}
	// rootCtx parent: Stop() cancels it, killing this transient child too
	checkCtx, cancel := context.WithTimeout(c.rootCtx, 60*time.Second)
	defer cancel()
	// same binary as above; args are tsgo options plus file paths that the
	// engine itself resolved from its rule directories (see checkMany above)
	checkCmd := exec.CommandContext(checkCtx, c.binPath, args...) // nosemgrep
	checkCmd.SysProcAttr = &syscall.SysProcAttr{Pdeathsig: syscall.SIGKILL}
	// a killed compiler that leaked its output pipe to a grandchild must
	// not stall this goroutine until the grandchild exits
	checkCmd.WaitDelay = 2 * time.Second
	// head+tail instead of CombinedOutput: diagnostics are line-sized, so an
	// output flood must not be retained wholesale (see boundedBuffer)
	var outBuf boundedBuffer
	checkCmd.Stdout = &outBuf
	checkCmd.Stderr = &outBuf
	c.checkRuns.Add(1)
	runErr := checkCmd.Run() // exit 1 on diags: expected
	out := outBuf.String()
	for _, p := range paths {
		results[p] = nil
	}
	parsed := 0        // positional diagnostics seen, filtered or not
	unpositioned := "" // first error tsgo printed without a (line,col) prefix
	for _, line := range strings.Split(string(out), "\n") {
		g := tsDiagRx.FindStringSubmatch(strings.TrimRight(line, "\r"))
		if g == nil {
			// an error with no position (config trouble, a vanished file -
			// TS6053) is not attributable to any one file; remember it so
			// no co-batched file gets a false clean verdict below
			if unpositioned == "" && tsErrorTokenRx.MatchString(line) {
				unpositioned = strings.TrimSpace(strings.TrimRight(line, "\r"))
			}
			continue
		}
		parsed++
		ln, _ := strconv.Atoi(g[2])
		col, _ := strconv.Atoi(g[3])
		code, _ := strconv.Atoi(g[5])
		if c.typesGl != "" && pathsRefSameFile(g[1], c.typesGl) {
			if isSyntacticCode(code) {
				// a parse error in the declarations suppresses semantic
				// checking of the WHOLE program: a silent "clean" verdict
				// for every file would be a lie
				return nil, fmt.Errorf("type check did not run: the API declarations are broken: %s", strings.TrimRight(line, "\r"))
			}
			continue // a broken d.ts should not spam every file's check
		}
		if registryPath != "" && pathsRefSameFile(g[1], registryPath) {
			if isSyntacticCode(code) {
				return nil, fmt.Errorf("type check did not run: the generated controls registry is broken: %s", strings.TrimRight(line, "\r"))
			}
			continue // diagnostics inside the generated registry are ours, not the rule's
		}
		// tsgo prints paths relative to its cwd ("usr/share/..." when the
		// service runs from /, "../../tmp/..." in tests). Report a requested
		// file by the path it was requested with and anything else as an
		// absolute path, so log lines and Editor.Check carry real, clickable
		// paths.
		// A diagnostic outside the batch (possible only via cross-file
		// resolution, which require()-style loading keeps rare) is charged
		// to paths[0] and takes that file's .js/.ts severity policy.
		owner := paths[0]
		file := g[1]
		for _, p := range paths {
			if pathsRefSameFile(file, p) {
				owner, file = p, p
				break
			}
		}
		if file == g[1] {
			if abs, err := filepath.Abs(file); err == nil {
				file = abs
			}
		}
		severity := g[4]
		if strings.HasSuffix(owner, ".js") {
			if sloppyJsCodes[code] {
				continue
			}
			severity = "warning" // advisory in JavaScript, which is not typed by contract
		}
		results[owner] = append(results[owner], TSDiag{File: file, Line: ln, Column: col, Severity: severity, Message: g[6], Code: code})
	}
	// tsgo exits 1 when it prints diagnostics - that is a healthy answer.
	// A cancelled or timed-out run, or a failure with nothing parseable,
	// means the check never really ran; the caller must not record a clean
	// verdict.
	if c.rootCtx.Err() != nil {
		return nil, fmt.Errorf("the compiler is shutting down")
	}
	if checkCtx.Err() != nil {
		return nil, fmt.Errorf("type check timed out")
	}
	// Exit 0 (clean) and exit 1 (diagnostics printed) are the healthy
	// outcomes. Anything else - a crash or signal, an error printed without
	// a position, output past the retention bound - means the run is not
	// trustworthy beyond the diagnostics it did print: a file WITH its own
	// diagnostics keeps them (they are real), but a file with none was not
	// necessarily checked and must not be recorded clean.
	abnormal := ""
	if runErr != nil {
		var exitErr *exec.ExitError
		if !errors.As(runErr, &exitErr) || exitErr.ExitCode() != 1 {
			abnormal = "compiler exited abnormally: " + runErr.Error()
		}
	}
	if abnormal == "" && unpositioned != "" {
		abnormal = unpositioned
	}
	if abnormal == "" && outBuf.Truncated() {
		abnormal = "compiler output exceeded the retention limit"
	}
	if abnormal != "" {
		if parsed == 0 {
			return nil, fmt.Errorf("type check failed: %v", abnormal)
		}
		for _, p := range paths {
			if len(results[p]) == 0 {
				results[p] = []TSDiag{{File: p, Line: 1, Column: 1, Severity: "warning",
					Message: "type check did not run: " + abnormal}}
			}
		}
		return results, nil
	}
	// A syntax error in ONE file makes TypeScript skip semantic checking for
	// the WHOLE program, so in a batch it would hide every other file's type
	// errors. Check the files without syntax errors again on their own; the
	// blocked files keep their (syntactic) diagnostics.
	blocked := map[string]bool{}
	for owner, diags := range results {
		for _, d := range diags {
			if d.File == owner && isSyntacticCode(d.Code) {
				blocked[owner] = true
				break
			}
		}
	}
	if len(blocked) > 0 && len(blocked) < len(paths) {
		rest := make([]string, 0, len(paths)-len(blocked))
		for _, p := range paths {
			if !blocked[p] {
				rest = append(rest, p)
			}
		}
		again, err := c.checkMany(rest, registryDts)
		if err != nil {
			return nil, err
		}
		for p, diags := range again {
			results[p] = diags
		}
	}
	// tsgo exited 1 but nothing parsed and nothing matched the error-token
	// scan above (the exit-0-with-unpositioned-error case is caught there):
	// the check did not really run - a bare exit-code pass here would record
	// a false "clean" verdict for files that were never validated. Filtered
	// diagnostics (d.ts/registry internals, sloppy-JS codes) still count as
	// parsed: the check did run.
	if parsed == 0 && runErr != nil {
		reason := firstLineOf(out)
		if reason == "" {
			reason = runErr.Error()
		}
		return nil, fmt.Errorf("type check failed: %v", reason)
	}
	return results, nil
}

func firstLineOf(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		s = s[:i]
	}
	return strings.TrimSpace(s)
}

// tsStopJoinTimeout bounds how long Stop waits for active check goroutines:
// their tsgo children are killed by the rootCtx cancellation, so the join is
// normally instant; the bound only keeps a pathologically wedged goroutine
// from hanging the engine's shutdown.
const tsStopJoinTimeout = 5 * time.Second

// Stop terminates the persistent tsgo child (if any), cancels active
// background checks (killing their transient tsgo children) and joins the
// check goroutines, with a bound.
func (c *TSCompiler) Stop() {
	if c == nil {
		return
	}
	// drop queued background checks: their reports go to an engine that is
	// stopping (the engine-side callback ignores them anyway). Cancelling
	// under batchMu orders this against flushBatch: any flush that saw the
	// context alive has already registered with checkWG.
	c.batchMu.Lock()
	if c.batchTimer != nil {
		c.batchTimer.Stop()
		c.batchTimer = nil
	}
	c.batch = nil
	c.rootCancel()
	c.batchMu.Unlock()
	joined := make(chan struct{})
	go func() {
		c.checkWG.Wait()
		close(joined)
	}()
	select {
	case <-joined:
	case <-time.After(tsStopJoinTimeout):
		wbgong.Error.Printf("ts check: background checks did not finish within %s of Stop", tsStopJoinTimeout)
	}
	c.mu.Lock()
	c.stopped = true
	c.kill()
	c.mu.Unlock()
}

// CheckAsync queues a background type check of path and hands its
// diagnostics to report (called from a background goroutine; the callback
// must handle its own syncing). Requests arriving within checkBatchDelay of
// each other are checked in ONE tsgo program; a newer request for the same
// path replaces a queued older one (the caller's generation guard would
// discard the older result anyway).
func (c *TSCompiler) CheckAsync(path string, registryDts string, report func([]TSDiag)) {
	c.batchMu.Lock()
	defer c.batchMu.Unlock()
	req := checkRequest{path: path, registryDts: registryDts, report: report}
	for i := range c.batch {
		if c.batch[i].path == path {
			// drop the queued older request; the new one is appended at the
			// END: flushBatch checks the whole batch against the LAST
			// request's registry snapshot (the newest), and an in-place
			// replacement would leave a request newer than that snapshot
			c.batch = append(c.batch[:i], c.batch[i+1:]...)
			break
		}
	}
	c.batch = append(c.batch, req)
	if c.batchTimer == nil {
		c.batchTimer = time.AfterFunc(checkBatchDelay, c.flushBatch)
	}
}

func (c *TSCompiler) flushBatch() {
	c.batchMu.Lock()
	reqs := c.batch
	c.batch = nil
	c.batchTimer = nil
	if len(reqs) == 0 {
		c.batchMu.Unlock()
		return
	}
	if c.rootCtx.Err() != nil {
		// Stop was called; run nothing, but still give every request a
		// terminal verdict (the engine-side callback ignores it anyway)
		c.batchMu.Unlock()
		for _, req := range reqs {
			req.report([]TSDiag{{File: req.path, Line: 1, Column: 1, Severity: "warning",
				Message: "type check did not run: the compiler is shutting down"}})
		}
		return
	}
	// register under batchMu: Stop cancels rootCtx under the same lock, so
	// it can never miss a goroutine that saw the context alive above
	c.checkWG.Add(1)
	c.batchMu.Unlock()
	go func() {
		defer c.checkWG.Done()
		reported := 0
		defer func() {
			if r := recover(); r != nil {
				wbgong.Error.Printf("ts check panic: %v", r)
				// every request still gets a terminal verdict: a swallowed
				// panic must not leave files pending forever; requests whose
				// real verdict was already delivered keep it
				for _, req := range reqs[reported:] {
					req.report([]TSDiag{{File: req.path, Line: 1, Column: 1, Severity: "warning",
						Message: fmt.Sprintf("type check did not run: internal error: %v", r)}})
				}
			}
		}()
		// a full tsgo process per batch: bound the parallelism so a mass
		// reload of many files does not stampede a small controller
		c.checkSem <- struct{}{}
		defer func() { <-c.checkSem }()
		paths := make([]string, len(reqs))
		for i, r := range reqs {
			paths[i] = r.path
		}
		// the registry only grows during a load burst; the newest snapshot
		// covers every request in the batch
		results, err := c.checkMany(paths, reqs[len(reqs)-1].registryDts)
		for _, r := range reqs {
			diags := results[r.path]
			if err != nil {
				// surface the failure as a warning diag: 'checked clean' and
				// 'check never ran' must stay distinguishable
				diags = []TSDiag{{File: r.path, Line: 1, Column: 1, Severity: "warning",
					Message: "type check did not run: " + err.Error()}}
			}
			r.report(diags)
			reported++
		}
	}()
}
