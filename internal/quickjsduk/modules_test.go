package duktape

import (
	"fmt"
	"path"
	"strings"
	"testing"
)

// testModuleHost serves modules from an in-memory map keyed by absolute
// path. Bare specifiers live under /mods, relative ones resolve against the
// importing module's path, extensionless ones probe ".js".
type testModuleHost struct {
	files map[string]string
	loads []string // record of LoadModuleSource calls
}

func (h *testModuleHost) ResolveModule(base, spec string) (string, error) {
	var p string
	switch {
	case strings.HasPrefix(spec, "./") || strings.HasPrefix(spec, "../"):
		if base == "" {
			return "", fmt.Errorf("cannot resolve %q without a base", spec)
		}
		p = path.Join(path.Dir(base), spec)
	case strings.HasPrefix(spec, "/"):
		p = spec
	default:
		p = "/mods/" + spec
	}
	for _, cand := range []string{p, p + ".js"} {
		if _, ok := h.files[cand]; ok {
			return cand, nil
		}
	}
	return "", fmt.Errorf("cannot find module %q", spec)
}

func (h *testModuleHost) LoadModuleSource(name string) (string, error) {
	src, ok := h.files[name]
	if !ok {
		return "", fmt.Errorf("cannot find module %q", name)
	}
	h.loads = append(h.loads, name)
	return src, nil
}

func (h *testModuleHost) InitCjsModule(d *Context, name string) {
	d.PushString(name)
	d.PutPropString(-2, "filename")
}

func (h *testModuleHost) InitImportMeta(d *Context, name string) {
	d.PushString("file://" + name)
	d.PutPropString(-2, "url")
	d.PushString(name)
	d.PutPropString(-2, "filename")
}

// loadEntry compiles src as a rule-file entry (the classic async wrapper or
// an ES module) at `filename`, runs it and settles its result. Fails the
// test on load errors.
func loadEntry(t *testing.T, ctx *Context, filename, src string) {
	t.Helper()
	isModule, rc := ctx.CompileScriptOrModule(src, filename,
		"(async function(module, exports){", "\n})")
	if rc != 0 {
		t.Fatalf("compile of %s failed: %s", filename, ctx.SafeToString(-1))
	}
	if isModule {
		if rc := ctx.EvalModuleNoPump(); rc != 0 {
			t.Fatalf("module eval of %s failed: %s", filename, ctx.SafeToString(-1))
		}
	} else {
		// mimic wb-rules: call the wrapper with module and exports objects
		ctx.PushObject()
		ctx.PushObject()
		if rc := ctx.PcallNoPump(2); rc != 0 {
			t.Fatalf("script eval of %s failed: %s", filename, ctx.SafeToString(-1))
		}
	}
	if st := ctx.PromiseStateTop(); st == PromiseRejected {
		ctx.PushPromiseResultTop()
		msg := ctx.SafeToString(-1)
		ctx.Pop()
		t.Fatalf("load of %s rejected: %s", filename, msg)
	}
	ctx.Pop()
	ctx.PumpJobs()
}

func evalString(t *testing.T, ctx *Context, expr string) string {
	t.Helper()
	if r := ctx.PevalString(expr); r != 0 {
		t.Fatalf("eval %q failed: %s", expr, ctx.SafeToString(-1))
	}
	got := ctx.SafeToString(-1)
	ctx.Pop()
	return got
}

func TestLooksLikeESModule(t *testing.T) {
	yes := []string{
		`import x from "y"`,
		`import { a, b } from "y";`,
		"\t import * as ns from 'y'",
		`import "side-effect"`,
		`import"tight"`,
		`log(import.meta.filename)`, // fails the wrapper, legal in a module
		`export default 42`,
		`export { a }`,
		`export * from "y"`,
		`export const x = 1`,
		"var a = 1;\nexport function f() {}",
	}
	no := []string{
		`importantThing = 1`,
		`exports.x = 1`,
		`var exporter = 5`,
		`import(dynamic)`,    // import() works in scripts
		`import ("dynamic")`, // still a call
		`x = import("mod")`,  // not at statement start
		`// import x from "y"`,
		` * import x from "y"`, // block-comment continuation line
		`log("import")`,
		`nothing here`,
	}
	for _, src := range yes {
		if !LooksLikeESModule(src) {
			t.Errorf("expected ESM: %q", src)
		}
	}
	for _, src := range no {
		if LooksLikeESModule(src) {
			t.Errorf("expected not ESM: %q", src)
		}
	}
}

func TestESModuleEntryImportsESModule(t *testing.T) {
	ctx := NewContext()
	defer ctx.DestroyHeap()
	host := &testModuleHost{files: map[string]string{
		"/mods/esmmod.js": "export const x = 40; export default 'dflt';",
	}}
	ctx.SetModuleHost(host)

	loadEntry(t, ctx, "/rules/a.js", `
import def, { x } from "esmmod";
globalThis.result = x + 2 + ':' + def;
globalThis.metaFile = import.meta.filename;
`)
	if got := evalString(t, ctx, "result"); got != "42:dflt" {
		t.Fatalf("bad result: %q", got)
	}
	// import.meta of the entry module is decorated by the host too
	if got := evalString(t, ctx, "metaFile"); got != "/rules/a.js" {
		t.Fatalf("bad import.meta.filename: %q", got)
	}
}

func TestImportCommonJSInterop(t *testing.T) {
	ctx := NewContext()
	defer ctx.DestroyHeap()
	ctx.SetModuleHost(&testModuleHost{files: map[string]string{
		"/mods/cjs.js": "exports.a = 1; exports.b = 2;",
	}})

	loadEntry(t, ctx, "/rules/a.js", `
import def from "cjs";
import { a, b } from "cjs";
import * as ns from "cjs";
globalThis.result = [a, b, def.a, ns.a, ns.default === def].join(',');
`)
	if got := evalString(t, ctx, "result"); got != "1,2,1,1,true" {
		t.Fatalf("bad interop result: %q", got)
	}
}

func TestRequireESModule(t *testing.T) {
	ctx := NewContext()
	defer ctx.DestroyHeap()
	ctx.SetModuleHost(&testModuleHost{files: map[string]string{
		"/mods/esmmod.js": "export const x = 42; export default 'd';",
	}})

	if got := evalString(t, ctx,
		`(function(){ var m = require("esmmod"); return m.x + ':' + m.default; })()`); got != "42:d" {
		t.Fatalf("bad require(esm) result: %q", got)
	}
}

func TestRequireTLAModuleFails(t *testing.T) {
	ctx := NewContext()
	defer ctx.DestroyHeap()
	ctx.SetModuleHost(&testModuleHost{files: map[string]string{
		"/mods/tla.js": "await Promise.resolve(); export const x = 1;",
	}})

	got := evalString(t, ctx, `(function(){
		try { require("tla"); return "no-error"; }
		catch (e) { return e.code; }
	})()`)
	if got != "ERR_REQUIRE_ASYNC_MODULE" {
		t.Fatalf("expected ERR_REQUIRE_ASYNC_MODULE, got %q", got)
	}
}

func TestRequireThrowingESModule(t *testing.T) {
	ctx := NewContext()
	defer ctx.DestroyHeap()
	ctx.SetModuleHost(&testModuleHost{files: map[string]string{
		"/mods/boom.js": "export const x = 1; throw new Error('esm boom');",
	}})
	var jobErrs []string
	ctx.SetJobErrorHandler(func(msg string) { jobErrs = append(jobErrs, msg) })

	got := evalString(t, ctx, `(function(){
		try { require("boom"); return "no-error"; }
		catch (e) { return e.message; }
	})()`)
	if got != "esm boom" {
		t.Fatalf("expected the module's error, got %q", got)
	}
	ctx.PumpJobs()
	// the rejection was surfaced synchronously; it must not be reported
	// again as an unhandled promise rejection
	if len(jobErrs) != 0 {
		t.Fatalf("duplicate rejection report: %v", jobErrs)
	}
}

func TestSharedInstanceAcrossRequireAndImport(t *testing.T) {
	for _, order := range []string{"require-first", "import-first"} {
		ctx := NewContext()
		host := &testModuleHost{files: map[string]string{
			"/mods/counted.js": "globalThis.evals = (globalThis.evals || 0) + 1; exports.n = globalThis.evals;",
		}}
		ctx.SetModuleHost(host)
		requireIt := `globalThis.viaRequire = require("counted").n;`
		importIt := `import { n } from "counted"; globalThis.viaImport = n;`
		if order == "require-first" {
			loadEntry(t, ctx, "/rules/r.js", requireIt)
			loadEntry(t, ctx, "/rules/i.js", importIt)
		} else {
			loadEntry(t, ctx, "/rules/i.js", importIt)
			loadEntry(t, ctx, "/rules/r.js", requireIt)
		}
		if got := evalString(t, ctx, "evals + ':' + viaRequire + ':' + viaImport"); got != "1:1:1" {
			t.Fatalf("%s: module evaluated more than once per realm: %q", order, got)
		}
		ctx.DestroyHeap()
	}
}

func TestSeparateInstancePerRealm(t *testing.T) {
	ctx := NewContext()
	defer ctx.DestroyHeap()
	ctx.SetModuleHost(&testModuleHost{files: map[string]string{
		"/mods/mod.js": "export const tag = {};",
	}})

	loadEntry(t, ctx, "/rules/a.js", `import { tag } from "mod"; globalThis.tagA = tag;`)

	ctx.PushThreadNewGlobalenv()
	realm2 := ctx.GetContext(-1)
	if realm2 == nil {
		t.Fatal("no second realm")
	}
	loadEntry(t, realm2, "/rules/b.js", `import { tag } from "mod"; globalThis.tagB = tag;`)
	if got := evalString(t, realm2, "typeof tagB"); got != "object" {
		t.Fatalf("second realm import failed: %q", got)
	}
	// distinct realms, distinct instances: tagA lives in realm 1 only
	if got := evalString(t, realm2, "typeof globalThis.tagA"); got != "undefined" {
		t.Fatalf("realms leak globals: %q", got)
	}
	ctx.Pop() // realm handle
}

func TestRelativeImports(t *testing.T) {
	ctx := NewContext()
	defer ctx.DestroyHeap()
	ctx.SetModuleHost(&testModuleHost{files: map[string]string{
		"/mods/pkg/main.js": `import { v } from "./lib.js"; export const out = v + 1;`,
		"/mods/pkg/lib.js":  `import { base } from "../base.js"; export const v = base + 1;`,
		"/mods/base.js":     `export const base = 40;`,
	}})

	loadEntry(t, ctx, "/rules/a.js", `import { out } from "pkg/main"; globalThis.result = out;`)
	if got := evalString(t, ctx, "result"); got != "42" {
		t.Fatalf("bad relative import chain: %q", got)
	}
}

func TestESModuleCycle(t *testing.T) {
	ctx := NewContext()
	defer ctx.DestroyHeap()
	ctx.SetModuleHost(&testModuleHost{files: map[string]string{
		"/mods/a.js": `import { fromB } from "b"; export const fromA = 1; export function useB() { return fromB; }`,
		"/mods/b.js": `import { fromA } from "a"; export const fromB = 2; export function useA() { return fromA; }`,
	}})

	loadEntry(t, ctx, "/rules/r.js", `
import { useB } from "a";
import { useA } from "b";
globalThis.result = useB() + ':' + useA();
`)
	if got := evalString(t, ctx, "result"); got != "2:1" {
		t.Fatalf("bad cycle result: %q", got)
	}
}

func TestDynamicImportFromScript(t *testing.T) {
	ctx := NewContext()
	defer ctx.DestroyHeap()
	ctx.SetModuleHost(&testModuleHost{files: map[string]string{
		"/mods/esmmod.js": "export const x = 7;",
	}})

	// a classic (non-module) rule file using import(): the promise resolves
	// on the job pump
	loadEntry(t, ctx, "/rules/classic.js", `
(async () => { globalThis.dyn = (await import("esmmod")).x; })();
`)
	if got := evalString(t, ctx, "dyn"); got != "7" {
		t.Fatalf("dynamic import failed: %q", got)
	}
}

func TestMissingStaticImportIsCompileError(t *testing.T) {
	ctx := NewContext()
	defer ctx.DestroyHeap()
	ctx.SetModuleHost(&testModuleHost{files: map[string]string{}})

	isModule, rc := ctx.CompileScriptOrModule(
		`import { x } from "nosuch"; log(x);`, "/rules/a.js",
		"(async function(module, exports){", "\n}")
	if rc == 0 {
		t.Fatalf("expected a compile error (isModule=%v)", isModule)
	}
	msg := ctx.SafeToString(-1)
	ctx.Pop()
	if !strings.Contains(msg, `cannot find module "nosuch"`) {
		t.Fatalf("unhelpful missing-module error: %q", msg)
	}
}

func TestScriptWithESMWordsStaysScript(t *testing.T) {
	ctx := NewContext()
	defer ctx.DestroyHeap()
	ctx.SetModuleHost(&testModuleHost{files: map[string]string{}})

	// mentions import/export at line starts only inside strings/comments
	loadEntry(t, ctx, "/rules/plain.js", `
// import me maybe
var s = "\nimport x from 'y'\n";
globalThis.plainRan = 1;
`)
	if got := evalString(t, ctx, "plainRan"); got != "1" {
		t.Fatalf("plain script did not run: %q", got)
	}
}

func TestESModuleTopLevelAwaitEntry(t *testing.T) {
	ctx := NewContext()
	defer ctx.DestroyHeap()
	ctx.SetModuleHost(&testModuleHost{files: map[string]string{
		"/mods/esmmod.js": "export const x = 1;",
	}})

	isModule, rc := ctx.CompileScriptOrModule(`
import { x } from "esmmod";
export {};
const v = await Promise.resolve(x + 41);
globalThis.tlaResult = v;
`, "/rules/tla.js", "(async function(module, exports){", "\n})")
	if rc != 0 {
		t.Fatalf("compile failed: %s", ctx.SafeToString(-1))
	}
	if !isModule {
		t.Fatal("expected a module")
	}
	if rc := ctx.EvalModuleNoPump(); rc != 0 {
		t.Fatalf("eval failed: %s", ctx.SafeToString(-1))
	}
	if st := ctx.PromiseStateTop(); st != PromisePending {
		t.Fatalf("expected a pending TLA promise, got state %d", st)
	}
	ctx.Pop()
	ctx.PumpJobs()
	if got := evalString(t, ctx, "tlaResult"); got != "42" {
		t.Fatalf("TLA module did not finish: %q", got)
	}
}

func TestRequireCJSThenImportSharesInstance(t *testing.T) {
	ctx := NewContext()
	defer ctx.DestroyHeap()
	ctx.SetModuleHost(&testModuleHost{files: map[string]string{
		"/mods/counted.js": "globalThis.cjsEvals = (globalThis.cjsEvals || 0) + 1; exports.n = globalThis.cjsEvals;",
	}})

	loadEntry(t, ctx, "/rules/r.js", `globalThis.viaRequire = require("counted").n;`)
	loadEntry(t, ctx, "/rules/i.js", `import def, { n } from "counted"; globalThis.viaImport = n + ':' + def.n;`)
	if got := evalString(t, ctx, "cjsEvals + ':' + viaRequire + ':' + viaImport"); got != "1:1:1:1" {
		t.Fatalf("CJS module evaluated more than once: %q", got)
	}
}
