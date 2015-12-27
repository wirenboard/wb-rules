package wbrules

import (
	"encoding/json"
	"fmt"
	"github.com/contactless/wbgo"
	"testing"
	"time"
)

type AlarmSuite struct {
	RuleSuiteBase
}

func (s *AlarmSuite) SetupTest() {
	s.SetupSkippingDefs("testrules_alarm.js")
	s.publishTestDev()
	s.loadAlarms()
}

func (s *AlarmSuite) loadAlarms() {
	confPath := s.CopyDataFileToTempDir("alarms.conf", "alarms.conf")
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
	for i := 0; i < 3; i++ {
		s.publishCellValue("somedev", "importantDevicePower", "0",
			"sampleAlarms/importantDeviceIsOff", "sampleAlarms/log")
		s.verifyAlarmCellChange("importantDeviceIsOff", true)
		s.verifyNotificationMsgs("Important device is off")
		s.Verify("new fake ticker: 1, 200000")

		// no repeated alarm upon the same value
		s.publishCellValue("somedev", "importantDevicePower", "0")
		s.VerifyEmpty()

		for j := 0; j < 3; j++ {
			ts := s.AdvanceTime(200 * time.Second)
			s.FireTimer(1, ts)
			s.Verify("timer.fire(): 1")
			s.expectCellChange("sampleAlarms/importantDeviceIsOff")
			s.verifyNotificationMsgs("Important device is off")
		}

		s.publishCellValue("somedev", "importantDevicePower", "1",
			"sampleAlarms/importantDeviceIsOff", "sampleAlarms/log")
		s.verifyAlarmCellChange("importantDeviceIsOff", false)
		s.Verify("timer.Stop(): 1")
		s.verifyNotificationMsgs("Important device is back on")

		// alarm stays off
		s.publishCellValue("somedev", "importantDevicePower", "1")
		s.VerifyEmpty()
	}
}

func (s *AlarmSuite) TestNonRepeatedExpectedValueAlarm() {
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

func (s *AlarmSuite) TestRepeatedMinMaxAlarmWithMaxCount() {
	// go below min
	s.publishCellValue("somedev", "devTemp", "9",
		"sampleAlarms/temperatureOutOfBounds", "sampleAlarms/log")
	s.verifyAlarmCellChange("temperatureOutOfBounds", true)
	s.verifyNotificationMsgs("Temperature out of bounds, value = 9")
	s.Verify("new fake ticker: 1, 10000")

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

	s.publishCellValue("somedev", "devTemp", "10",
		"sampleAlarms/temperatureOutOfBounds", "sampleAlarms/log")
	s.verifyAlarmCellChange("temperatureOutOfBounds", false)
	s.verifyNotificationMsgs("Temperature is within bounds again, value = 10")
	s.VerifyEmpty()

	// go over max
	s.publishCellValue("somedev", "devTemp", "16",
		"sampleAlarms/temperatureOutOfBounds", "sampleAlarms/log")
	s.verifyAlarmCellChange("temperatureOutOfBounds", true)
	s.verifyNotificationMsgs("Temperature out of bounds, value = 16")
	s.Verify("new fake ticker: 1, 10000")

	s.publishCellValue("somedev", "devTemp", "15",
		"sampleAlarms/temperatureOutOfBounds", "sampleAlarms/log")
	s.verifyAlarmCellChange("temperatureOutOfBounds", false)
	s.Verify("timer.Stop(): 1")
	s.verifyNotificationMsgs("Temperature is within bounds again, value = 15")
	s.VerifyEmpty()
}

// TBD: test min alarm
// TBD: test max alarm

func TestAlarmSuite(t *testing.T) {
	wbgo.RunSuites(t,
		new(AlarmSuite),
	)
}
