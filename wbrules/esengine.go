package wbrules

import (
	"bytes"
	"crypto/md5"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/DisposaBoy/JsonConfigReader"
	"github.com/stretchr/objx"
	duktape "github.com/wirenboard/go-duktape"
	"github.com/wirenboard/wbgong"
	bolt "go.etcd.io/bbolt"
)

type itemType int

// Editor.Check statuses. Kept in their own const block: the main block
// below derives itemType values from iota, which counts every preceding
// spec in the same block.
const (
	TS_CHECK_READY       = "ready"
	TS_CHECK_PENDING     = "pending"
	TS_CHECK_NOT_TS      = "not-ts"
	TS_CHECK_UNSUPPORTED = "unsupported"
)

const (
	LIB_FILE        = "lib.js"
	LIB_SYS_PATH    = "/usr/share/wb-rules-system/scripts"
	LIB_REL_PATH_1  = "scripts"
	LIB_REL_PATH_2  = "../scripts"
	MIN_INTERVAL_MS = 1
	// Max wall time for one synchronous JS run before the engine interrupts
	// it. A runaway rule (while(true), while(sleep(x)), ...) blocks the whole
	// single-threaded engine for this long - and takes the Editor RPC with it
	// - so keep it short; legitimate rule work is event-driven and quick.
	DEFAULT_JS_EXECUTION_LIMIT = 10 * time.Second
	// Cap on the shared JS heap. A runaway that allocates (e.g. a tight loop
	// building objects) throws out-of-memory here instead of growing until the
	// process is OOM-killed. 0 disables. Rules normally use a few MB.
	DEFAULT_JS_MEMORY_LIMIT       = 512 * 1024 * 1024
	MIN_INTERVAL_LOW_THRESHOLD_MS = 10
	PERSISTENT_DB_CHMOD           = 0640
	SOURCE_ITEM_DEVICE            = itemType(iota)
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
	CELL_OBJ_PROTO_NAME           = "_esCellPrototype"
	GLOBAL_INIT_ENV_FUNC_NAME     = "__esInitEnv"
)

var (
	noSuchPropError    = errors.New("no such property")
	wrongPropTypeError = errors.New("wrong property type")
	noLibJs            = errors.New("unable to locate lib.js")
)

var searchDirs = []string{LIB_SYS_PATH}

// cache for quicker filename hashing
var filenameMd5s = make(map[string]string)

// ReadConfigFunc reads the file readConfig() was asked for; nil means the
// real os.ReadFile.
type ReadConfigFunc func(path string) ([]byte, error)

type ESEngineOptions struct {
	*RuleEngineOptions
	PersistentDBFile     string
	PersistentDBFileMode os.FileMode
	// PersistentDBOpenTimeout bounds how long opening the persistent DB may
	// wait for the bolt file lock; 0 = the production default (1 s). Tests
	// raise it: on a loaded CI builder a just-closed engine's lock can
	// outlive the 1-second default and fail the next engine's construction.
	PersistentDBOpenTimeout time.Duration
	ModulesDirs             []string
	TsgoPath                string         // tsgo binary for TypeScript rules ("" = TS disabled)
	TsTypesPath             string         // wb-rules.d.ts used during async type checks
	JsExecutionLimit        time.Duration  // max wall time for one synchronous JS run; 0 = unlimited
	SpawnFunc               SpawnFunc      // runs spawn()/runShellCommand(); nil = the real Spawn
	ReadConfigFunc          ReadConfigFunc // reads files for readConfig(); nil = os.ReadFile
	JsMemoryLimit           int64          // max bytes for the shared JS heap; 0 = unlimited
	LoadGuardDir            string         // dir for load-crash guard state; empty disables the guard
}

func NewESEngineOptions() *ESEngineOptions {
	return &ESEngineOptions{
		RuleEngineOptions:    NewRuleEngineOptions(),
		PersistentDBFileMode: PERSISTENT_DB_CHMOD,
		JsExecutionLimit:     DEFAULT_JS_EXECUTION_LIMIT,
		JsMemoryLimit:        DEFAULT_JS_MEMORY_LIMIT,
	}
}

func (o *ESEngineOptions) SetLoadGuardDir(dir string) {
	o.LoadGuardDir = dir
}

func (o *ESEngineOptions) SetPersistentDBFile(file string) {
	o.PersistentDBFile = file
}

// SetSpawnFunc replaces the external-command runner used by spawn() and
// runShellCommand() (nil restores the real one). Tests that load untrusted
// scripts use it so their shell commands never run on the host.
func (o *ESEngineOptions) SetSpawnFunc(fn SpawnFunc) {
	o.SpawnFunc = fn
}

// SetReadConfigFunc replaces the file reader used by readConfig() (nil
// restores the real one, os.ReadFile). Tests that load untrusted scripts
// use it so those scripts cannot read host files.
func (o *ESEngineOptions) SetReadConfigFunc(fn ReadConfigFunc) {
	o.ReadConfigFunc = fn
}

func (o *ESEngineOptions) SetPersistentDBFileMode(mode os.FileMode) {
	o.PersistentDBFileMode = mode
}

// SetPersistentDBOpenTimeout raises the bolt file-lock timeout for opening
// the persistent DB (0 keeps the production default of 1 second).
func (o *ESEngineOptions) SetPersistentDBOpenTimeout(timeout time.Duration) {
	o.PersistentDBOpenTimeout = timeout
}

func (o *ESEngineOptions) SetModulesDirs(dirs []string) {
	o.ModulesDirs = dirs
}

func (o *ESEngineOptions) SetTsgoPath(path string) {
	o.TsgoPath = path
}

func (o *ESEngineOptions) SetTsTypesPath(path string) {
	o.TsTypesPath = path
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

	tracker wbgong.ContentTracker
	// spawnFunc runs external commands for spawn()/runShellCommand(); nil
	// means the real Spawn. Tests that load untrusted scripts (the corpus)
	// install a stub so top-level shell commands in those scripts never
	// execute on the host.
	spawnFunc SpawnFunc
	// readConfigFunc reads the file for readConfig(); nil means os.ReadFile.
	// Stubbed alongside spawnFunc when loading untrusted scripts.
	readConfigFunc          ReadConfigFunc
	persistentDBCache       map[string]string
	persistentDB            *bolt.DB
	persistentDBOpenTimeout time.Duration // bolt file-lock timeout; 0 = the 1 s default
	modulesDirs             []string
	loadGuard               *loadGuard

	// orphanedCallbacks records one-shot callback stash keys whose only
	// invocation was dropped because the engine had stopped (e.g. a spawn
	// completing after Stop). The recording goroutine cannot sweep them
	// itself - that would touch the JS heap concurrently - so they are swept
	// at the next single-threaded boundary: Start, Stop or Close.
	orphanedCallbacks    []orphanedCallback
	orphanedCallbacksMtx sync.Mutex

	// closed marks a terminal Close(): the JS heap is gone and the engine
	// must not be used again.
	closed bool

	tsc         *TSCompiler       // non-nil when TypeScript support is enabled
	tsTypesPath string            // installed wb-rules.d.ts (Editor.GetTypes)
	tsCheckGen  map[string]uint64 // engine-loop only: per-file check revision
	// tsCheckWanted is set by preprocessRuleSource (inside LoadScenario) and
	// consumed by loadScript once the file has run: the check is scheduled
	// only then, so the registry snapshot includes the virtual devices the
	// file itself defines (engine loop only)
	tsCheckWanted string

	tsCheckMu      sync.Mutex
	tsCheckResults map[string]*tsCheckEntry // latest background verdict per physical path
}

type orphanedCallback struct {
	ctx *ESContext
	key ESCallback
}

type tsCheckEntry struct {
	gen   uint64
	ready bool
	diags []TSDiag
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
		tsCheckGen:        make(map[string]uint64),
		tsCheckResults:    make(map[string]*tsCheckEntry),
		persistentDB:      nil,
		modulesDirs:       options.ModulesDirs,
		// detects (and, at construction, reacts to) a file that crashed the
		// process during its last load, so a poison-pill rule can't crash-loop
		// the engine forever
		loadGuard: newLoadGuard(options.LoadGuardDir, LOAD_CRASH_QUARANTINE_THRESHOLD),
	}
	engine.tsTypesPath = options.TsTypesPath
	if options.TsgoPath != "" {
		// the pipeline is wired whenever a compiler path is configured;
		// the binary itself is probed per operation (Available() is a
		// stateless LookPath), so a tsgo installed AFTER wb-rules started
		// is picked up on the next .ts load - no restart needed (dpkg
		// configures the wb-tsgo dependency first, but manual installs
		// and older-package upgrades may not)
		engine.tsc = NewTSCompiler(options.TsgoPath, options.TsTypesPath)
		engine.ctxFactory.preprocessor = engine.preprocessRuleSource
		engine.ctxFactory.lineTranslator = engine.tsc.TranslateLine
		// transpiled TypeScript runs strict (tsc's own semantics):
		// the stripped "use strict" prologue is re-added inside the
		// single-line wrapper, keeping line numbers aligned
		engine.ctxFactory.wrapPrologue = func(path string) string {
			if strings.HasSuffix(path, ".ts") && !strings.HasSuffix(path, ".d.ts") {
				return `"use strict";`
			}
			return ""
		}
		if !engine.tsc.Available() {
			wbgong.Error.Printf(
				"tsgo binary not found at %s; wb-rules depends on the wb-tsgo package (broken installation?) - .ts files will fail to load until it appears",
				options.TsgoPath)
		}
	} else {
		// explicit -tsgo="": .ts files are rejected, never run as raw JS
		engine.ctxFactory.preprocessor = func(path string, src []byte) ([]byte, error) {
			if strings.HasSuffix(path, ".d.ts") {
				// the deb ships wb-rules.d.ts inside a watched dir;
				// declaration files are not executable code and must
				// not produce a boot error
				return []byte("// TypeScript declaration file, nothing to execute\n"), nil
			}
			if strings.HasSuffix(path, ".ts") {
				return nil, fmt.Errorf(`TypeScript support is disabled (-tsgo="")`)
			}
			return src, nil
		}
	}
	engine.globalCtx = engine.ctxFactory.newESContext(func(thunk func()) { engine.MaybeCallSync(thunk) }, "")
	// a runaway rule (while(true) etc.) must error out, not freeze the loop
	engine.globalCtx.SetExecutionTimeLimit(options.JsExecutionLimit)
	engine.spawnFunc = options.SpawnFunc
	engine.readConfigFunc = options.ReadConfigFunc
	// and one that allocates without bound must throw, not OOM-kill the process
	engine.globalCtx.SetMemoryLimit(options.JsMemoryLimit)
	// async rule callbacks that throw after an await fail as promise jobs;
	// without this they die silently on stderr instead of the rule log
	engine.globalCtx.SetJobErrorHandler(func(msg string) {
		engine.Log(ENGINE_LOG_ERROR, fmt.Sprintf("async rule error: %s", msg))
	})

	engine.persistentDBOpenTimeout = options.PersistentDBOpenTimeout
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

	// init prototype for objects returned from _wbCellObject
	engine.initCellObjectPrototype(engine.globalCtx)

	// init threads storage
	engine.initGlobalThreadList(engine.globalCtx)

	// init modules storage
	engine.initModulesStorage(engine.globalCtx)

	engine.globalCtx.PushGlobalObject()
	engine.installBuiltins(engine.globalCtx)

	// set global prototype to __wbModulePrototype
	engine.globalCtx.GetPropString(-1, MODULE_OBJ_PROTO_NAME)
	engine.globalCtx.SetPrototype(-2)
	// [ global ]

	if err := engine.loadLib(); err != nil {
		wbgong.Error.Panicf("failed to load runtime library: %v", err)
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

	engine.cleanup.PushCleanupScope("__setup__")
	defer engine.cleanup.PopCleanupScope("__setup__")
	engine.RuleEngine.setupRuleEngineSettingsDevice()

	return
}

// preprocessRuleSource transpiles TypeScript rule files on load. The
// transpile is a fast type-strip (~1 ms); the full type check runs in the
// background afterwards and only ever produces warnings — rules run first,
// diagnostics arrive later.
func (engine *ESEngine) preprocessRuleSource(path string, src []byte) ([]byte, error) {
	if strings.HasSuffix(path, ".d.ts") {
		// declaration files carry no executable code and make the
		// transpiler panic; they may sit in watched rule dirs
		wbgong.Debug.Printf("skipping TypeScript declaration file %s", path)
		return []byte("// TypeScript declaration file, nothing to execute\n"), nil
	}
	isTS := strings.HasSuffix(path, ".ts")
	if !isTS && !strings.HasSuffix(path, ".js") {
		return src, nil
	}

	js := src // .js runs as-is; .ts is type-stripped below
	if isTS {
		if !engine.tsc.Available() {
			return nil, fmt.Errorf(
				"TypeScript compiler not found at %s - wb-rules depends on the wb-tsgo package (broken installation?)",
				engine.tsc.binPath)
		}
		transpiled, err := engine.tsc.Transpile(string(src), path)
		if err != nil {
			// any file that fails to transpile still gets a terminal
			// Editor.Check verdict - otherwise clients poll "pending" forever
			// (or, on reload, keep seeing the previous content's verdict)
			diag := TSDiag{File: path, Line: 1, Column: 1, Severity: "error", Message: err.Error()}
			var syntaxErr *TSSyntaxError
			if errors.As(err, &syntaxErr) {
				diag.Line = syntaxErr.Line
				diag.Message = syntaxErr.Text
				diag.Code = syntaxErr.Code
			}
			engine.tsCheckGen[path]++
			engine.tsCheckMu.Lock()
			engine.tsCheckResults[path] = &tsCheckEntry{
				gen:   engine.tsCheckGen[path],
				ready: true,
				diags: []TSDiag{diag},
			}
			engine.tsCheckMu.Unlock()
			return nil, err
		}
		js = []byte(transpiled)
	}

	// Background type check, for .ts and .js alike: the check runs with
	// --checkJs, so a .js file is type-checked against the wb-rules types and
	// the live-device registry too (e.g. dev["buzzer/enabled"] = 123). A .js
	// file needs no transpile and still runs even without tsgo, so only
	// request the check when tsgo is available. The request is honoured by
	// loadScript after LoadScenario returns, not here: the registry is
	// snapshotted when the check is scheduled, and the file's own
	// defineVirtualDevice() calls have not run yet at this point (on a
	// reload, runCleanups has just removed the previous incarnation's
	// devices) - scheduling now would check the file against a registry
	// that lacks exactly the controls it writes to most.
	if engine.tsc.Available() && engine.tsc.CheckSupported() {
		engine.tsCheckWanted = path
	}

	return js, nil
}

// tsCheckJournalCap bounds the "TS check:" lines one file's check writes to
// the rule log; the full list stays available through Editor.Check.
const tsCheckJournalCap = 10

// scheduleTsCheck queues the background type check of a loaded rule file
// (engine loop only: it touches tsCheckGen).
func (engine *ESEngine) scheduleTsCheck(path string) {
	// generation guard: a slow check for an old revision of the file must
	// not overwrite the newer check's verdict (the gen counter is touched
	// on the engine loop only)
	engine.tsCheckGen[path]++
	gen := engine.tsCheckGen[path]
	engine.tsCheckMu.Lock()
	engine.tsCheckResults[path] = &tsCheckEntry{gen: gen}
	engine.tsCheckMu.Unlock()
	// Generate the device registry now, on the engine loop (the driver is
	// read here, not from the background goroutine).
	engine.tsc.CheckAsync(path, engine.controlsRegistryDts(), func(diags []TSDiag) {
		if !engine.IsActive() {
			return
		}
		// MaybeCallSync: the engine may have stopped while the check ran;
		// never block a background goroutine on a dead sync queue.
		engine.MaybeCallSync(func() {
			if engine.tsCheckGen[path] != gen {
				return // a newer revision of the file is being checked
			}
			// The rules console gets diagnostics only for files the user can
			// act on (under the editable source root). System rule packages
			// are checked too - their verdicts stay in the Editor.Check
			// cache and in the debug log - but journaling them would page
			// the user about files that are not theirs to fix.
			_, _, editable, _, cerr := engine.checkSourcePath(path)
			journal := cerr == nil && editable
			for i, d := range diags {
				if !journal {
					wbgong.Debug.Printf("TS check (system file): %s:%d:%d: %s", d.File, d.Line, d.Column, d.Message)
					continue
				}
				if i == tsCheckJournalCap {
					engine.Log(ENGINE_LOG_WARNING, fmt.Sprintf(
						"TS check: %s: %d more diagnostics (see the file in the editor)", path, len(diags)-i))
					break
				}
				engine.Log(ENGINE_LOG_WARNING, fmt.Sprintf(
					"TS check: %s:%d:%d: %s", d.File, d.Line, d.Column, d.Message))
			}
			engine.tsCheckMu.Lock()
			engine.tsCheckResults[path] = &tsCheckEntry{gen: gen, ready: true, diags: diags}
			engine.tsCheckMu.Unlock()
		})
	})
}

type controlRef struct{ ref, ctrlType string }

// renderControlsRegistry renders the WbControls declaration that
// declaration-merges into the empty interface in wb-rules.d.ts. With it in
// the check program, the stringly-referenced APIs (dev["dev/ctrl"],
// getControl("dev/ctrl")) are typed by each live control's real type -
// matching what the homeui editor does from its own device list. Empty
// input yields "" so the check stays loose (no registry file added).
// aliasNameRx: only names that are plain identifiers can be declared; a
// defineAlias with an exotic name stays untyped (loose), never breaks the
// generated declarations.
var aliasNameRx = regexp.MustCompile(`^[A-Za-z_$][A-Za-z0-9_$]*$`)

func renderControlsRegistry(refs []controlRef, aliases []string) string {
	if len(refs) == 0 && len(aliases) == 0 {
		return ""
	}
	// JSON string literals are valid TS string literals; unlike strconv.Quote
	// they never emit \a or \UXXXXXXXX (which TS rejects), so an exotic control
	// id can't produce a malformed .d.ts that fails every rule's check. Matches
	// the editor side, which builds the registry with JSON.stringify.
	tsStr := func(s string) string {
		q, _ := json.Marshal(s)
		return string(q)
	}
	var b strings.Builder
	b.WriteString("interface WbControls {\n")
	for _, r := range refs {
		fmt.Fprintf(&b, "  %s: %s;\n", tsStr(r.ref), tsStr(r.ctrlType))
	}
	b.WriteString("}\n")
	// defineAlias("heaterOn", "relay/K1") creates a bare global the checker
	// cannot see (an accessor installed at run time): declare each alias so
	// documented alias usage - heaterOn = true - is not "Cannot find name"
	for _, name := range aliases {
		if aliasNameRx.MatchString(name) {
			fmt.Fprintf(&b, "declare var %s: any;\n", name)
		}
	}
	return b.String()
}

// aliasNames lists the names created by defineAlias() so far. The alias map
// (_WbRules.aliases) lives on the shared prototype realm, so one read covers
// every file. Engine loop only. Best effort: on any trouble the aliases just
// stay untyped.
func (engine *ESEngine) aliasNames() []string {
	if engine.globalCtx == nil {
		return nil
	}
	if engine.globalCtx.PevalString("JSON.stringify(Object.keys(_WbRules.aliases))") != 0 {
		engine.globalCtx.Pop()
		return nil
	}
	var names []string
	err := json.Unmarshal([]byte(engine.globalCtx.GetString(-1)), &names)
	engine.globalCtx.Pop()
	if err != nil {
		return nil
	}
	sort.Strings(names)
	return names
}

// controlsRegistryDts snapshots the driver's current device table into a
// WbControls declaration. Must be called on the engine loop (it reads the
// driver). Never fails the check: on any trouble it returns "" (loose).
func (engine *ESEngine) controlsRegistryDts() string {
	if engine.driver == nil {
		return ""
	}
	var refs []controlRef
	err := engine.driver.Access(func(tx wbgong.DriverTx) error {
		for _, dev := range tx.GetDevicesList() {
			devID := dev.GetId()
			if strings.HasPrefix(devID, "system__") {
				continue // match the editor: skip the engine's own system controls
			}
			for _, ctrl := range dev.ControlsList() {
				ctrlType := ctrl.GetType()
				if ctrlType == "" {
					continue // type not known yet -> would map to `any` anyway
				}
				refs = append(refs, controlRef{devID + "/" + ctrl.GetId(), ctrlType})
			}
		}
		return nil
	})
	if err != nil {
		wbgong.Error.Printf("ts check: cannot read device list for registry: %s", err)
		return ""
	}
	return renderControlsRegistry(refs, engine.aliasNames())
}

// Start starts the engine loops. On a restart it first reclaims callback
// stash entries orphaned while the engine was stopped (both Start and the
// sweep run before any loop goroutine exists, so the JS heap access is
// single-threaded).
func (engine *ESEngine) Start() {
	engine.sweepOrphanedCallbacks()
	engine.RuleEngine.Start()
}

// Stop shuts the engine down, including the TypeScript compiler child.
// The engine can be started again; Close is the terminal counterpart.
func (engine *ESEngine) Stop() {
	// engine loop first: a draining .ts load must not respawn the compiler
	engine.RuleEngine.Stop()
	// the loops are gone: reclaim one-shot callbacks whose completion was
	// dropped during the shutdown itself
	engine.sweepOrphanedCallbacks()
	if engine.tsc != nil {
		engine.tsc.Stop()
	}
}

// Close permanently destroys the engine: it stops the loops if they are
// still running, closes the persistent DB, and frees the native JS heap -
// DestroyHeap also releases every entry the process-global Go registries
// held for this heap. Unlike Stop, Close is terminal: the engine must not
// be used afterwards. Idempotent - extra calls do nothing.
//
// Follow-up candidates beyond Close (owned callback handles, an explicit
// heap owner object, per-file SourceRealm ownership) are tracked outside
// this code.
func (engine *ESEngine) Close() {
	if engine.closed {
		return
	}
	engine.closed = true
	if engine.IsActive() {
		engine.Stop()
	}
	if engine.persistentDB != nil {
		engine.persistentDB.Close()
		engine.persistentDB = nil
	}
	engine.globalCtx.DestroyHeap()
	// Mark every context invalid so any straggler (a Go finalizer, a late
	// producer whose thunk was dropped) sees IsValid()==false instead of
	// reaching into the freed heap.
	for _, ctx := range engine.localCtxs {
		ctx.markClosed()
	}
	engine.localCtxs = make(map[string]*ESContext)
	engine.globalCtx.markClosed()
}

// noteOrphanedCallback schedules a one-shot callback stash entry for the
// next single-threaded sweep; called from producer goroutines whose
// completion thunk the stopped engine dropped.
func (engine *ESEngine) noteOrphanedCallback(ctx *ESContext, key ESCallback) {
	engine.orphanedCallbacksMtx.Lock()
	engine.orphanedCallbacks = append(engine.orphanedCallbacks, orphanedCallback{ctx, key})
	engine.orphanedCallbacksMtx.Unlock()
}

func (engine *ESEngine) sweepOrphanedCallbacks() {
	engine.orphanedCallbacksMtx.Lock()
	orphans := engine.orphanedCallbacks
	engine.orphanedCallbacks = nil
	engine.orphanedCallbacksMtx.Unlock()
	for _, o := range orphans {
		// no-op when the context was invalidated meanwhile (reload swept it)
		o.ctx.RemoveCallback(o.key)
	}
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
		"setError":        engine.esVdevSetError,
		"getError":        engine.esVdevGetError,
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
		"setTitle":       engine.esVdevCellSetTitle,
		"setEnumTitles":  engine.esVdevCellSetEnumTitles,
		"getTitle":       engine.esVdevCellGetTitle,
		"setType":        engine.esVdevCellSetType,
		"getType":        engine.esVdevCellGetType,
		"setUnits":       engine.esVdevCellSetUnits,
		"getUnits":       engine.esVdevCellGetUnits,
		"setReadonly":    engine.esVdevCellSetReadonly,
		"getReadonly":    engine.esVdevCellGetReadonly,
		"setMax":         engine.esVdevCellSetMax,
		"getMax":         engine.esVdevCellGetMax,
		"setMin":         engine.esVdevCellSetMin,
		"getMin":         engine.esVdevCellGetMin,
		"setPrecision":   engine.esVdevCellSetPrecision,
		"getPrecision":   engine.esVdevCellGetPrecision,
		"setError":       engine.esVdevCellSetError,
		"getError":       engine.esVdevCellGetError,
		"setOrder":       engine.esVdevCellSetOrder,
		"getOrder":       engine.esVdevCellGetOrder,
		"setValue":       engine.esVdevCellSetValue,
		"getValue":       engine.esVdevCellGetValue,
	})

	ctx.PutPropString(-2, VDEV_OBJ_PROTO_CELL_NAME)
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
	engine.Log(ENGINE_LOG_ERROR, fmt.Sprintf("ECMAScript error: %v", err))
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
	var found bool

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

		delete(engine.ctxTimers, ctx)
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
		return NewFuncValueChangedRuleCondition(func() any { return f(nil) }), nil
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
		}
		conds[i] = cond
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
	cond, err := engine.buildRuleCond(ctx, defIndex)
	if err != nil {
		return nil, fmt.Errorf("error building rule condition: %w", err)
	}

	ruleId := engine.nextRuleId
	engine.nextRuleId++

	return NewRule(engine, ruleId, name, cond, then), nil
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
	for virtualPath := range engine.editableSources {
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

// cleanPath is a clean shortest path from root directory to this file
// virtualPath is a relative path for files in the edit directory
// underSourceRoot is true when this file is in the edit directory\
func (engine *ESEngine) checkSourcePath(path string) (cleanPath, virtualPath string, underSourceRoot, enabled bool, err error) {
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

func (engine *ESEngine) checkVirtualPath(path string) (cleanPath, virtualPath string, enabled bool, err error) {
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

// esBuiltinFuncs is the full set of Go builtins visible to rule scripts.
func (engine *ESEngine) esBuiltinFuncs() map[string]func(*ESContext) int {
	return map[string]func(*ESContext) int{
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
		"_wbAddCleanup":        engine.esWbAddCleanup,
	}
}

// installBuiltins defines the Go builtins (and log's level methods) as
// own properties of the object at the stack top.
func (engine *ESEngine) installBuiltins(ctx *ESContext) {
	ctx.DefineFunctions(engine.esBuiltinFuncs())
	ctx.GetPropString(-1, "log")
	ctx.DefineFunctions(map[string]func(*ESContext) int{
		"debug":   engine.makeLogFunc(ENGINE_LOG_DEBUG),
		"info":    engine.makeLogFunc(ENGINE_LOG_INFO),
		"warning": engine.makeLogFunc(ENGINE_LOG_WARNING),
		"error":   engine.makeLogFunc(ENGINE_LOG_ERROR),
	})
	ctx.Pop()
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
	if newLocalCtx == nil {
		// realm creation failed: the JS heap is at its memory limit. Report
		// a load error instead of dereferencing a nil context.
		engine.globalCtx.Pop3()
		// []
		return nil
	}

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

	// Bind the realm-sensitive API to this realm: QuickJS dispatches a C
	// function to the realm it was created in, and promise jobs (code
	// after an await) run with no calling-realm context - so builtins
	// inherited from the shared prototype would attribute late
	// defineRule/setTimeout calls to the wrong file. Realm-local copies
	// of the Go builtins plus rebound JS wrappers keep attribution.
	newLocalCtx.PushGlobalObject()
	// [ global ]
	engine.installBuiltins(newLocalCtx)
	newLocalCtx.Pop()
	// []
	// Compile the wrapper layer IN THIS REALM: the binder source comes
	// from the shared lib, but eval'ing it here creates realm-local
	// closures whose builtin references resolve to the realm-local Go
	// funcs installed above - so calls made after an await still land in
	// this realm and keep their file attribution.
	if newLocalCtx.PevalString("eval('(' + __wbBindRealmAPI.toString() + ')')(this);") != 0 {
		wbgong.Error.Println("failed to bind realm API for " + path)
	}
	newLocalCtx.Pop()

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
		wbgong.Debug.Printf("%s is NOT under source root %s", path, engine.sourceRoot)
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

	// a file that repeatedly crashed the whole process while loading is
	// quarantined: skip it so it can't crash-loop the engine. Editing it
	// (which changes its mtime) releases the quarantine.
	if engine.loadGuard.quarantined(path) {
		wbgong.Error.Printf("[loadguard] skipping quarantined file %s (edit it to retry)", path)
		// the file must not look healthy in the editor: record a load error
		// (shown by Editor.List/Load) and say so in the rules console
		scriptErr := NewScriptError(fmt.Sprintf(
			"[loadguard] this file crashed the rule engine while loading "+
				"%d times in a row and is skipped until it is edited",
			LOAD_CRASH_QUARANTINE_THRESHOLD), nil)
		engine.setSourceError(currentSource, &scriptErr)
		engine.Log(ENGINE_LOG_ERROR, fmt.Sprintf("%s: %s", path, scriptErr.Error()))
		return true, scriptErr
	}

	// create new context for this file
	newLocalCtx := engine.prepareNewContext(path)
	if newLocalCtx == nil {
		scriptErr := NewScriptError(
			"cannot create a JS context for this file (js heap memory limit reached?)", nil)
		engine.setSourceError(currentSource, &scriptErr)
		engine.Log(ENGINE_LOG_ERROR, fmt.Sprintf("%s: %s", path, scriptErr.Error()))
		return true, scriptErr
	}
	currentSource.Context = newLocalCtx

	// bracket the actual evaluation with the load-crash marker: if the process
	// dies inside LoadScenario, the surviving marker names this file next boot
	engine.loadGuard.beginLoad(path)
	engine.tsCheckWanted = ""
	err = engine.trackESError(path, newLocalCtx.LoadScenario(path))
	engine.loadGuard.endLoad(path)
	// the type check is scheduled only now, after the file has run: its own
	// virtual devices are in the driver's table and thus in the registry
	// snapshot (see preprocessRuleSource); a file that failed to transpile
	// got its terminal verdict there and does not ask for a check
	if engine.tsCheckWanted == path {
		engine.tsCheckWanted = ""
		engine.scheduleTsCheck(path)
	}
	// Keep a load failure retryable only when it may fix itself once the
	// environment changes - i.e. a .ts file that failed because tsgo isn't
	// available yet (the wb-tsgo package appears after wb-rules started). We
	// un-deduplicate it so a later reload re-runs it. NOT for a real error: a
	// .ts with a genuine syntax/type error (tsgo present) or any .js failure is
	// content-based, and un-tracking it would re-run it on every
	// unchanged-content FS event, re-firing partial side effects and re-logging.
	//
	// The .ts/.js split here is a load-path distinction, not a language one:
	// only .ts goes through tsgo, so only a .ts failure can be caused by tsgo
	// being absent. Available() re-probes the tsgo binary.
	if err != nil && strings.HasSuffix(path, ".ts") && engine.tsc != nil && !engine.tsc.Available() {
		engine.tracker.Untrack(virtualPath)
	}
	return true, err
}

// setSourceError records a load error on a file entry. The entry is shared
// with the Editor RPC (ListSourceFiles copies it under sourcesMtx), so the
// write must hold the same lock.
func (engine *ESEngine) setSourceError(entry *LocFileEntry, err *ScriptError) {
	engine.sourcesMtx.Lock()
	defer engine.sourcesMtx.Unlock()
	entry.Error = err
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

	// set error in the file entry (under the lock the RPC readers take)
	engine.sourcesMtx.Lock()
	if entry := engine.sources[path]; entry != nil {
		entry.Error = &scriptErr
	}
	engine.sourcesMtx.Unlock()

	engine.Log(ENGINE_LOG_ERROR, scriptErr.Error())
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
	r := make(chan error, 1)
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
		err = os.WriteFile(cleanPath, []byte(content), 0644)
		if err != nil {
			r <- err
			return
		}
		r <- engine.loadScriptAndRefresh(cleanPath, true)
	})
	return engine.waitOrStopped(r)
}

// waitOrStopped waits for a result from a thunk handed to WhenEngineReady/
// CallSync, failing fast with ErrEngineStopped when the engine stops before
// the thunk delivered one - the thunk was dropped and would leave the caller
// (e.g. the Editor RPC) hanging forever. The channel must be buffered so a
// dropped caller never blocks a late thunk.
func (engine *ESEngine) waitOrStopped(r chan error) error {
	select {
	case err := <-r:
		return err
	case <-engine.syncStopCh():
		// the loops are gone; prefer a result that arrived just before
		select {
		case err := <-r:
			return err
		default:
			return ErrEngineStopped
		}
	}
}

// LiveLoadFile loads the specified script in the running engine.
// If the engine isn't ready yet, the function waits for it to become
// ready. If the script didn't change since the last time it was loaded,
// the script isn't loaded.
func (engine *ESEngine) LiveLoadFile(path string) error {
	wbgong.Debug.Printf("LiveLoadFile: %s", path)
	r := make(chan error, 1)
	engine.WhenEngineReady(func() {
		r <- engine.loadScriptAndRefresh(path, false)
	})

	return engine.waitOrStopped(r)
}

func (engine *ESEngine) LiveRemoveFile(path string) error {
	wbgong.Debug.Printf("LiveRemoveFile: %s", path)
	path, virtualPath, _, _, err := engine.checkSourcePath(path)

	if err != nil {
		return err
	}

	engine.WhenEngineReady(func() {
		engine.tracker.Untrack(virtualPath)
		// Drop the per-file verdict so it can't accumulate over a
		// long-running daemon that sees many distinct paths, but KEEP the
		// generation counter (it is 8 bytes per path ever seen) and ADVANCE
		// it: an in-flight check of the removed content still holds the old
		// generation, and without the bump its callback would repopulate the
		// verdict cache for a file that no longer exists - or, if the file
		// is re-created (save-by-rename editors) and restarted at
		// generation 1, publish the removed content's verdict for the new
		// file. This runs on the engine loop (tsCheckGen's owner).
		engine.tsCheckGen[path]++
		engine.tsCheckMu.Lock()
		delete(engine.tsCheckResults, path)
		engine.tsCheckMu.Unlock()
		if engine.tsc != nil {
			engine.tsc.Forget(path)
		}
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
		engine.Log(ENGINE_LOG_ERROR, "getDevice(): bad parameters")
		return duktape.DUK_RET_ERROR
	}

	name := ctx.GetString(0)

	errDevice := engine.GetDevice(name)
	if errDevice != nil {
		engine.Log(ENGINE_LOG_DEBUG, fmt.Sprintf("Error in getting device: %s", errDevice))
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
		engine.Log(ENGINE_LOG_ERROR, "getControl(): bad parameters")
		return duktape.DUK_RET_ERROR
	}

	name := ctx.GetString(0)

	ids := strings.Split(name, "/")
	if len(ids) != 2 {
		engine.Log(ENGINE_LOG_ERROR, "getControl(): bad parameters: should be 'devID/cellID'")
		return duktape.DUK_RET_ERROR
	}
	devID := ids[0]
	ctrlID := ids[1]

	errDevice := engine.GetDevice(devID)
	if errDevice != nil {
		engine.Log(ENGINE_LOG_DEBUG, fmt.Sprintf("Error in getting device: %s", errDevice))
		ctx.PushUndefined()
		return 1
	}
	devProxy := engine.GetDeviceProxy(devID)
	ctrl := devProxy.getControl(ctrlID)
	if ctrl == nil {
		engine.Log(ENGINE_LOG_DEBUG, fmt.Sprintf("getControl(): no such control '%s'", ctrlID))
		ctx.PushUndefined()
		return 1
	}

	engine.makeControlObject(ctx, devID, ctrlID)

	return 1
}

// throwOnConversionError rethrows the error a getter or Proxy trap raised
// while a script-supplied object was being converted (GetJSObject returned
// the ConversionError sentinel): the converted object is unusable, and
// Duktape propagated such throws to the calling script, so treating it as a
// silent bad parameter would hide the script's own bug. The rethrown value
// is an Error carrying the original error's message ("TypeError: boom");
// the original error subclass is not preserved.
func throwOnConversionError(ctx *ESContext, v any) (int, bool) {
	ce, ok := v.(ConversionError)
	if !ok {
		return 0, false
	}
	ctx.PushErrorObject(duktape.DUK_ERR_ERROR, ce.Message)
	return duktape.DUK_RET_INSTACK_ERROR, true
}

// defineVirtualDevice creates virtual device object in MQTT
// and returns JS object to control it
func (engine *ESEngine) esDefineVirtualDevice(ctx *ESContext) int {
	if ctx.GetTop() != 2 || !ctx.IsString(0) || !ctx.IsObject(1) {
		return duktape.DUK_RET_ERROR
	}
	name := ctx.GetString(0)
	objAny := ctx.GetJSObject(1)
	if rc, thrown := throwOnConversionError(ctx, objAny); thrown {
		return rc
	}
	obj, ok := objAny.(objx.Map)
	if !ok { // an array or other non-plain object
		return duktape.DUK_RET_TYPE_ERROR
	}

	// The device's removal must land in the owning file's cleanup scope even
	// when this call is a top-level-await continuation: the ambient scope
	// pushed by loadScript was popped when the file's synchronous part
	// returned, so without this a device defined after an await would
	// survive the file's reload/removal. The realm-local __filename keeps
	// attribution correct after an await (see prepareNewContext).
	if currentFilename := ctx.GetCurrentFilename(); currentFilename != "" {
		engine.cleanup.PushCleanupScope(currentFilename)
		defer engine.cleanup.PopCleanupScope(currentFilename)
	}

	if err := engine.DefineVirtualDevice(name, obj); err != nil {
		wbgong.Error.Printf("device definition error: %v", err)
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

func (engine *ESEngine) esVdevSetError(ctx *ESContext) int {
	if !ctx.IsString(0) {
		wbgong.Error.Printf("setError(): bad parameters")
		return duktape.DUK_RET_ERROR
	}
	errorStr := ctx.GetString(0)

	ctx.PushThis()

	devId, err := engine.getStringPropFromObject(ctx, -1, VDEV_OBJ_PROP_DEVID)
	if err != nil {
		ctx.Pop()

		return duktape.DUK_RET_TYPE_ERROR
	}

	ctx.Pop()

	devProxy := engine.GetDeviceProxy(devId)
	devProxy.SetMeta(wbgong.CONV_META_SUBTOPIC_ERROR, errorStr)

	return 1
}

func (engine *ESEngine) esVdevGetError(ctx *ESContext) int {
	ctx.PushThis()

	devId, err := engine.getStringPropFromObject(ctx, -1, VDEV_OBJ_PROP_DEVID)
	if err != nil {
		ctx.Pop()

		return duktape.DUK_RET_TYPE_ERROR
	}

	ctx.Pop()

	var errString string
	devProxy := engine.GetDeviceProxy(devId)
	meta := devProxy.GetMeta()
	if meta != nil {
		if errStr, ok := meta[wbgong.CONV_META_SUBTOPIC_ERROR].(string); ok {
			errString = errStr
		}
	}
	ctx.PushString(errString)
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
		wbgong.Error.Printf("Error in removing control %s on device %s: %v", ctrlId, devId, errControl)
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
	ctrlDefAny := ctx.GetJSObject(1)
	if rc, thrown := throwOnConversionError(ctx, ctrlDefAny); thrown {
		return rc
	}
	ctrlDef, ok := ctrlDefAny.(objx.Map)
	if !ok {
		wbgong.Error.Printf("addControl(): bad parameters")
		return duktape.DUK_RET_TYPE_ERROR
	}

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
		wbgong.Error.Printf("Error in creating control %s on device %s: %v", ctrlId, devId, errControl)
	}
	return 0
}

func (engine *ESEngine) esVdevCellGetDescription(ctx *ESContext) int {
	ctrlProxy, dukRet := engine.getControlFromCtx(ctx)
	if dukRet < 0 {
		return dukRet
	}

	ctrl := ctrlProxy.getControl()
	if ctrl == nil {
		return duktape.DUK_RET_ERROR
	}

	ctx.PushString(ctrl.GetDescription())

	return 1
}

func (engine *ESEngine) esVdevCellGetTitle(ctx *ESContext) int {
	ctrlProxy, dukRet := engine.getControlFromCtx(ctx)
	if dukRet < 0 {
		return dukRet
	}

	lang := "en"
	if ctx.IsString(0) {
		lang = ctx.GetString(0)
	}

	ctrl := ctrlProxy.getControl()
	if ctrl == nil {
		return duktape.DUK_RET_ERROR
	}

	var titleStr string

	title := ctrl.GetTitle()

	if val, ok := title[lang]; ok {
		titleStr = val
	}

	ctx.PushString(titleStr)

	return 1
}

func (engine *ESEngine) esVdevCellGetType(ctx *ESContext) int {
	ctrlProxy, duk_ret := engine.getControlFromCtx(ctx)
	if duk_ret < 0 {
		return duk_ret
	}

	ctrl := ctrlProxy.getControl()
	if ctrl == nil {
		return duktape.DUK_RET_ERROR
	}

	ctx.PushString(ctrl.GetType())

	return 1
}

func (engine *ESEngine) esVdevCellGetUnits(ctx *ESContext) int {
	ctrlProxy, duk_ret := engine.getControlFromCtx(ctx)
	if duk_ret < 0 {
		return duk_ret
	}

	ctrl := ctrlProxy.getControl()
	if ctrl == nil {
		return duktape.DUK_RET_ERROR
	}

	ctx.PushString(ctrl.GetUnits())

	return 1
}

func (engine *ESEngine) esVdevCellGetReadonly(ctx *ESContext) int {
	ctrlProxy, duk_ret := engine.getControlFromCtx(ctx)
	if duk_ret < 0 {
		return duk_ret
	}

	ctrl := ctrlProxy.getControl()
	if ctrl == nil {
		return duktape.DUK_RET_ERROR
	}

	ctx.PushBoolean(ctrl.GetReadonly())

	return 1
}

func (engine *ESEngine) esVdevCellGetMax(ctx *ESContext) int {
	ctrlProxy, duk_ret := engine.getControlFromCtx(ctx)
	if duk_ret < 0 {
		return duk_ret
	}

	ctrl := ctrlProxy.getControl()
	if ctrl == nil {
		return duktape.DUK_RET_ERROR
	}

	ctx.PushNumber(ctrl.GetMax())

	return 1
}

func (engine *ESEngine) esVdevCellGetMin(ctx *ESContext) int {
	ctrlProxy, duk_ret := engine.getControlFromCtx(ctx)
	if duk_ret < 0 {
		return duk_ret
	}

	ctrl := ctrlProxy.getControl()
	if ctrl == nil {
		return duktape.DUK_RET_ERROR
	}

	ctx.PushNumber(ctrl.GetMin())

	return 1
}

func (engine *ESEngine) esVdevCellGetPrecision(ctx *ESContext) int {
	ctrlProxy, duk_ret := engine.getControlFromCtx(ctx)
	if duk_ret < 0 {
		return duk_ret
	}

	ctrl := ctrlProxy.getControl()
	if ctrl == nil {
		return duktape.DUK_RET_ERROR
	}

	ctx.PushNumber(ctrl.GetPrecision())

	return 1
}

func (engine *ESEngine) esVdevCellGetError(ctx *ESContext) int {
	ctrlProxy, duk_ret := engine.getControlFromCtx(ctx)
	if duk_ret < 0 {
		return duk_ret
	}
	ctrl := ctrlProxy.getControl()
	if ctrl == nil {
		return duktape.DUK_RET_ERROR
	}
	var errString string
	if ctrlErr := ctrl.GetError(); ctrlErr != nil {
		errString = ctrlErr.Error()
	}
	ctx.PushString(errString)
	return 1
}

func (engine *ESEngine) esVdevCellGetOrder(ctx *ESContext) int {
	ctrlProxy, duk_ret := engine.getControlFromCtx(ctx)
	if duk_ret < 0 {
		return duk_ret
	}

	ctrl := ctrlProxy.getControl()
	if ctrl == nil {
		return duktape.DUK_RET_ERROR
	}

	ctx.PushInt(ctrl.GetOrder())

	return 1
}

func (engine *ESEngine) esVdevCellGetId(ctx *ESContext) int {
	ctrlProxy, duk_ret := engine.getControlFromCtx(ctx)
	if duk_ret < 0 {
		return duk_ret
	}

	ctrl := ctrlProxy.getControl()
	if ctrl == nil {
		return duktape.DUK_RET_ERROR
	}

	ctx.PushString(ctrl.GetId())

	return 1
}

func (engine *ESEngine) esVdevCellGetValue(ctx *ESContext) int {
	ctrlProxy, duk_ret := engine.getControlFromCtx(ctx)
	if duk_ret < 0 {
		return duk_ret
	}

	ctrl := ctrlProxy.getControl()
	if ctrl == nil {
		return duktape.DUK_RET_ERROR
	}

	value, err := ctrl.GetValue()
	if err != nil {
		wbgong.Error.Printf("getValue (%s/%s) failed: %v", ctrlProxy.devProxy.name, ctrlProxy.name, err)
		return duktape.DUK_RET_ERROR
	}

	ctx.PushJSObject(value)

	return 1
}

func (engine *ESEngine) esVdevCellSetDescription(ctx *ESContext) int {
	if !ctx.IsString(0) {
		wbgong.Error.Printf("setDescription(): bad parameters")
		return duktape.DUK_RET_ERROR
	}
	descr := ctx.GetString(0)

	ctrlProxy, duk_ret := engine.getControlFromCtx(ctx)
	if duk_ret < 0 {
		return duk_ret
	}

	ctrlProxy.SetMeta(wbgong.CONV_META_SUBTOPIC_DESCRIPTION, descr)
	return 0
}

func (engine *ESEngine) esVdevCellSetTitle(ctx *ESContext) int {
	if !ctx.IsString(0) && !ctx.IsObject(0) {
		wbgong.Error.Printf("setTitle(): bad parameters")
		return duktape.DUK_RET_ERROR
	}
	m := make(wbgong.Title)
	if ctx.IsObject(0) {
		titlesAny := ctx.GetJSObject(0)
		if rc, thrown := throwOnConversionError(ctx, titlesAny); thrown {
			return rc
		}
		titles, ok := titlesAny.(objx.Map)
		if !ok {
			wbgong.Error.Printf("setTitle(): bad parameters")
			return duktape.DUK_RET_TYPE_ERROR
		}
		for k, v := range titles {
			str, ok := v.(string)
			if !ok {
				wbgong.Error.Printf("setTitle(): title for %q is not a string", k)
				return duktape.DUK_RET_TYPE_ERROR
			}
			m[k] = str
		}
	} else if ctx.IsString(0) {
		m["en"] = ctx.GetString(0)
	}

	jsonTitle, _ := json.Marshal(m)

	ctrlProxy, duk_ret := engine.getControlFromCtx(ctx)
	if duk_ret < 0 {
		return duk_ret
	}

	ctrlProxy.SetMeta(wbgong.CONV_META_SUBTOPIC_CONTROL_TITLE, string(jsonTitle))
	return 0
}

func (engine *ESEngine) esVdevCellSetEnumTitles(ctx *ESContext) int {
	if !ctx.IsObject(0) {
		wbgong.Error.Printf("setEnumTitles(): bad parameters")
		return duktape.DUK_RET_ERROR
	}

	enumTitlesAny := ctx.GetJSObject(0)
	if rc, thrown := throwOnConversionError(ctx, enumTitlesAny); thrown {
		return rc
	}
	enumTitles, ok := enumTitlesAny.(objx.Map)
	if !ok {
		wbgong.Error.Printf("setEnumTitles(): bad parameters")
		return duktape.DUK_RET_TYPE_ERROR
	}
	m := make(map[string]wbgong.Title)
	for value, title := range enumTitles {
		m[value] = make(wbgong.Title)
		langs, ok := title.(map[string]any)
		if !ok {
			wbgong.Error.Printf("setEnumTitles(): titles for %q must be an object {lang: title}", value)
			return duktape.DUK_RET_TYPE_ERROR
		}
		for k, v := range langs {
			str, ok := v.(string)
			if !ok {
				wbgong.Error.Printf("setEnumTitles(): title for %q/%q is not a string", value, k)
				return duktape.DUK_RET_TYPE_ERROR
			}
			m[value][k] = str
		}
	}

	jsonEnumTitles, _ := json.Marshal(m)

	ctrlProxy, duk_ret := engine.getControlFromCtx(ctx)
	if duk_ret < 0 {
		return duk_ret
	}

	ctrlProxy.SetMeta(wbgong.CONV_META_SUBTOPIC_CONTROL_ENUM, string(jsonEnumTitles))
	return 0
}

func (engine *ESEngine) esVdevCellSetType(ctx *ESContext) int {
	if !ctx.IsString(0) {
		wbgong.Error.Printf("setType(): bad parameters")
		return duktape.DUK_RET_ERROR
	}
	typeStr := ctx.GetString(0)

	ctrlProxy, duk_ret := engine.getControlFromCtx(ctx)
	if duk_ret < 0 {
		return duk_ret
	}

	ctrlProxy.SetMeta(wbgong.CONV_META_SUBTOPIC_TYPE, typeStr)

	return 0
}

func (engine *ESEngine) esVdevCellSetUnits(ctx *ESContext) int {
	if !ctx.IsString(0) {
		wbgong.Error.Printf("setUnits(): bad parameters")
		return duktape.DUK_RET_ERROR
	}
	unitsStr := ctx.GetString(0)

	ctrlProxy, duk_ret := engine.getControlFromCtx(ctx)
	if duk_ret < 0 {
		return duk_ret
	}

	ctrlProxy.SetMeta(wbgong.CONV_META_SUBTOPIC_UNITS, unitsStr)

	return 0
}

func (engine *ESEngine) esVdevCellSetReadonly(ctx *ESContext) int {
	if !ctx.IsBoolean(0) {
		wbgong.Error.Printf("setReadonly(): bad parameters")
		return duktape.DUK_RET_ERROR
	}
	readonly := ctx.GetBoolean(0)

	ctrlProxy, duk_ret := engine.getControlFromCtx(ctx)
	if duk_ret < 0 {
		return duk_ret
	}

	readonlyStr := wbgong.CONV_META_BOOL_FALSE
	if readonly {
		readonlyStr = wbgong.CONV_META_BOOL_TRUE
	}

	ctrlProxy.SetMeta(wbgong.CONV_META_SUBTOPIC_READONLY, readonlyStr)

	return 0
}

func (engine *ESEngine) esVdevCellSetMax(ctx *ESContext) int {
	if !ctx.IsNumber(0) {
		wbgong.Error.Printf("setMax(): bad parameters")
		return duktape.DUK_RET_ERROR
	}
	max := int(ctx.GetNumber(0))

	ctrlProxy, duk_ret := engine.getControlFromCtx(ctx)
	if duk_ret < 0 {
		return duk_ret
	}

	ctrlProxy.SetMeta(wbgong.CONV_META_SUBTOPIC_MAX, strconv.Itoa(max))

	return 0
}

func (engine *ESEngine) esVdevCellSetMin(ctx *ESContext) int {
	if !ctx.IsNumber(0) {
		wbgong.Error.Printf("setMin(): bad parameters")
		return duktape.DUK_RET_ERROR
	}
	min := int(ctx.GetNumber(0))

	ctrlProxy, duk_ret := engine.getControlFromCtx(ctx)
	if duk_ret < 0 {
		return duk_ret
	}

	ctrlProxy.SetMeta(wbgong.CONV_META_SUBTOPIC_MIN, strconv.Itoa(min))

	return 0
}

func (engine *ESEngine) esVdevCellSetPrecision(ctx *ESContext) int {
	if !ctx.IsNumber(0) {
		wbgong.Error.Printf("setPrecision(): bad parameters")
		return duktape.DUK_RET_ERROR
	}
	prec := float64(ctx.GetNumber(0))

	ctrlProxy, duk_ret := engine.getControlFromCtx(ctx)
	if duk_ret < 0 {
		return duk_ret
	}

	ctrlProxy.SetMeta(wbgong.CONV_META_SUBTOPIC_PRECISION, fmt.Sprintf("%f", prec))

	return 0
}

func (engine *ESEngine) esVdevCellSetError(ctx *ESContext) int {
	if !ctx.IsString(0) {
		wbgong.Error.Printf("setError(): bad parameters")
		return duktape.DUK_RET_ERROR
	}
	errorStr := ctx.GetString(0)

	ctrlProxy, duk_ret := engine.getControlFromCtx(ctx)
	if duk_ret < 0 {
		return duk_ret
	}

	cce := ctrlProxy.SetMeta(wbgong.CONV_META_SUBTOPIC_ERROR, errorStr)
	if cce != nil {
		engine.PushToEventBuffer(cce)
	}

	return 0
}

func (engine *ESEngine) esVdevCellSetOrder(ctx *ESContext) int {
	if !ctx.IsNumber(0) {
		wbgong.Error.Printf("setOrder(): bad parameters")
		return duktape.DUK_RET_ERROR
	}
	order := int(ctx.GetNumber(0))

	ctrlProxy, duk_ret := engine.getControlFromCtx(ctx)
	if duk_ret < 0 {
		return duk_ret
	}

	ctrlProxy.SetMeta(wbgong.CONV_META_SUBTOPIC_ORDER, strconv.Itoa(order))

	return 0
}

func (engine *ESEngine) esVdevCellSetValue(ctx *ESContext) int {
	var value any
	notifySubs := true

	ctrlProxy, duk_ret := engine.getControlFromCtx(ctx)
	if duk_ret < 0 {
		return duk_ret
	}

	if ctx.IsObject(0) {
		mAny := ctx.GetJSObject(0)
		if rc, thrown := throwOnConversionError(ctx, mAny); thrown {
			return rc
		}
		m, ok := mAny.(objx.Map)
		if !ok {
			wbgong.Error.Printf("setValue (%s/%s): bad parameters", ctrlProxy.devProxy.name, ctrlProxy.name)
			return duktape.DUK_RET_TYPE_ERROR
		}
		if !m.Has(JS_CTRLPROXY_FUNC_SETVALUE_VALUE) {
			wbgong.Error.Printf("setValue (%s/%s): no value parameter present", ctrlProxy.devProxy.name, ctrlProxy.name)
			return duktape.DUK_RET_TYPE_ERROR
		}
		value = m[JS_CTRLPROXY_FUNC_SETVALUE_VALUE]

		if m.Has(JS_CTRLPROXY_FUNC_SETVALUE_NOTIFY) {
			var obj = m.Get(JS_CTRLPROXY_FUNC_SETVALUE_NOTIFY)

			if !obj.IsBool() {
				wbgong.Error.Printf("setValue (%s/%s): notify field must be bool", ctrlProxy.devProxy.name, ctrlProxy.name)
				return duktape.DUK_RET_TYPE_ERROR
			}
			notifySubs = obj.Bool()
		}
	} else {
		value = ctx.GetJSObject(0)
	}

	// A non-nil error means the control disappeared (all other write failures
	// are reported inside SetValueAt); report it the same way, with the
	// caller's location - a write must never throw.
	if err := ctrlProxy.SetValueAt(value, notifySubs, engine.ruleCallSiteFunc(ctx)); err != nil {
		engine.Log(ENGINE_LOG_ERROR, engine.withRuleCallSite(ctx, err.Error()))
	}

	return 0
}

// ruleCallSite returns "<physical path>:<line>" of the innermost JS stack frame
// that belongs to a loaded rule file ("" if none) - the place in a rule that
// made the call currently executing. It costs a JS Error object, so callers
// use it lazily, on failure paths only (ruleCallSiteFunc).
func (engine *ESEngine) ruleCallSite(ctx *ESContext) string {
	tb := ctx.GetTraceback()
	engine.sourcesMtx.Lock()
	defer engine.sourcesMtx.Unlock()
	for _, loc := range tb {
		if _, loaded := engine.sources[loc.filename]; loaded {
			return fmt.Sprintf("%s:%d", loc.filename, loc.line)
		}
	}
	return ""
}

func (engine *ESEngine) ruleCallSiteFunc(ctx *ESContext) func() string {
	return func() string { return engine.ruleCallSite(ctx) }
}

// withRuleCallSite appends " at file:line" to msg when the call site is known.
func (engine *ESEngine) withRuleCallSite(ctx *ESContext, msg string) string {
	if at := engine.ruleCallSite(ctx); at != "" {
		return msg + " at " + at
	}
	return msg
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

	ctx.PushHeapStash()
	ctx.GetPropString(-1, CELL_OBJ_PROTO_NAME)
	ctx.SetPrototype(-3)
	ctx.Pop()

	return 1
}

func (engine *ESEngine) initCellObjectPrototype(ctx *ESContext) {
	ctx.PushHeapStash()
	defer ctx.Pop()

	ctx.PushObject()
	ctx.DefineFunctions(map[string]func(*ESContext) int{
		JS_DEVPROXY_FUNC_RAWVALUE: func(ctx *ESContext) int {
			ctx.PushThis()
			c, ok := ctx.GetGoObject(-1).(*ControlProxy)
			ctx.Pop()
			if !ok { // a cell method called with a foreign this
				return duktape.DUK_RET_TYPE_ERROR
			}

			ctx.PushString(c.RawValue())
			return 1
		},
		JS_DEVPROXY_FUNC_VALUE: func(ctx *ESContext) int {
			ctx.PushThis()
			c, ok := ctx.GetGoObject(-1).(*ControlProxy)
			ctx.Pop()
			if !ok { // a cell method called with a foreign this
				return duktape.DUK_RET_TYPE_ERROR
			}

			m := objx.New(map[string]any{
				JS_DEVPROXY_FUNC_VALUE_RET: c.Value(),
			})
			ctx.PushJSObject(m)
			return 1
		},
		JS_DEVPROXY_FUNC_SETVALUE: func(ctx *ESContext) int {
			ctx.PushThis()
			c, ok := ctx.GetGoObject(-1).(*ControlProxy)
			ctx.Pop()
			if !ok { // a cell method called with a foreign this
				return duktape.DUK_RET_TYPE_ERROR
			}

			if ctx.GetTop() != 1 || !ctx.IsObject(-1) {
				return duktape.DUK_RET_ERROR
			}
			mAny := ctx.GetJSObject(-1)
			if rc, thrown := throwOnConversionError(ctx, mAny); thrown {
				return rc
			}
			m, ok := mAny.(objx.Map)
			if !ok || !m.Has(JS_DEVPROXY_FUNC_SETVALUE_ARG) {
				wbgong.Error.Printf("invalid control definition")
				return duktape.DUK_RET_TYPE_ERROR
			}

			errSet := c.SetValueAt(m[JS_DEVPROXY_FUNC_SETVALUE_ARG], true, engine.ruleCallSiteFunc(ctx))
			if errSet != nil {
				engine.Log(ENGINE_LOG_ERROR, engine.withRuleCallSite(ctx, errSet.Error()))
			}
			return 1
		},
		JS_DEVPROXY_FUNC_SETMETA: func(ctx *ESContext) int {
			ctx.PushThis()
			c, ok := ctx.GetGoObject(-1).(*ControlProxy)
			ctx.Pop()
			if !ok { // a cell method called with a foreign this
				return duktape.DUK_RET_TYPE_ERROR
			}

			if ctx.GetTop() != 1 || !ctx.IsObject(-1) {
				return duktape.DUK_RET_ERROR
			}
			mAny := ctx.GetJSObject(-1)
			if rc, thrown := throwOnConversionError(ctx, mAny); thrown {
				return rc
			}
			m, ok := mAny.(objx.Map)
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
			c, ok := ctx.GetGoObject(-1).(*ControlProxy)
			ctx.Pop()
			if !ok { // a cell method called with a foreign this
				return duktape.DUK_RET_TYPE_ERROR
			}

			ctx.PushBoolean(c.IsComplete())
			return 1
		},
		JS_DEVPROXY_FUNC_GETMETA: func(ctx *ESContext) int {
			ctx.PushThis()
			c, ok := ctx.GetGoObject(-1).(*ControlProxy)
			ctx.Pop()
			if !ok { // a cell method called with a foreign this
				return duktape.DUK_RET_TYPE_ERROR
			}

			ctrlMeta := c.GetMeta()
			if ctrlMeta == nil {
				ctx.PushNull()
				return 1
			}

			dataMap := make(map[string]any)
			for key, value := range ctrlMeta {
				dataMap[key] = value
			}
			m := objx.New(dataMap)
			ctx.PushJSObject(m)

			return 1
		},
	})
	ctx.PutPropString(-2, CELL_OBJ_PROTO_NAME)
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
	if periodic && ms <= MIN_INTERVAL_LOW_THRESHOLD_MS {
		engine.Log(ENGINE_LOG_WARNING, fmt.Sprintf("_wbStartTimer: %d ms interval may degrade performance", int(ms)))
	}

	var callback func()
	var callbackKey ESCallback
	if name == NO_TIMER_NAME {
		callbackKey = ctx.storeCallback(0)
		key := callbackKey
		callback = func() {
			currentFilename := ctx.GetCurrentFilename()
			if currentFilename != "" {
				engine.cleanup.PushCleanupScope(currentFilename)
				defer engine.cleanup.PopCleanupScope(currentFilename)
			}
			ctx.invokeCallback(key, nil)
		}
	}

	interval := time.Duration(ms * float64(time.Millisecond))

	// get timer id
	timerId := engine.StartTimer(name, callback, interval, periodic)

	// add timer to script cleanup
	engine.handleTimerCleanup(ctx, timerId)

	if callbackKey != 0 {
		// The callback dies with the timer: sweep its stash entry the moment
		// the engine forgets the timer (fired one-shot, clearTimeout, file
		// cleanup) instead of waiting for a Go finalizer. The deferred sweep
		// let setTimeout+clearTimeout churn pile entries up between Go GCs,
		// and the JS atom table keeps that high-water mark forever. The hook
		// runs on the engine loop with no engine locks held (see removeTimer);
		// on an invalidated context RemoveCallback is a no-op - the entry was
		// already swept.
		key := callbackKey
		engine.OnTimerRemoveByIndex(timerId, func() {
			ctx.RemoveCallback(key)
		})
	}

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

	// The callback fires exactly once (result or spawnError), so its stash
	// entry is stored by key and swept right after that invocation - the
	// finalizer-driven sweep let heavy spawn churn pile entries up between
	// Go GCs (see esWbStartTimer for the same pattern on timers). If the
	// engine stops before the command finishes, the dropped completion is
	// recorded (noteOrphanedCallback) and the entry swept at the next
	// Start/Stop/Close; if the file reloads first, invalidate() has already
	// swept it and both invoke and remove are no-ops on the invalid context.
	hasCallback := false
	var callbackKey ESCallback

	if ctx.IsFunction(1) {
		callbackKey = ctx.storeCallback(1)
		hasCallback = true
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
	spawnFn := engine.spawnFunc
	if spawnFn == nil {
		spawnFn = Spawn
	}
	// invoke runs the stored callback once and sweeps its stash entry.
	invokeCallbackOnce := func(args objx.Map) {
		defer ctx.RemoveCallback(callbackKey)
		ctx.invokeCallback(callbackKey, args)
	}

	go func() {
		r, err := spawnFn(args[0], args[1:], captureOutput, captureErrorOutput, input)
		if err != nil {
			wbgong.Error.Printf("external command failed: %v", err)
			if hasCallback {
				spawnErr := err.Error()
				dropErr := engine.MaybeCallSync(func() {
					if !ctx.IsValid() {
						return
					}
					currentFilename := ctx.GetCurrentFilename()
					if currentFilename != "" {
						engine.cleanup.PushCleanupScope(currentFilename)
						defer engine.cleanup.PopCleanupScope(currentFilename)
					}
					// lib.js turns this into a promise rejection; the legacy
					// exitCallback contract never fired for exec failures and
					// still does not
					invokeCallbackOnce(objx.New(map[string]any{"spawnError": spawnErr}))
				})
				if dropErr != nil {
					// stopped engine dropped the thunk: the invocation that
					// would have swept the stash entry never runs - record
					// the key for the next single-threaded sweep
					engine.noteOrphanedCallback(ctx, callbackKey)
				}
			}
			return
		}
		if hasCallback {
			// MaybeCallSync: the engine may already be stopping and the
			// thunk dropped - see the orphan handling below
			dropErr := engine.MaybeCallSync(func() {
				// check that context is still alive
				// (file is not removed or reloaded)
				if !ctx.IsValid() {
					wbgong.Info.Println("ignore runShellCommand callback without Duktape context (maybe script is reloaded or removed)")
					return
				}

				currentFilename := ctx.GetCurrentFilename()
				if currentFilename != "" {
					engine.cleanup.PushCleanupScope(currentFilename)
					defer engine.cleanup.PopCleanupScope(currentFilename)
				}

				args := objx.New(map[string]any{
					"exitStatus": r.ExitStatus,
				})
				if captureOutput {
					args["capturedOutput"] = r.CapturedOutput
				}
				args["capturedErrorOutput"] = r.CapturedErrorOutput
				invokeCallbackOnce(args)
			})
			if dropErr != nil {
				// the stopped engine dropped this completion, so the
				// callback's stash entry was never swept; without this the
				// key (and the realm it pins) would leak across a restart
				engine.noteOrphanedCallback(ctx, callbackKey)
			}
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
		engine.Log(ENGINE_LOG_ERROR, "bad rule definition")
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
			fmt.Sprintf("defineRule error: %v", err))
		return duktape.DUK_RET_ERROR
	}

	engine.registerSourceItem(ctx, SOURCE_ITEM_RULE, name)

	// return rule ID
	ctx.PushNumber(float64(ruleId))
	return 1
}

// esWbAddCleanup registers a JS callback that runs when the calling file
// is unloaded (reload, removal, engine stop) - the JS-side counterpart of
// the Go cleanup scopes. Modules use it to release state the engine does
// not know about (the MQTT-RPC module clears its retained availability
// topics). The callback runs on the engine loop, before the file's realm
// is invalidated, so it may still publish; errors it throws are logged
// like any other callback error.
//
// Arguments:
// 1 - callback function
func (engine *ESEngine) esWbAddCleanup(ctx *ESContext) int {
	if ctx.GetTop() != 1 || !ctx.IsFunction(0) {
		engine.Log(ENGINE_LOG_ERROR, "invalid _wbAddCleanup call, expected a function")
		return duktape.DUK_RET_TYPE_ERROR
	}
	currentFilename := ctx.GetCurrentFilename()
	if currentFilename == "" {
		// the shared realm (lib.js) is never unloaded: nothing to run
		return 0
	}
	callback := ctx.WrapCallback(0)
	engine.cleanup.PushCleanupScope(currentFilename)
	defer engine.cleanup.PopCleanupScope(currentFilename)
	engine.cleanup.AddCleanup(func() {
		callback(nil)
	})
	return 0
}

// trackMqtt(topic, callback[, options]) - options.cache=false turns off
// the last-value replay for the subscription (see DefineMqttTracker)
func (engine *ESEngine) trackMqtt(ctx *ESContext) int {
	if ctx.GetTop() < 2 || !ctx.IsString(0) || !ctx.IsFunction(1) {
		engine.Log(ENGINE_LOG_ERROR, "bad track definition")
		return duktape.DUK_RET_ERROR
	}
	topic := ctx.GetString(0)
	cache := true
	if ctx.GetTop() >= 3 && ctx.IsObject(2) {
		ctx.GetPropString(2, "cache")
		if ctx.IsBoolean(-1) {
			cache = ctx.ToBoolean(-1)
		}
		ctx.Pop()
	}
	// the callback must be on top: DefineMqttTracker wraps stack index -1
	for ctx.GetTop() > 2 {
		ctx.Pop()
	}

	currentFilename := ctx.GetCurrentFilename()
	if currentFilename != "" {
		engine.cleanup.PushCleanupScope(currentFilename)
		defer engine.cleanup.PopCleanupScope(currentFilename)
	}

	engine.DefineMqttTracker(topic, ctx, cache)

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
		rule.SetState(state, engine.cron)
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
		engine.Log(ENGINE_LOG_ERROR, "invalid runRule call")
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
	numArgs := ctx.GetTop()
	logErrorOnNoFile := true

	argsValid := (numArgs == 1 || numArgs == 2) && ctx.IsString(0)
	if numArgs == 2 {
		argsValid = argsValid && ctx.IsObject(1)
	}

	if !argsValid {
		engine.Log(ENGINE_LOG_ERROR, "invalid readConfig call, should be readConfig(path [, params])")
		return duktape.DUK_RET_ERROR
	}

	if numArgs == 2 {
		paramsAny := ctx.GetJSObject(1)
		if rc, thrown := throwOnConversionError(ctx, paramsAny); thrown {
			return rc
		}
		params, ok := paramsAny.(objx.Map)
		if !ok {
			engine.Log(ENGINE_LOG_ERROR, "invalid readConfig call, params must be an object")
			return duktape.DUK_RET_TYPE_ERROR
		}
		if params.Has("logErrorOnNoFile") {
			logErrorOnNoFile, _ = params["logErrorOnNoFile"].(bool)
		}
	}

	path := ctx.GetString(0)
	readFile := engine.readConfigFunc
	if readFile == nil {
		readFile = os.ReadFile
	}
	data, err := readFile(path)

	if err != nil {
		if logErrorOnNoFile {
			engine.Log(ENGINE_LOG_ERROR, "failed to open config file: "+path)
		}
		return duktape.DUK_RET_ERROR
	}

	reader := JsonConfigReader.New(bytes.NewReader(data))
	preprocessedContent, err := io.ReadAll(reader)
	if err != nil {
		// JsonConfigReader doesn't produce its own errors, thus
		// any errors returned from it are I/O errors.
		engine.Log(ENGINE_LOG_ERROR, "failed to read config file: "+path)
		return duktape.DUK_RET_ERROR
	}

	parsedJSON, err := objx.FromJSON(string(preprocessedContent))
	if err != nil {
		engine.Log(ENGINE_LOG_ERROR, "failed to parse json: "+path)
		return duktape.DUK_RET_ERROR
	}
	ctx.PushJSObject(parsedJSON)
	return 1
}

func (engine *ESEngine) EvalScript(code string) error {
	var evalErr error
	// fail fast on a stopped engine: the old unconditional channel receive
	// waited forever for a thunk CallSync had silently dropped
	if err := engine.CallSyncWait(func() {
		evalErr = engine.globalCtx.EvalScript(code)
		if evalErr != nil {
			engine.Logf(ENGINE_LOG_ERROR, "eval error: %v", evalErr)
		}
	}); err != nil {
		return err
	}
	return evalErr
}

// Persistent storage features

// Create or open DB file
func (engine *ESEngine) SetPersistentDB(filename string) error {
	return engine.SetPersistentDBMode(filename, PERSISTENT_DB_CHMOD)
}

func (engine *ESEngine) SetPersistentDBMode(filename string, mode os.FileMode) (err error) {
	if engine.persistentDB != nil {
		engine.Log(ENGINE_LOG_ERROR, "persistent storage DB is already opened")
		err = fmt.Errorf("persistent storage DB is already opened")
		return
	}

	openTimeout := engine.persistentDBOpenTimeout
	if openTimeout <= 0 {
		openTimeout = 1 * time.Second // production default, unchanged
	}
	engine.persistentDB, err = bolt.Open(filename, mode,
		&bolt.Options{Timeout: openTimeout})

	if err != nil {
		engine.Log(ENGINE_LOG_ERROR, fmt.Sprintf("can't open persistent DB file: %v", err))
		return
	}

	return nil
}

// Force close DB
func (engine *ESEngine) ClosePersistentDB() (err error) {
	if engine.persistentDB == nil {
		engine.Log(ENGINE_LOG_ERROR, "DB is not opened, nothing to close")
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
		engine.Log(ENGINE_LOG_ERROR, "persistent DB is not initialized")
		return duktape.DUK_RET_ERROR
	}

	// arguments: (name [, options = { global bool }])
	var name string
	var global bool

	numArgs := ctx.GetTop()

	if numArgs < 1 || numArgs > 2 {
		engine.Log(ENGINE_LOG_ERROR, "bad persistent storage definition")
		return duktape.DUK_RET_ERROR
	}

	// parse name
	if !ctx.IsString(0) {
		engine.Log(ENGINE_LOG_ERROR, "persistent storage name must be string")
		return duktape.DUK_RET_ERROR
	}
	name = ctx.GetString(0)

	// parse options object
	if numArgs == 2 && !ctx.IsUndefined(1) {
		if !ctx.IsObject(1) {
			engine.Log(ENGINE_LOG_ERROR, "persistent storage options must be object")
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
		engine.Log(ENGINE_LOG_INFO, "create local storage name: "+name)
	}

	// push name as return value
	ctx.PushString(name)

	return 1
}

// Writes new value down to persistent DB
func (engine *ESEngine) esPersistentSet(ctx *ESContext) int {
	if engine.persistentDB == nil {
		engine.Log(ENGINE_LOG_ERROR, "persistent DB is not initialized")
		return duktape.DUK_RET_ERROR
	}

	// arguments: (bucket string, key string, value)
	var bucket, key, value string
	var shouldDelete bool

	if ctx.GetTop() != 3 {
		engine.Log(ENGINE_LOG_ERROR, "bad persistentSet request, arg number mismatch")
		return duktape.DUK_RET_ERROR
	}

	// parse bucket name
	if !ctx.IsString(0) {
		engine.Log(ENGINE_LOG_ERROR, "persistent storage bucket name must be string")
		return duktape.DUK_RET_ERROR
	}
	bucket = ctx.GetString(0)

	// parse key
	if !ctx.IsString(1) {
		engine.Log(ENGINE_LOG_ERROR, "persistent storage key must be string")
		return duktape.DUK_RET_ERROR
	}
	key = ctx.GetString(1)

	if ctx.IsNullOrUndefined(2) {
		shouldDelete = true
	} else {
		shouldDelete = false
		// Serialize the value; a non-serializable one (e.g. an object with a
		// reference cycle) must throw to the WRITING rule - storing a bogus
		// value silently, or reporting it through an unrelated log channel,
		// hides the bug from its author.
		var encErr error
		value, encErr = ctx.JsonEncodeChecked(2)
		if encErr != nil {
			ctx.PushErrorObject(duktape.DUK_ERR_ERROR,
				fmt.Sprintf("persistent storage %s/%s: cannot serialize value: %s", bucket, key, encErr))
			return duktape.DUK_RET_INSTACK_ERROR
		}
	}

	// perform a transaction
	engine.persistentDB.Update(func(tx *bolt.Tx) error {
		b, err := tx.CreateBucketIfNotExists([]byte(bucket))
		if err != nil {
			return err
		}

		if shouldDelete {
			if err := b.Delete([]byte(key)); err != nil {
				return err
			}
			return nil
		}

		if err := b.Put([]byte(key), []byte(value)); err != nil {
			return err
		}
		return nil
	})

	if shouldDelete {
		wbgong.Debug.Printf("delete value from persistent storage %s: '%s'", bucket, key)
	} else {
		wbgong.Debug.Printf("write value to persistent storage %s: '%s' <= '%s'", bucket, key, value)
	}

	return 0
}

// Gets a value from persitent DB
func (engine *ESEngine) esPersistentGet(ctx *ESContext) int {
	if engine.persistentDB == nil {
		engine.Log(ENGINE_LOG_ERROR, "persistent DB is not initialized")
		return duktape.DUK_RET_ERROR
	}

	// arguments: (bucket string, key string)
	var bucket, key, value string

	if ctx.GetTop() != 2 {
		engine.Log(ENGINE_LOG_ERROR, "bad persistentGet request, arg number mismatch")
		return duktape.DUK_RET_ERROR
	}

	// parse bucket name
	if !ctx.IsString(0) {
		engine.Log(ENGINE_LOG_ERROR, "persistent storage bucket name must be string")
		return duktape.DUK_RET_ERROR
	}
	bucket = ctx.GetString(0)

	// parse key
	if !ctx.IsString(1) {
		engine.Log(ENGINE_LOG_ERROR, "persistent storage key must be string")
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
		src, err := os.ReadFile(path)

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

	// throw a descriptive Error instead of a bare rc code so script errors
	// read "cannot find module 'X'" in the UI and logs
	ctx.PushErrorObject(duktape.DUK_ERR_ERROR, fmt.Sprintf("cannot find module %q", id))
	return duktape.DUK_RET_INSTACK_ERROR
}

// CheckTsFile serves the Editor.Check RPC from the background-check
// verdict cache - every .ts/.js load/save triggers a check, so this is a
// cheap read that never blocks the serially-dispatched RPC loop on a
// tsgo process. Status values: "ready" (diags valid), "pending" (check
// in flight, poll again), "not-ts" (not a checkable rule file: a .d.ts,
// a disabled file), "unsupported" (engine runs without tsgo).
func (engine *ESEngine) CheckTsFile(physicalPath string) ([]TSDiag, string) {
	// tsc is nil only with -tsgo=""; a configured-but-missing binary is
	// also "unsupported" until it appears (Available is a stateless
	// LookPath - safe from the RPC goroutine)
	if engine.tsc == nil || !engine.tsc.Available() || !engine.tsc.CheckSupported() {
		return nil, TS_CHECK_UNSUPPORTED
	}
	checkable := (strings.HasSuffix(physicalPath, ".ts") || strings.HasSuffix(physicalPath, ".js")) &&
		!strings.HasSuffix(physicalPath, ".d.ts")
	if !checkable {
		return nil, TS_CHECK_NOT_TS
	}
	engine.tsCheckMu.Lock()
	entry, found := engine.tsCheckResults[physicalPath]
	engine.tsCheckMu.Unlock()
	if !found {
		// loaded while tsgo was absent (a .js file runs without it), so no
		// check was ever scheduled: schedule one now that tsgo is here, and
		// answer pending - the next poll gets the verdict. Engine loop only
		// (tsCheckGen); MaybeCallSync waits for the engine loop like every
		// other editor operation but never blocks on a stopped engine.
		engine.MaybeCallSync(func() {
			engine.sourcesMtx.Lock()
			_, loaded := engine.sources[physicalPath]
			engine.sourcesMtx.Unlock()
			if !loaded {
				return
			}
			engine.tsCheckMu.Lock()
			_, scheduled := engine.tsCheckResults[physicalPath]
			engine.tsCheckMu.Unlock()
			if !scheduled {
				engine.scheduleTsCheck(physicalPath)
			}
		})
		return nil, TS_CHECK_PENDING
	}
	if !entry.ready {
		return nil, TS_CHECK_PENDING
	}
	return entry.diags, TS_CHECK_READY
}

// TsTypesContent returns the installed wb-rules.d.ts declarations.
func (engine *ESEngine) TsTypesContent() (string, error) {
	if engine.tsTypesPath == "" {
		return "", fmt.Errorf("no type declarations configured")
	}
	content, err := os.ReadFile(engine.tsTypesPath)
	if err != nil {
		return "", err
	}
	return string(content), nil
}
