package wbrules

import (
	"os"
	"testing"

	"github.com/wirenboard/wbgong/testutils"
)

// wb-rules Depends on wb-tsgo, so a missing compiler is a broken
// installation: .ts files must fail to load with an explicit error
// naming the looked-up path - and never run as raw JavaScript.
type NoTsgoSuite struct {
	RuleSuiteBase
}

func (s *NoTsgoSuite) SetupTest() {
	// broken-installation scenario: flag points at the default path,
	// nothing is installed there
	s.TsgoPath = "/definitely-no-tsgo-here"
	s.SetupSkippingDefs()
}

func (s *NoTsgoSuite) TestTsFileFailsExplicitly() {
	path := s.CopyDataFileToTempDir("testrules_ts.ts", "testrules_ts.ts")
	err := s.engine.LiveLoadFile(path)
	s.Error(err, "a .ts file without tsgo must fail to load")
	s.Contains(err.Error(),
		"TypeScript compiler not found at /definitely-no-tsgo-here - wb-rules depends on the wb-tsgo package (broken installation?)")

	// the engine itself stays healthy: plain JS still loads and runs
	s.Ck("LiveLoadScript()", s.LiveLoadScript("testrules_async_leak_empty.js"))
	s.publish("/devices/async_leak/controls/report/on", "1", "async_leak/report")
	s.SkipTill("wbrules-log -> /wbrules/log/info: [probe alive] (QoS 1)")
}

// The install-ordering scenario seen in the field: wb-rules started
// before the wb-tsgo package landed (only possible with pre-Depends
// packages or manual installs). The engine must pick the compiler up on
// the next .ts load - without a restart.
type LateTsgoSuite struct {
	RuleSuiteBase
	tsgoDir string
}

func (s *LateTsgoSuite) SetupTest() {
	var err error
	s.tsgoDir, err = os.MkdirTemp("", "wbrules-late-tsgo")
	s.Ck("MkdirTemp()", err)
	// the engine starts while this path does not exist yet
	s.TsgoPath = s.tsgoDir + "/tsgo"
	s.TsTypesPath = testTypesPath()
	s.SetupSkippingDefs()
}

func (s *LateTsgoSuite) TearDownTest() {
	s.RuleSuiteBase.TearDownTest()
	os.RemoveAll(s.tsgoDir)
}

func (s *LateTsgoSuite) TestTsgoAppearsWithoutRestart() {
	path := s.CopyDataFileToTempDir("testrules_ts.ts", "testrules_ts.ts")
	err := s.engine.LiveLoadFile(path)
	s.Error(err, "the compiler is not there yet")

	// the wb-tsgo package "arrives": symlink the real binary into place
	s.Ck("Symlink()", os.Symlink(tsgoForTests(), s.TsgoPath))

	// same engine, no restart: the .ts file now loads and its rules run
	s.Ck("LiveLoadFile()", s.engine.LiveLoadFile(path))
	// drain the load messages (failed-attempt error log, vdev creation)
	s.SkipTill("Subscribe -- driver: /devices/tsdev/controls/trigger/on")
	s.publish("/devices/tsdev/controls/trigger/on", "1", "tsdev/trigger", "tsdev/count")
	s.VerifyUnordered(
		"wbrules-log -> /wbrules/updates/changed: [testrules_ts.ts] (QoS 1)",
		"tst -> /devices/tsdev/controls/trigger/on: [1] (QoS 1)",
		"driver -> /devices/tsdev/controls/trigger: [1] (QoS 1, retained)",
		"driver -> /devices/tsdev/controls/count: [1] (QoS 1, retained)",
		"[info] ts rule fired: true count=1",
	)
	// the background type check must reach a clean verdict too (a noisy
	// one would leave unconsumed warnings and fail teardown)
	s.WaitFor(func() bool {
		diags, status := s.engine.CheckTsFile(path)
		return status == TS_CHECK_READY && len(diags) == 0
	})
}

// A .js file loaded while tsgo was absent runs anyway and gets no check
// scheduled; once tsgo appears, Editor.Check must not answer "pending"
// forever - the first query schedules the check lazily.
func (s *LateTsgoSuite) TestJsLoadedBeforeTsgoIsCheckedOnDemand() {
	path := s.CopyDataFileToTempDir("testrules_js_registry.js", "testrules_js_registry.js")
	s.Ck("LiveLoadFile()", s.engine.LiveLoadFile(path)) // .js runs without tsgo
	s.SkipTill("wbrules-log -> /wbrules/updates/changed: [testrules_js_registry.js] (QoS 1)")
	_, status := s.engine.CheckTsFile(path)
	s.Equal(TS_CHECK_UNSUPPORTED, status, "no compiler yet")

	s.Ck("Symlink()", os.Symlink(tsgoForTests(), s.TsgoPath))

	// the first query after tsgo appeared schedules the check; poll to ready
	s.WaitFor(func() bool {
		diags, status := s.engine.CheckTsFile(path)
		if status != TS_CHECK_READY {
			return false
		}
		// the fixture's line 8 (string to a numeric control) is not flagged
		// here: this engine defines no tsdev device, so the registry is
		// empty and dev[...] stays loose - a clean verdict is the expected one
		return len(diags) == 0
	})
}

func TestNoTsgoSuite(t *testing.T) {
	testutils.RunSuites(t, new(NoTsgoSuite))
}

func TestLateTsgoSuite(t *testing.T) {
	// skip HERE, not in SetupTest: testify treats a Skip inside SetupTest
	// as a failure and strands the fixture chain (stranded cwd cascades
	// into unrelated suites' module resolution under -trimpath)
	if tsgoForTests() == "" {
		t.Skip("no tsgo: WB_RULES_TSGO not set and /usr/bin/tsgo absent")
	}
	testutils.RunSuites(t, new(LateTsgoSuite))
}
