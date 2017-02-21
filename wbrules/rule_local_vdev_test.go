package wbrules

import (
	"github.com/contactless/wbgo/testutils"
	"regexp"
	"testing"
)

type LocalVirtualDeviceTestSuite struct {
	RuleSuiteBase
}

func (s *LocalVirtualDeviceTestSuite) SetupTest() {
	s.SetupSkippingDefs("testrules_local_vdev.js")
}

func (s *LocalVirtualDeviceTestSuite) TestLocalVirtualDevice() {
	s.publish("/devices/test/controls/local/on", "1", "test/local", "")

	s.Verify(
		"tst -> /devices/test/controls/local/on: [1] (QoS 1)",
		"driver -> /devices/test/controls/local: [1] (QoS 1)",
		"[info] triggered global device",
		regexp.MustCompile("driver -> /devices/local_.*_test/controls/myCell/on: \\[1\\] \\(QoS 0\\)"),
		regexp.MustCompile("driver -> /devices/local_.*_test/controls/myCell: \\[1\\] \\(QoS 1\\)"),
		"[info] triggered local device",
	)

}

func TestLocalVirtualDevice(t *testing.T) {
	testutils.RunSuites(t,
		new(LocalVirtualDeviceTestSuite),
	)
}
