package wbrules

import (
	"fmt"
	"log"
	"strings"
	"io/ioutil"
	"github.com/stretchr/objx"
	"github.com/GeertJohan/go.rice"
	duktape "github.com/ivan4th/go-duktape"
)

const DEFAULT_CELL_MAX = 255.0

type LogFunc func (string)
type RuleEngine struct {
	model *CellModel
	ctx *duktape.Context
	logFunc LogFunc
	cellChange chan string
	scriptBox *rice.Box
}

func NewRuleEngine(model *CellModel) (engine *RuleEngine) {
	engine = &RuleEngine{
		model: model,
		ctx: duktape.NewContext(),
		logFunc: func (message string) {
			log.Printf("RULE: %s\n", message)
		},
		scriptBox: rice.MustFindBox("scripts"),
	}
	engine.ctx.PushGlobalObject()
	engine.defineEngineFunctions(map[string]func() int {
		"defineVirtualDevice": engine.esDefineVirtualDevice,
		"log": engine.esLog,
		"_wbDevObject": engine.esWbDevObject,
		"_wbCellObject": engine.esWbCellObject,
	})
	engine.ctx.Pop()
	if err := engine.loadLib(); err != nil {
		log.Panicf("failed to load runtime library: %s", err)
	}
	return
}

func (engine *RuleEngine) loadLib() error {
	libStr, err := engine.scriptBox.String("lib.js")
	if err != nil {
		return  err
	}
	if r := engine.ctx.PevalString(libStr); r != 0 {
		return fmt.Errorf("failed to load lib.js: %s", engine.ctx.SafeToString(-1))
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
				log.Printf("cellType=%v", cellType)
				if cellType == "range" {
					fmax := DEFAULT_CELL_MAX
					max, ok := cellDef["max"]
					if ok {
						log.Printf("--- max: %v", max)
						fmax, ok = max.(float64)
						if !ok {
							return duktape.DUK_RET_ERROR
						}
					}
					// FIXME: can be float
					log.Printf("SetRangeCell: %s", cellName)
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
	bs, err := ioutil.ReadFile(path)
	if err != nil {
		return err
	}
	defer engine.ctx.Pop()
	if r := engine.ctx.PevalString(string(bs)); r != 0 {
		return fmt.Errorf("failed to load %s: %s", path, engine.ctx.SafeToString(-1))
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
					engine.cellChange = nil
				}
			}
		}
	}()
}
