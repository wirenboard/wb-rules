package wbrules

import (
	"fmt"
	"github.com/contactless/wbgo"
	"github.com/contactless/wbgo/testutils"
	"log"
	"sort"
	"strings"
	"testing"
	"time"
)

const (
	EXTRA_CELL_CHANGE_WAIT_TIME_MS = 50
)

type CellSuiteBase struct {
	testutils.Suite
	*testutils.FakeMQTTFixture
	driver               *wbgo.Driver
	client, driverClient wbgo.MQTTClient
	model                *CellModel
	cellChange           chan *CellSpec
}

func (s *CellSuiteBase) T() *testing.T {
	return s.Suite.T()
}

func (s *CellSuiteBase) SetupTest(waitForRetained bool) {
	s.Suite.SetupTest()
	s.FakeMQTTFixture = testutils.NewFakeMQTTFixture(s.T())
	s.model = NewCellModel()
	if waitForRetained {
		s.Broker.SetWaitForRetained(true)
	}
	s.client = s.Broker.MakeClient("tst")
	s.client.Start()
	s.driverClient = s.Broker.MakeClient("driver")
	s.driver = wbgo.NewDriver(s.model, s.driverClient)
	s.driver.SetAutoPoll(false)
	s.driver.SetAcceptsExternalDevices(true)
	s.cellChange = s.model.AcquireCellChangeChannel()
}

func (s *CellSuiteBase) expectCellChange(expectedCellNames ...string) {
	// Notifications happen asynchronously and aren't guaranteed to be
	// keep original order. Perhaps this needs to be fixed.
	actualCellNames := make([]string, len(expectedCellNames))
	for i := range actualCellNames {
		cellSpec := <-s.cellChange
		fullName := ""
		if cellSpec != nil {
			fullName = fmt.Sprintf("%s/%s", cellSpec.DevName, cellSpec.CellName)
		}
		actualCellNames[i] = fullName
	}
	sort.Strings(expectedCellNames)
	sort.Strings(actualCellNames)
	timer := time.NewTimer(EXTRA_CELL_CHANGE_WAIT_TIME_MS * time.Millisecond)
	select {
	case <-timer.C:
	case cellSpec := <-s.cellChange:
		s.Require().Fail("unexpected cell change", "cell: %v", cellSpec)
	}
}

func (s *CellSuiteBase) publish(topic, value string, expectedCellNames ...string) {
	retained := !strings.HasSuffix(topic, "/on")
	s.client.Publish(wbgo.MQTTMessage{topic, value, 1, retained})
	s.expectCellChange(expectedCellNames...)
}

func (s *CellSuiteBase) TearDownTest() {
	s.driver.Stop()
	cellSpec, ok := <-s.cellChange
	if ok {
		log.Printf("WARNING! unexpected cell change at the end of the test: %v", cellSpec)
	}
	s.Suite.TearDownTest()
}

type CellSuite struct {
	CellSuiteBase
}

func (s *CellSuite) SetupTest() {
	s.CellSuiteBase.SetupTest(false)
}

func (s *CellSuite) TestExternalCells() {
	s.driver.Start()
	s.SkipTill("Subscribe -- driver: /devices/+/controls/+/meta/max")

	dev := s.model.EnsureDevice("somedev")
	cell := dev.EnsureCell("paramOne")
	s.Equal("", cell.Value())
	s.Equal("text", cell.Type())

	s.publish("/devices/somedev/meta/name", "SomeDev", "")
	s.Equal("SomeDev", dev.Title())

	s.publish("/devices/somedev/controls/paramOne", "42", "somedev/paramOne")
	s.Equal("42", cell.Value())
	s.Exactly(dev, s.model.EnsureDevice("somedev"))
	s.Exactly(cell, dev.EnsureCell("paramOne"))
	s.Equal("text", cell.Type())
	s.False(cell.IsComplete())

	s.publish("/devices/somedev/controls/paramOne/meta/type", "temperature",
		"somedev/paramOne")
	s.Equal("temperature", cell.Type())
	s.Equal(float64(42), cell.Value())
	s.True(cell.IsComplete())

	s.publish("/devices/somedev/controls/paramTwo/meta/type", "pressure",
		"somedev/paramTwo")
	cell2 := dev.EnsureCell("paramTwo")
	s.Equal("pressure", cell2.Type())
	s.Equal(float64(0), cell2.Value())
	s.False(cell2.IsComplete())

	s.publish("/devices/somedev/controls/paramTwo", "755",
		"somedev/paramTwo")
	s.Equal("pressure", cell2.Type())
	s.Equal(float64(755), cell2.Value())
	s.True(cell2.IsComplete())

	s.SkipTill("tst -> /devices/somedev/controls/paramTwo: [755] (QoS 1, retained)")
	cell3 := dev.EnsureCell("paramThree")
	s.False(cell3.IsComplete())

	for i := 0; i < 3; i++ {
		s.driver.CallSync(func() {
			cell3.SetValue(43)
		})
		s.Verify(
			"driver -> /devices/somedev/controls/paramThree/on: [43] (QoS 1)",
		)
		s.expectCellChange() // no cell change till external 'somedev' driver answers
		if i == 0 {
			s.Equal("", cell3.Value()) // not changed yet
		}
		s.publish("/devices/somedev/controls/paramThree", "43", "somedev/paramThree")
		s.Verify(
			"tst -> /devices/somedev/controls/paramThree: [43] (QoS 1, retained)",
		)
		s.Equal("43", cell3.Value())
	}
}

func (s *CellSuite) TestLocalCells() {
	dev := s.model.EnsureLocalDevice("somedev", "SomeDev")
	cell1 := dev.SetCell("sw", "switch", true, false)
	s.True(cell1.IsComplete())
	cell2 := dev.SetCell("temp", "temperature", 20, false)
	s.True(cell2.IsComplete())
	s.driver.Start()
	s.Verify(
		"driver -> /devices/somedev/meta/name: [SomeDev] (QoS 1, retained)",
		"Subscribe -- driver: /devices/+/meta/name",
		"Subscribe -- driver: /devices/+/controls/+",
		"Subscribe -- driver: /devices/+/controls/+/meta/type",
		"Subscribe -- driver: /devices/+/controls/+/meta/max",
		"driver -> /devices/somedev/controls/sw/meta/type: [switch] (QoS 1, retained)",
		"driver -> /devices/somedev/controls/sw/meta/order: [1] (QoS 1, retained)",
		"driver -> /devices/somedev/controls/sw: [1] (QoS 1, retained)",
		"Subscribe -- driver: /devices/somedev/controls/sw/on",
		"driver -> /devices/somedev/controls/temp/meta/type: [temperature] (QoS 1, retained)",
		"driver -> /devices/somedev/controls/temp/meta/order: [2] (QoS 1, retained)",
		"driver -> /devices/somedev/controls/temp: [20] (QoS 1, retained)",
		"Subscribe -- driver: /devices/somedev/controls/temp/on",
	)
	s.Equal("switch", cell1.Type())
	s.Equal(true, cell1.Value())
	s.Equal("temperature", cell2.Type())
	s.Equal(float64(20), cell2.Value())
	s.Exactly(dev, s.model.EnsureDevice("somedev"))

	s.publish("/devices/somedev/controls/sw/on", "0", "somedev/sw")
	s.Equal("switch", cell1.Type())
	s.Equal(false, cell1.Value())
	s.Verify(
		"tst -> /devices/somedev/controls/sw/on: [0] (QoS 1)",
		"driver -> /devices/somedev/controls/sw: [0] (QoS 1, retained)",
	)

	s.driver.CallSync(func() {
		cell2.SetValue(20) // setting the same value again, still generates a message
		cell2.SetValue(22)
	})
	s.expectCellChange("somedev/temp", "somedev/temp")
	s.Verify(
		"driver -> /devices/somedev/controls/temp: [20] (QoS 1, retained)",
		"driver -> /devices/somedev/controls/temp: [22] (QoS 1, retained)",
	)
}

func (s *CellSuite) TestLocalRangeCells() {
	dev := s.model.EnsureLocalDevice("somedev", "SomeDev")
	cell := dev.SetRangeCell("foo", "10", 200, false)
	s.True(cell.IsComplete())
	s.driver.Start()
	s.Verify(
		"driver -> /devices/somedev/meta/name: [SomeDev] (QoS 1, retained)",
		"Subscribe -- driver: /devices/+/meta/name",
		"Subscribe -- driver: /devices/+/controls/+",
		"Subscribe -- driver: /devices/+/controls/+/meta/type",
		"Subscribe -- driver: /devices/+/controls/+/meta/max",
		"driver -> /devices/somedev/controls/foo/meta/type: [range] (QoS 1, retained)",
		"driver -> /devices/somedev/controls/foo/meta/order: [1] (QoS 1, retained)",
		"driver -> /devices/somedev/controls/foo/meta/max: [200] (QoS 1, retained)",
		"driver -> /devices/somedev/controls/foo: [10] (QoS 1, retained)",
		"Subscribe -- driver: /devices/somedev/controls/foo/on",
	)
}

func (s *CellSuite) TestExternalRangeCells() {
	s.driver.Start()
	s.SkipTill("Subscribe -- driver: /devices/+/controls/+/meta/max")

	s.publish("/devices/somedev/meta/name", "SomeDev", "")
	s.publish("/devices/somedev/controls/foo/meta/type", "range", "somedev/foo")
	s.publish("/devices/somedev/controls/foo/meta/max", "200", "somedev/foo")
	s.publish("/devices/somedev/controls/foo", "10", "somedev/foo")
	dev := s.model.EnsureDevice("somedev")
	cell := dev.EnsureCell("foo")
	s.Equal(float64(10), cell.Value())
	s.Equal(float64(200), cell.Max())
	s.Equal("range", cell.Type())
}

func (s *CellSuite) TestLocalButtonCells() {
	dev := s.model.EnsureLocalDevice("somedev", "SomeDev")
	cell := dev.SetButtonCell("foo")
	s.True(cell.IsComplete())
	s.driver.Start()
	s.Verify(
		"driver -> /devices/somedev/meta/name: [SomeDev] (QoS 1, retained)",
		"Subscribe -- driver: /devices/+/meta/name",
		"Subscribe -- driver: /devices/+/controls/+",
		"Subscribe -- driver: /devices/+/controls/+/meta/type",
		"Subscribe -- driver: /devices/+/controls/+/meta/max",
		"driver -> /devices/somedev/controls/foo/meta/type: [pushbutton] (QoS 1, retained)",
		"driver -> /devices/somedev/controls/foo/meta/order: [1] (QoS 1, retained)",
		"Subscribe -- driver: /devices/somedev/controls/foo/on",
	)
	for i := 0; i < 3; i++ {
		s.driver.CallSync(func() {
			cell.SetValue(i == 0) // true, then false, then false again
		})
		s.expectCellChange("somedev/foo")
		s.Verify(
			"driver -> /devices/somedev/controls/foo: [1] (QoS 1)",
		)
	}
	s.driver.CallSync(func() {
		cell.SetValue(1)
		cell.SetValue(1)
	})
	s.expectCellChange("somedev/foo", "somedev/foo")
	s.Verify(
		"driver -> /devices/somedev/controls/foo: [1] (QoS 1)",
		"driver -> /devices/somedev/controls/foo: [1] (QoS 1)",
	)
}

func (s *CellSuite) TestExternalButtonCells() {
	s.driver.Start()
	s.SkipTill("Subscribe -- driver: /devices/+/controls/+/meta/max")

	s.publish("/devices/somedev/meta/name", "SomeDev", "")
	s.publish("/devices/somedev/controls/foo/meta/type", "pushbutton", "somedev/foo")
	// note that pushbutton cells don't need any value to be complete
	dev := s.model.EnsureDevice("somedev")
	cell := dev.EnsureCell("foo")
	s.Equal(false, cell.Value())
	s.Equal("pushbutton", cell.Type())
	s.True(cell.IsComplete())

	for i := 0; i < 3; i++ {
		s.publish("/devices/somedev/controls/foo", "1", "somedev/foo")
		s.Equal(false, cell.Value())
	}
}

func (s *CellSuite) TestConvertRemoteToLocal() {
	// 'remote to local' transition happens when a new device is defined
	// that matches some previously retained metadata
	s.driver.Start()
	s.Verify(
		"Subscribe -- driver: /devices/+/meta/name",
		"Subscribe -- driver: /devices/+/controls/+",
		"Subscribe -- driver: /devices/+/controls/+/meta/type",
		"Subscribe -- driver: /devices/+/controls/+/meta/max",
	)
	s.publish("/devices/somedev/meta/name", "SomeDev", "")
	s.publish("/devices/somedev/controls/sw/meta/type", "switch", "somedev/sw")
	s.publish("/devices/somedev/controls/sw", "1", "somedev/sw")
	s.Verify(
		"tst -> /devices/somedev/meta/name: [SomeDev] (QoS 1, retained)",
		"tst -> /devices/somedev/controls/sw/meta/type: [switch] (QoS 1, retained)",
		"tst -> /devices/somedev/controls/sw: [1] (QoS 1, retained)",
	)

	var dev *CellModelLocalDevice
	var cell *Cell
	s.driver.CallSync(func() {
		dev = s.model.EnsureLocalDevice("somedev", "SomeDev1")
		// note that we use 'false' as the default value of the switch,
		// but it picks up the retained value
		cell = dev.SetCell("sw", "switch", false, false)
	})
	s.Verify(
		"driver -> /devices/somedev/meta/name: [SomeDev1] (QoS 1, retained)",
		"driver -> /devices/somedev/controls/sw/meta/type: [switch] (QoS 1, retained)",
		"driver -> /devices/somedev/controls/sw/meta/order: [1] (QoS 1, retained)",
		"driver -> /devices/somedev/controls/sw: [1] (QoS 1, retained)",
		"Subscribe -- driver: /devices/somedev/controls/sw/on",
	)
	s.driver.CallSync(func() {
		s.True(cell.IsComplete())
		s.True(cell.Value().(bool))
	})
}

func (s *CellSuite) TestDeviceRedefinition() {
	dev := s.model.EnsureLocalDevice("somedev", "SomeDev")
	swCell := dev.SetCell("sw", "switch", false, false)
	dev.SetCell("temp", "temperature", 20, false)

	s.driver.Start()
	s.SkipTill("Subscribe -- driver: /devices/somedev/controls/temp/on")

	s.publish("/devices/somedev/controls/sw/on", "1", "somedev/sw")
	s.Verify(
		"tst -> /devices/somedev/controls/sw/on: [1] (QoS 1)",
		"driver -> /devices/somedev/controls/sw: [1] (QoS 1, retained)",
	)

	s.driver.CallSync(func() {
		s.True(swCell.Value().(bool))
		s.model.RemoveLocalDevice("somedev")
		dev = s.model.EnsureLocalDevice("somedev", "SomeDev1")
		swCell = dev.SetCell("sw", "switch", false, false)
		dev.SetCell("temp", "temperature", 18, false)
	})

	// retained values are preserved
	s.Verify(
		"Unsubscribe -- driver: /devices/somedev/controls/sw/on",
		"Unsubscribe -- driver: /devices/somedev/controls/temp/on",
		"driver -> /devices/somedev/meta/name: [SomeDev1] (QoS 1, retained)",
		"driver -> /devices/somedev/controls/sw/meta/type: [switch] (QoS 1, retained)",
		"driver -> /devices/somedev/controls/sw/meta/order: [1] (QoS 1, retained)",
		"driver -> /devices/somedev/controls/sw: [1] (QoS 1, retained)",
		"Subscribe -- driver: /devices/somedev/controls/sw/on",
		"driver -> /devices/somedev/controls/temp/meta/type: [temperature] (QoS 1, retained)",
		"driver -> /devices/somedev/controls/temp/meta/order: [2] (QoS 1, retained)",
		"driver -> /devices/somedev/controls/temp: [20] (QoS 1, retained)",
		"Subscribe -- driver: /devices/somedev/controls/temp/on",
	)

	s.publish("/devices/somedev/controls/sw/on", "0", "somedev/sw")
	s.Verify(
		"tst -> /devices/somedev/controls/sw/on: [0] (QoS 1)",
		"driver -> /devices/somedev/controls/sw: [0] (QoS 1, retained)",
	)

	done := make(chan struct{})
	s.driver.CallSync(func() {
		s.True(swCell.IsComplete())
		s.False(swCell.Value().(bool))
		done <- struct{}{}
	})
	<-done
}

type WaitForRetainedCellSuite struct {
	CellSuiteBase
}

func (s *WaitForRetainedCellSuite) SetupTest() {
	s.CellSuiteBase.SetupTest(true)
}

func (s *WaitForRetainedCellSuite) TestAcceptRetainedValuesForLocalCells() {
	dev := s.model.EnsureLocalDevice("somedev", "SomeDev")
	cell1 := dev.SetCell("sw1", "switch", true, false)

	cell2 := dev.SetCell("sw2", "switch", false, false)

	s.driver.Start()
	s.Verify(
		// device .../meta/name being published first is actually
		// an unwanted side-effect of OnNewDevice(), but it doesn't
		// do much harm, so I'm not fixing it right now
		"driver -> /devices/somedev/meta/name: [SomeDev] (QoS 1, retained)",
		"Subscribe -- driver: /devices/+/meta/name",
		"Subscribe -- driver: /devices/+/controls/+",
		"Subscribe -- driver: /devices/+/controls/+/meta/type",
		"Subscribe -- driver: /devices/+/controls/+/meta/max",
	)
	s.VerifyEmpty()

	s.True(cell1.IsComplete())
	s.True(cell1.Value().(bool))
	s.True(cell2.IsComplete())
	s.False(cell2.Value().(bool))

	s.publish("/devices/somedev/controls/sw1", "0", "somedev/sw1")
	s.publish("/devices/somedev/controls/sw2", "1", "somedev/sw2")

	s.Verify(
		"tst -> /devices/somedev/controls/sw1: [0] (QoS 1, retained)",
		"tst -> /devices/somedev/controls/sw2: [1] (QoS 1, retained)",
	)
	s.VerifyEmpty()

	s.Broker.SetReady()
	s.Verify(
		"driver -> /devices/somedev/controls/sw1/meta/type: [switch] (QoS 1, retained)",
		"driver -> /devices/somedev/controls/sw1/meta/order: [1] (QoS 1, retained)",
		"driver -> /devices/somedev/controls/sw1: [0] (QoS 1, retained)",
		"Subscribe -- driver: /devices/somedev/controls/sw1/on",
		"driver -> /devices/somedev/controls/sw2/meta/type: [switch] (QoS 1, retained)",
		"driver -> /devices/somedev/controls/sw2/meta/order: [2] (QoS 1, retained)",
		"driver -> /devices/somedev/controls/sw2: [1] (QoS 1, retained)",
		"Subscribe -- driver: /devices/somedev/controls/sw2/on",
	)
	s.VerifyEmpty()

	s.True(cell1.IsComplete())
	s.False(cell1.Value().(bool))
	s.True(cell2.IsComplete())
	s.True(cell2.Value().(bool))

	s.publish("/devices/somedev/controls/sw2/on", "0", "somedev/sw2")
	s.False(cell1.Value().(bool))
	s.False(cell2.Value().(bool))
}

func TestCellSuite(t *testing.T) {
	testutils.RunSuites(t, new(CellSuite), new(WaitForRetainedCellSuite))
}
