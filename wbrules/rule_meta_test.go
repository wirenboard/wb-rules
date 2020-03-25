package wbrules

import (
	"testing"

	"github.com/contactless/wbgong/testutils"
)

type RuleMetaSuite struct {
	RuleSuiteBase
}

func (s *RuleMetaSuite) SetupTest() {
	s.SetupSkippingDefs("testrules_meta.js")
}

func (s *RuleMetaSuite) TestMeta() {
	// set error by js code
	s.SetCellValueNoVerify("testDevice", "textControl", "setError")
	s.Verify(
		"driver -> /devices/testDevice/controls/textControl: [setError] (QoS 1, retained)",
		"[info] got textControl, changed: testDevice/textControl -> setError",
		"driver -> /devices/testDevice/controls/textControl/meta/error: [error text] (QoS 1, retained)",
	)

	// unset error by js code
	s.SetCellValueNoVerify("testDevice", "textControl", "unsetError")
	s.Verify(
		"driver -> /devices/testDevice/controls/textControl: [unsetError] (QoS 1, retained)",
		"[info] got textControl, changed: testDevice/textControl -> unsetError",
		"driver -> /devices/testDevice/controls/textControl/meta/error: [] (QoS 1, retained)",
	)
	s.expectControlChange("testDevice/textControl")

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

func TestRuleMetaSuite(t *testing.T) {
	testutils.RunSuites(t,
		new(RuleMetaSuite),
	)
}
