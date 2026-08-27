package wbrules

import (
	"testing"

	"github.com/wirenboard/wbgong/testutils"
)

// Top-level await: a rule file may await directly at the top level. The
// wrapper is async, so the file keeps running past the await.
type TopLevelAwaitSuite struct {
	RuleSuiteBase
}

func (s *TopLevelAwaitSuite) SetupTest() {
	s.SetupSkippingDefs("testrules_tla.js")
}

func (s *TopLevelAwaitSuite) TestTopLevelAwaitRuns() {
	// tla_rule is defined after the top-level await and writes base+1 (=101)
	// using the awaited value; firing it proves the file continued past the
	// await and that post-await defineRule works.
	s.publish("/devices/tla/controls/trigger/on", "1", "tla/trigger", "tla/out")
	s.SkipTill("driver -> /devices/tla/controls/out: [101] (QoS 1, retained)")
}

func TestTopLevelAwaitSuite(t *testing.T) {
	testutils.RunSuites(t, new(TopLevelAwaitSuite))
}

// A file that throws at the top level with no await must still fail loading
// synchronously (its promise is rejected before any await), the same as the
// old synchronous wrapper - not silently defer.
type TopLevelThrowSuite struct {
	RuleSuiteBase
}

func (s *TopLevelThrowSuite) SetupTest() {
	s.SetupSkippingDefs()
}

func (s *TopLevelThrowSuite) TestTopLevelThrowIsLoadError() {
	// the throw happens before any await, so it must come back as a
	// synchronous load error (not a deferred "async rule error")
	err := s.LiveLoadScript("testrules_tla_throw.js")
	s.Error(err, "a top-level throw with no await must fail the load synchronously")
	s.Contains(err.Error(), "tla load boom")
	s.EnsureGotErrors()
	// draining up to the reload update consumes the single error-log line
	s.SkipTill("[changed] testrules_tla_throw.js")
}

func TestTopLevelThrowSuite(t *testing.T) {
	testutils.RunSuites(t, new(TopLevelThrowSuite))
}
