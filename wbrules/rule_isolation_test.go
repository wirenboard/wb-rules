package wbrules

import (
	"github.com/contactless/wbgo/testutils"
	"testing"
)

type RuleIsolationSuite struct {
	RuleSuiteBase
}

func (s *RuleIsolationSuite) SetupTest() {
	s.SetupSkippingDefs("testrules_isolation_1.js", "testrules_isolation_2.js")
}

func (s *RuleIsolationSuite) TestIsolation() {
	s.publish("/devices/vdev/controls/someCell/on", "1", "vdev/someCell")
	s.VerifyUnordered(
		"tst -> /devices/vdev/controls/someCell/on: [1] (QoS 1)",
		"driver -> /devices/vdev/controls/someCell: [1] (QoS 1, retained)",
		"[info] isolated_rule (testrules_isolation_1.js) 84",
		"[info] isolated_rule (testrules_isolation_2.js) 42",
	)
}

func TestRuleIsolationSuite(t *testing.T) {
	testutils.RunSuites(t,
		new(RuleIsolationSuite),
	)
}
