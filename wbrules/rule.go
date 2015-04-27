package wbrules

import (
	"bytes"
	"errors"
	"fmt"
	"github.com/GeertJohan/go.rice"
	wbgo "github.com/contactless/wbgo"
	duktape "github.com/ivan4th/go-duktape"
	"github.com/stretchr/objx"
	"log"
	"strconv"
	"strings"
	"time"
)

type esCallback uint64

const (
	NO_TIMER_NAME       = ""
	LIB_FILE            = "lib.js"
	DEFAULT_CELL_MAX    = 255.0
	TIMERS_CAPACITY     = 128
	RULES_CAPACITY      = 256
	CELL_RULES_CAPACITY = 8
	NO_CALLBACK         = esCallback(0)
)

type Timer interface {
	GetChannel() <-chan time.Time
	Stop()
}
type TimerFunc func(id int, d time.Duration, periodic bool) Timer

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
	Destroy()
}

type RuleConditionBase struct{}

func (ruleCond *RuleConditionBase) GetCells() []*Cell {
	return []*Cell{}
}

func (ruleCond *RuleConditionBase) Destroy() {}

type SimpleCallbackCondition struct {
	RuleConditionBase
	engine *RuleEngine
	cond   esCallback
}

func (ruleCond *SimpleCallbackCondition) invokeCond() bool {
	r, ok := ruleCond.engine.invokeCallback("ruleFuncs", ruleCond.cond, nil).(bool)
	return ok && r
}

func (ruleCond *SimpleCallbackCondition) Destroy() {
	ruleCond.engine.removeCallback("ruleFuncs", ruleCond.cond)
}

type LevelTriggeredRuleCondition struct {
	SimpleCallbackCondition
}

func newLevelTriggeredRuleCondition(engine *RuleEngine, defIndex int) *LevelTriggeredRuleCondition {
	return &LevelTriggeredRuleCondition{
		SimpleCallbackCondition: SimpleCallbackCondition{
			engine: engine,
			cond:   engine.storeRuleCallback(defIndex, "when"),
		},
	}
}

func (ruleCond *LevelTriggeredRuleCondition) Check(cell *Cell) (bool, interface{}) {
	return ruleCond.invokeCond(), nil
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
			engine: engine,
			cond:   engine.storeRuleCallback(defIndex, "asSoonAs"),
		},
		prevCondValue: false,
		firstRun:      false,
	}
}

func (ruleCond *EdgeTriggeredRuleCondition) Check(cell *Cell) (bool, interface{}) {
	current := ruleCond.invokeCond()
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
	thunk    esCallback
	oldValue interface{}
}

func newFuncValueChangedRuleCondition(engine *RuleEngine, funcIndex int) *FuncValueChangedRuleCondition {
	return &FuncValueChangedRuleCondition{
		engine:   engine,
		thunk:    engine.storeCallback("ruleFuncs", -1, nil),
		oldValue: nil,
	}
}

func (ruleCond *FuncValueChangedRuleCondition) Check(cell *Cell) (bool, interface{}) {
	v := ruleCond.engine.invokeCallback("ruleFuncs", ruleCond.thunk, nil)
	if ruleCond.oldValue == v {
		return false, nil
	}
	ruleCond.oldValue = v
	return true, v
}

type OrRuleCondition struct {
	conds []RuleCondition
}

func newOrRuleCondition(conds []RuleCondition) *OrRuleCondition {
	return &OrRuleCondition{conds}
}

func (ruleCond *OrRuleCondition) GetCells() []*Cell {
	r := make([]*Cell, 0, 10)
	for _, cond := range ruleCond.conds {
		r = append(r, cond.GetCells()...)
	}
	return r
}

func (ruleCond *OrRuleCondition) Destroy() {
	for _, cond := range ruleCond.conds {
		cond.Destroy()
	}
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
		return newFuncValueChangedRuleCondition(engine, defIndex), nil
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

type Rule struct {
	engine      *RuleEngine
	name        string
	cond        RuleCondition
	then        esCallback
	shouldCheck bool
}

func newRule(engine *RuleEngine, name string, defIndex int) (*Rule, error) {
	rule := &Rule{
		engine:      engine,
		name:        name,
		then:        0,
		shouldCheck: false,
	}
	ctx := engine.ctx

	if !ctx.HasPropString(defIndex, "then") {
		// this should be handled by lib.js
		return nil, errors.New("invalid rule -- no then")
	}
	rule.then = rule.engine.storeRuleCallback(defIndex, "then")
	hasWhen := ctx.HasPropString(defIndex, "when")
	hasAsSoonAs := ctx.HasPropString(defIndex, "asSoonAs")
	hasWhenChanged := ctx.HasPropString(defIndex, "whenChanged")

	if hasWhen {
		if hasAsSoonAs || hasWhenChanged {
			return nil, errors.New(
				"invalid rule -- cannot combine 'when' with 'asSoonAs' or 'whenChanged'")
		}
		rule.cond = newLevelTriggeredRuleCondition(engine, defIndex)
	} else if hasAsSoonAs {
		if hasWhenChanged {
			return nil, errors.New(
				"invalid rule -- cannot combine 'asSoonAs' with 'whenChanged'")
		}
		rule.cond = newEdgeTriggeredRuleCondition(engine, defIndex)
	} else if hasWhenChanged {
		var err error
		if rule.cond, err = newWhenChangedRuleCondition(engine, defIndex); err != nil {
			return nil, err
		}
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
	rule.engine.invokeCallback("ruleFuncs", rule.then, args)
}

func (rule *Rule) Destroy() {
	rule.cond.Destroy()
	if rule.then != 0 {
		rule.engine.removeCallback("ruleFuncs", rule.then)
	}
	rule.cond = newDestroyedRuleCondition()
}

type LogFunc func(string)

type RuleEngine struct {
	model             *CellModel
	mqttClient        wbgo.MQTTClient
	ctx               *duktape.Context
	logFunc           LogFunc
	cellChange        chan *CellSpec
	scriptBox         *rice.Box
	timerFunc         TimerFunc
	timers            []*TimerEntry
	callbackIndex     esCallback
	ruleMap           map[string]*Rule
	ruleList          []string
	notedCells        map[*Cell]bool
	notedTimers       map[string]bool
	cellToRuleMap     map[*Cell][]*Rule
	rulesWithoutCells map[*Rule]bool
	timerRules        map[string][]*Rule
	currentTimer      string
}

func NewRuleEngine(model *CellModel, mqttClient wbgo.MQTTClient) (engine *RuleEngine) {
	engine = &RuleEngine{
		model:      model,
		mqttClient: mqttClient,
		ctx:        duktape.NewContext(),
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
	}

	engine.initCallbackList("ruleEngineTimers")
	engine.initCallbackList("processes")
	engine.initCallbackList("ruleFuncs")

	engine.ctx.PushGlobalObject()
	engine.defineEngineFunctions(map[string]func() int{
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

func (engine *RuleEngine) initCallbackList(propName string) {
	// callback list stash property holds callback functions referenced by ids
	engine.ctx.PushGlobalStash()
	engine.ctx.PushObject()
	engine.ctx.PutPropString(-2, propName)
	engine.ctx.Pop()
}

func (engine *RuleEngine) pushCallbackKey(key interface{}) {
	switch key.(type) {
	case int:
		engine.ctx.PushNumber(float64(key.(int)))
	case esCallback:
		engine.ctx.PushString(strconv.FormatUint(uint64(key.(esCallback)), 16))
	default:
		log.Panicf("bad callback key: %v", key)
	}
}

func (engine *RuleEngine) invokeCallback(propName string, key interface{}, args objx.Map) interface{} {
	engine.ctx.PushGlobalStash()
	engine.ctx.GetPropString(-1, propName)
	engine.pushCallbackKey(key)
	argCount := 0
	if args != nil {
		PushJSObject(engine.ctx, args)
		argCount++
	}
	defer engine.ctx.Pop3() // pop: result, callback list object, global stash
	if s := engine.ctx.PcallProp(-2-argCount, argCount); s != 0 {
		wbgo.Error.Printf("failed to invoke callback %s[%v]: %s",
			propName, key, engine.ctx.SafeToString(-1))
		return nil
	} else if engine.ctx.IsBoolean(-1) {
		return engine.ctx.ToBoolean(-1)
	} else if engine.ctx.IsString(-1) {
		return engine.ctx.ToString(-1)
	} else if engine.ctx.IsNumber(-1) {
		return engine.ctx.ToNumber(-1)
	} else {
		return nil
	}
}

func (engine *RuleEngine) storeRuleCallback(defIndex int, propName string) esCallback {
	engine.ctx.GetPropString(defIndex, propName)
	defer engine.ctx.Pop()
	return engine.storeCallback("ruleFuncs", -1, nil)
}

// storeCallback stores the callback from the specified stack index
// (which should be >= 0) at 'key' in the callback list specified as propName.
// If key is specified as nil, a new callback key is generated and returned
// as uint64. In this case the returned value is guaranteed to be
// greater than zero.
func (engine *RuleEngine) storeCallback(propName string, callbackStackIndex int, key interface{}) esCallback {
	var r esCallback = 0
	if key == nil {
		r = engine.callbackIndex
		key = r
		engine.callbackIndex++
	}

	engine.ctx.PushGlobalStash()
	engine.ctx.GetPropString(-1, propName)
	engine.pushCallbackKey(key)
	if callbackStackIndex < 0 {
		engine.ctx.Dup(callbackStackIndex - 3)
	} else {
		engine.ctx.Dup(callbackStackIndex)
	}
	engine.ctx.PutProp(-3) // callbackList[key] = callback
	engine.ctx.Pop2()
	return r
}

func (engine *RuleEngine) removeCallback(propName string, key interface{}) {
	engine.ctx.PushGlobalStash()
	engine.ctx.GetPropString(-1, propName)
	engine.pushCallbackKey(key)
	engine.ctx.DelProp(-2)
	engine.ctx.Pop()
}

func (engine *RuleEngine) SetTimerFunc(timerFunc TimerFunc) {
	engine.timerFunc = timerFunc
}

func (engine *RuleEngine) loadLib() error {
	libStr, err := engine.scriptBox.String(LIB_FILE)
	if err != nil {
		return err
	}
	engine.ctx.PushString(LIB_FILE)
	// we use PcompileStringFilename here to get readable stacktraces
	if r := engine.ctx.PcompileStringFilename(0, libStr); r != 0 {
		defer engine.ctx.Pop()
		return fmt.Errorf("failed to compile lib.js: %s", engine.ctx.SafeToString(-1))
	}
	defer engine.ctx.Pop()
	if r := engine.ctx.Pcall(0); r != 0 {
		return fmt.Errorf("failed to run lib.js: %s", engine.ctx.SafeToString(-1))
	}
	return nil
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
	obj := GetJSObject(engine.ctx, -1).(objx.Map)
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
				cellValue, ok := cellDef["value"]
				if !ok {
					return duktape.DUK_RET_ERROR
				}

				cellReadonly := false;
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
	return 0
}

func (engine *RuleEngine) format() string {
	top := engine.ctx.GetTop()
	if top < 1 {
		return ""
	}
	s := engine.ctx.SafeToString(0)
	p := 1
	parts := strings.Split(s, "{{")
	buf := new(bytes.Buffer)
	for i, part := range parts {
		if i > 0 {
			buf.WriteString("{")
		}
		for j, subpart := range strings.Split(part, "{}") {
			if j > 0 && p < top {
				buf.WriteString(engine.ctx.SafeToString(p))
				p++
			}
			buf.WriteString(subpart)
		}
	}
	// write remaining parts
	for ; p < top; p++ {
		buf.WriteString(" ")
		buf.WriteString(engine.ctx.SafeToString(p))
	}
	return buf.String()
}

func (engine *RuleEngine) esFormat() int {
	engine.ctx.PushString(engine.format())
	return 1
}

func (engine *RuleEngine) esLog() int {
	engine.logFunc(engine.format())
	return 0
}

func (engine *RuleEngine) esDebug() int {
	wbgo.Debug.Printf("[rule debug] %s", engine.format())
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
	engine.defineEngineFunctions(map[string]func() int{
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
			PushJSObject(engine.ctx, m)
			return 1
		},
		"setValue": func() int {
			engine.trackCell(cell)
			if engine.ctx.GetTop() != 1 || !engine.ctx.IsObject(-1) {
				return duktape.DUK_RET_ERROR
			}
			m, ok := GetJSObject(engine.ctx, -1).(objx.Map)
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
		engine.invokeCallback("ruleEngineTimers", n, nil)
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
		engine.storeCallback("ruleEngineTimers", 0, n)
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
	engine.removeCallback("ruleEngineTimers", n)
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

	args := StringArrayToGo(engine.ctx, 0)
	if len(args) == 0 {
		return duktape.DUK_RET_ERROR
	}

	callbackIndex := NO_CALLBACK

	if engine.ctx.IsFunction(1) {
		callbackIndex = engine.storeCallback("processes", 1, nil)
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
				engine.invokeCallback("processes", callbackIndex, args)
				engine.removeCallback("processes", callbackIndex)
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

func (engine *RuleEngine) defineEngineFunctions(fns map[string]func() int) {
	for name, fn := range fns {
		f := fn
		engine.ctx.PushGoFunc(func(*duktape.Context) int {
			return f()
		})
		engine.ctx.PutPropString(-2, name)
	}
}

func (engine *RuleEngine) getCell(devName, cellName string) *Cell {
	return engine.model.EnsureDevice(devName).EnsureCell(cellName)
}

func (engine *RuleEngine) RunRules(cellSpec *CellSpec, timerName string) {
	var cell *Cell
	if cellSpec != nil {
		cell = engine.getCell(cellSpec.DevName, cellSpec.CellName)
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
	defer engine.ctx.Pop()
	if r := engine.ctx.PevalFile(path); r != 0 {
		engine.ctx.GetPropString(-1, "stack")
		message := engine.ctx.SafeToString(-1)
		engine.ctx.Pop()
		if message == "" {
			message = engine.ctx.SafeToString(-1)
		}
		return fmt.Errorf("failed to load %s: %s", path, message)
	}
	return nil
}

func (engine *RuleEngine) Start() {
	if engine.cellChange != nil {
		return
	}
	engine.cellChange = engine.model.AcquireCellChangeChannel()
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
					engine.cellChange = nil
				}
			}
		}
	}()
}
