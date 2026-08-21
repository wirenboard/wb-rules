package wbrules

import (
	"testing"

	"github.com/wirenboard/wbgong/testutils"
)

type VdevRemoveSuite struct {
	RuleSuiteBase
}

func (s *VdevRemoveSuite) SetupTest() {
	s.SetupSkippingDefs("testrules_vdev_remove.js")
}

func (s *VdevRemoveSuite) verifyVdevRmRemoved(trigger string) {
	s.VerifyUnordered(
		"tst -> /devices/ctl/controls/"+trigger+"/on: [1] (QoS 1)",
		"driver -> /devices/ctl/controls/"+trigger+": [1] (QoS 1, retained)",
		"Unsubscribe -- driver: /devices/vdev_rm/controls/sw/on",
		"driver -> /devices/vdev_rm/controls/sw/meta/order: [] (QoS 1, retained)",
		"driver -> /devices/vdev_rm/controls/sw/meta/readonly: [] (QoS 1, retained)",
		"driver -> /devices/vdev_rm/controls/sw/meta/type: [] (QoS 1, retained)",
		"driver -> /devices/vdev_rm/controls/sw/meta: [] (QoS 1, retained)",
		"driver -> /devices/vdev_rm/controls/sw: [] (QoS 1, retained)",
		"driver -> /devices/vdev_rm/meta/driver: [] (QoS 1, retained)",
		"driver -> /devices/vdev_rm/meta/name: [] (QoS 1, retained)",
		"driver -> /devices/vdev_rm/meta: [] (QoS 1, retained)",
		"[info] exists after "+map[string]string{
			"remove":         "remove",
			"removeByMethod": "remove()",
		}[trigger]+": false",
	)
}

func (s *VdevRemoveSuite) TestRemoveRedefineRemove() {
	// removeVirtualDevice() from a rule: all retained topics are unpublished
	s.publish("/devices/ctl/controls/remove/on", "1", "ctl/remove")
	s.verifyVdevRmRemoved("remove")

	// commands to the removed device are ignored
	s.publish("/devices/vdev_rm/controls/sw/on", "1")
	s.Verify("tst -> /devices/vdev_rm/controls/sw/on: [1] (QoS 1)")

	// the same id can be defined again without reloading the script
	s.publish("/devices/ctl/controls/redefine/on", "1", "ctl/redefine")
	s.VerifyUnordered(
		"tst -> /devices/ctl/controls/redefine/on: [1] (QoS 1)",
		"driver -> /devices/ctl/controls/redefine: [1] (QoS 1, retained)",
		"Subscribe -- driver: /devices/vdev_rm/controls/sw/on",
		"driver -> /devices/vdev_rm/meta/driver: [wbrules] (QoS 1, retained)",
		"driver -> /devices/vdev_rm/meta/name: [VDevRm2] (QoS 1, retained)",
		"driver -> /devices/vdev_rm/meta: [{\"driver\":\"wbrules\",\"title\":{\"en\":\"VDevRm2\"}}] (QoS 1, retained)",
		"driver -> /devices/vdev_rm/controls/sw/meta/type: [switch] (QoS 1, retained)",
		"driver -> /devices/vdev_rm/controls/sw/meta/readonly: [0] (QoS 1, retained)",
		"driver -> /devices/vdev_rm/controls/sw/meta/order: [1] (QoS 1, retained)",
		"driver -> /devices/vdev_rm/controls/sw/meta: [{\"order\":1,\"readonly\":false,\"type\":\"switch\"}] (QoS 1, retained)",
		"driver -> /devices/vdev_rm/controls/sw: [1] (QoS 1, retained)",
		"[info] exists after redefine: true",
	)

	// remove() method of the device object
	s.publish("/devices/ctl/controls/removeByMethod/on", "1", "ctl/removeByMethod")
	s.verifyVdevRmRemoved("removeByMethod")
}

func (s *VdevRemoveSuite) TestRemoveErrors() {
	s.publish("/devices/ctl/controls/removeBad/on", "1", "ctl/removeBad")
	s.VerifyUnordered(
		"tst -> /devices/ctl/controls/removeBad/on: [1] (QoS 1)",
		"driver -> /devices/ctl/controls/removeBad: [1] (QoS 1, retained)",
		"[info] error: cannot remove device somedev: Device is external",
		"[info] error: cannot remove device nonexistent: Device with given ID doesn't exist",
		"[info] error: cannot remove device wbrules: it is the rule engine settings device",
	)
	s.EnsureGotErrors()
}

func TestVdevRemoveSuite(t *testing.T) {
	testutils.RunSuites(t,
		new(VdevRemoveSuite),
	)
}
