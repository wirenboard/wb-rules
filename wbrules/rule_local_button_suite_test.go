package wbrules

import (
	"github.com/wirenboard/wbgong/testutils"
	"testing"
)

type RuleLocalButtonSuite struct {
	RuleSuiteBase
}

func (s *RuleLocalButtonSuite) SetupTest() {
	// s.RuleSuiteBase.SetupTest(false, "testrules_localbutton.js")
	s.RuleSuiteBase.SetupSkippingDefs("testrules_localbutton.js")
}

func (s *RuleLocalButtonSuite) TestLocalButtons() {
	for i := 0; i < 3; i++ {
		// The change rule must be fired on each button press ('1' .../on value message)
		s.publish("/devices/buttons/controls/somebutton/on", "1", "buttons/somebutton")
		s.VerifyUnordered(
			"tst -> /devices/buttons/controls/somebutton/on: [1] (QoS 1)",
			"driver -> /devices/buttons/controls/somebutton: [1] (QoS 1)", // note there's no 'retained' flag
			"[info] button pressed!",
		)
	}
}

func TestRuleLocalButtonsSuite(t *testing.T) {
	testutils.RunSuites(t,
		new(RuleLocalButtonSuite),
	)
}
