package wbrules

import (
	"fmt"
	"log"
	"time"
	"strings"
	"github.com/stretchr/objx"
	"github.com/GeertJohan/go.rice"
	duktape "github.com/ivan4th/go-duktape"
)

const (
	LIB_FILE = "lib.js"
	DEFAULT_CELL_MAX = 255.0
)

type Timer interface {
	GetChannel() <-chan time.Time
	Stop()
}
type TimerFunc func (name string, d time.Duration, periodic bool) Timer

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

func newTimer(name string, d time.Duration, periodic bool) Timer {
	if periodic {
		return &RealTicker{time.NewTicker(d)}
	} else {
		return &RealTimer{time.NewTimer(d)}
	}
}

type TimerEntry struct {
	timer Timer
	quit chan struct{}
}

type LogFunc func (string)
type RuleEngine struct {
	model *CellModel
	ctx *duktape.Context
	logFunc LogFunc
	cellChange chan string
	scriptBox *rice.Box
	timerFunc TimerFunc
	timers map[string]*TimerEntry
}

func NewRuleEngine(model *CellModel) (engine *RuleEngine) {
	engine = &RuleEngine{
		model: model,
		ctx: duktape.NewContext(),
		logFunc: func (message string) {
			log.Printf("RULE: %s\n", message)
		},
		scriptBox: rice.MustFindBox("scripts"),
		timerFunc: newTimer,
		timers: make(map[string]*TimerEntry),
	}
	engine.ctx.PushGlobalObject()
	engine.defineEngineFunctions(map[string]func() int {
		"defineVirtualDevice": engine.esDefineVirtualDevice,
		"log": engine.esLog,
		"_wbDevObject": engine.esWbDevObject,
		"_wbCellObject": engine.esWbCellObject,
		"_wbStartTimer": engine.esWbStartTimer,
		"_wbStopTimer": engine.esWbStopTimer,
	})
	engine.ctx.Pop()
	if err := engine.loadLib(); err != nil {
		log.Panicf("failed to load runtime library: %s", err)
	}
	return
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

func (engine *RuleEngine) esLog() int {
	strs := make([]string, 0, 100)
	for n := -engine.ctx.GetTop(); n < 0; n++ {
		strs = append(strs, engine.ctx.SafeToString(n))
	}
	if len(strs) > 0 {
		engine.logFunc(strings.Join(strs, " "))
	}
	return 0
}

func (engine *RuleEngine) esWbDevObject() int {
	log.Printf("esWbDevObject(): top=%d isString=%v", engine.ctx.GetTop(), engine.ctx.IsString(-1))
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
		log.Printf("WARNING: invalid _wbCellObject call")
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
				log.Printf("WARNING: invalid cell definition")
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

func (engine *RuleEngine) fireTimer(name string) {
	engine.ctx.PushGlobalObject()
	engine.ctx.PushString("_runTimer")
	engine.ctx.PushString(name)
	if r := engine.ctx.PcallProp(-3, 1); r != 0 {
		log.Printf("failed to fire timer '%s': %s", name, engine.ctx.SafeToString(-1))
	}
	engine.ctx.Pop2()
}

func (engine *RuleEngine) esWbStartTimer() int {
	if engine.ctx.GetTop() != 3 || !engine.ctx.IsString(-3) || !engine.ctx.IsNumber(-2) {
		return duktape.DUK_RET_ERROR
	}

	name := engine.ctx.GetString(-3)
	ms := engine.ctx.GetNumber(-2)
	periodic := engine.ctx.ToBoolean(-1)
	entry, found := engine.timers[name]
	if found {
		close(entry.quit)
	}

	entry = &TimerEntry{
		engine.timerFunc(name, time.Duration(ms * float64(time.Millisecond)), periodic),
		make(chan struct{}, 2),
	}
	engine.timers[name] = entry
	tickCh := entry.timer.GetChannel()
	go func () {
		for {
			select {
			case <- tickCh:
				engine.model.CallSync(func () {
					engine.fireTimer(name)
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
	return 0
}

func (engine *RuleEngine) esWbStopTimer() int {
	if engine.ctx.GetTop() != 1 || !engine.ctx.IsString(-1) {
		return duktape.DUK_RET_ERROR
	}
	name := engine.ctx.GetString(-1)
	entry, found := engine.timers[name]
	delete(engine.timers, name)
	if found {
		close(entry.quit)
	} else {
		log.Printf("warning: trying to stop unknown timer: %s", name)
	}
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

func (engine *RuleEngine) RunRules() error {
	engine.ctx.PushGlobalObject()
	engine.ctx.PushString("runRules")
	defer engine.ctx.Pop2()
	if r := engine.ctx.PcallProp(-2, 0); r != 0 {
		return fmt.Errorf("failed to run rules: %s", engine.ctx.SafeToString(-1))
	}
	return nil
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
	go func () {
		for {
			select {
			case cellName, ok := <- engine.cellChange:
				if ok {
					log.Printf(
						"rule engine: running rules after cell change: %s",
						cellName)
					engine.model.CallSync(func () {
						engine.RunRules()
					})
				} else {
					log.Printf("engine stopped")
					for _, entry := range engine.timers {
						close(entry.quit)
					}
					engine.timers = make(map[string]*TimerEntry)
					engine.cellChange = nil
				}
			}
		}
	}()
}
