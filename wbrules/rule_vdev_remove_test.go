package wbrules

import (
	"testing"

	"github.com/wirenboard/wbgong/testutils"
)

var vdevRmCtlControls = []string{"remove", "redefine", "removeByMethod", "removeBad", "redefineByTimer"}

// driver messages on removal of a device with switch controls
func vdevRemovedMsgs(devId string, ctrls ...string) []any {
	prefix := "driver -> /devices/" + devId
	msgs := []any{
		prefix + "/meta/driver: [] (QoS 1, retained)",
		prefix + "/meta/name: [] (QoS 1, retained)",
		prefix + "/meta: [] (QoS 1, retained)",
	}
	for _, ctrl := range ctrls {
		msgs = append(msgs,
			"Unsubscribe -- driver: /devices/"+devId+"/controls/"+ctrl+"/on",
			prefix+"/controls/"+ctrl+"/meta/order: [] (QoS 1, retained)",
			prefix+"/controls/"+ctrl+"/meta/readonly: [] (QoS 1, retained)",
			prefix+"/controls/"+ctrl+"/meta/type: [] (QoS 1, retained)",
			prefix+"/controls/"+ctrl+"/meta: [] (QoS 1, retained)",
			prefix+"/controls/"+ctrl+": [] (QoS 1, retained)",
		)
	}
	return msgs
}

// driver messages on definition of 'vdev_rm' with the switch 'sw'
func vdevRmDefinedMsgs(title, swValue string) []any {
	return []any{
		"Subscribe -- driver: /devices/vdev_rm/controls/sw/on",
		"driver -> /devices/vdev_rm/meta/driver: [wbrules] (QoS 1, retained)",
		"driver -> /devices/vdev_rm/meta/name: [" + title + "] (QoS 1, retained)",
		"driver -> /devices/vdev_rm/meta: [{\"driver\":\"wbrules\",\"title\":{\"en\":\"" + title + "\"}}] (QoS 1, retained)",
		"driver -> /devices/vdev_rm/controls/sw/meta/type: [switch] (QoS 1, retained)",
		"driver -> /devices/vdev_rm/controls/sw/meta/readonly: [0] (QoS 1, retained)",
		"driver -> /devices/vdev_rm/controls/sw/meta/order: [1] (QoS 1, retained)",
		"driver -> /devices/vdev_rm/controls/sw/meta: [{\"order\":1,\"readonly\":false,\"type\":\"switch\"}] (QoS 1, retained)",
		"driver -> /devices/vdev_rm/controls/sw: [" + swValue + "] (QoS 1, retained)",
	}
}

type RuleVdevRemoveSuite struct {
	RuleSuiteBase
}

func (s *RuleVdevRemoveSuite) SetupTest() {
	s.SetupSkippingDefs("testrules_vdev_remove.js")
}

// press sets ctl/<ctrl> to 1 and verifies the expected messages
func (s *RuleVdevRemoveSuite) press(ctrl string, expected ...any) {
	s.publish("/devices/ctl/controls/"+ctrl+"/on", "1", "ctl/"+ctrl)
	s.VerifyUnordered(append([]any{
		"tst -> /devices/ctl/controls/" + ctrl + "/on: [1] (QoS 1)",
		"driver -> /devices/ctl/controls/" + ctrl + ": [1] (QoS 1, retained)",
	}, expected...)...)
}

func (s *RuleVdevRemoveSuite) TestRemoveRedefineRemove() {
	// removeVirtualDevice(): all retained topics are unpublished
	s.press("remove", append(vdevRemovedMsgs("vdev_rm", "sw"), "[info] exists after remove: false")...)

	// commands to the removed device are ignored
	s.publish("/devices/vdev_rm/controls/sw/on", "1")
	s.Verify("tst -> /devices/vdev_rm/controls/sw/on: [1] (QoS 1)")

	// the same id can be defined again without reloading the script
	s.press("redefine", append(vdevRmDefinedMsgs("VDevRm2", "1"), "[info] exists after redefine: true")...)

	// remove() method of the device object
	s.press("removeByMethod", append(vdevRemovedMsgs("vdev_rm", "sw"), "[info] exists after remove(): false")...)
}

func (s *RuleVdevRemoveSuite) TestRemoveErrors() {
	s.press("removeBad", append(vdevRemovedMsgs("vdev_rm", "sw"),
		"[info] removed vdev_rm",
		"[info] error: cannot remove device vdev_rm: Device with given ID doesn't exist",
		"[info] error: cannot remove device somedev: Device is external",
		"[info] error: cannot remove device nonexistent: Device with given ID doesn't exist",
		"[info] error: cannot remove device wbrules: it is the rule engine settings device",
	)...)
	s.EnsureGotErrors()
}

// script cleanup must not complain about a device removed manually
func (s *RuleVdevRemoveSuite) TestRemoveScriptAfterRemove() {
	s.press("remove", append(vdevRemovedMsgs("vdev_rm", "sw"), "[info] exists after remove: false")...)

	s.RemoveScript("testrules_vdev_remove.js")
	s.VerifyUnordered(append(vdevRemovedMsgs("ctl", vdevRmCtlControls...), "[removed] testrules_vdev_remove.js")...)
	s.VerifyEmpty()
	s.EnsureNoErrorsOrWarnings()
}

// a device defined again in the file scope is removed by the script cleanup once
func (s *RuleVdevRemoveSuite) TestRemoveScriptAfterRedefine() {
	s.press("remove", append(vdevRemovedMsgs("vdev_rm", "sw"), "[info] exists after remove: false")...)

	s.press("redefineByTimer", "new fake timer: 1, 1000")
	s.FireTimer(1, s.CurrentTime())
	s.VerifyUnordered(append(vdevRmDefinedMsgs("VDevRm3", "1"), "timer.fire(): 1", "[info] redefined by timer")...)

	s.RemoveScript("testrules_vdev_remove.js")
	expected := append(vdevRemovedMsgs("ctl", vdevRmCtlControls...), "[removed] testrules_vdev_remove.js")
	s.VerifyUnordered(append(expected, vdevRemovedMsgs("vdev_rm", "sw")...)...)
	s.VerifyEmpty()
	s.EnsureNoErrorsOrWarnings()
}

func TestRuleVdevRemoveSuite(t *testing.T) {
	testutils.RunSuites(t,
		new(RuleVdevRemoveSuite),
	)
}
