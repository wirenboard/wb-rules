package wbrules

import (
	"os"
	"testing"

	"github.com/wirenboard/wbgong/testutils"
)

// A local PersistentStorage opened inside a promise job (after an await)
// must land in the same per-file bucket as one opened synchronously:
// the name is expanded with a hash of the calling file, which a promise
// job can only resolve through the realm-local builtin (the shared
// realm's closure would see no current file and skip the hash).
type AsyncPersistSuite struct {
	RuleSuiteBase
	tmpDir string
}

func (s *AsyncPersistSuite) SetupTest() {
	var err error
	s.tmpDir, err = os.MkdirTemp("", "wbrulestest")
	if err != nil {
		s.FailNow("can't create temp directory")
	}
	s.PersistentDBFile = s.tmpDir + "/async_persist.db"
	s.SetupSkippingDefs("testrules_async_persist.js")
}

func (s *AsyncPersistSuite) TearDownTest() {
	s.RuleSuiteBase.TearDownTest()
	os.RemoveAll(s.tmpDir)
}

func (s *AsyncPersistSuite) TestPostAwaitLocalStorageAttribution() {
	s.publish("/devices/async_ps/controls/probe/on", "1", "async_ps/probe")
	// "from-sync" through the post-await handle is the proof: the value
	// was written into the per-file bucket by the sync handle, so the
	// promise-job open resolved to the same (hashed) storage name
	s.SkipTill("wbrules-log -> /wbrules/log/info: [async ps sees: from-sync] (QoS 1)")
	s.SkipTill("wbrules-log -> /wbrules/log/info: [sync handle sees: from-async] (QoS 1)")
}

// A non-serializable value (an object with a reference cycle) must throw
// to the writing rule instead of storing a bogus value or reporting the
// failure through an unrelated log channel.
func (s *AsyncPersistSuite) TestCyclicValueThrowsToWriter() {
	s.publish("/devices/async_ps/controls/cyc/on", "1", "async_ps/cyc")
	s.SkipTill("wbrules-log -> /wbrules/log/info: [cyclic write rejected: true] (QoS 1)")
}

func TestAsyncPersistSuite(t *testing.T) {
	testutils.RunSuites(t, new(AsyncPersistSuite))
}
