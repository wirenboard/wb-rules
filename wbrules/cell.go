package wbrules

import (
	"errors"
	"fmt"
	wbgo "github.com/contactless/wbgo"
	"log"
	"sort"
	"strconv"
	"sync"
	"time"
)

const (
	CELL_CHANGE_SLICE_CAPACITY   = 4
	CELL_CHANGE_CLOSE_TIMEOUT_MS = 200
	CELL_TYPE_TEXT               = iota
	CELL_TYPE_BOOLEAN
	CELL_TYPE_FLOAT
	CELL_TYPE_BUTTON
)

type CellType int

var cellTypeMap map[string]CellType = map[string]CellType{
	"text":                 CELL_TYPE_TEXT,
	"switch":               CELL_TYPE_BOOLEAN,
	"wo-switch":            CELL_TYPE_BOOLEAN,
	"alarm":                CELL_TYPE_BOOLEAN,
	"pushbutton":           CELL_TYPE_BUTTON,
	"temperature":          CELL_TYPE_FLOAT,
	"rel_humidity":         CELL_TYPE_FLOAT,
	"atmospheric_pressure": CELL_TYPE_FLOAT,
	"rainfall":             CELL_TYPE_FLOAT,
	"wind_speed":           CELL_TYPE_FLOAT,
	"power":                CELL_TYPE_FLOAT,
	"power_consumption":    CELL_TYPE_FLOAT,
	"voltage":              CELL_TYPE_FLOAT,
	"water_flow":           CELL_TYPE_FLOAT,
	"consumption":          CELL_TYPE_FLOAT,
	"pressure":             CELL_TYPE_FLOAT,
	"range":                CELL_TYPE_FLOAT,
}

func cellType(controlType string) CellType {
	cellType, found := cellTypeMap[controlType]
	if found {
		return cellType
	}
	return CELL_TYPE_TEXT
}

type CellSpec struct {
	DevName  string
	CellName string
}

type CellModel struct {
	wbgo.ModelBase
	devices            map[string]CellModelDevice
	cellChangeChannels []chan *CellSpec
	started            bool
	publishDoneCh      chan struct{}
}

type CellModelDevice interface {
	wbgo.DeviceModel
	EnsureCell(name string) (cell *Cell)
	MustGetCell(name string) (cell *Cell)
	setValue(name, value string, notify bool)
	queryParams()
	shouldSetValueImmediately() bool
}

type CellModelDeviceBase struct {
	wbgo.DeviceBase
	model     *CellModel
	cells     map[string]*Cell
	self      CellModelDevice
	onSetCell func(*Cell)
}

type CellModelLocalDevice struct {
	CellModelDeviceBase
}

type CellModelExternalDevice struct {
	CellModelDeviceBase
}

type Cell struct {
	device      CellModelDevice
	name        string
	title       string
	controlType string
	max         float64
	value       string
	gotType     bool
	gotValue    bool
	readonly    bool
}

func NewCellModel() *CellModel {
	return &CellModel{
		devices:            make(map[string]CellModelDevice),
		cellChangeChannels: make([]chan *CellSpec, 0, CELL_CHANGE_SLICE_CAPACITY),
		publishDoneCh:      make(chan struct{}, 10),
	}
}

func (model *CellModel) Start() error {
	// should be called by the driver once and only once when it starts
	if model.started {
		panic("model already started")
	}
	model.started = true
	names := make([]string, 0, len(model.devices))
	for name := range model.devices {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		dev, ok := model.devices[name].(*CellModelLocalDevice)
		if ok {
			model.Observer.OnNewDevice(dev)
		}
	}
	model.Observer.WhenReady(func() {
		for _, name := range names {
			model.devices[name].queryParams()
		}
		// this is needed for tests to be able to publish
		// stuff after all of the local cells are published
		model.publishDoneCh <- struct{}{}
	})
	return nil
}

func (model *CellModel) Stop() {
	// should be called by the driver once and only once when it stops
	if !model.started {
		panic("model already stopped")
	}
	model.started = false
	chs := model.cellChangeChannels
	model.cellChangeChannels = make([]chan *CellSpec, 0, CELL_CHANGE_SLICE_CAPACITY)

	var wg sync.WaitGroup
	wg.Add(len(chs))
	for _, ch := range chs {
		go func(c chan *CellSpec) {
			model.closeCellChangeChannel(c)
			wg.Done()
		}(ch)
	}
	wg.Wait()
}

func (model *CellModel) newCellModelDevice(name string, title string) (dev *CellModelDeviceBase) {
	dev = &CellModelDeviceBase{
		model:     model,
		cells:     make(map[string]*Cell),
		onSetCell: nil,
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
	dev.onSetCell = func(cell *Cell) {
		if model.started {
			cell.value = dev.publishCell(cell)
		}
	}
	model.devices[name] = dev
	return
}

func (model *CellModel) MustGetDevice(name string) (dev CellModelDevice) {
	dev, found := model.devices[name]
	if !found {
		log.Panicf("device not found: %s", name)
	}
	return
}

func (model *CellModel) MustGetCell(cellSpec *CellSpec) *Cell {
	return model.MustGetDevice(cellSpec.DevName).MustGetCell(cellSpec.CellName)
}

func (model *CellModel) EnsureDevice(name string) (dev CellModelDevice) {
	dev, found := model.devices[name]
	if !found {
		dev = model.makeExternalDevice(name, name)
		model.Observer.OnNewDevice(dev)
	}
	return
}

func (model *CellModel) EnsureCell(cellSpec *CellSpec) *Cell {
	return model.EnsureDevice(cellSpec.DevName).EnsureCell(cellSpec.CellName)
}

func (model *CellModel) RemoveLocalDevice(name string) {
	dev, found := model.devices[name]
	if !found {
		return
	}
	delete(model.devices, name)
	model.Observer.RemoveDevice(dev)
}

func (model *CellModel) EnsureLocalDevice(name, title string) *CellModelLocalDevice {
	dev, found := model.devices[name]
	if found {
		if d, ok := dev.(*CellModelLocalDevice); ok {
			return d
		}
		wbgo.Debug.Printf("converting remote device %s to local", name)
	}

	newDevice := model.makeLocalDevice(name, title)
	if model.started {
		wbgo.Debug.Printf("publishing new device %s created while the model is active", name)
		model.Observer.OnNewDevice(newDevice)
	}
	return newDevice
}

func (model *CellModel) AddExternalDevice(name string) (wbgo.ExternalDeviceModel, error) {
	dev, _ := model.EnsureDevice(name).(wbgo.ExternalDeviceModel)
	if dev != nil {
		return dev, nil
	}
	return nil, errors.New("Device %s is registered as a local device")
}

func (model *CellModel) AcquireCellChangeChannel() chan *CellSpec {
	ch := make(chan *CellSpec)
	model.cellChangeChannels = append(model.cellChangeChannels, ch)
	return ch
}

func (model *CellModel) ReleaseCellChangeChannel(ch chan *CellSpec) {
	oldChannels := model.cellChangeChannels
	model.cellChangeChannels = make([]chan *CellSpec, 0, len(model.cellChangeChannels))
	for _, curCh := range oldChannels {
		if ch != curCh {
			model.cellChangeChannels = append(model.cellChangeChannels, curCh)
		}
	}
	model.closeCellChangeChannel(ch)
}

func (model *CellModel) closeCellChangeChannel(ch chan *CellSpec) {
	timer := time.NewTimer(CELL_CHANGE_CLOSE_TIMEOUT_MS * time.Millisecond)
	select {
	case <-timer.C:
		close(ch)
		return
	case _, ok := <-ch:
		if !ok {
			timer.Stop()
			return
		}
	}
}

func (model *CellModel) notify(cellSpec *CellSpec) {
	for _, ch := range model.cellChangeChannels {
		ch <- cellSpec
	}
}

func (model *CellModel) CallSync(thunk func()) {
	// FIXME: need to do it all in a more Go-like way
	model.Observer.CallSync(thunk)
}

func (model *CellModel) WhenReady(thunk func()) {
	model.Observer.WhenReady(thunk)
}

func (dev *CellModelDeviceBase) SetTitle(title string) {
	dev.DevTitle = title
	go dev.model.notify(nil)
}

func (dev *CellModelDeviceBase) setCell(name, controlType string, value interface{}, complete bool, max float64, readonly bool) (cell *Cell) {
	cell = &Cell{
		device:      dev.self,
		name:        name,
		title:       name,
		controlType: controlType,
		max:         max,
		gotType:     complete,
		gotValue:    complete,
		readonly:    readonly,
	}
	cell.maybeSetValueQuiet(value, true)
	dev.cells[name] = cell
	if dev.onSetCell != nil {
		dev.onSetCell(cell)
	}
	return
}

func (dev *CellModelDeviceBase) SetCell(name, controlType string, value interface{}, readonly bool) (cell *Cell) {
	return dev.setCell(name, controlType, value, true, -1, readonly)
}

func (dev *CellModelDeviceBase) SetRangeCell(name string, value interface{}, max float64, readonly bool) (cell *Cell) {
	return dev.setCell(name, "range", value, true, max, readonly)
}

func (dev *CellModelDeviceBase) SetButtonCell(name string) (cell *Cell) {
	return dev.setCell(name, "pushbutton", 0, true, -1, false)
}

func (dev *CellModelDeviceBase) MustGetCell(name string) (cell *Cell) {
	cell, found := dev.cells[name]
	if !found {
		log.Panicf("cell not found: %s/%s", dev.DevName, name)
	}
	return
}

func (dev *CellModelDeviceBase) EnsureCell(name string) (cell *Cell) {
	cell, found := dev.cells[name]
	if !found {
		wbgo.Debug.Printf("adding cell %s", name)
		cell = dev.setCell(name, "text", "", false, -1, false)
	}
	return
}

func (dev *CellModelDeviceBase) AcceptValue(name, value string) {
	if wbgo.DebuggingEnabled() {
		wbgo.Debug.Printf("cell %s <- %v", name, value)
	}

	cell := dev.EnsureCell(name)
	cell.value = value
	cell.gotValue = true
	go dev.model.notify(&CellSpec{dev.DevName, name})
}

func (dev *CellModelDeviceBase) setValue(name, value string, notify bool) {
	if !dev.model.started {
		panic("setValue -- but model not active!!!")
	}
	dev.Observer.OnValue(dev.self, name, value)
	if notify {
		go dev.model.notify(&CellSpec{dev.DevName, name})
	}
}

func (dev *CellModelLocalDevice) AcceptOnValue(name, value string) bool {
	if wbgo.DebuggingEnabled() {
		wbgo.Debug.Printf("cell %s <- %v [.../on]", name, value)
	}
	cell := dev.EnsureCell(name)
	cell.value = value
	cell.gotValue = true
	go dev.model.notify(&CellSpec{dev.DevName, name})
	return true
}

func (dev *CellModelLocalDevice) queryParams() {
	names := make([]string, 0, len(dev.cells))
	for name := range dev.cells {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		cell := dev.cells[name]
		dev.publishCell(cell)
	}
}

func (dev *CellModelLocalDevice) publishCell(cell *Cell) string {
	control := wbgo.Control{
		Name:   cell.name,
		Type:   cell.controlType,
		Value:  cell.value,
		HasMax: cell.max > 0,
		Max:    cell.max,
	}

	if cell.readonly {
		control.Writability = wbgo.ForceReadOnly
	}

	return dev.Observer.OnNewControl(dev, control)
}

func (dev *CellModelLocalDevice) IsVirtual() bool {
	// local cell model devices are virtual, i.e. they're not directly
	// to any hardware and they must pick up their previously defined
	// values when activated
	return true
}

func (dev *CellModelLocalDevice) shouldSetValueImmediately() bool {
	// for local devices, setting cell value must
	// change it's value immediately
	return true
}

func (dev *CellModelExternalDevice) AcceptControlType(name, controlType string) {
	cell := dev.EnsureCell(name)
	cell.gotType = true
	cell.controlType = controlType
	go dev.model.notify(&CellSpec{dev.DevName, name})
}

func (dev *CellModelExternalDevice) AcceptControlRange(name string, max float64) {
	cell := dev.EnsureCell(name)
	cell.max = max
	go dev.model.notify(&CellSpec{dev.DevName, name})
}

func (dev *CellModelExternalDevice) queryParams() {
	// NOOP
}

func (dev *CellModelExternalDevice) shouldSetValueImmediately() bool {
	// For external devices, setting cell value must not change
	// it's value immediately. Cell value will change when device
	// response to the '.../on' message is received.
	return false
}

func (cell *Cell) RawValue() string {
	return cell.value
}

func (cell *Cell) Max() float64 {
	return cell.max
}

func (cell *Cell) Value() interface{} {
	if wbgo.DebuggingEnabled() {
		wbgo.Debug.Printf("cell %s internal value = %v", cell.name, cell.value)
	}

	switch cellType(cell.controlType) {
	case CELL_TYPE_TEXT:
		return cell.value
	case CELL_TYPE_BOOLEAN:
		return cell.value == "1"
	case CELL_TYPE_BUTTON:
		return false
	case CELL_TYPE_FLOAT:
		if r, err := strconv.ParseFloat(cell.value, 64); err != nil {
			return float64(0)
		} else {
			return r
		}
	default:
		panic("invalid cell type")
	}
}

func (cell *Cell) maybeSetValueQuiet(value interface{}, actuallySet bool) (bool, string) {
	if cell.IsButton() {
		if actuallySet {
			cell.value = "0"
		}
		return true, "1"
	}

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
		if actuallySet {
			cell.value = newValue
		}
		return true, newValue
	}
	return false, cell.value
}

func (cell *Cell) SetValue(value interface{}) {
	cell.gotValue = true
	setImmediately := cell.device.shouldSetValueImmediately()
	_, newValue := cell.maybeSetValueQuiet(value, setImmediately)
	cell.device.setValue(cell.name, newValue, setImmediately)
}

func (cell *Cell) Type() string {
	return cell.controlType
}

func (cell *Cell) IsComplete() bool {
	return cell.gotType && (cell.gotValue || cell.IsButton())
}

func (cell *Cell) DevName() string {
	return cell.device.Name()
}

func (cell *Cell) Name() string {
	return cell.name
}

func (cell *Cell) IsButton() bool {
	return cellType(cell.controlType) == CELL_TYPE_BUTTON
}

// IsFreshButton returns true if this cell corresponds
// to a button that didn't receive value yet.
func (cell *Cell) IsFreshButton() bool {
	return cell.IsButton() && !cell.gotValue
}
