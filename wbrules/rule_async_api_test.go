package wbrules

import (
	"testing"
	"time"

	"github.com/wirenboard/wbgong"
	"github.com/wirenboard/wbgong/testutils"
)

// The promise-based library surface: spawn()/runShellCommand() return a
// promise settling on process exit (rejecting when the process cannot
// start), sleep() wraps a timer, nextMqtt() resolves with the next live
// message on a topic. All of them run their continuations inside promise
// jobs, so they double as realm-attribution coverage.
type AsyncApiSuite struct {
	RuleSuiteBase
}

func (s *AsyncApiSuite) SetupTest() {
	s.SetupSkippingDefs("testrules_async_api.js")
}

func (s *AsyncApiSuite) TestAwaitShellCommand() {
	s.publish("/devices/async_api/controls/shell/on", "1", "async_api/shell")
	s.SkipTill("wbrules-log -> /wbrules/log/info: [shell done: 0 [hello-async]] (QoS 1)")
}

func (s *AsyncApiSuite) TestSpawnRejectsWhenProcessCannotStart() {
	s.publish("/devices/async_api/controls/fail/on", "1", "async_api/fail")
	s.SkipTill("wbrules-log -> /wbrules/log/info: [spawn rejected: true] (QoS 1)")
}

func (s *AsyncApiSuite) TestSleep() {
	s.publish("/devices/async_api/controls/wait/on", "1", "async_api/wait")
	s.SkipTill("new fake timer: 1, 50")
	ts := s.AdvanceTime(50 * time.Millisecond)
	s.FireTimer(1, ts)
	s.SkipTill("wbrules-log -> /wbrules/log/info: [sleep done] (QoS 1)")
}

func (s *AsyncApiSuite) TestNextMqtt() {
	s.publish("/devices/async_api/controls/mqtt/on", "1", "async_api/mqtt")
	s.SkipTill("driver -> /devices/async_api/controls/mqtt: [1] (QoS 1, retained)")
	// a live (non-retained) message: nextMqtt deliberately ignores the
	// broker's retained cache - "next" means new
	s.client.Publish(wbgong.MQTTMessage{Topic: "/test/async/next", Payload: "42", QoS: 1})
	s.SkipTill("wbrules-log -> /wbrules/log/info: [next mqtt: /test/async/next = 42] (QoS 1)")
}

func TestAsyncApiSuite(t *testing.T) {
	testutils.RunSuites(t, new(AsyncApiSuite))
}

func (s *AsyncApiSuite) TestChanged() {
	s.publish("/devices/async_api/controls/track/on", "1", "async_api/track")
	s.SkipTill("driver -> /devices/async_api/controls/track: [1] (QoS 1, retained)")
	// the rule is now awaiting changed('async_api/level'); an external
	// write resolves it with the converted cell value
	s.publish("/devices/async_api/controls/level/on", "42", "async_api/level")
	s.SkipTill("wbrules-log -> /wbrules/log/info: [changed resolved: 42] (QoS 1)")
}

// The README's motion-detector rewrite: a single linear loop replaces
// timer-id bookkeeping. Exercises changed() with and without timeout,
// timeout cancellation, and proves a won race leaves no phantom
// "async rule error" behind.
func (s *AsyncApiSuite) TestMotionDetectorPattern() {
	s.publish("/devices/async_api/controls/motion/on", "1", "async_api/motion", "async_api/light")
	s.SkipTill("driver -> /devices/async_api/controls/light: [1] (QoS 1, retained)")

	// motion clears: the off-delay timer arms
	s.publish("/devices/async_api/controls/motion/on", "0", "async_api/motion")
	s.SkipTill("new fake timer: 1, 10000")

	// motion returns in time: the timer must be cancelled, light stays on
	s.publish("/devices/async_api/controls/motion/on", "1", "async_api/motion")
	s.SkipTill("timer.Stop(): 1")

	// clears again, and this time the delay runs out
	s.publish("/devices/async_api/controls/motion/on", "0", "async_api/motion")
	s.SkipTill("new fake timer: 2, 10000")
	ts := s.AdvanceTime(10 * time.Second)
	s.FireTimer(2, ts)
	// the timer-driven light write happens outside any publish - its
	// control-change notification must be consumed or the engine wedges
	s.expectControlChange("async_api/light")
	s.SkipTill("driver -> /devices/async_api/controls/light: [0] (QoS 1, retained)")
}
