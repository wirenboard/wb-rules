package wbrules

import (
	"crypto/md5"
	"encoding/base64"
	"errors"
	"fmt"
	"io/ioutil"
	"log"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/DisposaBoy/JsonConfigReader"
	"github.com/boltdb/bolt"
	"github.com/stretchr/objx"
	duktape "github.com/wirenboard/go-duktape"
	"github.com/wirenboard/wbgong"
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
	SOURCE_ITEM_TIMER

	FILE_DISABLED_SUFFIX = ".disabled"

	MODULE_FILENAME_PROP = "filename"
	MODULE_STATIC_PROP   = "static"

	GLOBAL_OBJ_PROTO_NAME = "__wbGlobalPrototype"
	MODULE_OBJ_PROTO_NAME = "__wbModulePrototype"

	VDEV_OBJ_PROP_DEVID      = "__deviceId"
	VDEV_OBJ_PROP_CELLID     = "__cellId"
	VDEV_OBJ_PROTO_NAME      = "__wbVdevPrototype"
	VDEV_OBJ_PROTO_CELL_NAME = "__wbVdevCellPrototype"

	THREAD_STORAGE_OBJ_NAME       = "_esThreads"
	MODULES_USER_STORAGE_OBJ_NAME = "_esModules"
	GLOBAL_INIT_ENV_FUNC_NAME     = "__esInitEnv"
)

var noSuchPropError = errors.New("no such property")
var wrongPropTypeError = errors.New("wrong property type")

var noLibJs = errors.New("unable to locate lib.js")
var searchDirs = []string{LIB_SYS_PATH}

// cache for quicker filename hashing
var filenameMd5s = make(map[string]string)

type sourceMap map[string]*LocFileEntry

type ESEngineOptions struct {
	*RuleEngineOptions
	PersistentDBFile     string
	PersistentDBFileMode os.FileMode
	ModulesDirs          []string
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

func (o *ESEngineOptions) SetModulesDirs(dirs []string) {
	o.ModulesDirs = dirs
}

type TimerSet struct {
	sync.Mutex
	timers map[TimerId]bool
}

func newTimerSet() *TimerSet {
	return &TimerSet{
		timers: make(map[TimerId]bool),
	}
}

type ESEngine struct {
	*RuleEngine
	ctxFactory *ESContextFactory     // ESContext factory
	globalCtx  *ESContext            // global context - prototype for local contexts in threads
	localCtxs  map[string]*ESContext // local scripts' contexts, mapped from script paths
	ctxTimers  map[*ESContext]*TimerSet

	sourceRoot      string
	sources         map[string]*LocFileEntry // entries for all loaded files, including system files. Keys are abs paths
	editableSources map[string]string        // map from virtual paths to abs paths for editable files
	sourcesMtx      sync.Mutex

	tracker           wbgong.ContentTracker
	persistentDBCache map[string]string
	persistentDB      *bolt.DB
	modulesDirs       []string
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

func NewESEngine(driver wbgong.Driver, logMqttClient wbgong.MQTTClient, options *ESEngineOptions) (engine *ESEngine, err error) {
	if options == nil {
		panic("no options given to NewESEngine")
	}

	engine = &ESEngine{
		RuleEngine:        NewRuleEngine(driver, logMqttClient, options.RuleEngineOptions),
		ctxFactory:        newESContextFactory(),
		localCtxs:         make(map[string]*ESContext),
		ctxTimers:         make(map[*ESContext]*TimerSet),
		sources:           make(map[string]*LocFileEntry),
		editableSources:   make(map[string]string),
		tracker:           wbgong.NewContentTracker(),
		persistentDBCache: make(map[string]string),
		persistentDB:      nil,
		modulesDirs:       options.ModulesDirs,
	}
	engine.globalCtx = engine.ctxFactory.newESContext(engine.MaybeCallSync, "")

	if options.PersistentDBFile != "" {
		if err = engine.SetPersistentDBMode(options.PersistentDBFile,
			options.PersistentDBFileMode); err != nil {
			return
			// panic("error opening persistent DB file: " + err.Error())
		}
		engine.Log(ENGINE_LOG_INFO, fmt.Sprintf("using file %s for persistent DB", options.PersistentDBFile))
	}

	engine.globalCtx.SetCallbackErrorHandler(engine.CallbackErrorHandler)

	// init modSearch for global
	engine.exportModSearch(engine.globalCtx)

	// init __wbModulePrototype
	engine.initModulePrototype(engine.globalCtx)

	// init virtual device prototype
	engine.initVdevPrototype(engine.globalCtx)

	// init virtual device cell prototype
	engine.initVdevCellPrototype(engine.globalCtx)

	// init threads storage
	engine.initGlobalThreadList(engine.globalCtx)

	// init modules storage
	engine.initModulesStorage(engine.globalCtx)

	engine.globalCtx.PushGlobalObject()

	engine.globalCtx.DefineFunctions(map[string]func(*ESContext) int{
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
		"_wbPersistentSet":     engine.esPersistentSet,
		"_wbPersistentGet":     engine.esPersistentGet,
		"disableRule":          engine.esWbDisableRule,
		"enableRule":           engine.esWbEnableRule,
		"runRule":              engine.esWbRunRule,
		"defineVirtualDevice":  engine.esDefineVirtualDevice,
		"getDevice":            engine.esGetDevice,
		"getControl":           engine.esGetControl,
		"_wbPersistentName":    engine.esPersistentName,
		"trackMqtt":            engine.trackMqtt,
	})
	engine.globalCtx.GetPropString(-1, "log")
	engine.globalCtx.DefineFunctions(map[string]func(*ESContext) int{
		"debug":   engine.makeLogFunc(ENGINE_LOG_DEBUG),
		"info":    engine.makeLogFunc(ENGINE_LOG_INFO),
		"warning": engine.makeLogFunc(ENGINE_LOG_WARNING),
		"error":   engine.makeLogFunc(ENGINE_LOG_ERROR),
	})
	engine.globalCtx.Pop()

	// set global prototype to __wbModulePrototype
	engine.globalCtx.GetPropString(-1, MODULE_OBJ_PROTO_NAME)
	engine.globalCtx.SetPrototype(-2)
	// [ global ]

	if err := engine.loadLib(); err != nil {
		wbgong.Error.Panicf("failed to load runtime library: %s", err)
	}

	engine.globalCtx.Pop()
	// []

	// save global object in heap stash as __wbGlobalPrototype
	engine.globalCtx.PushHeapStash()
	engine.globalCtx.PushGlobalObject()
	// [ heap global ]

	engine.globalCtx.PutPropString(-2, GLOBAL_OBJ_PROTO_NAME)
	// [ heap ]

	engine.globalCtx.Pop()
	// []

	return
}

func (engine *ESEngine) exportModSearch(ctx *ESContext) {
	ctx.GetGlobalString("Duktape")
	ctx.PushGoFunc(func(c *duktape.Context) int {
		return engine.ModSearch(c)
	})
	ctx.PutPropString(-2, "modSearch")
	ctx.Pop()
}

func (engine *ESEngine) initHeapStashObject(name string, ctx *ESContext) {
	ctx.PushHeapStash()
	defer ctx.Pop()
	// [ stash ]

	ctx.PushObject()
	// [ stash object ]
	ctx.PutPropString(-2, name)
}

// initGlobalThreadList creates an object in heap stash to
// store thread objects
func (engine *ESEngine) initGlobalThreadList(ctx *ESContext) {
	engine.initHeapStashObject(THREAD_STORAGE_OBJ_NAME, ctx)
}

func (engine *ESEngine) initModulesStorage(ctx *ESContext) {
	engine.initHeapStashObject(MODULES_USER_STORAGE_OBJ_NAME, ctx)
}

func (engine *ESEngine) removeThreadFromStorage(ctx *ESContext, path string) {
	ctx.PushHeapStash()
	// [ stash ]

	ctx.GetPropString(-1, THREAD_STORAGE_OBJ_NAME)
	// [ stash threads ]
	defer ctx.Pop2()

	// try to get thread by name
	if ctx.HasPropString(-1, path) {
		ctx.DelPropString(-1, path)
	} else {
		wbgong.Error.Printf("trying to remove thread %s, but it doesn't exist", path)
	}
}

// initModulePrototype inits __wbModulePrototype object
func (engine *ESEngine) initModulePrototype(ctx *ESContext) {
	ctx.PushGlobalObject()
	defer ctx.Pop()

	ctx.PushObject()
	// [ global __wbModulePrototype ]
	ctx.PutPropString(-2, MODULE_OBJ_PROTO_NAME)
}

// initVdevPrototype inits __wbVdevPrototype object - prototype
// for virtual device controllers
func (engine *ESEngine) initVdevPrototype(ctx *ESContext) {
	ctx.PushGlobalObject()
	defer ctx.Pop()

	ctx.PushObject()
	// [ global __wbVdevPrototype ]
	ctx.DefineFunctions(map[string]func(*ESContext) int{
		"getDeviceId":     engine.esVdevGetDeviceId,
		"getId":           engine.esVdevGetId,
		"getCellId":       engine.esVdevGetCellId,
		"addControl":      engine.esVdevAddControl,
		"getControl":      engine.esVdevGetControl,
		"isControlExists": engine.esVdevControlExists,
		"removeControl":   engine.esVdevRemoveControl,
		"controlsList":    engine.esVdevControlsList,
		"isVirtual":       engine.esVdevIsVirtual,
		// getCellValue and setCellValue are defined in lib.js
	})

	ctx.PutPropString(-2, "__wbVdevPrototype")
}

// initVdevCellPrototype inits __wbVdevCellPrototype object - prototype
// for virtual device cells controllers
func (engine *ESEngine) initVdevCellPrototype(ctx *ESContext) {
	ctx.PushGlobalObject()
	defer ctx.Pop()

	ctx.PushObject()
	// [ global __wbVdevCellPrototype ]
	ctx.DefineFunctions(map[string]func(*ESContext) int{
		"getId":          engine.esVdevCellGetId,
		"setDescription": engine.esVdevCellSetDescription,
		"getDescription": engine.esVdevCellGetDescription,
		"setType":        engine.esVdevCellSetType,
		"getType":        engine.esVdevCellGetType,
		"setUnits":       engine.esVdevCellSetUnits,
		"getUnits":       engine.esVdevCellGetUnits,
		"setReadonly":    engine.esVdevCellSetReadonly,
		"getReadonly":    engine.esVdevCellGetReadonly,
		"setMax":         engine.esVdevCellSetMax,
		"getMax":         engine.esVdevCellGetMax,
		"setError":       engine.esVdevCellSetError,
		"getError":       engine.esVdevCellGetError,
		"setOrder":       engine.esVdevCellSetOrder,
		"getOrder":       engine.esVdevCellGetOrder,
	})

	ctx.PutPropString(-2, "__wbVdevCellPrototype")
}

func (engine *ESEngine) makeControlObject(ctx *ESContext, devID, ctrlID string) {
	ctx.Pop()
	// [ args | ]

	// create virtual device cell object
	ctx.PushObject()
	// [ args | vDevObject ]

	// get prototype

	// get global object first
	ctx.PushGlobalObject()
	// [ args | vDevObject global ]

	// get prototype object
	ctx.GetPropString(-1, VDEV_OBJ_PROTO_CELL_NAME)
	// [ args | vDevObject global __wbVdevPrototype ]

	// apply prototype
	ctx.SetPrototype(-3)
	// [ args | vDevObject global ]

	ctx.Pop()
	// [ args | vDevObject ]

	// push device ID property

	ctx.PushString(ctrlID)
	// [ args | vDevObject cellId ]

	ctx.PutPropString(-2, VDEV_OBJ_PROP_CELLID)
	// [ args | vDevObject ]

	ctx.PushString(devID)
	// [ args | vDevObject devId ]

	ctx.PutPropString(-2, VDEV_OBJ_PROP_DEVID)
	// [ args | vDevObject ]
}

// Engine callback error handler
func (engine *ESEngine) CallbackErrorHandler(err ESError) {
	engine.Log(ENGINE_LOG_ERROR, fmt.Sprintf("ECMAScript error: %s", err))
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

func (engine *ESEngine) handleTimerCleanup(ctx *ESContext, timer TimerId) {
	var s *TimerSet
	var found = false

	// find timers set for current context
	if s, found = engine.ctxTimers[ctx]; !found {
		s = newTimerSet()
		engine.ctxTimers[ctx] = s
	}

	// register timer id
	s.timers[timer] = true

	// register cleanup handler
	engine.OnTimerRemoveByIndex(timer, func() {
		s.Lock()
		defer s.Unlock()
		delete(s.timers, timer)
	})
}

func (engine *ESEngine) runTimerCleanups(ctx *ESContext) {
	if s, found := engine.ctxTimers[ctx]; found {
		var ids = make([]TimerId, 0)

		// form timers list
		s.Lock()
		for id, active := range s.timers {
			if active {
				ids = append(ids, id)
			}
		}
		s.Unlock()

		// run cleanups
		for _, id := range ids {
			engine.StopTimerByIndex(id)
		}
	}
}

func (engine *ESEngine) buildSingleWhenChangedRuleCondition(ctx *ESContext, defIndex int) (RuleCondition, error) {
	if ctx.IsString(defIndex) {
		controlFullId := ctx.SafeToString(defIndex)
		parts := strings.SplitN(controlFullId, "/", 2)
		if len(parts) != 2 {
			return nil, fmt.Errorf("invalid whenChanged spec: '%s'", controlFullId)
		}
		return NewCellChangedRuleCondition(ControlSpec{parts[0], parts[1]})
	}
	if ctx.IsFunction(defIndex) {
		f := ctx.WrapCallback(defIndex)
		return NewFuncValueChangedRuleCondition(func() interface{} { return f(nil) }), nil
	}
	return nil, errors.New("whenChanged: array expected")
}

func (engine *ESEngine) buildWhenChangedRuleCondition(ctx *ESContext, defIndex int) (RuleCondition, error) {
	ctx.GetPropString(defIndex, "whenChanged")
	defer ctx.Pop()

	if !ctx.IsArray(-1) {
		return engine.buildSingleWhenChangedRuleCondition(ctx, -1)
	}

	conds := make([]RuleCondition, ctx.GetLength(-1))

	for i := range conds {
		ctx.GetPropIndex(-1, uint(i))
		cond, err := engine.buildSingleWhenChangedRuleCondition(ctx, -1)
		ctx.Pop()
		if err != nil {
			return nil, err
		} else {
			conds[i] = cond
		}
	}

	return NewOrRuleCondition(conds), nil
}

func (engine *ESEngine) buildRuleCond(ctx *ESContext, defIndex int) (RuleCondition, error) {
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
		return NewLevelTriggeredRuleCondition(engine.wrapRuleCondFunc(ctx, defIndex, "when")), nil

	case hasAsSoonAs && (hasWhenChanged || hasCron):
		return nil, errors.New(
			"invalid rule -- cannot combine 'asSoonAs' with 'whenChanged' or 'cron'")

	case hasAsSoonAs:
		return NewEdgeTriggeredRuleCondition(
			engine.wrapRuleCondFunc(ctx, defIndex, "asSoonAs")), nil

	case hasWhenChanged && hasCron:
		return nil, errors.New("invalid rule -- cannot combine 'whenChanged' with cron spec")

	case hasWhenChanged:
		return engine.buildWhenChangedRuleCondition(ctx, defIndex)

	case hasCron:
		ctx.GetPropString(defIndex, "_cron")
		defer ctx.Pop()
		return NewCronRuleCondition(ctx.SafeToString(-1)), nil

	default:
		return nil, errors.New(
			"invalid rule -- must provide one of 'when', 'asSoonAs' or 'whenChanged'")
	}
}

func (engine *ESEngine) buildRule(ctx *ESContext, name string, defIndex int) (*Rule, error) {
	if !ctx.HasPropString(defIndex, "then") {
		// this should be handled by lib.js
		return nil, errors.New("invalid rule -- no then")
	}
	then := engine.wrapRuleCallback(ctx, defIndex, "then")
	if cond, err := engine.buildRuleCond(ctx, defIndex); err != nil {
		return nil, err
	} else {
		ruleId := engine.nextRuleId
		engine.nextRuleId++

		return NewRule(engine, ruleId, name, cond, then), nil
	}
}

func (engine *ESEngine) loadLib() error {
	for _, dir := range searchDirs {
		path := filepath.Join(dir, LIB_FILE)
		if _, err := os.Stat(path); err == nil {
			return engine.globalCtx.LoadScript(path)
		}
	}
	return noLibJs
}

func (engine *ESEngine) registerSourceItem(ctx *ESContext, typ itemType, name string) {
	currentPath := ctx.GetCurrentFilename()
	if currentPath == "" {
		wbgong.Info.Printf("source item '%s' without script file, don't register it", name)
		return
	}

	currentSource := engine.sources[currentPath]

	if currentSource == nil {
		wbgong.Error.Panicf("Registering source item %d of file %s without entry", typ, currentPath)
	}

	var items *[]LocItem
	switch typ {
	case SOURCE_ITEM_DEVICE:
		items = &currentSource.Devices
	case SOURCE_ITEM_RULE:
		items = &currentSource.Rules
	case SOURCE_ITEM_TIMER:
		items = &currentSource.Timers
	default:
		log.Panicf("bad source item type %d", typ)
	}

	line := -1
	for _, loc := range ctx.GetTraceback() {
		// Here we depend upon the fact that duktape displays
		// unmodified source paths in the backtrace
		if loc.filename == currentPath {
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

	// prepare sorted list of local
	pathList := make([]string, 0, len(engine.editableSources))
	for virtualPath, _ := range engine.editableSources {
		pathList = append(pathList, virtualPath)
	}
	sort.Strings(pathList)

	entries = make([]LocFileEntry, len(pathList))
	for n, virtualPath := range pathList {
		entries[n] = *engine.sources[engine.editableSources[virtualPath]]
		entries[n].Context = nil // don't mess up with Duktape
	}
	return
}

// TODO
func (engine *ESEngine) LocateFile(virtualPath string) (entry LocFileEntry, err error) {
	return
}

// cleanPath is a clean shortest path from root directory to this file
// virtualPath is a relative path for files in the edit directory
// underSourceRoot is true when this file is in the edit directory\
func (engine *ESEngine) checkSourcePath(path string) (cleanPath string, virtualPath string, underSourceRoot bool, enabled bool, err error) {
	path, err = filepath.Abs(path)
	if err != nil {
		return
	}

	enabled = true

	cleanPath = filepath.Clean(path)
	if underSourceRoot = wbgong.IsSubpath(engine.sourceRoot, cleanPath); underSourceRoot {
		virtualPath, err = filepath.Rel(engine.sourceRoot, path)
	}

	// check if file is disabled
	if strings.HasSuffix(virtualPath, FILE_DISABLED_SUFFIX) {
		// cut suffix from virtual path
		// clean path need to stay clean!
		virtualPath = virtualPath[:len(virtualPath)-len(FILE_DISABLED_SUFFIX)]
		enabled = false
	}

	return
}

func (engine *ESEngine) checkVirtualPath(path string) (cleanPath string, virtualPath string, enabled bool, err error) {
	physicalPath := filepath.Join(engine.sourceRoot, filepath.Clean(path))
	cleanPath, virtualPath, underSourceRoot, enabled, err := engine.checkSourcePath(physicalPath)
	if err == nil && !underSourceRoot {
		err = errors.New("path not under source root")
	}
	return
}

func (engine *ESEngine) LoadFile(path string) (err error) {
	return engine.LiveLoadFile(path)
}

// Prepares new context
func (engine *ESEngine) prepareNewContext(path string) (newLocalCtx *ESContext) {
	// prepare threads storage
	engine.globalCtx.PushHeapStash()
	// [ stash ]

	engine.globalCtx.GetPropString(-1, THREAD_STORAGE_OBJ_NAME)
	// [ stash threads ]

	// create new thread and context
	engine.globalCtx.PushThreadNewGlobalenv()
	// [ stash threads thread ]
	newLocalCtx = engine.ctxFactory.newESContextFromDuktape(engine.globalCtx.syncFunc, path, engine.globalCtx.GetContext(-1))
	// [ stash threads thread ]

	engine.localCtxs[path] = newLocalCtx

	// save new thread into storage
	engine.globalCtx.PutPropString(-2, path)
	// [ stash threads ]
	engine.globalCtx.Pop2()
	// []

	// set error handler
	newLocalCtx.SetCallbackErrorHandler(engine.CallbackErrorHandler)

	// setup prototype for global object
	newLocalCtx.PushHeapStash()
	// [ stash ]
	newLocalCtx.PushGlobalObject()
	// [ stash global ]
	newLocalCtx.GetPropString(-2, GLOBAL_OBJ_PROTO_NAME)
	// [ stash global __wbGlobalProto ]

	// run initEnv function from prototype
	if newLocalCtx.HasPropString(-1, GLOBAL_INIT_ENV_FUNC_NAME) {
		newLocalCtx.GetPropString(-1, GLOBAL_INIT_ENV_FUNC_NAME)
		// [ ... initEnv ]
		newLocalCtx.PushGlobalObject()
		// [ ... initEnv global ]
		if newLocalCtx.Pcall(1) != 0 {
			wbgong.Error.Println("Failed to call __esInitEnv")
		}
		// [ ... ret ]
		newLocalCtx.Pop()
		// [ ... ]
	}

	newLocalCtx.SetPrototype(-2)

	// [ stash global ]

	newLocalCtx.Pop2()
	// []

	// export modSearch
	engine.exportModSearch(newLocalCtx)

	return
}

func (engine *ESEngine) loadScript(path string, loadIfUnchanged bool) (bool, error) {
	path, virtualPath, underSourceRoot, enabled, err := engine.checkSourcePath(path)
	if err != nil {
		return false, err
	}

	wasChangedOrFirstSeen, err := engine.tracker.Track(virtualPath, path)
	if err != nil {
		return false, err
	}
	if !loadIfUnchanged && !wasChangedOrFirstSeen {
		wbgong.Debug.Printf("script %s unchanged, not reloading (possibly just reloaded)", path)
		return false, nil
	}

	// cleanup if old script exists
	engine.runCleanups(path)

	engine.cleanup.PushCleanupScope(path)
	defer engine.cleanup.PopCleanupScope(path)

	// create new entry for current file
	currentSource := &LocFileEntry{
		VirtualPath:  virtualPath,
		PhysicalPath: path,
		Devices:      make([]LocItem, 0),
		Rules:        make([]LocItem, 0),
		Timers:       make([]LocItem, 0),
		Enabled:      enabled,
	}

	// if this file is editable, don't forget to save it in editable files map
	// which will be used to form file entries list in RPC
	if underSourceRoot {
		engine.editableSources[virtualPath] = path

		engine.cleanup.AddCleanup(func() {
			engine.sourcesMtx.Lock()
			defer engine.sourcesMtx.Unlock()

			delete(engine.editableSources, virtualPath)
		})
	} else {
		wbgong.Info.Printf("%s is NOT under source root %s", path, engine.sourceRoot)
	}

	// remove file entry from list on cleanup
	engine.cleanup.AddCleanup(func() {
		engine.sourcesMtx.Lock()
		delete(engine.sources, path)
		engine.sourcesMtx.Unlock()
	})

	// add file to sources list
	engine.sourcesMtx.Lock()
	engine.sources[path] = currentSource
	engine.sourcesMtx.Unlock()

	// check if file is disabled, if so - stop here
	if !enabled {
		return false, nil
	}

	// create new context for this file
	newLocalCtx := engine.prepareNewContext(path)
	currentSource.Context = newLocalCtx

	return true, engine.trackESError(path, newLocalCtx.LoadScenario(path))
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
		_, virtualPath, underSourceRoot, _, err :=
			engine.checkSourcePath(esLoc.filename)
		if err == nil && underSourceRoot {
			traceback = append(traceback, LocItem{esLoc.line, virtualPath})
		}
	}

	scriptErr := NewScriptError(esError.Message, traceback)

	// set error in the file entry
	engine.sources[path].Error = &scriptErr

	return scriptErr
}

func (engine *ESEngine) maybePublishUpdate(subtopic, physicalPath string) {
	_, virtualPath, underSourceRoot, _, err := engine.checkSourcePath(physicalPath)
	if err != nil {
		wbgong.Error.Printf("checkSourcePath() failed for %s: %s", physicalPath, err)
	}
	if underSourceRoot {
		engine.Publish("/wbrules/updates/"+subtopic, virtualPath, 1, false)
	}
}

func (engine *ESEngine) runCleanups(path string) {
	// run rules cleanups
	engine.cleanup.RunCleanups(path)

	// run context cleanups
	// try to get local context for this script
	if localCtx, ok := engine.localCtxs[path]; ok {
		wbgong.Debug.Printf("local context for script %s exists; removing it", path)

		// cleanup timers of this context
		engine.runTimerCleanups(engine.localCtxs[path])

		// TODO: launch internal cleanups
		engine.removeThreadFromStorage(engine.globalCtx, path)

		// invalidate local context
		localCtx.invalidate()

		delete(engine.localCtxs, path)
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
	engine.WhenEngineReady(func() {
		wbgong.Debug.Printf("OverwriteScript(%s)", virtualPath)
		cleanPath, virtualPath, _, err := engine.checkVirtualPath(virtualPath)
		wbgong.Debug.Printf("OverwriteScript: %s %s %v", cleanPath, virtualPath, err)
		if err != nil {
			r <- err
			return
		}

		// Make sure directories that contain the script exist
		if strings.Contains(virtualPath, "/") {
			if err = os.MkdirAll(filepath.Dir(cleanPath), 0777); err != nil {
				wbgong.Error.Printf("error making dirs for %s: %s", cleanPath, err)
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
	engine.WhenEngineReady(func() {
		r <- engine.loadScriptAndRefresh(path, false)
	})

	return <-r
}

func (engine *ESEngine) LiveRemoveFile(path string) error {
	wbgong.Info.Printf("LiveRemoveFile: %s", path)
	path, virtualPath, _, _, err := engine.checkSourcePath(path)

	if err != nil {
		return err
	}

	engine.WhenEngineReady(func() {
		engine.tracker.Untrack(virtualPath)
		engine.runCleanups(path)
		engine.Refresh()
		engine.maybePublishUpdate("removed", path)
	})
	return nil
}

func (engine *ESEngine) wrapRuleCallback(ctx *ESContext, defIndex int, propName string) ESCallbackFunc {
	ctx.GetPropString(defIndex, propName)
	defer ctx.Pop()
	return ctx.WrapCallback(-1)
}

func (engine *ESEngine) wrapRuleCondFunc(ctx *ESContext, defIndex int, defProp string) func() bool {
	f := engine.wrapRuleCallback(ctx, defIndex, defProp)
	return func() bool {
		r, ok := f(nil).(bool)
		return ok && r
	}
}

func getFilenameHash(filename string) string {
	if result, ok := filenameMd5s[filename]; ok {
		return result
	} else {
		// TODO: TBD: detect collisions on current configuration?
		hash := md5.Sum([]byte(filename))

		// reduce hash length to 32
		for i := 0; i < md5.Size/4; i++ {
			hash[i] = hash[i] ^ hash[md5.Size/4+i] ^ hash[md5.Size/2+i] ^ hash[md5.Size*3/4+i]
		}

		result = base64.RawURLEncoding.EncodeToString(hash[:md5.Size/4])
		filenameMd5s[filename] = result

		return result
	}
}

// localObjectId generates global-unique object ID
// for local one according to module file name.
// Used in defineVirtualDevice and PersistentStorage
func localObjectId(filename, objname string) string {
	hash := getFilenameHash(filename)
	return "_" + hash + objname
}

// expandLocalObjectId converts local object ID to global.
func (engine *ESEngine) expandLocalObjectId(ctx *ESContext, name string) string {
	filename := ctx.GetCurrentFilename()

	if filename != "" {
		name = localObjectId(filename, name)
	}

	return name
}

// getStringPropFromObject gets string property value from object
func (engine *ESEngine) getStringPropFromObject(ctx *ESContext, objIndex int, propName string) (id string, err error) {
	// [ ... obj ... ]

	if !ctx.HasPropString(objIndex, propName) {
		err = noSuchPropError
		return
	}

	ctx.GetPropString(objIndex, propName)
	defer ctx.Pop()
	// [ ... obj ... prop ]

	id = ctx.GetString(-1)

	if id == "" {
		err = wrongPropTypeError
		return
	}

	return
}

func (engine *ESEngine) esGetDevice(ctx *ESContext) int {
	if ctx.GetTop() != 1 || !ctx.IsString(0) {
		engine.Log(ENGINE_LOG_ERROR, fmt.Sprintf("getDevice(): bad parameters"))
		return duktape.DUK_RET_ERROR
	}

	name := ctx.GetString(0)

	errDevice := engine.GetDevice(name)
	if errDevice != nil {
		engine.Log(ENGINE_LOG_ERROR, fmt.Sprintf("Error in getting device: %s", errDevice))
		ctx.PushUndefined()
		return 1
	}
	// [ args | ]

	// create virtual device object
	ctx.PushObject()
	// [ args | vDevObject ]

	// get prototype

	// get global object first
	ctx.PushGlobalObject()
	// [ args | vDevObject global ]

	// get prototype object
	ctx.GetPropString(-1, VDEV_OBJ_PROTO_NAME)
	// [ args | vDevObject global __wbVdevPrototype ]

	// apply prototype
	ctx.SetPrototype(-3)
	// [ args | vDevObject global ]

	ctx.Pop()
	// [ args | vDevObject ]

	// push device ID property

	ctx.PushString(name)
	// [ args | vDevObject devId ]

	ctx.PutPropString(-2, VDEV_OBJ_PROP_DEVID)
	// [ args | vDevObject ]

	return 1
}

func (engine *ESEngine) esGetControl(ctx *ESContext) int {
	if ctx.GetTop() != 1 || !ctx.IsString(0) {
		engine.Log(ENGINE_LOG_ERROR, fmt.Sprintf("getDevice(): bad parameters"))
		return duktape.DUK_RET_ERROR
	}

	name := ctx.GetString(0)

	ids := strings.Split(name, "/")
	if len(ids) != 2 {
		engine.Log(ENGINE_LOG_ERROR, fmt.Sprintf("getDevice(): bad parameters: should be 'devID/cellID'"))
		return duktape.DUK_RET_ERROR
	}
	devID := ids[0]
	ctrlID := ids[1]

	errDevice := engine.GetDevice(devID)
	if errDevice != nil {
		engine.Log(ENGINE_LOG_ERROR, fmt.Sprintf("Error in getting device: %s", errDevice))
		ctx.PushUndefined()
		return 1
	}
	devProxy := engine.GetDeviceProxy(devID)
	ctrl := devProxy.getControl(ctrlID)
	if ctrl == nil {
		wbgong.Error.Printf("getControl(): no such control '%s'", ctrlID)
		ctx.PushUndefined()
		return 1
	}

	engine.makeControlObject(ctx, devID, ctrlID)

	return 1
}

// defineVirtualDevice creates virtual device object in MQTT
// and returns JS object to control it
func (engine *ESEngine) esDefineVirtualDevice(ctx *ESContext) int {
	if ctx.GetTop() != 2 || !ctx.IsString(0) || !ctx.IsObject(1) {
		return duktape.DUK_RET_ERROR
	}
	name := ctx.GetString(0)
	obj := ctx.GetJSObject(1).(objx.Map)

	if err := engine.DefineVirtualDevice(name, obj); err != nil {
		wbgong.Error.Printf("device definition error: %s", err)
		ctx.PushErrorObject(duktape.DUK_ERR_ERROR, err.Error())
		return duktape.DUK_RET_INSTACK_ERROR
	}
	engine.registerSourceItem(ctx, SOURCE_ITEM_DEVICE, name)

	// [ args | ]

	// create virtual device object
	ctx.PushObject()
	// [ args | vDevObject ]

	// get prototype

	// get global object first
	ctx.PushGlobalObject()
	// [ args | vDevObject global ]

	// get prototype object
	ctx.GetPropString(-1, VDEV_OBJ_PROTO_NAME)
	// [ args | vDevObject global __wbVdevPrototype ]

	// apply prototype
	ctx.SetPrototype(-3)
	// [ args | vDevObject global ]

	ctx.Pop()
	// [ args | vDevObject ]

	// push device ID property

	ctx.PushString(name)
	// [ args | vDevObject devId ]

	ctx.PutPropString(-2, VDEV_OBJ_PROP_DEVID)
	// [ args | vDevObject ]

	return 1
}

func (engine *ESEngine) esVdevIsVirtual(ctx *ESContext) int {
	// push this
	ctx.PushThis()
	// [ cell | this ]

	// get virtual device id
	devId, err := engine.getStringPropFromObject(ctx, -1, VDEV_OBJ_PROP_DEVID)
	if err != nil {
		ctx.Pop()
		// [ cell | ]

		return duktape.DUK_RET_TYPE_ERROR
	}
	ctx.Pop()
	devProxy := engine.GetDeviceProxy(devId)
	isVirtual, errVirtual := devProxy.isVirtual()
	if errVirtual != nil {
		wbgong.Error.Printf("isVirtial(): error in executing fufnction: %s", errVirtual)
		return duktape.DUK_RET_ERROR
	}

	ctx.PushBoolean(isVirtual)

	return 1
}

// esVdevGetDeviceId is deprecated, uses esVdevGetId with error message about deprecation
func (engine *ESEngine) esVdevGetDeviceId(ctx *ESContext) int {
	engine.Log(ENGINE_LOG_WARNING, "getDeviceId() is deprecated and will be removed soon, use getId() instead")
	return engine.esVdevGetId(ctx)
}

// esVdevGetId returns virtual device ID string (for MQTT)
// from virtual device object
// Exported to JS as method of virtual device object
func (engine *ESEngine) esVdevGetId(ctx *ESContext) int {
	// this -> virtual device object
	// no arguments
	if ctx.GetTop() != 0 {
		return duktape.DUK_RET_ERROR
	}

	ctx.PushThis()
	// [ this ]

	// get virtual device id
	devId, err := engine.getStringPropFromObject(ctx, -1, VDEV_OBJ_PROP_DEVID)
	if err != nil {
		ctx.Pop()
		// []

		return duktape.DUK_RET_TYPE_ERROR
	}

	ctx.Pop()
	// []

	// return id
	ctx.PushString(devId)
	// [ id ]

	return 1
}

// esVdevGetCellId returns virtual device cell ID string
// in 'dev/cell' form from virtual device object
// Exported to JS as method of virtual device object
// Arguments:
// * cell -> cell name
func (engine *ESEngine) esVdevGetCellId(ctx *ESContext) int {
	// this -> virtual device object
	// arguments:
	// 1 -> cell
	//
	// [ cell | ]

	if ctx.GetTop() != 1 || !ctx.IsString(-1) {
		return duktape.DUK_RET_ERROR
	}

	cellId := ctx.GetString(-1)

	// push this
	ctx.PushThis()
	// [ cell | this ]

	// get virtual device id
	devId, err := engine.getStringPropFromObject(ctx, -1, VDEV_OBJ_PROP_DEVID)
	if err != nil {
		ctx.Pop()
		// [ cell | ]

		return duktape.DUK_RET_TYPE_ERROR
	}

	ctx.Pop()
	// [ cell | ]

	cellId = devId + "/" + cellId

	ctx.PushString(cellId)
	// [ cell | cellId ]

	return 1
}

func (engine *ESEngine) esVdevRemoveControl(ctx *ESContext) int {
	if !ctx.IsString(0) {
		wbgong.Error.Printf("removeControl(): bad parameters")
		return duktape.DUK_RET_ERROR
	}
	ctrlId := ctx.GetString(0)

	// push this
	ctx.PushThis()
	// [ cell | this ]

	// get virtual device id
	devId, err := engine.getStringPropFromObject(ctx, -1, VDEV_OBJ_PROP_DEVID)
	if err != nil {
		ctx.Pop()
		// [ cell | ]

		return duktape.DUK_RET_TYPE_ERROR
	}

	ctx.Pop()

	errControl := engine.RemoveControl(devId, ctrlId)
	if errControl != nil {
		wbgong.Error.Printf("Error in removing control %s on device %s: %s", ctrlId, devId, errControl)
	}
	return 1
}

func (engine *ESEngine) esVdevControlExists(ctx *ESContext) int {
	if !ctx.IsString(0) {
		wbgong.Error.Printf("isControlExists(): bad parameters")
		return duktape.DUK_RET_ERROR
	}
	ctrlId := ctx.GetString(0)

	// push this
	ctx.PushThis()
	// [ cell | this ]

	// get virtual device id
	devId, err := engine.getStringPropFromObject(ctx, -1, VDEV_OBJ_PROP_DEVID)
	if err != nil {
		ctx.Pop()
		// [ cell | ]

		return duktape.DUK_RET_TYPE_ERROR
	}

	ctx.Pop()

	ctrl := engine.GetDeviceProxy(devId).getControl(ctrlId)
	if ctrl == nil {
		ctx.PushBoolean(false)
	} else {
		ctx.PushBoolean(true)
	}
	return 1
}

func (engine *ESEngine) esVdevGetControl(ctx *ESContext) int {
	if !ctx.IsString(0) {
		wbgong.Error.Printf("getControl(): bad parameters")
		return duktape.DUK_RET_ERROR
	}
	ctrlId := ctx.GetString(0)

	// push this
	ctx.PushThis()
	// [ cell | this ]

	// get virtual device id
	devId, err := engine.getStringPropFromObject(ctx, -1, VDEV_OBJ_PROP_DEVID)
	if err != nil {
		ctx.Pop()
		// [ cell | ]

		return duktape.DUK_RET_TYPE_ERROR
	}
	devProxy := engine.GetDeviceProxy(devId)
	ctrl := devProxy.getControl(ctrlId)
	if ctrl == nil {
		wbgong.Error.Printf("getControl(): no such control '%s'", ctrlId)
		ctx.PushUndefined()
		return 1
	}

	engine.makeControlObject(ctx, devId, ctrlId)

	return 1
}

func (engine *ESEngine) esVdevControlsList(ctx *ESContext) int {
	// push this
	ctx.PushThis()
	// [ cell | this ]

	// get virtual device id
	devId, err := engine.getStringPropFromObject(ctx, -1, VDEV_OBJ_PROP_DEVID)
	if err != nil {
		ctx.Pop()
		// [ cell | ]

		return duktape.DUK_RET_TYPE_ERROR
	}
	devProxy := engine.GetDeviceProxy(devId)
	ctrls := devProxy.controlsList()

	ctx.Pop()
	// [ args | ]
	vIndex := ctx.PushArray()

	for i, ctrl := range ctrls {
		// create virtual device cell object
		ctx.PushObject()
		// [ args | vDevObject ]

		// get prototype

		// get global object first
		ctx.PushGlobalObject()
		// [ args | vDevObject global ]

		// get prototype object
		ctx.GetPropString(-1, VDEV_OBJ_PROTO_CELL_NAME)
		// [ args | vDevObject global __wbVdevPrototype ]

		// apply prototype
		ctx.SetPrototype(-3)
		// [ args | vDevObject global ]

		ctx.Pop()
		// [ args | vDevObject ]

		// push device ID property

		ctx.PushString(ctrl.GetId())
		// [ args | vDevObject cellId ]

		ctx.PutPropString(-2, VDEV_OBJ_PROP_CELLID)
		// [ args | vDevObject ]

		ctx.PushString(devId)
		// [ args | vDevObject devId ]

		ctx.PutPropString(-2, VDEV_OBJ_PROP_DEVID)
		// [ args | vDevObject ]
		ctx.PutPropIndex(vIndex, uint(i))
	}

	return 1
}

func (engine *ESEngine) esVdevAddControl(ctx *ESContext) int {
	if !ctx.IsString(0) || !ctx.IsObject(1) {
		wbgong.Error.Printf("addControl(): bad parameters")
		return duktape.DUK_RET_ERROR
	}
	ctrlId := ctx.GetString(0)
	ctrlDef := ctx.GetJSObject(1).(objx.Map)

	// push this
	ctx.PushThis()
	// [ cell | this ]

	// get virtual device id
	devId, err := engine.getStringPropFromObject(ctx, -1, VDEV_OBJ_PROP_DEVID)
	if err != nil {
		ctx.Pop()
		// [ cell | ]

		return duktape.DUK_RET_TYPE_ERROR
	}

	ctx.Pop()

	errControl := engine.AddControl(devId, ctrlId, ctrlDef)
	if errControl != nil {
		wbgong.Error.Printf("Error in creating control %s on device %s: %s", ctrlId, devId, errControl)
	}
	return 1
}

func (engine *ESEngine) esVdevCellGetDescription(ctx *ESContext) int {
	ctrlProxy, err := engine.getControlFromCtx(ctx)
	if err != 1 {
		return err
	}

	ctx.PushString(ctrlProxy.getControl().GetDescription())

	return 1
}

func (engine *ESEngine) esVdevCellGetType(ctx *ESContext) int {
	ctrlProxy, err := engine.getControlFromCtx(ctx)
	if err != 1 {
		return err
	}

	ctx.PushString(ctrlProxy.getControl().GetType())

	return 1
}

func (engine *ESEngine) esVdevCellGetUnits(ctx *ESContext) int {
	ctrlProxy, err := engine.getControlFromCtx(ctx)
	if err != 1 {
		return err
	}

	ctx.PushString(ctrlProxy.getControl().GetUnits())

	return 1
}

func (engine *ESEngine) esVdevCellGetReadonly(ctx *ESContext) int {
	ctrlProxy, err := engine.getControlFromCtx(ctx)
	if err != 1 {
		return err
	}

	ctx.PushBoolean(ctrlProxy.getControl().GetReadonly())

	return 1
}

func (engine *ESEngine) esVdevCellGetMax(ctx *ESContext) int {
	ctrlProxy, err := engine.getControlFromCtx(ctx)
	if err != 1 {
		return err
	}

	ctx.PushInt(ctrlProxy.getControl().GetMax())

	return 1
}

func (engine *ESEngine) esVdevCellGetError(ctx *ESContext) int {
	ctrlProxy, err := engine.getControlFromCtx(ctx)
	if err != 1 {
		return err
	}
	var errString string
	if ctrlProxy.getControl().GetError() != nil {
		errString = ctrlProxy.getControl().GetError().Error()
	}
	ctx.PushString(errString)
	return 1
}

func (engine *ESEngine) esVdevCellGetOrder(ctx *ESContext) int {
	ctrlProxy, err := engine.getControlFromCtx(ctx)
	if err != 1 {
		return err
	}

	ctx.PushInt(ctrlProxy.getControl().GetOrder())

	return 1
}

func (engine *ESEngine) esVdevCellGetId(ctx *ESContext) int {
	ctrlProxy, err := engine.getControlFromCtx(ctx)
	if err != 1 {
		return err
	}

	ctx.PushString(ctrlProxy.getControl().GetId())

	return 1
}

func (engine *ESEngine) esVdevCellSetDescription(ctx *ESContext) int {
	if !ctx.IsString(0) {
		wbgong.Error.Printf("setDescription(): bad parameters")
		return duktape.DUK_RET_ERROR
	}
	descr := ctx.GetString(0)

	ctrlProxy, errCtrl := engine.getControlFromCtx(ctx)
	if errCtrl != 1 {
		return errCtrl
	}

	ctrlProxy.SetMeta(wbgong.CONV_META_SUBTOPIC_DESCRIPTION, descr)
	return 1
}

func (engine *ESEngine) esVdevCellSetType(ctx *ESContext) int {
	if !ctx.IsString(0) {
		wbgong.Error.Printf("setType(): bad parameters")
		return duktape.DUK_RET_ERROR
	}
	typeStr := ctx.GetString(0)

	ctrlProxy, errCtrl := engine.getControlFromCtx(ctx)
	if errCtrl != 1 {
		return errCtrl
	}

	ctrlProxy.SetMeta(wbgong.CONV_META_SUBTOPIC_TYPE, typeStr)

	return 1
}

func (engine *ESEngine) esVdevCellSetUnits(ctx *ESContext) int {
	if !ctx.IsString(0) {
		wbgong.Error.Printf("setUnits(): bad parameters")
		return duktape.DUK_RET_ERROR
	}
	unitsStr := ctx.GetString(0)

	ctrlProxy, errCtrl := engine.getControlFromCtx(ctx)
	if errCtrl != 1 {
		return errCtrl
	}

	ctrlProxy.SetMeta(wbgong.CONV_META_SUBTOPIC_UNITS, unitsStr)

	return 1
}

func (engine *ESEngine) esVdevCellSetReadonly(ctx *ESContext) int {
	if !ctx.IsBoolean(0) {
		wbgong.Error.Printf("setReadonly(): bad parameters")
		return duktape.DUK_RET_ERROR
	}
	readonly := ctx.GetBoolean(0)

	ctrlProxy, errCtrl := engine.getControlFromCtx(ctx)
	if errCtrl != 1 {
		return errCtrl
	}

	readonlyStr := wbgong.CONV_META_BOOL_FALSE
	if readonly {
		readonlyStr = wbgong.CONV_META_BOOL_TRUE
	}

	ctrlProxy.SetMeta(wbgong.CONV_META_SUBTOPIC_READONLY, readonlyStr)

	return 1
}

func (engine *ESEngine) esVdevCellSetMax(ctx *ESContext) int {
	if !ctx.IsNumber(0) {
		wbgong.Error.Printf("setMax(): bad parameters")
		return duktape.DUK_RET_ERROR
	}
	max := int(ctx.GetNumber(0))

	ctrlProxy, errCtrl := engine.getControlFromCtx(ctx)
	if errCtrl != 1 {
		return errCtrl
	}

	ctrlProxy.SetMeta(wbgong.CONV_META_SUBTOPIC_MAX, fmt.Sprintf("%d", max))

	return 1
}

func (engine *ESEngine) esVdevCellSetError(ctx *ESContext) int {
	if !ctx.IsString(0) {
		wbgong.Error.Printf("setError(): bad parameters")
		return duktape.DUK_RET_ERROR
	}
	errorStr := ctx.GetString(0)

	ctrlProxy, errCtrl := engine.getControlFromCtx(ctx)
	if errCtrl != 1 {
		return errCtrl
	}

	ctrlProxy.SetMeta(wbgong.CONV_META_SUBTOPIC_ERROR, errorStr)

	return 1
}

func (engine *ESEngine) esVdevCellSetOrder(ctx *ESContext) int {
	if !ctx.IsNumber(0) {
		wbgong.Error.Printf("setOrder(): bad parameters")
		return duktape.DUK_RET_ERROR
	}
	order := int(ctx.GetNumber(0))

	ctrlProxy, errCtrl := engine.getControlFromCtx(ctx)
	if errCtrl != 1 {
		return errCtrl
	}

	ctrlProxy.SetMeta(wbgong.CONV_META_SUBTOPIC_ORDER, fmt.Sprintf("%d", order))

	return 1
}

func (engine *ESEngine) getControlFromCtx(ctx *ESContext) (*ControlProxy, int) {
	// push this
	ctx.PushThis()
	// [ cell | this ]

	// get virtual device id
	devID, err := engine.getStringPropFromObject(ctx, -1, VDEV_OBJ_PROP_DEVID)
	if err != nil {
		ctx.Pop()
		// [ cell | ]

		return nil, duktape.DUK_RET_TYPE_ERROR
	}

	ctrlID, err := engine.getStringPropFromObject(ctx, -1, VDEV_OBJ_PROP_CELLID)
	if err != nil {
		ctx.Pop()
		// [ cell | ]

		return nil, duktape.DUK_RET_TYPE_ERROR
	}
	ctx.Pop()
	ctrl := engine.GetDeviceProxy(devID).EnsureControlProxy(ctrlID)
	if ctrl.control == nil {
		wbgong.Error.Printf("Control %s/%s not found", devID, ctrlID)
		return nil, duktape.DUK_RET_ERROR
	}
	return ctrl, 1
}

func (engine *ESEngine) esFormat(ctx *ESContext) int {
	ctx.PushString(ctx.Format())
	return 1
}

func (engine *ESEngine) makeLogFunc(level EngineLogLevel) func(ctx *ESContext) int {
	return func(ctx *ESContext) int {
		engine.Log(level, ctx.Format())
		return 0
	}
}

func (engine *ESEngine) esPublish(ctx *ESContext) int {
	retain := false
	qos := 0
	if ctx.GetTop() == 4 {
		retain = ctx.ToBoolean(-1)
		ctx.Pop()
	}
	if ctx.GetTop() == 3 {
		qos = int(ctx.ToNumber(-1))
		ctx.Pop()
		if qos < 0 || qos > 2 {
			return duktape.DUK_RET_ERROR
		}
	}
	if ctx.GetTop() != 2 {
		return duktape.DUK_RET_ERROR
	}
	if !ctx.IsString(-2) {
		return duktape.DUK_RET_TYPE_ERROR
	}
	topic := ctx.GetString(-2)
	payload := ctx.SafeToString(-1)
	engine.Publish(topic, payload, byte(qos), retain)
	return 0
}

func (engine *ESEngine) esWbDevObject(ctx *ESContext) int {
	if wbgong.DebuggingEnabled() {
		wbgong.Debug.Printf("esWbDevObject(): top=%d isString=%v", ctx.GetTop(), ctx.IsString(-1))
	}
	if ctx.GetTop() != 1 || !ctx.IsString(-1) {
		return duktape.DUK_RET_ERROR
	}
	devProxy := engine.GetDeviceProxy(ctx.GetString(-1))
	ctx.PushGoObject(devProxy)
	return 1
}

func (engine *ESEngine) esWbCellObject(ctx *ESContext) int {
	if ctx.GetTop() != 2 || !ctx.IsString(-1) || !ctx.IsObject(-2) {
		return duktape.DUK_RET_ERROR
	}
	devProxy, ok := ctx.GetGoObject(-2).(*DeviceProxy)
	if !ok {
		wbgong.Error.Printf("invalid _wbCellObject call")
		return duktape.DUK_RET_TYPE_ERROR
	}

	controlProxy := devProxy.EnsureControlProxy(ctx.GetString(-1))
	ctx.PushGoObject(controlProxy)
	ctx.DefineFunctions(map[string]func(*ESContext) int{
		JS_DEVPROXY_FUNC_RAWVALUE: func(ctx *ESContext) int {
			ctx.PushThis()
			c := ctx.GetGoObject(-1).(*ControlProxy)
			ctx.Pop()

			ctx.PushString(c.RawValue())
			return 1
		},
		JS_DEVPROXY_FUNC_VALUE: func(ctx *ESContext) int {
			ctx.PushThis()
			c := ctx.GetGoObject(-1).(*ControlProxy)
			ctx.Pop()

			m := objx.New(map[string]interface{}{
				JS_DEVPROXY_FUNC_VALUE_RET: c.Value(),
			})
			ctx.PushJSObject(m)
			return 1
		},
		JS_DEVPROXY_FUNC_SETVALUE: func(ctx *ESContext) int {
			ctx.PushThis()
			c := ctx.GetGoObject(-1).(*ControlProxy)
			ctx.Pop()

			if ctx.GetTop() != 1 || !ctx.IsObject(-1) {
				return duktape.DUK_RET_ERROR
			}
			m, ok := ctx.GetJSObject(-1).(objx.Map)
			if !ok || !m.Has(JS_DEVPROXY_FUNC_SETVALUE_ARG) {
				wbgong.Error.Printf("invalid control definition")
				return duktape.DUK_RET_TYPE_ERROR
			}
			c.SetValue(m[JS_DEVPROXY_FUNC_SETVALUE_ARG])
			return 1
		},
		JS_DEVPROXY_FUNC_SETMETA: func(ctx *ESContext) int {
			ctx.PushThis()
			c := ctx.GetGoObject(-1).(*ControlProxy)
			ctx.Pop()

			if ctx.GetTop() != 1 || !ctx.IsObject(-1) {
				return duktape.DUK_RET_ERROR
			}
			m, ok := ctx.GetJSObject(-1).(objx.Map)
			if !ok || !m.Has(JS_DEVPROXY_FUNC_SETVALUE_ARG) {
				wbgong.Error.Printf("invalid control definition")
				return duktape.DUK_RET_TYPE_ERROR
			}
			key := fmt.Sprintf("%v", m[JS_DEVPROXY_FUNC_SETVALUE_KEY])
			value := fmt.Sprintf("%v", m[JS_DEVPROXY_FUNC_SETVALUE_ARG])
			cce := c.SetMeta(key, value)
			if cce != nil {
				engine.PushToEventBuffer(cce)
			}
			return 1
		},
		JS_DEVPROXY_FUNC_ISCOMPLETE: func(ctx *ESContext) int {
			ctx.PushThis()
			c := ctx.GetGoObject(-1).(*ControlProxy)
			ctx.Pop()

			ctx.PushBoolean(c.IsComplete())
			return 1
		},
		JS_DEVPROXY_FUNC_GETMETA: func(ctx *ESContext) int {
			ctx.PushThis()
			c := ctx.GetGoObject(-1).(*ControlProxy)
			ctx.Pop()

			ctrlMeta := c.GetMeta()
			if ctrlMeta == nil {
				ctx.PushNull()
				return 1
			}

			dataMap := make(map[string]interface{})
			for key, value := range ctrlMeta {
				dataMap[key] = value
			}
			m := objx.New(dataMap)
			ctx.PushJSObject(m)

			return 1
		},
	})
	return 1
}

func (engine *ESEngine) esWbStartTimer(ctx *ESContext) int {
	if ctx.GetTop() != 3 || !ctx.IsNumber(1) {
		// FIXME: need to throw proper exception here
		wbgong.Error.Println("bad _wbStartTimer call")
		return duktape.DUK_RET_ERROR
	}

	name := NO_TIMER_NAME
	if ctx.IsString(0) {
		name = ctx.ToString(0)
		if name == "" {
			wbgong.Error.Println("empty timer name")
			return duktape.DUK_RET_ERROR
		}
		engine.StopTimerByName(name)
	} else if !ctx.IsFunction(0) {
		wbgong.Error.Println("invalid timer spec")
		return duktape.DUK_RET_ERROR
	}

	ms := ctx.GetNumber(1)
	if ms < MIN_INTERVAL_MS {
		ms = MIN_INTERVAL_MS
	}
	periodic := ctx.ToBoolean(2)

	var callback func()
	if name == NO_TIMER_NAME {
		f := ctx.WrapCallback(0)
		callback = func() { f(nil) }
	}

	interval := time.Duration(ms * float64(time.Millisecond))

	// get timer id
	timerId := engine.StartTimer(name, callback, interval, periodic)

	// add timer to script cleanup
	engine.handleTimerCleanup(ctx, timerId)

	ctx.PushNumber(float64(timerId))
	return 1
}

func (engine *ESEngine) esWbStopTimer(ctx *ESContext) int {
	if ctx.GetTop() != 1 {
		return duktape.DUK_RET_ERROR
	}
	if ctx.IsNumber(0) {
		n := TimerId(ctx.GetNumber(-1))
		if n == 0 {
			wbgong.Error.Printf("timer id cannot be zero")
			return 0
		}
		engine.StopTimerByIndex(n)
	} else if ctx.IsString(0) {
		engine.StopTimerByName(ctx.ToString(0))
	} else {
		return duktape.DUK_RET_ERROR
	}
	return 0
}

func (engine *ESEngine) esWbCheckCurrentTimer(ctx *ESContext) int {
	if ctx.GetTop() != 1 || !ctx.IsString(0) {
		return duktape.DUK_RET_ERROR
	}
	timerName := ctx.ToString(0)
	ctx.PushBoolean(engine.CheckTimer(timerName))
	return 1
}

func (engine *ESEngine) esWbSpawn(ctx *ESContext) int {
	if ctx.GetTop() != 5 || !ctx.IsArray(0) || !ctx.IsBoolean(2) ||
		!ctx.IsBoolean(3) {
		return duktape.DUK_RET_ERROR
	}

	args := ctx.StringArrayToGo(0)
	if len(args) == 0 {
		return duktape.DUK_RET_ERROR
	}

	callbackFn := ESCallbackFunc(nil)

	if ctx.IsFunction(1) {
		callbackFn = ctx.WrapCallback(1)
	} else if !ctx.IsNullOrUndefined(1) {
		return duktape.DUK_RET_ERROR
	}

	var input *string
	if ctx.IsString(4) {
		instr := ctx.GetString(4)
		input = &instr
	} else if !ctx.IsNullOrUndefined(4) {
		return duktape.DUK_RET_ERROR
	}

	captureOutput := ctx.GetBoolean(2)
	captureErrorOutput := ctx.GetBoolean(3)
	go func() {
		r, err := Spawn(args[0], args[1:], captureOutput, captureErrorOutput, input)
		if err != nil {
			wbgong.Error.Printf("external command failed: %s", err)
			return
		}
		if callbackFn != nil {
			engine.CallSync(func() {
				// check that context is still alive
				// (file is not removed or reloaded)
				if !ctx.IsValid() {
					wbgong.Info.Println("ignore runShellCommand callback without Duktape context (maybe script is reloaded or removed)")
					return
				}

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
			wbgong.Error.Printf("command '%s' failed with exit status %d",
				strings.Join(args, " "), r.ExitStatus)
		}
	}()
	return 0
}

func (engine *ESEngine) esWbDefineRule(ctx *ESContext) int {
	var ok = false
	var name string
	var objIndex int

	currentFilename := ctx.GetCurrentFilename()

	switch ctx.GetTop() {
	case 1:
		if ctx.IsObject(0) {
			objIndex = 0
			ok = true
		}
	case 2:
		if ctx.IsString(0) && ctx.IsObject(1) {
			objIndex = 1
			name = ctx.GetString(0)
			ok = true
		}
	}
	if !ok {
		engine.Log(ENGINE_LOG_ERROR, fmt.Sprintf("bad rule definition"))
		return duktape.DUK_RET_ERROR
	}

	var rule *Rule
	var err error
	var ruleId RuleId

	// configure cleanup scope
	if currentFilename != "" {
		engine.cleanup.PushCleanupScope(currentFilename)
		defer engine.cleanup.PopCleanupScope(currentFilename)
	}

	if rule, err = engine.buildRule(ctx, name, objIndex); err != nil {
		// FIXME: proper error handling
		engine.Log(ENGINE_LOG_ERROR,
			fmt.Sprintf("bad definition of rule '%s': %s", name, err))
		return duktape.DUK_RET_ERROR
	}

	if ruleId, err = engine.DefineRule(rule, ctx); err != nil {
		engine.Log(ENGINE_LOG_ERROR,
			fmt.Sprintf("defineRule error: %s", err))
		return duktape.DUK_RET_ERROR
	}

	engine.registerSourceItem(ctx, SOURCE_ITEM_RULE, name)

	// return rule ID
	ctx.PushNumber(float64(ruleId))
	return 1
}

func (engine *ESEngine) trackMqtt(ctx *ESContext) int {
	if !(ctx.IsString(0) && ctx.IsFunction(1)) {
		engine.Log(ENGINE_LOG_ERROR, fmt.Sprintf("bad track definition"))
		return duktape.DUK_RET_ERROR
	}
	topic := ctx.GetString(0)

	currentFilename := ctx.GetCurrentFilename()
	if currentFilename != "" {
		engine.cleanup.PushCleanupScope(currentFilename)
		defer engine.cleanup.PopCleanupScope(currentFilename)
	}

	engine.DefineMqttTracker(topic, ctx)

	return 1
}

func (engine *ESEngine) esWbRunRules(ctx *ESContext) int {
	switch ctx.GetTop() {
	case 0:
		engine.RunRules(nil, NO_TIMER_NAME)
	case 2:
		devId := ctx.SafeToString(0)
		ctrlId := ctx.SafeToString(1)
		e := &ControlChangeEvent{
			Spec: ControlSpec{devId, ctrlId},
		}
		engine.RunRules(e, NO_TIMER_NAME)
	default:
		return duktape.DUK_RET_ERROR
	}
	return 0
}

// esWbDisableRule prevents rule from runnning (from JS)
//
// Arguments:
// 1 - ruleId
func (engine *ESEngine) esWbDisableRule(ctx *ESContext) int {
	return engine.esWbCtrlRule(ctx, false)
}

// esWbEnableRule enables rule (from JS)
//
// Arguments:
// 1 - ruleId
func (engine *ESEngine) esWbEnableRule(ctx *ESContext) int {
	return engine.esWbCtrlRule(ctx, true)
}

func (engine *ESEngine) esWbCtrlRule(ctx *ESContext, state bool) int {
	act := "disable"
	if state {
		act = "enable"
	}

	if ctx.GetTop() != 1 || !ctx.IsNumber(0) {
		engine.Log(ENGINE_LOG_ERROR, fmt.Sprintf("invalid %sRule call", act))
		return duktape.DUK_RET_ERROR
	}

	ruleId := RuleId(ctx.GetInt(0))

	if rule, found := engine.ruleMap[ruleId]; found {
		rule.enabled = state
	} else {
		engine.Log(ENGINE_LOG_ERROR, fmt.Sprintf("trying to %s undefined rule: %d", act, ruleId))
		return duktape.DUK_RET_ERROR
	}

	if wbgong.DebuggingEnabled() {
		wbgong.Debug.Printf("[ruleengine] %sRule(ruleId=%d)", act, ruleId)
	}

	return 0
}

// esWbRunRule force runs rule 'then' function from JS
func (engine *ESEngine) esWbRunRule(ctx *ESContext) int {
	if ctx.GetTop() != 1 || !ctx.IsNumber(0) {
		engine.Log(ENGINE_LOG_ERROR, fmt.Sprintf("invalid runRule call"))
		return duktape.DUK_RET_ERROR
	}

	ruleId := RuleId(ctx.GetInt(0))

	if rule, found := engine.ruleMap[ruleId]; found {
		rule.then(nil)
	} else {
		engine.Log(ENGINE_LOG_ERROR, fmt.Sprintf("trying to call runRule for undefined rule: %d", ruleId))
		return duktape.DUK_RET_ERROR
	}

	return 0
}

func (engine *ESEngine) esReadConfig(ctx *ESContext) int {
	if ctx.GetTop() != 1 || !ctx.IsString(0) {
		engine.Log(ENGINE_LOG_ERROR, fmt.Sprintf("invalid readConfig call"))
		return duktape.DUK_RET_ERROR
	}
	path := ctx.GetString(0)
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
	ctx.PushJSObject(parsedJSON)
	return 1
}

func (engine *ESEngine) EvalScript(code string) error {
	ch := make(chan error)
	engine.CallSync(func() {
		err := engine.globalCtx.EvalScript(code)
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
func (engine *ESEngine) esPersistentName(ctx *ESContext) int {

	if engine.persistentDB == nil {
		engine.Log(ENGINE_LOG_ERROR, fmt.Sprintf("persistent DB is not initialized"))
		return duktape.DUK_RET_ERROR
	}

	// arguments: (name [, options = { global bool }])
	var name string
	var global bool

	numArgs := ctx.GetTop()

	if numArgs < 1 || numArgs > 2 {
		engine.Log(ENGINE_LOG_ERROR, fmt.Sprintf("bad persistent storage definition"))
		return duktape.DUK_RET_ERROR
	}

	// parse name
	if !ctx.IsString(0) {
		engine.Log(ENGINE_LOG_ERROR, fmt.Sprintf("persistent storage name must be string"))
		return duktape.DUK_RET_ERROR
	}
	name = ctx.GetString(0)

	// parse options object
	if numArgs == 2 && !ctx.IsUndefined(1) {
		if !ctx.IsObject(1) {
			engine.Log(ENGINE_LOG_ERROR, fmt.Sprintf("persistent storage options must be object"))
			return duktape.DUK_RET_ERROR
		}

		ctx.GetPropString(1, "global")
		global = ctx.GetBoolean(-1)
		ctx.Pop()
	}

	if global {

	} else {
		// get global ID for bucket if this is local storage
		name = engine.expandLocalObjectId(ctx, name)
		engine.Log(ENGINE_LOG_INFO, fmt.Sprintf("create local storage name: %s", name))
	}

	// push name as return value
	ctx.PushString(name)

	return 1
}

// Writes new value down to persistent DB
func (engine *ESEngine) esPersistentSet(ctx *ESContext) int {
	if engine.persistentDB == nil {
		engine.Log(ENGINE_LOG_ERROR, fmt.Sprintf("persistent DB is not initialized"))
		return duktape.DUK_RET_ERROR
	}

	// arguments: (bucket string, key string, value)
	var bucket, key, value string

	if ctx.GetTop() != 3 {
		engine.Log(ENGINE_LOG_ERROR, fmt.Sprintf("bad persistentSet request, arg number mismatch"))
		return duktape.DUK_RET_ERROR
	}

	// parse bucket name
	if !ctx.IsString(0) {
		engine.Log(ENGINE_LOG_ERROR, fmt.Sprintf("persistent storage bucket name must be string"))
		return duktape.DUK_RET_ERROR
	}
	bucket = ctx.GetString(0)

	// parse key
	if !ctx.IsString(1) {
		engine.Log(ENGINE_LOG_ERROR, fmt.Sprintf("persistent storage key must be string"))
		return duktape.DUK_RET_ERROR
	}
	key = ctx.GetString(1)

	// parse value
	value = ctx.JsonEncode(2)

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

	wbgong.Debug.Printf("write value to persistent storage %s: '%s' <= '%s'", bucket, key, value)

	return 0
}

// Gets a value from persitent DB
func (engine *ESEngine) esPersistentGet(ctx *ESContext) int {
	if engine.persistentDB == nil {
		engine.Log(ENGINE_LOG_ERROR, fmt.Sprintf("persistent DB is not initialized"))
		return duktape.DUK_RET_ERROR
	}

	// arguments: (bucket string, key string)
	var bucket, key, value string

	if ctx.GetTop() != 2 {
		engine.Log(ENGINE_LOG_ERROR, fmt.Sprintf("bad persistentGet request, arg number mismatch"))
		return duktape.DUK_RET_ERROR
	}

	// parse bucket name
	if !ctx.IsString(0) {
		engine.Log(ENGINE_LOG_ERROR, fmt.Sprintf("persistent storage bucket name must be string"))
		return duktape.DUK_RET_ERROR
	}
	bucket = ctx.GetString(0)

	// parse key
	if !ctx.IsString(1) {
		engine.Log(ENGINE_LOG_ERROR, fmt.Sprintf("persistent storage key must be string"))
		return duktape.DUK_RET_ERROR
	}
	key = ctx.GetString(1)

	wbgong.Debug.Printf("trying to get value from persistent storage %s: %s", bucket, key)

	// try to get these from cache
	var ok bool
	// read value
	engine.persistentDB.View(func(tx *bolt.Tx) error {
		ok = false
		b := tx.Bucket([]byte(bucket))
		if b == nil { // no such bucket -> undefined
			return nil
		}
		if v := b.Get([]byte(key)); v != nil {
			value = string(v)
			ok = true
		}
		return nil
	})

	if !ok {
		// push 'undefined'
		ctx.PushUndefined()
	} else {
		// push value into stack and decode JSON
		ctx.PushString(value)
		ctx.JsonDecode(-1)
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
	wbgong.Debug.Printf("[modsearch] required module %s", id)

	// try to find this module in directory
	for _, dir := range engine.modulesDirs {
		path := dir + "/" + id + ".js"
		wbgong.Debug.Printf("[modsearch] trying to read file %s", path)

		// TBD: something external to load scripts properly
		// now just try to read file
		src, err := ioutil.ReadFile(path)

		if err == nil {
			wbgong.Debug.Printf("[modsearch] file found!")

			// set module properties
			// put module.filename
			ctx.PushString(path)
			// [ args | path ]
			ctx.PutPropString(3, MODULE_FILENAME_PROP)
			// [ args | ]

			// put module.storage
			ctx.PushHeapStash()
			// [ args | heapStash ]
			ctx.GetPropString(-1, MODULES_USER_STORAGE_OBJ_NAME)
			// [ args | heapStash _esModules ]

			// check if storage for this module is allocated
			if !ctx.HasPropString(-1, path) {
				// create storage
				ctx.PushObject()
				// [ args | heapStash _esModules newStorage ]
				ctx.PutPropString(-2, path)
				// [ args | heapStash _esModules ]
			}
			// add this storage to module
			ctx.GetPropString(-1, path)
			// [ args | heapStash _esModules storage ]
			ctx.PutPropString(3, MODULE_STATIC_PROP)
			// [ args | heapStash _esModules ]
			ctx.Pop2()
			// [ args | ]

			// return module sources
			ctx.PushString(string(src))

			return 1
		}
	}

	wbgong.Error.Printf("error requiring module %s, not found", id)

	return duktape.DUK_RET_ERROR
}
