package wbrules

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/wirenboard/wbgong/testutils"
)

// RuleTypeScriptSuite exercises TypeScript rule files end to end:
// tsgo transpile on load, execution, error line numbers mapping to the
// .ts source, and the asynchronous background type check.
//
// tsgoForTests returns the tsgo binary the TypeScript suites run with:
// WB_RULES_TSGO when set, otherwise the installed /usr/bin/tsgo (the wb-tsgo
// package, present on a controller and on a CI host that builds with it);
// "" skips the suites.
func tsgoForTests() string {
	// The explicit opt-out wins even when a binary exists: a cross-build
	// chroot carries a foreign-arch tsgo that runs under qemu emulation -
	// an order of magnitude slower, failing on timing instead of substance.
	if os.Getenv("WB_RULES_NO_TSGO") == "1" {
		return ""
	}
	if p := os.Getenv("WB_RULES_TSGO"); p != "" {
		return p
	}
	if _, err := os.Stat("/usr/bin/tsgo"); err == nil {
		return "/usr/bin/tsgo"
	}
	return ""
}

// The TypeScript suites skip silently when no tsgo binary is found - which
// would let CI stay green without exercising the feature at all. This guard
// fails loudly instead, unless the run opts out explicitly with
// WB_RULES_NO_TSGO=1. (Declaring the wb-tsgo package a Build-Depends of
// wb-rules would give every CI build the binary unconditionally; that is a
// packaging decision still pending.)
func TestTypeScriptToolchainPresent(t *testing.T) {
	if os.Getenv("WB_RULES_NO_TSGO") == "1" {
		t.Skip("WB_RULES_NO_TSGO=1: the TypeScript suites are disabled deliberately")
	}
	if tsgoForTests() == "" {
		t.Fatal("tsgo not found (WB_RULES_TSGO unset, /usr/bin/tsgo absent): the TypeScript " +
			"suites would skip silently and the feature would go untested; install wb-tsgo " +
			"or set WB_RULES_TSGO, or set WB_RULES_NO_TSGO=1 to skip TypeScript deliberately")
	}
}

// Companion to the toolchain guard: the declarations the engine checks
// with must resolve to a real file even under -trimpath (the deb test
// stage), where a naive runtime.Caller join yields Go MODULE paths - the
// CI-only failure mode that once silently disabled every type check and
// turned the whole TypeScript suite into "unsupported" verdicts.
func TestTypesPathResolves(t *testing.T) {
	if _, err := os.Stat(testTypesPath()); err != nil {
		t.Fatalf("testTypesPath() does not resolve to a real file: %v", err)
	}
}

// Requires the tsgo binary: WB_RULES_TSGO or an installed /usr/bin/tsgo
// (the suite skips otherwise).
type RuleTypeScriptSuite struct {
	RuleSuiteBase
}

func (s *RuleTypeScriptSuite) SetupTest() {
	s.TsgoPath = tsgoForTests()
	s.TsTypesPath = testTypesPath()
	s.SetupSkippingDefs("testrules_ts.ts")
}

func (s *RuleTypeScriptSuite) TestTsRuleFires() {
	s.publish("/devices/tsdev/controls/trigger/on", "1", "tsdev/trigger", "tsdev/count")
	s.VerifyUnordered(
		"tst -> /devices/tsdev/controls/trigger/on: [1] (QoS 1)",
		"driver -> /devices/tsdev/controls/trigger: [1] (QoS 1, retained)",
		"driver -> /devices/tsdev/controls/count: [1] (QoS 1, retained)",
		"[info] ts rule fired: true count=1",
	)
}

func (s *RuleTypeScriptSuite) TestTsErrorLineNumbers() {
	s.publish("/devices/somedev/controls/sw", "1", "somedev/sw")
	s.Verify(
		"tst -> /devices/somedev/controls/sw: [1] (QoS 1, retained)",
		regexp.MustCompile(`(?s:ECMAScript error:.*ts-boom.*testrules_ts\.ts:29.*)`),
	)
}

func (s *RuleTypeScriptSuite) TestTsAsyncTypeCheck() {
	// load the type-broken file: it must run immediately...
	s.loadScripts([]string{"testrules_ts_badtypes.ts"})
	s.SkipTill("wbrules-log -> /wbrules/updates/changed: [testrules_ts_badtypes.ts] (QoS 1)")
	s.publish("/devices/tsdev/controls/trigger/on", "1", "tsdev/trigger", "tsdev/count")
	s.SkipTill("wbrules-log -> /wbrules/log/info: [badtypes rule fired anyway: not a number 42] (QoS 1)")
	// ...and the async check must eventually flag line 6 (Verify waits).
	// The path is reported absolute (tsgo itself prints it cwd-relative).
	s.Verify(regexp.MustCompile(`TS check: /.*testrules_ts_badtypes\.ts:6:.*`))
}

// Editor.Check serves the cached background verdict ("pending" until
// the check completes); Editor.GetTypes serves the installed
// declarations editors validate with.
func (s *RuleTypeScriptSuite) TestEditorCheckAndTypes() {
	editor := NewEditor(s.engine)

	waitReady := func(path string) EditorCheckResponse {
		var check EditorCheckResponse
		for i := 0; i < 100; i++ {
			s.Ck("Editor.Check", editor.Check(&EditorPathArgs{Path: path}, &check))
			if check.Status != TS_CHECK_PENDING {
				break
			}
			time.Sleep(100 * time.Millisecond)
		}
		s.Equal(TS_CHECK_READY, check.Status)
		return check
	}

	check := waitReady("testrules_ts.ts")
	s.Empty(check.Diags, "clean file must produce no diagnostics")

	s.loadScripts([]string{"testrules_ts_badtypes.ts"})
	s.SkipTill("wbrules-log -> /wbrules/updates/changed: [testrules_ts_badtypes.ts] (QoS 1)")
	check = waitReady("testrules_ts_badtypes.ts")
	s.NotEmpty(check.Diags)
	s.Equal(6, check.Diags[0].Line)
	s.Equal("error", check.Diags[0].Severity)
	s.Empty(check.Diags[0].File, "diag belongs to the checked file itself")

	var types EditorTypesResponse
	s.Ck("Editor.GetTypes", editor.GetTypes(&struct{}{}, &types))
	s.Contains(types.Content, "defineRule")
	// drain the async check warning for the badtypes file
	s.Verify(regexp.MustCompile(`TS check: .*testrules_ts_badtypes\.ts:6:.*`))
}

// The compile-time contract of types/wb-rules.d.ts: the assertion fixture
// pins both directions - idiomatic rule code must check clean, and the
// @ts-expect-error markers inside it turn into diagnostics themselves if
// an illegal construct (bad option/type combination, rule name instead of
// a rule id, ...) ever stops being a compile error.
func (s *RuleTypeScriptSuite) TestTypeAssertionsFixture() {
	s.loadScripts([]string{"testrules_ts_typeassert.ts"})
	s.SkipTill("wbrules-log -> /wbrules/updates/changed: [testrules_ts_typeassert.ts] (QoS 1)")

	editor := NewEditor(s.engine)
	var check EditorCheckResponse
	for i := 0; i < 100; i++ {
		s.Ck("Editor.Check", editor.Check(&EditorPathArgs{Path: "testrules_ts_typeassert.ts"}, &check))
		if check.Status != TS_CHECK_PENDING {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	s.Equal(TS_CHECK_READY, check.Status)
	s.Empty(check.Diags, "type assertion fixture must produce no diagnostics")
}

// The on-controller check generates a WbControls registry from the live
// device table, so the stringly-referenced APIs are typed against the
// controls that actually exist - here tsdev/count (numeric) and
// tsdev/trigger (switch), defined by testrules_ts.ts. Wrong-typed writes to
// them are flagged; a reference to a device that does not exist stays loose.
func (s *RuleTypeScriptSuite) TestOnControllerRegistryTyping() {
	s.loadScripts([]string{"testrules_ts_registry.ts"})
	s.SkipTill("wbrules-log -> /wbrules/updates/changed: [testrules_ts_registry.ts] (QoS 1)")

	editor := NewEditor(s.engine)
	var check EditorCheckResponse
	for i := 0; i < 100; i++ {
		s.Ck("Editor.Check", editor.Check(&EditorPathArgs{Path: "testrules_ts_registry.ts"}, &check))
		if check.Status != TS_CHECK_PENDING {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	s.Equal(TS_CHECK_READY, check.Status)

	lines := map[int]bool{}
	for _, d := range check.Diags {
		lines[d.Line] = true
	}
	s.True(lines[8], "string written to numeric tsdev/count must be flagged")
	s.True(lines[9], "number written to switch tsdev/trigger must be flagged")
	s.False(lines[10], "a correctly-typed write must not be flagged")
	s.False(lines[11], "a reference to an unregistered device must stay loose")

	// drain the two TS-check warnings this file logs, so they don't leak
	// into the next test's recorder
	s.Verify(
		regexp.MustCompile(`TS check: .*testrules_ts_registry\.ts:8:.*`),
		regexp.MustCompile(`TS check: .*testrules_ts_registry\.ts:9:.*`),
	)
}

// defineAlias creates a bare global at run time that the checker cannot
// see; the registry declares every alias so documented alias usage is not
// flagged as an unknown name.
func (s *RuleTypeScriptSuite) TestAliasesAreDeclaredForTheCheck() {
	s.loadScripts([]string{"testrules_ts_alias.ts"})
	s.SkipTill("wbrules-log -> /wbrules/updates/changed: [testrules_ts_alias.ts] (QoS 1)")
	editor := NewEditor(s.engine)
	check := s.waitCheck(editor, "testrules_ts_alias.ts")
	s.Empty(check.Diags, "alias usage must not produce diagnostics: %v", check.Diags)
}

// The registry snapshot is taken after the file has run, so the virtual
// devices the file itself defines are typed too - the most common shape of
// a rule file (define a vdev, write to it) is checked on first load and
// after a reload, which removes and re-creates the device.
func (s *RuleTypeScriptSuite) TestOwnVirtualDeviceIsInTheRegistry() {
	s.loadScripts([]string{"testrules_ts_ownvdev.ts"})
	s.SkipTill("wbrules-log -> /wbrules/updates/changed: [testrules_ts_ownvdev.ts] (QoS 1)")
	editor := NewEditor(s.engine)
	check := s.waitCheck(editor, "testrules_ts_ownvdev.ts")
	lines := map[int]bool{}
	for _, d := range check.Diags {
		lines[d.Line] = true
	}
	s.True(lines[10], "string written to own numeric control must be flagged")
	s.True(lines[11], "number written to own switch must be flagged")
	s.False(lines[12], "a correctly-typed write to own control must not be flagged")
	s.Verify(
		regexp.MustCompile(`TS check: .*testrules_ts_ownvdev\.ts:10:.*`),
		regexp.MustCompile(`TS check: .*testrules_ts_ownvdev\.ts:11:.*`),
	)

	// save from the editor (a forced reload): the device is removed and
	// re-created; the verdict must be the same
	s.Ck("OverwriteScript()", s.OverwriteScript("testrules_ts_ownvdev.ts", "testrules_ts_ownvdev.ts"))
	s.SkipTill("wbrules-log -> /wbrules/updates/changed: [testrules_ts_ownvdev.ts] (QoS 1)")
	check = s.waitCheck(editor, "testrules_ts_ownvdev.ts")
	lines = map[int]bool{}
	for _, d := range check.Diags {
		lines[d.Line] = true
	}
	s.True(lines[10] && lines[11], "own controls must stay typed after a reload")
	s.Verify(
		regexp.MustCompile(`TS check: .*testrules_ts_ownvdev\.ts:10:.*`),
		regexp.MustCompile(`TS check: .*testrules_ts_ownvdev\.ts:11:.*`),
	)
}

// A file that disappears between its load and the batched check (saved,
// then deleted or renamed within the batch window) must not poison the
// verdict of the files checked together with it: tsgo would abort the whole
// program on the missing file, so it is dropped from the run instead.
func (s *RuleTypeScriptSuite) TestVanishedFileDoesNotPoisonTheBatch() {
	s.loadScripts([]string{"testrules_ts_typeassert.ts", "testrules_js_registry.js"})
	// gone before the batch timer fires
	s.Ck("Remove()", os.Remove(filepath.Join(s.DataFileTempDir(), "testrules_ts_typeassert.ts")))
	s.SkipTill("wbrules-log -> /wbrules/updates/changed: [testrules_js_registry.js] (QoS 1)")
	editor := NewEditor(s.engine)
	reg := s.waitCheck(editor, "testrules_js_registry.js")
	lines := map[int]bool{}
	for _, d := range reg.Diags {
		lines[d.Line] = true
		s.NotContains(d.Message, "did not run", "the surviving file must get a real verdict")
	}
	s.True(lines[8] && lines[9], "the surviving file must be checked normally: %v", reg.Diags)
	s.Verify(
		regexp.MustCompile(`TS check: .*testrules_js_registry\.js:8:.*`),
		regexp.MustCompile(`TS check: .*testrules_js_registry\.js:9:.*`),
	)
}

// The on-controller check runs with --checkJs, so a plain .js rule file is
// type-checked against the wb-rules types and the live-device registry too -
// a wrong-typed write to a live control is flagged in .js exactly like in .ts.
func (s *RuleTypeScriptSuite) TestOnControllerRegistryTypingJs() {
	s.loadScripts([]string{"testrules_js_registry.js"})
	s.SkipTill("wbrules-log -> /wbrules/updates/changed: [testrules_js_registry.js] (QoS 1)")

	editor := NewEditor(s.engine)
	var check EditorCheckResponse
	for i := 0; i < 100; i++ {
		s.Ck("Editor.Check", editor.Check(&EditorPathArgs{Path: "testrules_js_registry.js"}, &check))
		if check.Status != TS_CHECK_PENDING {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	s.Equal(TS_CHECK_READY, check.Status)

	lines := map[int]bool{}
	for _, d := range check.Diags {
		lines[d.Line] = true
	}
	s.True(lines[8], "string written to numeric tsdev/count must be flagged in .js too")
	s.True(lines[9], "number written to switch tsdev/trigger must be flagged in .js too")
	s.False(lines[10], "a correctly-typed write must not be flagged")
	s.False(lines[11], "a reference to an unregistered device must stay loose")

	s.Verify(
		regexp.MustCompile(`TS check: .*testrules_js_registry\.js:8:.*`),
		regexp.MustCompile(`TS check: .*testrules_js_registry\.js:9:.*`),
	)
}

// require() of wb-rules modules and the constructible forms of
// PersistentStorage / StorableObject must stay clean in a .js file: TypeScript
// treats a call to the ambient require as a CommonJS import in .js and would
// otherwise report "Cannot find module" for every wb-rules module (the
// wildcard module in wb-rules.d.ts prevents that), and `new PersistentStorage`
// is what the shipped system rules write. The file is still genuinely checked:
// a wrong-typed registry write in it is flagged.
func (s *RuleTypeScriptSuite) TestJsRequireAndConstructibleStorage() {
	s.loadScripts([]string{"testrules_js_require.js"})
	s.SkipTill("wbrules-log -> /wbrules/updates/changed: [testrules_js_require.js] (QoS 1)")

	editor := NewEditor(s.engine)
	var check EditorCheckResponse
	for i := 0; i < 100; i++ {
		s.Ck("Editor.Check", editor.Check(&EditorPathArgs{Path: "testrules_js_require.js"}, &check))
		if check.Status != TS_CHECK_PENDING {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	s.Equal(TS_CHECK_READY, check.Status)

	lines := map[int]string{}
	for _, d := range check.Diags {
		lines[d.Line] = d.Message
	}
	s.NotContains(lines, 9, "require('logger.mod') must not be reported as a missing module")
	s.NotContains(lines, 11, "new PersistentStorage(...) must be accepted")
	s.NotContains(lines, 13, "new StorableObject(...) must be accepted")
	s.Contains(lines, 15, "the file must still be type-checked (wrong-typed registry write)")
	s.Len(check.Diags, 1, "exactly the one intended diagnostic: %v", check.Diags)

	s.Verify(regexp.MustCompile(`TS check: .*testrules_js_require\.js:15:.*`))
}

// waitCheck polls Editor.Check until the verdict for path is ready.
func (s *RuleTypeScriptSuite) waitCheck(editor *Editor, path string) EditorCheckResponse {
	var check EditorCheckResponse
	for i := 0; i < 100; i++ {
		s.Ck("Editor.Check", editor.Check(&EditorPathArgs{Path: path}, &check))
		if check.Status != TS_CHECK_PENDING {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	s.Equal(TS_CHECK_READY, check.Status)
	return check
}

// For a .js file the check is advisory: semantic complaints about valid
// sloppy-mode JavaScript idioms (Date arithmetic) are not reported, and what
// is reported comes as a warning.
func (s *RuleTypeScriptSuite) TestJsCheckSkipsSloppyIdiomsAndWarns() {
	s.loadScripts([]string{"testrules_js_sloppy.js"})
	s.SkipTill("wbrules-log -> /wbrules/updates/changed: [testrules_js_sloppy.js] (QoS 1)")
	check := s.waitCheck(NewEditor(s.engine), "testrules_js_sloppy.js")
	s.Len(check.Diags, 1, "only the real finding: %v", check.Diags)
	if len(check.Diags) == 1 {
		s.Equal(10, check.Diags[0].Line)
		s.Equal("warning", check.Diags[0].Severity, ".js diagnostics are advisory")
	}
	s.Verify(regexp.MustCompile(`TS check: .*testrules_js_sloppy\.js:10:.*`))
}

// A syntax error (for TypeScript) in one .js file - a legacy octal literal -
// is reported for that file and must not hide the type errors of a file
// checked in the same batch.
func (s *RuleTypeScriptSuite) TestSyntaxErrorInOneFileDoesNotBlindTheBatch() {
	s.waitCheck(NewEditor(s.engine), "testrules_ts.ts")
	runsBefore := s.engine.tsc.CheckRuns()
	s.loadScripts([]string{"testrules_js_octal.js", "testrules_js_registry.js"})
	s.SkipTill("wbrules-log -> /wbrules/updates/changed: [testrules_js_registry.js] (QoS 1)")
	editor := NewEditor(s.engine)
	octal := s.waitCheck(editor, "testrules_js_octal.js")
	// one batched run, then the re-run without the syntax-blocked file
	s.Equal(int64(2), s.engine.tsc.CheckRuns()-runsBefore, "the batch must be re-run once without the syntax-blocked file")
	s.Len(octal.Diags, 1, "exactly the octal diagnostic: %v", octal.Diags)
	if len(octal.Diags) == 1 {
		s.Equal(7, octal.Diags[0].Line)
		s.Equal(1121, octal.Diags[0].Code)
	}
	reg := s.waitCheck(editor, "testrules_js_registry.js")
	lines := map[int]bool{}
	for _, d := range reg.Diags {
		lines[d.Line] = true
	}
	s.True(lines[8] && lines[9], "the other file keeps its own type errors: %v", reg.Diags)
	s.VerifyUnordered(
		regexp.MustCompile(`TS check: .*testrules_js_octal\.js:7:.*`),
		regexp.MustCompile(`TS check: .*testrules_js_registry\.js:8:.*`),
		regexp.MustCompile(`TS check: .*testrules_js_registry\.js:9:.*`),
	)
}

// The journal shows at most tsCheckJournalCap lines per file check plus a
// summary; Editor.Check keeps the full list.
func (s *RuleTypeScriptSuite) TestJournalCapsDiagnosticsPerFile() {
	s.loadScripts([]string{"testrules_js_manyerrors.js"})
	s.SkipTill("wbrules-log -> /wbrules/updates/changed: [testrules_js_manyerrors.js] (QoS 1)")
	check := s.waitCheck(NewEditor(s.engine), "testrules_js_manyerrors.js")
	s.Len(check.Diags, 12)
	expected := make([]any, 0, tsCheckJournalCap+1)
	for i := 0; i < tsCheckJournalCap; i++ {
		expected = append(expected, regexp.MustCompile(fmt.Sprintf(`TS check: .*testrules_js_manyerrors\.js:%d:.*`, i+5)))
	}
	expected = append(expected, regexp.MustCompile(`TS check: .*testrules_js_manyerrors\.js: 2 more diagnostics.*`))
	s.Verify(expected...)
}

// Files loaded in quick succession are checked in ONE tsgo program and the
// diagnostics are split back per file: each file gets exactly its own.
func (s *RuleTypeScriptSuite) TestBatchedChecksSplitPerFile() {
	// SetupTest's own load (testrules_ts.ts) has a check in flight; wait it
	// out so the counter below counts only this test's batch
	s.waitCheck(NewEditor(s.engine), "testrules_ts.ts")
	runsBefore := s.engine.tsc.CheckRuns()
	s.loadScripts([]string{"testrules_ts_badtypes.ts", "testrules_ts_registry.ts", "testrules_js_registry.js"})
	s.SkipTill("wbrules-log -> /wbrules/updates/changed: [testrules_js_registry.js] (QoS 1)")
	editor := NewEditor(s.engine)
	linesOf := func(path string) []int {
		var out []int
		for _, d := range s.waitCheck(editor, path).Diags {
			out = append(out, d.Line)
		}
		sort.Ints(out)
		return out
	}
	s.Equal([]int{6}, linesOf("testrules_ts_badtypes.ts"))
	s.Equal([]int{8, 9}, linesOf("testrules_ts_registry.ts"))
	s.Equal([]int{8, 9}, linesOf("testrules_js_registry.js"))
	// three files loaded within the batch window -> ONE tsgo process
	s.Equal(int64(1), s.engine.tsc.CheckRuns()-runsBefore, "co-loaded files must be checked in one tsgo run")
	// drain the check warnings (badtypes:6, registry 8/9, js registry 8/9)
	s.VerifyUnordered(
		regexp.MustCompile(`TS check: .*testrules_ts_badtypes\.ts:6:.*`),
		regexp.MustCompile(`TS check: .*testrules_ts_registry\.ts:8:.*`),
		regexp.MustCompile(`TS check: .*testrules_ts_registry\.ts:9:.*`),
		regexp.MustCompile(`TS check: .*testrules_js_registry\.js:8:.*`),
		regexp.MustCompile(`TS check: .*testrules_js_registry\.js:9:.*`),
	)
}

// A .ts rule file may use top-level await: the runtime wraps files in an
// async function, and the check runs in module mode so tsgo does not warn.
func (s *RuleTypeScriptSuite) TestTopLevelAwaitChecksClean() {
	s.loadScripts([]string{"testrules_ts_tla.ts"})
	// the top-level await resolves during load and logs; drain up to the
	// reload update
	s.SkipTill("wbrules-log -> /wbrules/updates/changed: [testrules_ts_tla.ts] (QoS 1)")

	editor := NewEditor(s.engine)
	var check EditorCheckResponse
	for i := 0; i < 100; i++ {
		s.Ck("Editor.Check", editor.Check(&EditorPathArgs{Path: "testrules_ts_tla.ts"}, &check))
		if check.Status != TS_CHECK_PENDING {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	s.Equal(TS_CHECK_READY, check.Status)
	s.Empty(check.Diags, "top-level await must not produce a diagnostic")
}

// A vendor/custom control type outside the wb-rules type map: the runtime
// accepts it and delivers its value as a string, the registry generated
// from the live control carries the vendor type, and the background check
// must accept both the declaration and the stringly reference - never
// reject an unknown type.
func (s *RuleTypeScriptSuite) TestVendorControlTypeRuntimeAndRegistry() {
	s.loadScripts([]string{"testrules_ts_vendor.ts"})
	s.SkipTill("[changed] testrules_ts_vendor.ts")

	// the vendor-typed control exists and its value reaches the rule as a string
	s.publish("/devices/vendor_ts/controls/mode/on", "eco", "vendor_ts/mode")
	s.VerifyUnordered(
		"tst -> /devices/vendor_ts/controls/mode/on: [eco] (QoS 1)",
		"driver -> /devices/vendor_ts/controls/mode: [eco] (QoS 1, retained)",
		"[info] vendor mode: eco (string)",
	)

	// the check accepts the vendor type in the declaration and maps the
	// registry entry to a loose reference instead of erroring on it
	check := s.waitCheck(NewEditor(s.engine), "testrules_ts_vendor.ts")
	s.Empty(check.Diags, "a vendor-typed control must check clean: %v", check.Diags)
}

// Removing a file while its background check is in flight advances the
// file's check generation, so the in-flight verdict for content that no
// longer exists is discarded on arrival instead of repopulating the cache;
// a re-created file gets its own fresh verdict.
func (s *RuleTypeScriptSuite) TestRemovedFileCheckVerdictNotResurrected() {
	editor := NewEditor(s.engine)
	// let SetupTest's own check settle so the batch below is ours alone
	s.waitCheck(editor, "testrules_ts.ts")
	// the clean file is queued FIRST (its verdict is reported before the
	// surviving file's), then removed before the batch timer fires
	s.loadScripts([]string{"testrules_ts_typeassert.ts", "testrules_ts_registry.ts"})
	physPath := filepath.Join(s.DataFileTempDir(), "testrules_ts_typeassert.ts")
	s.Ck("Remove()", os.Remove(physPath))
	s.Ck("LiveRemoveFile()", s.engine.LiveRemoveFile(physPath))
	s.SkipTill("[removed] testrules_ts_typeassert.ts")

	// the co-batched survivor still gets its own verdict...
	reg := s.waitCheck(editor, "testrules_ts_registry.ts")
	lines := map[int]bool{}
	for _, d := range reg.Diags {
		lines[d.Line] = true
	}
	s.True(lines[8] && lines[9], "the surviving file must be checked normally: %v", reg.Diags)
	// ...and by now the removed file's report was delivered (it was queued
	// first): it must have been discarded, not cached
	s.engine.tsCheckMu.Lock()
	_, found := s.engine.tsCheckResults[physPath]
	s.engine.tsCheckMu.Unlock()
	s.False(found, "a removed file's in-flight verdict must not repopulate the cache")
	_, status := s.engine.CheckTsFile(physPath)
	s.NotEqual(TS_CHECK_READY, status, "no ready verdict for a removed file")
	s.Verify(
		regexp.MustCompile(`TS check: .*testrules_ts_registry\.ts:8:.*`),
		regexp.MustCompile(`TS check: .*testrules_ts_registry\.ts:9:.*`),
	)

	// save-by-rename shape: the file comes back and is checked afresh
	s.loadScripts([]string{"testrules_ts_typeassert.ts"})
	s.SkipTill("[changed] testrules_ts_typeassert.ts")
	check := s.waitCheck(editor, "testrules_ts_typeassert.ts")
	s.Empty(check.Diags, "the re-created file gets its own clean verdict: %v", check.Diags)
}

// The engine checks with --strict false; the same declarations must also
// hold under --strict true (external IDEs), which nothing else exercises.
func TestTypeAssertionsFixtureStrict(t *testing.T) {
	tsgo := tsgoForTests()
	if tsgo == "" {
		t.Skip("no tsgo: WB_RULES_TSGO not set and /usr/bin/tsgo absent")
	}
	root := filepath.Dir(testModulesDir())
	cmd := exec.Command(tsgo, "--noEmit", "--target", "esnext", "--lib", "esnext",
		"--strict", "true", "--module", "preserve", "--moduleDetection", "force", "--esModuleInterop",
		filepath.Join(root, "types", "wb-rules.d.ts"),
		filepath.Join(root, "wbrules", "testrules_ts_typeassert.ts"))
	out, err := cmd.CombinedOutput()
	if err != nil || len(strings.TrimSpace(string(out))) != 0 {
		t.Fatalf("type assertion fixture is not clean under --strict true: %v\n%s", err, out)
	}
}

func TestRuleTypeScriptSuite(t *testing.T) {
	if tsgoForTests() == "" {
		t.Skip("no tsgo: WB_RULES_TSGO not set and /usr/bin/tsgo absent")
	}
	testutils.RunSuites(t, new(RuleTypeScriptSuite))
}
