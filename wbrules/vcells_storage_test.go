package wbrules

import (
	"github.com/contactless/wbgo"
	"github.com/contactless/wbgo/testutils"
	"io/ioutil"
	"os"
	"testing"
)

type VirtualCellsStorageSuite struct {
	RuleSuiteBase
	tmpDir string
}

func (s *VirtualCellsStorageSuite) SetupFixture() {
	var err error
	s.tmpDir, err = ioutil.TempDir(os.TempDir(), "wbrulestest")
	if err != nil {
		s.FailNow("can't create temp directory")
	}
	wbgo.Debug.Printf("created temp dir %s", s.tmpDir)
}

func (s *VirtualCellsStorageSuite) TearDownFixture() {
	os.RemoveAll(s.tmpDir)
}

func (s *VirtualCellsStorageSuite) SetupTest() {
	s.VirtualCellsStorageFile = s.tmpDir + "/test-vcells.db"
	s.SetupSkippingDefs("testrules_vcells_storage.js")
}

func (s *VirtualCellsStorageSuite) TearDownTest() {
	s.RuleSuiteBase.TearDownTest()
}

func (s *VirtualCellsStorageSuite) TestStorage1() {
	// s.publish("/devices/test-trigger/controls/echo/on", "1", "test-trigger/echo")
	// s.Verify(
	// "tst -> /devices/test-trigger/controls/echo/on: [1] (QoS 1)",
	// "driver -> /devices/test-trigger/controls/echo: [1] (QoS 1)",
	// "[info] vdev false, true, false, foo",
	// )
	// s.VerifyEmpty()

	s.publish("/devices/test-trigger/controls/change1/on", "1", "test-trigger/change1",
		"test-vdev/cell1", "test-vdev/cell3", "test-vdev/cellText")
	s.Verify(
		"tst -> /devices/test-trigger/controls/change1/on: [1] (QoS 1)",
		"driver -> /devices/test-trigger/controls/change1: [1] (QoS 1)",
		"driver -> /devices/test-vdev/controls/cell1: [1] (QoS 1, retained)",
		"driver -> /devices/test-vdev/controls/cell3: [1] (QoS 1, retained)",
		"driver -> /devices/test-vdev/controls/cellText: [bar] (QoS 1, retained)",
	)
	s.VerifyEmpty()
}

func (s *VirtualCellsStorageSuite) TestStorage2() {
	s.publish("/devices/test-trigger/controls/echo/on", "1", "test-trigger/echo")
	s.Verify(
		"tst -> /devices/test-trigger/controls/echo/on: [1] (QoS 1)",
		"driver -> /devices/test-trigger/controls/echo: [1] (QoS 1)",
		"[info] vdev true, true, false, bar",
	)
	s.VerifyEmpty()
}

func TestVirtualCellsStorageSuite(t *testing.T) {
	s := new(VirtualCellsStorageSuite)
	s.SetupFixture()
	defer s.TearDownFixture()
	testutils.RunSuites(t, s)
}
