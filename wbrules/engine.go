package wbrules

import (
	"fmt"
	wbgo "github.com/contactless/wbgo"
	"github.com/robfig/cron"
	"github.com/stretchr/objx"
	"sort"
	"sync"
	"time"
)

const (
	NO_TIMER_NAME       = ""
	DEFAULT_CELL_MAX    = 255.0
	TIMERS_CAPACITY     = 128
	RULES_CAPACITY      = 256
	CELL_RULES_CAPACITY = 8
	NO_CALLBACK         = ESCallback(0)
)

type TimerFunc func(id int, d time.Duration, periodic bool) wbgo.Timer

func newTimer(id int, d time.Duration, periodic bool) wbgo.Timer {
	if periodic {
		return wbgo.NewRealTicker(d)
	} else {
		return wbgo.NewRealTimer(d)
	}
}

type TimerEntry struct {
	sync.Mutex
	timer    wbgo.Timer
	periodic bool
	quit     chan struct{}
	name     string
	thunk    func()
	active   bool
}

func (entry *TimerEntry) stop() {
	entry.Lock()
	defer entry.Unlock()
	if entry.quit != nil {
		close(entry.quit)
	}
	entry.active = false
}

type LogFunc func(string)

type proxyOwner interface {
	getRev() uint64
	cellModel() *CellModel
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
	return &DeviceProxy{owner, name, owner.cellModel().EnsureDevice(name), owner.getRev()}
}

func (devProxy *DeviceProxy) getDev() (CellModelDevice, bool) {
	if devProxy.rev != devProxy.owner.getRev() {
		devProxy.dev = devProxy.owner.cellModel().EnsureDevice(devProxy.name)
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

type RuleEngine struct {
	*ScopedCleanup
	rev               uint64
	model             *CellModel
	mqttClient        wbgo.MQTTClient
	logFunc           LogFunc
	cellChange        chan *CellSpec
	timerFunc         TimerFunc
	timers            []*TimerEntry
	callbackIndex     ESCallback
	ruleMap           map[string]*Rule
	ruleList          []string
	notedCells        map[*Cell]bool
	notedTimers       map[string]bool
	cellToRuleMap     map[*Cell][]*Rule
	rulesWithoutCells map[*Rule]bool
	timerRules        map[string][]*Rule
	currentTimer      string
	cronMaker         func() Cron
	cron              Cron
	statusMtx         sync.Mutex
}

func NewRuleEngine(model *CellModel, mqttClient wbgo.MQTTClient) *RuleEngine {
	return &RuleEngine{
		ScopedCleanup: MakeScopedCleanup(),
		rev:           0,
		model:         model,
		mqttClient:    mqttClient,
		logFunc: func(message string) {
			wbgo.Info.Printf("RULE: %s\n", message)
		},
		timerFunc:         newTimer,
		timers:            make([]*TimerEntry, 0, TIMERS_CAPACITY),
		callbackIndex:     1,
		ruleMap:           make(map[string]*Rule),
		ruleList:          make([]string, 0, RULES_CAPACITY),
		notedCells:        nil,
		notedTimers:       nil,
		cellToRuleMap:     make(map[*Cell][]*Rule),
		rulesWithoutCells: make(map[*Rule]bool),
		timerRules:        make(map[string][]*Rule),
		currentTimer:      NO_TIMER_NAME,
		cronMaker:         func() Cron { return cron.New() },
		cron:              nil,
	}
}

func (engine *RuleEngine) SetTimerFunc(timerFunc TimerFunc) {
	engine.timerFunc = timerFunc
}

func (engine *RuleEngine) SetCronMaker(cronMaker func() Cron) {
	engine.cronMaker = cronMaker
}

func (engine *RuleEngine) SetLogFunc(logFunc LogFunc) {
	engine.logFunc = logFunc
}

func (engine *RuleEngine) startTrackingDeps() {
	engine.notedCells = make(map[*Cell]bool)
	engine.notedTimers = make(map[string]bool)
}

func (engine *RuleEngine) storeRuleCellSpec(rule *Rule, cellSpec CellSpec) {
	dev := engine.model.EnsureDevice(cellSpec.DevName)
	engine.storeRuleCell(rule, dev.EnsureCell(cellSpec.CellName))
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
}

func (engine *RuleEngine) storeRuleTimer(rule *Rule, timerName string) {
	list, found := engine.timerRules[timerName]
	if !found {
		list = make([]*Rule, 0, CELL_RULES_CAPACITY)
	}
	engine.timerRules[timerName] = append(list, rule)
}

func (engine *RuleEngine) storeRuleDeps(rule *Rule) {
	if len(engine.notedCells) > 0 {
		for cell, _ := range engine.notedCells {
			engine.storeRuleCell(rule, cell)
		}
	} else if len(engine.notedTimers) > 0 {
		for timerName, _ := range engine.notedTimers {
			engine.storeRuleTimer(rule, timerName)
		}
	} else {
		wbgo.Debug.Printf("rule %s doesn't use any cells inside condition functions", rule.name)
		engine.rulesWithoutCells[rule] = true
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

func (engine *RuleEngine) checkTimer(timerName string) bool {
	engine.trackTimer(timerName)
	return engine.currentTimer != NO_TIMER_NAME && engine.currentTimer == timerName
}

func (engine *RuleEngine) fireTimer(n int) {
	entry := engine.timers[n-1]
	if entry == nil {
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

func (engine *RuleEngine) removeTimer(n int) {
	engine.timers[n-1] = nil
}

func (engine *RuleEngine) stopTimerByName(name string) {
	for i, entry := range engine.timers {
		if entry != nil && name == entry.name {
			engine.removeTimer(i + 1)
			entry.stop()
			break
		}
	}
}

func (engine *RuleEngine) stopTimerByIndex(n int) {
	if n == 0 || n > len(engine.timers) {
		return
	}
	if entry := engine.timers[n-1]; entry != nil {
		engine.removeTimer(n)
		entry.stop()
	} else {
		wbgo.Error.Printf("trying to stop unknown timer: %d", n)
	}
}

func (engine *RuleEngine) getCell(devName, cellName string) *Cell {
	return engine.model.EnsureDevice(devName).EnsureCell(cellName)
}

func (engine *RuleEngine) RunRules(cellSpec *CellSpec, timerName string) {
	var cell *Cell
	if cellSpec != nil {
		cell = engine.getCell(cellSpec.DevName, cellSpec.CellName)
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
		for rule, _ := range engine.rulesWithoutCells {
			rule.ShouldCheck()
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

	engine.cron = engine.cronMaker()
	// note for rule reloading: will need to restart cron
	// to reload rules properly
	for _, name := range engine.ruleList {
		engine.ruleMap[name].MaybeAddToCron(engine.cron)
	}
	engine.cron.Start()
}

func (engine *RuleEngine) Start() {
	if engine.cellChange != nil {
		return
	}
	engine.statusMtx.Lock()
	engine.cellChange = engine.model.AcquireCellChangeChannel()
	engine.statusMtx.Unlock()
	ready := make(chan struct{})
	engine.model.WhenReady(func() {
		engine.RunRules(nil, NO_TIMER_NAME)
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
			case <-engine.cellChange:
			}
		}
		engine.model.CallSync(engine.setupCron)
		for {
			select {
			case cellSpec, ok := <-engine.cellChange:
				if ok {
					if cellSpec != nil {
						wbgo.Debug.Printf(
							"rule engine: running rules after cell change: %s/%s",
							cellSpec.DevName, cellSpec.CellName)
					} else {
						wbgo.Debug.Printf(
							"rule engine: running rules")
					}
					engine.model.CallSync(func() {
						engine.RunRules(cellSpec, NO_TIMER_NAME)
					})
				} else {
					wbgo.Debug.Printf("engine stopped")
					for _, entry := range engine.timers {
						if entry != nil {
							entry.stop()
						}
					}
					engine.timers = engine.timers[:0]
					engine.model.ReleaseCellChangeChannel(engine.cellChange)
					engine.statusMtx.Lock()
					engine.cellChange = nil
					engine.statusMtx.Unlock()
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

func (engine *RuleEngine) startTimer(name string, callback func(), interval time.Duration, periodic bool) int {
	entry := &TimerEntry{
		periodic: periodic,
		quit:     nil,
		name:     name,
		active:   true,
	}

	var n = 0

	for i := 0; i < len(engine.timers); i++ {
		if engine.timers[i] == nil {
			engine.timers[i] = entry
			n = i + 1
			break
		}
	}
	if n == 0 {
		engine.timers = append(engine.timers, entry)
		n = len(engine.timers)
	}

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
		entry.quit = make(chan struct{}, 2)
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
						return
					}
				case <-entry.quit:
					entry.timer.Stop()
					return
				}
			}
		}()
	})
	return n
}

func (engine *RuleEngine) publish(topic, payload string, qos byte, retain bool) {
	engine.mqttClient.Publish(wbgo.MQTTMessage{
		Topic:    topic,
		Payload:  payload,
		QoS:      byte(qos),
		Retained: retain,
	})
}

func (engine *RuleEngine) defineVirtualDevice(name string, obj objx.Map) error {
	title := name
	if obj.Has("title") {
		title = obj.Get("title").Str(name)
	}

	// if the device was for some reason defined in another script,
	// we must remove it
	engine.model.RemoveLocalDevice(name)

	dev := engine.model.EnsureLocalDevice(name, title)
	engine.AddCleanup(func() {
		// runs when the rule file is reloaded
		engine.model.RemoveLocalDevice(name)
	})

	if !obj.Has("cells") {
		return nil
	}

	v := obj.Get("cells")
	if !v.IsMSI() {
		return fmt.Errorf("device %s doesn't have 'cells' property", name)
	}

	// Sorting cells by their names is not important when defining device
	// while the engine is not active because all the cells will be published
	// all at once when the engine starts.
	// On the other hand, when defining the device for the active engine
	// the newly added cells are published immediately and if their order
	// changes (map key order is random) the tests may break.
	m := v.MSI()
	cellNames := make([]string, 0, len(m))
	for cellName, _ := range m {
		cellNames = append(cellNames, cellName)
	}
	sort.Strings(cellNames)

	for _, cellName := range cellNames {
		maybeCellDef := m[cellName]
		cellDef, ok := maybeCellDef.(map[string]interface{})
		if !ok {
			return fmt.Errorf("%s/%s: cell definition is not an object", name, cellName)
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

		cellValue, ok := cellDef["value"]
		if !ok {
			return fmt.Errorf("%s/%s: cell value required for cell type %s",
				name, cellName, cellType)
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

func (engine *RuleEngine) defineRule(rule *Rule) {
	if oldRule, found := engine.ruleMap[rule.name]; found {
		oldRule.Destroy()
	} else {
		engine.ruleList = append(engine.ruleList, rule.name)
	}
	engine.ruleMap[rule.name] = rule
	engine.AddCleanup(func() {
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

// refresh() should be called after engine rules are altered
// while the engine is running.
func (engine *RuleEngine) refresh() {
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

func (engine *ESEngine) cellModel() *CellModel {
	return engine.model
}

func (engine *ESEngine) getRev() uint64 {
	return engine.rev
}

func (engine *ESEngine) getDeviceProxy(name string) *DeviceProxy {
	return makeDeviceProxy(engine, name)
}
