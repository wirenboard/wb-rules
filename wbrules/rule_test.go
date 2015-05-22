package wbrules

import (
	wbgo "github.com/contactless/wbgo"
	"github.com/stretchr/testify/assert"
	"os"
	"path"
	"testing"
	"time"
)

var baseRuleTestTime = time.Date(2015, 2, 27, 19, 33, 17, 0, time.UTC)

func makeTime(d time.Duration) time.Time {
	return baseRuleTestTime.Add(d)
}

type fakeTimer struct {
	t        *testing.T
	id       int
	c        chan time.Time
	d        time.Duration
	periodic bool
	active   bool
	rec      *wbgo.Recorder
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

type ruleFixture struct {
	*cellFixture
	engine *ESEngine
	timers map[int]*fakeTimer
	cron   *fakeCron
}

func newRuleFixture(t *testing.T, waitForRetained bool, ruleFile string) *ruleFixture {
	fixture := &ruleFixture{
		newCellFixture(t, waitForRetained),
		nil,
		make(map[int]*fakeTimer),
		nil,
	}
	fixture.engine = NewESEngine(fixture.model, fixture.driverClient)
	fixture.engine.SetTimerFunc(fixture.newFakeTimer)
	fixture.engine.SetCronMaker(func() Cron {
		fixture.cron = newFakeCron(t)
		return fixture.cron
	})
	fixture.engine.SetLogFunc(func(message string) {
		fixture.broker.Rec("[rule] %s", message)
	})
	assert.Equal(t, nil, fixture.engine.LoadScript(ruleFile))
	fixture.driver.Start()
	if !waitForRetained {
		fixture.publishSomedev()
	}
	return fixture
}

func newRuleFixtureSkippingDefs(t *testing.T, ruleFile string) (fixture *ruleFixture) {
	fixture = newRuleFixture(t, false, ruleFile)
	fixture.broker.SkipTill("tst -> /devices/somedev/controls/temp: [19] (QoS 1, retained)")
	fixture.engine.Start()
	return
}

func (fixture *ruleFixture) publishSomedev() {
	<-fixture.model.publishDoneCh
	fixture.publish("/devices/somedev/meta/name", "SomeDev", "")
	fixture.publish("/devices/somedev/controls/sw/meta/type", "switch", "somedev/sw")
	fixture.publish("/devices/somedev/controls/sw", "0", "somedev/sw")
	fixture.publish("/devices/somedev/controls/temp/meta/type", "temperature", "somedev/temp")
	fixture.publish("/devices/somedev/controls/temp", "19", "somedev/temp")
}

func (fixture *ruleFixture) newFakeTimer(id int, d time.Duration, periodic bool) Timer {
	timer := &fakeTimer{
		t:        fixture.t,
		id:       id,
		c:        make(chan time.Time),
		d:        d,
		periodic: periodic,
		active:   true,
		rec:      &fixture.broker.Recorder,
	}
	fixture.timers[id] = timer
	fixture.broker.Rec("newFakeTimer(): %d, %d, %v", id, d/time.Millisecond, periodic)
	return timer
}

func (fixture *ruleFixture) Verify(logs ...string) {
	fixture.broker.Verify(logs...)
}

func (fixture *ruleFixture) VerifyUnordered(logs ...string) {
	fixture.broker.VerifyUnordered(logs...)
}

func (fixture *ruleFixture) SetCellValue(device, cellName string, value interface{}) {
	fixture.driver.CallSync(func() {
		fixture.model.EnsureDevice(device).EnsureCell(cellName).SetValue(value)
	})
	actualCellSpec := <-fixture.cellChange
	assert.Equal(fixture.t, device+"/"+cellName,
		actualCellSpec.DevName+"/"+actualCellSpec.CellName)
}

func (fixture *ruleFixture) tearDown() {
	fixture.cellFixture.tearDown()
	wbgo.WaitFor(fixture.t, func() bool {
		return !fixture.engine.IsActive()
	})
}

func TestDeviceDefinition(t *testing.T) {
	fixture := newRuleFixture(t, false, "testrules.js")
	defer fixture.tearDown()
	fixture.Verify(
		"driver -> /devices/stabSettings/meta/name: [Stabilization Settings] (QoS 1, retained)",
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
		"tst -> /devices/somedev/meta/name: [SomeDev] (QoS 1, retained)",
		"tst -> /devices/somedev/controls/sw/meta/type: [switch] (QoS 1, retained)",
		"tst -> /devices/somedev/controls/sw: [0] (QoS 1, retained)",
		"tst -> /devices/somedev/controls/temp/meta/type: [temperature] (QoS 1, retained)",
		"tst -> /devices/somedev/controls/temp: [19] (QoS 1, retained)",
	)
}

func TestRules(t *testing.T) {
	fixture := newRuleFixtureSkippingDefs(t, "testrules.js")
	defer fixture.tearDown()

	fixture.SetCellValue("stabSettings", "enabled", true)
	fixture.Verify(
		"driver -> /devices/stabSettings/controls/enabled: [1] (QoS 1, retained)",
		"[rule] heaterOn fired, changed: stabSettings/enabled -> true",
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
		"[rule] heaterOff fired, changed: somedev/temp -> 22",
		"driver -> /devices/somedev/controls/sw/on: [0] (QoS 1)",
	)

	fixture.publish("/devices/somedev/controls/temp", "18", "somedev/temp", "somedev/sw")
	fixture.Verify(
		"tst -> /devices/somedev/controls/temp: [18] (QoS 1, retained)",
		"[rule] heaterOn fired, changed: somedev/temp -> 18",
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
		"[rule] heaterOff fired, changed: stabSettings/enabled -> false",
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
	fixture.publish("/devices/somedev/controls/foo", prefix+"t", "somedev/foo")
	fixture.Verify(
		"tst -> /devices/somedev/controls/foo/meta/type: [text] (QoS 1, retained)",
		"tst -> /devices/somedev/controls/foo: ["+prefix+"t] (QoS 1, retained)",
		"newFakeTimer(): 1, 500, false",
		"newFakeTimer(): 2, 500, false",
	)

	fixture.publish("/devices/somedev/controls/foo", prefix+"s", "somedev/foo")
	fixture.Verify(
		"tst -> /devices/somedev/controls/foo: ["+prefix+"s] (QoS 1, retained)",
		"timer.Stop(): 1",
		"timer.Stop(): 2",
	)

	fixture.publish("/devices/somedev/controls/foo", prefix+"t", "somedev/foo")
	fixture.Verify(
		"tst -> /devices/somedev/controls/foo: ["+prefix+"t] (QoS 1, retained)",
		"newFakeTimer(): 1, 500, false",
		"newFakeTimer(): 2, 500, false",
	)

	ts := makeTime(500 * time.Millisecond)
	fixture.timers[1].fire(ts)
	fixture.timers[2].fire(ts)
	fixture.Verify(
		"timer.fire(): 1",
		"timer.fire(): 2",
		"[rule] timer fired",
		"[rule] timer1 fired",
	)

	fixture.publish("/devices/somedev/controls/foo", prefix+"p", "somedev/foo")
	fixture.Verify(
		"tst -> /devices/somedev/controls/foo: ["+prefix+"p] (QoS 1, retained)",
		"newFakeTimer(): 1, 500, true",
	)

	for i := 1; i < 4; i++ {
		targetTime := makeTime(time.Duration(500*i) * time.Millisecond)
		fixture.timers[1].fire(targetTime)
		fixture.Verify(
			"timer.fire(): 1",
			"[rule] timer fired",
		)
	}

	fixture.publish("/devices/somedev/controls/foo", prefix+"t", "somedev/foo")
	fixture.Verify(
		"tst -> /devices/somedev/controls/foo: [" + prefix + "t] (QoS 1, retained)",
	)
	fixture.VerifyUnordered(
		"timer.Stop(): 1",
		"newFakeTimer(): 1, 500, false",
		"newFakeTimer(): 2, 500, false",
	)

	ts = makeTime(5 * 500 * time.Millisecond)
	fixture.timers[1].fire(ts)
	fixture.timers[2].fire(ts)
	fixture.Verify(
		"timer.fire(): 1",
		"timer.fire(): 2",
		"[rule] timer fired",
		"[rule] timer1 fired",
	)
}

func TestTimers(t *testing.T) {
	fixture := newRuleFixtureSkippingDefs(t, "testrules_timers.js")
	defer fixture.tearDown()

	fixture.VerifyTimers("")
}

func TestDirectTimers(t *testing.T) {
	fixture := newRuleFixtureSkippingDefs(t, "testrules_timers.js")
	defer fixture.tearDown()

	fixture.VerifyTimers("+")
}

func TestDirectMQTTMessages(t *testing.T) {
	fixture := newRuleFixtureSkippingDefs(t, "testrules.js")
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
	fixture := newRuleFixture(t, true, "testrules.js")
	defer fixture.tearDown()
	fixture.engine.Start()

	fixture.Verify(
		"driver -> /devices/stabSettings/meta/name: [Stabilization Settings] (QoS 1, retained)",
		"Subscribe -- driver: /devices/+/meta/name",
		"Subscribe -- driver: /devices/+/controls/+",
		"Subscribe -- driver: /devices/+/controls/+/meta/type",
		"Subscribe -- driver: /devices/+/controls/+/meta/max",
	)
	fixture.broker.VerifyEmpty()

	fixture.publish("/devices/stabSettings/controls/enabled", "1", "stabSettings/enabled")
	// lower the threshold so that the rule doesn't fire immediately
	// (which mixes up cell change events during fixture.publishSomedev())
	fixture.publish("/devices/stabSettings/controls/lowThreshold", "18", "stabSettings/lowThreshold")
	fixture.broker.Verify(
		"tst -> /devices/stabSettings/controls/enabled: [1] (QoS 1, retained)",
		"tst -> /devices/stabSettings/controls/lowThreshold: [18] (QoS 1, retained)",
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
		"driver -> /devices/stabSettings/controls/lowThreshold: [18] (QoS 1, retained)",
		"Subscribe -- driver: /devices/stabSettings/controls/lowThreshold/on",
	)
	fixture.publishSomedev()
	fixture.broker.Verify(
		"tst -> /devices/somedev/meta/name: [SomeDev] (QoS 1, retained)",
		"tst -> /devices/somedev/controls/sw/meta/type: [switch] (QoS 1, retained)",
		"tst -> /devices/somedev/controls/sw: [0] (QoS 1, retained)",
		"tst -> /devices/somedev/controls/temp/meta/type: [temperature] (QoS 1, retained)",
		"tst -> /devices/somedev/controls/temp: [19] (QoS 1, retained)",
	)
	fixture.publish("/devices/somedev/controls/temp", "16", "somedev/temp", "somedev/sw")
	fixture.broker.Verify(
		"tst -> /devices/somedev/controls/temp: [16] (QoS 1, retained)",
		"[rule] heaterOn fired, changed: somedev/temp -> 16",
		"driver -> /devices/somedev/controls/sw/on: [1] (QoS 1)",
	)
	fixture.broker.VerifyEmpty()
}

func TestCellChange(t *testing.T) {
	fixture := newRuleFixtureSkippingDefs(t, "testrules.js")
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
	// no change
	fixture.publish("/devices/somedev/controls/tempx", "42", "somedev/tempx")
	fixture.publish("/devices/somedev/controls/tempx", "42", "somedev/tempx")
	fixture.Verify(
		"tst -> /devices/somedev/controls/tempx: [42] (QoS 1, retained)",
		"tst -> /devices/somedev/controls/tempx: [42] (QoS 1, retained)",
	)

}

func TestLocalButtons(t *testing.T) {
	fixture := newRuleFixture(t, false, "testrules_localbutton.js")
	defer fixture.tearDown()
	fixture.engine.Start()

	fixture.Verify(
		"driver -> /devices/buttons/meta/name: [Button Test] (QoS 1, retained)",
		"Subscribe -- driver: /devices/+/meta/name",
		"Subscribe -- driver: /devices/+/controls/+",
		"Subscribe -- driver: /devices/+/controls/+/meta/type",
		"Subscribe -- driver: /devices/+/controls/+/meta/max",
		"driver -> /devices/buttons/controls/somebutton/meta/type: [pushbutton] (QoS 1, retained)",
		"driver -> /devices/buttons/controls/somebutton/meta/order: [1] (QoS 1, retained)",
		"Subscribe -- driver: /devices/buttons/controls/somebutton/on",
		// FIXME: don't need these here
		"tst -> /devices/somedev/meta/name: [SomeDev] (QoS 1, retained)",
		"tst -> /devices/somedev/controls/sw/meta/type: [switch] (QoS 1, retained)",
		"tst -> /devices/somedev/controls/sw: [0] (QoS 1, retained)",
		"tst -> /devices/somedev/controls/temp/meta/type: [temperature] (QoS 1, retained)",
		"tst -> /devices/somedev/controls/temp: [19] (QoS 1, retained)",
	)
	fixture.broker.VerifyEmpty()

	for i := 0; i < 3; i++ {
		// The change rule must be fired on each button press ('1' .../on value message)
		fixture.publish("/devices/buttons/controls/somebutton/on", "1", "buttons/somebutton")
		fixture.Verify(
			"tst -> /devices/buttons/controls/somebutton/on: [1] (QoS 1)",
			"driver -> /devices/buttons/controls/somebutton: [1] (QoS 1)", // note there's no 'retained' flag
			"[rule] button pressed!",
		)
	}
}

func TestRemoteButtons(t *testing.T) {
	// FIXME: handling remote buttons, i.e. buttons that
	// are defined for external devices and not via defineVirtualDevice(),
	// needs more work. We need to handle /on messages for these
	// instead of value messages. As of now, the code will work
	// unless the remote driver retains button value, in which
	// case extra change events will be received on startup
	fixture := newRuleFixtureSkippingDefs(t, "testrules.js")
	defer fixture.tearDown()

	// The change rule must be fired on each button press ('1' value message)
	fixture.publish("/devices/somedev/controls/abutton/meta/type", "pushbutton", "somedev/abutton")
	fixture.publish("/devices/somedev/controls/abutton", "1", "somedev/abutton")
	fixture.Verify(
		"tst -> /devices/somedev/controls/abutton/meta/type: [pushbutton] (QoS 1, retained)",
		"tst -> /devices/somedev/controls/abutton: [1] (QoS 1, retained)",
		"[rule] cellChange2: somedev/abutton=false (boolean)",
	)
	fixture.publish("/devices/somedev/controls/abutton", "1", "somedev/abutton")
	fixture.Verify(
		"tst -> /devices/somedev/controls/abutton: [1] (QoS 1, retained)",
		"[rule] cellChange2: somedev/abutton=false (boolean)",
	)
}

func TestFuncValueChange(t *testing.T) {
	fixture := newRuleFixtureSkippingDefs(t, "testrules.js")
	defer fixture.tearDown()

	fixture.publish("/devices/somedev/controls/cellforfunc", "2", "somedev/cellforfunc")
	fixture.Verify(
		// the cell is incomplete here
		"tst -> /devices/somedev/controls/cellforfunc: [2] (QoS 1, retained)",
	)

	fixture.publish("/devices/somedev/controls/cellforfunc/meta/type", "temperature", "somedev/cellforfunc")
	fixture.Verify(
		"tst -> /devices/somedev/controls/cellforfunc/meta/type: [temperature] (QoS 1, retained)",
		"[rule] funcValueChange: false (boolean)",
	)

	fixture.publish("/devices/somedev/controls/cellforfunc", "5", "somedev/cellforfunc")
	fixture.Verify(
		"tst -> /devices/somedev/controls/cellforfunc: [5] (QoS 1, retained)",
		"[rule] funcValueChange: true (boolean)",
	)

	fixture.publish("/devices/somedev/controls/cellforfunc", "7", "somedev/cellforfunc")
	fixture.Verify(
		// expression value not changed
		"tst -> /devices/somedev/controls/cellforfunc: [7] (QoS 1, retained)",
	)

	fixture.publish("/devices/somedev/controls/cellforfunc", "1", "somedev/cellforfunc")
	fixture.Verify(
		"tst -> /devices/somedev/controls/cellforfunc: [1] (QoS 1, retained)",
		"[rule] funcValueChange: false (boolean)",
	)

	fixture.publish("/devices/somedev/controls/cellforfunc", "0", "somedev/cellforfunc")
	fixture.Verify(
		// expression value not changed
		"tst -> /devices/somedev/controls/cellforfunc: [0] (QoS 1, retained)",
	)

	// somedev/cellforfunc1 is listed by name
	fixture.publish("/devices/somedev/controls/cellforfunc1", "2", "somedev/cellforfunc1")
	fixture.publish("/devices/somedev/controls/cellforfunc2", "2", "somedev/cellforfunc2")
	fixture.Verify(
		// the cell is incomplete here
		"tst -> /devices/somedev/controls/cellforfunc1: [2] (QoS 1, retained)",
		"tst -> /devices/somedev/controls/cellforfunc2: [2] (QoS 1, retained)",
	)

	fixture.publish("/devices/somedev/controls/cellforfunc1/meta/type", "temperature", "somedev/cellforfunc1")
	fixture.Verify(
		"tst -> /devices/somedev/controls/cellforfunc1/meta/type: [temperature] (QoS 1, retained)",
		"[rule] funcValueChange2: somedev/cellforfunc1: 2 (number)",
	)

	fixture.publish("/devices/somedev/controls/cellforfunc2/meta/type", "temperature", "somedev/cellforfunc2")
	fixture.Verify(
		"tst -> /devices/somedev/controls/cellforfunc2/meta/type: [temperature] (QoS 1, retained)",
		"[rule] funcValueChange2: (no cell): false (boolean)",
	)

	fixture.publish("/devices/somedev/controls/cellforfunc2", "5", "somedev/cellforfunc2")
	fixture.Verify(
		"tst -> /devices/somedev/controls/cellforfunc2: [5] (QoS 1, retained)",
		"[rule] funcValueChange2: (no cell): true (boolean)",
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
	fixture := newRuleFixtureSkippingDefs(t, "testrules_command.js")
	defer fixture.tearDown()

	dir, cleanup := wbgo.SetupTempDir(t)
	defer cleanup()

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
	wbgo.WaitFor(t, func() bool {
		return fileExists(t, path.Join(dir, "samplefile1.txt"))
	})
}

func TestRunShellCommandIO(t *testing.T) {
	fixture := newRuleFixtureSkippingDefs(t, "testrules_command.js")
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
	fixture := newRuleFixtureSkippingDefs(t, "testrules_opt.js")
	defer fixture.tearDown()

	fixture.publish("/devices/somedev/controls/countIt/meta/type", "text", "somedev/countIt")
	fixture.publish("/devices/somedev/controls/countIt", "0", "somedev/countIt")
	fixture.Verify(
		// That's the first time when all rules are run.
		// somedev/countIt and somedev/countItLT are incomplete here, but
		// the engine notes that rules' conditions depend on the cells
		"[rule] condCount: asSoonAs()",
		"[rule] condCountLT: when()",
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

	// now check optimization of level-triggered rules
	fixture.publish("/devices/somedev/controls/countItLT/meta/type", "text", "somedev/countItLT")
	fixture.publish("/devices/somedev/controls/countItLT", "0", "somedev/countItLT")
	fixture.Verify(
		"tst -> /devices/somedev/controls/countItLT/meta/type: [text] (QoS 1, retained)",
		"tst -> /devices/somedev/controls/countItLT: [0] (QoS 1, retained)",
		// here the value of the cell changes, so the rule is invoked
		"[rule] condCountLT: when()")

	fixture.publish("/devices/somedev/controls/countItLT", "42", "somedev/countItLT")
	fixture.Verify(
		"tst -> /devices/somedev/controls/countItLT: [42] (QoS 1, retained)",
		"[rule] condCountLT: when()",
		// when function called during the first run + when countItLT
		// value changed to 42
		"[rule] condCountLT fired, count=3")

	fixture.publish("/devices/somedev/controls/countItLT", "43", "somedev/countItLT")
	fixture.Verify(
		"tst -> /devices/somedev/controls/countItLT: [43] (QoS 1, retained)",
		"[rule] condCountLT: when()",
		"[rule] condCountLT fired, count=4")

	fixture.publish("/devices/somedev/controls/countItLT", "0", "somedev/countItLT")
	fixture.Verify(
		"tst -> /devices/somedev/controls/countItLT: [0] (QoS 1, retained)",
		"[rule] condCountLT: when()")

	fixture.publish("/devices/somedev/controls/countItLT", "1", "somedev/countItLT")
	fixture.Verify(
		"tst -> /devices/somedev/controls/countItLT: [1] (QoS 1, retained)",
		"[rule] condCountLT: when()")
}

func TestReadOnlyCells(t *testing.T) {
	fixture := newRuleFixture(t, false, "testrules_readonly.js")
	defer fixture.tearDown()
	fixture.Verify(
		"driver -> /devices/roCells/meta/name: [Readonly Cell Test] (QoS 1, retained)",
		"Subscribe -- driver: /devices/+/meta/name",
		"Subscribe -- driver: /devices/+/controls/+",
		"Subscribe -- driver: /devices/+/controls/+/meta/type",
		"Subscribe -- driver: /devices/+/controls/+/meta/max",
		"driver -> /devices/roCells/controls/rocell/meta/type: [switch] (QoS 1, retained)",
		"driver -> /devices/roCells/controls/rocell/meta/readonly: [1] (QoS 1, retained)",
		"driver -> /devices/roCells/controls/rocell/meta/order: [1] (QoS 1, retained)",
		"driver -> /devices/roCells/controls/rocell: [0] (QoS 1, retained)",
		"tst -> /devices/somedev/meta/name: [SomeDev] (QoS 1, retained)",
		"tst -> /devices/somedev/controls/sw/meta/type: [switch] (QoS 1, retained)",
		"tst -> /devices/somedev/controls/sw: [0] (QoS 1, retained)",
		"tst -> /devices/somedev/controls/temp/meta/type: [temperature] (QoS 1, retained)",
		"tst -> /devices/somedev/controls/temp: [19] (QoS 1, retained)",
	)
}

func TestCron(t *testing.T) {
	fixture := newRuleFixtureSkippingDefs(t, "testrules_cron.js")
	defer fixture.tearDown()

	wbgo.WaitFor(t, func() bool {
		c := make(chan bool)
		fixture.model.CallSync(func() {
			c <- fixture.cron != nil && fixture.cron.started
		})
		return <-c
	})

	fixture.cron.invokeEntries("@hourly")
	fixture.cron.invokeEntries("@hourly")
	fixture.cron.invokeEntries("@daily")
	fixture.cron.invokeEntries("@hourly")

	fixture.Verify(
		"[rule] @hourly rule fired",
		"[rule] @hourly rule fired",
		"[rule] @daily rule fired",
		"[rule] @hourly rule fired",
	)

	// the new script contains rules with same names as in
	// testrules_cron.js that should override the previous
	// rules
	fixture.engine.LiveLoadScript("testrules_cron_for_reload.js")

	fixture.cron.invokeEntries("@hourly")
	fixture.cron.invokeEntries("@hourly")
	fixture.cron.invokeEntries("@daily")
	fixture.cron.invokeEntries("@hourly")

	fixture.Verify(
		"[rule] @hourly rule fired (new)",
		"[rule] @hourly rule fired (new)",
		"[rule] @daily rule fired (new)",
		"[rule] @hourly rule fired (new)",
	)
}

func TestReload(t *testing.T) {
	fixture := newRuleFixtureSkippingDefs(t, "testrules_reload.js")
	defer fixture.tearDown()

	fixture.broker.Verify(
		"[rule] detectRun: (no cell) (s=false, a=10)",
		"[rule] detectRun1: (no cell) (s=false, a=10)",
	)

	fixture.publish("/devices/vdev/controls/someCell/on", "1", "vdev/someCell")
	fixture.broker.Verify(
		"tst -> /devices/vdev/controls/someCell/on: [1] (QoS 1)",
		"driver -> /devices/vdev/controls/someCell: [1] (QoS 1, retained)",
		"[rule] detectRun: vdev/someCell (s=true, a=10)",
		"[rule] detectRun1: vdev/someCell (s=true, a=10)",
		"[rule] rule1: vdev/someCell=true",
		"[rule] rule2: vdev/someCell=true",
	)

	fixture.publish("/devices/vdev/controls/anotherCell/on", "17", "vdev/anotherCell")
	fixture.Verify(
		"tst -> /devices/vdev/controls/anotherCell/on: [17] (QoS 1)",
		"driver -> /devices/vdev/controls/anotherCell: [17] (QoS 1, retained)",
		"[rule] detectRun: vdev/anotherCell (s=true, a=17)",
		"[rule] detectRun1: vdev/anotherCell (s=true, a=17)",
		"[rule] rule3: vdev/anotherCell=17",
	)

	// Let's pretend we edited the script. Actually we're
	// reloading it while making it use a bit different device and
	// rule definitions.
	fixture.engine.EvalScript("alteredMode = true;")
	fixture.engine.LiveLoadScript("testrules_reload.js")

	fixture.Verify(
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
		"[rule] detectRun: (no cell) (s=true)",
	)

	// this one must be ignored because anotherCell is no longer there
	// after the device is redefined
	fixture.publish("/devices/vdev/controls/anotherCell/on", "11")
	fixture.publish("/devices/vdev/controls/someCell/on", "0", "vdev/someCell")

	fixture.broker.Verify(
		"tst -> /devices/vdev/controls/anotherCell/on: [11] (QoS 1)",
		"tst -> /devices/vdev/controls/someCell/on: [0] (QoS 1)",
		"driver -> /devices/vdev/controls/someCell: [0] (QoS 1, retained)",
		"[rule] detectRun: vdev/someCell (s=false)",
		"[rule] rule1: vdev/someCell=false",
		// rule2 is gone, rule3 is gone together with its anotherCell
	)

	fixture.publish("/devices/vdev/controls/someCell/on", "1", "vdev/someCell")
	fixture.broker.Verify(
		"tst -> /devices/vdev/controls/someCell/on: [1] (QoS 1)",
		"driver -> /devices/vdev/controls/someCell: [1] (QoS 1, retained)",
		"[rule] detectRun: vdev/someCell (s=true)",
		"[rule] rule1: vdev/someCell=true",
	)

	// TBD: stop any timers started while evaluating the script.
	// This will require extra care because cleanup procedure
	// for the timer must be revoked once the timer is stopped.
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
