package wbrules

import (
	"testing"

	"github.com/wirenboard/wbgong/testutils"
)

type RuleEmptyStringSuite struct {
	RuleSuiteBase
}

func (s *RuleEmptyStringSuite) SetupTest() {
	s.SetupSkippingDefs("testrules_empty_string.js")
}

// TestWhenChangedEmptyString reproduces SOFT-5446:
// whenChanged doesn't fire when a text control value changes to or from "".
func (s *RuleEmptyStringSuite) TestWhenChangedEmptyString() {
	// 1. First SetCellValue from initial "".
	// In the test environment, the initial value "" doesn't generate a
	// ControlValueEvent through driverEventHandler (local device, no MQTT echo),
	// so this is the first ControlValueEvent and gets suppressed by the
	// first-value check. In production, the MQTT broker echoes the initial
	// publication back, making this the second event (which fires).
	s.SetCellValue("emptyStrTest", "text", "test1")
	s.Verify(
		"driver -> /devices/emptyStrTest/controls/text: [test1] (QoS 1, retained)",
	)

	// 2. Set text to "test2" — whenChanged should fire (normal change)
	s.SetCellValue("emptyStrTest", "text", "test2")
	s.Verify(
		"driver -> /devices/emptyStrTest/controls/text: [test2] (QoS 1, retained)",
		"[info] textChanged fired",
	)

	// 3. Set text to "" — whenChanged SHOULD fire (this was broken: SOFT-5446)
	s.SetCellValue("emptyStrTest", "text", "")
	s.Verify(
		"driver -> /devices/emptyStrTest/controls/text: [] (QoS 1, retained)",
		"[info] textChanged fired",
	)

	// 4. Set text to "aaa" after "" — whenChanged SHOULD fire (this was broken: SOFT-5446)
	// Root cause: ToTypedValue("", "text") returns nil, so PrevValue is nil,
	// and the old PrevValue==nil check incorrectly treated it as a first value.
	s.SetCellValue("emptyStrTest", "text", "aaa")
	s.Verify(
		"driver -> /devices/emptyStrTest/controls/text: [aaa] (QoS 1, retained)",
		"[info] textChanged fired",
	)

	// 5. Normal change — whenChanged should fire
	s.SetCellValue("emptyStrTest", "text", "bbb")
	s.Verify(
		"driver -> /devices/emptyStrTest/controls/text: [bbb] (QoS 1, retained)",
		"[info] textChanged fired",
	)
}

func TestRuleEmptyStringSuite(t *testing.T) {
	testutils.RunSuites(t,
		new(RuleEmptyStringSuite),
	)
}
