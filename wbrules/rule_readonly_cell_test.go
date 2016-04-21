package wbrules

import (
	"github.com/contactless/wbgo/testutils"
	"testing"
)

type RuleReadOnlyCellSuite struct {
	RuleSuiteBase
}

func (s *RuleReadOnlyCellSuite) SetupTest() {
	s.RuleSuiteBase.SetupTest(false, "testrules_readonly.js")
}

func (s *RuleReadOnlyCellSuite) TestReadOnlyCells() {
	s.Verify(
		"driver -> /devices/roCells/meta/name: [Readonly Cell Test] (QoS 1, retained)",
		"driver -> /devices/wbrules/meta/name: [Rule Engine Settings] (QoS 1, retained)",
		"Subscribe -- driver: /devices/+/meta/name",
		"Subscribe -- driver: /devices/+/controls/+",
		"Subscribe -- driver: /devices/+/controls/+/meta/type",
		"Subscribe -- driver: /devices/+/controls/+/meta/max",
		"driver -> /devices/roCells/controls/rocell/meta/type: [switch] (QoS 1, retained)",
		"driver -> /devices/roCells/controls/rocell/meta/readonly: [1] (QoS 1, retained)",
		"driver -> /devices/roCells/controls/rocell/meta/order: [1] (QoS 1, retained)",
		"driver -> /devices/roCells/controls/rocell: [0] (QoS 1, retained)",
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

func TestRuleReadOnlyCellSuite(t *testing.T) {
	testutils.RunSuites(t,
		new(RuleReadOnlyCellSuite),
	)
}
