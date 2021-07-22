package wbrules

import (
	"github.com/wirenboard/wbgong/testutils"
	"testing"
)

type RuleCronSuite struct {
	RuleSuiteBase
}

func (s *RuleCronSuite) SetupTest() {
	s.SetupSkippingDefs("testrules_cron.js")
}

func (s *RuleCronSuite) TestCron() {
	s.WaitFor(func() bool {
		c := make(chan bool)
		s.engine.CallSync(func() {
			c <- s.cron != nil && s.cron.started
		})
		return <-c
	})

	s.cron.invokeEntries("@hourly")
	s.cron.invokeEntries("@hourly")
	s.cron.invokeEntries("@daily")
	s.cron.invokeEntries("@hourly")

	s.Verify(
		"[info] @hourly rule fired",
		"[info] @hourly rule fired",
		"[info] @daily rule fired",
		"[info] @hourly rule fired",
	)

	// the new script contains rules with same names as in
	// testrules_cron.js that should override the previous rules
	s.ReplaceScript("testrules_cron.js", "testrules_cron_changed.js")
	s.Verify(
		"[changed] testrules_cron.js",
	)

	s.cron.invokeEntries("@hourly")
	s.cron.invokeEntries("@hourly")
	s.cron.invokeEntries("@daily")
	s.cron.invokeEntries("@hourly")

	s.Verify(
		"[info] @hourly rule fired (new)",
		"[info] @hourly rule fired (new)",
		"[info] @daily rule fired (new)",
		"[info] @hourly rule fired (new)",
	)
}

func TestRuleCronSuite(t *testing.T) {
	testutils.RunSuites(t,
		new(RuleCronSuite),
	)
}
