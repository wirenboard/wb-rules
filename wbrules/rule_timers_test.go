package wbrules

import (
	"github.com/contactless/wbgo"
	"testing"
	"time"
)

type RuleTimersSuite struct {
	RuleSuiteBase
}

func (s *RuleTimersSuite) SetupTest() {
	s.SetupSkippingDefs("testrules_timers.js")
}

func (s *RuleTimersSuite) VerifyTimers(prefix string) {
	s.publish("/devices/somedev/controls/foo/meta/type", "text", "somedev/foo")
	s.publish("/devices/somedev/controls/foo", prefix+"t", "somedev/foo")
	s.Verify(
		"tst -> /devices/somedev/controls/foo/meta/type: [text] (QoS 1, retained)",
		"tst -> /devices/somedev/controls/foo: ["+prefix+"t] (QoS 1, retained)",
		"new fake timer: 1, 500",
		"new fake timer: 2, 500",
	)

	s.publish("/devices/somedev/controls/foo", prefix+"s", "somedev/foo")
	s.Verify(
		"tst -> /devices/somedev/controls/foo: ["+prefix+"s] (QoS 1, retained)",
		"timer.Stop(): 1",
		"timer.Stop(): 2",
	)

	s.publish("/devices/somedev/controls/foo", prefix+"t", "somedev/foo")
	s.Verify(
		"tst -> /devices/somedev/controls/foo: ["+prefix+"t] (QoS 1, retained)",
		"new fake timer: 1, 500",
		"new fake timer: 2, 500",
	)

	ts := s.AdvanceTime(500 * time.Millisecond)
	s.FireTimer(1, ts)
	s.FireTimer(2, ts)
	s.Verify(
		"timer.fire(): 1",
		"timer.fire(): 2",
		"[info] timer fired",
		"[info] timer1 fired",
	)

	s.publish("/devices/somedev/controls/foo", prefix+"p", "somedev/foo")
	s.Verify(
		"tst -> /devices/somedev/controls/foo: ["+prefix+"p] (QoS 1, retained)",
		"new fake ticker: 1, 500",
	)

	for i := 1; i < 4; i++ {
		targetTime := s.AdvanceTime(time.Duration(500*i) * time.Millisecond)
		s.FireTimer(1, targetTime)
		s.Verify(
			"timer.fire(): 1",
			"[info] timer fired",
		)
	}

	s.publish("/devices/somedev/controls/foo", prefix+"t", "somedev/foo")
	s.Verify(
		"tst -> /devices/somedev/controls/foo: [" + prefix + "t] (QoS 1, retained)",
	)
	s.VerifyUnordered(
		"timer.Stop(): 1",
		"new fake timer: 1, 500",
		"new fake timer: 2, 500",
	)

	ts = s.AdvanceTime(5 * 500 * time.Millisecond)
	s.FireTimer(1, ts)
	s.FireTimer(2, ts)
	s.Verify(
		"timer.fire(): 1",
		"timer.fire(): 2",
		"[info] timer fired",
		"[info] timer1 fired",
	)
}

func (s *RuleTimersSuite) TestTimers() {
	s.VerifyTimers("")
}

func (s *RuleTimersSuite) TestDirectTimers() {
	s.VerifyTimers("+")
}

func (s *RuleTimersSuite) TestShortTimers() {
	s.publish("/devices/somedev/controls/foo/meta/type", "text", "somedev/foo")
	s.publish("/devices/somedev/controls/foo", "short", "somedev/foo")

	s.Verify(
		"tst -> /devices/somedev/controls/foo/meta/type: [text] (QoS 1, retained)",
		"tst -> /devices/somedev/controls/foo: [short] (QoS 1, retained)",
		"new fake timer: 1, 1",
		"new fake timer: 2, 1",
		"new fake ticker: 3, 1",
		"new fake ticker: 4, 1",
		"new fake timer: 5, 1",
		"new fake timer: 6, 1",
		"new fake ticker: 7, 1",
		"new fake ticker: 8, 1",
	)
	s.VerifyEmpty()
}

func TestRuleTimersSuite(t *testing.T) {
	wbgo.RunSuites(t,
		new(RuleTimersSuite),
	)
}
