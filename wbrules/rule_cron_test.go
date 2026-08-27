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

type RuleCronDisableSuite struct {
	RuleSuiteBase
}

func (s *RuleCronDisableSuite) SetupTest() {
	s.SetupSkippingDefs("testrules_cron_disable.js")
}

// TestCronRuleStaysDisabledAfterReload is a regression test for a bug where a
// cron rule turned off via disableRule() came back to life on the next engine
// reload (e.g. when another script was saved). Refresh() rebuilds the cron from
// scratch for every rule, and a disabled rule must not be re-armed.
func (s *RuleCronDisableSuite) TestCronRuleStaysDisabledAfterReload() {
	s.WaitFor(func() bool {
		c := make(chan bool)
		s.engine.CallSync(func() {
			c <- s.cron != nil && s.cron.started
		})
		return <-c
	})

	// the rule is enabled by default and fires
	s.cron.invokeEntries("@hourly")
	s.Verify(
		"[info] cron rule fired",
	)

	// disable it via disableRule()
	s.publish("/devices/cron_switch/controls/enabled/on", "0", "cron_switch/enabled")
	s.SkipTill("[info] cron rule disabled")

	// disabled rule must not fire
	s.cron.invokeEntries("@hourly")
	s.VerifyEmpty()

	// saving another script triggers an engine reload (Refresh -> setupCron),
	// which rebuilds the cron for all rules; the disabled rule must stay disabled
	s.Ck("LiveLoadScript()", s.LiveLoadScript("testrules_cron_disable_reload.js"))
	s.VerifyUnordered(
		"[info] extra script loaded",
		"[changed] testrules_cron_disable_reload.js",
	)

	s.cron.invokeEntries("@hourly")
	s.VerifyEmpty()

	// re-enabling it via enableRule() brings it back
	s.publish("/devices/cron_switch/controls/enabled/on", "1", "cron_switch/enabled")
	s.SkipTill("[info] cron rule enabled")

	s.cron.invokeEntries("@hourly")
	// SkipTill, not Verify: the driver's echo of the enabled/on publish
	// (driver client) can trail the rule log line (wbrules-log client)
	// under load and still be queued at this point
	s.SkipTill("[info] cron rule fired")
}

func TestRuleCronSuite(t *testing.T) {
	testutils.RunSuites(t,
		new(RuleCronSuite),
		new(RuleCronDisableSuite),
	)
}
