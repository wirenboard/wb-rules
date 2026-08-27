package wbrules

import (
	"regexp"
	"testing"

	"github.com/wirenboard/wbgong/testutils"
)

// The fs module from TypeScript: import statements (namespace, default,
// named; fs/promises) and export statements work because the transpiler
// emits CommonJS, and the interop/binding helpers it prepends do not
// upset the source-line mapping of tracebacks.
type RuleTsFsSuite struct {
	RuleSuiteBase
}

func (s *RuleTsFsSuite) SetupTest() {
	s.TsgoPath = tsgoForTests()
	s.TsTypesPath = testTypesPath()
	s.SetupSkippingDefs()
}

func (s *RuleTsFsSuite) TestTsImportsAndExports() {
	s.Ck("LiveLoadScript", s.LiveLoadScript("testrules_ts_fs.ts"))
	s.Verify("[info] ts fs: true true true false")
	s.SkipTill("[changed] testrules_ts_fs.ts")
	s.Verify("[info] ts fs async: true")
}

func (s *RuleTsFsSuite) TestTsFsErrorLineNumbers() {
	s.Ck("LiveLoadScript", s.LiveLoadScript("testrules_ts_fs.ts"))
	s.SkipTill("[info] ts fs async: true")
	s.publish("/devices/somedev/controls/sw", "1", "somedev/sw")
	s.Verify(
		"tst -> /devices/somedev/controls/sw: [1] (QoS 1, retained)",
		regexp.MustCompile(`(?s:ECMAScript error:.*ts-fs-boom.*testrules_ts_fs\.ts:23.*)`),
	)
}

// An error thrown after an await is reported by the promise-rejection
// tracker, not the synchronous error path; its stack must be mapped to
// .ts lines just the same (the interop preamble shifts generated lines).
func (s *RuleTsFsSuite) TestTsFsAsyncErrorLineNumbers() {
	s.Ck("LiveLoadScript", s.LiveLoadScript("testrules_ts_fs.ts"))
	s.SkipTill("[info] ts fs async: true")
	s.publish("/devices/somedev/controls/temp", "20", "somedev/temp")
	s.Verify(
		"tst -> /devices/somedev/controls/temp: [20] (QoS 1, retained)",
		regexp.MustCompile(`(?s:async rule error:.*ts-fs-async-boom.*testrules_ts_fs\.ts:31.*)`),
	)
}

func TestRuleTsFsSuite(t *testing.T) {
	if tsgoForTests() == "" {
		t.Skip("no tsgo: WB_RULES_TSGO not set and /usr/bin/tsgo absent")
	}
	testutils.RunSuites(t, new(RuleTsFsSuite))
}
