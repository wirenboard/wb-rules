package wbrules

import (
	"fmt"
	"log"
	"time"
	"strconv"
	"strings"
	"github.com/stretchr/objx"
	"github.com/GeertJohan/go.rice"
	duktape "github.com/ivan4th/go-duktape"
	wbgo "github.com/contactless/wbgo"
)

const (
	LIB_FILE = "lib.js"
	DEFAULT_CELL_MAX = 255.0
	TIMERS_CAPACITY = 128
)

type Timer interface {
	GetChannel() <-chan time.Time
	Stop()
}
type TimerFunc func (id int, d time.Duration, periodic bool) Timer

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
	timer Timer
	periodic bool
	quit chan struct{}
}

type LogFunc func (string)
type RuleEngine struct {
	model *CellModel
	mqttClient wbgo.MQTTClient
	ctx *duktape.Context
	logFunc LogFunc
	cellChange chan string
	scriptBox *rice.Box
	timerFunc TimerFunc
	timers []*TimerEntry
	callbackIndex uint64
}

func NewRuleEngine(model *CellModel, mqttClient wbgo.MQTTClient) (engine *RuleEngine) {
	engine = &RuleEngine{
		model: model,
		mqttClient: mqttClient,
		ctx: duktape.NewContext(),
		logFunc: func (message string) {
			wbgo.Info.Printf("RULE: %s\n", message)
		},
		scriptBox: rice.MustFindBox("scripts"),
		timerFunc: newTimer,
		timers: make([]*TimerEntry, 0, TIMERS_CAPACITY),
		callbackIndex: 1,
	}

	engine.initCallbackList("ruleEngineTimers")
	engine.initCallbackList("processes")

	engine.ctx.PushGlobalObject()
	engine.defineEngineFunctions(map[string]func() int {
		"defineVirtualDevice": engine.esDefineVirtualDevice,
		"log": engine.esLog,
		"debug": engine.esDebug,
		"publish": engine.esPublish,
		"_wbDevObject": engine.esWbDevObject,
		"_wbCellObject": engine.esWbCellObject,
		"_wbStartTimer": engine.esWbStartTimer,
		"_wbStopTimer": engine.esWbStopTimer,
		"_wbSpawn": engine.esWbSpawn,
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
	case uint64:
		engine.ctx.PushString(strconv.FormatUint(key.(uint64), 16))
	default:
		log.Panicf("bad callback key: %v", key)
	}
}

func (engine *RuleEngine) invokeCallback(propName string, key interface{}, args objx.Map) {
	engine.ctx.PushGlobalStash()
	engine.ctx.GetPropString(-1, propName)
	engine.pushCallbackKey(key)
	argCount := 0
	if args != nil {
		PushJSObject(engine.ctx, args)
		argCount++
	}
	if r := engine.ctx.PcallProp(-2 - argCount, argCount); r != 0 {
		wbgo.Error.Printf("failed to invoke callback %s[%v]: %s",
			propName, key, engine.ctx.SafeToString(-1))
	}
	engine.ctx.Pop3() // pop: result, callback list object, global stash
}

// storeCallback stores the callback from the specified stack index
// (which should be >= 0) at 'key' in the callback list specified as propName.
// If key is specified as nil, a new callback key is generated and returned
// as uint64. In this case the returned value is guaranteed to be
// greater than zero.
func (engine *RuleEngine) storeCallback(propName string, callbackIndex int, key interface{}) uint64 {
	var r uint64 = 0
	if key == nil {
		r = engine.callbackIndex
		key = r
		engine.callbackIndex++
	}

	engine.ctx.PushGlobalStash()
	engine.ctx.GetPropString(-1, propName)
	engine.pushCallbackKey(key)
	engine.ctx.Dup(callbackIndex)
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
		return  err
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
					dev.SetRangeCell(cellName, cellValue, fmax)
				} else {
					dev.SetCell(cellName, cellType.(string), cellValue)
				}
			}
		}
	}
	return 0
}

func (engine *RuleEngine) getLogStrArg() (string, bool) {
	strs := make([]string, 0, 100)
	for n := -engine.ctx.GetTop(); n < 0; n++ {
		strs = append(strs, engine.ctx.SafeToString(n))
	}
	if len(strs) > 0 {
		return strings.Join(strs, " "), true
	}
	return "", false
}

func (engine *RuleEngine) esLog() int {
	s, show := engine.getLogStrArg()
	if show {
		engine.logFunc(s)
	}
	return 0
}

func (engine *RuleEngine) esDebug() int {
	s, show := engine.getLogStrArg()
	if show {
		wbgo.Debug.Printf("[rule debug] %s", s)
	}
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
		Topic: engine.ctx.GetString(-2),
		Payload: engine.ctx.SafeToString(-1),
		QoS: byte(qos),
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
	engine.defineEngineFunctions(map[string]func() int {
		"rawValue": func () int {
			engine.ctx.PushString(cell.RawValue())
			return 1
		},
		"value": func () int {
			m := objx.New(map[string]interface{} {
				"v": cell.Value(),
			})
			PushJSObject(engine.ctx, m)
			return 1
		},
		"setValue": func () int {
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
		"isComplete": func () int {
			engine.ctx.PushBoolean(cell.IsComplete())
			return 1
		},
	})
	return 1
}

func (engine *RuleEngine) fireTimer(n int) {
	entry := engine.timers[n - 1]
	if entry == nil {
		wbgo.Error.Printf("firing unknown timer %d", n)
		return
	}
	engine.invokeCallback("ruleEngineTimers", n, nil)

	if !entry.periodic {
		engine.removeTimer(n)
	}
}

func (engine *RuleEngine) esWbStartTimer() int {
	if engine.ctx.GetTop() != 3 || !engine.ctx.IsFunction(-3) || !engine.ctx.IsNumber(-2) {
		return duktape.DUK_RET_ERROR
	}

	ms := engine.ctx.GetNumber(-2)
	periodic := engine.ctx.ToBoolean(-1)

	entry := &TimerEntry{
		//,
		periodic: periodic,
		quit: make(chan struct{}, 2),
	}

	var n int
	for n = 1; n <= len(engine.timers); n++ {
		if engine.timers[n - 1] == nil {
			engine.timers[n - 1] = entry
		}
		break
	}
	if n > len(engine.timers) {
		engine.timers = append(engine.timers, entry)
	}

	engine.storeCallback("ruleEngineTimers", 0, n)

	entry.timer = engine.timerFunc(n, time.Duration(ms * float64(time.Millisecond)), periodic)
	tickCh := entry.timer.GetChannel()
	go func () {
		for {
			select {
			case <- tickCh:
				engine.model.CallSync(func () {
					engine.fireTimer(n)
				})
				if !periodic {
					return
				}
			case <- entry.quit:
				entry.timer.Stop()
				return
			}
		}
	}()

	engine.ctx.PushNumber(float64(n))
	return 1
}

func (engine *RuleEngine) removeTimer(n int) {
	engine.removeCallback("ruleEngineTimers", n)
	engine.timers[n - 1] = nil
}

func (engine *RuleEngine) esWbStopTimer() int {
	if engine.ctx.GetTop() != 1 || !engine.ctx.IsNumber(-1) {
		return duktape.DUK_RET_ERROR
	}
	n := engine.ctx.GetInt(-1)
	if n == 0 {
		wbgo.Error.Printf("timer id cannot be zero")
		return 0
	}
	if entry := engine.timers[n - 1]; entry != nil {
		engine.removeTimer(n)
		close(entry.quit)
	} else {
		wbgo.Error.Printf("trying to stop unknown timer: %d", n)
	}
	return 0
}

func (engine *RuleEngine) esWbSpawn() int {
	if engine.ctx.GetTop() != 5 || !engine.ctx.IsArray(0) || !engine.ctx.IsBoolean(2) ||
		!engine.ctx.IsBoolean(3) {
		return duktape.DUK_RET_ERROR
	}

	nArgs := engine.ctx.GetLength(0)
	if nArgs == 0 {
		return duktape.DUK_RET_ERROR
	}

	args := make([]string, nArgs)
	for i := 0; i < nArgs; i++ {
		engine.ctx.GetPropIndex(0, uint(i))
		args[i] = engine.ctx.SafeToString(-1)
		engine.ctx.Pop()
	}

	callbackIndex := uint64(0)

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
	go func () {
		r, err := Spawn(args[0], args[1:], captureOutput, captureErrorOutput, input)
		if err != nil {
			wbgo.Error.Printf("external command failed: %s", err)
			return
		}
		if callbackIndex > 0 {
			engine.model.CallSync(func () {
				args := objx.New(map[string]interface{} {
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

func (engine *RuleEngine) defineEngineFunctions(fns map[string]func() int) {
	for name, fn := range fns {
		f := fn
		engine.ctx.PushGoFunc(func (*duktape.Context) int {
			return f()
		})
		engine.ctx.PutPropString(-2, name)
	}
}

func (engine *RuleEngine) RunRules(changedCellName string) {
	engine.ctx.PushGlobalObject()
	engine.ctx.PushString("runRules")
	engine.ctx.PushString(changedCellName)
	defer engine.ctx.Pop2()
	if r := engine.ctx.PcallProp(-3, 1); r != 0 {
		wbgo.Error.Printf("failed to run rules: %s", engine.ctx.SafeToString(-1))
	}
}

func (engine *RuleEngine) LoadScript(path string) error {
	defer engine.ctx.Pop()
	if r := engine.ctx.PevalFile(path); r != 0 {
		return fmt.Errorf("failed to load lib.js: %s", engine.ctx.SafeToString(-1))
	}
	return nil
}

func (engine *RuleEngine) Start() {
	if engine.cellChange != nil {
		return
	}
	engine.cellChange = engine.model.AcquireCellChangeChannel()
	ready := make(chan struct{})
	engine.model.WhenReady(func () {
		engine.RunRules("")
		close(ready)
	})
	go func () {
		_, _ = <- ready
		for {
			select {
			case cellName, ok := <- engine.cellChange:
				if ok {
					wbgo.Debug.Printf(
						"rule engine: running rules after cell change: %s",
						cellName)
					engine.model.CallSync(func () {
						engine.RunRules(cellName)
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
