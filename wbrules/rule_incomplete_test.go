package wbrules

import (
	"testing"

	"github.com/contactless/wbgong/testutils"
)

type RuleIncompleteSuite struct {
	RuleSuiteBase
}

func (s *RuleIncompleteSuite) SetupTest() {
	s.SetupSkippingDefs("testrules_incomplete_1.js", "testrules_incomplete_2.js")
}

// checks if control gets/sets correct values inside rule
func (s *RuleIncompleteSuite) checkChange() {
	// set text to control
	s.publish("/devices/testControl/controls/switch_control/on", "1", "testControl/switch_control", "testControl/pers_text")
	s.VerifyUnordered(
		"driver -> /devices/testControl/controls/switch_control: [1] (QoS 1, retained)",
		"tst -> /devices/testControl/controls/switch_control/on: [1] (QoS 1)",
		"wbrules-log -> /wbrules/log/info: [text: Test text] (QoS 1)",
		"driver -> /devices/testControl/controls/pers_text: [Test text] (QoS 1, retained)",
	)
	s.VerifyEmpty()

	// set no text to control
	s.publish("/devices/testControl/controls/switch_control/on", "0", "testControl/switch_control", "testControl/pers_text")
	s.VerifyUnordered(
		"driver -> /devices/testControl/controls/switch_control: [0] (QoS 1, retained)",
		"tst -> /devices/testControl/controls/switch_control/on: [0] (QoS 1)",
		"wbrules-log -> /wbrules/log/info: [text: ] (QoS 1)",
		"driver -> /devices/testControl/controls/pers_text: [] (QoS 1, retained)",
	)
	s.VerifyEmpty()
}

func (s *RuleIncompleteSuite) TestIncomplete() {
	s.checkChange()

	// reload script to invalidate cache
	s.RemoveScript("testrules_incomplete_2.js")
	s.LiveLoadScript("testrules_incomplete_2.js")
	s.SkipTill("[changed] testrules_incomplete_2.js")

	s.checkChange()
}

func TestRuleIncompleteSuite(t *testing.T) {
	testutils.RunSuites(t, new(RuleIncompleteSuite))
}
