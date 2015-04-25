package wbrules

import (
	"fmt"
	wbgo "github.com/contactless/wbgo"
	"github.com/stretchr/testify/assert"
	"log"
	"strings"
	"testing"
	"time"
)

const (
	EXTRA_CELL_CHANGE_WAIT_TIME_MS = 50
)

type cellFixture struct {
	t                    *testing.T
	driver               *wbgo.Driver
	broker               *wbgo.FakeMQTTBroker
	client, driverClient wbgo.MQTTClient
	model                *CellModel
	cellChange           chan *CellSpec
}

func NewCellFixture(t *testing.T, waitForRetained bool) *cellFixture {
	fixture := &cellFixture{
		t:      t,
		broker: wbgo.NewFakeMQTTBroker(t),
		model:  NewCellModel(),
	}
	if waitForRetained {
		fixture.broker.SetWaitForRetained(true)
	}
	fixture.client = fixture.broker.MakeClient("tst")
	fixture.client.Start()
	fixture.driverClient = fixture.broker.MakeClient("driver")
	fixture.driver = wbgo.NewDriver(fixture.model, fixture.driverClient)
	fixture.driver.SetAutoPoll(false)
	fixture.driver.SetAcceptsExternalDevices(true)
	fixture.cellChange = fixture.model.AcquireCellChangeChannel()
	wbgo.SetupTestLogging(t)
	return fixture
}

func (fixture *cellFixture) expectCellChange(expectedCellNames ...string) {
	for _, expectedCellName := range expectedCellNames {
		cellSpec := <-fixture.cellChange
		fullName := ""
		if cellSpec != nil {
			fullName = fmt.Sprintf("%s/%s", cellSpec.DevName, cellSpec.CellName)
		}
		assert.Equal(fixture.t, expectedCellName, fullName)
	}
	timer := time.NewTimer(EXTRA_CELL_CHANGE_WAIT_TIME_MS * time.Millisecond)
	select {
	case <-timer.C:
	case cellSpec := <-fixture.cellChange:
		fixture.t.Fatalf("unexpected cell change: %v", cellSpec)
	}
}

func (fixture *cellFixture) publish(topic, value string, expectedCellNames ...string) {
	retained := !strings.HasSuffix(topic, "/on")
	fixture.client.Publish(wbgo.MQTTMessage{topic, value, 1, retained})
	fixture.expectCellChange(expectedCellNames...)
}

func (fixture *cellFixture) tearDown() {
	fixture.driver.Stop()
	cellSpec, ok := <-fixture.cellChange
	if ok {
		log.Printf("WARNING! unexpected cell change at the end of the test: %v", cellSpec)
	}
}

func TestExternalCells(t *testing.T) {
	fixture := NewCellFixture(t, false)
	defer fixture.tearDown()
	fixture.driver.Start()
	dev := fixture.model.EnsureDevice("somedev")
	cell := dev.EnsureCell("paramOne")
	assert.Equal(t, "", cell.Value())
	assert.Equal(t, "text", cell.Type())

	fixture.publish("/devices/somedev/meta/name", "SomeDev", "")
	assert.Equal(t, "SomeDev", dev.Title())

	fixture.publish("/devices/somedev/controls/paramOne", "42", "somedev/paramOne")
	assert.Equal(t, "42", cell.Value())
	assert.Exactly(t, dev, fixture.model.EnsureDevice("somedev"))
	assert.Exactly(t, cell, dev.EnsureCell("paramOne"))
	assert.Equal(t, "text", cell.Type())
	assert.False(t, cell.IsComplete())

	fixture.publish("/devices/somedev/controls/paramOne/meta/type", "temperature",
		"somedev/paramOne")
	assert.Equal(t, "temperature", cell.Type())
	assert.Equal(t, float64(42), cell.Value())
	assert.True(t, cell.IsComplete())

	fixture.publish("/devices/somedev/controls/paramTwo/meta/type", "pressure",
		"somedev/paramTwo")
	cell2 := dev.EnsureCell("paramTwo")
	assert.Equal(t, "pressure", cell2.Type())
	assert.Equal(t, 0, cell2.Value())
	assert.False(t, cell2.IsComplete())

	fixture.publish("/devices/somedev/controls/paramTwo", "755",
		"somedev/paramTwo")
	assert.Equal(t, "pressure", cell2.Type())
	assert.Equal(t, 755, cell2.Value())
	assert.True(t, cell2.IsComplete())

	fixture.broker.SkipTill("tst -> /devices/somedev/controls/paramTwo: [755] (QoS 1, retained)")
	cell3 := dev.EnsureCell("paramThree")
	assert.False(t, cell3.IsComplete())
	fixture.driver.CallSync(func() {
		cell3.SetValue(43)
	})
	fixture.expectCellChange("somedev/paramThree")

	assert.Equal(t, "43", cell3.Value())
	fixture.broker.Verify(
		"driver -> /devices/somedev/controls/paramThree/on: [43] (QoS 1)",
	)
}

func TestLocalCells(t *testing.T) {
	fixture := NewCellFixture(t, false)
	defer fixture.tearDown()
	dev := fixture.model.EnsureLocalDevice("somedev", "SomeDev")
	cell1 := dev.SetCell("sw", "switch", true, false)
	assert.True(t, cell1.IsComplete())
	cell2 := dev.SetCell("temp", "temperature", 20, false)
	assert.True(t, cell2.IsComplete())
	fixture.driver.Start()
	fixture.broker.Verify(
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
	assert.Equal(t, "switch", cell1.Type())
	assert.Equal(t, true, cell1.Value())
	assert.Equal(t, "temperature", cell2.Type())
	assert.Equal(t, 20, cell2.Value())
	assert.Exactly(t, dev, fixture.model.EnsureDevice("somedev"))

	fixture.publish("/devices/somedev/controls/sw/on", "0", "somedev/sw")
	assert.Equal(t, "switch", cell1.Type())
	assert.Equal(t, false, cell1.Value())
	fixture.broker.Verify(
		"tst -> /devices/somedev/controls/sw/on: [0] (QoS 1)",
		"driver -> /devices/somedev/controls/sw: [0] (QoS 1, retained)",
	)

	fixture.driver.CallSync(func() {
		cell2.SetValue(20) // this setting has no effect
		cell2.SetValue(22)
	})
	fixture.expectCellChange("somedev/temp")
	fixture.broker.Verify(
		"driver -> /devices/somedev/controls/temp: [22] (QoS 1, retained)",
	)
}

func TestLocalRangeCells(t *testing.T) {
	fixture := NewCellFixture(t, false)
	defer fixture.tearDown()
	dev := fixture.model.EnsureLocalDevice("somedev", "SomeDev")
	cell := dev.SetRangeCell("foo", "10", 200, false)
	assert.True(t, cell.IsComplete())
	fixture.driver.Start()
	fixture.broker.Verify(
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

func TestExternalRangeCells(t *testing.T) {
	fixture := NewCellFixture(t, false)
	defer fixture.tearDown()
	fixture.driver.Start()
	fixture.publish("/devices/somedev/meta/name", "SomeDev", "")
	fixture.publish("/devices/somedev/controls/foo/meta/type", "range", "somedev/foo")
	fixture.publish("/devices/somedev/controls/foo/meta/max", "200", "somedev/foo")
	fixture.publish("/devices/somedev/controls/foo", "10", "somedev/foo")
	dev := fixture.model.EnsureDevice("somedev")
	cell := dev.EnsureCell("foo")
	assert.Equal(t, 10, cell.Value())
	assert.Equal(t, 200, cell.Max())
	assert.Equal(t, "range", cell.Type())
}

func TestAcceptRetainedValuesForLocalCells(t *testing.T) {
	fixture := NewCellFixture(t, true)
	defer fixture.tearDown()

	dev := fixture.model.EnsureLocalDevice("somedev", "SomeDev")
	cell1 := dev.SetCell("sw1", "switch", true, false)

	cell2 := dev.SetCell("sw2", "switch", false, false)

	fixture.driver.Start()
	fixture.broker.Verify(
		// device .../meta/name being published first is actually
		// an unwanted side-effect of OnNewDevice(), but it doesn't
		// do much harm, so I'm not fixing it right now
		"driver -> /devices/somedev/meta/name: [SomeDev] (QoS 1, retained)",
		"Subscribe -- driver: /devices/+/meta/name",
		"Subscribe -- driver: /devices/+/controls/+",
		"Subscribe -- driver: /devices/+/controls/+/meta/type",
		"Subscribe -- driver: /devices/+/controls/+/meta/max",
	)
	fixture.broker.VerifyEmpty()

	assert.True(t, cell1.IsComplete())
	assert.True(t, cell1.Value().(bool))
	assert.True(t, cell2.IsComplete())
	assert.False(t, cell2.Value().(bool))

	fixture.publish("/devices/somedev/controls/sw1", "0", "somedev/sw1")
	fixture.publish("/devices/somedev/controls/sw2", "1", "somedev/sw2")

	fixture.broker.Verify(
		"tst -> /devices/somedev/controls/sw1: [0] (QoS 1, retained)",
		"tst -> /devices/somedev/controls/sw2: [1] (QoS 1, retained)",
	)
	fixture.broker.VerifyEmpty()

	fixture.broker.SetReady()
	fixture.broker.Verify(
		"driver -> /devices/somedev/controls/sw1/meta/type: [switch] (QoS 1, retained)",
		"driver -> /devices/somedev/controls/sw1/meta/order: [1] (QoS 1, retained)",
		"driver -> /devices/somedev/controls/sw1: [0] (QoS 1, retained)",
		"Subscribe -- driver: /devices/somedev/controls/sw1/on",
		"driver -> /devices/somedev/controls/sw2/meta/type: [switch] (QoS 1, retained)",
		"driver -> /devices/somedev/controls/sw2/meta/order: [2] (QoS 1, retained)",
		"driver -> /devices/somedev/controls/sw2: [1] (QoS 1, retained)",
		"Subscribe -- driver: /devices/somedev/controls/sw2/on",
	)
	fixture.broker.VerifyEmpty()

	assert.True(t, cell1.IsComplete())
	assert.False(t, cell1.Value().(bool))
	assert.True(t, cell2.IsComplete())
	assert.True(t, cell2.Value().(bool))

	fixture.publish("/devices/somedev/controls/sw2/on", "0", "somedev/sw2")
	assert.False(t, cell1.Value().(bool))
	assert.False(t, cell2.Value().(bool))
}
