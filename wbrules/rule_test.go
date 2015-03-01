package wbrules

import (
	"time"
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
	name string
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
	timer.rec.Rec("timer.fire(): %s", timer.name)
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
	timer.rec.Rec("timer.Stop(): %s", timer.name)
}

type ruleFixture struct {
	cellFixture
	engine *RuleEngine
	timers map[string]*fakeTimer
}

func NewRuleFixture(t *testing.T, waitForRetained bool) *ruleFixture {
	fixture := &ruleFixture{
		*NewCellFixture(t, waitForRetained),
		nil,
		make(map[string]*fakeTimer),
	}
	fixture.engine = NewRuleEngine(fixture.model, fixture.driverClient)
	fixture.engine.SetTimerFunc(fixture.newFakeTimer)
	fixture.engine.SetLogFunc(func (message string) {
		fixture.broker.Rec("[rule] %s", message)
	})
	assert.Equal(t, nil, fixture.engine.LoadScript("testrules.js"))
	fixture.driver.Start()
	fixture.publish("/devices/somedev/meta/name", "SomeDev", "")
	fixture.publish("/devices/somedev/controls/sw/meta/type", "switch", "sw")
	fixture.publish("/devices/somedev/controls/sw", "0", "sw")
	fixture.publish("/devices/somedev/controls/temp/meta/type", "temperature", "temp")
	fixture.publish("/devices/somedev/controls/temp", "19", "temp")
	return fixture
}

func NewRuleFixtureSkippingDefs(t *testing.T) (fixture *ruleFixture) {
	fixture = NewRuleFixture(t, false)
	fixture.broker.SkipTill("tst -> /devices/somedev/controls/temp: [19] (QoS 1, retained)")
	fixture.engine.Start() // FIXME: should auto-start
	return
}

func (fixture *ruleFixture) newFakeTimer(name string, d time.Duration, periodic bool) Timer {
	timer := &fakeTimer{
		t: fixture.t,
		name: name,
		c: make(chan time.Time),
		d: d,
		periodic: periodic,
		active: true,
		rec: &fixture.broker.Recorder,
	}
	fixture.timers[name] = timer
	fixture.broker.Rec("newFakeTimer(): %s, %d, %v", name, d / time.Millisecond, periodic)
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
	actualCellName := <- fixture.cellChange
	assert.Equal(fixture.t, cellName, actualCellName)
}

func TestDeviceDefinition(t *testing.T) {
	fixture := NewRuleFixture(t, false)
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
	fixture := NewRuleFixtureSkippingDefs(t)
	defer fixture.tearDown()

	fixture.SetCellValue("stabSettings", "enabled", true)
	fixture.Verify(
		"driver -> /devices/stabSettings/controls/enabled: [1] (QoS 1, retained)",
		"[rule] heaterOn fired",
 		"driver -> /devices/somedev/controls/sw/on: [1] (QoS 1)",
	)
	fixture.expectCellChange("sw")

	fixture.publish("/devices/somedev/controls/temp", "21", "temp")
	fixture.Verify(
		"tst -> /devices/somedev/controls/temp: [21] (QoS 1, retained)",
	)

	fixture.publish("/devices/somedev/controls/temp", "22", "temp", "sw")
	fixture.Verify(
		"tst -> /devices/somedev/controls/temp: [22] (QoS 1, retained)",
		"[rule] heaterOff fired",
 		"driver -> /devices/somedev/controls/sw/on: [0] (QoS 1)",
	)

	fixture.publish("/devices/somedev/controls/temp", "18", "temp", "sw")
	fixture.Verify(
		"tst -> /devices/somedev/controls/temp: [18] (QoS 1, retained)",
		"[rule] heaterOn fired",
 		"driver -> /devices/somedev/controls/sw/on: [1] (QoS 1)",
	)

	// edge-triggered rule doesn't fire
	fixture.publish("/devices/somedev/controls/temp", "19", "temp")
	fixture.Verify(
		"tst -> /devices/somedev/controls/temp: [19] (QoS 1, retained)",
	)

	fixture.SetCellValue("stabSettings", "enabled", false)
	fixture.Verify(
		"driver -> /devices/stabSettings/controls/enabled: [0] (QoS 1, retained)",
		"[rule] heaterOff fired",
 		"driver -> /devices/somedev/controls/sw/on: [0] (QoS 1)",
	)
	fixture.expectCellChange("sw")

	fixture.publish("/devices/somedev/controls/foobar", "1", "foobar")
	fixture.publish("/devices/somedev/controls/foobar/meta/type", "text", "foobar")
	fixture.Verify(
		"tst -> /devices/somedev/controls/foobar: [1] (QoS 1, retained)",
		"tst -> /devices/somedev/controls/foobar/meta/type: [text] (QoS 1, retained)",
		"[rule] initiallyIncompleteLevelTriggered fired",
	)

	// level-triggered rule fires again here
	fixture.publish("/devices/somedev/controls/foobar", "2", "foobar")
	fixture.Verify(
		"tst -> /devices/somedev/controls/foobar: [2] (QoS 1, retained)",
		"[rule] initiallyIncompleteLevelTriggered fired",
	)
}

func TestTimers(t *testing.T) {
	fixture := NewRuleFixtureSkippingDefs(t)
	defer fixture.tearDown()

	fixture.publish("/devices/somedev/controls/foo/meta/type", "text", "foo")
	fixture.publish("/devices/somedev/controls/foo", "t", "foo")
	fixture.Verify(
		"tst -> /devices/somedev/controls/foo/meta/type: [text] (QoS 1, retained)",
		"tst -> /devices/somedev/controls/foo: [t] (QoS 1, retained)",
		"newFakeTimer(): sometimer, 500, false",
	)

	fixture.publish("/devices/somedev/controls/foo", "-", "foo")
	fixture.Verify(
		"tst -> /devices/somedev/controls/foo: [-] (QoS 1, retained)",
		"timer.Stop(): sometimer",
	)

	fixture.publish("/devices/somedev/controls/foo", "t", "foo")
	fixture.Verify(
		"tst -> /devices/somedev/controls/foo: [t] (QoS 1, retained)",
		"newFakeTimer(): sometimer, 500, false",
	)

	fixture.timers["sometimer"].fire(makeTime(500 * time.Millisecond))
	fixture.Verify(
		"timer.fire(): sometimer",
		"[rule] timer fired",
	)

	fixture.publish("/devices/somedev/controls/foo", "p", "foo")
	fixture.Verify(
		"tst -> /devices/somedev/controls/foo: [p] (QoS 1, retained)",
		"newFakeTimer(): sometimer, 500, true",
	)

	for i := 1; i < 4; i++ {
		targetTime := makeTime(time.Duration(500 * i) * time.Millisecond)
		fixture.timers["sometimer"].fire(targetTime)
		fixture.Verify(
			"timer.fire(): sometimer",
			"[rule] timer fired",
		)
	}

	fixture.publish("/devices/somedev/controls/foo", "t", "foo")
	fixture.Verify(
		"tst -> /devices/somedev/controls/foo: [t] (QoS 1, retained)",
	)
	fixture.VerifyUnordered(
		"timer.Stop(): sometimer",
		"newFakeTimer(): sometimer, 500, false",
	)

	fixture.timers["sometimer"].fire(makeTime(5 * 500 * time.Millisecond))
	fixture.Verify(
		"timer.fire(): sometimer",
		"[rule] timer fired",
	)
}

func TestDirectMQTTMessages(t *testing.T) {
	fixture := NewRuleFixtureSkippingDefs(t)
	defer fixture.tearDown()

	fixture.publish("/devices/somedev/controls/sendit/meta/type", "switch", "sendit")
	fixture.publish("/devices/somedev/controls/sendit", "1", "sendit")
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
	fixture := NewRuleFixture(t, true)
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

	fixture.publish("/devices/stabSettings/controls/enabled", "1", "enabled")
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
	fixture.expectCellChange("sw")
}

// TBD: metadata (like, meta["devname"]["controlName"])
// TBD: proper data path:
// http://stackoverflow.com/questions/18537257/golang-how-to-get-the-directory-of-the-currently-running-file
// TBD: test bad device/rule defs
// TBD: traceback
// TBD: if rule *did* change anything (SetValue had an effect), re-run rules
//      and do so till no values are changed
// TBD: don't hang upon bad Verify() list
//      (deadlock detection fails due to duktape)
// TBD: should use separate recorder for the fixture, not abuse the fake broker
