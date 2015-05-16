package wbrules

import (
	"errors"
	"fmt"
	"github.com/GeertJohan/go.rice"
	wbgo "github.com/contactless/wbgo"
	duktape "github.com/ivan4th/go-duktape"
	"github.com/robfig/cron"
	"github.com/stretchr/objx"
	"strings"
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

func newLevelTriggeredRuleCondition(engine *RuleEngine, defIndex int) *LevelTriggeredRuleCondition {
	return &LevelTriggeredRuleCondition{
		SimpleCallbackCondition: SimpleCallbackCondition{
			cond: engine.wrapRuleCondFunc(defIndex, "when"),
		},
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

func newEdgeTriggeredRuleCondition(engine *RuleEngine, defIndex int) *EdgeTriggeredRuleCondition {
	return &EdgeTriggeredRuleCondition{
		SimpleCallbackCondition: SimpleCallbackCondition{
			cond: engine.wrapRuleCondFunc(defIndex, "asSoonAs"),
		},
		prevCondValue: false,
		firstRun:      false,
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
	engine   *RuleEngine
	cell     *Cell
	oldValue interface{}
}

func newCellChangedRuleCondition(engine *RuleEngine, cellNameIndex int) (*CellChangedRuleCondition, error) {
	cellFullName := engine.ctx.SafeToString(cellNameIndex)

	parts := strings.SplitN(cellFullName, "/", 2)
	if len(parts) != 2 {
		return nil, fmt.Errorf("invalid whenChanged spec: '%s'", cellFullName)
	}

	return &CellChangedRuleCondition{
		engine:   engine,
		cell:     engine.getCell(parts[0], parts[1]),
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
	engine   *RuleEngine
	thunk    func() interface{}
	oldValue interface{}
}

func newFuncValueChangedRuleCondition(engine *RuleEngine, funcIndex int) *FuncValueChangedRuleCondition {
	f := engine.ctx.WrapCallback("ruleFuncs", funcIndex)
	return &FuncValueChangedRuleCondition{
		engine:   engine,
		thunk:    func() interface{} { return f(nil) },
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

func newSingleWhenChangedRuleCondition(engine *RuleEngine, defIndex int) (RuleCondition, error) {
	if engine.ctx.IsString(defIndex) {
		return newCellChangedRuleCondition(engine, defIndex)
	}
	if engine.ctx.IsFunction(-1) {
		return newFuncValueChangedRuleCondition(engine, -1), nil
	}
	return nil, errors.New("whenChanged: array expected")
}

func newWhenChangedRuleCondition(engine *RuleEngine, defIndex int) (RuleCondition, error) {
	ctx := engine.ctx
	ctx.GetPropString(defIndex, "whenChanged")
	defer ctx.Pop()

	if !ctx.IsArray(-1) {
		return newSingleWhenChangedRuleCondition(engine, -1)
	}

	conds := make([]RuleCondition, ctx.GetLength(-1))

	for i := range conds {
		ctx.GetPropIndex(-1, uint(i))
		cond, err := newSingleWhenChangedRuleCondition(engine, -1)
		ctx.Pop()
		if err != nil {
			return nil, err
		} else {
			conds[i] = cond
		}
	}

	return newOrRuleCondition(conds), nil
}

type CronRuleCondition struct {
	RuleConditionBase
	spec string
}

func newCronRuleCondition(engine *RuleEngine, defIndex int) *CronRuleCondition {
	engine.ctx.GetPropString(defIndex, "_cron")
	defer engine.ctx.Pop()
	return &CronRuleCondition{spec: engine.ctx.SafeToString(-1)}
}

func (ruleCond *CronRuleCondition) MaybeAddToCron(cron Cron, thunk func()) error {
	return cron.AddFunc(ruleCond.spec, thunk)
}

type Rule struct {
	engine      *RuleEngine
	name        string
	cond        RuleCondition
	then        ESCallbackFunc
	shouldCheck bool
}

func newRule(engine *RuleEngine, name string, defIndex int) (*Rule, error) {
	rule := &Rule{
		engine:      engine,
		name:        name,
		then:        nil,
		shouldCheck: false,
	}
	ctx := engine.ctx

	if !ctx.HasPropString(defIndex, "then") {
		// this should be handled by lib.js
		return nil, errors.New("invalid rule -- no then")
	}
	rule.then = rule.engine.wrapRuleCallback(defIndex, "then")
	hasWhen := ctx.HasPropString(defIndex, "when")
	hasAsSoonAs := ctx.HasPropString(defIndex, "asSoonAs")
	hasWhenChanged := ctx.HasPropString(defIndex, "whenChanged")
	hasCron := ctx.HasPropString(defIndex, "_cron")

	if hasWhen {
		if hasAsSoonAs || hasWhenChanged || hasCron {
			// _cron is added by lib.js. Under normal circumstances
			// it may not be combined with 'when' here, so no special message
			return nil, errors.New(
				"invalid rule -- cannot combine 'when' with 'asSoonAs' or 'whenChanged'")
		}
		rule.cond = newLevelTriggeredRuleCondition(engine, defIndex)
	} else if hasAsSoonAs {
		if hasWhenChanged || hasCron {
			return nil, errors.New(
				"invalid rule -- cannot combine 'asSoonAs' with 'whenChanged'")
		}
		rule.cond = newEdgeTriggeredRuleCondition(engine, defIndex)
	} else if hasWhenChanged {
		if hasCron {
			return nil, errors.New("invalid cron spec")
		}

		var err error
		if rule.cond, err = newWhenChangedRuleCondition(engine, defIndex); err != nil {
			return nil, err
		}
	} else if hasCron {
		rule.cond = newCronRuleCondition(engine, defIndex)
	} else {
		return nil, errors.New(
			"invalid rule -- must provide one of 'when', 'asSoonAs' or 'whenChanged'")
	}

	for _, cell := range rule.cond.GetCells() {
		engine.storeRuleCell(rule, cell)
	}

	return rule, nil
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
	rule.engine.startTrackingDeps()
	shouldFire, newValue := rule.cond.Check(cell)
	var args objx.Map
	rule.engine.storeRuleDeps(rule)
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
	ctx               *ESContext
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

func NewRuleEngine(model *CellModel, mqttClient wbgo.MQTTClient) (engine *RuleEngine) {
	engine = &RuleEngine{
		model:      model,
		mqttClient: mqttClient,
		ctx:        newESContext(),
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

	engine.ctx.InitCallbackList("ruleEngineTimers")
	engine.ctx.InitCallbackList("processes")
	engine.ctx.InitCallbackList("ruleFuncs")

	engine.ctx.PushGlobalObject()
	engine.ctx.DefineFunctions(map[string]func() int{
		"defineVirtualDevice":  engine.esDefineVirtualDevice,
		"format":               engine.esFormat,
		"log":                  engine.esLog,
		"debug":                engine.esDebug,
		"publish":              engine.esPublish,
		"_wbDevObject":         engine.esWbDevObject,
		"_wbCellObject":        engine.esWbCellObject,
		"_wbStartTimer":        engine.esWbStartTimer,
		"_wbStopTimer":         engine.esWbStopTimer,
		"_wbCheckCurrentTimer": engine.esWbCheckCurrentTimer,
		"_wbSpawn":             engine.esWbSpawn,
		"_wbDefineRule":        engine.esWbDefineRule,
		"runRules":             engine.esWbRunRules,
	})
	engine.ctx.Pop()
	if err := engine.loadLib(); err != nil {
		wbgo.Error.Panicf("failed to load runtime library: %s", err)
	}
	return
}

func (engine *RuleEngine) wrapRuleCallback(defIndex int, propName string) ESCallbackFunc {
	engine.ctx.GetPropString(defIndex, propName)
	defer engine.ctx.Pop()
	return engine.ctx.WrapCallback("ruleFuncs", -1)
}

func (engine *RuleEngine) wrapRuleCondFunc(defIndex int, defProp string) func() bool {
	f := engine.wrapRuleCallback(defIndex, defProp)
	return func() bool {
		r, ok := f(nil).(bool)
		return ok && r
	}
}

func (engine *RuleEngine) SetTimerFunc(timerFunc TimerFunc) {
	engine.timerFunc = timerFunc
}

func (engine *RuleEngine) SetCronMaker(cronMaker func() Cron) {
	engine.cronMaker = cronMaker
}

func (engine *RuleEngine) loadLib() error {
	libStr, err := engine.scriptBox.String(LIB_FILE)
	if err != nil {
		return err
	}
	return engine.ctx.LoadEmbeddedScript(LIB_FILE, libStr)
}

func (engine *RuleEngine) SetLogFunc(logFunc LogFunc) {
	engine.logFunc = logFunc
}

func (engine *RuleEngine) esDefineVirtualDevice() int {
	if engine.ctx.GetTop() != 2 || !engine.ctx.IsString(-2) || !engine.ctx.IsObject(-1) {
		return duktape.DUK_RET_ERROR
	}
	name := engine.ctx.GetString(-2)
	title := name
	obj := engine.ctx.GetJSObject(-1).(objx.Map)
	if obj.Has("title") {
		title = obj.Get("title").Str(name)
	}
	dev := engine.model.EnsureLocalDevice(name, title)
	if obj.Has("cells") {
		if v := obj.Get("cells"); !v.IsMSI() {
			return duktape.DUK_RET_ERROR
		} else {
			for cellName, maybeCellDef := range v.MSI() {
				cellDef, ok := maybeCellDef.(map[string]interface{})
				if !ok {
					return duktape.DUK_RET_ERROR
				}
				cellType, ok := cellDef["type"]
				if !ok {
					return duktape.DUK_RET_ERROR
				}
				// FIXME: too much spaghetti for my taste
				if cellType == "pushbutton" {
					dev.SetButtonCell(cellName)
				} else {
					cellValue, ok := cellDef["value"]
					if !ok {
						return duktape.DUK_RET_ERROR
					}

					cellReadonly := false
					cellReadonlyRaw, hasReadonly := cellDef["readonly"]
					if hasReadonly {
						cellReadonly, ok = cellReadonlyRaw.(bool)
						if !ok {
							return duktape.DUK_RET_ERROR
						}
					}

					if cellType == "range" {
						fmax := DEFAULT_CELL_MAX
						max, ok := cellDef["max"]
						if ok {
							fmax, ok = max.(float64)
							if !ok {
								return duktape.DUK_RET_ERROR
							}
						}
						// FIXME: can be float
						dev.SetRangeCell(cellName, cellValue, fmax, cellReadonly)
					} else {
						dev.SetCell(cellName, cellType.(string), cellValue, cellReadonly)
					}
				}
			}
		}
	}
	return 0
}

func (engine *RuleEngine) esFormat() int {
	engine.ctx.PushString(engine.ctx.Format())
	return 1
}

func (engine *RuleEngine) esLog() int {
	engine.logFunc(engine.ctx.Format())
	return 0
}

func (engine *RuleEngine) esDebug() int {
	wbgo.Debug.Printf("[rule debug] %s", engine.ctx.Format())
	return 0
}

func (engine *RuleEngine) esPublish() int {
	retain := false
	qos := 0
	if engine.ctx.GetTop() == 4 {
		retain = engine.ctx.ToBoolean(-1)
		engine.ctx.Pop()
	}
	if engine.ctx.GetTop() == 3 {
		qos = int(engine.ctx.ToNumber(-1))
		engine.ctx.Pop()
		if qos < 0 || qos > 2 {
			return duktape.DUK_RET_ERROR
		}
	}
	if engine.ctx.GetTop() != 2 {
		return duktape.DUK_RET_ERROR
	}
	if !engine.ctx.IsString(-2) {
		return duktape.DUK_RET_TYPE_ERROR
	}
	engine.mqttClient.Publish(wbgo.MQTTMessage{
		Topic:    engine.ctx.GetString(-2),
		Payload:  engine.ctx.SafeToString(-1),
		QoS:      byte(qos),
		Retained: retain,
	})
	return 0
}

func (engine *RuleEngine) esWbDevObject() int {
	wbgo.Debug.Printf("esWbDevObject(): top=%d isString=%v", engine.ctx.GetTop(), engine.ctx.IsString(-1))
	if engine.ctx.GetTop() != 1 || !engine.ctx.IsString(-1) {
		return duktape.DUK_RET_ERROR
	}
	dev := engine.model.EnsureDevice(engine.ctx.GetString(-1))
	engine.ctx.PushGoObject(dev)
	return 1
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

func (engine *RuleEngine) esWbCellObject() int {
	if engine.ctx.GetTop() != 2 || !engine.ctx.IsString(-1) || !engine.ctx.IsObject(-2) {
		return duktape.DUK_RET_ERROR
	}
	dev, ok := engine.ctx.GetGoObject(-2).(CellModelDevice)
	if !ok {
		wbgo.Error.Printf("invalid _wbCellObject call")
		return duktape.DUK_RET_TYPE_ERROR
	}
	cell := dev.EnsureCell(engine.ctx.GetString(-1))
	engine.ctx.PushGoObject(cell)
	engine.ctx.DefineFunctions(map[string]func() int{
		"rawValue": func() int {
			engine.trackCell(cell)
			engine.ctx.PushString(cell.RawValue())
			return 1
		},
		"value": func() int {
			engine.trackCell(cell)
			m := objx.New(map[string]interface{}{
				"v": cell.Value(),
			})
			engine.ctx.PushJSObject(m)
			return 1
		},
		"setValue": func() int {
			engine.trackCell(cell)
			if engine.ctx.GetTop() != 1 || !engine.ctx.IsObject(-1) {
				return duktape.DUK_RET_ERROR
			}
			m, ok := engine.ctx.GetJSObject(-1).(objx.Map)
			if !ok || !m.Has("v") {
				wbgo.Error.Printf("invalid cell definition")
				return duktape.DUK_RET_TYPE_ERROR
			}
			cell.SetValue(m["v"])
			return 1
		},
		"isComplete": func() int {
			engine.trackCell(cell)
			engine.ctx.PushBoolean(cell.IsComplete())
			return 1
		},
	})
	return 1
}

func (engine *RuleEngine) fireTimer(n int) {
	entry := engine.timers[n-1]
	if entry == nil {
		wbgo.Error.Printf("firing unknown timer %d", n)
		return
	}
	if entry.name == NO_TIMER_NAME {
		engine.ctx.InvokeCallback("ruleEngineTimers", n, nil)
	} else {
		engine.RunRules(nil, entry.name)
	}

	if !entry.periodic {
		engine.removeTimer(n)
	}
}

func (engine *RuleEngine) esWbStartTimer() int {
	if engine.ctx.GetTop() != 3 || !engine.ctx.IsNumber(1) {
		// FIXME: need to throw proper exception here
		wbgo.Error.Println("bad _wbStartTimer call")
		return duktape.DUK_RET_ERROR
	}

	name := NO_TIMER_NAME
	if engine.ctx.IsString(0) {
		name = engine.ctx.ToString(0)
		if name == "" {
			wbgo.Error.Println("empty timer name")
			return duktape.DUK_RET_ERROR
		}
		engine.stopTimerByName(name)
	} else if !engine.ctx.IsFunction(0) {
		wbgo.Error.Println("invalid timer spec")
		return duktape.DUK_RET_ERROR
	}

	ms := engine.ctx.GetNumber(1)
	periodic := engine.ctx.ToBoolean(2)

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
		engine.ctx.StoreCallback("ruleEngineTimers", 0, n)
	}

	entry.timer = engine.timerFunc(n, time.Duration(ms*float64(time.Millisecond)), periodic)
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

	engine.ctx.PushNumber(float64(n))
	return 1
}

func (engine *RuleEngine) removeTimer(n int) {
	// note that n may not be present in ruleEngineTimers, but
	// it shouldn't cause any problems as deleting nonexistent
	// property is not an error
	engine.ctx.RemoveCallback("ruleEngineTimers", n)
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

func (engine *RuleEngine) esWbStopTimer() int {
	if engine.ctx.GetTop() != 1 {
		return duktape.DUK_RET_ERROR
	}
	if engine.ctx.IsNumber(0) {
		n := engine.ctx.GetInt(-1)
		if n == 0 {
			wbgo.Error.Printf("timer id cannot be zero")
			return 0
		}
		engine.stopTimerByIndex(n)
	} else if engine.ctx.IsString(0) {
		engine.stopTimerByName(engine.ctx.ToString(0))
	} else {
		return duktape.DUK_RET_ERROR
	}
	return 0
}

func (engine *RuleEngine) esWbCheckCurrentTimer() int {
	if engine.ctx.GetTop() != 1 || !engine.ctx.IsString(0) {
		return duktape.DUK_RET_ERROR
	}
	timerName := engine.ctx.ToString(0)
	engine.trackTimer(timerName)
	if engine.currentTimer == NO_TIMER_NAME || engine.currentTimer != timerName {
		engine.ctx.PushFalse()
	} else {
		engine.ctx.PushTrue()
	}
	return 1
}

func (engine *RuleEngine) esWbSpawn() int {
	if engine.ctx.GetTop() != 5 || !engine.ctx.IsArray(0) || !engine.ctx.IsBoolean(2) ||
		!engine.ctx.IsBoolean(3) {
		return duktape.DUK_RET_ERROR
	}

	args := engine.ctx.StringArrayToGo(0)
	if len(args) == 0 {
		return duktape.DUK_RET_ERROR
	}

	callbackIndex := NO_CALLBACK

	if engine.ctx.IsFunction(1) {
		callbackIndex = engine.ctx.StoreCallback("processes", 1, nil)
	} else if !engine.ctx.IsNullOrUndefined(1) {
		return duktape.DUK_RET_ERROR
	}

	var input *string
	if engine.ctx.IsString(4) {
		instr := engine.ctx.GetString(4)
		input = &instr
	} else if !engine.ctx.IsNullOrUndefined(4) {
		return duktape.DUK_RET_ERROR
	}

	captureOutput := engine.ctx.GetBoolean(2)
	captureErrorOutput := engine.ctx.GetBoolean(3)
	command := engine.ctx.GetString(0)
	go func() {
		r, err := Spawn(args[0], args[1:], captureOutput, captureErrorOutput, input)
		if err != nil {
			wbgo.Error.Printf("external command failed: %s", err)
			return
		}
		if callbackIndex > 0 {
			engine.model.CallSync(func() {
				args := objx.New(map[string]interface{}{
					"exitStatus": r.ExitStatus,
				})
				if captureOutput {
					args["capturedOutput"] = r.CapturedOutput
				}
				args["capturedErrorOutput"] = r.CapturedErrorOutput
				engine.ctx.InvokeCallback("processes", callbackIndex, args)
				engine.ctx.RemoveCallback("processes", callbackIndex)
			})
		} else if r.ExitStatus != 0 {
			wbgo.Error.Printf("command '%s' failed: %s", command, err)
		}
	}()
	return 0
}

func (engine *RuleEngine) esWbDefineRule() int {
	if engine.ctx.GetTop() != 2 || !engine.ctx.IsString(0) || !engine.ctx.IsObject(1) {
		engine.logFunc(fmt.Sprintf("bad rule definition"))
		return duktape.DUK_RET_ERROR
	}
	name := engine.ctx.GetString(0)
	newRule, err := newRule(engine, name, 1)
	if err != nil {
		// FIXME: proper error handling
		engine.logFunc(fmt.Sprintf("bad definition of rule '%s': %s", name, err))
		return duktape.DUK_RET_ERROR
	}
	if oldRule, found := engine.ruleMap[name]; found {
		oldRule.Destroy()
	} else {
		engine.ruleList = append(engine.ruleList, name)
	}
	engine.ruleMap[name] = newRule
	return 0
}

func (engine *RuleEngine) esWbRunRules() int {
	switch engine.ctx.GetTop() {
	case 0:
		engine.RunRules(nil, NO_TIMER_NAME)
	case 2:
		devName := engine.ctx.SafeToString(0)
		cellName := engine.ctx.SafeToString(1)
		engine.RunRules(&CellSpec{devName, cellName}, NO_TIMER_NAME)
	default:
		return duktape.DUK_RET_ERROR
	}
	return 0
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

func (engine *RuleEngine) LoadScript(path string) error {
	return engine.ctx.LoadScript(path)
}

// LiveLoadScript loads the specified script in the running engine.
// If the engine isn't ready yet, the function waits for it to become
// ready.
func (engine *RuleEngine) LiveLoadScript(path string) error {
	r := make(chan error)
	engine.model.WhenReady(func() {
		engine.model.CallSync(func() {
			err := engine.LoadScript(path)
			// must reload cron rules even in case of LoadScript() error,
			// because a part of script was still probably loaded
			engine.setupCron()
			r <- err
		})
	})

	return <-r
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
