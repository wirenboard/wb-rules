package wbrules

import (
	"github.com/contactless/wbgo"
	"github.com/contactless/wbgo/testutils"
	"io/ioutil"
	"os"
	"testing"
)

type PersistentStorageSuite struct {
	RuleSuiteBase
	tmpDir string
}

func (s *PersistentStorageSuite) SetupFixture() {
	var err error
	s.tmpDir, err = ioutil.TempDir(os.TempDir(), "wbrulestest")
	if err != nil {
		s.FailNow("can't create temp directory")
	}
	wbgo.Debug.Printf("created temp dir %s", s.tmpDir)
}

func (s *PersistentStorageSuite) TearDownFixture() {
	os.RemoveAll(s.tmpDir)
}

func (s *PersistentStorageSuite) SetupTest() {
	s.PersistentDBFile = s.tmpDir + "/test_persistent.db"
	s.SetupSkippingDefs("testrules_persistent.js")
}

func (s *PersistentStorageSuite) TearDownTest() {
	s.RuleSuiteBase.TearDownTest()
}

func (s *PersistentStorageSuite) TestPersistentStorage() {
	s.publish("/devices/vdev/controls/write/on", "1", "vdev/write")

	s.VerifyUnordered(
		"tst -> /devices/vdev/controls/write/on: [1] (QoS 1)",
		"driver -> /devices/vdev/controls/write: [1] (QoS 1, retained)",
		"[info] write objects 42, \"HelloWorld\", {\"name\":\"MyObj\",\"foo\":\"bar\",\"baz\":84}",
	)

}

// try to read from persistent storage
func (s *PersistentStorageSuite) TestPersistentStorage2() {
	s.publish("/devices/vdev/controls/read/on", "1", "vdev/read")
	s.VerifyUnordered(
		"tst -> /devices/vdev/controls/read/on: [1] (QoS 1)",
		"driver -> /devices/vdev/controls/read: [1] (QoS 1, retained)",
		"[info] read objects 42, \"HelloWorld\", {\"name\":\"MyObj\",\"foo\":\"bar\",\"baz\":84}",
	)

}

func TestPersistentStorageSuite(t *testing.T) {
	s := new(PersistentStorageSuite)
	s.SetupFixture()
	defer s.TearDownFixture()
	testutils.RunSuites(t, s)
}
