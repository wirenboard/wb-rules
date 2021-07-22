package wbrules

import (
	"testing"

	"github.com/wirenboard/wbgong/testutils"
)

type RuleMetaSuite struct {
	RuleSuiteBase
}

func (s *RuleMetaSuite) SetupTest() {
	s.SetupSkippingDefs("testrules_meta.js")
}

func (s *RuleMetaSuite) TestMeta() {
	// set error by js code
	s.publish("/devices/testDevice/controls/startControl/on", "1", "testDevice/startControl")
	s.VerifyUnordered(
		"driver -> /devices/testDevice/controls/startControl: [1] (QoS 1, retained)",
		"tst -> /devices/testDevice/controls/startControl/on: [1] (QoS 1)",
		"wbrules-log -> /wbrules/log/info: [got startControl, changed: testDevice/startControl -> true] (QoS 1)",
		"driver -> /devices/testDevice/controls/textControl/meta/error: [error text] (QoS 1, retained)",
		"driver -> /devices/testDevice/controls/textControl/meta/description: [new description] (QoS 1, retained)",
		"driver -> /devices/testDevice/controls/textControl/meta/type: [range] (QoS 1, retained)",
		"driver -> /devices/testDevice/controls/textControl/meta/max: [255] (QoS 1, retained)",
		"driver -> /devices/testDevice/controls/textControl/meta/units: [meters] (QoS 1, retained)",
		"driver -> /devices/testDevice/controls/textControl/meta/order: [4] (QoS 1, retained)",
		"driver -> /devices/testDevice/controls/textControl/meta/readonly: [1] (QoS 1, retained)",
	)

	// unset error by js code
	s.publish("/devices/testDevice/controls/startControl/on", "0", "testDevice/startControl")
	s.VerifyUnordered(
		"driver -> /devices/testDevice/controls/startControl: [0] (QoS 1, retained)",
		"tst -> /devices/testDevice/controls/startControl/on: [0] (QoS 1)",
		"wbrules-log -> /wbrules/log/info: [got startControl, changed: testDevice/startControl -> false] (QoS 1)",
		"driver -> /devices/testDevice/controls/textControl/meta/error: [] (QoS 1, retained)",
		"driver -> /devices/testDevice/controls/textControl/meta/description: [old description] (QoS 1, retained)",
		"driver -> /devices/testDevice/controls/textControl/meta/type: [text] (QoS 1, retained)",
		"driver -> /devices/testDevice/controls/textControl/meta/max: [0] (QoS 1, retained)",
		"driver -> /devices/testDevice/controls/textControl/meta/order: [5] (QoS 1, retained)",
		"driver -> /devices/testDevice/controls/textControl/meta/units: [chars] (QoS 1, retained)",
		"driver -> /devices/testDevice/controls/textControl/meta/readonly: [0] (QoS 1, retained)",
	)

	// when error set on somedev/sw -> testDevice/switchControl is changed to true
	s.publish("/devices/somedev/controls/sw/meta/error", "another error", "somedev/sw", "testDevice/switchControl")
	s.VerifyUnordered(
		"tst -> /devices/somedev/controls/sw/meta/error: [another error] (QoS 1, retained)",
		"driver -> /devices/testDevice/controls/switchControl: [1] (QoS 1, retained)",
		"[info] got sw, changed: somedev/sw#error -> another error",
	)

	// when error unset on somedev/sw -> testDevice/switchControl is changed to false
	s.publish("/devices/somedev/controls/sw/meta/error", "", "somedev/sw", "testDevice/switchControl")
	s.VerifyUnordered(
		"tst -> /devices/somedev/controls/sw/meta/error: [] (QoS 1, retained)",
		"driver -> /devices/testDevice/controls/switchControl: [0] (QoS 1, retained)",
		"[info] got sw, changed: somedev/sw#error -> ",
		"[info] somedev/sw = false",
	)

	s.VerifyEmpty()
}

func (s *RuleMetaSuite) TestUndefinedControlMeta() {
	s.publish("/devices/testDevice/controls/checkUndefinedControl/on",
		"1", "testDevice/checkUndefinedControl")
	s.VerifyUnordered(
		"driver -> /devices/testDevice/controls/checkUndefinedControl: [1] (QoS 1, retained)",
		"tst -> /devices/testDevice/controls/checkUndefinedControl/on: [1] (QoS 1)",
		"wbrules-log -> /wbrules/log/info: [Meta: null] (QoS 1)",
	)
}

func (s *RuleMetaSuite) TestVirtualDeviceOrder() {
	s.publish("/devices/testDevice/controls/vDevWithOrder/on", "1", "testDevice/vDevWithOrder")
	s.VerifyUnordered(
		"driver -> /devices/testDevice/controls/vDevWithOrder: [1] (QoS 1, retained)",
		"tst -> /devices/testDevice/controls/vDevWithOrder/on: [1] (QoS 1)",

		"Subscribe -- driver: /devices/vDevWithOrder/controls/test1/on",
		"Subscribe -- driver: /devices/vDevWithOrder/controls/test2/on",

		"driver -> /devices/vDevWithOrder/meta/driver: [wbrules] (QoS 1, retained)",
		"driver -> /devices/vDevWithOrder/meta/name: [] (QoS 1, retained)",

		"driver -> /devices/vDevWithOrder/controls/test1: [hello] (QoS 1, retained)",
		"driver -> /devices/vDevWithOrder/controls/test1/meta/type: [text] (QoS 1, retained)",
		"driver -> /devices/vDevWithOrder/controls/test1/meta/readonly: [1] (QoS 1, retained)",
		"driver -> /devices/vDevWithOrder/controls/test1/meta/order: [4] (QoS 1, retained)",
		"driver -> /devices/vDevWithOrder/controls/test2: [world] (QoS 1, retained)",
		"driver -> /devices/vDevWithOrder/controls/test2/meta/type: [text] (QoS 1, retained)",
		"driver -> /devices/vDevWithOrder/controls/test2/meta/order: [3] (QoS 1, retained)",
		"driver -> /devices/vDevWithOrder/controls/test2/meta/readonly: [1] (QoS 1, retained)",
	)
}

func TestRuleMetaSuite(t *testing.T) {
	testutils.RunSuites(t,
		new(RuleMetaSuite),
	)
}
