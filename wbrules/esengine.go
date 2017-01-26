package wbrules

import (
	"errors"
	"fmt"
	"github.com/DisposaBoy/JsonConfigReader"
	"github.com/boltdb/bolt"
	duktape "github.com/contactless/go-duktape"
	wbgo "github.com/contactless/wbgo"
	"github.com/stretchr/objx"
	"io/ioutil"
	"log"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

type itemType int

const (
	LIB_FILE            = "lib.js"
	LIB_SYS_PATH        = "/usr/share/wb-rules-system/scripts"
	LIB_REL_PATH_1      = "scripts"
	LIB_REL_PATH_2      = "../scripts"
	MIN_INTERVAL_MS     = 1
	PERSISTENT_DB_CHMOD = 0640
	SOURCE_ITEM_DEVICE  = itemType(iota)
	SOURCE_ITEM_RULE
)

var noLibJs = errors.New("unable to locate lib.js")
var searchDirs = []string{LIB_SYS_PATH}

type sourceMap map[string]*LocFileEntry

type ESEngineOptions struct {
	*RuleEngineOptions
	PersistentDBFile     string
	PersistentDBFileMode os.FileMode
	ScriptDirs           []string
}

func NewESEngineOptions() *ESEngineOptions {
	return &ESEngineOptions{
		RuleEngineOptions:    NewRuleEngineOptions(),
		PersistentDBFileMode: PERSISTENT_DB_CHMOD,
	}
}

func (o *ESEngineOptions) SetPersistentDBFile(file string) {
	o.PersistentDBFile = file
}

func (o *ESEngineOptions) SetPersistentDBFileMode(mode os.FileMode) {
	o.PersistentDBFileMode = mode
}

func (o *ESEngineOptions) SetScriptDirs(dirs []string) {
	o.ScriptDirs = dirs
}

type ESEngine struct {
	*RuleEngine
	ctx               *ESContext
	sourceRoot        string
	sources           sourceMap
	currentSource     *LocFileEntry
	sourcesMtx        sync.Mutex
	tracker           *wbgo.ContentTracker
	persistentDBCache map[string]string
	persistentDB      *bolt.DB
	scriptDirs        []string
}

func init() {
	if wd, err := os.Getwd(); err == nil {
		searchDirs = []string{
			LIB_SYS_PATH,
			filepath.Join(wd, LIB_REL_PATH_1),
			filepath.Join(wd, LIB_REL_PATH_2),
		}
	}
}

func NewESEngine(model *CellModel, mqttClient wbgo.MQTTClient, options *ESEngineOptions) (engine *ESEngine) {
	if options == nil {
		panic("no options given to NewESEngine")
	}

	engine = &ESEngine{
		RuleEngine:        NewRuleEngine(model, mqttClient, options.RuleEngineOptions),
		ctx:               newESContext(model.CallSync),
		sources:           make(sourceMap),
		tracker:           wbgo.NewContentTracker(),
		persistentDBCache: make(map[string]string),
		persistentDB:      nil,
		scriptDirs:        options.ScriptDirs,
	}

	if options.PersistentDBFile != "" {
		if err := engine.SetPersistentDBMode(options.PersistentDBFile,
			options.PersistentDBFileMode); err != nil {
			panic("error opening persistent DB file: " + err.Error())
		}
	}

	engine.ctx.SetCallbackErrorHandler(func(err ESError) {
		engine.Log(ENGINE_LOG_ERROR, fmt.Sprintf("ECMAScript error: %s", err))
	})

	// export modSearch
	engine.ctx.GetGlobalString("Duktape")
	engine.ctx.PushGoFunc(func(c *duktape.Context) int {
		return engine.ModSearch(c)
	})
	engine.ctx.PutPropString(-2, "modSearch")
	engine.ctx.Pop()

	engine.ctx.PushGlobalObject()
	engine.ctx.DefineFunctions(map[string]func() int{
		"defineVirtualDevice":  engine.esDefineVirtualDevice,
		"format":               engine.esFormat,
		"log":                  engine.makeLogFunc(ENGINE_LOG_INFO),
		"debug":                engine.makeLogFunc(ENGINE_LOG_DEBUG),
		"publish":              engine.esPublish,
		"_wbDevObject":         engine.esWbDevObject,
		"_wbCellObject":        engine.esWbCellObject,
		"_wbStartTimer":        engine.esWbStartTimer,
		"_wbStopTimer":         engine.esWbStopTimer,
		"_wbCheckCurrentTimer": engine.esWbCheckCurrentTimer,
		"_wbSpawn":             engine.esWbSpawn,
		"_wbDefineRule":        engine.esWbDefineRule,
		"runRules":             engine.esWbRunRules,
		"readConfig":           engine.esReadConfig,
		"_wbPersistentName":    engine.esPersistentName,
		"_wbPersistentSet":     engine.esPersistentSet,
		"_wbPersistentGet":     engine.esPersistentGet,
	})
	engine.ctx.GetPropString(-1, "log")
	engine.ctx.DefineFunctions(map[string]func() int{
		"debug":   engine.makeLogFunc(ENGINE_LOG_DEBUG),
		"info":    engine.makeLogFunc(ENGINE_LOG_INFO),
		"warning": engine.makeLogFunc(ENGINE_LOG_WARNING),
		"error":   engine.makeLogFunc(ENGINE_LOG_ERROR),
	})
	engine.ctx.Pop2()
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
			"invalid rule -- cannot combine 'when' with 'asSoonAs', 'whenChanged' or 'cron'")

	case hasWhen:
		return NewLevelTriggeredRuleCondition(engine.wrapRuleCondFunc(defIndex, "when")), nil

	case hasAsSoonAs && (hasWhenChanged || hasCron):
		return nil, errors.New(
			"invalid rule -- cannot combine 'asSoonAs' with 'whenChanged' or 'cron'")

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
	for _, dir := range searchDirs {
		path := filepath.Join(dir, LIB_FILE)
		if _, err := os.Stat(path); err == nil {
			return engine.ctx.LoadScript(path)
		}
	}
	return noLibJs
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

func (engine *ESEngine) checkVirtualPath(path string) (cleanPath string, virtualPath string, err error) {
	physicalPath := filepath.Join(engine.sourceRoot, filepath.Clean(path))
	cleanPath, virtualPath, underSourceRoot, err := engine.checkSourcePath(physicalPath)
	if err == nil && !underSourceRoot {
		err = errors.New("path not under source root")
	}
	return
}

func (engine *ESEngine) LoadFile(path string) (err error) {
	_, err = engine.loadScript(path, true)
	return
}

func (engine *ESEngine) loadScript(path string, loadIfUnchanged bool) (bool, error) {
	path, virtualPath, underSourceRoot, err := engine.checkSourcePath(path)
	if err != nil {
		return false, err
	}

	if engine.currentSource != nil {
		// must use a stack of sources to support recursive LoadScript()
		panic("recursive loadScript() calls not supported")
	}

	wasChangedOrFirstSeen, err := engine.tracker.Track(virtualPath, path)
	if err != nil {
		return false, err
	}
	if !loadIfUnchanged && !wasChangedOrFirstSeen {
		wbgo.Debug.Printf("script %s unchanged, not reloading (possibly just reloaded)", path)
		return false, nil
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

	return true, engine.trackESError(path, engine.ctx.LoadScenario(path))
}

func (engine *ESEngine) trackESError(path string, err error) error {
	esError, ok := err.(ESError)
	if !ok {
		return err
	}

	// ESError contains physical file paths in its traceback.
	// Here we need to translate them to virtual paths.
	// We skip any frames that refer to files that don't
	// reside under the source root.
	traceback := make([]LocItem, 0, len(esError.Traceback))
	for _, esLoc := range esError.Traceback {
		_, virtualPath, underSourceRoot, err :=
			engine.checkSourcePath(esLoc.filename)
		if err == nil && underSourceRoot {
			traceback = append(traceback, LocItem{esLoc.line, virtualPath})
		}
	}

	scriptErr := NewScriptError(esError.Message, traceback)
	if engine.currentSource != nil {
		engine.currentSource.Error = &scriptErr
	}
	return scriptErr
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

func (engine *ESEngine) loadScriptAndRefresh(path string, loadIfUnchanged bool) (err error) {
	loaded, err := engine.loadScript(path, loadIfUnchanged)
	if loaded {
		// must call refresh() even in case of loadScript() error,
		// because a part of script was still probably loaded
		engine.Refresh()
		engine.maybePublishUpdate("changed", path)
	}
	return
}

func (engine *ESEngine) LiveWriteScript(virtualPath, content string) error {
	r := make(chan error)
	engine.model.WhenReady(func() {
		wbgo.Debug.Printf("OverwriteScript(%s)", virtualPath)
		cleanPath, virtualPath, err := engine.checkVirtualPath(virtualPath)
		wbgo.Debug.Printf("OverwriteScript: %s %s %v", cleanPath, virtualPath, err)
		if err != nil {
			r <- err
			return
		}

		// Make sure directories that contain the script exist
		if strings.Contains(virtualPath, "/") {
			if err = os.MkdirAll(filepath.Dir(cleanPath), 0777); err != nil {
				wbgo.Error.Printf("error making dirs for %s: %s", cleanPath, err)
				r <- err
				return
			}
		}

		// WriteFile() will cause DirWatcher to wake up and invoke
		// LiveLoadFile for the file, but as the new content
		// will be already registered with the contentTracker,
		// duplicate reload will not happen
		err = ioutil.WriteFile(cleanPath, []byte(content), 0777)
		if err != nil {
			r <- err
			return
		}
		r <- engine.loadScriptAndRefresh(cleanPath, true)
	})
	return <-r
}

// LiveLoadFile loads the specified script in the running engine.
// If the engine isn't ready yet, the function waits for it to become
// ready. If the script didn't change since the last time it was loaded,
// the script isn't loaded.
func (engine *ESEngine) LiveLoadFile(path string) error {
	r := make(chan error)
	engine.model.WhenReady(func() {
		r <- engine.loadScriptAndRefresh(path, false)
	})

	return <-r
}

func (engine *ESEngine) LiveRemoveFile(path string) error {
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
		engine.ctx.PushErrorObject(duktape.DUK_ERR_ERROR, err.Error())
		return duktape.DUK_RET_INSTACK_ERROR
	}
	engine.maybeRegisterSourceItem(SOURCE_ITEM_DEVICE, name)
	return 0
}

func (engine *ESEngine) esFormat() int {
	engine.ctx.PushString(engine.ctx.Format())
	return 1
}

func (engine *ESEngine) makeLogFunc(level EngineLogLevel) func() int {
	return func() int {
		engine.Log(level, engine.ctx.Format())
		return 0
	}
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
		n := uint64(engine.ctx.GetNumber(-1))
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
		engine.Log(ENGINE_LOG_ERROR, fmt.Sprintf("bad rule definition"))
		return duktape.DUK_RET_ERROR
	}
	shortName := engine.ctx.GetString(0)
	name := shortName
	if engine.currentSource != nil {
		name = engine.currentSource.VirtualPath + "/" + shortName
	}
	if rule, err := engine.buildRule(name, 1); err != nil {
		// FIXME: proper error handling
		engine.Log(ENGINE_LOG_ERROR,
			fmt.Sprintf("bad definition of rule '%s': %s", name, err))
		return duktape.DUK_RET_ERROR
	} else {
		engine.DefineRule(rule)
		engine.maybeRegisterSourceItem(SOURCE_ITEM_RULE, shortName)
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

func (engine *ESEngine) esReadConfig() int {
	if engine.ctx.GetTop() != 1 || !engine.ctx.IsString(0) {
		engine.Log(ENGINE_LOG_ERROR, fmt.Sprintf("invalid readConfig call"))
		return duktape.DUK_RET_ERROR
	}
	path := engine.ctx.GetString(0)
	in, err := os.Open(path)
	if err != nil {
		engine.Log(ENGINE_LOG_ERROR, fmt.Sprintf("failed to open config file: %s", path))
		return duktape.DUK_RET_ERROR
	}
	defer in.Close()

	reader := JsonConfigReader.New(in)
	preprocessedContent, err := ioutil.ReadAll(reader)
	if err != nil {
		// JsonConfigReader doesn't produce its own errors, thus
		// any errors returned from it are I/O errors.
		engine.Log(ENGINE_LOG_ERROR, fmt.Sprintf("failed to read config file: %s", path))
		return duktape.DUK_RET_ERROR
	}

	parsedJSON, err := objx.FromJSON(string(preprocessedContent))
	if err != nil {
		engine.Log(ENGINE_LOG_ERROR, fmt.Sprintf("failed to parse json: %s", path))
		return duktape.DUK_RET_ERROR
	}
	engine.ctx.PushJSObject(parsedJSON)
	return 1
}

func (engine *ESEngine) EvalScript(code string) error {
	ch := make(chan error)
	engine.model.CallSync(func() {
		err := engine.ctx.EvalScript(code)
		if err != nil {
			engine.Logf(ENGINE_LOG_ERROR, "eval error: %s", err)
		}
		ch <- err
	})
	return <-ch
}

// Persistent storage features

// Create or open DB file
func (engine *ESEngine) SetPersistentDB(filename string) error {
	return engine.SetPersistentDBMode(filename, PERSISTENT_DB_CHMOD)
}

func (engine *ESEngine) SetPersistentDBMode(filename string, mode os.FileMode) (err error) {
	if engine.persistentDB != nil {
		engine.Log(ENGINE_LOG_ERROR, fmt.Sprintf("persistent storage DB is already opened"))
		err = fmt.Errorf("persistent storage DB is already opened")
		return
	}

	engine.persistentDB, err = bolt.Open(filename, mode,
		&bolt.Options{Timeout: 1 * time.Second})

	if err != nil {
		engine.Log(ENGINE_LOG_ERROR, fmt.Sprintf("can't open persistent DB file: %s", err))
		return
	}

	return nil
}

// Force close DB
func (engine *ESEngine) ClosePersistentDB() (err error) {
	if engine.persistentDB == nil {
		engine.Log(ENGINE_LOG_ERROR, fmt.Sprintf("DB is not opened, nothing to close"))
		err = fmt.Errorf("nothing to close")
		return
	}

	err = engine.persistentDB.Close()

	return
}

// Creates a name for persistent storage bucket.
// Used in 'PersistentStorage(name, options)'
func (engine *ESEngine) esPersistentName() int {
	if engine.persistentDB == nil {
		engine.Log(ENGINE_LOG_ERROR, fmt.Sprintf("persistent DB is not initialized"))
		return duktape.DUK_RET_ERROR
	}

	// arguments: (name string[, options = { global bool }])
	var name string
	global := false

	numArgs := engine.ctx.GetTop()

	if numArgs < 1 || numArgs > 2 {
		engine.Log(ENGINE_LOG_ERROR, fmt.Sprintf("bad persistent storage definition"))
		return duktape.DUK_RET_ERROR
	}

	// parse name
	if !engine.ctx.IsString(0) {
		engine.Log(ENGINE_LOG_ERROR, fmt.Sprintf("persistent storage name must be string"))
		return duktape.DUK_RET_ERROR
	}
	name = engine.ctx.GetString(0)

	// parse options object
	if numArgs == 2 && !engine.ctx.IsUndefined(1) {
		if !engine.ctx.IsObject(1) {
			engine.Log(ENGINE_LOG_ERROR, fmt.Sprintf("persistent storage options must be object"))
			return duktape.DUK_RET_ERROR
		}

		if engine.ctx.HasPropString(1, "global") {
			ctx := engine.ctx
			ctx.GetPropString(1, "global")

			if !ctx.IsBoolean(-1) {
				ctx.Pop()
				engine.Log(ENGINE_LOG_ERROR, fmt.Sprintf("persistent storage 'global' option must be bool"))
				return duktape.DUK_RET_ERROR
			}

			global = ctx.GetBoolean(-1)
			ctx.Pop()
		}
	}

	// non-global storages are not supported yet
	// TODO: true files isolation and fileName params for areas
	if !global {
		engine.Log(ENGINE_LOG_ERROR, fmt.Sprintf("Non-global persistent storages are not supported yet; force {global: true} to use it"))
		return duktape.DUK_RET_ERROR
	}

	// push name as return value
	engine.ctx.PushString(name)

	return 1
}

// Writes new value down to persistent DB
func (engine *ESEngine) esPersistentSet() int {
	if engine.persistentDB == nil {
		engine.Log(ENGINE_LOG_ERROR, fmt.Sprintf("persistent DB is not initialized"))
		return duktape.DUK_RET_ERROR
	}

	// arguments: (bucket string, key string, value)
	var bucket, key, value string

	if engine.ctx.GetTop() != 3 {
		engine.Log(ENGINE_LOG_ERROR, fmt.Sprintf("bad persistentSet request, arg number mismatch"))
		return duktape.DUK_RET_ERROR
	}

	// parse bucket name
	if !engine.ctx.IsString(0) {
		engine.Log(ENGINE_LOG_ERROR, fmt.Sprintf("persistent storage bucket name must be string"))
		return duktape.DUK_RET_ERROR
	}
	bucket = engine.ctx.GetString(0)

	// parse key
	if !engine.ctx.IsString(1) {
		engine.Log(ENGINE_LOG_ERROR, fmt.Sprintf("persistent storage key must be string"))
		return duktape.DUK_RET_ERROR
	}
	key = engine.ctx.GetString(1)

	// parse value
	value = engine.ctx.JsonEncode(2)

	// perform a transaction
	engine.persistentDB.Update(func(tx *bolt.Tx) error {
		b, err := tx.CreateBucketIfNotExists([]byte(bucket))
		if err != nil {
			return err
		}

		if err := b.Put([]byte(key), []byte(value)); err != nil {
			return err
		}
		return nil
	})

	return 0
}

// Gets a value from persitent DB
func (engine *ESEngine) esPersistentGet() int {
	if engine.persistentDB == nil {
		engine.Log(ENGINE_LOG_ERROR, fmt.Sprintf("persistent DB is not initialized"))
		return duktape.DUK_RET_ERROR
	}

	// arguments: (bucket string, key string)
	var bucket, key, value string

	if engine.ctx.GetTop() != 2 {
		engine.Log(ENGINE_LOG_ERROR, fmt.Sprintf("bad persistentGet request, arg number mismatch"))
		return duktape.DUK_RET_ERROR
	}

	// parse bucket name
	if !engine.ctx.IsString(0) {
		engine.Log(ENGINE_LOG_ERROR, fmt.Sprintf("persistent storage bucket name must be string"))
		return duktape.DUK_RET_ERROR
	}
	bucket = engine.ctx.GetString(0)

	// parse key
	if !engine.ctx.IsString(1) {
		engine.Log(ENGINE_LOG_ERROR, fmt.Sprintf("persistent storage key must be string"))
		return duktape.DUK_RET_ERROR
	}
	key = engine.ctx.GetString(1)

	// try to get these from cache
	var ok bool
	// read value
	engine.persistentDB.View(func(tx *bolt.Tx) error {
		b := tx.Bucket([]byte(bucket))
		if b == nil { // no such bucket -> undefined
			ok = false
			return nil
		}
		ok = true
		value = string(b.Get([]byte(key)))
		return nil
	})

	if !ok {
		// push 'undefined'
		engine.ctx.PushUndefined()
	} else {
		// push value into stack and decode JSON
		engine.ctx.PushString(value)
		engine.ctx.JsonDecode(-1)
	}

	return 1
}

// native modSearch implementation
func (engine *ESEngine) ModSearch(ctx *duktape.Context) int {
	// arguments:
	// 0: id
	// 1: require
	// 2: exports
	// 3: module

	// get module name (id)
	id := ctx.GetString(0)
	wbgo.Debug.Printf("[modsearch] required module %s", id)

	// try to find this module in directory
	for _, dir := range engine.scriptDirs {
		path := dir + "/" + id + ".js"
		wbgo.Debug.Printf("[modsearch] trying to read file %s", path)

		// TODO: something external to load scripts properly
		// now just try to read file
		src, err := ioutil.ReadFile(path)

		if err == nil {
			wbgo.Debug.Printf("[modsearch] file found!")
			wbgo.Debug.Printf("[modsearch] script file: %s", string(src))
			// TODO: export all stuff
			ctx.PushString(string(src))

			return 1
		}
	}

	wbgo.Warn.Printf("error requiring module %s, not found", id)

	return duktape.DUK_RET_ERROR
}
