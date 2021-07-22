package wbrules

import (
	"testing"

	"github.com/wirenboard/wbgong/testutils"
)

type RuleControlsAPISuite struct {
	RuleSuiteBase
}

func (s *RuleControlsAPISuite) SetupTest() {
	s.SetupSkippingDefs("testrules_controls_api.js")
}

func (s *RuleControlsAPISuite) TestAPI() {
	// spawn new control
	s.publish("/devices/spawner/controls/spawn/on", "1", "spawner/spawn")
	s.VerifyUnordered(
		"driver -> /devices/spawner/controls/spawn: [1] (QoS 1, retained)",
		"tst -> /devices/spawner/controls/spawn/on: [1] (QoS 1)",
		"wbrules-log -> /wbrules/log/info: [ctrlID: change, error: ] (QoS 1)",
		"wbrules-log -> /wbrules/log/info: [ctrlID: check, error: ] (QoS 1)",
		"wbrules-log -> /wbrules/log/info: [ctrlID: spawn, error: ] (QoS 1)",
		"driver -> /devices/spawner/controls/wrCtrlID/meta/readonly: [0] (QoS 1, retained)",
		"driver -> /devices/spawner/controls/wrCtrlID/meta/order: [4] (QoS 1, retained)",
		"driver -> /devices/spawner/controls/wrCtrlID/meta/type: [text] (QoS 1, retained)",
		"driver -> /devices/spawner/controls/wrCtrlID: [test-text] (QoS 1, retained)",
		"Subscribe -- driver: /devices/spawner/controls/wrCtrlID/on",
	)

	// change control metadata by API from script
	s.publish("/devices/spawner/controls/change/on", "1", "spawner/change")
	s.VerifyUnordered(
		"driver -> /devices/spawner/controls/change: [1] (QoS 1, retained)",
		"tst -> /devices/spawner/controls/change/on: [1] (QoS 1)",
		"driver -> /devices/spawner/controls/wrCtrlID/meta/description: [true Description] (QoS 1, retained)",
		"driver -> /devices/spawner/controls/wrCtrlID/meta/error: [new Error] (QoS 1, retained)",
		"driver -> /devices/spawner/controls/wrCtrlID/meta/order: [5] (QoS 1, retained)",
		"driver -> /devices/spawner/controls/wrCtrlID/meta/type: [range] (QoS 1, retained)",
		"driver -> /devices/spawner/controls/wrCtrlID/meta/readonly: [1] (QoS 1, retained)",
		"driver -> /devices/spawner/controls/wrCtrlID/meta/max: [255] (QoS 1, retained)",
		"driver -> /devices/spawner/controls/wrCtrlID/meta/units: [meters] (QoS 1, retained)",
	)

	// check getters API inside script
	s.publish("/devices/spawner/controls/check/on", "1", "spawner/check")
	s.VerifyUnordered(
		"driver -> /devices/spawner/controls/check: [1] (QoS 1, retained)",
		"tst -> /devices/spawner/controls/check/on: [1] (QoS 1)",
		"wbrules-log -> /wbrules/log/info: [ctrlID: somedev, isVirtual: false] (QoS 1)",
		"wbrules-log -> /wbrules/log/info: [ctrlID: spawner, isVirtual: true] (QoS 1)",
		"wbrules-log -> /wbrules/log/info: [ctrlID: wrCtrlID, error: new Error] (QoS 1)",
		"wbrules-log -> /wbrules/log/info: [ctrlID: wrCtrlID, order: 5] (QoS 1)",
		"wbrules-log -> /wbrules/log/info: [ctrlID: wrCtrlID, type: range] (QoS 1)",
		"wbrules-log -> /wbrules/log/info: [ctrlID: wrCtrlID, readonly: true] (QoS 1)",
		"wbrules-log -> /wbrules/log/info: [ctrlID: wrCtrlID, max: 255] (QoS 1)",
		"wbrules-log -> /wbrules/log/info: [ctrlID: wrCtrlID, units: meters] (QoS 1)",
	)

	// change control metadata by API from script
	s.publish("/devices/spawner/controls/change/on", "0", "spawner/change")
	s.VerifyUnordered(
		"driver -> /devices/spawner/controls/change: [0] (QoS 1, retained)",
		"tst -> /devices/spawner/controls/change/on: [0] (QoS 1)",
		"driver -> /devices/spawner/controls/wrCtrlID/meta/error: [] (QoS 1, retained)",
		"driver -> /devices/spawner/controls/wrCtrlID/meta/description: [new Description] (QoS 1, retained)",
		"driver -> /devices/spawner/controls/wrCtrlID/meta/order: [4] (QoS 1, retained)",
		"driver -> /devices/spawner/controls/wrCtrlID/meta/type: [text] (QoS 1, retained)",
		"driver -> /devices/spawner/controls/wrCtrlID/meta/max: [0] (QoS 1, retained)",
		"driver -> /devices/spawner/controls/wrCtrlID/meta/units: [chars] (QoS 1, retained)",
		"driver -> /devices/spawner/controls/wrCtrlID/meta/readonly: [0] (QoS 1, retained)",
	)

	// check getters API inside script
	s.publish("/devices/spawner/controls/check/on", "0", "spawner/check")
	s.VerifyUnordered(
		"driver -> /devices/spawner/controls/check: [0] (QoS 1, retained)",
		"tst -> /devices/spawner/controls/check/on: [0] (QoS 1)",
		"wbrules-log -> /wbrules/log/info: [ctrlID: somedev, isVirtual: false] (QoS 1)",
		"wbrules-log -> /wbrules/log/info: [ctrlID: spawner, isVirtual: true] (QoS 1)",
		"wbrules-log -> /wbrules/log/info: [ctrlID: wrCtrlID, error: ] (QoS 1)",
		"wbrules-log -> /wbrules/log/info: [ctrlID: wrCtrlID, order: 4] (QoS 1)",
		"wbrules-log -> /wbrules/log/info: [ctrlID: wrCtrlID, type: text] (QoS 1)",
		"wbrules-log -> /wbrules/log/info: [ctrlID: wrCtrlID, readonly: false] (QoS 1)",
		"wbrules-log -> /wbrules/log/info: [ctrlID: wrCtrlID, max: 0] (QoS 1)",
		"wbrules-log -> /wbrules/log/info: [ctrlID: wrCtrlID, units: chars] (QoS 1)",
	)

	s.VerifyEmpty()

}

func TestRuleControlsAPISuite(t *testing.T) {
	testutils.RunSuites(t,
		new(RuleControlsAPISuite),
	)
}
