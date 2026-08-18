package wbrules

import (
	"regexp"
	"testing"

	"github.com/wirenboard/wbgong/testutils"
)

type RuleSetValueTypeSuite struct {
	RuleSuiteBase
}

func (s *RuleSetValueTypeSuite) SetupTest() {
	s.SetupSkippingDefs("testrules_setvalue_type.js")
}

// A wrong-typed control write is reported to the rule debug console
// (/wbrules/log/error, which homeui shows) via both getControl(...).setValue()
// and the dev["dev/ctrl"] = ... proxy, and does NOT throw: the rule keeps
// running, the cached value is not poisoned, and a correctly-typed write still
// succeeds.
func (s *RuleSetValueTypeSuite) TestWrongTypedWriteIsVisibleAndNonFatal() {
	// The correct write below changes vdev/num, so expect that change too.
	s.publish("/devices/somedev/controls/sw", "1", "somedev/sw", "vdev/num")
	s.VerifyUnordered(
		"tst -> /devices/somedev/controls/sw: [1] (QoS 1, retained)",
		// both rejected writes are surfaced to the rule log (homeui-visible),
		// with the control ref and the driver's conversion reason.
		regexp.MustCompile(
			`wbrules-log -> /wbrules/log/error: \[control vdev/num: write ignored.*convert.*\] \(QoS 1\)`),
		regexp.MustCompile(
			`wbrules-log -> /wbrules/log/error: \[control vdev/num: write ignored.*convert.*\] \(QoS 1\)`),
		// the rule continued past both bad writes (no throw) and read back the
		// real value 0 (cache not poisoned).
		"[info] after bad writes: value=0",
		// the correctly-typed write succeeded and updated the control.
		"driver -> /devices/vdev/controls/num: [42] (QoS 1, retained)",
		"[info] correct write: value=42",
	)
	s.VerifyEmpty()
}

func TestRuleSetValueTypeSuite(t *testing.T) {
	testutils.RunSuites(t,
		new(RuleSetValueTypeSuite),
	)
}
