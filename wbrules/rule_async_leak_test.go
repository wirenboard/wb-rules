package wbrules

import (
	"fmt"
	"testing"
	"time"

	"github.com/wirenboard/wbgong/testutils"
)

// Leak and stale-object hunting for the async library at the engine
// level: parked waiters must stay bounded, timed-out waiters must be
// reclaimed, and a file reload must sweep every piece of async state
// the file created (anonymous changed() rules, MQTT trackers, timers).
type AsyncLeakSuite struct {
	RuleSuiteBase
}

func (s *AsyncLeakSuite) SetupTest() {
	s.SetupSkippingDefs("testrules_async_leak.js")
}

// engineCounts reads engine registries on the engine loop (the maps are
// owned by it; reading from the test goroutine would race).
func (s *AsyncLeakSuite) engineCounts() (rules, trackers, timers int) {
	// CallSync only enqueues the thunk (it does not wait) - synchronize
	// with a channel so the counts are read on the engine loop AND
	// visible here before returning
	done := make(chan struct{})
	s.engine.CallSync(func() {
		rules = len(s.engine.ruleMap)
		for _, m := range s.engine.tracks {
			trackers += len(m)
		}
		timers = len(s.engine.timers)
		close(done)
	})
	<-done
	return
}

// drainControlEcho deterministically consumes the driver's trailing control
// echo. A test that ends with publish(x/on) + SkipTill(<rule log>) can leave
// the driver's own retained echo of x still in flight when TearDown calls
// VerifyEmpty - the echo and the rule log travel on different connections,
// so their recorder order is not fixed. One more driver-side write (control
// a, unwatched by any named rule) is FIFO with the earlier echo on the
// driver connection: skipping till its own echo guarantees everything the
// driver published before it - the racing echo included - was consumed.
func (s *AsyncLeakSuite) drainControlEcho(value string) {
	s.publish("/devices/async_leak/controls/a/on", value, "async_leak/a")
	s.SkipTill(fmt.Sprintf("driver -> /devices/async_leak/controls/a: [%s] (QoS 1, retained)", value))
}

func (s *AsyncLeakSuite) TestParkedWaitersBoundedSubscriptions() {
	rules0, trackers0, _ := s.engineCounts()
	for i := 1; i <= 3; i++ {
		val := fmt.Sprintf("%d", i%2)
		s.publish("/devices/async_leak/controls/park/on", val, "async_leak/park")
		s.SkipTill(fmt.Sprintf("wbrules-log -> /wbrules/log/info: [parked; waiters: %d] (QoS 1)", i*20))
	}
	rules1, trackers1, _ := s.engineCounts()
	// 30 changed() on one control -> ONE anonymous rule; 30 nextMqtt on
	// one topic -> ONE tracker. Anything more is a leak.
	s.Equal(rules0+1, rules1, "changed() must reuse one rule per control")
	s.Equal(trackers0+1, trackers1, "nextMqtt() must reuse one tracker per topic")
	s.drainControlEcho("7")
}

func (s *AsyncLeakSuite) TestTimedOutWaitersReclaimed() {
	s.publish("/devices/async_leak/controls/churn/on", "1", "async_leak/churn")
	s.SkipTill("wbrules-log -> /wbrules/log/info: [churn armed; waiters: 10] (QoS 1)")
	// fixture load armed fake timer 1 (the hour sleep); the churn armed
	// 2..11. Fire the timeouts: every rejection is caught in the rule.
	// FireTimer returns once the timer goroutine took the event; the
	// callback is queued on the engine loop asynchronously, so wait for
	// each rejection to be handled before asking for the report - otherwise
	// the report races the last timeout and sees a waiter still parked.
	for id := 2; id <= 11; id++ {
		ts := s.AdvanceTime(50 * time.Millisecond)
		s.FireTimer(uint64(id), ts)
		s.SkipTill("wbrules-log -> /wbrules/log/info: [churn timeout] (QoS 1)")
	}
	s.publish("/devices/async_leak/controls/report/on", "1", "async_leak/report")
	s.SkipTill("wbrules-log -> /wbrules/log/info: [report waiters: 0] (QoS 1)")
	s.drainControlEcho("8")
}

func (s *AsyncLeakSuite) TestAsyncStateSweptOnReload() {
	s.publish("/devices/async_leak/controls/park/on", "1", "async_leak/park")
	s.SkipTill("wbrules-log -> /wbrules/log/info: [parked; waiters: 20] (QoS 1)")

	s.ReplaceScript("testrules_async_leak.js", "testrules_async_leak_empty.js")
	s.publish("/devices/async_leak/controls/report/on", "1", "async_leak/report")
	s.SkipTill("wbrules-log -> /wbrules/log/info: [probe alive] (QoS 1)")

	rules, trackers, timers := s.engineCounts()
	// the replacement file defines exactly one rule; every anonymous
	// changed() rule, nextMqtt tracker, and the parked hour-sleep timer
	// from the old file must be gone
	s.Equal(1, rules, "stale rules survived the reload")
	s.Equal(0, trackers, "stale MQTT trackers survived the reload")
	s.Equal(0, timers, "stale timers survived the reload")
	s.drainControlEcho("9")
}

func TestAsyncLeakSuite(t *testing.T) {
	testutils.RunSuites(t, new(AsyncLeakSuite))
}
