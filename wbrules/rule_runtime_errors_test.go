package wbrules

import (
	"regexp"
	"testing"

	"github.com/wirenboard/wbgong/testutils"
)

type RuleRuntimeErrorsSuite struct {
	RuleSuiteBase
}

func (s *RuleRuntimeErrorsSuite) SetupTest() {
	s.SetupSkippingDefs("testrules_runtime_errors.js")
}

func (s *RuleRuntimeErrorsSuite) TestRuntimeErrors() {
	s.publish("/devices/somedev/controls/foobar/meta/type", "switch", "somedev/foobar")
	s.publish("/devices/somedev/controls/foobar", "1", "somedev/foobar")
	s.Verify(
		"tst -> /devices/somedev/controls/foobar/meta/type: [switch] (QoS 1, retained)",
		"tst -> /devices/somedev/controls/foobar: [1] (QoS 1, retained)",
		regexp.MustCompile(
			`(?s:ECMAScript error:.*ReferenceError.*testrules_runtime_errors\.js:8.*)`),
	)
	s.EnsureGotErrors()
}

func TestRuleRuntimeErrorsSuite(t *testing.T) {
	testutils.RunSuites(t,
		new(RuleRuntimeErrorsSuite),
	)
}
