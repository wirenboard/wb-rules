package wbrules

import (
	"fmt"
	"github.com/contactless/wbgo/testutils"
	"testing"
)

type LocalVirtualDeviceTestSuite struct {
	RuleSuiteBase
}

func (s *LocalVirtualDeviceTestSuite) SetupTest() {
	s.SetupSkippingDefs("testrules_local_vdev.js")
}

func (s *LocalVirtualDeviceTestSuite) TestLocalVirtualDevice() {

	// get local device ID first
	s.publish("/devices/test/controls/getid/on", "1", "test/getid")
	localDeviceId := ""
	s.Verify(
		"tst -> /devices/test/controls/getid/on: [1] (QoS 1)",
		"driver -> /devices/test/controls/getid: [1] (QoS 1)",
		testutils.RegexpCaptureMatcher("driver -> /wbrules/log/info: \\[device id: '(.*)'\\] \\(QoS 1\\)",
			func(matches []string) bool {
				localDeviceId = matches[1]
				return true
			}),
	)

	s.publish("/devices/test/controls/local/on", "1", "test/local", fmt.Sprintf("%s/myCell", localDeviceId))

	s.Verify(
		"tst -> /devices/test/controls/local/on: [1] (QoS 1)",
		"driver -> /devices/test/controls/local: [1] (QoS 1)",
		"[info] triggered global device",
		fmt.Sprintf("driver -> /devices/%s/controls/myCell/on: [1] (QoS 0)", localDeviceId),
		fmt.Sprintf("driver -> /devices/%s/controls/myCell: [1] (QoS 1)", localDeviceId),
		"[info] triggered local device",
	)
}

func TestLocalVirtualDevice(t *testing.T) {
	testutils.RunSuites(t,
		new(LocalVirtualDeviceTestSuite),
	)
}
