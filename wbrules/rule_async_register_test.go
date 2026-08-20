package wbrules

import (
	"testing"
	"time"

	"github.com/wirenboard/wbgong/testutils"
)

// Post-await registration: rules and timers created inside a promise job
// (after an await) must work and be attributed to their file.
type AsyncRegisterSuite struct {
	RuleSuiteBase
}

func (s *AsyncRegisterSuite) SetupTest() {
	s.SetupSkippingDefs("testrules_async_register.js")
}

func (s *AsyncRegisterSuite) TestPostAwaitRegistration() {
	s.publish("/devices/async_reg/controls/boot/on", "1", "async_reg/boot")
	s.VerifyUnordered(
		"tst -> /devices/async_reg/controls/boot/on: [1] (QoS 1)",
		"driver -> /devices/async_reg/controls/boot: [1] (QoS 1, retained)",
		"wbrules-log -> /wbrules/log/info: [late registration done] (QoS 1)",
		// the suite runs fake timers: creation proves the setTimeout from
		// inside the promise job reached this file's timer registry
		"new fake timer: 1, 1",
	)
	ts := s.AdvanceTime(time.Millisecond)
	s.FireTimer(1, ts)
	s.Verify(
		"timer.fire(): 1",
		"wbrules-log -> /wbrules/log/info: [late timer fired] (QoS 1)",
	)

	// the late-registered rule fires
	s.publish("/devices/async_reg/controls/probe/on", "1", "async_reg/probe")
	s.VerifyUnordered(
		"tst -> /devices/async_reg/controls/probe/on: [1] (QoS 1)",
		"driver -> /devices/async_reg/controls/probe: [1] (QoS 1, retained)",
		"wbrules-log -> /wbrules/log/info: [late rule fired: true] (QoS 1)",
	)

	// ...and is attributed to the file that created it
	entries, err := s.engine.ListSourceFiles()
	s.Ck("ListSourceFiles", err)
	found := false
	for _, entry := range entries {
		if entry.VirtualPath != "testrules_async_register.js" {
			continue
		}
		for _, rule := range entry.Rules {
			if rule.Name == "late_rule" {
				found = true
			}
		}
	}
	s.True(found, "late_rule must be attributed to its source file")
}

func TestAsyncRegisterSuite(t *testing.T) {
	testutils.RunSuites(t, new(AsyncRegisterSuite))
}
