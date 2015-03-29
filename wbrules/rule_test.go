package wbrules

import (
	"os"
	"path"
	"time"
	"io/ioutil"
	"testing"
	"github.com/stretchr/testify/assert"
	wbgo "github.com/contactless/wbgo"
)

var baseRuleTestTime = time.Date(2015, 2, 27, 19, 33, 17, 0, time.UTC)

func makeTime(d time.Duration) time.Time {
	return baseRuleTestTime.Add(d)
}

type fakeTimer struct {
	t *testing.T
	id int
	c chan time.Time
	d time.Duration
	periodic bool
	active bool
	rec *wbgo.Recorder
}

func (timer *fakeTimer) GetChannel() <-chan time.Time {
	return timer.c
}

func (timer *fakeTimer) fire(t time.Time) {
	timer.rec.Rec("timer.fire(): %d", timer.id)
	assert.True(timer.t, timer.active)
	timer.c <- t
	if !timer.periodic {
		timer.active = false
	}
}

func (timer *fakeTimer) Stop() {
	// note that we don't close timer here,
	// mimicking the behavior of real timers and tickers
	timer.active = false
	timer.rec.Rec("timer.Stop(): %d", timer.id)
}

type ruleFixture struct {
	cellFixture
	engine *RuleEngine
	timers map[int]*fakeTimer
}

func NewRuleFixture(t *testing.T, waitForRetained bool, ruleFile string) *ruleFixture {
	fixture := &ruleFixture{
		*NewCellFixture(t, waitForRetained),
		nil,
		make(map[int]*fakeTimer),
	}
	fixture.engine = NewRuleEngine(fixture.model, fixture.driverClient)
	fixture.engine.SetTimerFunc(fixture.newFakeTimer)
	fixture.engine.SetLogFunc(func (message string) {
		fixture.broker.Rec("[rule] %s", message)
	})
	assert.Equal(t, nil, fixture.engine.LoadScript(ruleFile))
	fixture.driver.Start()
	fixture.publish("/devices/somedev/meta/name", "SomeDev", "")
	fixture.publish("/devices/somedev/controls/sw/meta/type", "switch", "somedev/sw")
	fixture.publish("/devices/somedev/controls/sw", "0", "somedev/sw")
	fixture.publish("/devices/somedev/controls/temp/meta/type", "temperature", "somedev/temp")
	fixture.publish("/devices/somedev/controls/temp", "19", "somedev/temp")
	return fixture
}

func NewRuleFixtureSkippingDefs(t *testing.T, ruleFile string) (fixture *ruleFixture) {
	fixture = NewRuleFixture(t, false, ruleFile)
	fixture.broker.SkipTill("tst -> /devices/somedev/controls/temp: [19] (QoS 1, retained)")
	fixture.engine.Start() // FIXME: should auto-start
	return
}

func (fixture *ruleFixture) newFakeTimer(id int, d time.Duration, periodic bool) Timer {
	timer := &fakeTimer{
		t: fixture.t,
		id: id,
		c: make(chan time.Time),
		d: d,
		periodic: periodic,
		active: true,
		rec: &fixture.broker.Recorder,
	}
	fixture.timers[id] = timer
	fixture.broker.Rec("newFakeTimer(): %d, %d, %v", id, d / time.Millisecond, periodic)
	return timer
}

func (fixture *ruleFixture) Verify(logs... string) {
	fixture.broker.Verify(logs...)
}

func (fixture *ruleFixture) VerifyUnordered(logs... string) {
	fixture.broker.VerifyUnordered(logs...)
}

func (fixture *ruleFixture) SetCellValue(device, cellName string, value interface{}) {
	fixture.driver.CallSync(func () {
		fixture.model.EnsureDevice(device).EnsureCell(cellName).SetValue(value)
	})
	actualCellSpec := <- fixture.cellChange
	assert.Equal(fixture.t, device + "/" + cellName,
		actualCellSpec.DevName + "/" + actualCellSpec.CellName)
}

func TestDeviceDefinition(t *testing.T) {
	fixture := NewRuleFixture(t, false, "testrules.js")
	defer fixture.tearDown()
	fixture.Verify(
		"driver -> /devices/stabSettings/meta/name: [Stabilization Settings] (QoS 1, retained)",
		"Subscribe -- driver: /devices/+/meta/name",
		"Subscribe -- driver: /devices/+/controls/+",
		"Subscribe -- driver: /devices/+/controls/+/meta/type",
		"Subscribe -- driver: /devices/+/controls/+/meta/max",
		"tst -> /devices/somedev/meta/name: [SomeDev] (QoS 1, retained)",
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
		"tst -> /devices/somedev/controls/sw/meta/type: [switch] (QoS 1, retained)",
		"tst -> /devices/somedev/controls/sw: [0] (QoS 1, retained)",
		"tst -> /devices/somedev/controls/temp/meta/type: [temperature] (QoS 1, retained)",
		"tst -> /devices/somedev/controls/temp: [19] (QoS 1, retained)",
	)
}

func TestRules(t *testing.T) {
	fixture := NewRuleFixtureSkippingDefs(t, "testrules.js")
	defer fixture.tearDown()

	fixture.SetCellValue("stabSettings", "enabled", true)
	fixture.Verify(
		"driver -> /devices/stabSettings/controls/enabled: [1] (QoS 1, retained)",
		"[rule] heaterOn fired",
 		"driver -> /devices/somedev/controls/sw/on: [1] (QoS 1)",
	)
	fixture.expectCellChange("somedev/sw")

	fixture.publish("/devices/somedev/controls/temp", "21", "somedev/temp")
	fixture.Verify(
		"tst -> /devices/somedev/controls/temp: [21] (QoS 1, retained)",
	)

	fixture.publish("/devices/somedev/controls/temp", "22", "somedev/temp", "somedev/sw")
	fixture.Verify(
		"tst -> /devices/somedev/controls/temp: [22] (QoS 1, retained)",
		"[rule] heaterOff fired",
 		"driver -> /devices/somedev/controls/sw/on: [0] (QoS 1)",
	)

	fixture.publish("/devices/somedev/controls/temp", "18", "somedev/temp", "somedev/sw")
	fixture.Verify(
		"tst -> /devices/somedev/controls/temp: [18] (QoS 1, retained)",
		"[rule] heaterOn fired",
 		"driver -> /devices/somedev/controls/sw/on: [1] (QoS 1)",
	)

	// edge-triggered rule doesn't fire
	fixture.publish("/devices/somedev/controls/temp", "19", "somedev/temp")
	fixture.Verify(
		"tst -> /devices/somedev/controls/temp: [19] (QoS 1, retained)",
	)

	fixture.SetCellValue("stabSettings", "enabled", false)
	fixture.Verify(
		"driver -> /devices/stabSettings/controls/enabled: [0] (QoS 1, retained)",
		"[rule] heaterOff fired",
 		"driver -> /devices/somedev/controls/sw/on: [0] (QoS 1)",
	)
	fixture.expectCellChange("somedev/sw")

	fixture.publish("/devices/somedev/controls/foobar", "1", "somedev/foobar")
	fixture.publish("/devices/somedev/controls/foobar/meta/type", "text", "somedev/foobar")
	fixture.Verify(
		"tst -> /devices/somedev/controls/foobar: [1] (QoS 1, retained)",
		"tst -> /devices/somedev/controls/foobar/meta/type: [text] (QoS 1, retained)",
		"[rule] initiallyIncompleteLevelTriggered fired",
	)

	// level-triggered rule fires again here
	fixture.publish("/devices/somedev/controls/foobar", "2", "somedev/foobar")
	fixture.Verify(
		"tst -> /devices/somedev/controls/foobar: [2] (QoS 1, retained)",
		"[rule] initiallyIncompleteLevelTriggered fired",
	)
}

func (fixture *ruleFixture) VerifyTimers(prefix string) {
	fixture.publish("/devices/somedev/controls/foo/meta/type", "text", "somedev/foo")
	fixture.publish("/devices/somedev/controls/foo", prefix + "t", "somedev/foo")
	fixture.Verify(
		"tst -> /devices/somedev/controls/foo/meta/type: [text] (QoS 1, retained)",
		"tst -> /devices/somedev/controls/foo: [" + prefix + "t] (QoS 1, retained)",
		"newFakeTimer(): 1, 500, false",
	)

	fixture.publish("/devices/somedev/controls/foo", prefix + "s", "somedev/foo")
	fixture.Verify(
		"tst -> /devices/somedev/controls/foo: [" + prefix + "s] (QoS 1, retained)",
		"timer.Stop(): 1",
	)

	fixture.publish("/devices/somedev/controls/foo", prefix + "t", "somedev/foo")
	fixture.Verify(
		"tst -> /devices/somedev/controls/foo: [" + prefix + "t] (QoS 1, retained)",
		"newFakeTimer(): 1, 500, false",
	)

	fixture.timers[1].fire(makeTime(500 * time.Millisecond))
	fixture.Verify(
		"timer.fire(): 1",
		"[rule] timer fired",
	)

	fixture.publish("/devices/somedev/controls/foo", prefix + "p", "somedev/foo")
	fixture.Verify(
		"tst -> /devices/somedev/controls/foo: [" + prefix + "p] (QoS 1, retained)",
		"newFakeTimer(): 1, 500, true",
	)

	for i := 1; i < 4; i++ {
		targetTime := makeTime(time.Duration(500 * i) * time.Millisecond)
		fixture.timers[1].fire(targetTime)
		fixture.Verify(
			"timer.fire(): 1",
			"[rule] timer fired",
		)
	}

	fixture.publish("/devices/somedev/controls/foo", prefix + "t", "somedev/foo")
	fixture.Verify(
		"tst -> /devices/somedev/controls/foo: [" + prefix + "t] (QoS 1, retained)",
	)
	fixture.VerifyUnordered(
		"timer.Stop(): 1",
		"newFakeTimer(): 1, 500, false",
	)

	fixture.timers[1].fire(makeTime(5 * 500 * time.Millisecond))
	fixture.Verify(
		"timer.fire(): 1",
		"[rule] timer fired",
	)
}

func TestTimers(t *testing.T) {
	fixture := NewRuleFixtureSkippingDefs(t, "testrules_timers.js")
	defer fixture.tearDown()

	fixture.VerifyTimers("")
}

func TestDirectTimers(t *testing.T) {
	fixture := NewRuleFixtureSkippingDefs(t, "testrules_timers.js")
	defer fixture.tearDown()

	fixture.VerifyTimers("+")
}

func TestDirectMQTTMessages(t *testing.T) {
	fixture := NewRuleFixtureSkippingDefs(t, "testrules.js")
	defer fixture.tearDown()

	fixture.publish("/devices/somedev/controls/sendit/meta/type", "switch", "somedev/sendit")
	fixture.publish("/devices/somedev/controls/sendit", "1", "somedev/sendit")
	fixture.Verify(
		"tst -> /devices/somedev/controls/sendit/meta/type: [switch] (QoS 1, retained)",
		"tst -> /devices/somedev/controls/sendit: [1] (QoS 1, retained)",
		"driver -> /abc/def/ghi: [0] (QoS 0)",
		"driver -> /misc/whatever: [abcdef] (QoS 1)",
		"driver -> /zzz/foo: [qqq] (QoS 2)",
		"driver -> /zzz/foo/qwerty: [42] (QoS 2, retained)",
	)
}

func TestRetainedState(t *testing.T) {
	fixture := NewRuleFixture(t, true, "testrules.js")
	defer fixture.tearDown()
	fixture.engine.Start() // FIXME: should auto-start

	fixture.Verify(
		"driver -> /devices/stabSettings/meta/name: [Stabilization Settings] (QoS 1, retained)",
		"Subscribe -- driver: /devices/+/meta/name",
		"Subscribe -- driver: /devices/+/controls/+",
		"Subscribe -- driver: /devices/+/controls/+/meta/type",
		"Subscribe -- driver: /devices/+/controls/+/meta/max",
		"tst -> /devices/somedev/meta/name: [SomeDev] (QoS 1, retained)",
		"tst -> /devices/somedev/controls/sw/meta/type: [switch] (QoS 1, retained)",
		"tst -> /devices/somedev/controls/sw: [0] (QoS 1, retained)",
		"tst -> /devices/somedev/controls/temp/meta/type: [temperature] (QoS 1, retained)",
		"tst -> /devices/somedev/controls/temp: [19] (QoS 1, retained)",
	)
	fixture.broker.VerifyEmpty()

	fixture.publish("/devices/stabSettings/controls/enabled", "1", "stabSettings/enabled")
	fixture.broker.Verify(
		"tst -> /devices/stabSettings/controls/enabled: [1] (QoS 1, retained)",
	)
	fixture.broker.VerifyEmpty()

	fixture.broker.SetReady()
	fixture.broker.Verify(
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
		"driver -> /devices/stabSettings/controls/lowThreshold: [20] (QoS 1, retained)",
		"Subscribe -- driver: /devices/stabSettings/controls/lowThreshold/on",
		"[rule] heaterOn fired",
 		"driver -> /devices/somedev/controls/sw/on: [1] (QoS 1)",
	)
	fixture.broker.VerifyEmpty()
	fixture.expectCellChange("somedev/sw")
}

func TestCellChange(t *testing.T) {
	fixture := NewRuleFixtureSkippingDefs(t, "testrules.js")
	defer fixture.tearDown()

	fixture.publish("/devices/somedev/controls/foobarbaz/meta/type", "text", "somedev/foobarbaz")
	fixture.publish("/devices/somedev/controls/foobarbaz", "abc", "somedev/foobarbaz")
	fixture.Verify(
		"tst -> /devices/somedev/controls/foobarbaz/meta/type: [text] (QoS 1, retained)",
		"tst -> /devices/somedev/controls/foobarbaz: [abc] (QoS 1, retained)",
		"[rule] cellChange1: somedev/foobarbaz=abc (string)",
		"[rule] cellChange2: somedev/foobarbaz=abc (string)",
	)

	fixture.publish("/devices/somedev/controls/tempx/meta/type", "temperature", "somedev/tempx")
	fixture.publish("/devices/somedev/controls/tempx", "42", "somedev/tempx")
	fixture.Verify(
		"tst -> /devices/somedev/controls/tempx/meta/type: [temperature] (QoS 1, retained)",
		"tst -> /devices/somedev/controls/tempx: [42] (QoS 1, retained)",
		"[rule] cellChange2: somedev/tempx=42 (number)",
	)
}

func fileExists(t *testing.T, path string) bool {
	if _, err := os.Stat(path); err != nil {
		if os.IsNotExist(err) {
			return false
		} else {
			t.Fatalf("unexpected error when checking for samplefile: %s", err)
		}
	}
	return true
}

func verifyFileExists(t *testing.T, path string) {
	if !fileExists(t, path) {
		t.Fatalf("file does not exist: %s", path)
	}
}

func TestRunShellCommand(t *testing.T) {
	fixture := NewRuleFixtureSkippingDefs(t, "testrules_command.js")
	defer fixture.tearDown()

	wd, err := os.Getwd()
	if err != nil {
		t.Fatalf("couldn't get the current directory")
	}

	dir, err := ioutil.TempDir(os.TempDir(), "ruletest")
	if err != nil {
		t.Fatalf("couldn't create temporary directory")
		return
	}
	os.Chdir(dir)
	defer os.RemoveAll(dir)
	defer os.Chdir(wd)

	fixture.publish("/devices/somedev/controls/cmd/meta/type", "text", "somedev/cmd")
	fixture.publish("/devices/somedev/controls/cmdNoCallback/meta/type", "text",
		"somedev/cmdNoCallback")
	fixture.publish("/devices/somedev/controls/cmd", "touch samplefile.txt", "somedev/cmd")
	fixture.Verify(
		"tst -> /devices/somedev/controls/cmd/meta/type: [text] (QoS 1, retained)",
		"tst -> /devices/somedev/controls/cmdNoCallback/meta/type: [text] (QoS 1, retained)",
		"tst -> /devices/somedev/controls/cmd: [touch samplefile.txt] (QoS 1, retained)",
		"[rule] cmd: touch samplefile.txt",
		"[rule] exit(0): touch samplefile.txt",
	)

	verifyFileExists(t, path.Join(dir, "samplefile.txt"))

	fixture.publish("/devices/somedev/controls/cmd", "touch nosuchdir/samplefile.txt 2>/dev/null", "somedev/cmd")
	fixture.Verify(
		"tst -> /devices/somedev/controls/cmd: [touch nosuchdir/samplefile.txt 2>/dev/null] (QoS 1, retained)",
		"[rule] cmd: touch nosuchdir/samplefile.txt 2>/dev/null",
		"[rule] exit(1): touch nosuchdir/samplefile.txt 2>/dev/null", // no such file or directory
	)

	fixture.publish("/devices/somedev/controls/cmdNoCallback", "1", "somedev/cmdNoCallback")
	fixture.publish("/devices/somedev/controls/cmd", "touch samplefile1.txt", "somedev/cmd")
	fixture.Verify(
		"tst -> /devices/somedev/controls/cmdNoCallback: [1] (QoS 1, retained)",
		"tst -> /devices/somedev/controls/cmd: [touch samplefile1.txt] (QoS 1, retained)",
		"[rule] cmd: touch samplefile1.txt",
		"[rule] (no callback)",
	)
	wbgo.WaitFor(t, func () bool {
		return fileExists(t, path.Join(dir, "samplefile1.txt"))
	})
}

func TestRunShellCommandIO(t *testing.T) {
	fixture := NewRuleFixtureSkippingDefs(t, "testrules_command.js")
	defer fixture.tearDown()

	fixture.publish("/devices/somedev/controls/cmdWithOutput/meta/type", "text",
		"somedev/cmdWithOutput")
	fixture.publish("/devices/somedev/controls/cmdWithOutput", "echo abc; echo qqq",
		"somedev/cmdWithOutput")
	fixture.Verify(
		"tst -> /devices/somedev/controls/cmdWithOutput/meta/type: [text] (QoS 1, retained)",
		"tst -> /devices/somedev/controls/cmdWithOutput: [echo abc; echo qqq] (QoS 1, retained)",
		"[rule] cmdWithOutput: echo abc; echo qqq",
		"[rule] exit(0): echo abc; echo qqq",
		"[rule] output: abc",
		"[rule] output: qqq",
	)

	fixture.publish("/devices/somedev/controls/cmdWithOutput", "echo abc; echo qqq 1>&2; exit 1",
		"somedev/cmdWithOutput")
	fixture.Verify(
		"tst -> /devices/somedev/controls/cmdWithOutput: [echo abc; echo qqq 1>&2; exit 1] (QoS 1, retained)",
		"[rule] cmdWithOutput: echo abc; echo qqq 1>&2; exit 1",
		"[rule] exit(1): echo abc; echo qqq 1>&2; exit 1",
		"[rule] output: abc",
		"[rule] error: qqq",
	)

	fixture.publish("/devices/somedev/controls/cmdWithOutput", "xxyz!sed s/x/y/g",
		"somedev/cmdWithOutput")
	fixture.Verify(
		"tst -> /devices/somedev/controls/cmdWithOutput: [xxyz!sed s/x/y/g] (QoS 1, retained)",
		"[rule] cmdWithOutput: sed s/x/y/g",
		"[rule] exit(0): sed s/x/y/g",
		"[rule] output: yyyz",
	)
}

func TestRuleCheckOptimization(t *testing.T) {
	fixture := NewRuleFixtureSkippingDefs(t, "testrules_opt.js")
	defer fixture.tearDown()

	fixture.publish("/devices/somedev/controls/countIt/meta/type", "text", "somedev/countIt")
	fixture.publish("/devices/somedev/controls/countIt", "0", "somedev/countIt")
	fixture.Verify(
		// That's the first time when all rules are run.
		// somedev/countIt is incomplete here, but
		// the engine notes that rule's condition depends
		// on the cell
		"[rule] condCount: asSoonAs()",
		"tst -> /devices/somedev/controls/countIt/meta/type: [text] (QoS 1, retained)",
		"tst -> /devices/somedev/controls/countIt: [0] (QoS 1, retained)",
		// here the value of the cell changes, so the rule is invoked
		"[rule] condCount: asSoonAs()")

	fixture.publish("/devices/somedev/controls/temp", "25", "somedev/temp")
	fixture.publish("/devices/somedev/controls/countIt", "42", "somedev/countIt")
	fixture.Verify(
		"tst -> /devices/somedev/controls/temp: [25] (QoS 1, retained)",
		// changing unrelated cell doesn't cause the rule to be invoked
		"tst -> /devices/somedev/controls/countIt: [42] (QoS 1, retained)",
		"[rule] condCount: asSoonAs()",
		// asSoonAs function called during the first run + when countIt
		// value changed to 42
		"[rule] condCount fired, count=3",
		// ruleWithoutCells follows condCount rule in testrules.js
		// and doesn't utilize any cells. It's run just once when condCount
		// rule sets a global variable to true.
		"[rule] ruleWithoutCells fired")

	fixture.publish("/devices/somedev/controls/countIt", "0", "somedev/countIt")
	fixture.Verify(
		"tst -> /devices/somedev/controls/countIt: [0] (QoS 1, retained)",
		"[rule] condCount: asSoonAs()")
	fixture.publish("/devices/somedev/controls/countIt", "42", "somedev/countIt")
	fixture.Verify(
		"tst -> /devices/somedev/controls/countIt: [42] (QoS 1, retained)",
		"[rule] condCount: asSoonAs()",
		"[rule] condCount fired, count=5")
}

// TBD: metadata (like, meta["devname"]["controlName"])
// TBD: proper data path:
// http://stackoverflow.com/questions/18537257/golang-how-to-get-the-directory-of-the-currently-running-file
// TBD: test bad device/rule defs
// TBD: traceback
// TBD: if rule *did* change anything (SetValue had an effect), re-run rules
//      and do so till no va\lues are changed
// TBD: don't hang upon bad Verify() list
//      (deadlock detection fails due to duktape)
// TBD: should use separate recorder for the fixture, not abuse the fake broker
