package wbrules

import (
	"fmt"
	wbgo "github.com/contactless/wbgo"
	"io/ioutil"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
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
	*wbgo.FakeTimerFixture
	engine      *ESEngine
	cron        *fakeCron
	scriptDir   string
	rmScriptDir func()
}

var logVerifyRx = regexp.MustCompile(`^\[(info|debug|warning|error)\] (.*)`)

func (s *RuleSuiteBase) Verify(items ...string) {
	for n, item := range items {
		groups := logVerifyRx.FindStringSubmatch(item)
		if groups == nil {
			continue
		}
		logLevelStr, message := groups[1], groups[2]
		items[n] = fmt.Sprintf("driver -> /wbrules/log/%s: [%s] (QoS 1)", logLevelStr, message)
	}
	s.CellSuiteBase.Verify(items...)
}

func (s *RuleSuiteBase) SetupTest(waitForRetained bool, ruleFiles ...string) {
	s.CellSuiteBase.SetupTest(waitForRetained)
	s.FakeTimerFixture = wbgo.NewFakeTimerFixture(s.T(), s.Recorder)
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

func (s *RuleSuiteBase) ScriptPath(script string) string {
	return filepath.Join(s.scriptDir, script)
}

func (s *RuleSuiteBase) copyScriptToTempDir(sourceName, targetName string) (targetPath string) {
	data, err := ioutil.ReadFile(sourceName)
	s.Ck("ReadFile()", err)
	targetPath = s.ScriptPath(targetName)
	if strings.Contains(targetName, "/") {
		// the target file is under a subdir
		s.Ck("MkdirAll", os.MkdirAll(filepath.Dir(targetPath), 0777))
	}
	s.Ck("WriteFile", ioutil.WriteFile(targetPath, data, 0777))
	return
}

func (s *RuleSuiteBase) loadScripts(scripts []string) {
	wd, err := os.Getwd()
	s.Ck("Getwd()", err)
	s.scriptDir, s.rmScriptDir = wbgo.SetupTempDir(s.T()) // this does chdir
	s.Ck("SetSourceRoot()", s.engine.SetSourceRoot(s.scriptDir))
	// change back to the original working directory
	s.Ck("Chdir()", os.Chdir(wd))
	// Copy scripts to the temporary directory recreating a part
	// of original directory structure that contains these
	// scripts.
	for _, script := range scripts {
		copiedScriptPath := s.copyScriptToTempDir(script, script)
		s.Ck("LoadScript()", s.engine.LoadScript(copiedScriptPath))
	}
}

func (s *RuleSuiteBase) ReplaceScript(oldName, newName string) {
	copiedScriptPath := s.copyScriptToTempDir(newName, oldName)
	s.Ck("LiveLoadScript()", s.engine.LiveLoadScript(copiedScriptPath))
}

func (s *RuleSuiteBase) RemoveScript(oldName string) {
	s.engine.LiveRemoveScript(s.ScriptPath(oldName))
}

func (s *RuleSuiteBase) SetupSkippingDefs(ruleFiles ...string) {
	s.SetupTest(false, ruleFiles...)
	s.SkipTill("tst -> /devices/somedev/controls/temp: [19] (QoS 1, retained)")
	s.engine.Start()
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
	s.rmScriptDir()
	s.CellSuiteBase.TearDownTest()
	s.WaitFor(func() bool {
		return !s.engine.IsActive()
	})
}

type RuleDefSuite struct {
	RuleSuiteBase
}

func (s *RuleDefSuite) SetupTest() {
	s.RuleSuiteBase.SetupTest(false, "testrules.js")
}

func (s *RuleDefSuite) TestDeviceDefinition() {
	s.Verify(
		"driver -> /devices/stabSettings/meta/name: [Stabilization Settings] (QoS 1, retained)",
		"driver -> /devices/wbrules/meta/name: [Rule Engine Settings] (QoS 1, retained)",

		"Subscribe -- driver: /devices/+/meta/name",
		"Subscribe -- driver: /devices/+/controls/+",
		"Subscribe -- driver: /devices/+/controls/+/meta/type",
		"Subscribe -- driver: /devices/+/controls/+/meta/max",

		"driver -> /devices/stabSettings/controls/enabled/meta/type: [switch] (QoS 1, retained)",
		"driver -> /devices/stabSettings/controls/enabled/meta/order: [1] (QoS 1, retained)",
		"driver -> /devices/stabSettings/controls/enabled: [0] (QoS 1, retained)",
		"Subscribe -- driver: /devices/stabSettings/controls/enabled/on",
		"driver -> /devices/stabSettings/controls/highThreshold/meta/type: [range] (QoS 1, retained)",
		"driver -> /devices/stabSettings/controls/highThreshold/meta/order: [2] (QoS 1, retained)",
		"driver -> /devices/stabSettings/controls/highThreshold/meta/max: [50] (QoS 1, retained)",
		"driver -> /devices/stabSettings/controls/highThreshold: [22] (QoS 1, retained)",
		"Subscribe -- driver: /devices/stabSettings/controls/highThreshold/on",
		"driver -> /devices/stabSettings/controls/lowThreshold/meta/type: [range] (QoS 1, retained)",
		"driver -> /devices/stabSettings/controls/lowThreshold/meta/order: [3] (QoS 1, retained)",
		"driver -> /devices/stabSettings/controls/lowThreshold/meta/max: [40] (QoS 1, retained)",
		"driver -> /devices/stabSettings/controls/lowThreshold: [20] (QoS 1, retained)",
		"Subscribe -- driver: /devices/stabSettings/controls/lowThreshold/on",

		"driver -> /devices/wbrules/controls/Rule debugging/meta/type: [switch] (QoS 1, retained)",
		"driver -> /devices/wbrules/controls/Rule debugging/meta/order: [1] (QoS 1, retained)",
		"driver -> /devices/wbrules/controls/Rule debugging: [0] (QoS 1, retained)",
		"Subscribe -- driver: /devices/wbrules/controls/Rule debugging/on",

		"tst -> /devices/somedev/meta/name: [SomeDev] (QoS 1, retained)",
		"tst -> /devices/somedev/controls/sw/meta/type: [switch] (QoS 1, retained)",
		"tst -> /devices/somedev/controls/sw: [0] (QoS 1, retained)",
		"tst -> /devices/somedev/controls/temp/meta/type: [temperature] (QoS 1, retained)",
		"tst -> /devices/somedev/controls/temp: [19] (QoS 1, retained)",
	)
}

type RuleBasicsSuite struct {
	RuleSuiteBase
}

func (s *RuleBasicsSuite) SetupTest() {
	s.SetupSkippingDefs("testrules.js")
}

func (s *RuleBasicsSuite) TestRules() {
	s.SetCellValue("stabSettings", "enabled", true)
	s.Verify(
		"driver -> /devices/stabSettings/controls/enabled: [1] (QoS 1, retained)",
		"[info] heaterOn fired, changed: stabSettings/enabled -> true",
		"driver -> /devices/somedev/controls/sw/on: [1] (QoS 1)",
	)
	s.publish("/devices/somedev/controls/sw", "1", "somedev/sw")
	s.Verify(
		"tst -> /devices/somedev/controls/sw: [1] (QoS 1, retained)",
	)

	s.publish("/devices/somedev/controls/temp", "21", "somedev/temp")
	s.Verify(
		"tst -> /devices/somedev/controls/temp: [21] (QoS 1, retained)",
	)

	s.publish("/devices/somedev/controls/temp", "22", "somedev/temp")
	s.Verify(
		"tst -> /devices/somedev/controls/temp: [22] (QoS 1, retained)",
		"[info] heaterOff fired, changed: somedev/temp -> 22",
		"driver -> /devices/somedev/controls/sw/on: [0] (QoS 1)",
	)
	s.publish("/devices/somedev/controls/sw", "0", "somedev/sw")
	s.Verify(
		"tst -> /devices/somedev/controls/sw: [0] (QoS 1, retained)",
	)

	s.publish("/devices/somedev/controls/temp", "18", "somedev/temp")
	s.Verify(
		"tst -> /devices/somedev/controls/temp: [18] (QoS 1, retained)",
		"[info] heaterOn fired, changed: somedev/temp -> 18",
		"driver -> /devices/somedev/controls/sw/on: [1] (QoS 1)",
	)
	s.publish("/devices/somedev/controls/sw", "1", "somedev/sw")
	s.Verify(
		"tst -> /devices/somedev/controls/sw: [1] (QoS 1, retained)",
	)

	// edge-triggered rule doesn't fire
	s.publish("/devices/somedev/controls/temp", "19", "somedev/temp")
	s.Verify(
		"tst -> /devices/somedev/controls/temp: [19] (QoS 1, retained)",
	)

	s.SetCellValue("stabSettings", "enabled", false)
	s.Verify(
		"driver -> /devices/stabSettings/controls/enabled: [0] (QoS 1, retained)",
		"[info] heaterOff fired, changed: stabSettings/enabled -> false",
		"driver -> /devices/somedev/controls/sw/on: [0] (QoS 1)",
	)
	s.publish("/devices/somedev/controls/sw", "0", "somedev/sw")
	s.Verify(
		"tst -> /devices/somedev/controls/sw: [0] (QoS 1, retained)",
	)

	s.publish("/devices/somedev/controls/foobar", "1", "somedev/foobar")
	s.publish("/devices/somedev/controls/foobar/meta/type", "text", "somedev/foobar")
	s.Verify(
		"tst -> /devices/somedev/controls/foobar: [1] (QoS 1, retained)",
		"tst -> /devices/somedev/controls/foobar/meta/type: [text] (QoS 1, retained)",
		"[info] initiallyIncompleteLevelTriggered fired",
	)

	// level-triggered rule fires again here
	s.publish("/devices/somedev/controls/foobar", "2", "somedev/foobar")
	s.Verify(
		"tst -> /devices/somedev/controls/foobar: [2] (QoS 1, retained)",
		"[info] initiallyIncompleteLevelTriggered fired",
	)
}

func (s *RuleBasicsSuite) TestDirectMQTTMessages() {
	s.publish("/devices/somedev/controls/sendit/meta/type", "switch", "somedev/sendit")
	s.publish("/devices/somedev/controls/sendit", "1", "somedev/sendit")
	s.Verify(
		"tst -> /devices/somedev/controls/sendit/meta/type: [switch] (QoS 1, retained)",
		"tst -> /devices/somedev/controls/sendit: [1] (QoS 1, retained)",
		"driver -> /abc/def/ghi: [0] (QoS 0)",
		"driver -> /misc/whatever: [abcdef] (QoS 1)",
		"driver -> /zzz/foo: [qqq] (QoS 2)",
		"driver -> /zzz/foo/qwerty: [42] (QoS 2, retained)",
	)
}

func (s *RuleBasicsSuite) TestCellChange() {
	s.publish("/devices/somedev/controls/foobarbaz/meta/type", "text", "somedev/foobarbaz")
	s.publish("/devices/somedev/controls/foobarbaz", "abc", "somedev/foobarbaz")
	s.Verify(
		"tst -> /devices/somedev/controls/foobarbaz/meta/type: [text] (QoS 1, retained)",
		"tst -> /devices/somedev/controls/foobarbaz: [abc] (QoS 1, retained)",
		"[info] cellChange1: somedev/foobarbaz=abc (string)",
		"[info] cellChange2: somedev/foobarbaz=abc (string)",
	)

	s.publish("/devices/somedev/controls/tempx/meta/type", "temperature", "somedev/tempx")
	s.publish("/devices/somedev/controls/tempx", "42", "somedev/tempx")
	s.Verify(
		"tst -> /devices/somedev/controls/tempx/meta/type: [temperature] (QoS 1, retained)",
		"tst -> /devices/somedev/controls/tempx: [42] (QoS 1, retained)",
		"[info] cellChange2: somedev/tempx=42 (number)",
	)
	// no change
	s.publish("/devices/somedev/controls/tempx", "42", "somedev/tempx")
	s.publish("/devices/somedev/controls/tempx", "42", "somedev/tempx")
	s.Verify(
		"tst -> /devices/somedev/controls/tempx: [42] (QoS 1, retained)",
		"tst -> /devices/somedev/controls/tempx: [42] (QoS 1, retained)",
	)
}

func (s *RuleBasicsSuite) TestRemoteButtons() {
	// FIXME: handling remote buttons, i.e. buttons that
	// are defined for external devices and not via defineVirtualDevice(),
	// needs more work. We need to handle /on messages for these
	// instead of value messages. As of now, the code will work
	// unless the remote driver retains button value, in which
	// case extra change events will be received on startup
	// The change rule must be fired on each button press ('1' value message)
	s.publish("/devices/somedev/controls/abutton/meta/type", "pushbutton", "somedev/abutton")
	s.publish("/devices/somedev/controls/abutton", "1", "somedev/abutton")
	s.Verify(
		"tst -> /devices/somedev/controls/abutton/meta/type: [pushbutton] (QoS 1, retained)",
		"tst -> /devices/somedev/controls/abutton: [1] (QoS 1, retained)",
		"[info] cellChange2: somedev/abutton=false (boolean)",
	)
	s.publish("/devices/somedev/controls/abutton", "1", "somedev/abutton")
	s.Verify(
		"tst -> /devices/somedev/controls/abutton: [1] (QoS 1, retained)",
		"[info] cellChange2: somedev/abutton=false (boolean)",
	)
}

func (s *RuleBasicsSuite) TestFuncValueChange() {
	s.publish("/devices/somedev/controls/cellforfunc", "2", "somedev/cellforfunc")
	s.Verify(
		// the cell is incomplete here
		"tst -> /devices/somedev/controls/cellforfunc: [2] (QoS 1, retained)",
	)

	s.publish("/devices/somedev/controls/cellforfunc/meta/type", "temperature", "somedev/cellforfunc")
	s.Verify(
		"tst -> /devices/somedev/controls/cellforfunc/meta/type: [temperature] (QoS 1, retained)",
		"[info] funcValueChange: false (boolean)",
	)

	s.publish("/devices/somedev/controls/cellforfunc", "5", "somedev/cellforfunc")
	s.Verify(
		"tst -> /devices/somedev/controls/cellforfunc: [5] (QoS 1, retained)",
		"[info] funcValueChange: true (boolean)",
	)

	s.publish("/devices/somedev/controls/cellforfunc", "7", "somedev/cellforfunc")
	s.Verify(
		// expression value not changed
		"tst -> /devices/somedev/controls/cellforfunc: [7] (QoS 1, retained)",
	)

	s.publish("/devices/somedev/controls/cellforfunc", "1", "somedev/cellforfunc")
	s.Verify(
		"tst -> /devices/somedev/controls/cellforfunc: [1] (QoS 1, retained)",
		"[info] funcValueChange: false (boolean)",
	)

	s.publish("/devices/somedev/controls/cellforfunc", "0", "somedev/cellforfunc")
	s.Verify(
		// expression value not changed
		"tst -> /devices/somedev/controls/cellforfunc: [0] (QoS 1, retained)",
	)

	// somedev/cellforfunc1 is listed by name
	s.publish("/devices/somedev/controls/cellforfunc1", "2", "somedev/cellforfunc1")
	s.publish("/devices/somedev/controls/cellforfunc2", "2", "somedev/cellforfunc2")
	s.Verify(
		// the cell is incomplete here
		"tst -> /devices/somedev/controls/cellforfunc1: [2] (QoS 1, retained)",
		"tst -> /devices/somedev/controls/cellforfunc2: [2] (QoS 1, retained)",
	)

	s.publish("/devices/somedev/controls/cellforfunc1/meta/type", "temperature", "somedev/cellforfunc1")
	s.Verify(
		"tst -> /devices/somedev/controls/cellforfunc1/meta/type: [temperature] (QoS 1, retained)",
		"[info] funcValueChange2: somedev/cellforfunc1: 2 (number)",
	)

	s.publish("/devices/somedev/controls/cellforfunc2/meta/type", "temperature", "somedev/cellforfunc2")
	s.Verify(
		"tst -> /devices/somedev/controls/cellforfunc2/meta/type: [temperature] (QoS 1, retained)",
		"[info] funcValueChange2: (no cell): false (boolean)",
	)

	s.publish("/devices/somedev/controls/cellforfunc2", "5", "somedev/cellforfunc2")
	s.Verify(
		"tst -> /devices/somedev/controls/cellforfunc2: [5] (QoS 1, retained)",
		"[info] funcValueChange2: (no cell): true (boolean)",
	)
}

type RuleTimersSuite struct {
	RuleSuiteBase
}

func (s *RuleTimersSuite) SetupTest() {
	s.SetupSkippingDefs("testrules_timers.js")
}

func (s *RuleTimersSuite) VerifyTimers(prefix string) {
	s.publish("/devices/somedev/controls/foo/meta/type", "text", "somedev/foo")
	s.publish("/devices/somedev/controls/foo", prefix+"t", "somedev/foo")
	s.Verify(
		"tst -> /devices/somedev/controls/foo/meta/type: [text] (QoS 1, retained)",
		"tst -> /devices/somedev/controls/foo: ["+prefix+"t] (QoS 1, retained)",
		"new fake timer: 1, 500",
		"new fake timer: 2, 500",
	)

	s.publish("/devices/somedev/controls/foo", prefix+"s", "somedev/foo")
	s.Verify(
		"tst -> /devices/somedev/controls/foo: ["+prefix+"s] (QoS 1, retained)",
		"timer.Stop(): 1",
		"timer.Stop(): 2",
	)

	s.publish("/devices/somedev/controls/foo", prefix+"t", "somedev/foo")
	s.Verify(
		"tst -> /devices/somedev/controls/foo: ["+prefix+"t] (QoS 1, retained)",
		"new fake timer: 1, 500",
		"new fake timer: 2, 500",
	)

	ts := s.AdvanceTime(500 * time.Millisecond)
	s.FireTimer(1, ts)
	s.FireTimer(2, ts)
	s.Verify(
		"timer.fire(): 1",
		"timer.fire(): 2",
		"[info] timer fired",
		"[info] timer1 fired",
	)

	s.publish("/devices/somedev/controls/foo", prefix+"p", "somedev/foo")
	s.Verify(
		"tst -> /devices/somedev/controls/foo: ["+prefix+"p] (QoS 1, retained)",
		"new fake ticker: 1, 500",
	)

	for i := 1; i < 4; i++ {
		targetTime := s.AdvanceTime(time.Duration(500*i) * time.Millisecond)
		s.FireTimer(1, targetTime)
		s.Verify(
			"timer.fire(): 1",
			"[info] timer fired",
		)
	}

	s.publish("/devices/somedev/controls/foo", prefix+"t", "somedev/foo")
	s.Verify(
		"tst -> /devices/somedev/controls/foo: [" + prefix + "t] (QoS 1, retained)",
	)
	s.VerifyUnordered(
		"timer.Stop(): 1",
		"new fake timer: 1, 500",
		"new fake timer: 2, 500",
	)

	ts = s.AdvanceTime(5 * 500 * time.Millisecond)
	s.FireTimer(1, ts)
	s.FireTimer(2, ts)
	s.Verify(
		"timer.fire(): 1",
		"timer.fire(): 2",
		"[info] timer fired",
		"[info] timer1 fired",
	)
}

func (s *RuleTimersSuite) TestTimers() {
	s.VerifyTimers("")
}

func (s *RuleTimersSuite) TestDirectTimers() {
	s.VerifyTimers("+")
}

func (s *RuleTimersSuite) TestShortTimers() {
	s.publish("/devices/somedev/controls/foo/meta/type", "text", "somedev/foo")
	s.publish("/devices/somedev/controls/foo", "short", "somedev/foo")

	s.Verify(
		"tst -> /devices/somedev/controls/foo/meta/type: [text] (QoS 1, retained)",
		"tst -> /devices/somedev/controls/foo: [short] (QoS 1, retained)",
		"new fake timer: 1, 1",
		"new fake timer: 2, 1",
		"new fake ticker: 3, 1",
		"new fake ticker: 4, 1",
		"new fake timer: 5, 1",
		"new fake timer: 6, 1",
		"new fake ticker: 7, 1",
		"new fake ticker: 8, 1",
	)
	s.VerifyEmpty()
}

type RuleToplevelTimersSuite struct {
	RuleSuiteBase
}

func (s *RuleToplevelTimersSuite) SetupTest() {
	s.RuleSuiteBase.SetupTest(true, "testrules_topleveltimers.js")
	s.engine.Start()
}

func (s *RuleToplevelTimersSuite) TestToplevelTimers() {
	// make sure timers aren't started until the rule engine is ready
	s.Verify(
		"driver -> /devices/wbrules/meta/name: [Rule Engine Settings] (QoS 1, retained)",
		"Subscribe -- driver: /devices/+/meta/name",
		"Subscribe -- driver: /devices/+/controls/+",
		"Subscribe -- driver: /devices/+/controls/+/meta/type",
		"Subscribe -- driver: /devices/+/controls/+/meta/max",
	)
	s.VerifyEmpty()
	s.Broker.SetReady()
	s.VerifyUnordered(
		"new fake timer: 1, 1000",
		"driver -> /devices/wbrules/controls/Rule debugging/meta/type: [switch] (QoS 1, retained)",
		"driver -> /devices/wbrules/controls/Rule debugging/meta/order: [1] (QoS 1, retained)",
		"driver -> /devices/wbrules/controls/Rule debugging: [0] (QoS 1, retained)",
		"Subscribe -- driver: /devices/wbrules/controls/Rule debugging/on",
	)
	ts := s.AdvanceTime(1000 * time.Millisecond)
	s.FireTimer(1, ts)
	s.Verify(
		"timer.fire(): 1",
		"[info] timer fired",
	)
	s.VerifyEmpty()
}

type RuleRetainedStateSuite struct {
	RuleSuiteBase
}

func (s *RuleRetainedStateSuite) SetupTest() {
	s.RuleSuiteBase.SetupTest(true, "testrules.js")
	s.engine.Start()
}

func (s *RuleRetainedStateSuite) TestRetainedState() {
	s.Verify(
		"driver -> /devices/stabSettings/meta/name: [Stabilization Settings] (QoS 1, retained)",
		"driver -> /devices/wbrules/meta/name: [Rule Engine Settings] (QoS 1, retained)",
		"Subscribe -- driver: /devices/+/meta/name",
		"Subscribe -- driver: /devices/+/controls/+",
		"Subscribe -- driver: /devices/+/controls/+/meta/type",
		"Subscribe -- driver: /devices/+/controls/+/meta/max",
	)
	s.VerifyEmpty()

	s.publish("/devices/stabSettings/controls/enabled", "1", "stabSettings/enabled")
	// lower the threshold so that the rule doesn't fire immediately
	// (which mixes up cell change events during s.publishSomedev())
	s.publish("/devices/stabSettings/controls/lowThreshold", "18", "stabSettings/lowThreshold")
	s.Verify(
		"tst -> /devices/stabSettings/controls/enabled: [1] (QoS 1, retained)",
		"tst -> /devices/stabSettings/controls/lowThreshold: [18] (QoS 1, retained)",
	)
	s.VerifyEmpty()

	s.Broker.SetReady()
	s.Verify(
		"driver -> /devices/stabSettings/controls/enabled/meta/type: [switch] (QoS 1, retained)",
		"driver -> /devices/stabSettings/controls/enabled/meta/order: [1] (QoS 1, retained)",
		"driver -> /devices/stabSettings/controls/enabled: [1] (QoS 1, retained)",
		"Subscribe -- driver: /devices/stabSettings/controls/enabled/on",
		"driver -> /devices/stabSettings/controls/highThreshold/meta/type: [range] (QoS 1, retained)",
		"driver -> /devices/stabSettings/controls/highThreshold/meta/order: [2] (QoS 1, retained)",
		"driver -> /devices/stabSettings/controls/highThreshold/meta/max: [50] (QoS 1, retained)",
		"driver -> /devices/stabSettings/controls/highThreshold: [22] (QoS 1, retained)",
		"Subscribe -- driver: /devices/stabSettings/controls/highThreshold/on",
		"driver -> /devices/stabSettings/controls/lowThreshold/meta/type: [range] (QoS 1, retained)",
		"driver -> /devices/stabSettings/controls/lowThreshold/meta/order: [3] (QoS 1, retained)",
		"driver -> /devices/stabSettings/controls/lowThreshold/meta/max: [40] (QoS 1, retained)",
		"driver -> /devices/stabSettings/controls/lowThreshold: [18] (QoS 1, retained)",
		"Subscribe -- driver: /devices/stabSettings/controls/lowThreshold/on",

		"driver -> /devices/wbrules/controls/Rule debugging/meta/type: [switch] (QoS 1, retained)",
		"driver -> /devices/wbrules/controls/Rule debugging/meta/order: [1] (QoS 1, retained)",
		"driver -> /devices/wbrules/controls/Rule debugging: [0] (QoS 1, retained)",
		"Subscribe -- driver: /devices/wbrules/controls/Rule debugging/on",
	)
	s.publishSomedev()
	s.Verify(
		"tst -> /devices/somedev/meta/name: [SomeDev] (QoS 1, retained)",
		"tst -> /devices/somedev/controls/sw/meta/type: [switch] (QoS 1, retained)",
		"tst -> /devices/somedev/controls/sw: [0] (QoS 1, retained)",
		"tst -> /devices/somedev/controls/temp/meta/type: [temperature] (QoS 1, retained)",
		"tst -> /devices/somedev/controls/temp: [19] (QoS 1, retained)",
	)
	s.publish("/devices/somedev/controls/temp", "16", "somedev/temp")
	s.Verify(
		"tst -> /devices/somedev/controls/temp: [16] (QoS 1, retained)",
		"[info] heaterOn fired, changed: somedev/temp -> 16",
		"driver -> /devices/somedev/controls/sw/on: [1] (QoS 1)",
	)
	s.publish("/devices/somedev/controls/sw", "1", "somedev/sw")
	s.Verify(
		"tst -> /devices/somedev/controls/sw: [1] (QoS 1, retained)",
	)
	s.VerifyEmpty()
}

type RuleLocalButtonSuite struct {
	RuleSuiteBase
}

func (s *RuleLocalButtonSuite) SetupTest() {
	s.RuleSuiteBase.SetupTest(false, "testrules_localbutton.js")
	s.engine.Start()
}

func (s *RuleLocalButtonSuite) TestLocalButtons() {
	s.Verify(
		"driver -> /devices/buttons/meta/name: [Button Test] (QoS 1, retained)",
		"driver -> /devices/wbrules/meta/name: [Rule Engine Settings] (QoS 1, retained)",
		"Subscribe -- driver: /devices/+/meta/name",
		"Subscribe -- driver: /devices/+/controls/+",
		"Subscribe -- driver: /devices/+/controls/+/meta/type",
		"Subscribe -- driver: /devices/+/controls/+/meta/max",
		"driver -> /devices/buttons/controls/somebutton/meta/type: [pushbutton] (QoS 1, retained)",
		"driver -> /devices/buttons/controls/somebutton/meta/order: [1] (QoS 1, retained)",
		"Subscribe -- driver: /devices/buttons/controls/somebutton/on",

		"driver -> /devices/wbrules/controls/Rule debugging/meta/type: [switch] (QoS 1, retained)",
		"driver -> /devices/wbrules/controls/Rule debugging/meta/order: [1] (QoS 1, retained)",
		"driver -> /devices/wbrules/controls/Rule debugging: [0] (QoS 1, retained)",
		"Subscribe -- driver: /devices/wbrules/controls/Rule debugging/on",
		// FIXME: don't need these here
		"tst -> /devices/somedev/meta/name: [SomeDev] (QoS 1, retained)",
		"tst -> /devices/somedev/controls/sw/meta/type: [switch] (QoS 1, retained)",
		"tst -> /devices/somedev/controls/sw: [0] (QoS 1, retained)",
		"tst -> /devices/somedev/controls/temp/meta/type: [temperature] (QoS 1, retained)",
		"tst -> /devices/somedev/controls/temp: [19] (QoS 1, retained)",
	)
	s.VerifyEmpty()

	for i := 0; i < 3; i++ {
		// The change rule must be fired on each button press ('1' .../on value message)
		s.publish("/devices/buttons/controls/somebutton/on", "1", "buttons/somebutton")
		s.Verify(
			"tst -> /devices/buttons/controls/somebutton/on: [1] (QoS 1)",
			"driver -> /devices/buttons/controls/somebutton: [1] (QoS 1)", // note there's no 'retained' flag
			"[info] button pressed!",
		)
	}
}

type RuleShellCommandSuite struct {
	RuleSuiteBase
}

func (s *RuleShellCommandSuite) SetupTest() {
	s.SetupSkippingDefs("testrules_command.js")
}

func (s *RuleShellCommandSuite) fileExists(path string) bool {
	if _, err := os.Stat(path); err != nil {
		if os.IsNotExist(err) {
			return false
		} else {
			s.Require().Fail("unexpected error when checking for samplefile", "%s", err)
		}
	}
	return true
}

func (s *RuleShellCommandSuite) verifyFileExists(path string) {
	if !s.fileExists(path) {
		s.Require().Fail("file does not exist", "%s", path)
	}
}

func (s *RuleShellCommandSuite) TestRunShellCommand() {
	dir, cleanup := wbgo.SetupTempDir(s.T())
	defer cleanup()

	s.publish("/devices/somedev/controls/cmd/meta/type", "text", "somedev/cmd")
	s.publish("/devices/somedev/controls/cmdNoCallback/meta/type", "text",
		"somedev/cmdNoCallback")
	s.publish("/devices/somedev/controls/cmd", "touch samplefile.txt", "somedev/cmd")
	s.Verify(
		"tst -> /devices/somedev/controls/cmd/meta/type: [text] (QoS 1, retained)",
		"tst -> /devices/somedev/controls/cmdNoCallback/meta/type: [text] (QoS 1, retained)",
		"tst -> /devices/somedev/controls/cmd: [touch samplefile.txt] (QoS 1, retained)",
		"[info] cmd: touch samplefile.txt",
		"[info] exit(0): touch samplefile.txt",
	)

	s.verifyFileExists(path.Join(dir, "samplefile.txt"))

	s.publish("/devices/somedev/controls/cmd", "touch nosuchdir/samplefile.txt 2>/dev/null", "somedev/cmd")
	s.Verify(
		"tst -> /devices/somedev/controls/cmd: [touch nosuchdir/samplefile.txt 2>/dev/null] (QoS 1, retained)",
		"[info] cmd: touch nosuchdir/samplefile.txt 2>/dev/null",
		"[info] exit(1): touch nosuchdir/samplefile.txt 2>/dev/null", // no such file or directory
	)

	s.publish("/devices/somedev/controls/cmdNoCallback", "1", "somedev/cmdNoCallback")
	s.publish("/devices/somedev/controls/cmd", "touch samplefile1.txt", "somedev/cmd")
	s.Verify(
		"tst -> /devices/somedev/controls/cmdNoCallback: [1] (QoS 1, retained)",
		"tst -> /devices/somedev/controls/cmd: [touch samplefile1.txt] (QoS 1, retained)",
		"[info] cmd: touch samplefile1.txt",
		"[info] (no callback)",
	)
	s.WaitFor(func() bool {
		return s.fileExists(path.Join(dir, "samplefile1.txt"))
	})
}

func (s *RuleShellCommandSuite) TestRunShellCommandIO() {
	s.publish("/devices/somedev/controls/cmdWithOutput/meta/type", "text",
		"somedev/cmdWithOutput")
	s.publish("/devices/somedev/controls/cmdWithOutput", "echo abc; echo qqq",
		"somedev/cmdWithOutput")
	s.Verify(
		"tst -> /devices/somedev/controls/cmdWithOutput/meta/type: [text] (QoS 1, retained)",
		"tst -> /devices/somedev/controls/cmdWithOutput: [echo abc; echo qqq] (QoS 1, retained)",
		"[info] cmdWithOutput: echo abc; echo qqq",
		"[info] exit(0): echo abc; echo qqq",
		"[info] output: abc",
		"[info] output: qqq",
	)

	s.publish("/devices/somedev/controls/cmdWithOutput", "echo abc; echo qqq 1>&2; exit 1",
		"somedev/cmdWithOutput")
	s.Verify(
		"tst -> /devices/somedev/controls/cmdWithOutput: [echo abc; echo qqq 1>&2; exit 1] (QoS 1, retained)",
		"[info] cmdWithOutput: echo abc; echo qqq 1>&2; exit 1",
		"[info] exit(1): echo abc; echo qqq 1>&2; exit 1",
		"[info] output: abc",
		"[info] error: qqq",
	)

	s.publish("/devices/somedev/controls/cmdWithOutput", "xxyz!sed s/x/y/g",
		"somedev/cmdWithOutput")
	s.Verify(
		"tst -> /devices/somedev/controls/cmdWithOutput: [xxyz!sed s/x/y/g] (QoS 1, retained)",
		"[info] cmdWithOutput: sed s/x/y/g",
		"[info] exit(0): sed s/x/y/g",
		"[info] output: yyyz",
	)
}

type RuleOptimizationSuite struct {
	RuleSuiteBase
}

func (s *RuleOptimizationSuite) SetupTest() {
	s.SetupSkippingDefs("testrules_opt.js")
}

func (s *RuleOptimizationSuite) TestRuleCheckOptimization() {
	s.Verify(
		// That's the first time when all rules are run.
		// somedev/countIt and somedev/countItLT are incomplete here, but
		// the engine notes that rules' conditions depend on the cells
		"[info] condCount: asSoonAs()",
		"[info] condCountLT: when()",
	)
	s.publish("/devices/somedev/controls/countIt/meta/type", "text", "somedev/countIt")
	s.publish("/devices/somedev/controls/countIt", "0", "somedev/countIt")
	s.Verify(
		"tst -> /devices/somedev/controls/countIt/meta/type: [text] (QoS 1, retained)",
		"tst -> /devices/somedev/controls/countIt: [0] (QoS 1, retained)",
		// here the value of the cell changes, so the rule is invoked
		"[info] condCount: asSoonAs()")

	s.publish("/devices/somedev/controls/temp", "25", "somedev/temp")
	s.publish("/devices/somedev/controls/countIt", "42", "somedev/countIt")
	s.Verify(
		"tst -> /devices/somedev/controls/temp: [25] (QoS 1, retained)",
		// changing unrelated cell doesn't cause the rule to be invoked
		"tst -> /devices/somedev/controls/countIt: [42] (QoS 1, retained)",
		"[info] condCount: asSoonAs()",
		// asSoonAs function called during the first run + when countIt
		// value changed to 42
		"[info] condCount fired, count=3",
		// ruleWithoutCells follows condCount rule in testrules.js
		// and doesn't utilize any cells. It's run just once when condCount
		// rule sets a global variable to true.
		"[info] ruleWithoutCells fired")

	s.publish("/devices/somedev/controls/countIt", "0", "somedev/countIt")
	s.Verify(
		"tst -> /devices/somedev/controls/countIt: [0] (QoS 1, retained)",
		"[info] condCount: asSoonAs()")
	s.publish("/devices/somedev/controls/countIt", "42", "somedev/countIt")
	s.Verify(
		"tst -> /devices/somedev/controls/countIt: [42] (QoS 1, retained)",
		"[info] condCount: asSoonAs()",
		"[info] condCount fired, count=5")

	// now check optimization of level-triggered rules
	s.publish("/devices/somedev/controls/countItLT/meta/type", "text", "somedev/countItLT")
	s.publish("/devices/somedev/controls/countItLT", "0", "somedev/countItLT")
	s.Verify(
		"tst -> /devices/somedev/controls/countItLT/meta/type: [text] (QoS 1, retained)",
		"tst -> /devices/somedev/controls/countItLT: [0] (QoS 1, retained)",
		// here the value of the cell changes, so the rule is invoked
		"[info] condCountLT: when()")

	s.publish("/devices/somedev/controls/countItLT", "42", "somedev/countItLT")
	s.Verify(
		"tst -> /devices/somedev/controls/countItLT: [42] (QoS 1, retained)",
		"[info] condCountLT: when()",
		// when function called during the first run + when countItLT
		// value changed to 42
		"[info] condCountLT fired, count=3")

	s.publish("/devices/somedev/controls/countItLT", "43", "somedev/countItLT")
	s.Verify(
		"tst -> /devices/somedev/controls/countItLT: [43] (QoS 1, retained)",
		"[info] condCountLT: when()",
		"[info] condCountLT fired, count=4")

	s.publish("/devices/somedev/controls/countItLT", "0", "somedev/countItLT")
	s.Verify(
		"tst -> /devices/somedev/controls/countItLT: [0] (QoS 1, retained)",
		"[info] condCountLT: when()")

	s.publish("/devices/somedev/controls/countItLT", "1", "somedev/countItLT")
	s.Verify(
		"tst -> /devices/somedev/controls/countItLT: [1] (QoS 1, retained)",
		"[info] condCountLT: when()")
}

type RuleReadOnlyCellSuite struct {
	RuleSuiteBase
}

func (s *RuleReadOnlyCellSuite) SetupTest() {
	s.RuleSuiteBase.SetupTest(false, "testrules_readonly.js")
}

func (s *RuleReadOnlyCellSuite) TestReadOnlyCells() {
	s.Verify(
		"driver -> /devices/roCells/meta/name: [Readonly Cell Test] (QoS 1, retained)",
		"driver -> /devices/wbrules/meta/name: [Rule Engine Settings] (QoS 1, retained)",
		"Subscribe -- driver: /devices/+/meta/name",
		"Subscribe -- driver: /devices/+/controls/+",
		"Subscribe -- driver: /devices/+/controls/+/meta/type",
		"Subscribe -- driver: /devices/+/controls/+/meta/max",
		"driver -> /devices/roCells/controls/rocell/meta/type: [switch] (QoS 1, retained)",
		"driver -> /devices/roCells/controls/rocell/meta/readonly: [1] (QoS 1, retained)",
		"driver -> /devices/roCells/controls/rocell/meta/order: [1] (QoS 1, retained)",
		"driver -> /devices/roCells/controls/rocell: [0] (QoS 1, retained)",
		"driver -> /devices/wbrules/controls/Rule debugging/meta/type: [switch] (QoS 1, retained)",
		"driver -> /devices/wbrules/controls/Rule debugging/meta/order: [1] (QoS 1, retained)",
		"driver -> /devices/wbrules/controls/Rule debugging: [0] (QoS 1, retained)",
		"Subscribe -- driver: /devices/wbrules/controls/Rule debugging/on",
		"tst -> /devices/somedev/meta/name: [SomeDev] (QoS 1, retained)",
		"tst -> /devices/somedev/controls/sw/meta/type: [switch] (QoS 1, retained)",
		"tst -> /devices/somedev/controls/sw: [0] (QoS 1, retained)",
		"tst -> /devices/somedev/controls/temp/meta/type: [temperature] (QoS 1, retained)",
		"tst -> /devices/somedev/controls/temp: [19] (QoS 1, retained)",
	)
}

type RuleCronSuite struct {
	RuleSuiteBase
}

func (s *RuleCronSuite) SetupTest() {
	s.SetupSkippingDefs("testrules_cron.js")
}

func (s *RuleCronSuite) TestCron() {
	s.WaitFor(func() bool {
		c := make(chan bool)
		s.model.CallSync(func() {
			c <- s.cron != nil && s.cron.started
		})
		return <-c
	})

	s.cron.invokeEntries("@hourly")
	s.cron.invokeEntries("@hourly")
	s.cron.invokeEntries("@daily")
	s.cron.invokeEntries("@hourly")

	s.Verify(
		"[info] @hourly rule fired",
		"[info] @hourly rule fired",
		"[info] @daily rule fired",
		"[info] @hourly rule fired",
	)

	// the new script contains rules with same names as in
	// testrules_cron.js that should override the previous rules
	s.ReplaceScript("testrules_cron.js", "testrules_cron_changed.js")
	s.Verify(
		"driver -> /wbrules/updates/changed: [testrules_cron.js] (QoS 1)",
	)

	s.cron.invokeEntries("@hourly")
	s.cron.invokeEntries("@hourly")
	s.cron.invokeEntries("@daily")
	s.cron.invokeEntries("@hourly")

	s.Verify(
		"[info] @hourly rule fired (new)",
		"[info] @hourly rule fired (new)",
		"[info] @daily rule fired (new)",
		"[info] @hourly rule fired (new)",
	)
}

type RuleReloadSuite struct {
	RuleSuiteBase
}

func (s *RuleReloadSuite) SetupTest() {
	s.SetupSkippingDefs("testrules_reload_1.js", "testrules_reload_2.js")
	s.Verify(
		"[info] detRun",
		"[info] detectRun: (no cell) (s=false, a=10)",
		"[info] detectRun1: (no cell) (s=false, a=10)",
	)
}

func (s *RuleReloadSuite) TestReload() {
	s.publish("/devices/vdev/controls/someCell/on", "1", "vdev/someCell")
	s.Verify(
		"tst -> /devices/vdev/controls/someCell/on: [1] (QoS 1)",
		"driver -> /devices/vdev/controls/someCell: [1] (QoS 1, retained)",
		"[info] detRun",
		"[info] detectRun: vdev/someCell (s=true, a=10)",
		"[info] detectRun1: vdev/someCell (s=true, a=10)",
		"[info] rule1: vdev/someCell=true",
		"[info] rule2: vdev/someCell=true",
	)

	s.publish("/devices/vdev/controls/anotherCell/on", "17", "vdev/anotherCell")
	s.Verify(
		"tst -> /devices/vdev/controls/anotherCell/on: [17] (QoS 1)",
		"driver -> /devices/vdev/controls/anotherCell: [17] (QoS 1, retained)",
		"[info] detRun",
		"[info] detectRun: vdev/anotherCell (s=true, a=17)",
		"[info] detectRun1: vdev/anotherCell (s=true, a=17)",
		"[info] rule3: vdev/anotherCell=17",
	)

	s.ReplaceScript("testrules_reload_2.js", "testrules_reload_2_changed.js")
	s.Verify(
		// devices are removed when the older version if the
		// script is unloaded
		"Unsubscribe -- driver: /devices/vdev/controls/anotherCell/on",
		"Unsubscribe -- driver: /devices/vdev/controls/someCell/on",
		// vdev1 is not redefined after reload, but must be still
		// removed
		"Unsubscribe -- driver: /devices/vdev1/controls/qqq/on",
		// device redefinition begins
		"driver -> /devices/vdev/meta/name: [VDev] (QoS 1, retained)",
		"driver -> /devices/vdev/controls/someCell/meta/type: [switch] (QoS 1, retained)",
		"driver -> /devices/vdev/controls/someCell/meta/order: [1] (QoS 1, retained)",
		// value '1' of the switch from the retained message
		"driver -> /devices/vdev/controls/someCell: [1] (QoS 1, retained)",
		"Subscribe -- driver: /devices/vdev/controls/someCell/on",
		// rules are run after reload
		"[info] detRun",
		"[info] detectRun: (no cell) (s=true)",
		// change notification for the client-side script editor
		"driver -> /wbrules/updates/changed: [testrules_reload_2.js] (QoS 1)",
	)

	// this one must be ignored because anotherCell is no longer there
	// after the device is redefined
	s.publish("/devices/vdev/controls/anotherCell/on", "11")
	s.publish("/devices/vdev/controls/someCell/on", "0", "vdev/someCell")

	s.Verify(
		"tst -> /devices/vdev/controls/anotherCell/on: [11] (QoS 1)",
		"tst -> /devices/vdev/controls/someCell/on: [0] (QoS 1)",
		"driver -> /devices/vdev/controls/someCell: [0] (QoS 1, retained)",
		"[info] detRun",
		"[info] detectRun: vdev/someCell (s=false)",
		"[info] rule1: vdev/someCell=false",
		// rule2 is gone, rule3 is gone together with its anotherCell
	)

	s.publish("/devices/vdev/controls/someCell/on", "1", "vdev/someCell")
	s.Verify(
		"tst -> /devices/vdev/controls/someCell/on: [1] (QoS 1)",
		"driver -> /devices/vdev/controls/someCell: [1] (QoS 1, retained)",
		"[info] detRun",
		"[info] detectRun: vdev/someCell (s=true)",
		"[info] rule1: vdev/someCell=true",
	)

	// TBD: stop any timers started while evaluating the script.
	// This will require extra care because cleanup procedure
	// for the timer must be revoked once the timer is stopped.
}

func (s *RuleReloadSuite) TestRemoveScript() {
	s.RemoveScript("testrules_reload_2.js")
	s.Verify(
		// devices are removed
		"Unsubscribe -- driver: /devices/vdev/controls/anotherCell/on",
		"Unsubscribe -- driver: /devices/vdev/controls/someCell/on",
		"Unsubscribe -- driver: /devices/vdev1/controls/qqq/on",
		// rules are run after removal
		"[info] detRun",
		// removal notification for the client-side script editor
		"driver -> /wbrules/updates/removed: [testrules_reload_2.js] (QoS 1)",
	)

	// both ignored (cells are no longer there)
	s.publish("/devices/vdev/controls/anotherCell/on", "11")
	s.publish("/devices/vdev/controls/someCell/on", "0")

	s.Verify(
		"tst -> /devices/vdev/controls/anotherCell/on: [11] (QoS 1)",
		"tst -> /devices/vdev/controls/someCell/on: [0] (QoS 1)",
	)

	// vdev0 is intact because it's from testrules_reload_1.js
	s.publish("/devices/vdev0/controls/someCell/on", "1", "vdev0/someCell")
	s.Verify(
		"tst -> /devices/vdev0/controls/someCell/on: [1] (QoS 1)",
		"driver -> /devices/vdev0/controls/someCell: [1] (QoS 1, retained)",
		"[info] detRun",
	)
}

type RuleCellChangesSuite struct {
	RuleSuiteBase
}

func (s *RuleCellChangesSuite) SetupTest() {
	s.SetupSkippingDefs("testrules_cellchanges.js")
}

func (s *RuleCellChangesSuite) TestAssigningSameValueToACellSeveralTimes() {
	s.publish("/devices/cellch/controls/button/on", "1",
		"cellch/button", "cellch/sw", "cellch/misc")
	s.Verify(
		"tst -> /devices/cellch/controls/button/on: [1] (QoS 1)",
		"driver -> /devices/cellch/controls/button: [1] (QoS 1)", // no 'retained' flag for button
		"driver -> /devices/cellch/controls/sw: [1] (QoS 1, retained)",
		"driver -> /devices/cellch/controls/misc: [1] (QoS 1, retained)",
		"[info] startCellChange: sw <- true",
		"[info] switchChanged: sw=true",
		"driver -> /devices/somedev/controls/sw/on: [1] (QoS 1)",
	)
	s.publish("/devices/somedev/controls/sw", "1", "somedev/sw")
	s.Verify(
		"tst -> /devices/somedev/controls/sw: [1] (QoS 1, retained)",
	)

	s.publish("/devices/cellch/controls/button/on", "1",
		"cellch/button", "cellch/sw", "cellch/misc")
	s.Verify(
		"tst -> /devices/cellch/controls/button/on: [1] (QoS 1)",
		"driver -> /devices/cellch/controls/button: [1] (QoS 1)", // no 'retained' flag for button
		"driver -> /devices/cellch/controls/sw: [0] (QoS 1, retained)",
		"driver -> /devices/cellch/controls/misc: [1] (QoS 1, retained)",
		"[info] startCellChange: sw <- false",
		"[info] switchChanged: sw=false",
		"driver -> /devices/somedev/controls/sw/on: [1] (QoS 1)",
	)
	s.publish("/devices/somedev/controls/sw", "1", "somedev/sw")
	s.Verify(
		"tst -> /devices/somedev/controls/sw: [1] (QoS 1, retained)",
	)
}

type LocationSuite struct {
	RuleSuiteBase
}

func (s *LocationSuite) SetupTest() {
	s.SetupSkippingDefs(
		"testrules_defhelper.js",
		"testrules_locations.js",
		"loc1/testrules_more.js")
	// FIXME: need to wait for the engine to become ready because
	// the engine cannot be stopped before it's ready in the
	// context of the tests.
	ready := false
	var mtx sync.Mutex
	s.model.WhenReady(func() {
		mtx.Lock()
		ready = true
		mtx.Unlock()
	})
	s.WaitFor(func() bool {
		mtx.Lock()
		defer mtx.Unlock()
		return ready
	})
}

func (s *LocationSuite) listSourceFiles() (entries []LocFileEntry) {
	var err error
	entries, err = s.engine.ListSourceFiles()
	s.Ck("ListSourceFiles", err)
	return
}

func (s *LocationSuite) TestLocations() {
	s.Equal([]LocFileEntry{
		{
			VirtualPath:  "loc1/testrules_more.js",
			PhysicalPath: s.ScriptPath("loc1/testrules_more.js"),
			Devices: []LocItem{
				{4, "qqq"},
			},
			Rules: []LocItem{},
		},
		{
			VirtualPath:  "testrules_defhelper.js",
			PhysicalPath: s.ScriptPath("testrules_defhelper.js"),
			Devices:      []LocItem{},
			Rules:        []LocItem{},
		},
		{
			VirtualPath:  "testrules_locations.js",
			PhysicalPath: s.ScriptPath("testrules_locations.js"),
			Devices: []LocItem{
				{4, "misc"},
				{14, "foo"},
			},
			Rules: []LocItem{
				{7, "whateverRule"},
				// the problem with duktape: the last line of the
				// defineRule() call is recorded
				{24, "another"},
			},
		},
	}, s.listSourceFiles())
}

func (s *LocationSuite) TestUpdatingLocations() {
	s.ReplaceScript("testrules_locations.js", "testrules_locations_changed.js")
	s.ReplaceScript("loc1/testrules_more.js", "loc1/testrules_more_changed.js")
	s.Equal([]LocFileEntry{
		{
			VirtualPath:  "loc1/testrules_more.js",
			PhysicalPath: s.ScriptPath("loc1/testrules_more.js"),
			Devices: []LocItem{
				{4, "qqqNew"},
			},
			Rules: []LocItem{},
		},
		{
			VirtualPath:  "testrules_defhelper.js",
			PhysicalPath: s.ScriptPath("testrules_defhelper.js"),
			Devices:      []LocItem{},
			Rules:        []LocItem{},
		},
		{
			VirtualPath:  "testrules_locations.js",
			PhysicalPath: s.ScriptPath("testrules_locations.js"),
			Devices: []LocItem{
				{4, "miscNew"},
				{14, "foo"},
			},
			Rules: []LocItem{
				{7, "whateverNewRule"},
				// a problem with duktape: the last line of the
				// defineRule() call is recorded
				{24, "another"},
			},
		},
	}, s.listSourceFiles())
}

func (s *LocationSuite) TestRemoval() {
	s.RemoveScript("testrules_locations.js")
	s.WaitFor(func() bool {
		return len(s.listSourceFiles()) == 2
	})
	s.Equal([]LocFileEntry{
		{
			VirtualPath:  "loc1/testrules_more.js",
			PhysicalPath: s.ScriptPath("loc1/testrules_more.js"),
			Devices: []LocItem{
				{4, "qqq"},
			},
			Rules: []LocItem{},
		},
		{
			VirtualPath:  "testrules_defhelper.js",
			PhysicalPath: s.ScriptPath("testrules_defhelper.js"),
			Devices:      []LocItem{},
			Rules:        []LocItem{},
		},
	}, s.listSourceFiles())

	s.RemoveScript("loc1/testrules_more.js")
	s.WaitFor(func() bool {
		return len(s.listSourceFiles()) == 1
	})
	s.Equal([]LocFileEntry{
		{
			VirtualPath:  "testrules_defhelper.js",
			PhysicalPath: s.ScriptPath("testrules_defhelper.js"),
			Devices:      []LocItem{},
			Rules:        []LocItem{},
		},
	}, s.listSourceFiles())
}

type LogSuite struct {
	RuleSuiteBase
}

func (s *LogSuite) SetupTest() {
	s.SetupSkippingDefs("testrules_log.js")
}

func (s *LogSuite) TestLog() {
	s.engine.EvalScript("testLog()")
	s.Verify(
		"[info] log()",
		"[info] log.info(42)",
		"[warning] log.warning(42)",
		"[error] log.error(42)",
	)
	s.publish("/devices/wbrules/controls/Rule debugging/on", "1", "wbrules/Rule debugging")
	s.Verify(
		"tst -> /devices/wbrules/controls/Rule debugging/on: [1] (QoS 1)",
		"driver -> /devices/wbrules/controls/Rule debugging: [1] (QoS 1, retained)",
	)
	s.engine.EvalScript("testLog()")
	s.Verify(
		"[info] log()",
		"[debug] debug()",
		"[debug] log.debug(42)",
		"[info] log.info(42)",
		"[warning] log.warning(42)",
		"[error] log.error(42)",
	)
}

func TestRuleSuite(t *testing.T) {
	wbgo.RunSuites(t,
		new(RuleDefSuite),
		new(RuleBasicsSuite),
		new(RuleTimersSuite),
		new(RuleToplevelTimersSuite),
		new(RuleRetainedStateSuite),
		new(RuleLocalButtonSuite),
		new(RuleShellCommandSuite),
		new(RuleOptimizationSuite),
		new(RuleReadOnlyCellSuite),
		new(RuleCronSuite),
		new(RuleReloadSuite),
		new(RuleCellChangesSuite),
		new(LocationSuite),
		new(LogSuite),
	)
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
