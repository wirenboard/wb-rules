package wbrules

import (
	"testing"

	"github.com/wirenboard/wbgong/testutils"
)

type RuleDevicesListSuite struct {
	RuleSuiteBase
}

func (s *RuleDevicesListSuite) SetupTest() {
	s.SetupSkippingDefs("testrules_devices_list.js")
}

func (s *RuleDevicesListSuite) TestDevicesList() {
	s.publish("/devices/lctl/controls/list/on", "1", "lctl/list")
	s.VerifyUnordered(
		"tst -> /devices/lctl/controls/list/on: [1] (QoS 1)",
		"driver -> /devices/lctl/controls/list: [1] (QoS 1, retained)",
		// sorted, virtual and external devices together
		"[info] ids: lctl,listdev_a,listdev_b,somedev,wbrules",
		"[info] virtual: lctl,listdev_a,listdev_b,wbrules",
		"[info] driver of lctl: [wbrules]",
		"[info] driver of listdev_a: [wbrules]",
		"[info] driver of listdev_b: [wbrules]",
		"[info] driver of somedev: []",
		"[info] driver of wbrules: [wbrules]",
		"[info] listdev_a controls via list element: 2",
	)
	s.VerifyEmpty()
	s.EnsureNoErrorsOrWarnings()
}

func TestRuleDevicesListSuite(t *testing.T) {
	testutils.RunSuites(t,
		new(RuleDevicesListSuite),
	)
}
