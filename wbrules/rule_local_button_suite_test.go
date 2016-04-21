package wbrules

import (
	"github.com/contactless/wbgo/testutils"
	"testing"
)

type RuleLocalButtonSuite struct {
	RuleSuiteBase
}

func (s *RuleLocalButtonSuite) SetupTest() {
	s.RuleSuiteBase.SetupTest(false, "testrules_localbutton.js")
	s.engine.Start()
	<-s.engine.ReadyCh()
}

func (s *RuleLocalButtonSuite) TestLocalButtons() {
	s.Verify(
		"driver -> /devices/buttons/meta/name: [Button Test] (QoS 1, retained)",
		"driver -> /devices/wbrules/meta/name: [Rule Engine Settings] (QoS 1, retained)",
		"Subscribe -- driver: /devices/+/meta/name",
		"Subscribe -- driver: /devices/+/controls/+",
		"Subscribe -- driver: /devices/+/controls/+/meta/type",
		"Subscribe -- driver: /devices/+/controls/+/meta/max",
		"driver -> /devices/buttons/controls/somebutton/meta/type: [pushbutton] (QoS 1, retained)",
		"driver -> /devices/buttons/controls/somebutton/meta/order: [1] (QoS 1, retained)",
		"Subscribe -- driver: /devices/buttons/controls/somebutton/on",

		"driver -> /devices/wbrules/controls/Rule debugging/meta/type: [switch] (QoS 1, retained)",
		"driver -> /devices/wbrules/controls/Rule debugging/meta/order: [1] (QoS 1, retained)",
		"driver -> /devices/wbrules/controls/Rule debugging: [0] (QoS 1, retained)",
		"Subscribe -- driver: /devices/wbrules/controls/Rule debugging/on",
		// FIXME: don't need these here
		"tst -> /devices/somedev/meta/name: [SomeDev] (QoS 1, retained)",
		"tst -> /devices/somedev/controls/sw/meta/type: [switch] (QoS 1, retained)",
		"tst -> /devices/somedev/controls/sw: [0] (QoS 1, retained)",
		"tst -> /devices/somedev/controls/temp/meta/type: [temperature] (QoS 1, retained)",
		"tst -> /devices/somedev/controls/temp: [19] (QoS 1, retained)",
	)
	s.VerifyEmpty()

	for i := 0; i < 3; i++ {
		// The change rule must be fired on each button press ('1' .../on value message)
		s.publish("/devices/buttons/controls/somebutton/on", "1", "buttons/somebutton")
		s.Verify(
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
