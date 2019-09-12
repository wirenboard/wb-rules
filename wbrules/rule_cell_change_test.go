package wbrules

import (
	"github.com/evgeny-boger/wbgo/testutils"
	"testing"
)

type RuleCellChangesSuite struct {
	RuleSuiteBase
}

func (s *RuleCellChangesSuite) SetupTest() {
	s.SetupSkippingDefs("testrules_cellchanges.js")
}

func (s *RuleCellChangesSuite) TestAssigningSameValueToACellSeveralTimes() {
	// There was a problem with 'whenChanged' rules being marked as
	// 'rules without cells' which was negatively affecting performance
	// (related to SOFT-181).
	// The engine prints warnings if a rule gets marked as cell-less,
	// but only in case if debugging is enabled, as not to pollute
	// logs with too much warnings.
	// wbgo.SetDebuggingEnabled(true)
	// We don't want to skew other test resuls becuse Engine
	// initializes its MQTT debug flag fron wbgo debug flag
	// defer wbgo.SetDebuggingEnabled(false)

	s.publish("/devices/cellch/controls/button/on", "1",
		"cellch/button", "cellch/sw", "cellch/misc")
	s.VerifyUnordered(
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
	s.VerifyUnordered(
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
	// SOFT-181, see comment at the beginning of this test
	s.EnsureNoErrorsOrWarnings()
}

func TestRuleCellChangesSuite(t *testing.T) {
	testutils.RunSuites(t,
		new(RuleCellChangesSuite),
	)
}
