package wbrules

import (
	"errors"
	"fmt"
	"github.com/GeertJohan/go.rice"
	wbgo "github.com/contactless/wbgo"
	duktape "github.com/ivan4th/go-duktape"
	"github.com/stretchr/objx"
	"log"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

type itemType int

const (
	LIB_FILE           = "lib.js"
	MIN_INTERVAL_MS    = 1
	SOURCE_ITEM_DEVICE = itemType(iota)
	SOURCE_ITEM_RULE
)

type sourceMap map[string]*LocFileEntry

type ESEngine struct {
	*RuleEngine
	ctx           *ESContext
	scriptBox     *rice.Box
	sourceRoot    string
	sources       sourceMap
	currentSource *LocFileEntry
	sourcesMtx    sync.Mutex
}

func NewESEngine(model *CellModel, mqttClient wbgo.MQTTClient) (engine *ESEngine) {
	engine = &ESEngine{
		RuleEngine: NewRuleEngine(model, mqttClient),
		ctx:        newESContext(model.CallSync),
		scriptBox:  rice.MustFindBox("scripts"),
		sources:    make(sourceMap),
	}

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

func (engine *ESEngine) ScriptDir() string {
	// for Editor
	return engine.sourceRoot
}

func (engine *ESEngine) SetSourceRoot(sourceRoot string) (err error) {
	sourceRoot, err = filepath.Abs(sourceRoot)
	if err != nil {
		return
	}
	engine.sourceRoot = filepath.Clean(sourceRoot)
	return
}

func (engine *ESEngine) buildSingleWhenChangedRuleCondition(defIndex int) (RuleCondition, error) {
	if engine.ctx.IsString(defIndex) {
		cellFullName := engine.ctx.SafeToString(defIndex)
		parts := strings.SplitN(cellFullName, "/", 2)
		if len(parts) != 2 {
			return nil, fmt.Errorf("invalid whenChanged spec: '%s'", cellFullName)
		}
		return NewCellChangedRuleCondition(CellSpec{parts[0], parts[1]})
	}
	if engine.ctx.IsFunction(defIndex) {
		f := engine.ctx.WrapCallback(defIndex)
		return NewFuncValueChangedRuleCondition(func() interface{} { return f(nil) }), nil
	}
	return nil, errors.New("whenChanged: array expected")
}

func (engine *ESEngine) buildWhenChangedRuleCondition(defIndex int) (RuleCondition, error) {
	ctx := engine.ctx
	ctx.GetPropString(defIndex, "whenChanged")
	defer ctx.Pop()

	if !ctx.IsArray(-1) {
		return engine.buildSingleWhenChangedRuleCondition(-1)
	}

	conds := make([]RuleCondition, ctx.GetLength(-1))

	for i := range conds {
		ctx.GetPropIndex(-1, uint(i))
		cond, err := engine.buildSingleWhenChangedRuleCondition(-1)
		ctx.Pop()
		if err != nil {
			return nil, err
		} else {
			conds[i] = cond
		}
	}

	return NewOrRuleCondition(conds), nil
}

func (engine *ESEngine) buildRuleCond(defIndex int) (RuleCondition, error) {
	ctx := engine.ctx
	hasWhen := ctx.HasPropString(defIndex, "when")
	hasAsSoonAs := ctx.HasPropString(defIndex, "asSoonAs")
	hasWhenChanged := ctx.HasPropString(defIndex, "whenChanged")
	hasCron := ctx.HasPropString(defIndex, "_cron")

	switch {
	case hasWhen && (hasAsSoonAs || hasWhenChanged || hasCron):
		// _cron is added by lib.js. Under normal circumstances
		// it may not be combined with 'when' here, so no special message
		return nil, errors.New(
			"invalid rule -- cannot combine 'when' with 'asSoonAs' or 'whenChanged'")

	case hasWhen:
		return NewLevelTriggeredRuleCondition(engine.wrapRuleCondFunc(defIndex, "when")), nil

	case hasAsSoonAs && (hasWhenChanged || hasCron):
		return nil, errors.New(
			"invalid rule -- cannot combine 'asSoonAs' with 'whenChanged'")

	case hasAsSoonAs:
		return NewEdgeTriggeredRuleCondition(
			engine.wrapRuleCondFunc(defIndex, "asSoonAs")), nil

	case hasWhenChanged && hasCron:
		return nil, errors.New("invalid rule -- cannot combine 'whenChanged' with cron spec")

	case hasWhenChanged:
		return engine.buildWhenChangedRuleCondition(defIndex)

	case hasCron:
		engine.ctx.GetPropString(defIndex, "_cron")
		defer engine.ctx.Pop()
		return NewCronRuleCondition(engine.ctx.SafeToString(-1)), nil

	default:
		return nil, errors.New(
			"invalid rule -- must provide one of 'when', 'asSoonAs' or 'whenChanged'")
	}
}

func (engine *ESEngine) buildRule(name string, defIndex int) (*Rule, error) {
	if !engine.ctx.HasPropString(defIndex, "then") {
		// this should be handled by lib.js
		return nil, errors.New("invalid rule -- no then")
	}
	then := engine.wrapRuleCallback(defIndex, "then")
	if cond, err := engine.buildRuleCond(defIndex); err != nil {
		return nil, err
	} else {
		return NewRule(engine, name, cond, then), nil
	}
}

func (engine *ESEngine) loadLib() error {
	libStr, err := engine.scriptBox.String(LIB_FILE)
	if err != nil {
		return err
	}
	return engine.ctx.LoadEmbeddedScript(LIB_FILE, libStr)
}

func (engine *ESEngine) maybeRegisterSourceItem(typ itemType, name string) {
	if engine.currentSource == nil {
		return
	}

	var items *[]LocItem
	switch typ {
	case SOURCE_ITEM_DEVICE:
		items = &engine.currentSource.Devices
	case SOURCE_ITEM_RULE:
		items = &engine.currentSource.Rules
	default:
		log.Panicf("bad source item type %d", typ)
	}

	line := -1
	for _, loc := range engine.ctx.GetTraceback() {
		// Here we depend upon the fact that duktape displays
		// unmodified source paths in the backtrace
		if loc.filename == engine.currentSource.PhysicalPath {
			line = loc.line
		}
	}
	if line == -1 {
		return
	}
	*items = append(*items, LocItem{line, name})
}

func (engine *ESEngine) ListSourceFiles() (entries []LocFileEntry, err error) {
	engine.sourcesMtx.Lock()
	defer engine.sourcesMtx.Unlock()
	pathList := make([]string, 0, len(engine.sources))
	for virtualPath, _ := range engine.sources {
		pathList = append(pathList, virtualPath)
	}
	sort.Strings(pathList)
	entries = make([]LocFileEntry, len(pathList))
	for n, virtualPath := range pathList {
		entries[n] = *engine.sources[virtualPath]
	}
	return
}

func (engine *ESEngine) checkSourcePath(path string) (cleanPath string, virtualPath string, underSourceRoot bool, err error) {
	path, err = filepath.Abs(path)
	if err != nil {
		return
	}

	cleanPath = filepath.Clean(path)
	if underSourceRoot = wbgo.IsSubpath(engine.sourceRoot, cleanPath); underSourceRoot {
		virtualPath, err = filepath.Rel(engine.sourceRoot, path)
	}

	return
}

func (engine *ESEngine) LoadScript(path string) error {
	path, virtualPath, underSourceRoot, err := engine.checkSourcePath(path)
	if err != nil {
		return err
	}

	if engine.currentSource != nil {
		// must use a stack of sources to support recursive LoadScript()
		panic("recursive LoadScript() calls not supported")
	}

	// remove rules and devices defined in the previous
	// version of this script
	engine.cleanup.RunCleanups(path)

	engine.cleanup.PushCleanupScope(path)
	defer engine.cleanup.PopCleanupScope(path)
	if underSourceRoot {
		engine.currentSource = &LocFileEntry{
			VirtualPath:  virtualPath,
			PhysicalPath: path,
			Devices:      make([]LocItem, 0),
			Rules:        make([]LocItem, 0),
		}
		engine.cleanup.AddCleanup(func() {
			engine.sourcesMtx.Lock()
			delete(engine.sources, virtualPath)
			engine.sourcesMtx.Unlock()
		})
		defer func() {
			engine.sourcesMtx.Lock()
			engine.sources[virtualPath] = engine.currentSource
			engine.sourcesMtx.Unlock()
			engine.currentSource = nil
		}()
	}

	return engine.ctx.LoadScript(path)
}

func (engine *ESEngine) maybePublishUpdate(subtopic, physicalPath string) {
	_, virtualPath, underSourceRoot, err := engine.checkSourcePath(physicalPath)
	if err != nil {
		wbgo.Error.Printf("checkSourcePath() failed for %s: %s", physicalPath, err)
	}
	if underSourceRoot {
		engine.Publish("/wbrules/updates/"+subtopic, virtualPath, 1, false)
	}
}

// LiveLoadScript loads the specified script in the running engine.
// If the engine isn't ready yet, the function waits for it to become
// ready.
func (engine *ESEngine) LiveLoadScript(path string) error {
	r := make(chan error)
	engine.model.WhenReady(func() {
		err := engine.LoadScript(path)
		// must call refresh() even in case of LoadScript() error,
		// because a part of script was still probably loaded
		engine.Refresh()
		engine.maybePublishUpdate("changed", path)
		r <- err
	})

	return <-r
}

func (engine *ESEngine) LiveRemoveScript(path string) error {
	engine.model.WhenReady(func() {
		engine.cleanup.RunCleanups(path)
		engine.Refresh()
		engine.maybePublishUpdate("removed", path)
	})
	return nil
}

func (engine *ESEngine) wrapRuleCallback(defIndex int, propName string) ESCallbackFunc {
	engine.ctx.GetPropString(defIndex, propName)
	defer engine.ctx.Pop()
	return engine.ctx.WrapCallback(-1)
}

func (engine *ESEngine) wrapRuleCondFunc(defIndex int, defProp string) func() bool {
	f := engine.wrapRuleCallback(defIndex, defProp)
	return func() bool {
		r, ok := f(nil).(bool)
		return ok && r
	}
}

func (engine *ESEngine) esDefineVirtualDevice() int {
	if engine.ctx.GetTop() != 2 || !engine.ctx.IsString(-2) || !engine.ctx.IsObject(-1) {
		return duktape.DUK_RET_ERROR
	}
	name := engine.ctx.GetString(-2)
	obj := engine.ctx.GetJSObject(-1).(objx.Map)
	if err := engine.DefineVirtualDevice(name, obj); err != nil {
		wbgo.Error.Printf("device definition error: %s", err)
		return duktape.DUK_RET_ERROR
	}
	engine.maybeRegisterSourceItem(SOURCE_ITEM_DEVICE, name)
	return 0
}

func (engine *ESEngine) esFormat() int {
	engine.ctx.PushString(engine.ctx.Format())
	return 1
}

func (engine *ESEngine) esLog() int {
	engine.logFunc(engine.ctx.Format())
	return 0
}

func (engine *ESEngine) esDebug() int {
	wbgo.Debug.Printf("[rule debug] %s", engine.ctx.Format())
	return 0
}

func (engine *ESEngine) esPublish() int {
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
	topic := engine.ctx.GetString(-2)
	payload := engine.ctx.SafeToString(-1)
	engine.Publish(topic, payload, byte(qos), retain)
	return 0
}

func (engine *ESEngine) esWbDevObject() int {
	wbgo.Debug.Printf("esWbDevObject(): top=%d isString=%v", engine.ctx.GetTop(), engine.ctx.IsString(-1))
	if engine.ctx.GetTop() != 1 || !engine.ctx.IsString(-1) {
		return duktape.DUK_RET_ERROR
	}
	devProxy := engine.GetDeviceProxy(engine.ctx.GetString(-1))
	engine.ctx.PushGoObject(devProxy)
	return 1
}

func (engine *ESEngine) esWbCellObject() int {
	if engine.ctx.GetTop() != 2 || !engine.ctx.IsString(-1) || !engine.ctx.IsObject(-2) {
		return duktape.DUK_RET_ERROR
	}
	devProxy, ok := engine.ctx.GetGoObject(-2).(*DeviceProxy)
	if !ok {
		wbgo.Error.Printf("invalid _wbCellObject call")
		return duktape.DUK_RET_TYPE_ERROR
	}
	cellProxy := devProxy.EnsureCell(engine.ctx.GetString(-1))
	engine.ctx.PushGoObject(cellProxy)
	engine.ctx.DefineFunctions(map[string]func() int{
		"rawValue": func() int {
			engine.ctx.PushString(cellProxy.RawValue())
			return 1
		},
		"value": func() int {
			m := objx.New(map[string]interface{}{
				"v": cellProxy.Value(),
			})
			engine.ctx.PushJSObject(m)
			return 1
		},
		"setValue": func() int {
			if engine.ctx.GetTop() != 1 || !engine.ctx.IsObject(-1) {
				return duktape.DUK_RET_ERROR
			}
			m, ok := engine.ctx.GetJSObject(-1).(objx.Map)
			if !ok || !m.Has("v") {
				wbgo.Error.Printf("invalid cell definition")
				return duktape.DUK_RET_TYPE_ERROR
			}
			cellProxy.SetValue(m["v"])
			return 1
		},
		"isComplete": func() int {
			engine.ctx.PushBoolean(cellProxy.IsComplete())
			return 1
		},
	})
	return 1
}

func (engine *ESEngine) esWbStartTimer() int {
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
		engine.StopTimerByName(name)
	} else if !engine.ctx.IsFunction(0) {
		wbgo.Error.Println("invalid timer spec")
		return duktape.DUK_RET_ERROR
	}

	ms := engine.ctx.GetNumber(1)
	if ms < MIN_INTERVAL_MS {
		ms = MIN_INTERVAL_MS
	}
	periodic := engine.ctx.ToBoolean(2)

	var callback func()
	if name == NO_TIMER_NAME {
		f := engine.ctx.WrapCallback(0)
		callback = func() { f(nil) }
	}

	interval := time.Duration(ms * float64(time.Millisecond))
	engine.ctx.PushNumber(
		float64(engine.StartTimer(name, callback, interval, periodic)))
	return 1
}

func (engine *ESEngine) esWbStopTimer() int {
	if engine.ctx.GetTop() != 1 {
		return duktape.DUK_RET_ERROR
	}
	if engine.ctx.IsNumber(0) {
		n := engine.ctx.GetInt(-1)
		if n == 0 {
			wbgo.Error.Printf("timer id cannot be zero")
			return 0
		}
		engine.StopTimerByIndex(n)
	} else if engine.ctx.IsString(0) {
		engine.StopTimerByName(engine.ctx.ToString(0))
	} else {
		return duktape.DUK_RET_ERROR
	}
	return 0
}

func (engine *ESEngine) esWbCheckCurrentTimer() int {
	if engine.ctx.GetTop() != 1 || !engine.ctx.IsString(0) {
		return duktape.DUK_RET_ERROR
	}
	timerName := engine.ctx.ToString(0)
	engine.ctx.PushBoolean(engine.CheckTimer(timerName))
	return 1
}

func (engine *ESEngine) esWbSpawn() int {
	if engine.ctx.GetTop() != 5 || !engine.ctx.IsArray(0) || !engine.ctx.IsBoolean(2) ||
		!engine.ctx.IsBoolean(3) {
		return duktape.DUK_RET_ERROR
	}

	args := engine.ctx.StringArrayToGo(0)
	if len(args) == 0 {
		return duktape.DUK_RET_ERROR
	}

	callbackFn := ESCallbackFunc(nil)

	if engine.ctx.IsFunction(1) {
		callbackFn = engine.ctx.WrapCallback(1)
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
	go func() {
		r, err := Spawn(args[0], args[1:], captureOutput, captureErrorOutput, input)
		if err != nil {
			wbgo.Error.Printf("external command failed: %s", err)
			return
		}
		if callbackFn != nil {
			engine.model.CallSync(func() {
				args := objx.New(map[string]interface{}{
					"exitStatus": r.ExitStatus,
				})
				if captureOutput {
					args["capturedOutput"] = r.CapturedOutput
				}
				args["capturedErrorOutput"] = r.CapturedErrorOutput
				callbackFn(args)
			})
		} else if r.ExitStatus != 0 {
			wbgo.Error.Printf("command '%s' failed with exit status %d",
				strings.Join(args, " "), r.ExitStatus)
		}
	}()
	return 0
}

func (engine *ESEngine) esWbDefineRule() int {
	if engine.ctx.GetTop() != 2 || !engine.ctx.IsString(0) || !engine.ctx.IsObject(1) {
		engine.logFunc(fmt.Sprintf("bad rule definition"))
		return duktape.DUK_RET_ERROR
	}
	name := engine.ctx.GetString(0)
	if rule, err := engine.buildRule(name, 1); err != nil {
		// FIXME: proper error handling
		engine.logFunc(fmt.Sprintf("bad definition of rule '%s': %s", name, err))
		return duktape.DUK_RET_ERROR
	} else {
		engine.DefineRule(rule)
		engine.maybeRegisterSourceItem(SOURCE_ITEM_RULE, name)
	}
	return 0
}

func (engine *ESEngine) esWbRunRules() int {
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

func (engine *ESEngine) EvalScript(code string) error {
	ch := make(chan error)
	engine.model.CallSync(func() {
		ch <- engine.ctx.EvalScript(code)
	})
	return <-ch
}
