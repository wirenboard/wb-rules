package wbrules

import (
	"github.com/contactless/wbgo/testutils"
	"testing"
)

type RuleBasicsSuite struct {
	RuleSuiteBase
}

func (s *RuleBasicsSuite) SetupTest() {
	s.SetupSkippingDefs("testrules.js")
}

func (s *RuleBasicsSuite) TestRules() {
	s.SetCellValue("stabSettings", "enabled", true)
	s.Verify(
		"driver -> /devices/stabSettings/controls/enabled: [1] (QoS 1, retained)",
		"[info] heaterOn fired, changed: stabSettings/enabled -> true",
		"driver -> /devices/somedev/controls/sw/on: [1] (QoS 1)",
	)
	s.publish("/devices/somedev/controls/sw", "1", "somedev/sw")
	s.Verify(
		"tst -> /devices/somedev/controls/sw: [1] (QoS 1, retained)",
	)

	s.publish("/devices/somedev/controls/temp", "21", "somedev/temp")
	s.Verify(
		"tst -> /devices/somedev/controls/temp: [21] (QoS 1, retained)",
	)

	s.publish("/devices/somedev/controls/temp", "22", "somedev/temp")
	s.Verify(
		"tst -> /devices/somedev/controls/temp: [22] (QoS 1, retained)",
		"[info] heaterOff fired, changed: somedev/temp -> 22",
		"driver -> /devices/somedev/controls/sw/on: [0] (QoS 1)",
	)
	s.publish("/devices/somedev/controls/sw", "0", "somedev/sw")
	s.Verify(
		"tst -> /devices/somedev/controls/sw: [0] (QoS 1, retained)",
	)

	s.publish("/devices/somedev/controls/temp", "18", "somedev/temp")
	s.Verify(
		"tst -> /devices/somedev/controls/temp: [18] (QoS 1, retained)",
		"[info] heaterOn fired, changed: somedev/temp -> 18",
		"driver -> /devices/somedev/controls/sw/on: [1] (QoS 1)",
	)
	s.publish("/devices/somedev/controls/sw", "1", "somedev/sw")
	s.Verify(
		"tst -> /devices/somedev/controls/sw: [1] (QoS 1, retained)",
	)

	// edge-triggered rule doesn't fire
	s.publish("/devices/somedev/controls/temp", "19", "somedev/temp")
	s.Verify(
		"tst -> /devices/somedev/controls/temp: [19] (QoS 1, retained)",
	)

	s.SetCellValue("stabSettings", "enabled", false)
	s.Verify(
		"driver -> /devices/stabSettings/controls/enabled: [0] (QoS 1, retained)",
		"[info] heaterOff fired, changed: stabSettings/enabled -> false",
		"driver -> /devices/somedev/controls/sw/on: [0] (QoS 1)",
	)
	s.publish("/devices/somedev/controls/sw", "0", "somedev/sw")
	s.Verify(
		"tst -> /devices/somedev/controls/sw: [0] (QoS 1, retained)",
	)

	s.publish("/devices/somedev/controls/foobar", "1", "somedev/foobar")
	s.publish("/devices/somedev/controls/foobar/meta/type", "text", "somedev/foobar")
	s.Verify(
		"tst -> /devices/somedev/controls/foobar: [1] (QoS 1, retained)",
		"tst -> /devices/somedev/controls/foobar/meta/type: [text] (QoS 1, retained)",
		"[info] initiallyIncompleteLevelTriggered fired",
	)

	// level-triggered rule fires again here
	s.publish("/devices/somedev/controls/foobar", "2", "somedev/foobar")
	s.Verify(
		"tst -> /devices/somedev/controls/foobar: [2] (QoS 1, retained)",
		"[info] initiallyIncompleteLevelTriggered fired",
	)
}

func (s *RuleBasicsSuite) TestDirectMQTTMessages() {
	s.publish("/devices/somedev/controls/sendit/meta/type", "switch", "somedev/sendit")
	s.publish("/devices/somedev/controls/sendit", "1", "somedev/sendit")
	s.Verify(
		"tst -> /devices/somedev/controls/sendit/meta/type: [switch] (QoS 1, retained)",
		"tst -> /devices/somedev/controls/sendit: [1] (QoS 1, retained)",
		"driver -> /abc/def/ghi: [0] (QoS 0)",
		"driver -> /misc/whatever: [abcdef] (QoS 1)",
		"driver -> /zzz/foo: [qqq] (QoS 2)",
		"driver -> /zzz/foo/qwerty: [42] (QoS 2, retained)",
	)
}

func (s *RuleBasicsSuite) TestCellChange() {
	s.publish("/devices/somedev/controls/foobarbaz/meta/type", "text", "somedev/foobarbaz")
	s.publish("/devices/somedev/controls/foobarbaz", "abc", "somedev/foobarbaz")
	s.Verify(
		"tst -> /devices/somedev/controls/foobarbaz/meta/type: [text] (QoS 1, retained)",
		"tst -> /devices/somedev/controls/foobarbaz: [abc] (QoS 1, retained)",
		"[info] cellChange1: somedev/foobarbaz=abc (string)",
		"[info] cellChange2: somedev/foobarbaz=abc (string)",
	)

	s.publish("/devices/somedev/controls/tempx/meta/type", "temperature", "somedev/tempx")
	s.publish("/devices/somedev/controls/tempx", "42", "somedev/tempx")
	s.Verify(
		"tst -> /devices/somedev/controls/tempx/meta/type: [temperature] (QoS 1, retained)",
		"tst -> /devices/somedev/controls/tempx: [42] (QoS 1, retained)",
		"[info] cellChange2: somedev/tempx=42 (number)",
	)
	// no change
	s.publish("/devices/somedev/controls/tempx", "42", "somedev/tempx")
	s.publish("/devices/somedev/controls/tempx", "42", "somedev/tempx")
	s.Verify(
		"tst -> /devices/somedev/controls/tempx: [42] (QoS 1, retained)",
		"tst -> /devices/somedev/controls/tempx: [42] (QoS 1, retained)",
	)
}

func (s *RuleBasicsSuite) TestRemoteButtons() {
	// FIXME: handling remote buttons, i.e. buttons that
	// are defined for external devices and not via defineVirtualDevice(),
	// needs more work. We need to handle /on messages for these
	// instead of value messages. As of now, the code will work
	// unless the remote driver retains button value, in which
	// case extra change events will be received on startup
	// The change rule must be fired on each button press ('1' value message)
	s.publish("/devices/somedev/controls/abutton/meta/type", "pushbutton", "somedev/abutton")
	s.publish("/devices/somedev/controls/abutton", "1", "somedev/abutton")
	s.Verify(
		"tst -> /devices/somedev/controls/abutton/meta/type: [pushbutton] (QoS 1, retained)",
		"tst -> /devices/somedev/controls/abutton: [1] (QoS 1, retained)",
		"[info] cellChange2: somedev/abutton=false (boolean)",
	)
	s.publish("/devices/somedev/controls/abutton", "1", "somedev/abutton")
	s.Verify(
		"tst -> /devices/somedev/controls/abutton: [1] (QoS 1, retained)",
		"[info] cellChange2: somedev/abutton=false (boolean)",
	)
}

func (s *RuleBasicsSuite) TestFuncValueChange() {
	s.publish("/devices/somedev/controls/cellforfunc", "2", "somedev/cellforfunc")
	s.Verify(
		// the cell is incomplete here
		"tst -> /devices/somedev/controls/cellforfunc: [2] (QoS 1, retained)",
	)

	s.publish("/devices/somedev/controls/cellforfunc/meta/type", "temperature", "somedev/cellforfunc")
	s.Verify(
		"tst -> /devices/somedev/controls/cellforfunc/meta/type: [temperature] (QoS 1, retained)",
		"[info] funcValueChange: false (boolean)",
	)

	s.publish("/devices/somedev/controls/cellforfunc", "5", "somedev/cellforfunc")
	s.Verify(
		"tst -> /devices/somedev/controls/cellforfunc: [5] (QoS 1, retained)",
		"[info] funcValueChange: true (boolean)",
	)

	s.publish("/devices/somedev/controls/cellforfunc", "7", "somedev/cellforfunc")
	s.Verify(
		// expression value not changed
		"tst -> /devices/somedev/controls/cellforfunc: [7] (QoS 1, retained)",
	)

	s.publish("/devices/somedev/controls/cellforfunc", "1", "somedev/cellforfunc")
	s.Verify(
		"tst -> /devices/somedev/controls/cellforfunc: [1] (QoS 1, retained)",
		"[info] funcValueChange: false (boolean)",
	)

	s.publish("/devices/somedev/controls/cellforfunc", "0", "somedev/cellforfunc")
	s.Verify(
		// expression value not changed
		"tst -> /devices/somedev/controls/cellforfunc: [0] (QoS 1, retained)",
	)

	// somedev/cellforfunc1 is listed by name
	s.publish("/devices/somedev/controls/cellforfunc1", "2", "somedev/cellforfunc1")
	s.publish("/devices/somedev/controls/cellforfunc2", "2", "somedev/cellforfunc2")
	s.Verify(
		// the cell is incomplete here
		"tst -> /devices/somedev/controls/cellforfunc1: [2] (QoS 1, retained)",
		"tst -> /devices/somedev/controls/cellforfunc2: [2] (QoS 1, retained)",
	)

	s.publish("/devices/somedev/controls/cellforfunc1/meta/type", "temperature", "somedev/cellforfunc1")
	s.Verify(
		"tst -> /devices/somedev/controls/cellforfunc1/meta/type: [temperature] (QoS 1, retained)",
		"[info] funcValueChange2: somedev/cellforfunc1: 2 (number)",
	)

	s.publish("/devices/somedev/controls/cellforfunc2/meta/type", "temperature", "somedev/cellforfunc2")
	s.Verify(
		"tst -> /devices/somedev/controls/cellforfunc2/meta/type: [temperature] (QoS 1, retained)",
		"[info] funcValueChange2: (no cell): false (boolean)",
	)

	s.publish("/devices/somedev/controls/cellforfunc2", "5", "somedev/cellforfunc2")
	s.Verify(
		"tst -> /devices/somedev/controls/cellforfunc2: [5] (QoS 1, retained)",
		"[info] funcValueChange2: (no cell): true (boolean)",
	)
}

func TestRuleBasicsSuite(t *testing.T) {
	testutils.RunSuites(t,
		new(RuleBasicsSuite),
	)
}
