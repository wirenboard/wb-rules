package wbrules

import (
	"github.com/contactless/wbgo/testutils"
	"testing"
)

type RuleOptimizationSuite struct {
	RuleSuiteBase
}

func (s *RuleOptimizationSuite) SetupTest() {
	s.SetupSkippingDefs("testrules_opt.js")
}

func (s *RuleOptimizationSuite) TestRuleCheckOptimization() {
	s.Verify(
		// That's the first time when all rules are run.
		// somedev/countIt and somedev/countItLT are incomplete here, but
		// the engine notes that rules' conditions depend on the cells
		"[info] condCount: asSoonAs()",
		"[info] condCountLT: when()",
	)
	s.publish("/devices/somedev/controls/countIt/meta/type", "text", "somedev/countIt")
	s.publish("/devices/somedev/controls/countIt", "0", "somedev/countIt")
	s.Verify(
		"tst -> /devices/somedev/controls/countIt/meta/type: [text] (QoS 1, retained)",
		"tst -> /devices/somedev/controls/countIt: [0] (QoS 1, retained)",
		// here the value of the cell changes, so the rule is invoked
		"[info] condCount: asSoonAs()")

	s.publish("/devices/somedev/controls/temp", "25", "somedev/temp")
	s.publish("/devices/somedev/controls/countIt", "42", "somedev/countIt")
	s.Verify(
		"tst -> /devices/somedev/controls/temp: [25] (QoS 1, retained)",
		// changing unrelated cell doesn't cause the rule to be invoked
		"tst -> /devices/somedev/controls/countIt: [42] (QoS 1, retained)",
		"[info] condCount: asSoonAs()",
		// asSoonAs function called during the first run + when countIt
		// value changed to 42
		"[info] condCount fired, count=3",
		// ruleWithoutCells follows condCount rule in testrules.js
		// and doesn't utilize any cells. It's run just once when condCount
		// rule sets a global variable to true.
		"[info] ruleWithoutCells fired")

	s.publish("/devices/somedev/controls/countIt", "0", "somedev/countIt")
	s.Verify(
		"tst -> /devices/somedev/controls/countIt: [0] (QoS 1, retained)",
		"[info] condCount: asSoonAs()")
	s.publish("/devices/somedev/controls/countIt", "42", "somedev/countIt")
	s.Verify(
		"tst -> /devices/somedev/controls/countIt: [42] (QoS 1, retained)",
		"[info] condCount: asSoonAs()",
		"[info] condCount fired, count=5")

	// now check optimization of level-triggered rules
	s.publish("/devices/somedev/controls/countItLT/meta/type", "text", "somedev/countItLT")
	s.publish("/devices/somedev/controls/countItLT", "0", "somedev/countItLT")
	s.Verify(
		"tst -> /devices/somedev/controls/countItLT/meta/type: [text] (QoS 1, retained)",
		"tst -> /devices/somedev/controls/countItLT: [0] (QoS 1, retained)",
		// here the value of the cell changes, so the rule is invoked
		"[info] condCountLT: when()")

	s.publish("/devices/somedev/controls/countItLT", "42", "somedev/countItLT")
	s.Verify(
		"tst -> /devices/somedev/controls/countItLT: [42] (QoS 1, retained)",
		"[info] condCountLT: when()",
		// when function called during the first run + when countItLT
		// value changed to 42
		"[info] condCountLT fired, count=3")

	s.publish("/devices/somedev/controls/countItLT", "43", "somedev/countItLT")
	s.Verify(
		"tst -> /devices/somedev/controls/countItLT: [43] (QoS 1, retained)",
		"[info] condCountLT: when()",
		"[info] condCountLT fired, count=4")

	s.publish("/devices/somedev/controls/countItLT", "0", "somedev/countItLT")
	s.Verify(
		"tst -> /devices/somedev/controls/countItLT: [0] (QoS 1, retained)",
		"[info] condCountLT: when()")

	s.publish("/devices/somedev/controls/countItLT", "1", "somedev/countItLT")
	s.Verify(
		"tst -> /devices/somedev/controls/countItLT: [1] (QoS 1, retained)",
		"[info] condCountLT: when()")
}

func TestRuleOptimizationSuite(t *testing.T) {
	testutils.RunSuites(t,
		new(RuleOptimizationSuite),
	)
}
