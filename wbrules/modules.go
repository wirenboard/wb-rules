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
// A candidate is tried as given, then with ".js" and ".ts" appended, and a
// missing ".js" falls back to the ".ts" of the same name (the TypeScript
// convention of importing the emitted name).

var moduleProbeExts = []string{".js", ".ts"}

// probeModuleFile returns the first existing regular file for path p among
// the candidate spellings, or "".
func probeModuleFile(p string) string {
	cands := []string{p}
	for _, ext := range moduleProbeExts {
		cands = append(cands, p+ext)
	}
	if strings.HasSuffix(p, ".js") {
		cands = append(cands, strings.TrimSuffix(p, ".js")+".ts")
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

// ResolveModule implements duktape.ModuleHost.
func (engine *ESEngine) ResolveModule(base, spec string) (string, error) {
	wbgong.Debug.Printf("[modules] resolve %q from %q", spec, base)
	notFound := func() error {
		var err error
		if base != "" {
			err = fmt.Errorf("cannot find module %q (imported from %s)", spec, base)
		} else {
			err = fmt.Errorf("cannot find module %q", spec)
		}
		// the script gets the error thrown; the service log gets a line too
		// (a missing module is a deployment problem worth noticing)
		wbgong.Error.Printf("[modules] %s", err)
		return err
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
	if !strings.HasSuffix(name, ".ts") {
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
// Only JavaScript and TypeScript files are served, as their source (a .ts
// module is what the editor's language service wants, not its transpile).
func (engine *ESEngine) ResolveModuleForEditor(fromPhysical, spec string) (string, string, error) {
	name, err := engine.ResolveModule(fromPhysical, spec)
	if err != nil {
		return "", "", err
	}
	if !strings.HasSuffix(name, ".js") && !strings.HasSuffix(name, ".ts") {
		return "", "", fmt.Errorf("cannot find module %q: %s is not a JavaScript or TypeScript file", spec, name)
	}
	src, err := os.ReadFile(name)
	if err != nil {
		return "", "", fmt.Errorf("cannot read module %s: %w", name, err)
	}
	return name, string(src), nil
}
