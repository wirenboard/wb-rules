package wbrules

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"syscall"

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

	cmd    *exec.Cmd
	stdin  io.WriteCloser
	stdout *bufio.Reader
	nextID int64

	mapsMu   sync.Mutex
	lineMaps map[string][]int // per transpiled file: generated line (1-based) -> source line
}

// TSSyntaxError is a fatal transpile-time (syntax) error.
type TSSyntaxError struct {
	FileName string
	Line     int
	Text     string
}

func (e *TSSyntaxError) Error() string {
	return fmt.Sprintf("TypeScript: %s (line %d)", e.Text, e.Line)
}

// TSDiag is one type-checker diagnostic.
type TSDiag struct {
	File     string
	Line     int
	Column   int
	Severity string // "error" or "warning"
	Message  string
}

const tsScriptTargetESNext = 99 // core.ScriptTarget enum value

func NewTSCompiler(binPath, typesPath string) *TSCompiler {
	c := &TSCompiler{binPath: binPath, lineMaps: map[string][]int{}}
	if typesPath != "" {
		if _, err := os.Stat(typesPath); err == nil {
			c.typesGl = typesPath
		}
	}
	return c
}

// Available reports whether the tsgo binary exists.
func (c *TSCompiler) Available() bool {
	if c == nil || c.binPath == "" {
		return false
	}
	_, err := exec.LookPath(c.binPath)
	return err == nil
}

func (c *TSCompiler) ensureStartedLocked() error {
	if c.cmd != nil && c.cmd.ProcessState == nil {
		return nil
	}
	cmd := exec.Command(c.binPath, "--api", "--async")
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
	cmd.Stderr = nil
	if err := cmd.Start(); err != nil {
		return err
	}
	c.cmd = cmd
	c.stdin = stdin
	c.stdout = bufio.NewReader(stdout)
	go func() { _ = cmd.Wait() }() // reap; next call notices death and respawns
	return nil
}

func (c *TSCompiler) writeMsg(v any) error {
	b, err := json.Marshal(v)
	if err != nil {
		return err
	}
	_, err = fmt.Fprintf(c.stdin, "Content-Length: %d\r\n\r\n%s", len(b), b)
	return err
}

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
	if err != nil {
		// one respawn attempt: the child may have died
		c.kill()
		out, err = c.transpileLocked(src, fileName)
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
	if err := c.writeMsg(req); err != nil {
		return "", err
	}
	resp, err := c.readMsg()
	if err != nil {
		return "", err
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
			Text     string `json:"text"`
		} `json:"diagnostics"`
	}
	if err := json.Unmarshal(resp["result"], &res); err != nil {
		return "", err
	}
	for _, d := range res.Diagnostics {
		// category: per typescript, 1 = Error
		line := 1 + strings.Count(src[:min(d.Pos, len(src))], "\n")
		return "", &TSSyntaxError{FileName: fileName, Line: line, Text: d.Text}
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
var tsDiagRx = regexp.MustCompile(`^(.+?)\((\d+),(\d+)\): (error|warning) TS\d+: (.*)$`)

// Check runs the full type checker on the given file (plus the wb-rules
// builtin declarations) and returns diagnostics. Blocking — run it from a
// goroutine; rule execution must never wait for it.
func (c *TSCompiler) Check(path string) []TSDiag {
	// --lib esnext: no DOM globals, so rule-script names like 'history'
	// or 'name' don't collide with browser declarations
	args := []string{"--noEmit", "--target", "esnext", "--lib", "esnext", "--strict", "false", path}
	if c.typesGl != "" {
		args = append(args, c.typesGl)
	}
	out, _ := exec.Command(c.binPath, args...).CombinedOutput() // exit 2 on diags: expected
	var diags []TSDiag
	for _, line := range strings.Split(string(out), "\n") {
		if g := tsDiagRx.FindStringSubmatch(strings.TrimRight(line, "\r")); g != nil {
			ln, _ := strconv.Atoi(g[2])
			col, _ := strconv.Atoi(g[3])
			if c.typesGl != "" && g[1] == c.typesGl {
				continue // a broken d.ts should not spam every file's check
			}
			diags = append(diags, TSDiag{File: g[1], Line: ln, Column: col, Severity: g[4], Message: g[5]})
		}
	}
	return diags
}

// Stop terminates the persistent tsgo child (if any).
func (c *TSCompiler) Stop() {
	if c == nil {
		return
	}
	c.mu.Lock()
	c.kill()
	c.mu.Unlock()
}

// CheckAsync runs Check in the background and hands results to report
// (called from the goroutine; the callback must handle its own syncing).
func (c *TSCompiler) CheckAsync(path string, report func([]TSDiag)) {
	go func() {
		defer func() {
			if r := recover(); r != nil {
				wbgong.Error.Printf("ts check panic for %s: %v", path, r)
			}
		}()
		report(c.Check(path))
	}()
}
