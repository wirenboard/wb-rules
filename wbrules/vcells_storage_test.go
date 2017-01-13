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
	s.publish("/devices/test-trigger/controls/echo/on", "1", "test-trigger/echo")

	s.VerifyUnordered(
		"[info] vdev 0, 1, 0, foo",
	)
}

func TestVirtualCellsStorageSuite(t *testing.T) {
	s := new(VirtualCellsStorageSuite)
	s.SetupFixture()
	defer s.TearDownFixture()
	testutils.RunSuites(t, s)
}
