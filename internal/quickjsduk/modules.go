package duktape

/*
#include <stdlib.h>
#include "shim.h"
*/
import "C"

import (
	"regexp"
	"strings"
	"unsafe"
)

// ---------------------------------------------------------------------------
// Modules: CommonJS require() (Duktape 1.x id semantics with wb-rules
// extensions) and ES modules (static import/export, import(), import.meta),
// interoperable in both directions.
//
// Ownership of the file system belongs to the embedder: the shim only knows
// module NAMES (for files, their absolute path) and asks the ModuleHost for
// resolution, sources and the per-module metadata objects.
//
// Instances are per realm (per rule file), like Duktape's per-global-env
// require cache: an ES module imported by two rule files is evaluated once
// in each. Within one realm a file is one instance however it is reached -
// `require("x")`, `import "x"` and `import "/abs/path/x.js"` all share it.

// ModuleHost serves module loading for one runtime. All calls arrive on the
// goroutine executing JavaScript, from inside a compile, a require() or a
// promise job.
type ModuleHost interface {
	// ResolveModule turns specifier `spec`, as written in module `base` (the
	// importing module's name; "" for a bare require() id), into the
	// canonical module name - an absolute file path. The error's message is
	// what the script sees (e.g. `cannot find module "x"`).
	ResolveModule(base, spec string) (string, error)
	// LoadModuleSource returns the JavaScript source of a resolved module (a
	// TypeScript file arrives transpiled).
	LoadModuleSource(name string) (string, error)
	// InitCjsModule decorates the CommonJS `module` object at the stack top
	// for module `name` (filename, static storage): [ ... module ] -> [ ... module ]
	InitCjsModule(d *Context, name string)
	// InitImportMeta decorates an ES module's import.meta at the stack top
	// for module `name`: [ ... meta ] -> [ ... meta ]
	InitImportMeta(d *Context, name string)
}

// SetModuleHost installs the runtime-wide module host (all realms of the
// heap share it). Without a host, require() and import fail with an error.
func (d *Context) SetModuleHost(h ModuleHost) {
	s := d.st()
	regMu.Lock()
	s.rts.moduleHost = h
	regMu.Unlock()
}

// esmSyntaxRx spots statement-level module syntax: an import declaration
// (not the import() call) or an export declaration at the start of a line or
// after a `;`/`}`, and import.meta anywhere. Deliberately loose - a comment
// or template line can match - because a match only decides to TRY the
// module parse after the classic script wrapper failed to compile.
var esmSyntaxRx = regexp.MustCompile(`(?m)(?:(?:^|[;}])\s*(?:import\b\s*[\w$*{"']|export\b\s*[\w$*{])|\bimport\s*\.\s*meta\b)`)

// LooksLikeESModule reports whether src carries ES module syntax at statement
// level (import/export declarations or import.meta). Cheap and approximate;
// see CompileScriptOrModule for how it is used.
func LooksLikeESModule(src string) bool {
	if !strings.Contains(src, "import") && !strings.Contains(src, "export") {
		return false
	}
	return esmSyntaxRx.MatchString(src)
}

func fileKey(name string) string { return "file:" + name }

// resolveModuleID resolves "./x" / "../x" against the requiring module's id
// the way Duktape's CommonJS support does (against the ID, not the file
// system: a top-level "./x" is just "x").
func resolveModuleID(baseID, id string) string {
	if !strings.HasPrefix(id, "./") && !strings.HasPrefix(id, "../") {
		return id
	}
	base := ""
	if i := strings.LastIndex(baseID, "/"); i >= 0 {
		base = baseID[:i]
	}
	parts := []string{}
	if base != "" {
		parts = strings.Split(base, "/")
	}
	for _, seg := range strings.Split(id, "/") {
		switch seg {
		case ".", "":
		case "..":
			if len(parts) > 0 {
				parts = parts[:len(parts)-1]
			}
		default:
			parts = append(parts, seg)
		}
	}
	return strings.Join(parts, "/")
}

// throwErrorWithCode throws a plain Error carrying a Node.js-style `code`.
func throwErrorWithCode(ctx *C.JSContext, msg, code string) C.JSValue {
	e := C.JS_NewError(ctx)
	if C.qjd_tag(e) == C.JS_TAG_EXCEPTION {
		return e
	}
	cm := C.CString(msg)
	setPropStr(ctx, e, "message", C.JS_NewStringLen(ctx, cm, C.size_t(len(msg))))
	C.free(unsafe.Pointer(cm))
	if code != "" {
		cc := C.CString(code)
		setPropStr(ctx, e, "code", C.JS_NewStringLen(ctx, cc, C.size_t(len(code))))
		C.free(unsafe.Pointer(cc))
	}
	return C.JS_Throw(ctx, e)
}

func newJSString(ctx *C.JSContext, s string) C.JSValue {
	cs := C.CString(s)
	defer C.free(unsafe.Pointer(cs))
	return C.JS_NewStringLen(ctx, cs, C.size_t(len(s)))
}

// host returns the module host installed on this runtime (nil if none).
func (rts *runtimeState) host() ModuleHost {
	regMu.Lock()
	defer regMu.Unlock()
	return rts.moduleHost
}

// moduleFormatByName: an explicit format from the file name, Node.js-style:
// .mjs/.mts are always ES modules, .cjs/.cts always classic scripts; other
// names (.js/.ts) are decided by their syntax.
func moduleFormatByName(name string) (isModule, explicit bool) {
	switch {
	case strings.HasSuffix(name, ".mjs"), strings.HasSuffix(name, ".mts"):
		return true, true
	case strings.HasSuffix(name, ".cjs"), strings.HasSuffix(name, ".cts"):
		return false, true
	}
	return false, false
}

// compileSource compiles src for module `name` either as the classic script
// wrapper `prefix + src + suffix` (a function expression) or, when that fails
// and the source carries module syntax, as an ES module. A .mjs/.mts name
// skips the wrapper and compiles as a module outright (so a file without
// any import/export still gets module semantics: strict mode, import.meta);
// a .cjs/.cts name never tries the module parse. Returns the compiled
// function or module value (owned by the caller), whether it is a module, and
// whether an exception is pending instead. A module already compiled in this
// realm under `name` is returned again instead of being recompiled (QuickJS
// caches module definitions per realm by name; a duplicate would be a
// second, separately evaluated instance).
//
// legacyErrors: report a non-module file's syntax error from a parse of the
// wrapper in PROGRAM form (the parenthesised expression form, which produces
// the function value, phrases some errors differently - an unbalanced brace
// trips on the closing paren). wb-rules always validated rule files that
// way, so their error messages and lines stay exactly as they were; the
// extra parse happens only for a file that failed to compile.
func (rts *runtimeState) compileSource(ctx *C.JSContext, src, name, prefix, suffix string, legacyErrors bool) (val C.JSValue, isModule bool, exc bool) {
	regMu.Lock()
	cached, ok := rts.esModules[moduleKey{ctx, name}]
	regMu.Unlock()
	if ok {
		return C.JS_DupValue(ctx, cached), true, false
	}

	cname := C.CString(name)
	defer C.free(unsafe.Pointer(cname))

	forceModule, explicit := moduleFormatByName(name)
	var fn C.JSValue
	if !forceModule {
		wrapped := prefix + src + suffix
		cw := C.CString(wrapped)
		anchor := rts.anchor()
		rts.pushActive(ctx)
		fn = C.qjd_eval(ctx, cw, C.size_t(len(wrapped)), cname, evalGlobal, anchor)
		rts.popActive()
		C.free(unsafe.Pointer(cw))
		if C.qjd_tag(fn) != C.JS_TAG_EXCEPTION {
			return fn, false, false
		}
	}
	if !forceModule && (explicit || !LooksLikeESModule(src)) {
		if legacyErrors && strings.HasPrefix(prefix, "(") && strings.HasSuffix(suffix, ")") {
			program := prefix[1:] + src + suffix[:len(suffix)-1]
			cp := C.CString(program)
			rts.pushActive(ctx)
			pv := C.qjd_eval(ctx, cp, C.size_t(len(program)), cname, evalGlobal|C.JS_EVAL_FLAG_COMPILE_ONLY, 0)
			rts.popActive()
			C.free(unsafe.Pointer(cp))
			if C.qjd_tag(pv) == C.JS_TAG_EXCEPTION {
				// the program form's error is now the pending exception (the
				// throw replaced the expression form's)
				return pv, false, true
			}
			C.JS_FreeValue(ctx, pv)
		}
		return fn, false, true // the wrapper's syntax error stands
	}
	// module syntax is present (or the name says module): the wrapper
	// cannot hold it, the module parser can - its verdict (and its error)
	// is the relevant one
	if !forceModule {
		C.JS_FreeValue(ctx, C.JS_GetException(ctx))
	}

	csrc := C.CString(src)
	anchor := rts.anchor()
	rts.pushActive(ctx)
	mod := C.qjd_compile_module(ctx, csrc, C.size_t(len(src)), cname, anchor)
	rts.popActive()
	C.free(unsafe.Pointer(csrc))
	if C.qjd_tag(mod) == C.JS_TAG_EXCEPTION {
		return mod, true, true
	}
	rts.registerESModule(ctx, name, mod)
	return mod, true, false
}

// registerESModule remembers a freshly compiled module (so a later require()
// of the same file finds the instance QuickJS already holds) and lets the
// host decorate its import.meta.
func (rts *runtimeState) registerESModule(ctx *C.JSContext, name string, mod C.JSValue) {
	regMu.Lock()
	rts.esModules[moduleKey{ctx, name}] = C.JS_DupValue(ctx, mod)
	regMu.Unlock()
	h := rts.host()
	if h == nil {
		return
	}
	cs := stateFor(ctx)
	if cs == nil {
		return
	}
	meta := C.qjd_import_meta(ctx, mod)
	if C.qjd_tag(meta) == C.JS_TAG_EXCEPTION {
		C.JS_FreeValue(ctx, C.JS_GetException(ctx))
		return
	}
	cs.push(meta)
	h.InitImportMeta(&Context{unsafe.Pointer(ctx)}, name)
	C.JS_FreeValue(ctx, cs.popTransfer())
}

// evaluateModuleSync links and evaluates a compiled module and returns its
// namespace, for require() of an ES module: the module must settle
// synchronously. A module that awaits at the top level leaves a pending
// promise and cannot be required (Node.js: ERR_REQUIRE_ASYNC_MODULE); a
// module that throws rethrows here, synchronously, and the rejection record
// the tracker took is withdrawn so the error is not reported twice.
func (rts *runtimeState) evaluateModuleSync(ctx *C.JSContext, mod C.JSValue, name string) (ns C.JSValue, exc bool) {
	anchor := rts.anchor()
	rts.pushActive(ctx)
	p := C.qjd_eval_module(ctx, mod, anchor)
	rts.popActive()
	if C.qjd_tag(p) == C.JS_TAG_EXCEPTION {
		return p, true
	}
	switch C.qjd_promise_state(ctx, p) {
	case C.int(PromiseFulfilled):
		C.JS_FreeValue(ctx, p)
		ns = C.qjd_module_namespace(ctx, mod)
		return ns, C.qjd_tag(ns) == C.JS_TAG_EXCEPTION
	case C.int(PromiseRejected):
		reason := C.qjd_promise_result(ctx, p)
		ptr, text := rejectionReason(ctx, p)
		rts.retractRejection(uintptr(C.qjd_value_ptr(p)), ptr, text)
		C.JS_FreeValue(ctx, p)
		return C.JS_Throw(ctx, reason), true
	default:
		C.JS_FreeValue(ctx, p)
		return throwErrorWithCode(ctx,
			"require() of ES module "+name+" is not possible: it uses top-level await (import it with import() instead)",
			"ERR_REQUIRE_ASYNC_MODULE"), true
	}
}

// newCjsModuleObject builds the `module` and `exports` objects for a CommonJS
// module, decorated by the host. Both are owned by the caller.
func (rts *runtimeState) newCjsModuleObject(ctx *C.JSContext, id, name string) (module, exports C.JSValue) {
	module = C.JS_NewObject(ctx)
	exports = C.JS_NewObject(ctx)
	setPropStr(ctx, module, "exports", C.JS_DupValue(ctx, exports))
	setPropStr(ctx, module, "id", newJSString(ctx, id))
	if h := rts.host(); h != nil {
		if cs := stateFor(ctx); cs != nil {
			cs.push(C.JS_DupValue(ctx, module))
			h.InitCjsModule(&Context{unsafe.Pointer(ctx)}, name)
			C.JS_FreeValue(ctx, cs.popTransfer())
		}
	}
	return module, exports
}

const (
	cjsWrapPrefix = "(function(require,exports,module){"
	cjsWrapSuffix = "\n})"
)

// loadCompiled runs a compiled module (CommonJS wrapper function or ES
// module) as id `id` for file `name`, registers it in the realm's require
// cache under the id and the file key, and returns the module object (owned
// by the caller) whose .exports is the result: the CommonJS exports, or the
// ES module namespace. `compiled` is consumed.
func (rts *runtimeState) loadCompiled(ctx *C.JSContext, id, name string, compiled C.JSValue, isModule bool) (module C.JSValue, exc bool) {
	module, exports := rts.newCjsModuleObject(ctx, id, name)

	// Pre-register for require-cycles: a module requiring its requirer sees
	// the partially-built exports instead of recursing forever.
	keys := []moduleKey{{ctx, id}, {ctx, fileKey(name)}}
	regMu.Lock()
	for _, k := range keys {
		if old, ok := rts.modules[k]; ok {
			rts.deadVals = append(rts.deadVals, old)
		}
		rts.modules[k] = C.JS_DupValue(ctx, module)
	}
	regMu.Unlock()
	fail := func(v C.JSValue) (C.JSValue, bool) {
		regMu.Lock()
		var dead []C.JSValue
		for _, k := range keys {
			if m, ok := rts.modules[k]; ok {
				dead = append(dead, m)
				delete(rts.modules, k)
			}
		}
		regMu.Unlock()
		for _, m := range dead {
			C.JS_FreeValue(ctx, m)
		}
		C.JS_FreeValue(ctx, module)
		C.JS_FreeValue(ctx, exports)
		return v, true
	}

	if isModule {
		ns, exc := rts.evaluateModuleSync(ctx, compiled, name)
		C.JS_FreeValue(ctx, compiled)
		if exc {
			return fail(ns)
		}
		setPropStr(ctx, module, "exports", ns)
		C.JS_FreeValue(ctx, exports)
		return module, false
	}

	cid := C.CString(id)
	boundRequire := C.qjd_new_require(ctx, cid)
	C.free(unsafe.Pointer(cid))
	callArgs := []C.JSValue{boundRequire, exports, module}
	rts.pushActive(ctx)
	res := C.qjd_call(ctx, compiled, exports, 3, &callArgs[0], 0) // nested: a compile or require() is in progress
	rts.popActive()
	C.JS_FreeValue(ctx, compiled)
	C.JS_FreeValue(ctx, boundRequire)
	if C.qjd_tag(res) == C.JS_TAG_EXCEPTION {
		return fail(res)
	}
	C.JS_FreeValue(ctx, res)
	C.JS_FreeValue(ctx, exports)
	return module, false
}

// cachedModule returns a dup'd cached module object for a key, if any.
func (rts *runtimeState) cachedModule(ctx *C.JSContext, key string) (C.JSValue, bool) {
	regMu.Lock()
	defer regMu.Unlock()
	if m, ok := rts.modules[moduleKey{ctx, key}]; ok {
		return C.JS_DupValue(ctx, m), true
	}
	return C.qjd_undefined(), false
}

//export goRequire
func goRequire(ctx *C.JSContext, thisVal C.JSValue, argc C.int, argv *C.JSValue, baseVal C.JSValue) C.JSValue {
	cs := stateFor(ctx)
	if cs == nil || argc < 1 {
		return throwTypeErr(ctx, "require: bad context or missing id")
	}
	rts := cs.rts
	args := unsafe.Slice(argv, int(argc))
	var n C.size_t
	cid := C.JS_ToCStringLen(ctx, &n, args[0])
	if cid == nil {
		return C.qjd_exception()
	}
	id := C.GoStringN(cid, C.int(n))
	C.JS_FreeCString(ctx, cid)

	var bn C.size_t
	if cb := C.JS_ToCStringLen(ctx, &bn, baseVal); cb != nil {
		id = resolveModuleID(C.GoStringN(cb, C.int(bn)), id)
		C.JS_FreeCString(ctx, cb)
	}

	if mod, ok := rts.cachedModule(ctx, id); ok {
		out := C.qjd_get_module_exports(ctx, mod)
		C.JS_FreeValue(ctx, mod)
		return out
	}

	h := rts.host()
	if h == nil {
		return throwTypeErr(ctx, "require: no module host")
	}
	var name string
	var err error
	rts.excludeFromWatchdog(func() { name, err = h.ResolveModule("", id) })
	if err != nil {
		return throwErrorWithCode(ctx, err.Error(), "MODULE_NOT_FOUND")
	}
	// the same file already loaded under another id (or imported): alias
	if mod, ok := rts.cachedModule(ctx, fileKey(name)); ok {
		regMu.Lock()
		if old, had := rts.modules[moduleKey{ctx, id}]; had {
			rts.deadVals = append(rts.deadVals, old)
		}
		rts.modules[moduleKey{ctx, id}] = C.JS_DupValue(ctx, mod)
		regMu.Unlock()
		out := C.qjd_get_module_exports(ctx, mod)
		C.JS_FreeValue(ctx, mod)
		return out
	}
	// an ES module this realm has compiled (imported) but not required yet:
	// no need to read and transpile its source again
	var compiled C.JSValue
	var isModule, exc bool
	regMu.Lock()
	cached, known := rts.esModules[moduleKey{ctx, name}]
	regMu.Unlock()
	if known {
		compiled, isModule = C.JS_DupValue(ctx, cached), true
	} else {
		var src string
		rts.excludeFromWatchdog(func() { src, err = h.LoadModuleSource(name) })
		if err != nil {
			return throwErrorWithCode(ctx, err.Error(), "")
		}
		compiled, isModule, exc = rts.compileSource(ctx, src, name, cjsWrapPrefix, cjsWrapSuffix, false)
		if exc {
			return compiled
		}
	}
	mod, exc := rts.loadCompiled(ctx, id, name, compiled, isModule)
	if exc {
		return mod
	}
	out := C.qjd_get_module_exports(ctx, mod)
	C.JS_FreeValue(ctx, mod)
	return out
}

//export goModuleNormalize
func goModuleNormalize(ctx *C.JSContext, cbase *C.char, cname *C.char) *C.char {
	cs := stateFor(ctx)
	if cs == nil {
		throwTypeErr(ctx, "import: stale context")
		return nil
	}
	h := cs.rts.host()
	if h == nil {
		throwTypeErr(ctx, "import: no module host")
		return nil
	}
	var resolved string
	var err error
	cs.rts.excludeFromWatchdog(func() { resolved, err = h.ResolveModule(C.GoString(cbase), C.GoString(cname)) })
	if err != nil {
		throwErrorWithCode(ctx, err.Error(), "ERR_MODULE_NOT_FOUND")
		return nil
	}
	cr := C.CString(resolved)
	defer C.free(unsafe.Pointer(cr))
	return C.qjd_js_strdup(ctx, cr)
}

//export goModuleLoad
func goModuleLoad(ctx *C.JSContext, cname *C.char) *C.JSModuleDef {
	cs := stateFor(ctx)
	if cs == nil {
		throwTypeErr(ctx, "import: stale context")
		return nil
	}
	rts := cs.rts
	h := rts.host()
	if h == nil {
		throwTypeErr(ctx, "import: no module host")
		return nil
	}
	name := C.GoString(cname)

	// A CommonJS module this realm already required: bridge its exports.
	if mod, ok := rts.cachedModule(ctx, fileKey(name)); ok {
		exports := C.qjd_get_module_exports(ctx, mod)
		C.JS_FreeValue(ctx, mod)
		m := C.qjd_new_cjs_module(ctx, cname, exports)
		C.JS_FreeValue(ctx, exports)
		return m
	}

	var src string
	var err error
	rts.excludeFromWatchdog(func() { src, err = h.LoadModuleSource(name) })
	if err != nil {
		throwErrorWithCode(ctx, err.Error(), "")
		return nil
	}
	compiled, isModule, exc := rts.compileSource(ctx, src, name, cjsWrapPrefix, cjsWrapSuffix, false)
	if exc {
		return nil
	}
	if isModule {
		m := C.qjd_module_def(compiled)
		C.JS_FreeValue(ctx, compiled) // esModules and QuickJS's own list keep it alive
		return m
	}
	// CommonJS: run it now (as Node.js does when an ES module imports
	// CommonJS) and expose module.exports through a synthetic module
	mod, exc := rts.loadCompiled(ctx, name, name, compiled, false)
	if exc {
		return nil
	}
	exports := C.qjd_get_module_exports(ctx, mod)
	C.JS_FreeValue(ctx, mod)
	m := C.qjd_new_cjs_module(ctx, cname, exports)
	C.JS_FreeValue(ctx, exports)
	return m
}

// CompileScriptOrModule compiles src for file `filename` as the script
// wrapper `prefix + src + suffix` (a function expression: the classic rule
// file shape) or, when that fails and the source carries module syntax, as
// an ES module. Pushes the function value or the module value; on error the
// exception is pushed and the result is non-zero. A compiled module has its
// import.meta initialised by the host and is remembered for require().
// Like the other compile entry points, pending promise jobs are drained
// afterwards (a CommonJS dependency may have queued some while loading).
// A non-module file's syntax error is reported the way wb-rules always did
// (compileSource legacyErrors). A module is one per realm and name: a
// second call with the same filename returns the already compiled module,
// whatever source it is given - callers load each file into a fresh realm.
func (d *Context) CompileScriptOrModule(src, filename, prefix, suffix string) (isModule bool, rc int) {
	s := d.st()
	val, isModule, exc := s.rts.compileSource(s.ctx, src, filename, prefix, suffix, true)
	if exc {
		s.push(C.JS_GetException(s.ctx))
		s.rts.pumpJobs()
		return false, 1
	}
	s.push(val)
	s.rts.pumpJobs()
	return isModule, 0
}

// IsModule reports whether the value at idx is a compiled ES module.
func (d *Context) IsModule(idx int) bool {
	s := d.st()
	return C.qjd_is_module(s.at(idx)) != 0
}

// EvalModuleNoPump links and evaluates the compiled module at the stack top,
// replacing it with its evaluation promise: [ ... module ] -> [ ... promise ].
// The promise is already settled unless the module (or a dependency) awaits
// at the top level. Jobs are not pumped so the caller can inspect and, if it
// surfaces a rejection itself, retract it (RetractTopPromiseRejection)
// before calling PumpJobs. On failure the exception replaces the module and
// the result is non-zero.
func (d *Context) EvalModuleNoPump() int {
	s := d.st()
	mod := s.popTransfer()
	anchor := s.rts.anchor()
	s.rts.pushActive(s.ctx)
	p := C.qjd_eval_module(s.ctx, mod, anchor)
	s.rts.popActive()
	C.JS_FreeValue(s.ctx, mod)
	if C.qjd_tag(p) == C.JS_TAG_EXCEPTION {
		s.push(C.JS_GetException(s.ctx))
		return 1
	}
	s.push(p)
	return 0
}

// PushModuleNamespace pushes the namespace object of the compiled (and
// evaluated) module at idx.
func (d *Context) PushModuleNamespace(idx int) {
	s := d.st()
	s.push(C.qjd_module_namespace(s.ctx, s.at(idx)))
}
