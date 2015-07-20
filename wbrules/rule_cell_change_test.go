package wbrules

import (
	"github.com/contactless/wbgo"
	"testing"
)

type RuleCellChangesSuite struct {
	RuleSuiteBase
}

func (s *RuleCellChangesSuite) SetupTest() {
	s.SetupSkippingDefs("testrules_cellchanges.js")
}

func (s *RuleCellChangesSuite) TestAssigningSameValueToACellSeveralTimes() {
	s.publish("/devices/cellch/controls/button/on", "1",
		"cellch/button", "cellch/sw", "cellch/misc")
	s.Verify(
		"tst -> /devices/cellch/controls/button/on: [1] (QoS 1)",
		"driver -> /devices/cellch/controls/button: [1] (QoS 1)", // no 'retained' flag for button
		"driver -> /devices/cellch/controls/sw: [1] (QoS 1, retained)",
		"driver -> /devices/cellch/controls/misc: [1] (QoS 1, retained)",
		"[info] startCellChange: sw <- true",
		"[info] switchChanged: sw=true",
		"driver -> /devices/somedev/controls/sw/on: [1] (QoS 1)",
	)
	s.publish("/devices/somedev/controls/sw", "1", "somedev/sw")
	s.Verify(
		"tst -> /devices/somedev/controls/sw: [1] (QoS 1, retained)",
	)

	s.publish("/devices/cellch/controls/button/on", "1",
		"cellch/button", "cellch/sw", "cellch/misc")
	s.Verify(
		"tst -> /devices/cellch/controls/button/on: [1] (QoS 1)",
		"driver -> /devices/cellch/controls/button: [1] (QoS 1)", // no 'retained' flag for button
		"driver -> /devices/cellch/controls/sw: [0] (QoS 1, retained)",
		"driver -> /devices/cellch/controls/misc: [1] (QoS 1, retained)",
		"[info] startCellChange: sw <- false",
		"[info] switchChanged: sw=false",
		"driver -> /devices/somedev/controls/sw/on: [1] (QoS 1)",
	)
	s.publish("/devices/somedev/controls/sw", "1", "somedev/sw")
	s.Verify(
		"tst -> /devices/somedev/controls/sw: [1] (QoS 1, retained)",
	)
}

func TestRuleCellChangesSuite(t *testing.T) {
	wbgo.RunSuites(t,
		new(RuleCellChangesSuite),
	)
}
