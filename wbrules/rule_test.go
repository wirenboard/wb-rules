package wbrules

import (
	"fmt"
	"io/ioutil"
	"os"
	"regexp"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/wirenboard/wbgong"
	"github.com/wirenboard/wbgong/testutils"
)

const (
	WBRULES_DRIVER_ID              = "wbrules"
	EXTRA_CTRL_CHANGE_WAIT_TIME_MS = 50
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
	wbgong.Debug.Printf("fakeCron.Start()")
	cron.started = true
}

func (cron *fakeCron) Stop() {
	wbgong.Debug.Printf("fakeCron.Stop()")
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
	testutils.Suite
	*testutils.FakeMQTTFixture

	*testutils.DataFileFixture
	*testutils.FakeTimerFixture

	driver                          wbgong.Driver
	client, driverClient, logClient wbgong.MQTTClient

	engine *ESEngine

	controlChange <-chan *ControlChangeEvent

	ruleFile string
	cron     *fakeCron

	PersistentDBFile string
	VdevStorageFile  string
	ModulesPath      string /* ':'-separated list */
	CleanUp          func()
}

var logVerifyRx = regexp.MustCompile(`^\[(info|debug|warning|error)\] (.*)`)
var updatesVerifyRx = regexp.MustCompile(`^\[(changed|removed)\] (.*)`)

// creates necessary file paths if some are not defined already
func (s *RuleSuiteBase) createTempFiles() {
	tmpDir, err := ioutil.TempDir(os.TempDir(), "wbrulestest")
	if err != nil {
		s.FailNow("can't create temp directory")
	}
	wbgong.Debug.Printf("created temp dir %s", tmpDir)

	if s.PersistentDBFile == "" {
		s.PersistentDBFile = tmpDir + "/test-persistent.db"
	}

	s.CleanUp = func() {
		os.RemoveAll(tmpDir)
	}

	wbgong.Debug.Printf("RuleSuiteBase created temp dir %s", tmpDir)
}

func (s *RuleSuiteBase) preprocessItemsForVerify(items []interface{}) (newItems []interface{}) {
	newItems = make([]interface{}, len(items))
	for n, item := range items {
		itemStr, ok := item.(string)
		if !ok {
			newItems[n] = item
			continue
		}
		uGroups := updatesVerifyRx.FindStringSubmatch(itemStr)
		if uGroups != nil {
			action, message := uGroups[1], uGroups[2]
			newItems[n] = fmt.Sprintf("wbrules-log -> /wbrules/updates/%s: [%s] (QoS 1)", action, message)
			continue
		}

		groups := logVerifyRx.FindStringSubmatch(itemStr)
		if groups != nil {
			logLevelStr, message := groups[1], groups[2]
			newItems[n] = fmt.Sprintf("wbrules-log -> /wbrules/log/%s: [%s] (QoS 1)", logLevelStr, message)
			continue
		}

		newItems[n] = item
	}
	return
}

func (s *RuleSuiteBase) Verify(items ...interface{}) {
	s.FakeMQTTFixture.Verify(s.preprocessItemsForVerify(items)...)
}

func (s *RuleSuiteBase) VerifyUnordered(items ...interface{}) {
	s.FakeMQTTFixture.VerifyUnordered(s.preprocessItemsForVerify(items)...)
}

func (s *RuleSuiteBase) SkipTill(item interface{}) {
	items := make([]interface{}, 1)
	items[0] = item
	s.FakeMQTTFixture.SkipTill(s.preprocessItemsForVerify(items)[0].(string))
}

func (s *RuleSuiteBase) expectControlChange(expectedControlNames ...string) {
	// need to compare lists correctly
	if expectedControlNames == nil {
		expectedControlNames = make([]string, 0)
	}

	// Notifications happen asynchronously and aren't guaranteed to be
	// keep original order. Perhaps this needs to be fixed.
	actualControlNames := make([]string, len(expectedControlNames))

	for i := range actualControlNames {
		var e *ControlChangeEvent

		wbgong.Debug.Printf("TEST: controlChange channel is %v", s.controlChange)

		t := time.After(5 * time.Second)
	FORLOOP1:
		for {
			select {
			case e = <-s.controlChange:
				if strings.Contains(e.Spec.ControlId, "#") {
					continue FORLOOP1
				}
				wbgong.Debug.Printf("received ControlChangeEvent %v\n", e)
				break FORLOOP1
			case <-t:
				s.FailNow(fmt.Sprintf("timeout waiting for control change event: '%s'", expectedControlNames[i]))
			}
		}

		t = nil

		ctrlSpec := e.Spec
		fullName := fmt.Sprintf("%s/%s", ctrlSpec.DeviceId, ctrlSpec.ControlId)
		actualControlNames[i] = fullName
	}
	sort.Strings(expectedControlNames)
	sort.Strings(actualControlNames)

	// compare
	s.Equal(expectedControlNames, actualControlNames)

	timer := time.After(EXTRA_CTRL_CHANGE_WAIT_TIME_MS * time.Millisecond)
	select {
	case <-timer:
	case e := <-s.controlChange:
		if !strings.Contains(e.Spec.ControlId, "#") {
			s.Require().Fail("unexpected control change", "control: %v", e.Spec)
		}
	}
}

func (s *RuleSuiteBase) T() *testing.T {
	return s.Suite.T()
}

func (s *RuleSuiteBase) SetupTest(waitForRetained bool, ruleFiles ...string) {
	var err error

	wbgong.SetDebuggingEnabled(true)

	s.Suite.SetupTest()
	s.FakeMQTTFixture = testutils.NewFakeMQTTFixture(s.T())

	s.Broker.SetWaitForRetained(waitForRetained)

	s.client = s.Broker.MakeClient("tst")
	s.client.Start()

	if s.PersistentDBFile == "" {
		s.createTempFiles()
	}

	s.driverClient = s.Broker.MakeClient("driver")
	dargs := wbgong.NewDriverArgs().
		SetId(WBRULES_DRIVER_ID).
		SetMqtt(s.driverClient).
		SetTesting()

	if s.VdevStorageFile == "" {
		dargs.SetUseStorage(false)
	} else {
		dargs.SetUseStorage(true)
		dargs.SetStoragePath(s.VdevStorageFile)
	}

	s.driver, err = wbgong.NewDriverBase(dargs)
	s.Ck("can't create driver", err)

	err = s.driver.StartLoop()
	s.Ck("StartLoop()", err)

	// wait for the first ready event
	s.driver.WaitForReady()

	s.driver.SetFilter(&wbgong.AllDevicesFilter{})

	s.cron = nil

	engineOptions := NewESEngineOptions()
	engineOptions.SetPersistentDBFile(s.PersistentDBFile)
	engineOptions.SetModulesDirs(strings.Split(s.ModulesPath, ":"))
	s.logClient = s.Broker.MakeClient("wbrules-log")

	s.engine, err = NewESEngine(s.driver, s.logClient, engineOptions)
	s.Ck("NewESEngine()", err)

	s.engine.SetTimerFunc(s.newFakeTimer)
	s.engine.SetCronMaker(func() Cron {
		s.cron = newFakeCron(s.T())
		return s.cron
	})

	s.controlChange = s.engine.SubscribeControlChange()
	s.DataFileFixture = testutils.NewDataFileFixture(s.T())
	s.FakeTimerFixture = testutils.NewFakeTimerFixture(s.T(), s.Recorder)

	s.engine.Start()

	s.loadScripts(ruleFiles)

	if !waitForRetained {
		s.publishSomedev()
	}
}

func (s *RuleSuiteBase) loadScripts(scripts []string) {
	s.Ck("SetSourceRoot()", s.engine.SetSourceRoot(s.DataFileTempDir()))
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

func (s *RuleSuiteBase) RenameScript(oldName, newName string) {
	s.Ck("RenameScript()", os.Rename(oldName, newName))
}

func (s *RuleSuiteBase) OverwriteScript(oldName, newName string) error {
	return s.engine.LiveWriteScript(oldName, s.ReadSourceDataFile(newName))
}

func (s *RuleSuiteBase) LiveLoadScript(script string) error {
	copiedScriptPath := s.CopyDataFileToTempDir(script, script)
	return s.engine.LiveLoadFile(copiedScriptPath)
}

// load script right from its location
// usable to test persistent storages
func (s *RuleSuiteBase) LiveLoadScriptToDir(script, dir string) error {
	data := s.ReadSourceDataFile(script)
	path := dir + "/" + script
	s.DataFileFixture.Ckf("WriteFile", ioutil.WriteFile(path, []byte(data), 0777))
	return s.engine.LiveLoadFile(path)
}

func (s *RuleSuiteBase) RemoveScript(oldName string) {
	s.engine.LiveRemoveFile(s.DataFilePath(oldName))
}

func (s *RuleSuiteBase) SetupSkippingDefs(ruleFiles ...string) {
	s.SetupTest(false, ruleFiles...)
	s.SkipTill("tst -> /devices/somedev/controls/temp: [19] (QoS 1, retained)")

	select {
	case <-time.After(5 * time.Second):
		s.FailNow("engine is not ready for such a long time")
	case <-s.engine.ReadyCh():
	}
	return
}

func (s *RuleSuiteBase) newFakeTimer(id TimerId, d time.Duration, periodic bool) wbgong.Timer {
	return s.NewFakeTimerOrTicker(uint64(id), d, periodic)
}

func (s *RuleSuiteBase) publish(topic, value string, expectedCellNames ...string) {
	retained := !strings.HasSuffix(topic, "/on")
	wbgong.Debug.Printf("publishing %s to %s, expecting change of %v", value, topic, expectedCellNames)
	s.client.Publish(wbgong.MQTTMessage{topic, value, 1, retained})
	s.expectControlChange(expectedCellNames...)
}

func (s *RuleSuiteBase) publishSomedev() {
	s.publish("/devices/somedev/meta/name", "SomeDev")
	s.publish("/devices/somedev/controls/sw/meta/type", "switch", "somedev/sw")
	s.publish("/devices/somedev/controls/sw", "0", "somedev/sw")
	s.publish("/devices/somedev/controls/temp/meta/type", "temperature", "somedev/temp")
	s.publish("/devices/somedev/controls/temp", "19", "somedev/temp")
}

func (s *RuleSuiteBase) SetCellValue(devId, ctrlId string, value interface{}) {
	err := s.driver.Access(func(tx wbgong.DriverTx) (err error) {
		dev := tx.GetDevice(devId)
		ctrl := dev.GetControl(ctrlId)
		err = ctrl.UpdateValue(value, true)()

		return err
	})
	s.Ck("Access()", err)
	event := <-s.controlChange
	ctrlSpec := event.Spec

	s.Equal(devId+"/"+ctrlId, ctrlSpec.String())
}

func (s *RuleSuiteBase) SetCellValueNoVerify(devID, ctrlID string, value interface{}) {
	err := s.driver.Access(func(tx wbgong.DriverTx) (err error) {
		dev := tx.GetDevice(devID)
		ctrl := dev.GetControl(ctrlID)
		err = ctrl.UpdateValue(value, true)()

		return err
	})
	s.Ck("Access()", err)
	<-s.controlChange
}

func (s *RuleSuiteBase) TearDownTest() {
	s.Broker.VerifyEmpty()

	s.TearDownDataFiles()

	s.engine.Stop()
	s.WaitFor(func() bool {
		return !s.engine.IsActive()
	})

	s.engine.ClosePersistentDB()
	s.PersistentDBFile = ""
	s.VdevStorageFile = ""

	if s.CleanUp != nil {
		s.CleanUp()
	}

	err := s.driver.StopLoop()
	s.Ck("StopLoop()", err)

	s.client.Stop()
	s.logClient.Stop()

	s.driver.Close()
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
