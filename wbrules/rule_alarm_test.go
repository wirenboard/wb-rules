package wbrules

import (
	"encoding/json"
	"fmt"
	"github.com/contactless/wbgo/testutils"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"
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

	s.Verify(
		"driver -> /devices/sampleAlarms/meta/name: [Sample Alarms] (QoS 1, retained)",
		"driver -> /devices/sampleAlarms/controls/alarm_importantDeviceIsOff/meta/type: [alarm] (QoS 1, retained)",
		"driver -> /devices/sampleAlarms/controls/alarm_importantDeviceIsOff/meta/readonly: [1] (QoS 1, retained)",
		"driver -> /devices/sampleAlarms/controls/alarm_importantDeviceIsOff/meta/order: [1] (QoS 1, retained)",
		"driver -> /devices/sampleAlarms/controls/alarm_importantDeviceIsOff: [0] (QoS 1, retained)",
		"driver -> /devices/sampleAlarms/controls/alarm_temperatureOutOfBounds/meta/type: [alarm] (QoS 1, retained)",
		"driver -> /devices/sampleAlarms/controls/alarm_temperatureOutOfBounds/meta/readonly: [1] (QoS 1, retained)",
		"driver -> /devices/sampleAlarms/controls/alarm_temperatureOutOfBounds/meta/order: [2] (QoS 1, retained)",
		"driver -> /devices/sampleAlarms/controls/alarm_temperatureOutOfBounds: [0] (QoS 1, retained)",
		"driver -> /devices/sampleAlarms/controls/alarm_unnecessaryDeviceIsOn/meta/type: [alarm] (QoS 1, retained)",
		"driver -> /devices/sampleAlarms/controls/alarm_unnecessaryDeviceIsOn/meta/readonly: [1] (QoS 1, retained)",
		"driver -> /devices/sampleAlarms/controls/alarm_unnecessaryDeviceIsOn/meta/order: [3] (QoS 1, retained)",
		"driver -> /devices/sampleAlarms/controls/alarm_unnecessaryDeviceIsOn: [0] (QoS 1, retained)",
		"driver -> /devices/sampleAlarms/controls/log/meta/type: [text] (QoS 1, retained)",
		"driver -> /devices/sampleAlarms/controls/log/meta/readonly: [1] (QoS 1, retained)",
		"driver -> /devices/sampleAlarms/controls/log/meta/order: [4] (QoS 1, retained)",
		"driver -> /devices/sampleAlarms/controls/log: [] (QoS 1, retained)",
	)
}

func (s *AlarmSuite) cellRef(dev, ctl string) (cellRef, topicBase string) {
	return dev + "/" + ctl, fmt.Sprintf("/devices/%s/controls/%s", dev, ctl)
}

func (s *AlarmSuite) publishCell(dev, ctl, typ, value string) {
	cellRef, topicBase := s.cellRef(dev, ctl)
	s.publish(topicBase+"/meta/type", typ, cellRef)
	s.publish(topicBase, value, cellRef)
	s.Verify(
		fmt.Sprintf("tst -> %s/meta/type: [%s] (QoS 1, retained)", topicBase, typ),
		fmt.Sprintf("tst -> %s: [%s] (QoS 1, retained)", topicBase, value))
}

func (s *AlarmSuite) publishTestDev() {
	s.publishCell("somedev", "importantDevicePower", "switch", "1")
	s.publishCell("somedev", "unnecessaryDevicePower", "switch", "0")
	s.publishCell("somedev", "devTemp", "temperature", "11")
}

func (s *AlarmSuite) publishCellValue(dev, ctl, value string, expectedCellNames ...string) {
	cellRef, topicBase := s.cellRef(dev, ctl)
	s.publish(topicBase, value, append([]string{cellRef}, expectedCellNames...)...)
	s.Verify(fmt.Sprintf("tst -> %s: [%s] (QoS 1, retained)", topicBase, value))
}

func (s *AlarmSuite) verifyAlarmCellChange(name string, active bool) {
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
		s.publishCellValue("somedev", "importantDevicePower", "0",
			"sampleAlarms/importantDeviceIsOff", "sampleAlarms/log")
		s.verifyAlarmCellChange("importantDeviceIsOff", true)
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
		s.publishCellValue("somedev", "importantDevicePower", "0")
		s.VerifyEmpty()

		for j := 0; j < 3; j++ {
			ts := s.AdvanceTime(200 * time.Second)
			s.FireTimer(uint64(timerId), ts)
			s.Verify(fmt.Sprintf("timer.fire(): %d", timerId))
			s.expectCellChange("sampleAlarms/importantDeviceIsOff")
			s.verifyNotificationMsgs("Important device is off")
		}

		s.publishCellValue("somedev", "importantDevicePower", "1",
			"sampleAlarms/importantDeviceIsOff", "sampleAlarms/log")
		s.verifyAlarmCellChange("importantDeviceIsOff", false)
		s.Verify(fmt.Sprintf("timer.Stop(): %d", timerId))
		s.verifyNotificationMsgs("Important device is back on")

		// alarm stays off
		s.publishCellValue("somedev", "importantDevicePower", "1")
		s.VerifyEmpty()
	}
}

func (s *AlarmSuite) TestNonRepeatedExpectedValueAlarm() {
	s.loadAlarms()
	for i := 0; i < 3; i++ {
		s.publishCellValue("somedev", "unnecessaryDevicePower", "1",
			"sampleAlarms/unnecessaryDeviceIsOn", "sampleAlarms/log")
		s.verifyAlarmCellChange("unnecessaryDeviceIsOn", true)
		s.verifyNotificationMsgs("Unnecessary device is on")

		// no repeated alarm upon the same value
		s.publishCellValue("somedev", "unnecessaryDevicePower", "1")
		s.VerifyEmpty()

		s.publishCellValue("somedev", "unnecessaryDevicePower", "0",
			"sampleAlarms/unnecessaryDeviceIsOn", "sampleAlarms/log")
		s.verifyAlarmCellChange("unnecessaryDeviceIsOn", false)
		s.verifyNotificationMsgs("somedev/unnecessaryDevicePower is back to normal, value = false")
		s.VerifyEmpty()

		s.publishCellValue("somedev", "unnecessaryDevicePower", "0")
		s.VerifyEmpty()
	}
}

func (s *AlarmSuite) setOutOfRangeTemp(temp int) {
	s.publishCellValue("somedev", "devTemp", strconv.Itoa(temp),
		"sampleAlarms/temperatureOutOfBounds", "sampleAlarms/log")
	s.verifyAlarmCellChange("temperatureOutOfBounds", true)
	s.verifyNotificationMsgs(fmt.Sprintf("Temperature out of bounds, value = %d", temp))
	s.Verify(regexp.MustCompile(`^new fake ticker: \d+, 10000$`))
}

func (s *AlarmSuite) setOkTemp(temp int, stopTimer bool) {
	s.publishCellValue("somedev", "devTemp", strconv.Itoa(temp),
		"sampleAlarms/temperatureOutOfBounds", "sampleAlarms/log")
	s.verifyAlarmCellChange("temperatureOutOfBounds", false)
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

	s.publishCellValue("somedev", "devTemp", "8")
	s.VerifyEmpty() // still out of bounds, but timer wasn't fired yet

	for i := 0; i < 4; i++ {
		ts := s.AdvanceTime(10 * time.Millisecond)
		s.FireTimer(1, ts)
		s.Verify("timer.fire(): 1")
		s.expectCellChange("sampleAlarms/importantDeviceIsOff")
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
