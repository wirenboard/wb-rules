package wbrules

import (
	"regexp"
	"testing"

	"github.com/wirenboard/wbgong/testutils"
)

type RuleSetValueVisibleSuite struct {
	RuleSuiteBase
}

func (s *RuleSetValueVisibleSuite) SetupTest() {
	s.SetupSkippingDefs("testrules_setvalue_visible.js")
}

// A wrong-typed control write must be reported to the rule debug console
// (engine.Log -> /wbrules/log/error, which homeui shows) with the file:line
// of the offending write - for both write paths, including the dev[] proxy
// whose innermost frames are lib.js - and must NOT throw: the rule keeps
// running and the cached value is not poisoned. A write to a control that
// does not exist is reported the same way.
func (s *RuleSetValueVisibleSuite) TestWrongTypedWriteIsVisibleAndNonFatal() {
	s.publish("/devices/somedev/controls/sw", "1", "somedev/sw")
	s.VerifyUnordered(
		"tst -> /devices/somedev/controls/sw: [1] (QoS 1, retained)",
		// surfaced to the rule log (what homeui's debug console shows), each
		// with the location of the write in the rule file
		regexp.MustCompile(`wbrules-log -> /wbrules/log/error: \[control vdev/num: write ignored \(.*\) at /.*testrules_setvalue_visible\.js:17\] \(QoS 1\)`),
		regexp.MustCompile(`wbrules-log -> /wbrules/log/error: \[control vdev/num: write ignored \(.*\) at /.*testrules_setvalue_visible\.js:18\] \(QoS 1\)`),
		regexp.MustCompile(`wbrules-log -> /wbrules/log/error: \[.*unexisting control vdev/nonexistent.* at /.*testrules_setvalue_visible\.js:19\] \(QoS 1\)`),
		// the rule continued past the bad writes (no throw) and read back the
		// real value 0 (cache not poisoned)
		"[info] after bad writes: value=0",
	)
	s.VerifyEmpty()
}

func TestRuleSetValueVisibleSuite(t *testing.T) {
	testutils.RunSuites(t, new(RuleSetValueVisibleSuite))
}
