package wbrules

import (
	"testing"

	"github.com/wirenboard/wbgong/testutils"
)

type JSThreadingTestSuite struct {
	RuleSuiteBase
}

func (s *JSThreadingTestSuite) SetupTest() {
	s.SetupSkippingDefs("testrules_threading_1.js", "testrules_threading_2.js")
}

func (s *JSThreadingTestSuite) TestRuntime() {
	s.publish("/devices/test/controls/test/on", "1", "test/test")
	s.Verify("tst -> /devices/test/controls/test/on: [1] (QoS 1)")
	s.VerifyUnordered(
		"driver -> /devices/test/controls/test: [1] (QoS 1)",
		"[info] it works!",
	)
}

func (s *JSThreadingTestSuite) TestThreadsIsolation() {
	s.publish("/devices/test/controls/isolation/on", "1", "test/isolation")

	s.VerifyUnordered(
		"tst -> /devices/test/controls/isolation/on: [1] (QoS 1)",
		"driver -> /devices/test/controls/isolation: [1] (QoS 1)",
		"[info] 1: myvar: 42",
		"[info] 1: add 2 and 3: 5",
		"[info] 2: myvar: 84",
		"[info] 2: add 2 and 3: -1",
	)
}

func TestJSThreading(t *testing.T) {
	testutils.RunSuites(t, new(JSThreadingTestSuite))
}
