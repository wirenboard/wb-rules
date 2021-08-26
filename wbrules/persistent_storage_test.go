package wbrules

import (
	"io/ioutil"
	"os"
	"testing"

	"github.com/wirenboard/wbgong"
	"github.com/wirenboard/wbgong/testutils"
)

type PersistentStorageSuite struct {
	RuleSuiteBase
	tmpDir string
}

func (s *PersistentStorageSuite) SetupFixture() {
	var err error

	// we need to create separated temp directory because persistent DB file
	// should be keeped between tests
	s.tmpDir, err = ioutil.TempDir(os.TempDir(), "wbrulestest")
	if err != nil {
		s.FailNow("can't create temp directory")
	}
	wbgong.Debug.Printf("created temp dir %s", s.tmpDir)
}

func (s *PersistentStorageSuite) TearDownFixture() {
	os.RemoveAll(s.tmpDir)
}

func (s *PersistentStorageSuite) SetupTest() {
	s.PersistentDBFile = s.tmpDir + "/test_persistent.db"
	s.VdevStorageFile = s.tmpDir + "/test-vdev.db"
	s.SetupSkippingDefs()
	s.LiveLoadScriptToDir("testrules_persistent.js", s.tmpDir)
	s.SkipTill("[info] loaded file 1")
	s.LiveLoadScriptToDir("testrules_persistent_2.js", s.tmpDir)
	s.SkipTill("[info] loaded file 2")
}

func (s *PersistentStorageSuite) TearDownTest() {
	s.RuleSuiteBase.TearDownTest()
}

func (s *PersistentStorageSuite) TestPersistentStorage() {
	s.publish("/devices/vdev/controls/write/on", "1", "vdev/write")

	s.VerifyUnordered(
		"tst -> /devices/vdev/controls/write/on: [1] (QoS 1)",
		"driver -> /devices/vdev/controls/write: [1] (QoS 1, retained)",
		"[info] pure object is not created",
		"[info] pure subobject is not created",
		"[info] write objects 42, \"HelloWorld\", {\"name\":\"MyObj\",\"foo\":\"bar\",\"baz\":84,\"sub\":{\"hello\":\"world\"}}",
	)

}

// try to read from persistent storage
func (s *PersistentStorageSuite) TestPersistentStorage2() {
	s.publish("/devices/vdev/controls/read/on", "1", "vdev/read")
	s.VerifyUnordered(
		"tst -> /devices/vdev/controls/read/on: [1] (QoS 1)",
		"driver -> /devices/vdev/controls/read: [1] (QoS 1, retained)",
		"[info] read objects 42, \"HelloWorld\", {\"name\":\"MyObj\",\"foo\":\"bar\",\"baz\":84,\"sub\":{\"hello\":\"world\"}}",
		"[info] read objects 42, \"HelloWorld\", {\"name\":\"MyObj\",\"foo\":\"bar\",\"baz\":84,\"sub\":{\"hello\":\"earth\"}}",
	)

}

// test local storages in different files
func (s *PersistentStorageSuite) TestLocalPersistentStorage() {
	// write values
	s.publish("/devices/vdev/controls/localWrite1/on", "1", "vdev/localWrite1")
	s.SkipTill("[info] file1: write to local PS")

	s.publish("/devices/vdev/controls/localWrite2/on", "1", "vdev/localWrite2")
	s.SkipTill("[info] file2: write to local PS")

	// now read values
	s.publish("/devices/vdev/controls/localRead1/on", "1", "vdev/localRead1")
	s.SkipTill("[info] file1: read objects \"hello_from_1\", undefined")

	s.publish("/devices/vdev/controls/localRead2/on", "1", "vdev/localRead2")
	s.SkipTill("[info] file2: read objects undefined, \"hello_from_2\"")
}

func (s *PersistentStorageSuite) TestLocalPersistentStorage2() {
	// now read values
	s.publish("/devices/vdev/controls/localRead1/on", "1", "vdev/localRead1")
	s.SkipTill("[info] file1: read objects \"hello_from_1\", undefined")

	s.publish("/devices/vdev/controls/localRead2/on", "1", "vdev/localRead2")
	s.SkipTill("[info] file2: read objects undefined, \"hello_from_2\"")
}

func TestPersistentStorageSuite(t *testing.T) {
	s := new(PersistentStorageSuite)
	s.SetupFixture()
	defer s.TearDownFixture()
	testutils.RunSuites(t, s)
}
