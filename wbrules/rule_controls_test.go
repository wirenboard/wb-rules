package wbrules

import (
	"testing"

	"github.com/wirenboard/wbgong/testutils"
)

type RuleControlsSuite struct {
	RuleSuiteBase
}

func (s *RuleControlsSuite) SetupTest() {
	s.SetupSkippingDefs("testrules_rule_controls.js")
}

func (s *RuleControlsSuite) TestTrigger() {
	s.publish("/devices/ctrltest/controls/trigger/on", "1", "ctrltest/trigger")

	s.Verify("tst -> /devices/ctrltest/controls/trigger/on: [1] (QoS 1)")
	s.VerifyUnordered(
		"driver -> /devices/ctrltest/controls/trigger: [1] (QoS 1)",
		"[info] controllable rule fired",
	)
}

func (s *RuleControlsSuite) TestDisable() {
	s.publish("/devices/ctrltest/controls/disable/on", "1", "ctrltest/disable")

	s.Verify("tst -> /devices/ctrltest/controls/disable/on: [1] (QoS 1)")
	s.VerifyUnordered(
		"driver -> /devices/ctrltest/controls/disable: [1] (QoS 1)",
		"[info] disable",
	)

	s.publish("/devices/ctrltest/controls/trigger/on", "1", "ctrltest/trigger")
	s.Verify(
		"tst -> /devices/ctrltest/controls/trigger/on: [1] (QoS 1)",
		"driver -> /devices/ctrltest/controls/trigger: [1] (QoS 1)",
	)

	s.VerifyEmpty()

	s.publish("/devices/ctrltest/controls/enable/on", "1", "ctrltest/enable")

	s.Verify("tst -> /devices/ctrltest/controls/enable/on: [1] (QoS 1)")
	s.VerifyUnordered(
		"driver -> /devices/ctrltest/controls/enable: [1] (QoS 1)",
		"[info] enable",
	)

	s.publish("/devices/ctrltest/controls/trigger/on", "1", "ctrltest/trigger")

	s.Verify("tst -> /devices/ctrltest/controls/trigger/on: [1] (QoS 1)")
	s.VerifyUnordered(
		"driver -> /devices/ctrltest/controls/trigger: [1] (QoS 1)",
		"[info] controllable rule fired",
	)
}

func (s *RuleControlsSuite) TestRunRule() {
	s.publish("/devices/ctrltest/controls/run/on", "1", "ctrltest/run")

	s.Verify("tst -> /devices/ctrltest/controls/run/on: [1] (QoS 1)")
	s.VerifyUnordered(
		"driver -> /devices/ctrltest/controls/run: [1] (QoS 1)",
		"[info] run",
		"[info] controllable rule fired",
	)
}

func TestRuleControls(t *testing.T) {
	testutils.RunSuites(t, new(RuleControlsSuite))
}
