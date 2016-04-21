package wbrules

import (
	"github.com/contactless/wbgo/testutils"
	"testing"
)

type RuleDefSuite struct {
	RuleSuiteBase
}

func (s *RuleDefSuite) SetupTest() {
	s.RuleSuiteBase.SetupTest(false, "testrules.js")
}

func (s *RuleDefSuite) TestDeviceDefinition() {
	s.Verify(
		"driver -> /devices/stabSettings/meta/name: [Stabilization Settings] (QoS 1, retained)",
		"driver -> /devices/wbrules/meta/name: [Rule Engine Settings] (QoS 1, retained)",

		"Subscribe -- driver: /devices/+/meta/name",
		"Subscribe -- driver: /devices/+/controls/+",
		"Subscribe -- driver: /devices/+/controls/+/meta/type",
		"Subscribe -- driver: /devices/+/controls/+/meta/max",

		"driver -> /devices/stabSettings/controls/enabled/meta/type: [switch] (QoS 1, retained)",
		"driver -> /devices/stabSettings/controls/enabled/meta/order: [1] (QoS 1, retained)",
		"driver -> /devices/stabSettings/controls/enabled: [0] (QoS 1, retained)",
		"Subscribe -- driver: /devices/stabSettings/controls/enabled/on",
		"driver -> /devices/stabSettings/controls/highThreshold/meta/type: [range] (QoS 1, retained)",
		"driver -> /devices/stabSettings/controls/highThreshold/meta/order: [2] (QoS 1, retained)",
		"driver -> /devices/stabSettings/controls/highThreshold/meta/max: [50] (QoS 1, retained)",
		"driver -> /devices/stabSettings/controls/highThreshold: [22] (QoS 1, retained)",
		"Subscribe -- driver: /devices/stabSettings/controls/highThreshold/on",
		"driver -> /devices/stabSettings/controls/lowThreshold/meta/type: [range] (QoS 1, retained)",
		"driver -> /devices/stabSettings/controls/lowThreshold/meta/order: [3] (QoS 1, retained)",
		"driver -> /devices/stabSettings/controls/lowThreshold/meta/max: [40] (QoS 1, retained)",
		"driver -> /devices/stabSettings/controls/lowThreshold: [20] (QoS 1, retained)",
		"Subscribe -- driver: /devices/stabSettings/controls/lowThreshold/on",

		"driver -> /devices/wbrules/controls/Rule debugging/meta/type: [switch] (QoS 1, retained)",
		"driver -> /devices/wbrules/controls/Rule debugging/meta/order: [1] (QoS 1, retained)",
		"driver -> /devices/wbrules/controls/Rule debugging: [0] (QoS 1, retained)",
		"Subscribe -- driver: /devices/wbrules/controls/Rule debugging/on",

		"tst -> /devices/somedev/meta/name: [SomeDev] (QoS 1, retained)",
		"tst -> /devices/somedev/controls/sw/meta/type: [switch] (QoS 1, retained)",
		"tst -> /devices/somedev/controls/sw: [0] (QoS 1, retained)",
		"tst -> /devices/somedev/controls/temp/meta/type: [temperature] (QoS 1, retained)",
		"tst -> /devices/somedev/controls/temp: [19] (QoS 1, retained)",
	)
}

func TestRuleDefSuite(t *testing.T) {
	testutils.RunSuites(t,
		new(RuleDefSuite),
	)
}
