package wbrules

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/wirenboard/wbgong/testutils"
)

type AlarmSuite struct {
	RuleSuiteBase
}

func (s *AlarmSuite) SetupTest() {
	s.SetupSkippingDefs("testrules_alarm.js")
	s.publishTestDev()

}

func (s *AlarmSuite) loadAlarms() {
	s.loadAlarmsSkipping("")
}

func (s *AlarmSuite) loadAlarmsSkipping(skipLine string) {
	confPath := s.CopyModifiedDataFileToTempDir("alarms.conf", "alarms.conf", func(text string) string {
		if skipLine == "" {
			return text
		}
		lines := strings.Split(text, "\n")
		out := make([]string, 0, len(lines))
		for _, line := range lines {
			if !strings.Contains(line, skipLine) {
				out = append(out, line)
			}
		}
		return strings.Join(out, "\n")
	})
	confPathJS, err := json.Marshal(confPath)
	if err != nil {
		panic("json.Marshal() failed on string?")
	}
	// here we simulate loading of alarms into the running engine
	s.Ck("failed to init alarms", s.engine.EvalScript(fmt.Sprintf("Alarms.load(%s)", confPathJS)))
	s.engine.Refresh()

	s.VerifyUnordered(
		"driver -> /devices/sampleAlarms/meta/name: [Sample Alarms] (QoS 1, retained)",
		"driver -> /devices/sampleAlarms/meta/driver: [wbrules] (QoS 1, retained)",
		"driver -> /devices/sampleAlarms/controls/alarm_importantDeviceIsOff/meta/type: [alarm] (QoS 1, retained)",
		"driver -> /devices/sampleAlarms/controls/alarm_importantDeviceIsOff/meta/readonly: [1] (QoS 1, retained)",
		"driver -> /devices/sampleAlarms/controls/alarm_importantDeviceIsOff/meta/order: [1] (QoS 1, retained)",
		"driver -> /devices/sampleAlarms/controls/alarm_importantDeviceIsOff: [0] (QoS 1, retained)",
		"Subscribe -- driver: /devices/sampleAlarms/controls/alarm_importantDeviceIsOff/on",
		"driver -> /devices/sampleAlarms/controls/alarm_temperatureOutOfBounds/meta/type: [alarm] (QoS 1, retained)",
		"driver -> /devices/sampleAlarms/controls/alarm_temperatureOutOfBounds/meta/readonly: [1] (QoS 1, retained)",
		"driver -> /devices/sampleAlarms/controls/alarm_temperatureOutOfBounds/meta/order: [2] (QoS 1, retained)",
		"driver -> /devices/sampleAlarms/controls/alarm_temperatureOutOfBounds: [0] (QoS 1, retained)",
		"Subscribe -- driver: /devices/sampleAlarms/controls/alarm_temperatureOutOfBounds/on",
		"driver -> /devices/sampleAlarms/controls/alarm_unnecessaryDeviceIsOn/meta/type: [alarm] (QoS 1, retained)",
		"driver -> /devices/sampleAlarms/controls/alarm_unnecessaryDeviceIsOn/meta/readonly: [1] (QoS 1, retained)",
		"driver -> /devices/sampleAlarms/controls/alarm_unnecessaryDeviceIsOn/meta/order: [3] (QoS 1, retained)",
		"driver -> /devices/sampleAlarms/controls/alarm_unnecessaryDeviceIsOn: [0] (QoS 1, retained)",
		"Subscribe -- driver: /devices/sampleAlarms/controls/alarm_unnecessaryDeviceIsOn/on",
		"driver -> /devices/sampleAlarms/controls/log/meta/type: [text] (QoS 1, retained)",
		"driver -> /devices/sampleAlarms/controls/log/meta/readonly: [1] (QoS 1, retained)",
		"driver -> /devices/sampleAlarms/controls/log/meta/order: [4] (QoS 1, retained)",
		"driver -> /devices/sampleAlarms/controls/log: [] (QoS 1, retained)",
		"Subscribe -- driver: /devices/sampleAlarms/controls/log/on",
	)
}

func (s *AlarmSuite) controlRef(dev, ctl string) (controlRef, topicBase string) {
	return dev + "/" + ctl, fmt.Sprintf("/devices/%s/controls/%s", dev, ctl)
}

func (s *AlarmSuite) publishControl(dev, ctl, typ, value string) {
	controlRef, topicBase := s.controlRef(dev, ctl)
	s.publish(topicBase+"/meta/type", typ, controlRef)
	s.publish(topicBase, value, controlRef)
	s.Verify(
		fmt.Sprintf("tst -> %s/meta/type: [%s] (QoS 1, retained)", topicBase, typ),
		fmt.Sprintf("tst -> %s: [%s] (QoS 1, retained)", topicBase, value))
}

func (s *AlarmSuite) publishTestDev() {
	s.publishControl("somedev", "importantDevicePower", "switch", "1")
	s.publishControl("somedev", "unnecessaryDevicePower", "switch", "0")
	s.publishControl("somedev", "devTemp", "temperature", "11")
}

func (s *AlarmSuite) publishControlValue(dev, ctl, value string, expectedControlNames ...string) {
	controlRef, topicBase := s.controlRef(dev, ctl)
	s.publish(topicBase, value, append([]string{controlRef}, expectedControlNames...)...)
	s.Verify(fmt.Sprintf("tst -> %s: [%s] (QoS 1, retained)", topicBase, value))
}

func (s *AlarmSuite) verifyAlarmControlChange(name string, active bool) {
	activeStr := "0"
	if active {
		activeStr = "1"
	}
	s.Verify(fmt.Sprintf(
		"driver -> /devices/sampleAlarms/controls/alarm_%s: [%s] (QoS 1, retained)",
		name, activeStr))
}

func (s *AlarmSuite) verifyNotificationMsgs(text string) {
	s.Verify(
		fmt.Sprintf("driver -> /devices/sampleAlarms/controls/log: [%s] (QoS 1, retained)", text),
		fmt.Sprintf("[info] EMAIL TO: someone@example.com SUBJ: alarm! TEXT: %s", text),
		fmt.Sprintf("[info] EMAIL TO: anotherone@example.com SUBJ: Alarm: %s TEXT: %s", text, text),
		fmt.Sprintf("[info] SMS TO: +78122128506 TEXT: %s", text),
	)
}

func (s *AlarmSuite) TestRepeatedExpectedValueAlarm() {
	s.loadAlarms()
	for i := 0; i < 3; i++ {
		s.publishControlValue("somedev", "importantDevicePower", "0",
			"sampleAlarms/alarm_importantDeviceIsOff", "sampleAlarms/log")
		s.verifyAlarmControlChange("importantDeviceIsOff", true)
		s.verifyNotificationMsgs("Important device is off")
		var timerId int
		s.Verify(testutils.RegexpCaptureMatcher(
			`^new fake ticker: (\d+), 200000`, func(m []string) bool {
				var err error
				timerId, err = strconv.Atoi(m[1])
				s.Ck("Atoi()", err)
				return true
			}))

		// no repeated alarm upon the same value
		s.publishControlValue("somedev", "importantDevicePower", "0")
		s.VerifyEmpty()

		for j := 0; j < 3; j++ {
			ts := s.AdvanceTime(200 * time.Second)
			s.FireTimer(uint64(timerId), ts)
			s.Verify(fmt.Sprintf("timer.fire(): %d", timerId))
			s.expectControlChange("sampleAlarms/log")
			s.verifyNotificationMsgs("Important device is off")
		}

		s.publishControlValue("somedev", "importantDevicePower", "1",
			"sampleAlarms/alarm_importantDeviceIsOff", "sampleAlarms/log")
		s.verifyAlarmControlChange("importantDeviceIsOff", false)
		s.Verify(fmt.Sprintf("timer.Stop(): %d", timerId))
		s.verifyNotificationMsgs("Important device is back on")

		// alarm stays off
		s.publishControlValue("somedev", "importantDevicePower", "1")
		s.VerifyEmpty()
	}
}

func (s *AlarmSuite) TestNonRepeatedExpectedValueAlarm() {
	s.loadAlarms()
	for i := 0; i < 3; i++ {
		s.publishControlValue("somedev", "unnecessaryDevicePower", "1",
			"sampleAlarms/alarm_unnecessaryDeviceIsOn", "sampleAlarms/log")
		s.verifyAlarmControlChange("unnecessaryDeviceIsOn", true)
		s.verifyNotificationMsgs("Unnecessary device is on")

		// no repeated alarm upon the same value
		s.publishControlValue("somedev", "unnecessaryDevicePower", "1")
		s.VerifyEmpty()

		s.publishControlValue("somedev", "unnecessaryDevicePower", "0",
			"sampleAlarms/alarm_unnecessaryDeviceIsOn", "sampleAlarms/log")
		s.verifyAlarmControlChange("unnecessaryDeviceIsOn", false)
		s.verifyNotificationMsgs("somedev/unnecessaryDevicePower is back to normal, value = false")
		s.VerifyEmpty()

		s.publishControlValue("somedev", "unnecessaryDevicePower", "0")
		s.VerifyEmpty()
	}
}

func (s *AlarmSuite) setOutOfRangeTemp(temp int) {
	s.publishControlValue("somedev", "devTemp", strconv.Itoa(temp),
		"sampleAlarms/alarm_temperatureOutOfBounds", "sampleAlarms/log")
	s.verifyAlarmControlChange("temperatureOutOfBounds", true)
	s.verifyNotificationMsgs(fmt.Sprintf("Temperature out of bounds, value = %d", temp))
	s.Verify(regexp.MustCompile(`^new fake ticker: \d+, 10000$`))
}

func (s *AlarmSuite) setOkTemp(temp int, stopTimer bool) {
	s.publishControlValue("somedev", "devTemp", strconv.Itoa(temp),
		"sampleAlarms/alarm_temperatureOutOfBounds", "sampleAlarms/log")
	s.verifyAlarmControlChange("temperatureOutOfBounds", false)
	if stopTimer {
		s.Verify(regexp.MustCompile(`^timer\.Stop\(\): \d+`))
	}
	s.verifyNotificationMsgs(fmt.Sprintf("Temperature is within bounds again, value = %d", temp))
	s.VerifyEmpty()
}

func (s *AlarmSuite) TestRepeatedMinMaxAlarmWithMaxCount() {
	s.loadAlarms()

	// go below min
	s.setOutOfRangeTemp(9)

	s.publishControlValue("somedev", "devTemp", "8")
	s.VerifyEmpty() // still out of bounds, but timer wasn't fired yet

	for i := 0; i < 4; i++ {
		ts := s.AdvanceTime(10 * time.Millisecond)
		s.FireTimer(1, ts)
		s.Verify("timer.fire(): 1")
		s.expectControlChange("sampleAlarms/log")
		s.verifyNotificationMsgs("Temperature out of bounds, value = 8")
	}
	s.Verify("timer.Stop(): 1")

	s.setOkTemp(10, false)

	// go over max
	s.setOutOfRangeTemp(16)
	s.setOkTemp(15, true)
}

func (s *AlarmSuite) TestMinAlarm() {
	s.loadAlarmsSkipping("**maxtemp**")
	s.setOutOfRangeTemp(9)
	s.setOkTemp(16, true) // maxValue removed, 16 must be ok
}

func (s *AlarmSuite) TestMaxAlarm() {
	s.loadAlarmsSkipping("**mintemp**")
	s.setOutOfRangeTemp(16)
	s.setOkTemp(9, true) // minValue removed, 9 must be ok
}

func TestAlarmSuite(t *testing.T) {
	testutils.RunSuites(t,
		new(AlarmSuite),
	)
}
