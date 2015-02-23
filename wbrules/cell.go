package wbrules

import (
        "fmt"
	"log"
	"sort"
	"errors"
	"strconv"
	wbgo "github.com/contactless/wbgo"
)

type CellModel struct {
	wbgo.ModelBase
	devices map[string]CellModelDevice
	cellChange chan string
	started bool
}

type CellModelDevice interface {
	wbgo.DeviceModel
	EnsureCell(name string) (cell *Cell)
	setValue(name, value string)
	queryParams()
}

type CellModelDeviceBase struct {
	wbgo.DeviceBase
	model *CellModel
	cells map[string]*Cell
	self CellModelDevice
}

type CellModelLocalDevice struct {
	CellModelDeviceBase
}

type CellModelExternalDevice struct {
	CellModelDeviceBase
}

type Cell struct {
	device CellModelDevice
	name string
	title string
	controlType string
	value string
}

func NewCellModel() *CellModel {
	return &CellModel{
		devices: make(map[string]CellModelDevice),
	}
}

func (model *CellModel) Start() error {
	model.started = true
	names := make([]string, 0, len(model.devices))
	for name := range model.devices {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		dev := model.devices[name]
		model.Observer.OnNewDevice(dev)
		dev.queryParams()
	}
	return nil
}

func (model *CellModel) newCellModelDevice(name string, title string) (dev *CellModelDeviceBase) {
	dev = &CellModelDeviceBase{
		model: model,
		cells: make(map[string]*Cell),
	}
	dev.DevName = name
	dev.DevTitle = title
	return
}

func (model *CellModel) makeExternalDevice(name string, title string) (dev *CellModelExternalDevice) {
	dev = &CellModelExternalDevice{*model.newCellModelDevice(name, title)}
	dev.self = dev
	model.devices[name] = dev
	return
}

func (model *CellModel) makeLocalDevice(name string, title string) (dev *CellModelLocalDevice) {
	dev = &CellModelLocalDevice{*model.newCellModelDevice(name, title)}
	dev.self = dev
	model.devices[name] = dev
	return
}

func (model *CellModel) EnsureDevice(name string) (dev CellModelDevice) {
	dev, found := model.devices[name]
	if !found {
		dev = model.makeExternalDevice(name, name)
		model.Observer.OnNewDevice(dev)
	}
	return
}

func (model *CellModel) EnsureLocalDevice(name, title string) *CellModelLocalDevice {
	if model.started {
		panic("Cannot register local devices after the model is started")
	}

	dev, found := model.devices[name]
	if !found {
		return model.makeLocalDevice(name, title)
	}

	if d, ok := dev.(*CellModelLocalDevice); ok {
		return d
	} else {
		panic("External/local device name conflict")
	}
}

func (model *CellModel) AddDevice(name string) (wbgo.ExternalDeviceModel, error) {
	dev, _ := model.EnsureDevice(name).(wbgo.ExternalDeviceModel)
	if dev != nil {
		return dev, nil
	}
	return nil, errors.New("Device %s is registered as a local device")
}

func (model *CellModel) AcquireCellChangeChannel() chan string {
	if model.cellChange == nil {
		model.cellChange = make(chan string)
	}
	return model.cellChange
}

func (model *CellModel) notify(cellName string) {
	if model.cellChange != nil {
		model.cellChange <- cellName
	}
}

func (dev *CellModelDeviceBase) SetTitle(title string) {
	dev.DevTitle = title
	dev.model.notify("")
}

func (dev *CellModelDeviceBase) SetCell(name, controlType string, value interface{}) (cell *Cell) {
	cell = &Cell{
		device: dev.self,
		name: name,
		title: name,
		controlType: controlType,
	}
	cell.setValueQuiet(value)
	dev.cells[name] = cell
	return
}

func (dev *CellModelDeviceBase) EnsureCell(name string) (cell *Cell) {
	cell, found := dev.cells[name]
	if !found {
		log.Printf("adding cell %s", name)
		cell = dev.SetCell(name, "text", "")
	}
	return
}

func (dev *CellModelDeviceBase) SendValue(name, value string) bool {
	log.Printf("cell %s <- %v", name, value)
	dev.EnsureCell(name).value = value
	dev.model.notify(name)
	return true
}

func (dev *CellModelDeviceBase) setValue(name, value string) {
	dev.Observer.OnValue(dev.self, name, value)
}

func (dev *CellModelLocalDevice) queryParams() {
	names := make([]string, 0, len(dev.cells))
	for name := range dev.cells {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		cell := dev.cells[name]
		dev.Observer.OnNewControl(dev, name, cell.controlType, cell.value, false)
	}
}

func (dev *CellModelExternalDevice) SendControlType(name, controlType string) {
	dev.EnsureCell(name).controlType = controlType
	dev.model.notify(name)
}

func (dev *CellModelExternalDevice) queryParams() {
	// NOOP
}

func (cell *Cell) RawValue() string {
	return cell.value
}

func (cell *Cell) Value() interface{} {
	log.Printf("cell %s internal value = %v", cell.name, cell.value)
	switch cell.controlType {
	case "text":
		return cell.value
	case "switch", "wo-switch":
		return cell.value == "1"
	default:
		if r, err := strconv.ParseFloat(cell.value, 64); err != nil {
			return float64(0)
		} else {
			return r
		}
	}
}

func (cell *Cell) setValueQuiet(value interface{}) bool {
	var newValue string
	switch v := value.(type) {
	case string:
		newValue = v
	case bool:
		if v {
			newValue = "1"
		} else {
			newValue = "0"
		}
	default:
		newValue = fmt.Sprintf("%v", value)
	}

	if cell.value != newValue {
		cell.value = newValue
		return true
	}
	return false
}

func (cell *Cell) SetValue(value interface{}) {
	if cell.setValueQuiet(value) {
		cell.device.setValue(cell.name, cell.value)
	}
}

func (cell *Cell) Type() string {
	return cell.controlType
}
