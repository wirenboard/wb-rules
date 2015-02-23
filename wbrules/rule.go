package wbrules

import (
	"fmt"
	"io/ioutil"
	"github.com/stretchr/objx"
	duktape "github.com/ivan4th/go-duktape"
)

type RuleEngine struct {
	model *CellModel
	ctx *duktape.Context
}

func NewRuleEngine(model *CellModel) (engine *RuleEngine) {
	engine = &RuleEngine{
		model: model,
		ctx: duktape.NewContext(),
	}
	engine.defineEngineFunctions(map[string]func() bool {
		"defineVirtualDevice": engine.defineVirtualDevice,
		"defineRule": engine.defineRule,
	})
	return
}

func (engine *RuleEngine) defineVirtualDevice() bool {
	if engine.ctx.GetTop() != 2 || !engine.ctx.IsString(-2) || !engine.ctx.IsObject(-1) {
		return false
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
			return false
		} else {
			for cellName, maybeCellDef := range v.MSI() {
				cellDef, ok := maybeCellDef.(map[string]interface{})
				if !ok {
					return false
				}
				cellType, ok := cellDef["type"]
				if !ok {
					return false
				}
				cellValue, ok := cellDef["value"]
				if !ok {
					return false
				}
				dev.SetCell(cellName, cellType.(string), cellValue)
			}
		}
	}
	return true
}

func (engine *RuleEngine) defineRule() bool {
	return true
}

func (engine *RuleEngine) defineEngineFunctions(fns map[string]func() bool) {
	engine.ctx.PushGlobalObject()
	for name, fn := range fns {
		f := fn
		engine.ctx.PushGoFunc(func (*duktape.Context) int {
			if f() {
				return 0 // returns undefined
			} else {
				return duktape.DUK_RET_ERROR // FIXME: better diagnostics
			}
		})
		engine.ctx.PutPropString(-2, name)
	}
	engine.ctx.Pop()
}

func (engine *RuleEngine) LoadScript(path string) error {
	bs, err := ioutil.ReadFile(path)
	if err != nil {
		return err
	}
	defer engine.ctx.Pop()
	if r := engine.ctx.PevalString(string(bs)); r != 0 {
		return fmt.Errorf("failed to load rules from %s: %s", path, engine.ctx.SafeToString(-1))
	}
	return nil
}
