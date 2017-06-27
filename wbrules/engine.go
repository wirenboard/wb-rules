package wbrules

import (
	"fmt"
	"github.com/boltdb/bolt"
	wbgo "github.com/contactless/wbgo"
	"github.com/robfig/cron"
	"github.com/stretchr/objx"
	"log"
	"os"
	"sort"
	"strings"
	"sync"
	"time"
)

type EngineLogLevel int

const (
	NO_TIMER_NAME                 = ""
	DEFAULT_CELL_MAX              = 255.0
	RULES_CAPACITY                = 256
	CELL_RULES_CAPACITY           = 8
	NO_CALLBACK                   = ESCallback(0)
	RULE_ENGINE_SETTINGS_DEV_NAME = "wbrules"
	RULE_DEBUG_CELL_NAME          = "Rule debugging"

	VIRTUAL_CELLS_DB_CHMOD = 0640

	ENGINE_LOG_DEBUG = EngineLogLevel(iota)
	ENGINE_LOG_INFO
	ENGINE_LOG_WARNING
	ENGINE_LOG_ERROR
)

type TimerFunc func(id uint64, d time.Duration, periodic bool) wbgo.Timer

func newTimer(id uint64, d time.Duration, periodic bool) wbgo.Timer {
	if periodic {
		return wbgo.NewRealTicker(d)
	} else {
		return wbgo.NewRealTimer(d)
	}
}

type TimerEntry struct {
	sync.Mutex
	timer         wbgo.Timer
	periodic      bool
	quit, quitted chan struct{}
	name          string
	thunk         func()
	active        bool
}

func (entry *TimerEntry) stop() {
	entry.Lock()
	defer entry.Unlock()
	if entry.quit != nil {
		close(entry.quit)
		// make sure the timer is really stopped before continuing
		<-entry.quitted
	}
	entry.active = false
}

type proxyOwner interface {
	CellModel() *CellModel
	getRev() uint64
	trackCell(*Cell)
}

type DeviceProxy struct {
	owner proxyOwner
	name  string
	dev   CellModelDevice
	rev   uint64
}

// CellProxy tracks cell access with the engine
// and makes sure that always the actual current device
// cell object is accessed while avoiding excess
// name lookups.
type CellProxy struct {
	devProxy *DeviceProxy
	name     string
	cell     *Cell
}

func makeDeviceProxy(owner proxyOwner, name string) *DeviceProxy {
	return &DeviceProxy{owner, name, owner.CellModel().EnsureDevice(name), owner.getRev()}
}

func (devProxy *DeviceProxy) getDev() (CellModelDevice, bool) {
	if devProxy.rev != devProxy.owner.getRev() {
		devProxy.dev = devProxy.owner.CellModel().EnsureDevice(devProxy.name)
		return devProxy.dev, true
	}
	return devProxy.dev, false
}

func (devProxy *DeviceProxy) EnsureCell(name string) *CellProxy {
	dev, _ := devProxy.getDev()
	return &CellProxy{devProxy, name, dev.EnsureCell(name)}
}

func (cellProxy *CellProxy) getCell() *Cell {
	if dev, updated := cellProxy.devProxy.getDev(); updated {
		cellProxy.cell = dev.EnsureCell(cellProxy.name)
	}
	cellProxy.devProxy.owner.trackCell(cellProxy.cell)
	return cellProxy.cell
}

func (cellProxy *CellProxy) RawValue() string {
	return cellProxy.getCell().RawValue()
}

func (cellProxy *CellProxy) Value() interface{} {
	return cellProxy.getCell().Value()
}

func (cellProxy *CellProxy) SetValue(value interface{}) {
	cellProxy.getCell().SetValue(value)
}

func (cellProxy *CellProxy) IsComplete() bool {
	return cellProxy.getCell().IsComplete()
}

// cronProxy helps to avoid race conditions when
// invoking cron funcs
type cronProxy struct {
	Cron
	exec func(func())
}

func newCronProxy(cron Cron, exec func(func())) *cronProxy {
	return &cronProxy{cron, exec}
}

func (cp cronProxy) AddFunc(spec string, cmd func()) error {
	return cp.Cron.AddFunc(spec, func() {
		cp.exec(cmd)
	})
}

type RuleEngineOptions struct {
	VirtualCellsStorageFile     string
	VirtualCellsStorageFileMode os.FileMode
}

func NewRuleEngineOptions() *RuleEngineOptions {
	return &RuleEngineOptions{
		VirtualCellsStorageFileMode: VIRTUAL_CELLS_DB_CHMOD,
	}
}

func (o *RuleEngineOptions) SetVirtualCellsStorageFileMode(mode os.FileMode) {
	o.VirtualCellsStorageFileMode = mode
}

func (o *RuleEngineOptions) SetVirtualCellsStorageFile(file string) {
	o.VirtualCellsStorageFile = file
}

type RuleEngine struct {
	cleanup             *ScopedCleanup
	rev                 uint64
	model               *CellModel
	mqttClient          wbgo.MQTTClient
	cellChange          chan *CellSpec
	timerFunc           TimerFunc
	nextTimerId         uint64
	timers              map[uint64]*TimerEntry
	callbackIndex       ESCallback
	ruleMap             map[string]*Rule
	ruleList            []string
	notedCells          map[*Cell]bool
	notedTimers         map[string]bool
	cellToRuleMap       map[*Cell][]*Rule
	rulesWithoutCells   map[*Rule]bool
	timerRules          map[string][]*Rule
	currentTimer        string
	cronMaker           func() Cron
	cron                Cron
	statusMtx           sync.Mutex
	debugMtx            sync.Mutex
	debugEnabled        bool
	readyCh             chan struct{}
	virtualCellsStorage *bolt.DB
}

func NewRuleEngine(model *CellModel, mqttClient wbgo.MQTTClient, options *RuleEngineOptions) (engine *RuleEngine) {
	if options == nil {
		panic("no options given to NewRuleEngine")
	}

	engine = &RuleEngine{
		cleanup:             MakeScopedCleanup(),
		rev:                 0,
		model:               model,
		mqttClient:          mqttClient,
		timerFunc:           newTimer,
		nextTimerId:         1,
		timers:              make(map[uint64]*TimerEntry),
		callbackIndex:       1,
		ruleMap:             make(map[string]*Rule),
		ruleList:            make([]string, 0, RULES_CAPACITY),
		notedCells:          nil,
		notedTimers:         nil,
		cellToRuleMap:       make(map[*Cell][]*Rule),
		rulesWithoutCells:   make(map[*Rule]bool),
		timerRules:          make(map[string][]*Rule),
		currentTimer:        NO_TIMER_NAME,
		cronMaker:           func() Cron { return cron.New() },
		cron:                nil,
		debugEnabled:        wbgo.DebuggingEnabled(),
		readyCh:             nil,
		virtualCellsStorage: nil,
	}

	if options.VirtualCellsStorageFile != "" {
		if err := engine.SetVirtualCellsDBMode(options.VirtualCellsStorageFile,
			options.VirtualCellsStorageFileMode); err != nil {
			panic("error opening virtual cells storage: " + err.Error())
		}
	}

	engine.setupRuleEngineSettingsDevice()
	return
}

func (engine *RuleEngine) ReadyCh() <-chan struct{} {
	if engine.readyCh == nil {
		panic("cannot engine's readyCh before the engine is started")
	}
	return engine.readyCh
}

// Create or open virtual cells DB file
func (engine *RuleEngine) SetVirtualCellsDB(filename string) (err error) {
	return engine.SetVirtualCellsDBMode(filename, VIRTUAL_CELLS_DB_CHMOD)
}

func (engine *RuleEngine) SetVirtualCellsDBMode(filename string, mode os.FileMode) (err error) {
	if engine.virtualCellsStorage != nil {
		engine.Log(ENGINE_LOG_ERROR, fmt.Sprintf("virtual cells DB is already opened"))
		err = fmt.Errorf("virtual cells DB is aleready opened")
		return
	}

	engine.virtualCellsStorage, err = bolt.Open(filename, mode,
		&bolt.Options{Timeout: 1 * time.Second})

	if err != nil {
		engine.Log(ENGINE_LOG_ERROR, fmt.Sprintf("can't open virtual cells DB file: %s", err))
	}

	return
}

// Force close virtual cells DB
func (engine *RuleEngine) CloseVirtualCellsDB() (err error) {
	if engine.virtualCellsStorage == nil {
		engine.Log(ENGINE_LOG_ERROR, fmt.Sprintf("virtual cells DB is not opened, nothing to close"))
		err = fmt.Errorf("nothing to close")
		return
	}

	err = engine.virtualCellsStorage.Close()

	return
}

// "Cell not found" error
type CellNotFoundError struct{}

func (e *CellNotFoundError) Error() string {
	return ""
}

// Get cell value by name from virtual cells DB
func (engine *RuleEngine) getVirtualCellValueFromDB(device string, control string) (value string, err error) {
	var ok bool

	if engine.virtualCellsStorage == nil {
		err = fmt.Errorf("DB is not initialized")
		return
	}

	err = engine.virtualCellsStorage.View(func(tx *bolt.Tx) error {
		b := tx.Bucket([]byte(device))
		if b == nil { // no bucket for this device
			ok = false
			return nil
		}
		ok = true

		rval := b.Get([]byte(control))
		if rval == nil {
			ok = false
			return nil
		}

		value = string(rval)
		return nil
	})

	if !ok {
		err = &CellNotFoundError{}
	}

	return
}

// Set cell value in virtual cells DB
func (engine *RuleEngine) storeVirtualCellValueToDBRaw(device string, control string, value string) (err error) {
	ok := true

	if engine.virtualCellsStorage == nil {
		err = fmt.Errorf("DB is not initialized")
		return
	}

	err = engine.virtualCellsStorage.Update(func(tx *bolt.Tx) error {
		b, err := tx.CreateBucketIfNotExists([]byte(device))
		if err != nil {
			ok = false
			return err
		}

		if err := b.Put([]byte(control), []byte(value)); err != nil {
			ok = false
			return err
		}

		return nil
	})

	if !ok {
		err = fmt.Errorf("error writing cell value to DB: %s", err)
		return
	}

	wbgo.Debug.Printf("%s/%s: store virtual cell value to DB: \"%s\"",
		device, control, value)

	return
}

func (engine *RuleEngine) storeVirtualCellValueToDB(cellSpec *CellSpec) (err error) {
	if cellSpec == nil {
		return fmt.Errorf("cellSpec is nil")
	}

	cell := engine.model.EnsureCell(cellSpec)

	// check that this cell belongs to virtual device
	// FIXME: this is awful
	if _, ok := cell.device.(*CellModelLocalDevice); !ok {
		return nil
	}

	return engine.storeVirtualCellValueToDBRaw(cellSpec.DevName, cellSpec.CellName, cell.value)
}

func (engine *RuleEngine) setupRuleEngineSettingsDevice() {
	err := engine.DefineVirtualDevice(RULE_ENGINE_SETTINGS_DEV_NAME, objx.Map{
		"title": "Rule Engine Settings",
		"cells": objx.Map{
			RULE_DEBUG_CELL_NAME: objx.Map{
				"type":  "switch",
				"value": false,
			},
		},
	})
	if err != nil {
		log.Panicf("cannot define wbrules device: %s", err)
	}
}

func (engine *RuleEngine) SetTimerFunc(timerFunc TimerFunc) {
	engine.timerFunc = timerFunc
}

func (engine *RuleEngine) SetCronMaker(cronMaker func() Cron) {
	engine.cronMaker = cronMaker
}

func (engine *RuleEngine) StartTrackingDeps() {
	engine.notedCells = make(map[*Cell]bool)
	engine.notedTimers = make(map[string]bool)
}

func (engine *RuleEngine) StoreRuleCellSpec(rule *Rule, cellSpec *CellSpec) {
	engine.storeRuleCell(rule, engine.model.EnsureCell(cellSpec))
}

func (engine *RuleEngine) storeRuleCell(rule *Rule, cell *Cell) {
	list, found := engine.cellToRuleMap[cell]
	if !found {
		list = make([]*Rule, 0, CELL_RULES_CAPACITY)
	} else {
		for _, item := range list {
			if item == rule {
				return
			}
		}
	}
	wbgo.Debug.Printf("adding cell %s for rule %s", cell.Name(), rule.name)
	engine.cellToRuleMap[cell] = append(list, rule)
	engine.rulesWithoutCells[rule] = false
}

func (engine *RuleEngine) storeRuleTimer(rule *Rule, timerName string) {
	list, found := engine.timerRules[timerName]
	if !found {
		list = make([]*Rule, 0, CELL_RULES_CAPACITY)
	}
	engine.timerRules[timerName] = append(list, rule)
}

func (engine *RuleEngine) StoreRuleDeps(rule *Rule) {
	if len(engine.notedCells) > 0 {
		for cell, _ := range engine.notedCells {
			engine.storeRuleCell(rule, cell)
		}
	} else if len(engine.notedTimers) > 0 {
		for timerName, _ := range engine.notedTimers {
			engine.storeRuleTimer(rule, timerName)
		}
	} else if !rule.IsNonCellRule() {
		if _, found := engine.rulesWithoutCells[rule]; !found {
			// Rules without cells in their conditions negatively affect
			// the engine performance because they must be checked
			// too often. Only mark a rule as such if it doesn't have
			// any cells associated with it and it isn't a non-cell rule
			// (such as a cron rule)
			if wbgo.DebuggingEnabled() {
				// Here we use Warn output but only in case if debugging is enabled.
				// This improves testability (due to EnsureNoErrorsOrWarnings()) but
				// avoids polluting logs with endless warnings when debugging is off.
				wbgo.Warn.Printf("rule %s doesn't use any cells inside condition functions", rule.name)
			}
			engine.rulesWithoutCells[rule] = true
		}
	}
	engine.notedCells = nil
	engine.notedTimers = nil
}

func (engine *RuleEngine) trackCell(cell *Cell) {
	if engine.notedCells != nil {
		engine.notedCells[cell] = true
	}
}

func (engine *RuleEngine) trackTimer(timerName string) {
	if engine.notedTimers != nil {
		engine.notedTimers[timerName] = true
	}
}

func (engine *RuleEngine) CheckTimer(timerName string) bool {
	engine.trackTimer(timerName)
	return engine.currentTimer != NO_TIMER_NAME && engine.currentTimer == timerName
}

func (engine *RuleEngine) fireTimer(n uint64) {
	entry, found := engine.timers[n]
	if !found {
		wbgo.Error.Printf("firing unknown timer %d", n)
		return
	}
	if entry.name == NO_TIMER_NAME {
		entry.thunk()
	} else {
		engine.RunRules(nil, entry.name)
	}

	if !entry.periodic {
		engine.removeTimer(n)
	}
}

func (engine *RuleEngine) removeTimer(n uint64) {
	delete(engine.timers, n)
}

func (engine *RuleEngine) StopTimerByName(name string) {
	for n, entry := range engine.timers {
		if entry != nil && name == entry.name {
			engine.removeTimer(n)
			entry.stop()
			break
		}
	}
}

func (engine *RuleEngine) StopTimerByIndex(n uint64) {
	if n == 0 {
		return
	}
	if entry, found := engine.timers[n]; found {
		engine.removeTimer(n)
		entry.stop()
	} else {
		wbgo.Error.Printf("trying to stop unknown timer: %d", n)
	}
}

func (engine *RuleEngine) RunRules(cellSpec *CellSpec, timerName string) {
	var cell *Cell
	if cellSpec != nil {
		cell = engine.model.EnsureCell(cellSpec)
		if cell.IsFreshButton() {
			// special case - a button that wasn't pressed yet
			return
		}
		if cell.IsComplete() {
			// cell-dependent rules aren't run when any of their
			// condition cells are incomplete
			if list, found := engine.cellToRuleMap[cell]; found {
				for _, rule := range list {
					rule.ShouldCheck()
				}
			}
		}
		for rule, isWithoutCells := range engine.rulesWithoutCells {
			if isWithoutCells {
				rule.ShouldCheck()
			}
		}
	}

	if timerName != NO_TIMER_NAME {
		engine.currentTimer = timerName
		if list, found := engine.timerRules[timerName]; found {
			for _, rule := range list {
				rule.ShouldCheck()
			}
		}
	}

	for _, name := range engine.ruleList {
		engine.ruleMap[name].Check(cell)
	}
	engine.currentTimer = NO_TIMER_NAME
}

func (engine *RuleEngine) setupCron() {
	if engine.cron != nil {
		engine.cron.Stop()
	}

	engine.cron = newCronProxy(engine.cronMaker(), engine.model.CallSync)
	// note for rule reloading: will need to restart cron
	// to reload rules properly
	for _, name := range engine.ruleList {
		rule := engine.ruleMap[name]
		rule.MaybeAddToCron(engine.cron)
	}
	engine.cron.Start()
}

func (engine *RuleEngine) handleStop() {
	wbgo.Debug.Printf("engine stopped")
	for _, entry := range engine.timers {
		entry.stop()
	}
	engine.timers = make(map[uint64]*TimerEntry)
	engine.model.ReleaseCellChangeChannel(engine.cellChange)
	engine.statusMtx.Lock()
	engine.cellChange = nil
	engine.readyCh = nil
	engine.statusMtx.Unlock()
}

func (engine *RuleEngine) isDebugCell(cellSpec *CellSpec) bool {
	return cellSpec.DevName == RULE_ENGINE_SETTINGS_DEV_NAME &&
		cellSpec.CellName == RULE_DEBUG_CELL_NAME
}

func (engine *RuleEngine) updateDebugEnabled() {
	engine.model.CallSync(func() {
		debugCell := engine.model.MustGetCell(
			&CellSpec{
				RULE_ENGINE_SETTINGS_DEV_NAME,
				RULE_DEBUG_CELL_NAME,
			})
		engine.debugMtx.Lock()
		engine.debugEnabled = debugCell.Value().(bool)
		engine.debugMtx.Unlock()
	})
}

func (engine *RuleEngine) Start() {
	if engine.cellChange != nil {
		return
	}
	engine.readyCh = make(chan struct{})
	engine.statusMtx.Lock()
	engine.cellChange = engine.model.AcquireCellChangeChannel()
	engine.statusMtx.Unlock()
	ready := make(chan struct{})
	engine.model.WhenReady(func() {
		close(ready)
	})
	go func() {
		// cell changes are ignored until the engine is ready
		// FIXME: some very small probability of race condition is
		// present here
	ReadyWaitLoop:
		for {
			select {
			case <-ready:
				break ReadyWaitLoop
			case cellSpec, ok := <-engine.cellChange:
				if ok {
					wbgo.Debug.Printf("cell change (not ready yet): %s", cellSpec)
					if cellSpec == nil || engine.isDebugCell(cellSpec) {
						engine.updateDebugEnabled()
					}
				} else {
					wbgo.Debug.Printf("stoping the engine (not ready yet)")
					engine.handleStop()
					return
				}
			}
		}
		wbgo.Debug.Printf("setting up cron")
		engine.model.CallSync(engine.setupCron)
		wbgo.Debug.Printf("doing the first rule run")
		engine.model.CallSync(func() {
			engine.RunRules(nil, NO_TIMER_NAME)
		})
		close(engine.readyCh)
		wbgo.Debug.Printf("the engine is ready")
		// wbgo.Info.Printf("******** READY ********")
		for {
			select {
			case cellSpec, ok := <-engine.cellChange:
				if ok {
					if wbgo.DebuggingEnabled() {
						wbgo.Debug.Printf("cell change: %v", cellSpec)
						if cellSpec != nil {
							wbgo.Debug.Printf(
								"rule engine: running rules after cell change: %s/%s",
								cellSpec.DevName, cellSpec.CellName)
						} else {
							wbgo.Debug.Printf(
								"rule engine: running rules")
						}
					}
					if cellSpec == nil || engine.isDebugCell(cellSpec) {
						engine.updateDebugEnabled()
					}

					if cellSpec != nil {
						if err := engine.storeVirtualCellValueToDB(cellSpec); err != nil {
							wbgo.Warn.Printf("%s/%s: can't set virtual cell value: %s",
								cellSpec.DevName, cellSpec.CellName, err)
						}
					}

					engine.model.CallSync(func() {
						engine.RunRules(cellSpec, NO_TIMER_NAME)
					})
				} else {
					engine.handleStop()
					return
				}
			}
		}
	}()
}

func (engine *RuleEngine) IsActive() bool {
	engine.statusMtx.Lock()
	defer engine.statusMtx.Unlock()
	return engine.cellChange != nil
}

func (engine *RuleEngine) StartTimer(name string, callback func(), interval time.Duration, periodic bool) uint64 {
	entry := &TimerEntry{
		periodic: periodic,
		quit:     nil,
		quitted:  nil,
		name:     name,
		active:   true,
	}

	n := engine.nextTimerId
	engine.nextTimerId += 1
	engine.timers[n] = entry

	if name == NO_TIMER_NAME {
		entry.thunk = callback
	} else if callback != nil {
		wbgo.Warn.Printf("warning: ignoring callback func for a named timer")
	}

	engine.model.WhenReady(func() {
		entry.Lock()
		defer entry.Unlock()
		if !entry.active {
			// stopped before the engine is ready
			return
		}
		entry.quit = make(chan struct{}, 2) // FIXME: is 2 necessary here?
		entry.quitted = make(chan struct{})
		entry.timer = engine.timerFunc(n, interval, periodic)
		tickCh := entry.timer.GetChannel()
		go func() {
			for {
				select {
				case <-tickCh:
					engine.model.CallSync(func() {
						entry.Lock()
						wasActive := entry.active
						entry.Unlock()
						if wasActive {
							engine.fireTimer(n)
						}
					})
					if !periodic {
						close(entry.quitted)
						return
					}
				case <-entry.quit:
					entry.timer.Stop()
					close(entry.quitted)
					return
				}
			}
		}()
	})
	return n
}

func (engine *RuleEngine) Publish(topic, payload string, qos byte, retain bool) {
	engine.mqttClient.Start()
	engine.mqttClient.Publish(wbgo.MQTTMessage{
		Topic:    topic,
		Payload:  payload,
		QoS:      byte(qos),
		Retained: retain,
	})
}

func (engine *RuleEngine) DefineVirtualDevice(name string, obj objx.Map) error {

	// check device name (non-zero length, no wildcard symbols)
	if len(name) == 0 {
		return fmt.Errorf("invalid virtual device name: empty string")
	}

	if strings.ContainsAny(name, "#+") {
		return fmt.Errorf("invalid virtual device name '%s': "+
			"device name must not contain wildcard characters ('#' and '+')", name)
	}

	title := name
	if obj.Has("title") {
		title = obj.Get("title").Str(name)
	}

	// if the device was for some reason defined in another script,
	// we must remove it
	engine.model.RemoveLocalDevice(name)

	dev := engine.model.EnsureLocalDevice(name, title)
	engine.cleanup.AddCleanup(func() {
		// runs when the rule file is reloaded
		engine.model.RemoveLocalDevice(name)
	})

	if !obj.Has("cells") {
		return nil
	}

	v := obj.Get("cells")
	var m objx.Map
	switch {
	case v.IsObjxMap():
		m = v.ObjxMap()
	case v.IsMSI():
		m = objx.Map(v.MSI())
	default:
		return fmt.Errorf("device %s doesn't have proper 'cells' property", name)
	}

	// Sorting cells by their names is not important when defining device
	// while the engine is not active because all the cells will be published
	// all at once when the engine starts.
	// On the other hand, when defining the device for the active engine
	// the newly added cells are published immediately and if their order
	// changes (map key order is random) the tests may break.
	cellNames := make([]string, 0, len(m))
	for cellName, _ := range m {
		cellNames = append(cellNames, cellName)
	}
	sort.Strings(cellNames)

	for _, cellName := range cellNames {
		maybeCellDef := m[cellName]
		cellDef, ok := maybeCellDef.(objx.Map)
		if !ok {
			cd, ok := maybeCellDef.(map[string]interface{})
			if !ok {
				return fmt.Errorf("%s/%s: bad cell definition", name, cellName)
			}
			cellDef = objx.Map(cd)
		}
		cellType, ok := cellDef["type"]
		if !ok {
			return fmt.Errorf("%s/%s: no cell type", name, cellName)
		}
		// FIXME: too much spaghetti for my taste
		if cellType == "pushbutton" {
			dev.SetButtonCell(cellName)
			continue
		}

		forceDefault := false
		forceDefaultRaw, hasForceDefault := cellDef["forceDefault"]

		if hasForceDefault {
			ok := false
			forceDefault, ok = forceDefaultRaw.(bool)
			if !ok {
				return fmt.Errorf("%s/%s: non-boolean value of forceDefault propery",
					name, cellName)
			}
		}

		cellValue, ok := cellDef["value"]
		if !ok {
			return fmt.Errorf("%s/%s: cell value required for cell type %s",
				name, cellName, cellType)
		}

		// try to get last stored value of virtual cell
		if !forceDefault {
			if v, err := engine.getVirtualCellValueFromDB(name, cellName); err == nil {
				cellValue = v
				wbgo.Debug.Printf("%s/%s: set previous virtual cell value \"%s\"",
					name, cellName, cellValue)
			} else if _, ok = err.(*CellNotFoundError); ok {
				// cell not found, do nothing
				wbgo.Debug.Printf("%s/%s: previous cell value not found", name, cellName)
			} else {
				wbgo.Warn.Printf("%s/%s: can't get previous virtual cell value: %s",
					name, cellName, err)
			}
		}

		cellReadonly := false
		cellReadonlyRaw, hasReadonly := cellDef["readonly"]

		if hasReadonly {
			cellReadonly, ok = cellReadonlyRaw.(bool)
			if !ok {
				return fmt.Errorf("%s/%s: non-boolean value of readonly property",
					name, cellName)
			}
		}

		if cellType == "range" {
			fmax := DEFAULT_CELL_MAX
			max, ok := cellDef["max"]
			if ok {
				fmax, ok = max.(float64)
				if !ok {
					return fmt.Errorf("%s/%s: non-numeric value of max property",
						name, cellName)
				}
			}
			// FIXME: can be float
			dev.SetRangeCell(cellName, cellValue, fmax, cellReadonly)
		} else {
			dev.SetCell(cellName, cellType.(string), cellValue, cellReadonly)
		}
	}

	return nil
}

func (engine *RuleEngine) DefineRule(rule *Rule) {
	if oldRule, found := engine.ruleMap[rule.name]; found {
		oldRule.Destroy()
	} else {
		engine.ruleList = append(engine.ruleList, rule.name)
	}
	engine.ruleMap[rule.name] = rule
	engine.cleanup.AddCleanup(func() {
		delete(engine.ruleMap, rule.name)
		for i, name := range engine.ruleList {
			if name == rule.name {
				engine.ruleList = append(
					engine.ruleList[0:i],
					engine.ruleList[i+1:]...)
				break
			}
		}
	})
}

// Refresh() should be called after engine rules are altered
// while the engine is running.
func (engine *RuleEngine) Refresh() {
	engine.rev++ // invalidate cell proxies
	engine.setupCron()

	// Some cell pointers are now probably invalid
	engine.cellToRuleMap = make(map[*Cell][]*Rule)
	for _, rule := range engine.ruleMap {
		rule.StoreInitiallyKnownDeps()
	}
	engine.rulesWithoutCells = make(map[*Rule]bool)
	engine.timerRules = make(map[string][]*Rule)
	engine.RunRules(nil, NO_TIMER_NAME)
}

func (engine *RuleEngine) CellModel() *CellModel {
	return engine.model
}

func (engine *RuleEngine) getRev() uint64 {
	return engine.rev
}

func (engine *RuleEngine) GetDeviceProxy(name string) *DeviceProxy {
	return makeDeviceProxy(engine, name)
}

func (engine *RuleEngine) Log(level EngineLogLevel, message string) {
	var topicItem string
	switch level {
	case ENGINE_LOG_DEBUG:
		wbgo.Debug.Printf("[rule debug] %s", message)
		engine.debugMtx.Lock()
		defer engine.debugMtx.Unlock()
		if !engine.debugEnabled {
			return
		}
		topicItem = "debug"
	case ENGINE_LOG_INFO:
		wbgo.Info.Printf("[rule info] %s", message)
		topicItem = "info"
	case ENGINE_LOG_WARNING:
		wbgo.Warn.Printf("[rule warning] %s", message)
		topicItem = "warning"
	case ENGINE_LOG_ERROR:
		wbgo.Error.Printf("[rule error] %s", message)
		topicItem = "error"
	}
	engine.Publish("/wbrules/log/"+topicItem, message, 1, false)
}

func (engine *RuleEngine) Logf(level EngineLogLevel, format string, v ...interface{}) {
	engine.Log(level, fmt.Sprintf(format, v...))
}
