package wbrules

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/wirenboard/wbgong/testutils"
)

// ES modules: a rule file with import/export runs as a real ES module; the
// module directories serve ES and CommonJS modules to import and require()
// alike; relative imports resolve next to the importing file.
type EsmSuite struct {
	RuleSuiteBase
}

// testRuleModulesDir is wbrules/test-modules resolved like testModulesDir
// (source-relative, not cwd-relative: a failing test elsewhere can strand
// the working directory).
func testRuleModulesDir() string {
	return filepath.Join(filepath.Dir(testModulesDir()), "wbrules", "test-modules")
}

func (s *EsmSuite) SetupTest() {
	s.ModulesPath = testRuleModulesDir()
	s.SetupSkippingDefs()
}

// loadEsm loads a fixture as a live file and consumes its load-time log
// lines (the module initialisation messages) in order, skipping the device
// definition traffic interleaved with them.
//
// Order: a CommonJS module imported by an ES module is executed while the
// importer is being linked (as in Node.js), so its side effects come before
// those of the ES modules, which run in dependency order when the importer
// is evaluated.
func (s *EsmSuite) loadEsm(name string, loadLog ...any) {
	s.Ck("LiveLoadScript", s.LiveLoadScript(name))
	for _, item := range loadLog {
		s.SkipTill(item)
	}
	s.SkipTill("[changed] " + name)
}

func (s *EsmSuite) TestEsmRuleFile() {
	// static imports are evaluated at load, depth first, once each; the
	// module's own import.meta is decorated by the engine
	s.loadEsm("testrules_esm.js",
		"[info] Module helloworld init",
		"[info] Module esm util init",
		"[info] Module esm helper init",
		"[info] esm meta: true true true",
	)
	s.publish("/devices/esm/controls/trigger/on", "1", "esm/trigger", "esm/out")
	s.VerifyUnordered(
		"tst -> /devices/esm/controls/trigger/on: [1] (QoS 1)",
		"driver -> /devices/esm/controls/trigger: [1] (QoS 1, retained)",
		// named + default imports of a CommonJS module, namespace import
		// of an ES module with a default export
		"[info] esm: hello world 2 42 15 true util-default",
		"driver -> /devices/esm/controls/out: [42] (QoS 1, retained)",
		"[info] esm counter: 1",
		"[info] esm where: true true true true",
	)
	s.publish("/devices/esm/controls/trigger/on", "0", "esm/trigger", "esm/out")
	s.VerifyUnordered(
		"tst -> /devices/esm/controls/trigger/on: [0] (QoS 1)",
		"driver -> /devices/esm/controls/trigger: [0] (QoS 1, retained)",
		"[info] esm: hello world 2 42 15 true util-default",
		"driver -> /devices/esm/controls/out: [42] (QoS 1, retained)",
		"[info] esm counter: 2",
		"[info] esm where: true true true true",
	)
}

func (s *EsmSuite) TestEsmInstancePerFileStaticShared() {
	s.loadEsm("testrules_esm.js",
		"[info] Module helloworld init",
		"[info] Module esm util init",
		"[info] Module esm helper init",
		"[info] esm meta: true true true",
	)
	// the second file gets its own instance of the module (it initialises
	// again in that realm)...
	s.loadEsm("testrules_esm_2.js",
		"[info] Module esm util init",
		"[info] Module esm helper init",
	)
	// ...but import.meta.static is one storage per module file
	s.publish("/devices/esm/controls/trigger/on", "1", "esm/trigger", "esm/out")
	s.SkipTill("[info] esm counter: 1")
	s.SkipTill("[info] esm where: true true true true")
	s.publish("/devices/esm2/controls/trigger/on", "1", "esm2/trigger")
	s.SkipTill("[info] esm2 counter: 2")
	s.publish("/devices/esm/controls/trigger/on", "0", "esm/trigger", "esm/out")
	s.SkipTill("[info] esm counter: 3")
	s.SkipTill("[info] esm where: true true true true")
}

func (s *EsmSuite) TestEsmReload() {
	s.loadEsm("testrules_esm.js",
		"[info] Module helloworld init",
		"[info] Module esm util init",
		"[info] Module esm helper init",
		"[info] esm meta: true true true",
	)
	// a reload builds a fresh realm: the modules initialise again there and
	// the previous incarnation's rule is gone
	s.Ck("OverwriteScript", s.OverwriteScript("testrules_esm.js", "testrules_esm_v2.js"))
	s.SkipTill("[info] Module esm util init")
	s.SkipTill("[info] Module esm helper init")
	s.SkipTill("[changed] testrules_esm.js")
	s.publish("/devices/esm/controls/trigger/on", "1", "esm/trigger")
	s.Verify(
		"tst -> /devices/esm/controls/trigger/on: [1] (QoS 1)",
		"driver -> /devices/esm/controls/trigger: [1] (QoS 1, retained)",
		"[info] esm v2: hello again 2",
	)
}

func (s *EsmSuite) TestEsmTopLevelAwait() {
	// the importer waits for a dependency's top-level await, then awaits
	// itself; rules defined after the await are attributed to the file
	s.loadEsm("testrules_esm_tla.js",
		"[info] Module esm tla init",
		"[info] esmtla loaded: 8",
	)
	s.publish("/devices/esmtla/controls/trigger/on", "1", "esmtla/trigger", "esmtla/out")
	s.SkipTill("driver -> /devices/esmtla/controls/out: [8] (QoS 1, retained)")
}

func (s *EsmSuite) TestEsmMissingImportIsLoadError() {
	err := s.LiveLoadScript("testrules_esm_missing.js")
	s.Error(err, "a missing static import must fail the load")
	s.Contains(err.Error(), `cannot find module "test/esm/nosuch"`)
	s.Contains(err.Error(), "imported from")
	s.EnsureGotErrors()
	s.SkipTill("[changed] testrules_esm_missing.js")
}

func (s *EsmSuite) TestEsmThrowIsLoadError() {
	// a throw during module evaluation is a synchronous load error (the
	// module's evaluation promise is rejected before any await) and is
	// reported once - not again as an unhandled rejection
	err := s.LiveLoadScript("testrules_esm_throw.js")
	s.Error(err)
	s.Contains(err.Error(), "esm load boom")
	s.EnsureGotErrors()
	// one error record (with the module's own stack), then the update
	s.Verify(
		regexp.MustCompile(`(?s:^wbrules-log -> /wbrules/log/error: \[Error: esm load boom.*testrules_esm_throw\.js:2.*)`),
		"[changed] testrules_esm_throw.js",
	)
}

func (s *EsmSuite) TestEsmThrowingDependencyIsLoadError() {
	err := s.LiveLoadScript("testrules_esm_dep_throw.js")
	s.Error(err)
	s.Contains(err.Error(), "esm module boom")
	s.EnsureGotErrors()
	s.Verify(
		regexp.MustCompile(`(?s:^wbrules-log -> /wbrules/log/error: \[Error: esm module boom.*test/esm/boom\.js:2.*)`),
		"[changed] testrules_esm_dep_throw.js",
	)
}

func (s *EsmSuite) TestRequireAndDynamicImportOfEsm() {
	s.loadEsm("testrules_esm_require.js")
	// require() of an ES module without top-level await returns its
	// namespace, through a CommonJS module too; one with top-level await is
	// refused with a Node.js-style code
	s.publish("/devices/esmreq/controls/req/on", "1", "esmreq/req")
	s.Verify(
		"tst -> /devices/esmreq/controls/req/on: [1] (QoS 1)",
		"driver -> /devices/esmreq/controls/req: [1] (QoS 1, retained)",
		"[info] Module esm util init",
		"[info] Module esm helper init",
		"[info] req: hello req 2",
		"[info] Module esm_cjs_bridge init",
		"[info] bridge: hello cjs 2 ERR_REQUIRE_ASYNC_MODULE",
		"[info] req tla: ERR_REQUIRE_ASYNC_MODULE",
		// the refused module had started evaluating (its synchronous part
		// ran); it finishes on the job queue and stays loaded for import()
		"[info] Module esm tla init",
	)
	// import() from a classic file (and of a top-level-await module) works
	s.publish("/devices/esmreq/controls/dyn/on", "1", "esmreq/dyn")
	s.Verify(
		"tst -> /devices/esmreq/controls/dyn/on: [1] (QoS 1)",
		"driver -> /devices/esmreq/controls/dyn: [1] (QoS 1, retained)",
		"[info] dyn: 8 util-default 7",
	)
}

func (s *EsmSuite) TestEsmSiblingImport() {
	// a library next to the rule file, reached by a relative import; it
	// imports from the module directories itself
	s.CopyDataFileToTempDir("esmlib/sib.js", "esmlib/sib.js")
	s.loadEsm("testrules_esm_sibling.js",
		"[info] Module esm util init",
		"[info] Module esmlib sib init",
	)
	s.publish("/devices/esmsib/controls/trigger/on", "1", "esmsib/trigger")
	s.Verify(
		"tst -> /devices/esmsib/controls/trigger/on: [1] (QoS 1)",
		"driver -> /devices/esmsib/controls/trigger: [1] (QoS 1, retained)",
		"[info] sib: sibling 42",
	)
}

func (s *EsmSuite) TestEsmErrorLineNumbers() {
	s.loadEsm("testrules_esm_err.js", "[info] Module esm util init")
	s.publish("/devices/esmerr/controls/trigger/on", "1", "esmerr/trigger")
	s.Verify(
		"tst -> /devices/esmerr/controls/trigger/on: [1] (QoS 1)",
		"driver -> /devices/esmerr/controls/trigger: [1] (QoS 1, retained)",
		regexp.MustCompile(`(?s:ECMAScript error:.*esm-boom 2.*testrules_esm_err\.js:12.*)`),
	)
}

// Editor.ResolveModule resolves a specifier the way the engine does for the
// file it is written in and hands the editor the module's source.
func (s *EsmSuite) TestEditorResolveModule() {
	s.CopyDataFileToTempDir("esmlib/sib.js", "esmlib/sib.js")
	s.loadEsm("testrules_esm_sibling.js",
		"[info] Module esm util init",
		"[info] Module esmlib sib init",
	)
	editor := NewEditor(s.engine)
	var reply EditorResolveModuleResponse
	// relative to the rule file (given by its virtual path)
	s.Ck("ResolveModule", editor.ResolveModule(
		&EditorResolveModuleArgs{From: "testrules_esm_sibling.js", Specifier: "./esmlib/sib.js"}, &reply))
	s.True(strings.HasSuffix(reply.Path, "/esmlib/sib.js"), reply.Path)
	s.Contains(reply.Content, `export const sib`)
	// bare, from the module directories, following the module's own import
	libPath := reply.Path
	s.Ck("ResolveModule", editor.ResolveModule(
		&EditorResolveModuleArgs{From: libPath, Specifier: "test/esm/util"}, &reply))
	s.Equal(filepath.Join(testRuleModulesDir(), "test", "esm", "util.js"), reply.Path)
	s.Contains(reply.Content, "export const double")
	// a .ts module is served as TypeScript source
	s.Ck("ResolveModule", editor.ResolveModule(
		&EditorResolveModuleArgs{From: libPath, Specifier: "test/esm/typed"}, &reply))
	s.True(strings.HasSuffix(reply.Path, "/test/esm/typed.ts"), reply.Path)
	s.Contains(reply.Content, "export interface Point")
	// unresolvable: the file-not-found editor error, message from the engine
	err := editor.ResolveModule(&EditorResolveModuleArgs{From: libPath, Specifier: "no/such"}, &reply)
	s.Error(err)
	s.Contains(err.Error(), `cannot find module "no/such"`)
	// the editor is served files from the rules workspace only: an absolute
	// specifier (or a relative one from a foreign base) pointing elsewhere
	// is refused, while the runtime resolver would accept it
	outside := filepath.Join(s.T().TempDir(), "outside.js")
	s.Ck("write", os.WriteFile(outside, []byte("exports.x = 1;"), 0o600))
	err = editor.ResolveModule(&EditorResolveModuleArgs{From: libPath, Specifier: outside}, &reply)
	s.Error(err)
	s.Contains(err.Error(), "outside the rule and module directories")
	s.Empty(reply.Path, "a failed call leaves no stale reply")
	err = editor.ResolveModule(&EditorResolveModuleArgs{From: outside, Specifier: "./outside.js"}, &reply)
	s.Error(err)
	// an unsaved rule (unknown to the engine) still resolves bare specifiers;
	// a relative one has nothing to be relative to
	s.Ck("ResolveModule", editor.ResolveModule(
		&EditorResolveModuleArgs{From: "unsaved.ts", Specifier: "test/esm/util"}, &reply))
	s.Contains(reply.Content, "export const double")
	err = editor.ResolveModule(&EditorResolveModuleArgs{From: "unsaved.ts", Specifier: "./x.js"}, &reply)
	s.Error(err)
	s.Contains(err.Error(), "needs an importing file")
}

func (s *EsmSuite) TestExplicitCjsExtension() {
	// .cjs: a script by name whatever its text looks like (the .mjs
	// counterpart, which needs the TypeScript modules, is in the TS suite)
	s.loadEsm("testrules_esm_explicit.cjs")
	s.publish("/devices/cjsx/controls/trigger/on", "1", "cjsx/trigger")
	s.Verify(
		"tst -> /devices/cjsx/controls/trigger/on: [1] (QoS 1)",
		"driver -> /devices/cjsx/controls/trigger: [1] (QoS 1, retained)",
		"[info] cjsx: object 29",
	)
}

func TestEsmSuite(t *testing.T) {
	testutils.RunSuites(t, new(EsmSuite))
}
