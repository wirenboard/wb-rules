package wbrules

import (
	"fmt"
	"github.com/GeertJohan/go.rice"
	wbgo "github.com/contactless/wbgo"
	"github.com/robfig/cron"
	"github.com/stretchr/objx"
	"sync"
	"time"
)

const (
	NO_TIMER_NAME       = ""
	LIB_FILE            = "lib.js"
	DEFAULT_CELL_MAX    = 255.0
	TIMERS_CAPACITY     = 128
	RULES_CAPACITY      = 256
	CELL_RULES_CAPACITY = 8
	NO_CALLBACK         = ESCallback(0)
)

type depTracker interface {
	storeRuleCell(rule *Rule, cell *Cell)
	startTrackingDeps()
	storeRuleDeps(rule *Rule)
}

type Timer interface {
	GetChannel() <-chan time.Time
	Stop()
}
type TimerFunc func(id int, d time.Duration, periodic bool) Timer

type Cron interface {
	AddFunc(spec string, cmd func()) error
	Start()
	Stop()
}

type RealTicker struct {
	innerTicker *time.Ticker
}

func (ticker *RealTicker) GetChannel() <-chan time.Time {
	if ticker.innerTicker == nil {
		panic("trying to get channel from a stopped ticker")
	}
	return ticker.innerTicker.C
}

func (ticker *RealTicker) Stop() {
	if ticker.innerTicker != nil {
		ticker.innerTicker.Stop()
		ticker.innerTicker = nil
	}
}

type RealTimer struct {
	innerTimer *time.Timer
}

func (timer *RealTimer) GetChannel() <-chan time.Time {
	if timer.innerTimer == nil {
		panic("trying to get channel from a stopped timer")
	}
	return timer.innerTimer.C
}

func (timer *RealTimer) Stop() {
	if timer.innerTimer != nil {
		timer.innerTimer.Stop()
		timer.innerTimer = nil
	}
}

func newTimer(id int, d time.Duration, periodic bool) Timer {
	if periodic {
		return &RealTicker{time.NewTicker(d)}
	} else {
		return &RealTimer{time.NewTimer(d)}
	}
}

type TimerEntry struct {
	timer    Timer
	periodic bool
	quit     chan struct{}
	name     string
	thunk    func()
}

type RuleCondition interface {
	// Check checks whether the rule should be run
	// and returns a boolean value indicating whether
	// it should be run and an optional value
	// to be passed as newValue to the rule. In
	// case nil is returned as the optional value,
	// the value of cell must be used.
	Check(cell *Cell) (bool, interface{})
	GetCells() []*Cell
	MaybeAddToCron(cron Cron, thunk func()) error
}

type RuleConditionBase struct{}

func (ruleCond *RuleConditionBase) Check(Cell *Cell) (bool, interface{}) {
	return false, nil
}

func (ruleCond *RuleConditionBase) GetCells() []*Cell {
	return []*Cell{}
}

func (ruleCond *RuleConditionBase) MaybeAddToCron(cron Cron, thunk func()) error {
	return nil
}

type SimpleCallbackCondition struct {
	RuleConditionBase
	cond func() bool
}

type LevelTriggeredRuleCondition struct {
	SimpleCallbackCondition
}

func newLevelTriggeredRuleCondition(cond func() bool) *LevelTriggeredRuleCondition {
	return &LevelTriggeredRuleCondition{
		SimpleCallbackCondition: SimpleCallbackCondition{cond: cond},
	}
}

func (ruleCond *LevelTriggeredRuleCondition) Check(cell *Cell) (bool, interface{}) {
	return ruleCond.cond(), nil
}

type DestroyedRuleCondition struct {
	RuleConditionBase
}

func newDestroyedRuleCondition() *DestroyedRuleCondition {
	return &DestroyedRuleCondition{}
}

func (ruleCond *DestroyedRuleCondition) Check(cell *Cell) (bool, interface{}) {
	panic("invoking a destroyed rule")
}

type EdgeTriggeredRuleCondition struct {
	SimpleCallbackCondition
	prevCondValue bool
	firstRun      bool
}

func newEdgeTriggeredRuleCondition(cond func() bool) *EdgeTriggeredRuleCondition {
	return &EdgeTriggeredRuleCondition{
		SimpleCallbackCondition: SimpleCallbackCondition{cond: cond},
		prevCondValue:           false,
		firstRun:                false,
	}
}

func (ruleCond *EdgeTriggeredRuleCondition) Check(cell *Cell) (bool, interface{}) {
	current := ruleCond.cond()
	shouldFire := current && (ruleCond.firstRun || current != ruleCond.prevCondValue)
	ruleCond.prevCondValue = current
	ruleCond.firstRun = false
	return shouldFire, nil
}

type CellChangedRuleCondition struct {
	RuleConditionBase
	cell     *Cell
	oldValue interface{}
}

func newCellChangedRuleCondition(cell *Cell) (*CellChangedRuleCondition, error) {
	return &CellChangedRuleCondition{
		cell:     cell,
		oldValue: nil,
	}, nil
}

func (ruleCond *CellChangedRuleCondition) GetCells() []*Cell {
	return []*Cell{ruleCond.cell}
}

func (ruleCond *CellChangedRuleCondition) Check(cell *Cell) (bool, interface{}) {
	if cell == nil || cell != ruleCond.cell {
		return false, nil
	}

	if !cell.IsComplete() {
		wbgo.Debug.Printf("skipping rule due to incomplete cell in whenChanged: %s/%s",
			cell.DevName(), cell.Name())
		return false, nil
	}

	v := cell.Value()
	if ruleCond.oldValue == v && !cell.IsButton() {
		return false, nil
	}
	ruleCond.oldValue = v
	return true, nil
}

type FuncValueChangedRuleCondition struct {
	RuleConditionBase
	thunk    func() interface{}
	oldValue interface{}
}

func newFuncValueChangedRuleCondition(f func() interface{}) *FuncValueChangedRuleCondition {
	return &FuncValueChangedRuleCondition{
		thunk:    f,
		oldValue: nil,
	}
}

func (ruleCond *FuncValueChangedRuleCondition) Check(cell *Cell) (bool, interface{}) {
	v := ruleCond.thunk()
	if ruleCond.oldValue == v {
		return false, nil
	}
	ruleCond.oldValue = v
	return true, v
}

type OrRuleCondition struct {
	RuleConditionBase
	conds []RuleCondition
}

func newOrRuleCondition(conds []RuleCondition) *OrRuleCondition {
	return &OrRuleCondition{conds: conds}
}

func (ruleCond *OrRuleCondition) GetCells() []*Cell {
	r := make([]*Cell, 0, 10)
	for _, cond := range ruleCond.conds {
		r = append(r, cond.GetCells()...)
	}
	return r
}

func (ruleCond *OrRuleCondition) Check(cell *Cell) (bool, interface{}) {
	for _, cond := range ruleCond.conds {
		if shouldFire, newValue := cond.Check(cell); shouldFire {
			return true, newValue
		}
	}
	return false, nil
}

type CronRuleCondition struct {
	RuleConditionBase
	spec string
}

func newCronRuleCondition(spec string) *CronRuleCondition {
	return &CronRuleCondition{spec: spec}
}

func (ruleCond *CronRuleCondition) MaybeAddToCron(cron Cron, thunk func()) error {
	return cron.AddFunc(ruleCond.spec, thunk)
}

type Rule struct {
	tracker     depTracker
	name        string
	cond        RuleCondition
	then        ESCallbackFunc
	shouldCheck bool
}

func NewRule(tracker depTracker, name string, cond RuleCondition, then ESCallbackFunc) *Rule {
	rule := &Rule{
		tracker:     tracker,
		name:        name,
		cond:        cond,
		then:        then,
		shouldCheck: false,
	}
	for _, cell := range rule.cond.GetCells() {
		tracker.storeRuleCell(rule, cell)
	}
	return rule
}

func (rule *Rule) ShouldCheck() {
	rule.shouldCheck = true
}

func (rule *Rule) Check(cell *Cell) {
	if cell != nil && !rule.shouldCheck {
		// Don't invoke js if no cells mentioned in the
		// condition callback changed. If rules are run
		// not due to a cell being changed, still need
		// to call JS though.
		return
	}
	rule.tracker.startTrackingDeps()
	shouldFire, newValue := rule.cond.Check(cell)
	var args objx.Map
	rule.tracker.storeRuleDeps(rule)
	rule.shouldCheck = false

	switch {
	case !shouldFire:
		return
	case newValue != nil:
		args = objx.New(map[string]interface{}{
			"newValue": newValue,
		})
	case cell != nil:
		args = objx.New(map[string]interface{}{
			"device":   cell.DevName(),
			"cell":     cell.Name(),
			"newValue": cell.Value(),
		})
	}
	rule.then(args)
}

func (rule *Rule) MaybeAddToCron(cron Cron) {
	err := rule.cond.MaybeAddToCron(cron, func() {
		rule.then(nil)
	})
	if err != nil {
		wbgo.Error.Printf("rule %s: invalid cron spec: %s", rule.name, err)
	}
}

func (rule *Rule) Destroy() {
	rule.then = nil
	rule.cond = newDestroyedRuleCondition()
}

type LogFunc func(string)

type RuleEngine struct {
	model             *CellModel
	mqttClient        wbgo.MQTTClient
	logFunc           LogFunc
	cellChange        chan *CellSpec
	scriptBox         *rice.Box
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
		model:      model,
		mqttClient: mqttClient,
		logFunc: func(message string) {
			wbgo.Info.Printf("RULE: %s\n", message)
		},
		scriptBox:         rice.MustFindBox("scripts"),
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
	// note that n may not be present in ruleEngineTimers, but
	// it shouldn't cause any problems as deleting nonexistent
	// property is not an error
	engine.timers[n-1] = nil
}

func (engine *RuleEngine) stopTimerByName(name string) {
	for i, entry := range engine.timers {
		if entry != nil && name == entry.name {
			engine.removeTimer(i + 1)
			close(entry.quit)
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
		close(entry.quit)
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
							close(entry.quit)
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
		quit:     make(chan struct{}, 2),
		name:     name,
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

	entry.timer = engine.timerFunc(n, interval, periodic)
	tickCh := entry.timer.GetChannel()
	go func() {
		for {
			select {
			case <-tickCh:
				engine.model.CallSync(func() {
					engine.fireTimer(n)
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
	dev := engine.model.EnsureLocalDevice(name, title)

	if !obj.Has("cells") {
		return nil
	}

	v := obj.Get("cells")
	if !v.IsMSI() {
		return fmt.Errorf("device %s doesn't have 'cells' property", name)
	}

	for cellName, maybeCellDef := range v.MSI() {
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
}
