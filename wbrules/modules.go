package wbrules

import (
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"

	duktape "github.com/wirenboard/go-duktape"
	"github.com/wirenboard/wbgong"
)

// The engine is the shim's ModuleHost: it owns the file system side of
// require() and import - where module names come from, how their source is
// produced (a .ts module is transpiled like a .ts rule file) and what the
// per-module metadata objects carry (module.filename/module.static for
// CommonJS, import.meta.url/filename/dirname/static for ES modules).
//
// Resolution:
//   - a relative specifier ("./x", "../x") in an import resolves against the
//     importing file's directory on the real file system - Node.js semantics,
//     so a rule file can import a sibling or a file in a subdirectory;
//   - an absolute specifier ("/etc/wb-rules-modules/x.js") is taken as is;
//   - a bare specifier ("x", "dir/x") is looked up in the module directories
//     (WB_RULES_MODULES), in order - the classic require() lookup.
//
// require() keeps its Duktape-era id semantics: "./x" is resolved against the
// requiring MODULE ID (so from a rule file, "./x" is just "x" in the module
// directories), never against the file system. See resolveModuleID.
//
// A candidate is tried as given (if it names a .js/.ts/.mjs/.mts/.cjs/.cts
// file), a missing ".js" falls back to the ".ts" of the same name (the
// TypeScript convention of importing the emitted name; .mjs -> .mts,
// .cjs -> .cts likewise), and a name without a module extension gets them
// appended in turn.

// File kinds by extension, Node.js-style: .js/.ts decide their format by
// their syntax (an import/export declaration makes an ES module), .mjs/.mts
// are always ES modules and .cjs/.cts always classic CommonJS-style
// scripts. .ts/.mts/.cts are TypeScript, transpiled on load.
var (
	jsExts          = []string{".js", ".mjs", ".cjs"}
	tsExts          = []string{".ts", ".mts", ".cts"}
	declExts        = []string{".d.ts", ".d.mts", ".d.cts"}
	moduleProbeExts = append(append([]string{}, jsExts...), tsExts...)
)

func hasAnySuffix(p string, exts []string) bool {
	for _, ext := range exts {
		if strings.HasSuffix(p, ext) {
			return true
		}
	}
	return false
}

// isTypeScriptFile: a .ts/.mts/.cts source (declaration files excluded).
func isTypeScriptFile(p string) bool { return hasAnySuffix(p, tsExts) && !isDeclarationFile(p) }

// isDeclarationFile: .d.ts and friends - no executable code.
func isDeclarationFile(p string) bool { return hasAnySuffix(p, declExts) }

// isJavaScriptFile: a .js/.mjs/.cjs source.
func isJavaScriptFile(p string) bool { return hasAnySuffix(p, jsExts) }

// isModuleFile reports whether a path names a file the engine can load as a
// module (JavaScript or TypeScript) - a `import "./config.json"` must fail
// as unsupported rather than run through the CommonJS wrapper.
func isModuleFile(p string) bool { return hasAnySuffix(p, moduleProbeExts) }

// tsSourceFor maps an emitted JavaScript name to its TypeScript source
// name (the TypeScript convention of importing the output name), "" if the
// name is not a JavaScript one.
func tsSourceFor(p string) string {
	for i, ext := range jsExts {
		if strings.HasSuffix(p, ext) {
			return strings.TrimSuffix(p, ext) + tsExts[i]
		}
	}
	return ""
}

// probeModuleFile returns the first existing regular module file for path p
// among the candidate spellings, or "": the path as written (if it names a
// .js/.ts file), the TypeScript source behind a ".js" name, then ".js" and
// ".ts" appended.
func probeModuleFile(p string) string {
	var cands []string
	if isModuleFile(p) {
		cands = append(cands, p)
		if ts := tsSourceFor(p); ts != "" {
			cands = append(cands, ts)
		}
	} else {
		for _, ext := range moduleProbeExts {
			cands = append(cands, p+ext)
		}
	}
	for _, c := range cands {
		if fi, err := os.Stat(c); err == nil && fi.Mode().IsRegular() {
			if abs, err := filepath.Abs(c); err == nil {
				return abs
			}
			return c
		}
	}
	return ""
}

// ResolveModule implements duktape.ModuleHost (the runtime's resolution: a
// miss is also logged, a missing module being a deployment problem worth
// noticing in the service log).
func (engine *ESEngine) ResolveModule(base, spec string) (string, error) {
	name, err := engine.resolveModule(base, spec)
	if err != nil {
		wbgong.Error.Printf("[modules] %s", err)
	}
	return name, err
}

func (engine *ESEngine) resolveModule(base, spec string) (string, error) {
	wbgong.Debug.Printf("[modules] resolve %q from %q", spec, base)
	notFound := func() error {
		if base != "" {
			return fmt.Errorf("cannot find module %q (imported from %s)", spec, base)
		}
		return fmt.Errorf("cannot find module %q", spec)
	}
	switch {
	case spec == "":
		return "", notFound()
	case strings.HasPrefix(spec, "./") || strings.HasPrefix(spec, "../"):
		if base == "" {
			return "", fmt.Errorf("cannot find module %q: a relative specifier needs an importing file", spec)
		}
		if r := probeModuleFile(filepath.Join(filepath.Dir(base), spec)); r != "" {
			return r, nil
		}
	case strings.HasPrefix(spec, "/"):
		if r := probeModuleFile(filepath.Clean(spec)); r != "" {
			return r, nil
		}
	default:
		for _, dir := range engine.modulesDirs {
			if dir == "" {
				continue
			}
			if r := probeModuleFile(filepath.Join(dir, spec)); r != "" {
				return r, nil
			}
		}
	}
	return "", notFound()
}

// LoadModuleSource implements duktape.ModuleHost: the module's JavaScript,
// a .ts file transpiled through the same compiler as a .ts rule file (so its
// tracebacks map back to source lines too).
func (engine *ESEngine) LoadModuleSource(name string) (string, error) {
	src, err := os.ReadFile(name)
	if err != nil {
		return "", fmt.Errorf("cannot read module %s: %w", name, err)
	}
	if !isTypeScriptFile(name) {
		return string(src), nil
	}
	if engine.tsc == nil {
		return "", fmt.Errorf(`cannot load module %s: TypeScript support is disabled (-tsgo="")`, name)
	}
	if !engine.tsc.Available() {
		return "", fmt.Errorf(
			"cannot load module %s: TypeScript compiler not found at %s - wb-rules depends on the wb-tsgo package (broken installation?)",
			name, engine.tsc.binPath)
	}
	js, err := engine.tsc.Transpile(string(src), name)
	if err != nil {
		return "", fmt.Errorf("cannot load module %s: %w", name, err)
	}
	return js, nil
}

// pushModuleStatic pushes the storage object shared by every instance of the
// module file `name` (heap stash _esModules[name]) - module.static for
// CommonJS, import.meta.static for ES modules - creating it on first use.
func pushModuleStatic(d *duktape.Context, name string) {
	d.PushHeapStash()
	// [ stash ]
	d.GetPropString(-1, MODULES_USER_STORAGE_OBJ_NAME)
	// [ stash _esModules ]
	if !d.HasPropString(-1, name) {
		d.PushObject()
		d.PutPropString(-2, name)
	}
	d.GetPropString(-1, name)
	// [ stash _esModules storage ]
	d.Insert(-3)
	// [ storage stash _esModules ]
	d.Pop2()
	// [ storage ]
}

// InitCjsModule implements duktape.ModuleHost: [ ... module ] -> [ ... module ]
func (engine *ESEngine) InitCjsModule(d *duktape.Context, name string) {
	d.PushString(name)
	d.PutPropString(-2, MODULE_FILENAME_PROP)
	pushModuleStatic(d, name)
	d.PutPropString(-2, MODULE_STATIC_PROP)
}

// InitImportMeta implements duktape.ModuleHost: [ ... meta ] -> [ ... meta ]
func (engine *ESEngine) InitImportMeta(d *duktape.Context, name string) {
	d.PushString((&url.URL{Scheme: "file", Path: name}).String())
	d.PutPropString(-2, "url")
	d.PushString(name)
	d.PutPropString(-2, MODULE_FILENAME_PROP)
	d.PushString(filepath.Dir(name))
	d.PutPropString(-2, "dirname")
	pushModuleStatic(d, name)
	d.PutPropString(-2, MODULE_STATIC_PROP)
}

// ResolveModuleForEditor implements ModuleResolver (Editor.ResolveModule).
// Files are served as their source (a .ts module is what the editor's
// language service wants, not its transpile), and only from the rules
// workspace: the rule directory and the module directories. The runtime
// resolves an absolute specifier anywhere (a rule can read any file through
// spawn() anyway), but the editor must not become a general file reader.
// Misses are not logged: an editor completing a specifier probes freely.
func (engine *ESEngine) ResolveModuleForEditor(fromPhysical, spec string) (string, string, error) {
	name, err := engine.resolveModule(fromPhysical, spec)
	if err != nil {
		return "", "", err
	}
	if !engine.inRulesWorkspace(name) {
		return "", "", fmt.Errorf("cannot find module %q: %s is outside the rule and module directories", spec, name)
	}
	src, err := os.ReadFile(name)
	if err != nil {
		return "", "", fmt.Errorf("cannot read module %s: %w", name, err)
	}
	return name, string(src), nil
}

// inRulesWorkspace reports whether an absolute path lies under the rules
// source root or one of the module directories.
func (engine *ESEngine) inRulesWorkspace(name string) bool {
	roots := append([]string{}, engine.modulesDirs...)
	if engine.sourceRoot != "" {
		roots = append(roots, engine.sourceRoot)
	}
	for _, root := range roots {
		if root == "" {
			continue
		}
		if abs, err := filepath.Abs(root); err == nil {
			root = abs
		}
		if rel, err := filepath.Rel(root, name); err == nil && rel != ".." && !strings.HasPrefix(rel, "../") {
			return true
		}
	}
	return false
}
