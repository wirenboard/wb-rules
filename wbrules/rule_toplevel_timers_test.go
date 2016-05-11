package wbrules

import (
	"github.com/contactless/wbgo/testutils"
	"testing"
	"time"
)

type RuleToplevelTimersSuite struct {
	RuleSuiteBase
}

func (s *RuleToplevelTimersSuite) SetupTest() {
	s.RuleSuiteBase.SetupTest(true, "testrules_topleveltimers.js")
	s.engine.Start()
}

func (s *RuleToplevelTimersSuite) TestToplevelTimers() {
	// make sure timers aren't started until the rule engine is ready
	s.Verify(
		"driver -> /devices/wbrules/meta/name: [Rule Engine Settings] (QoS 1, retained)",
		"Subscribe -- driver: /devices/+/meta/name",
		"Subscribe -- driver: /devices/+/controls/+",
		"Subscribe -- driver: /devices/+/controls/+/meta/type",
		"Subscribe -- driver: /devices/+/controls/+/meta/max",
	)
	s.VerifyEmpty()
	s.Broker.SetReady()
	<-s.engine.ReadyCh()
	s.VerifyUnordered(
		// timer id = 2 because timer 1 was created & removed immediately
		// before the engine was ready
		"new fake timer: 2, 1000",
		"driver -> /devices/wbrules/controls/Rule debugging/meta/type: [switch] (QoS 1, retained)",
		"driver -> /devices/wbrules/controls/Rule debugging/meta/order: [1] (QoS 1, retained)",
		"driver -> /devices/wbrules/controls/Rule debugging: [0] (QoS 1, retained)",
		"Subscribe -- driver: /devices/wbrules/controls/Rule debugging/on",
	)
	ts := s.AdvanceTime(1000 * time.Millisecond)
	s.FireTimer(2, ts)
	s.Verify(
		"timer.fire(): 2",
		"[info] timer fired",
	)
	s.VerifyEmpty()
}

func TestRuleToplevelTimersSuite(t *testing.T) {
	testutils.RunSuites(t,
		new(RuleToplevelTimersSuite),
	)
}
