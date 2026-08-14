package wbrules

import (
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"testing"

	"github.com/wirenboard/wbgong/testutils"
)

// RuleTypeScriptSuite exercises TypeScript rule files end to end:
// tsgo transpile on load, execution, error line numbers mapping to the
// .ts source, and the asynchronous background type check.
//
// Requires the tsgo binary: set WB_RULES_TSGO (suite skips otherwise).
type RuleTypeScriptSuite struct {
	RuleSuiteBase
}

func (s *RuleTypeScriptSuite) SetupTest() {
	tsgo := os.Getenv("WB_RULES_TSGO")
	_, thisFile, _, _ := runtime.Caller(0)
	s.TsgoPath = tsgo
	s.TsTypesPath = filepath.Join(filepath.Dir(thisFile), "..", "types", "wb-rules.d.ts")
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
	// ...and the async check must eventually flag line 6 (Verify waits)
	s.Verify(regexp.MustCompile(`TS check: .*testrules_ts_badtypes\.ts:6:.*`))
	// diagnostics are also published as retained JSON for the UI editor
	s.Verify(regexp.MustCompile(`/wbrules/ts-check/testrules_ts_badtypes\.ts: \[\{"file":"testrules_ts_badtypes\.ts","diags":\[\{"line":6,.*"severity":"error".*\]\}\] \(QoS 1, retained\)`))
}

func TestRuleTypeScriptSuite(t *testing.T) {
	if os.Getenv("WB_RULES_TSGO") == "" {
		t.Skip("WB_RULES_TSGO not set")
	}
	testutils.RunSuites(t, new(RuleTypeScriptSuite))
}
