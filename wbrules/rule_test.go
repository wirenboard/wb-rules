package wbrules

import (
	"fmt"
	"github.com/contactless/wbgo"
	"github.com/contactless/wbgo/testutils"
	"os"
	"regexp"
	"testing"
	"time"
)

type fakeCron struct {
	t       *testing.T
	started bool
	entries map[string][]func()
}

func newFakeCron(t *testing.T) *fakeCron {
	return &fakeCron{t, false, make(map[string][]func())}
}

func (cron *fakeCron) AddFunc(spec string, cmd func()) error {
	if entries, found := cron.entries[spec]; found {
		cron.entries[spec] = append(entries, cmd)
	} else {
		cron.entries[spec] = []func(){cmd}
	}
	return nil
}

func (cron *fakeCron) Start() {
	wbgo.Debug.Printf("fakeCron.Start()")
	cron.started = true
}

func (cron *fakeCron) Stop() {
	wbgo.Debug.Printf("fakeCron.Stop()")
	cron.started = false
}

func (cron *fakeCron) invokeEntries(spec string) {
	if !cron.started {
		cron.t.Fatalf("trying to invoke cron entry (spec '%s') when cron isn't started yet",
			spec)
	}
	if entries, found := cron.entries[spec]; found {
		for _, cmd := range entries {
			cmd()
		}
	}
}

type RuleSuiteBase struct {
	CellSuiteBase
	ruleFile string
	*testutils.DataFileFixture
	*testutils.FakeTimerFixture
	engine *ESEngine
	cron   *fakeCron
}

var logVerifyRx = regexp.MustCompile(`^\[(info|debug|warning|error)\] (.*)`)

func (s *RuleSuiteBase) preprocessItemsForVerify(items []interface{}) (newItems []interface{}) {
	newItems = make([]interface{}, len(items))
	for n, item := range items {
		itemStr, ok := item.(string)
		if !ok {
			newItems[n] = item
			continue
		}
		groups := logVerifyRx.FindStringSubmatch(itemStr)
		if groups == nil {
			newItems[n] = item
			continue
		}
		logLevelStr, message := groups[1], groups[2]
		newItems[n] = fmt.Sprintf("driver -> /wbrules/log/%s: [%s] (QoS 1)", logLevelStr, message)
	}
	return
}

func (s *RuleSuiteBase) Verify(items ...interface{}) {
	s.CellSuiteBase.Verify(s.preprocessItemsForVerify(items)...)
}

func (s *RuleSuiteBase) VerifyUnordered(items ...interface{}) {
	s.CellSuiteBase.VerifyUnordered(s.preprocessItemsForVerify(items)...)
}

func (s *RuleSuiteBase) SetupTest(waitForRetained bool, ruleFiles ...string) {
	s.CellSuiteBase.SetupTest(waitForRetained)
	s.DataFileFixture = testutils.NewDataFileFixture(s.T())
	s.FakeTimerFixture = testutils.NewFakeTimerFixture(s.T(), s.Recorder)
	s.cron = nil
	s.engine = NewESEngine(s.model, s.driverClient)
	s.engine.SetTimerFunc(s.newFakeTimer)
	s.engine.SetCronMaker(func() Cron {
		s.cron = newFakeCron(s.T())
		return s.cron
	})
	s.loadScripts(ruleFiles)
	s.driver.Start()
	if !waitForRetained {
		s.publishSomedev()
	}
}

func (s *RuleSuiteBase) loadScripts(scripts []string) {
	wd, err := os.Getwd()
	s.Ck("Getwd()", err)
	s.Ck("SetSourceRoot()", s.engine.SetSourceRoot(s.DataFileTempDir()))
	// change back to the original working directory
	s.Ck("Chdir()", os.Chdir(wd))
	// Copy scripts to the temporary directory recreating a part
	// of original directory structure that contains these
	// scripts.
	for _, script := range scripts {
		copiedScriptPath := s.CopyDataFileToTempDir(script, script)
		s.Ck("LoadFile()", s.engine.LoadFile(copiedScriptPath))
	}
}

func (s *RuleSuiteBase) ReplaceScript(oldName, newName string) {
	copiedScriptPath := s.CopyDataFileToTempDir(newName, oldName)
	s.Ck("LiveLoadFile()", s.engine.LiveLoadFile(copiedScriptPath))
}

func (s *RuleSuiteBase) OverwriteScript(oldName, newName string) error {
	return s.engine.LiveWriteScript(oldName, s.ReadSourceDataFile(newName))
}

func (s *RuleSuiteBase) LiveLoadScript(script string) error {
	copiedScriptPath := s.CopyDataFileToTempDir(script, script)
	return s.engine.LiveLoadFile(copiedScriptPath)
}

func (s *RuleSuiteBase) RemoveScript(oldName string) {
	s.engine.LiveRemoveFile(s.DataFilePath(oldName))
}

func (s *RuleSuiteBase) SetupSkippingDefs(ruleFiles ...string) {
	s.SetupTest(false, ruleFiles...)
	s.SkipTill("tst -> /devices/somedev/controls/temp: [19] (QoS 1, retained)")
	s.engine.Start()
	<-s.engine.ReadyCh()
	return
}

func (s *RuleSuiteBase) newFakeTimer(id int, d time.Duration, periodic bool) wbgo.Timer {
	return s.NewFakeTimerOrTicker(id, d, periodic)
}

func (s *RuleSuiteBase) publishSomedev() {
	<-s.model.publishDoneCh
	s.publish("/devices/somedev/meta/name", "SomeDev", "")
	s.publish("/devices/somedev/controls/sw/meta/type", "switch", "somedev/sw")
	s.publish("/devices/somedev/controls/sw", "0", "somedev/sw")
	s.publish("/devices/somedev/controls/temp/meta/type", "temperature", "somedev/temp")
	s.publish("/devices/somedev/controls/temp", "19", "somedev/temp")
}

func (s *RuleSuiteBase) SetCellValue(device, cellName string, value interface{}) {
	s.driver.CallSync(func() {
		s.model.EnsureDevice(device).EnsureCell(cellName).SetValue(value)
	})
	actualCellSpec := <-s.cellChange
	s.Equal(device+"/"+cellName,
		actualCellSpec.DevName+"/"+actualCellSpec.CellName)
}

func (s *RuleSuiteBase) TearDownTest() {
	s.TearDownDataFiles()
	s.CellSuiteBase.TearDownTest()
	s.WaitFor(func() bool {
		return !s.engine.IsActive()
	})
}

// TBD: metadata (like, meta["devname"]["controlName"])
// TBD: test bad device/rule defs
// TBD: traceback
// TBD: if rule *did* change anything (SetValue had an effect), re-run rules
//      and do so till no va\lues are changed
// TBD: don't hang upon bad Verify() list
//      (deadlock detection fails due to duktape)
// TBD: should use separate recorder for the fixture, not abuse the fake broker
// TBD: abstract away duktape stuff from the primary engine. This will be useful for scenes etc.
//      Also, it will make the code cleaner.
//      IMPORTANT HINT: get rid of explicit callback key spec altogether!
//      And get rid of separate callback storages, too.
// TBD: destroy ES context when stopping the engine
// TBD: indicate an error upon access to undefined cells of local devices
