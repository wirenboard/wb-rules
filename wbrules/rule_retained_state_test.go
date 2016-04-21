package wbrules

import (
	"github.com/contactless/wbgo/testutils"
	"testing"
)

type RuleRetainedStateSuite struct {
	RuleSuiteBase
}

func (s *RuleRetainedStateSuite) SetupTest() {
	s.RuleSuiteBase.SetupTest(true, "testrules.js")
	s.engine.Start()
}

func (s *RuleRetainedStateSuite) TestRetainedState() {
	s.Verify(
		"driver -> /devices/stabSettings/meta/name: [Stabilization Settings] (QoS 1, retained)",
		"driver -> /devices/wbrules/meta/name: [Rule Engine Settings] (QoS 1, retained)",
		"Subscribe -- driver: /devices/+/meta/name",
		"Subscribe -- driver: /devices/+/controls/+",
		"Subscribe -- driver: /devices/+/controls/+/meta/type",
		"Subscribe -- driver: /devices/+/controls/+/meta/max",
	)
	s.VerifyEmpty()

	s.publish("/devices/stabSettings/controls/enabled", "1", "stabSettings/enabled")
	// lower the threshold so that the rule doesn't fire immediately
	// (which mixes up cell change events during s.publishSomedev())
	s.publish("/devices/stabSettings/controls/lowThreshold", "18", "stabSettings/lowThreshold")
	s.Verify(
		"tst -> /devices/stabSettings/controls/enabled: [1] (QoS 1, retained)",
		"tst -> /devices/stabSettings/controls/lowThreshold: [18] (QoS 1, retained)",
	)
	s.VerifyEmpty()

	s.Broker.SetReady()
	<-s.engine.ReadyCh()

	s.Verify(
		"driver -> /devices/stabSettings/controls/enabled/meta/type: [switch] (QoS 1, retained)",
		"driver -> /devices/stabSettings/controls/enabled/meta/order: [1] (QoS 1, retained)",
		"driver -> /devices/stabSettings/controls/enabled: [1] (QoS 1, retained)",
		"Subscribe -- driver: /devices/stabSettings/controls/enabled/on",
		"driver -> /devices/stabSettings/controls/highThreshold/meta/type: [range] (QoS 1, retained)",
		"driver -> /devices/stabSettings/controls/highThreshold/meta/order: [2] (QoS 1, retained)",
		"driver -> /devices/stabSettings/controls/highThreshold/meta/max: [50] (QoS 1, retained)",
		"driver -> /devices/stabSettings/controls/highThreshold: [22] (QoS 1, retained)",
		"Subscribe -- driver: /devices/stabSettings/controls/highThreshold/on",
		"driver -> /devices/stabSettings/controls/lowThreshold/meta/type: [range] (QoS 1, retained)",
		"driver -> /devices/stabSettings/controls/lowThreshold/meta/order: [3] (QoS 1, retained)",
		"driver -> /devices/stabSettings/controls/lowThreshold/meta/max: [40] (QoS 1, retained)",
		"driver -> /devices/stabSettings/controls/lowThreshold: [18] (QoS 1, retained)",
		"Subscribe -- driver: /devices/stabSettings/controls/lowThreshold/on",

		"driver -> /devices/wbrules/controls/Rule debugging/meta/type: [switch] (QoS 1, retained)",
		"driver -> /devices/wbrules/controls/Rule debugging/meta/order: [1] (QoS 1, retained)",
		"driver -> /devices/wbrules/controls/Rule debugging: [0] (QoS 1, retained)",
		"Subscribe -- driver: /devices/wbrules/controls/Rule debugging/on",
	)
	s.publishSomedev()
	s.Verify(
		"tst -> /devices/somedev/meta/name: [SomeDev] (QoS 1, retained)",
		"tst -> /devices/somedev/controls/sw/meta/type: [switch] (QoS 1, retained)",
		"tst -> /devices/somedev/controls/sw: [0] (QoS 1, retained)",
		"tst -> /devices/somedev/controls/temp/meta/type: [temperature] (QoS 1, retained)",
		"tst -> /devices/somedev/controls/temp: [19] (QoS 1, retained)",
	)
	s.publish("/devices/somedev/controls/temp", "16", "somedev/temp")
	s.Verify(
		"tst -> /devices/somedev/controls/temp: [16] (QoS 1, retained)",
		"[info] heaterOn fired, changed: somedev/temp -> 16",
		"driver -> /devices/somedev/controls/sw/on: [1] (QoS 1)",
	)
	s.publish("/devices/somedev/controls/sw", "1", "somedev/sw")
	s.Verify(
		"tst -> /devices/somedev/controls/sw: [1] (QoS 1, retained)",
	)
	s.VerifyEmpty()
}

func TestRuleRetainedStateSuite(t *testing.T) {
	testutils.RunSuites(t,
		new(RuleRetainedStateSuite),
	)
}
