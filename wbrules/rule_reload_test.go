package wbrules

import (
	"fmt"
	"github.com/contactless/wbgo/testutils"
	"testing"
)

type RuleReloadSuite struct {
	RuleSuiteBase
}

func (s *RuleReloadSuite) SetupTest() {
	s.SetupSkippingDefs("testrules_reload_1.js", "testrules_reload_2.js")
	s.Verify(
		"[info] detRun",
		"[info] detectRun: (no cell) (s=false, a=10)",
		"[info] detectRun1: (no cell) (s=false, a=10)",
	)
}

func (s *RuleReloadSuite) TestReload() {
	s.publish("/devices/vdev/controls/someCell/on", "1", "vdev/someCell")
	s.Verify(
		"tst -> /devices/vdev/controls/someCell/on: [1] (QoS 1)",
		"driver -> /devices/vdev/controls/someCell: [1] (QoS 1, retained)",
		"[info] detRun",
		"[info] detectRun: vdev/someCell (s=true, a=10)",
		"[info] detectRun1: vdev/someCell (s=true, a=10)",
		"[info] rule1: vdev/someCell=true",
		"[info] rule2: vdev/someCell=true",
	)

	s.publish("/devices/vdev/controls/anotherCell/on", "17", "vdev/anotherCell")
	s.Verify(
		"tst -> /devices/vdev/controls/anotherCell/on: [17] (QoS 1)",
		"driver -> /devices/vdev/controls/anotherCell: [17] (QoS 1, retained)",
		"[info] detRun",
		"[info] detectRun: vdev/anotherCell (s=true, a=17)",
		"[info] detectRun1: vdev/anotherCell (s=true, a=17)",
		"[info] rule3: vdev/anotherCell=17",
	)

	s.ReplaceScript("testrules_reload_2.js", "testrules_reload_2_changed.js")
	s.Verify(
		// devices are removed when the older version if the
		// script is unloaded
		"Unsubscribe -- driver: /devices/vdev/controls/anotherCell/on",
		"Unsubscribe -- driver: /devices/vdev/controls/someCell/on",
		// vdev1 is not redefined after reload, but must be still
		// removed
		"Unsubscribe -- driver: /devices/vdev1/controls/qqq/on",
		// device redefinition begins
		"driver -> /devices/vdev/meta/name: [VDev] (QoS 1, retained)",
		"driver -> /devices/vdev/controls/someCell/meta/type: [switch] (QoS 1, retained)",
		"driver -> /devices/vdev/controls/someCell/meta/order: [1] (QoS 1, retained)",
		// value '1' of the switch from the retained message
		"driver -> /devices/vdev/controls/someCell: [1] (QoS 1, retained)",
		"Subscribe -- driver: /devices/vdev/controls/someCell/on",
		// rules are run after reload
		"[info] detRun",
		"[info] detectRun: (no cell) (s=true)",
		// change notification for the client-side script editor
		"driver -> /wbrules/updates/changed: [testrules_reload_2.js] (QoS 1)",
	)

	// this one must be ignored because anotherCell is no longer there
	// after the device is redefined
	s.publish("/devices/vdev/controls/anotherCell/on", "11")
	s.publish("/devices/vdev/controls/someCell/on", "0", "vdev/someCell")

	s.Verify(
		"tst -> /devices/vdev/controls/anotherCell/on: [11] (QoS 1)",
		"tst -> /devices/vdev/controls/someCell/on: [0] (QoS 1)",
		"driver -> /devices/vdev/controls/someCell: [0] (QoS 1, retained)",
		"[info] detRun",
		"[info] detectRun: vdev/someCell (s=false)",
		"[info] rule1: vdev/someCell=false",
		// rule2 is gone, rule3 is gone together with its anotherCell
	)

	s.publish("/devices/vdev/controls/someCell/on", "1", "vdev/someCell")
	s.Verify(
		"tst -> /devices/vdev/controls/someCell/on: [1] (QoS 1)",
		"driver -> /devices/vdev/controls/someCell: [1] (QoS 1, retained)",
		"[info] detRun",
		"[info] detectRun: vdev/someCell (s=true)",
		"[info] rule1: vdev/someCell=true",
	)

	// TBD: stop any timers started while evaluating the script.
	// This will require extra care because cleanup procedure
	// for the timer must be revoked once the timer is stopped.
}

func (s *RuleReloadSuite) TestRemoveScript() {
	s.RemoveScript("testrules_reload_2.js")
	s.Verify(
		// devices are removed
		"Unsubscribe -- driver: /devices/vdev/controls/anotherCell/on",
		"Unsubscribe -- driver: /devices/vdev/controls/someCell/on",
		"Unsubscribe -- driver: /devices/vdev1/controls/qqq/on",
		// rules are run after removal
		"[info] detRun",
		// removal notification for the client-side script editor
		"driver -> /wbrules/updates/removed: [testrules_reload_2.js] (QoS 1)",
	)

	// both ignored (cells are no longer there)
	s.publish("/devices/vdev/controls/anotherCell/on", "11")
	s.publish("/devices/vdev/controls/someCell/on", "0")

	s.Verify(
		"tst -> /devices/vdev/controls/anotherCell/on: [11] (QoS 1)",
		"tst -> /devices/vdev/controls/someCell/on: [0] (QoS 1)",
	)

	// vdev0 is intact because it's from testrules_reload_1.js
	s.publish("/devices/vdev0/controls/someCell/on", "1", "vdev0/someCell")
	s.Verify(
		"tst -> /devices/vdev0/controls/someCell/on: [1] (QoS 1)",
		"driver -> /devices/vdev0/controls/someCell: [1] (QoS 1, retained)",
		"[info] detRun",
	)
}

func (s *RuleReloadSuite) TestNoReloading() {
	s.engine.EvalScript("testrules_reload_1_loaded = false;")
	// no actual replacement should happen here
	s.ReplaceScript("testrules_reload_1.js", "testrules_reload_1.js")
	s.Ck("unexpected reload", s.engine.EvalScript(
		"if(testrules_reload_1_loaded) throw new Error('unexpected reload')"))
	s.VerifyEmpty()
}

func (s *RuleReloadSuite) verifyReloadCount(n int) {
	script := fmt.Sprintf(
		`if(testrules_reload_2_n != %d)
                   throw new Error(
                     "bad reload count: " + testrules_reload_2_n + " instead of " + %d)`,
		n, n)
	s.Ck("bad reload count", s.engine.EvalScript(script))
}

func (s *RuleReloadSuite) TestWriteScript() {
	for n := 1; n < 3; n++ {
		// OverwriteScript() calls LiveWriteScript() which causes
		// script to reload every time
		s.Ck("OverwriteScript()",
			s.OverwriteScript("testrules_reload_2.js", "testrules_reload_2_changed.js"))
		s.verifyReloadCount(n)
	}

	// let's load script from a new directory
	s.Ck("OverwriteScript()",
		s.OverwriteScript("subdir/testrules_reload_42.js", "testrules_reload_2_changed.js"))
	s.verifyReloadCount(3)

	// the following ReplaceScript() which calls LiveLoadFile()
	// has now effect because the new content is already registered
	s.ReplaceScript("testrules_reload_2.js", "testrules_reload_2_changed.js")
	s.verifyReloadCount(3)
}

func TestRuleReloadSuite(t *testing.T) {
	testutils.RunSuites(t,
		new(RuleReloadSuite),
	)
}
