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
	confPath := s.CopyDataFileToTempDir("alarms.conf", "alarms.conf")
	confPathJS, err := json.Marshal(confPath)
	if err != nil {
		panic("json.Marshal() failed on string?")
	}

	// here we simulate loading of alarms into the running engine
	s.Ck("failed to init alarms", s.engine.EvalScript(fmt.Sprintf("Alarms.load(%s)", confPathJS)))
	s.engine.Refresh()

	s.publishTestDev()
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

func (s *AlarmSuite) publishCellValue(dev, ctl, value string) {
	cellRef, topicBase := s.cellRef(dev, ctl)
	s.publish(topicBase, value, cellRef)
	s.Verify(fmt.Sprintf("tst -> %s: [%s] (QoS 1, retained)", topicBase, value))
}

func (s *AlarmSuite) TestRepeatedExpectedValueAlarm() {
	for i := 0; i < 3; i++ {
		s.publishCellValue("somedev", "importantDevicePower", "0")
		s.Verify(
			"[info] EMAIL TO: someone@example.com SUBJ: alarm! TEXT: Important device is off",
			"[info] EMAIL TO: anotherone@example.com SUBJ: Alarm: Important device is off "+
				"TEXT: Important device is off",
			"[info] SMS TO: +78122128506 TEXT: Important device is off",
			"new fake ticker: 1, 200000",
		)

		// no repeated alarm upon the same value
		s.publishCellValue("somedev", "importantDevicePower", "0")
		s.VerifyEmpty()

		for j := 0; j < 3; j++ {
			ts := s.AdvanceTime(5 * 500 * time.Millisecond)
			s.FireTimer(1, ts)
			s.Verify(
				"timer.fire(): 1",
				"[info] EMAIL TO: someone@example.com SUBJ: alarm! TEXT: Important device is off",
				"[info] EMAIL TO: anotherone@example.com SUBJ: Alarm: Important device is off "+
					"TEXT: Important device is off",
				"[info] SMS TO: +78122128506 TEXT: Important device is off",
			)
		}

		s.publishCellValue("somedev", "importantDevicePower", "1")
		s.Verify(
			"timer.Stop(): 1",
			"[info] EMAIL TO: someone@example.com SUBJ: alarm! TEXT: Important device is back on",
			"[info] EMAIL TO: anotherone@example.com SUBJ: Alarm: Important device is back on "+
				"TEXT: Important device is back on",
			"[info] SMS TO: +78122128506 TEXT: Important device is back on",
		)

		// alarm stays off
		s.publishCellValue("somedev", "importantDevicePower", "1")
		s.VerifyEmpty()
	}
}

func TestAlarmSuite(t *testing.T) {
	wbgo.RunSuites(t,
		new(AlarmSuite),
	)
}
