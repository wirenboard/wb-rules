package wbrules

import (
	"strings"
	"testing"
        wbgo "github.com/contactless/wbgo"
	"github.com/stretchr/testify/assert"
)

type cellFixture struct {
	t *testing.T
	driver *wbgo.Driver
	broker *wbgo.FakeMQTTBroker
	client wbgo.MQTTClient
	model *CellModel
	cellChange chan string
}

func NewCellFixture(t *testing.T) *cellFixture {
	fixture := &cellFixture{
		t: t,
		broker: wbgo.NewFakeMQTTBroker(t),
		model: NewCellModel(),
	}
	fixture.client = fixture.broker.MakeClient("tst")
	fixture.client.Start()
	fixture.driver = wbgo.NewDriver(fixture.model, fixture.broker.MakeClient("driver"))
	fixture.driver.SetAutoPoll(false)
	fixture.driver.SetAcceptsExternalDevices(true)
	fixture.cellChange = fixture.model.AcquireCellChangeChannel()
	return fixture
}

func (fixture *cellFixture) expectCellChange(expectedCellName string) {
	cellName := <- fixture.cellChange
	assert.Equal(fixture.t, expectedCellName, cellName)
}

func (fixture *cellFixture) publish(topic, value, expectedCellName string) {
	retained := !strings.HasSuffix(topic, "/on")
	fixture.client.Publish(wbgo.MQTTMessage{topic, value, 1, retained})
	fixture.expectCellChange(expectedCellName)
}

func (fixture *cellFixture) tearDown() {
	fixture.driver.Stop()
	_, ok := <- fixture.cellChange
	assert.False(fixture.t, ok)
}

func TestExternalCells(t *testing.T) {
	fixture := NewCellFixture(t)
	defer fixture.tearDown()
	fixture.driver.Start()
	dev := fixture.model.EnsureDevice("somedev")
	cell := dev.EnsureCell("paramOne")
	assert.Equal(t, "", cell.Value())
	assert.Equal(t, "text", cell.Type())

	fixture.publish("/devices/somedev/meta/name", "SomeDev", "")
	assert.Equal(t, "SomeDev", dev.Title())

	fixture.publish("/devices/somedev/controls/paramOne", "42", "paramOne")
	assert.Equal(t, "42", cell.Value())
	assert.Exactly(t, dev, fixture.model.EnsureDevice("somedev"))
	assert.Exactly(t, cell, dev.EnsureCell("paramOne"))
	assert.Equal(t, "text", cell.Type())
	assert.False(t, cell.IsComplete())

	fixture.publish("/devices/somedev/controls/paramOne/meta/type", "temperature", "paramOne")
	assert.Equal(t, "temperature", cell.Type())
	assert.Equal(t, float64(42), cell.Value())
	assert.True(t, cell.IsComplete())

	fixture.publish("/devices/somedev/controls/paramTwo/meta/type", "pressure", "paramTwo")
	cell2 := dev.EnsureCell("paramTwo")
	assert.Equal(t, "pressure", cell2.Type())
	assert.Equal(t, 0, cell2.Value())
	assert.False(t, cell2.IsComplete())

	fixture.publish("/devices/somedev/controls/paramTwo", "755", "paramTwo")
	assert.Equal(t, "pressure", cell2.Type())
	assert.Equal(t, 755, cell2.Value())
	assert.True(t, cell2.IsComplete())

	fixture.broker.Reset()
	cell3 := dev.EnsureCell("paramThree")
	assert.False(t, cell3.IsComplete())
	fixture.driver.CallSync(func () {
		cell3.SetValue(43)
	})
	fixture.expectCellChange("paramThree")

	assert.Equal(t, "43", cell3.Value())
	fixture.broker.Verify(
		"driver -> /devices/somedev/controls/paramThree/on: [43] (QoS 1)",
	)
}

func TestLocalCells(t *testing.T) {
	fixture := NewCellFixture(t)
	defer fixture.tearDown()
	dev := fixture.model.EnsureLocalDevice("somedev", "SomeDev")
	cell1 := dev.SetCell("sw", "switch", true)
	assert.True(t, cell1.IsComplete())
	cell2 := dev.SetCell("temp", "temperature", 20)
	assert.True(t, cell2.IsComplete())
	fixture.driver.Start()
	fixture.broker.Verify(
		"driver -> /devices/somedev/meta/name: [SomeDev] (QoS 1, retained)",
		"driver -> /devices/somedev/controls/sw/meta/type: [switch] (QoS 1, retained)",
		"driver -> /devices/somedev/controls/sw/meta/order: [1] (QoS 1, retained)",
		"driver -> /devices/somedev/controls/sw: [1] (QoS 1, retained)",
		"Subscribe -- driver: /devices/somedev/controls/sw/on",
		"driver -> /devices/somedev/controls/temp/meta/type: [temperature] (QoS 1, retained)",
		"driver -> /devices/somedev/controls/temp/meta/order: [2] (QoS 1, retained)",
		"driver -> /devices/somedev/controls/temp: [20] (QoS 1, retained)",
		"Subscribe -- driver: /devices/somedev/controls/temp/on",
		"Subscribe -- driver: /devices/+/meta/name",
		"Subscribe -- driver: /devices/+/controls/+",
		"Subscribe -- driver: /devices/+/controls/+/meta/type",
		"Subscribe -- driver: /devices/+/controls/+/meta/max",
	)
	assert.Equal(t, "switch", cell1.Type())
	assert.Equal(t, true, cell1.Value())
	assert.Equal(t, "temperature", cell2.Type())
	assert.Equal(t, 20, cell2.Value())
	assert.Exactly(t, dev, fixture.model.EnsureDevice("somedev"))

	fixture.publish("/devices/somedev/controls/sw/on", "0", "sw")
	assert.Equal(t, "switch", cell1.Type())
	assert.Equal(t, false, cell1.Value())
	fixture.broker.Verify(
		"tst -> /devices/somedev/controls/sw/on: [0] (QoS 1)",
		"driver -> /devices/somedev/controls/sw: [0] (QoS 1, retained)",
	)

	fixture.driver.CallSync(func () {
		cell2.SetValue(20) // this setting has no effect
		cell2.SetValue(22)
	})
	fixture.expectCellChange("temp")
	fixture.broker.Verify(
		"driver -> /devices/somedev/controls/temp: [22] (QoS 1, retained)",
	)
}

func TestLocalRangeCells(t *testing.T) {
	fixture := NewCellFixture(t)
	defer fixture.tearDown()
	dev := fixture.model.EnsureLocalDevice("somedev", "SomeDev")
	cell := dev.SetRangeCell("foo", "10", 200)
	assert.True(t, cell.IsComplete())
	fixture.driver.Start()
	fixture.broker.Verify(
		"driver -> /devices/somedev/meta/name: [SomeDev] (QoS 1, retained)",
		"driver -> /devices/somedev/controls/foo/meta/type: [range] (QoS 1, retained)",
		"driver -> /devices/somedev/controls/foo/meta/order: [1] (QoS 1, retained)",
		"driver -> /devices/somedev/controls/foo/meta/max: [200] (QoS 1, retained)",
		"driver -> /devices/somedev/controls/foo: [10] (QoS 1, retained)",
		"Subscribe -- driver: /devices/somedev/controls/foo/on",
		"Subscribe -- driver: /devices/+/meta/name",
		"Subscribe -- driver: /devices/+/controls/+",
		"Subscribe -- driver: /devices/+/controls/+/meta/type",
		"Subscribe -- driver: /devices/+/controls/+/meta/max",
	)
}

func TestExternalRangeCells(t *testing.T) {
	fixture := NewCellFixture(t)
	defer fixture.tearDown()
	fixture.driver.Start()
	fixture.publish("/devices/somedev/meta/name", "SomeDev", "")
	fixture.publish("/devices/somedev/controls/foo/meta/type", "range", "foo")
	fixture.publish("/devices/somedev/controls/foo/meta/max", "200", "foo")
	fixture.publish("/devices/somedev/controls/foo", "10", "foo")
	dev := fixture.model.EnsureDevice("somedev")
	cell := dev.EnsureCell("foo")
	assert.Equal(t, 10, cell.Value())
	assert.Equal(t, 200, cell.Max())
	assert.Equal(t, "range", cell.Type())
}
