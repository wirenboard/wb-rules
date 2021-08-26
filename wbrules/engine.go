package wbrules

import (
	"errors"
	"fmt"
	"log"
	"sort"
	"strconv"
	"sync"
	"sync/atomic"
	"time"

	"github.com/alexcesaro/statsd"
	"github.com/stretchr/objx"
	"github.com/wirenboard/wbgong"
	cron "gopkg.in/robfig/cron.v1"
)

type EngineLogLevel int
type TimerId uint64

const (
	NO_TIMER_NAME                 = ""
	RULES_CAPACITY                = 256
	NO_CALLBACK                   = ESCallback(0)
	RULE_ENGINE_SETTINGS_DEV_NAME = "wbrules"
	RULE_DEBUG_CELL_NAME          = "Rule debugging"

	SYNC_QUEUE_LEN = 32

	ENGINE_LOG_DEBUG = EngineLogLevel(iota)
	ENGINE_LOG_INFO
	ENGINE_LOG_WARNING
	ENGINE_LOG_ERROR

	ENGINE_CONTROL_CHANGE_QUEUE_LEN     = 16
	ENGINE_CONTROL_CHANGE_SUBS_CAPACITY = 2
	ENGINE_CONTROL_RULES_CAPACITY       = 8
	ENGINE_NOTED_CONTROLS_CAPACITY      = 4

	ENGINE_EVENT_BUFFER_CAP = 16

	ENGINE_UNINITIALIZED_RULES_CAPACITY = 16

	ENGINE_ACTIVE = 1
	ENGINE_STOP   = 0

	ATOMIC_TRUE  = 1
	ATOMIC_FALSE = 0

	ENGINE_CALLSYNC_TIMEOUT = 120 * time.Second

	ENGINE_STATSD_POLL_INTERVAL = 5 * time.Second
	ENGINE_STATSD_PREFIX        = "engine"
)

// errors
var (
	ControlNotFoundError = errors.New("Control is not found")
)

type ControlSpec struct {
	DeviceId  string
	ControlId string
}

func (c *ControlSpec) String() string {
	return c.DeviceId + "/" + c.ControlId
}

type TimerFunc func(id TimerId, d time.Duration, periodic bool) wbgong.Timer

func newTimer(id TimerId, d time.Duration, periodic bool) wbgong.Timer {
	if periodic {
		return wbgong.NewRealTicker(d)
	} else {
		return wbgong.NewRealTimer(d)
	}
}

type TimerEntry struct {
	sync.Mutex
	timer          wbgong.Timer
	periodic       bool
	quit, quitted  chan struct{}
	name           string
	thunk          func()
	active         bool
	onRemoveHndlrs []func()
}

func (entry *TimerEntry) stop() {
	entry.Lock()
	defer entry.Unlock()
	if entry.quit != nil {
		close(entry.quit)
		// make sure the timer is really stopped before continuing
		<-entry.quitted
	}
	entry.active = false
}

func (entry *TimerEntry) onRemove(thunk func()) {
	entry.onRemoveHndlrs = append(entry.onRemoveHndlrs, thunk)
}

func (entry *TimerEntry) handleRemove() {
	for i := range entry.onRemoveHndlrs {
		entry.onRemoveHndlrs[i]()
	}
}

type proxyOwner interface {
	Driver() wbgong.Driver
	getRev() uint32
	trackControlSpec(ControlSpec)
}

type DeviceProxy struct {
	owner proxyOwner
	name  string
	dev   wbgong.Device
	rev   uint32
}

// ControlProxy tracks control access with the engine
// and makes sure that always the actual current device
// control object is accessed while avoiding excess
// name lookups.
type ControlProxy struct {
	sync.Mutex

	devProxy *DeviceProxy
	name     string
	control  wbgong.Control

	cachedValue interface{}
	cacheValid  bool
}

func getDeviceRefFromDriver(devId string, drv wbgong.Driver) (dev wbgong.Device, err error) {
	err = drv.Access(func(tx wbgong.DriverTx) error {
		dev = tx.GetDevice(devId)
		return nil
	})
	return
}

// You might wont to return error from here, but be careful,
// some rules want control spec without actual control
func makeDeviceProxy(owner proxyOwner, devId string) *DeviceProxy {
	dev, _ := getDeviceRefFromDriver(devId, owner.Driver())
	return &DeviceProxy{owner, devId, dev, owner.getRev()}
}

func (devProxy *DeviceProxy) updated() bool {
	return (devProxy.rev != devProxy.owner.getRev())
}

func (devProxy *DeviceProxy) EnsureControlProxy(ctrlId string) *ControlProxy {
	if wbgong.DebuggingEnabled() {
		wbgong.Debug.Printf("[devProxy] EnsureControlProxy for control %s/%s", devProxy.name, ctrlId)
	}
	return &ControlProxy{
		devProxy:    devProxy,
		name:        ctrlId,
		control:     devProxy.getControl(ctrlId),
		cachedValue: nil,
		cacheValid:  false,
	}
}

func (devProxy *DeviceProxy) getControl(ctrlId string) wbgong.Control {
	devId := devProxy.name

	if wbgong.DebuggingEnabled() {
		wbgong.Debug.Printf("[devProxy] getControl for control %s/%s", devId, ctrlId)
	}

	var c wbgong.Control
	devProxy.owner.Driver().Access(func(tx wbgong.DriverTx) error {
		dev := tx.GetDevice(devId)
		if dev == nil {
			return nil // TODO: careful with error here, some rules want control spec without control itself
		}
		c = dev.GetControl(ctrlId)
		return nil
	})

	return c
}

func (devProxy *DeviceProxy) controlsList() []wbgong.Control {
	devId := devProxy.name

	if wbgong.DebuggingEnabled() {
		wbgong.Debug.Printf("[devProxy] controlsList for device %s", devId)
	}

	var c []wbgong.Control
	devProxy.owner.Driver().Access(func(tx wbgong.DriverTx) error {
		dev := tx.GetDevice(devId)
		if dev == nil {
			return nil // TODO: careful with error here, some rules want control spec without control itself
		}
		c = dev.ControlsList()
		return nil
	})

	return c
}

func (devProxy *DeviceProxy) isVirtual() (isLocal bool, err error) {
	devId := devProxy.name

	if wbgong.DebuggingEnabled() {
		wbgong.Debug.Printf("[devProxy] isVirtual for device %s", devId)
	}

	err = devProxy.owner.Driver().Access(func(tx wbgong.DriverTx) error {
		dev := tx.GetDevice(devId)
		if dev == nil {
			return wbgong.DeviceNotExistError // TODO: careful with error here, some rules want control spec without control itself
		}
		_, isLocal = dev.(wbgong.LocalDevice)
		return nil
	})

	return
}

func (ctrlProxy *ControlProxy) updateValueHandler(ctrl wbgong.Control, value interface{},
	prevValue interface{}, tx wbgong.DriverTx) error {
	ctrlProxy.Lock()
	defer ctrlProxy.Unlock()

	ctrlProxy.cacheValid = true
	ctrlProxy.cachedValue = value

	return nil
}

// just a syntax sugar
func (ctrlProxy *ControlProxy) accessDriver(f func(tx wbgong.DriverTx) error) error {
	return ctrlProxy.devProxy.owner.Driver().Access(f)
}

func (ctrlProxy *ControlProxy) getControl() wbgong.Control {
	if ctrlProxy.devProxy.updated() {
		if wbgong.DebuggingEnabled() {
			wbgong.Debug.Printf("[controlProxy %s/%s] cache invalidate!", ctrlProxy.devProxy.name, ctrlProxy.name)
		}
		ctrlProxy.Lock()
		ctrlProxy.cacheValid = false
		// FIXME: reset value handler on the old control if any
		ctrlProxy.Unlock()

		ctrlProxy.control = ctrlProxy.devProxy.getControl(ctrlProxy.name)
	}

	ctrlProxy.devProxy.owner.trackControlSpec(ControlSpec{ctrlProxy.devProxy.name, ctrlProxy.name})
	return ctrlProxy.control
}

// TODO: return error on non-existing/incomplete control
func (ctrlProxy *ControlProxy) RawValue() (v string) {
	ctrl := ctrlProxy.getControl()
	if ctrl == nil {
		return ""
	}

	ctrlProxy.accessDriver(func(tx wbgong.DriverTx) error {
		ctrl.SetTx(tx)
		v = ctrl.GetRawValue()
		return nil
	})
	return
}

func (ctrlProxy *ControlProxy) GetMeta() (m wbgong.MetaInfo) {
	ctrl := ctrlProxy.getControl()
	if ctrl == nil {
		return nil
	}

	ctrlProxy.accessDriver(func(tx wbgong.DriverTx) error {
		ctrl.SetTx(tx)
		m = ctrl.GetMeta()
		return nil
	})
	return
}

// TODO: return error on non-existing/incomplete control
func (ctrlProxy *ControlProxy) Value() (v interface{}) {
	if wbgong.DebuggingEnabled() {
		wbgong.Debug.Printf("[ctrlProxy] getting value of control %s/%s", ctrlProxy.devProxy.name, ctrlProxy.name)
	}

	ctrl := ctrlProxy.getControl()
	if ctrl == nil {
		return nil
	}

	isLocal := false
	// check cached value first
	ctrlProxy.Lock()
	if ctrlProxy.cacheValid {
		v = ctrlProxy.cachedValue
		ctrlProxy.Unlock()
	} else {
		// update cache value
		ctrlProxy.Unlock()
		err := ctrlProxy.accessDriver(func(tx wbgong.DriverTx) (err error) {
			ctrl.SetTx(tx)
			v, err = ctrl.GetValue()
			if err != nil {
				return
			}

			_, isLocal = ctrl.GetDevice().(wbgong.LocalDevice)
			// set update value handler to keep cache clear and fresh
			if isLocal {
				ctrl.SetOnValueReceiveHandler(ctrlProxy.updateValueHandler)
			} else {
				ctrl.SetValueUpdateHandler(ctrlProxy.updateValueHandler)
			}
			return
		})

		// update cache value and set validation flag
		if err != nil {
			v = nil
		} else {
			ctrlProxy.Lock()
			ctrlProxy.cachedValue = v
			ctrlProxy.cacheValid = true
			ctrlProxy.Unlock()
		}
	}

	if wbgong.DebuggingEnabled() {
		wbgong.Debug.Printf("[ctrlProxy] getValue(%s/%s): %v", ctrlProxy.devProxy.name, ctrlProxy.name, v)
	}
	return
}

func (ctrlProxy *ControlProxy) SetValue(value interface{}, notifySubs bool) {
	if wbgong.DebuggingEnabled() {
		wbgong.Debug.Printf("[ctrlProxy %s/%s] SetValue(%v)", ctrlProxy.devProxy.name, ctrlProxy.name, value)
	}

	ctrl := ctrlProxy.getControl()
	if ctrl == nil {
		wbgong.Error.Printf("failed to SetValue for unexisting control %s/%s: %v", ctrlProxy.devProxy.name, ctrlProxy.name, value)
		return
	}

	isLocal := false
	prevValue := ctrlProxy.Value()

	err := ctrlProxy.accessDriver(func(tx wbgong.DriverTx) error {
		ctrl.SetTx(tx)

		_, isLocal = ctrl.GetDevice().(wbgong.LocalDevice)
		if isLocal {
			return ctrl.UpdateValue(value, notifySubs)()
		} else {
			return ctrl.SetOnValue(value)()
		}
	})

	if isLocal && notifySubs {
		// run update value handler immediately, don't wait for wbgong backend
		ctrlProxy.updateValueHandler(nil, value, prevValue, nil)
	}

	if err != nil {
		wbgong.Error.Printf("control %s/%s SetValue() error: %s", ctrlProxy.devProxy.name, ctrlProxy.name, err)
	}
}

// SetMeta sets meta field of control
func (ctrlProxy *ControlProxy) SetMeta(key, metaValue string) (cce *ControlChangeEvent) {
	if wbgong.DebuggingEnabled() {
		wbgong.Debug.Printf("[ctrlProxy %s/%s] SetMeta(%v=%v)", ctrlProxy.devProxy.name, ctrlProxy.name, key, metaValue)
	}

	ctrl := ctrlProxy.getControl()
	if ctrl == nil {
		wbgong.Error.Printf("failed to SetMeta for unexisting control")
		return
	}

	var spec ControlSpec
	isComplete := false
	isRetained := false
	var prevMetaValue string

	isLocal := false
	errAccess := ctrlProxy.accessDriver(func(tx wbgong.DriverTx) error {
		ctrl.SetTx(tx)
		_, isLocal = ctrl.GetDevice().(wbgong.LocalDevice)
		if !isLocal {
			return wbgong.ExternalControlError
		}
		isComplete = ctrl.IsComplete()
		isRetained = ctrl.IsRetained()

		allMeta := ctrl.GetMeta()
		var ok bool
		if prevMetaValue, ok = allMeta[key]; !ok {
			prevMetaValue = ""
		}

		ctrlID := fmt.Sprintf("%s#%s", ctrl.GetId(), key)
		spec = ControlSpec{ctrl.GetDevice().GetId(), ctrlID}

		switch key {
		case wbgong.CONV_META_SUBTOPIC_DESCRIPTION:
			err := ctrl.SetDescription(metaValue)()
			if err != nil {
			}
		case wbgong.CONV_META_SUBTOPIC_ERROR:
			return ctrl.SetError(errors.New(metaValue))()
		case wbgong.CONV_META_SUBTOPIC_MAX:
			if max, err := strconv.Atoi(metaValue); err != nil {
				return err
			} else {
				return ctrl.SetMax(max)()
			}
		case wbgong.CONV_META_SUBTOPIC_ORDER:
			if order, err := strconv.Atoi(metaValue); err != nil {
				return err
			} else {
				return ctrl.SetOrder(order)()
			}
		case wbgong.CONV_META_SUBTOPIC_READONLY:
			if v, err := wbgong.RawValueToDataTyped(metaValue, wbgong.CONV_DATATYPE_BOOLEAN); err != nil {
				return err
			} else {
				return ctrl.SetReadonly(v.(bool))()
			}
		case wbgong.CONV_META_SUBTOPIC_TYPE:
			return ctrl.SetType(metaValue)()
		case wbgong.CONV_META_SUBTOPIC_UNITS:
			return ctrl.SetUnits(metaValue)()
		}
		return nil
	})

	if errAccess != nil {
		wbgong.Error.Printf("control %s/%s SetMeta(%s=%s) error: %s", ctrlProxy.devProxy.name,
			ctrlProxy.name, key, metaValue, errAccess)
		return
	}
	cce = &ControlChangeEvent{
		Spec:       spec,
		IsComplete: isComplete,
		IsRetained: isRetained,
		Value:      metaValue,
		PrevValue:  prevMetaValue,
	}
	return
}

// FIXME: error handling here
func (ctrlProxy *ControlProxy) IsComplete() (v bool) {
	ctrl := ctrlProxy.getControl()
	if ctrl == nil {
		return false
	}

	_ = ctrlProxy.accessDriver(func(tx wbgong.DriverTx) error {
		ctrl.SetTx(tx)
		v = ctrl.IsComplete()
		return nil
	})
	return v
}

// cronProxy helps to avoid race conditions when
// invoking cron funcs
type cronProxy struct {
	Cron
	exec func(func())
}

func newCronProxy(cron Cron, exec func(func())) *cronProxy {
	return &cronProxy{cron, exec}
}

func (cp cronProxy) AddFunc(spec string, cmd func()) error {
	return cp.Cron.AddFunc(spec, func() {
		cp.exec(cmd)
	})
}

// ControlChangeEvent
type ControlChangeEvent struct {
	Spec       ControlSpec
	IsComplete bool
	IsRetained bool
	Value      interface{}
	PrevValue  interface{}
}

type RuleEngineOptions struct {
	debugQueues   bool
	cleanupOnStop bool
	Statsd        wbgong.StatsdClientWrapper
}

func NewRuleEngineOptions() *RuleEngineOptions {
	return &RuleEngineOptions{
		debugQueues:   false,
		cleanupOnStop: false,
	}
}

func (o *RuleEngineOptions) SetTesting(v bool) *RuleEngineOptions {
	o.debugQueues = v
	return o
}

func (o *RuleEngineOptions) SetCleanupOnStop(v bool) *RuleEngineOptions {
	o.cleanupOnStop = v
	return o
}

func (o *RuleEngineOptions) SetStatsdClient(c wbgong.StatsdClientWrapper) *RuleEngineOptions {
	o.Statsd = c
	return o
}

type RuleEngine struct {
	active          uint32 // atomic
	cleanup         *ScopedCleanup
	rev             uint32 // atomic
	syncQueueActive bool
	syncQueue       chan func()
	syncQuitCh      chan chan struct{}
	mqttClient      wbgong.MQTTClient // for service
	driver          wbgong.Driver
	driverReadyCh   chan struct{}

	eventBuffer *EventBuffer

	timerFunc   TimerFunc
	nextTimerId TimerId

	timersMutex sync.Mutex
	timers      map[TimerId]*TimerEntry

	callbackIndex ESCallback
	nextRuleId    RuleId

	rulesMutex            sync.Mutex
	ruleMap               map[RuleId]*Rule
	ruleList              []RuleId
	controlToRulesListMap map[ControlSpec][]*Rule
	rulesWithoutControls  map[*Rule]bool
	timerRules            map[string][]*Rule
	uninitializedRules    []*Rule

	notedControls   []ControlSpec
	notedTimers     map[string]bool
	currentTimer    string
	cronMaker       func() Cron
	cron            Cron
	statusMtx       sync.Mutex
	getTimerMtx     sync.Mutex
	debugEnabled    uint32 // atomic
	readyCh         chan struct{}
	readyQueue      *wbgong.DeferredList
	timerDeferQueue *wbgong.DeferredList

	tracks           map[string]map[uint32]MqttTracker
	nextTrackID      uint32 // TrackID is used to watch a track in cleanups
	mqttTrackerMutex sync.Mutex

	cleanupOnStop bool

	statsdClient wbgong.StatsdClientWrapper

	// subscriptions to control change events
	// suitable for testing
	controlChangeSubsMutex sync.Mutex
	controlChangeSubs      []chan *ControlChangeEvent
}

func NewRuleEngine(driver wbgong.Driver, mqtt wbgong.MQTTClient, options *RuleEngineOptions) (engine *RuleEngine) {
	if options == nil {
		panic("no options given to NewRuleEngine")
	}

	engine = &RuleEngine{
		active:                ENGINE_STOP,
		cleanup:               MakeScopedCleanup(),
		rev:                   0,
		syncQueue:             make(chan func(), SYNC_QUEUE_LEN),
		syncQueueActive:       true,
		syncQuitCh:            make(chan chan struct{}, 1),
		mqttClient:            mqtt,
		driver:                driver,
		driverReadyCh:         nil,
		timerFunc:             newTimer,
		nextTimerId:           1,
		timers:                make(map[TimerId]*TimerEntry),
		callbackIndex:         1,
		nextRuleId:            1,
		ruleMap:               make(map[RuleId]*Rule),
		ruleList:              make([]RuleId, 0, RULES_CAPACITY),
		notedControls:         nil,
		notedTimers:           nil,
		controlToRulesListMap: make(map[ControlSpec][]*Rule),
		rulesWithoutControls:  make(map[*Rule]bool),
		timerRules:            make(map[string][]*Rule),
		currentTimer:          NO_TIMER_NAME,
		cronMaker:             func() Cron { return cron.New() },
		cron:                  nil,
		debugEnabled:          ATOMIC_FALSE,
		readyCh:               nil,
		uninitializedRules:    make([]*Rule, 0, ENGINE_UNINITIALIZED_RULES_CAPACITY),
		cleanupOnStop:         options.cleanupOnStop,
		tracks:                make(map[string]map[uint32]MqttTracker),

		controlChangeSubs: make([]chan *ControlChangeEvent, 0, ENGINE_CONTROL_CHANGE_SUBS_CAPACITY),
	}

	// if options.debugQueues {
	// engine.controlChangeChLen = 0
	// } else {
	// engine.controlChangeChLen = ENGINE_CONTROL_CHANGE_QUEUE_LEN
	// }

	engine.readyQueue = wbgong.NewDeferredList(engine.CallSync)
	engine.timerDeferQueue = wbgong.NewDeferredList(engine.CallHere)

	engine.setupRuleEngineSettingsDevice()

	if options.Statsd != nil {
		engine.statsdClient = options.Statsd.Clone(ENGINE_STATSD_PREFIX)
		engine.statsdClient.SetCallback(engine.collectStats)
	}

	return
}

func (engine *RuleEngine) collectStats(s *statsd.Client) {
	// callSync queue
	s.Gauge("sync_queue.len", len(engine.syncQueue))
	s.Gauge("sync_queue.cap", cap(engine.syncQueue))

	// number of timers
	s.Gauge("timers", len(engine.timers))

	// length of event buffer
	s.Gauge("events", engine.eventBuffer.length())
}

func (engine *RuleEngine) ReadyCh() <-chan struct{} {
	if engine.readyCh == nil {
		panic("cannot engine's readyCh before the engine is started")
	}
	return engine.readyCh
}

func (engine *RuleEngine) SubscribeControlChange() <-chan *ControlChangeEvent {
	engine.controlChangeSubsMutex.Lock()
	defer engine.controlChangeSubsMutex.Unlock()

	ret := make(chan *ControlChangeEvent, 0) // ENGINE_CONTROL_CHANGE_QUEUE_LEN)
	engine.controlChangeSubs = append(engine.controlChangeSubs, ret)
	wbgong.Debug.Printf("[ruleengine] Add subscriber for ControlChangeEvent (channel %v)", ret)
	return ret
}

func (engine *RuleEngine) UnsubscribeControlChange(sub <-chan *ControlChangeEvent) {
	i := 0
	found := false
	for i = range engine.controlChangeSubs {
		if engine.controlChangeSubs[i] == sub {
			found = true
			break
		}
	}

	engine.controlChangeSubsMutex.Lock()
	defer engine.controlChangeSubsMutex.Unlock()

	if found {
		engine.controlChangeSubs = append(engine.controlChangeSubs[:i], engine.controlChangeSubs[i+1:]...)
	}
}

func (engine *RuleEngine) notifyControlChangeSubs(e *ControlChangeEvent) {
	engine.controlChangeSubsMutex.Lock()
	defer engine.controlChangeSubsMutex.Unlock()

	for i := range engine.controlChangeSubs {
		engine.controlChangeSubs[i] <- e
	}
}

func (engine *RuleEngine) syncLoop() {
	wbgong.Info.Println("[engine] Starting sync loop")
	for {
		select {
		case f, ok := <-engine.syncQueue:
			if ok {
				f()
			}
		case q := <-engine.syncQuitCh:
			wbgong.Info.Println("[engine] Stopping sync loop")
			close(q)
			return
		}
	}
}

func (engine *RuleEngine) processEvent(event *ControlChangeEvent) {
	if wbgong.DebuggingEnabled() {
		wbgong.Debug.Printf("control change: %s", event.Spec)
		wbgong.Debug.Printf("rule engine: running rules after control change: %s", event.Spec)
	}
	if engine.isDebugControl(event.Spec) {
		engine.updateDebugEnabled()
	}

	engine.CallSync(func() {
		engine.RunRules(event, NO_TIMER_NAME)
	})

	engine.notifyControlChangeSubs(event)
}

func (engine *RuleEngine) mainLoop() {
	// control changes are ignored until the engine is ready
	// FIXME: some very small probability of race condition is
	// present here
	wbgong.Info.Println("[engine] Starting main loop")
ReadyWaitLoop:
	for {
		select {
		case <-engine.driverReadyCh:
			break ReadyWaitLoop
		case _, ok := <-engine.eventBuffer.Observe():
			if ok {
				events := engine.eventBuffer.Retrieve()

				for _, event := range events {
					wbgong.Debug.Printf("control change (not ready yet): %s", event.Spec)
					engine.notifyControlChangeSubs(event)
					if engine.isDebugControl(event.Spec) {
						engine.updateDebugEnabled()
					}
				}
			} else {
				wbgong.Debug.Printf("stoping the engine (not ready yet)")
				engine.handleStop()
				return
			}
		}
	}
	wbgong.Debug.Printf("setting up cron")
	engine.CallSync(engine.setupCron)

	// the first rule run is removed, now it's all done with the first real event

	engine.CallSync(engine.readyQueue.Ready)
	engine.CallSync(engine.timerDeferQueue.Ready)
	close(engine.readyCh)

	wbgong.Info.Printf("the engine is ready")
	// wbgong.Info.Printf("******** READY ********")
	for {
		select {
		case _, ok := <-engine.eventBuffer.Observe():
			if ok {
				events := engine.eventBuffer.Retrieve()
				for _, event := range events {
					engine.processEvent(event)
				}
			} else {
				engine.handleStop()
				wbgong.Info.Println("[engine] Stop main loop")
				return
			}
		}
	}
}

// PushToEventBuffer sends prepared ControlChangeEvent to engines event buffer
func (engine *RuleEngine) PushToEventBuffer(cce *ControlChangeEvent) {
	engine.eventBuffer.PushEvent(cce)
}

// this method runs safely in driver loop
func (engine *RuleEngine) driverEventHandler(event wbgong.DriverEvent) {
	if atomic.LoadUint32(&engine.active) == ENGINE_STOP {
		return
	}

	if wbgong.DebuggingEnabled() {
		wbgong.Debug.Printf("[engine] driverEventHandler(event %T(%v))", event, event)
	}

	var value, prevValue interface{}

	var spec ControlSpec
	isComplete := false
	isRetained := false

	switch e := event.(type) {
	case wbgong.ControlValueEvent:
		ctrl := e.Control
		spec = ControlSpec{ctrl.GetDevice().GetId(), ctrl.GetId()}

		var err error

		value, err = ctrl.GetValue()
		if err != nil {
			wbgong.Info.Printf("%s: failed to convert value '%s', passing raw",
				spec.String(), ctrl.GetRawValue())
			value = ctrl.GetRawValue()
		}

		prevValue, err = wbgong.ToTypedValue(e.PrevRawValue, ctrl.GetType())
		if err != nil {
			wbgong.Info.Printf("%s: failed to convert previous value '%s', passing raw",
				spec.String(), e.PrevRawValue)
			prevValue = e.PrevRawValue
		}

		isComplete = ctrl.IsComplete()
		isRetained = ctrl.IsRetained()
	case wbgong.NewExternalDeviceControlMetaEvent:
		ctrl := e.Control
		spec = ControlSpec{ctrl.GetDevice().GetId(), ctrl.GetId()}

		isComplete = ctrl.IsComplete()
		isRetained = ctrl.IsRetained()

		var err error

		value, err = ctrl.GetValue()
		if err != nil {
			wbgong.Info.Printf("%s: failed to convert value '%s', passing raw",
				spec.String(), ctrl.GetRawValue())
			value = ctrl.GetRawValue()
		}
		prevValue = value

		// here we need to invalidate controls/devices proxy
		atomic.AddUint32(&engine.rev, 1)

		// pushing event about new external meta received
		metaCtrl := fmt.Sprintf("%s#%s", e.Control.GetId(), e.Type)
		metaSpec := ControlSpec{e.Control.GetDevice().GetId(), metaCtrl}

		metaCCE := &ControlChangeEvent{
			Spec:       metaSpec,
			IsComplete: isComplete,
			IsRetained: isRetained,
			Value:      e.Value,
			PrevValue:  e.PrevValue,
		}
		engine.eventBuffer.PushEvent(metaCCE)
	default:
		return
	}

	cce := &ControlChangeEvent{
		Spec:       spec,
		IsComplete: isComplete,
		IsRetained: isRetained,
		Value:      value,
		PrevValue:  prevValue,
	}

	engine.eventBuffer.PushEvent(cce)
}

func (engine *RuleEngine) CallSync(thunk func()) {
	if atomic.LoadUint32(&engine.debugEnabled) == ATOMIC_TRUE {
		select {
		case engine.syncQueue <- thunk:
		case <-time.After(ENGINE_CALLSYNC_TIMEOUT):
			panic("[engine] CallSync stuck!")
		}
	} else {
		engine.syncQueue <- thunk
	}
}

func (engine *RuleEngine) MaybeCallSync(thunk func()) {
	if engine.syncQueueActive {
		engine.CallSync(thunk)
	} else {
		thunk()
	}
}

func (engine *RuleEngine) CallHere(thunk func()) {
	thunk()
}

func (engine *RuleEngine) WhenEngineReady(thunk func()) {
	engine.readyQueue.MaybeDefer(thunk)
}

func (engine *RuleEngine) setupRuleEngineSettingsDevice() {
	err := engine.DefineVirtualDevice(RULE_ENGINE_SETTINGS_DEV_NAME, objx.Map{
		"title": "Rule Engine Settings",
		"cells": objx.Map{
			RULE_DEBUG_CELL_NAME: objx.Map{
				"type":  "switch",
				"value": atomic.LoadUint32(&engine.debugEnabled),
			},
		},
	})
	if err != nil {
		log.Panicf("cannot define wbrules device: %s", err)
	}
}

func (engine *RuleEngine) SetTimerFunc(timerFunc TimerFunc) {
	engine.timerFunc = timerFunc
}

func (engine *RuleEngine) SetCronMaker(cronMaker func() Cron) {
	engine.cronMaker = cronMaker
}

func (engine *RuleEngine) SetUninitializedRule(rule *Rule) {
	engine.uninitializedRules = append(engine.uninitializedRules, rule)
}

func (engine *RuleEngine) StartTrackingDeps() {
	engine.notedControls = make([]ControlSpec, 0, ENGINE_NOTED_CONTROLS_CAPACITY)
	engine.notedTimers = make(map[string]bool)
}

func (engine *RuleEngine) StoreRuleControlSpec(rule *Rule, spec ControlSpec) {
	list, found := engine.controlToRulesListMap[spec]
	if !found {
		list = make([]*Rule, 0, ENGINE_CONTROL_RULES_CAPACITY)
	} else {
		for _, item := range list {
			if item == rule {
				return
			}
		}
	}
	wbgong.Debug.Printf("adding control spec %s for rule %d", spec.String(), rule.id)
	engine.controlToRulesListMap[spec] = append(list, rule)
	engine.rulesWithoutControls[rule] = false
}

func (engine *RuleEngine) storeRuleTimer(rule *Rule, timerName string) {
	list, found := engine.timerRules[timerName]
	if !found {
		list = make([]*Rule, 0, ENGINE_CONTROL_RULES_CAPACITY)
	}
	engine.timerRules[timerName] = append(list, rule)
}

func (engine *RuleEngine) StoreRuleDeps(rule *Rule) {
	if len(engine.notedControls) > 0 {
		for _, spec := range engine.notedControls {
			engine.StoreRuleControlSpec(rule, spec)
		}
	} else if len(engine.notedTimers) > 0 {
		for timerName, _ := range engine.notedTimers {
			engine.storeRuleTimer(rule, timerName)
		}
	} else if !rule.HasDeps() {
		if wo, found := engine.rulesWithoutControls[rule]; !found || wo {
			// Rules without controls in their conditions negatively affect
			// the engine performance because they must be checked
			// too often. Only mark a rule as such if it doesn't have
			// any controls associated with it and it isn't an control-independent rule
			// (such as a cron rule)
			if wbgong.DebuggingEnabled() {
				// Here we use Warn output but only in case if debugging is enabled.
				// This improves testability (due to EnsureNoErrorsOrWarnings()) but
				// avoids polluting logs with endless warnings when debugging is off.
				wbgong.Warn.Printf("rule %s doesn't use any controls inside condition functions", rule.name)
			}
			if !found {
				engine.rulesWithoutControls[rule] = true
			}
		}
	}
	engine.notedControls = nil
	engine.notedTimers = nil
}

func (engine *RuleEngine) trackControlSpec(s ControlSpec) {
	if engine.notedControls != nil {
		engine.notedControls = append(engine.notedControls, s)
	}
}

func (engine *RuleEngine) trackTimer(timerName string) {
	if engine.notedTimers != nil {
		engine.notedTimers[timerName] = true
	}
}

func (engine *RuleEngine) CheckTimer(timerName string) bool {
	engine.trackTimer(timerName)
	return engine.currentTimer != NO_TIMER_NAME && engine.currentTimer == timerName
}

func (engine *RuleEngine) fireTimer(n TimerId) {
	engine.timersMutex.Lock()
	entry, found := engine.timers[n]
	engine.timersMutex.Unlock()

	if !found {
		wbgong.Error.Printf("firing unknown timer %d", n)
		return
	}
	if entry.name == NO_TIMER_NAME {
		entry.thunk()
	} else {
		engine.RunRules(nil, entry.name)
	}

	if !entry.periodic {
		engine.timersMutex.Lock()
		engine.removeTimer(n)
		engine.timersMutex.Unlock()
	}
}

func (engine *RuleEngine) removeTimer(n TimerId) {
	if entry, found := engine.timers[n]; found {
		entry.handleRemove()
		delete(engine.timers, n)
	}
}

func (engine *RuleEngine) StopTimerByName(name string) {
	engine.timersMutex.Lock()

	for n, entry := range engine.timers {
		if entry != nil && name == entry.name {
			engine.removeTimer(n)
			engine.timersMutex.Unlock()
			entry.stop()
			return
		}
	}

	engine.timersMutex.Unlock()
}

func (engine *RuleEngine) StopTimerByIndex(n TimerId) {
	if entry, found := engine.FindTimerByIndex(n); found {
		engine.timersMutex.Lock()
		engine.removeTimer(n)
		engine.timersMutex.Unlock()

		entry.stop()
	} else {
		wbgong.Error.Printf("trying to stop unknown timer: %d", n)
	}
}

func (engine *RuleEngine) FindTimerByIndex(n TimerId) (entry *TimerEntry, found bool) {
	if n == 0 {
		return
	}

	engine.timersMutex.Lock()
	defer engine.timersMutex.Unlock()

	entry, found = engine.timers[n]
	return
}

func (engine *RuleEngine) OnTimerRemoveByIndex(n TimerId, thunk func()) {
	if entry, found := engine.FindTimerByIndex(n); found {
		entry.onRemove(thunk)
	} else {
		wbgong.Error.Printf("trying to handle remove of unknown timer: %d", n)
	}
}

func (engine *RuleEngine) RunRules(ctrlEvent *ControlChangeEvent, timerName string) {
	if wbgong.DebuggingEnabled() {
		wbgong.Debug.Println("[ruleengine] RunRules, event ", ctrlEvent, ", timer ", timerName)
		wbgong.Debug.Printf("[ruleengine] RulesLists for all: %v", engine.controlToRulesListMap)
	}
	engine.rulesMutex.Lock()
	defer engine.rulesMutex.Unlock()

	// select all uninitialized rules to run and clean list
	for _, rule := range engine.uninitializedRules {
		rule.ShouldCheck()
	}
	// clear uninitialized rules list
	engine.uninitializedRules = make([]*Rule, 0, ENGINE_UNINITIALIZED_RULES_CAPACITY)

	if ctrlEvent != nil {
		/*if cell.IsFreshButton() {
			// special case - a button that wasn't pressed yet
			return
		}*/
		if ctrlEvent.IsComplete {
			// control-dependent rules aren't run when any of their
			// condition controls are incomplete
			if list, found := engine.controlToRulesListMap[ctrlEvent.Spec]; found {
				for _, rule := range list {
					rule.ShouldCheck()
				}
			}
		}
		for rule, isWithoutControls := range engine.rulesWithoutControls {
			if isWithoutControls {
				rule.ShouldCheck()
			}
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

	for _, ruleId := range engine.ruleList {
		engine.ruleMap[ruleId].Check(ctrlEvent)
	}
	engine.currentTimer = NO_TIMER_NAME
}

func (engine *RuleEngine) setupCron() {
	if engine.cron != nil {
		engine.cron.Stop()
	}

	engine.cron = newCronProxy(engine.cronMaker(), engine.CallSync)
	// note for rule reloading: will need to restart cron
	// to reload rules properly
	func() {
		engine.rulesMutex.Lock()
		defer engine.rulesMutex.Unlock()

		for _, ruleId := range engine.ruleList {
			rule := engine.ruleMap[ruleId]
			rule.MaybeAddToCron(engine.cron)
		}
	}()

	engine.cron.Start()
}

func (engine *RuleEngine) handleStop() {
	wbgong.Debug.Printf("engine stopped")

	engine.timersMutex.Lock()
	timerEntries := make([]*TimerEntry, 0, len(engine.timers))
	for _, entry := range engine.timers {
		timerEntries = append(timerEntries, entry)
	}
	engine.timers = make(map[TimerId]*TimerEntry)
	engine.timersMutex.Unlock()

	for _, entry := range timerEntries {
		entry.stop()
	}

	engine.statusMtx.Lock()
	engine.readyCh = nil
	engine.driverReadyCh = nil
	engine.syncQueueActive = false
	close(engine.syncQueue)
	engine.statusMtx.Unlock()
}

func (engine *RuleEngine) isDebugControl(ctrlSpec ControlSpec) bool {
	return ctrlSpec.DeviceId == RULE_ENGINE_SETTINGS_DEV_NAME &&
		ctrlSpec.ControlId == RULE_DEBUG_CELL_NAME
}

func (engine *RuleEngine) updateDebugEnabled() {
	engine.CallSync(func() {
		var val bool
		err := engine.driver.Access(func(tx wbgong.DriverTx) error {
			dev := tx.GetDevice(RULE_ENGINE_SETTINGS_DEV_NAME)
			if dev == nil {
				return ControlNotFoundError
			}
			ctrl := dev.GetControl(RULE_DEBUG_CELL_NAME)
			if ctrl == nil {
				return ControlNotFoundError
			}

			i, err := ctrl.GetValue()
			val = i.(bool)
			return err
		})

		if err != nil {
			panic("No debug control in rule engine service device")
		}

		var set uint32 = ATOMIC_FALSE
		if val {
			set = ATOMIC_TRUE
		}
		atomic.StoreUint32(&engine.debugEnabled, set)
	})
}

func (engine *RuleEngine) Start() {
	// start statsd client
	if engine.statsdClient != nil {
		engine.statsdClient.Start(ENGINE_STATSD_POLL_INTERVAL)
	}

	engine.readyCh = make(chan struct{})
	engine.driverReadyCh = make(chan struct{}, 1)
	engine.eventBuffer = NewEventBuffer()

	engine.driver.OnDriverEvent(engine.driverEventHandler)
	engine.driver.OnRetainReady(func(tx wbgong.DriverTx) {
		engine.driverReadyCh <- struct{}{}
	})
	engine.syncQueueActive = true
	atomic.StoreUint32(&engine.active, ENGINE_ACTIVE)

	go engine.mainLoop()
	go engine.syncLoop()
}

func (engine *RuleEngine) Stop() {
	atomic.StoreUint32(&engine.active, ENGINE_STOP)

	// run all necessary cleanups
	if engine.cleanupOnStop {
		wbgong.Info.Println("[engine] Performing MQTT cleanup on stop")
		engine.cleanup.RunAllCleanups()
	}

	engine.eventBuffer.Close()

	// stop sync loop
	q := make(chan struct{})
	engine.syncQuitCh <- q
	<-q

	// wait for main loop to release sync queue
	<-engine.syncQueue

	// stop statsd
	if engine.statsdClient != nil {
		engine.statsdClient.Stop()
	}
}

func (engine *RuleEngine) IsActive() bool {
	return atomic.LoadUint32(&engine.active) == ENGINE_ACTIVE
}

func (engine *RuleEngine) StartTimer(name string, callback func(), interval time.Duration, periodic bool) TimerId {
	entry := &TimerEntry{
		periodic: periodic,
		quit:     nil,
		quitted:  nil,
		name:     name,
		active:   true,
	}

	engine.timersMutex.Lock()
	n := engine.nextTimerId
	engine.nextTimerId += 1
	engine.timers[n] = entry
	engine.timersMutex.Unlock()

	if name == NO_TIMER_NAME {
		entry.thunk = callback
	} else if callback != nil {
		wbgong.Warn.Printf("warning: ignoring callback func for a named timer")
	}

	wbgong.Debug.Printf("[engine] Starting timer '%s' (id %d)", name, n)

	engine.timerDeferQueue.MaybeDefer(func() {
		entry.Lock()
		defer entry.Unlock()
		if !entry.active {
			// stopped before the engine is ready
			return
		}
		entry.quit = make(chan struct{}, 2) // FIXME: is 2 necessary here?
		entry.quitted = make(chan struct{})

		engine.getTimerMtx.Lock()
		entry.timer = engine.timerFunc(n, interval, periodic)
		engine.getTimerMtx.Unlock()

		tickCh := entry.timer.GetChannel()
		go func() {
			for {
				select {
				case <-tickCh:
					entryFunc := func() {
						entry.Lock()
						wasActive := entry.active
						entry.Unlock()
						if wasActive {
							engine.fireTimer(n)
						}
					}

					// try to push entry processing function into sync queue or
					// exit immediately on quit signal
					// timer may block here if you try to use classic CallSync
					select {
					case engine.syncQueue <- entryFunc:
					case <-entry.quit:
						entry.timer.Stop()
						close(entry.quitted)
						return
					}

					// stop timer loop if it is not periodical
					if !periodic {
						close(entry.quitted)
						return
					}

				case <-entry.quit:
					entry.timer.Stop()
					close(entry.quitted)
					return
				}
			}
		}()
	})

	return n
}

func (engine *RuleEngine) Publish(topic, payload string, qos byte, retain bool) {
	engine.mqttClient.Start()
	engine.mqttClient.Publish(wbgong.MQTTMessage{
		Topic:    topic,
		Payload:  payload,
		QoS:      byte(qos),
		Retained: retain,
	})
}

func fillControlArgs(devId, ctrlId string, ctrlDef objx.Map, args wbgong.ControlArgs) error {
	// fill in control args
	//
	// try to get type
	ctrlType, ok := ctrlDef[VDEV_CONTROL_DESCR_PROP_TYPE]
	if !ok {
		return fmt.Errorf("%s/%s: no control type", devId, ctrlId)
	}
	args.SetType(ctrlType.(string))

	// get 'forceDefault' metaproperty
	forceDefault := false
	forceDefaultRaw, hasForceDefault := ctrlDef[VDEV_CONTROL_DESCR_PROP_FORCEDEFAULT]
	if hasForceDefault {
		ok := false
		forceDefault, ok = forceDefaultRaw.(bool)
		if !ok {
			return fmt.Errorf("%s/%s: non-boolean value of forceDefault propery",
				devId, ctrlId)
		}
	}
	args.SetDoLoadPrevious(!forceDefault)

	// get 'lazyInit' metaproperty
	lazyInit := false
	lazyInitRaw, hasLazyInit := ctrlDef[VDEV_CONTROL_DESCR_PROP_LAZYINIT]
	if hasLazyInit {
		ok := false
		lazyInit, ok = lazyInitRaw.(bool)
		if !ok {
			return fmt.Errorf("%s/%s: non-boolean value of lazyInit propery",
				devId, ctrlId)
		}
	}
	args.SetLazyInit(lazyInit)

	ctrlValue, ok := ctrlDef[VDEV_CONTROL_DESCR_PROP_VALUE]
	if !ok && ctrlType != "pushbutton" { // FIXME: awful, need some special checkers
		return fmt.Errorf("%s/%s: control value required for control type %s",
			devId, ctrlId, ctrlType)
	}

	// get 'order' property
	orderValue, hasOrder := ctrlDef[VDEV_CONTROL_DESCR_PROP_ORDER]
	if hasOrder {
		order := 0.0
		ok := false
		order, ok = orderValue.(float64)
		if !ok {
			return fmt.Errorf("%s/%s: non-number value of order property, has %T",
				devId, ctrlId, orderValue)
		}
		if order < 0 {
			return fmt.Errorf("%s/%s: invalid order value, must be int >= 0",
				devId, ctrlId)
		}
		args.SetOrder(int(order))
	}

	// set control value itself
	args.SetValue(ctrlValue)

	_, hasWritable := ctrlDef[VDEV_CONTROL_DESCR_PROP_WRITEABLE]
	if hasWritable {
		return fmt.Errorf("writeable flag is deprecated, use readonly instead: https://github.com/wirenboard/wb-rules/blob/master/README-readonly.md")
	}

	// get readonly/writeable flag
	ctrlReadonly := VDEV_CONTROL_READONLY_DEFAULT

	ctrlReadonlyRaw, hasReadonly := ctrlDef[VDEV_CONTROL_DESCR_PROP_READONLY]
	if hasReadonly {
		ctrlReadonly, ok = ctrlReadonlyRaw.(bool)
		if !ok {
			return fmt.Errorf("%s/%s: non-boolean value of 'readonly' property",
				devId, ctrlId)
		}
	}

	// set readonly flag
	if hasReadonly {
		args.SetReadonly(ctrlReadonly)
		// switch, pushbutton,range, rgb are writable by default
	} else if ctrlType == wbgong.CONV_TYPE_SWITCH {
		args.SetReadonly(false)
	} else if ctrlType == wbgong.CONV_TYPE_PUSHBUTTON {
		args.SetReadonly(false)
	} else if ctrlType == wbgong.CONV_TYPE_RANGE {
		args.SetReadonly(false)
	} else if ctrlType == wbgong.CONV_TYPE_RGB {
		args.SetReadonly(false)
	} else { // all other types is readonly by default
		args.SetReadonly(ctrlReadonly)
	}

	// get properties for 'range' type
	// FIXME: deprecated
	if ctrlType == wbgong.CONV_TYPE_RANGE {
		fmax := VDEV_CONTROL_RANGE_MAX_DEFAULT
		max, ok := ctrlDef[VDEV_CONTROL_DESCR_PROP_MAX]
		if ok {
			fmax, ok = max.(float64)
			if !ok {
				return fmt.Errorf("%s/%s: non-numeric value of max property",
					devId, ctrlId)
			}
		}

		// set argument
		args.SetMax(int(fmax))
	}
	if descr, ok := ctrlDef[VDEV_CONTROL_DESCR_PROP_DESCRIPTION]; ok {
		if fdescr, okString := descr.(string); okString {
			args.SetDescription(fdescr)
		} else {
			return fmt.Errorf("%s/%s: non-string value of description property",
				devId, ctrlId)
		}
	}
	return nil
}

func (engine *RuleEngine) RemoveControl(devID, ctrlID string) error {
	errAccess := engine.driver.Access(func(tx wbgong.DriverTx) (err error) {
		dev := tx.GetDevice(devID)
		if dev == nil {
			return wbgong.DeviceNotExistError
		}
		localDevice, isLocal := dev.(wbgong.LocalDevice)
		if !isLocal {
			return wbgong.ExternalDeviceError
		}

		err = localDevice.RemoveControl(ctrlID)()

		return
	})

	if errAccess != nil {
		return errAccess
	}
	return nil
}

func (engine *RuleEngine) AddControl(devID, ctrlID string, ctrlDef objx.Map) error {
	args := wbgong.NewControlArgs().SetId(ctrlID)

	// fill in control args
	errFill := fillControlArgs(devID, ctrlID, ctrlDef, args)
	if errFill != nil {
		return errFill
	}
	// create virtual device using collected descriptions

	errAccess := engine.driver.Access(func(tx wbgong.DriverTx) (err error) {
		dev := tx.GetDevice(devID)
		if dev == nil {
			return wbgong.DeviceNotExistError
		}
		localDevice, isLocal := dev.(wbgong.LocalDevice)
		if !isLocal {
			return wbgong.ExternalDeviceError
		}

		_, err = localDevice.CreateControl(args)()

		return
	})

	if errAccess != nil {
		return errAccess
	}
	return nil
}

func (engine *RuleEngine) GetDevice(devId string) error {
	// create virtual device using collected descriptions
	errAccess := engine.driver.Access(func(tx wbgong.DriverTx) (err error) {
		// create device by device description
		dev := tx.GetDevice(devId)
		if dev == nil {
			return wbgong.DeviceNotExistError
		}
		return
	})

	if errAccess != nil {
		return errAccess
	}
	return nil
}

func (engine *RuleEngine) DefineVirtualDevice(devId string, obj objx.Map) error {
	// if device description has no controls (cells), skip this
	if !obj.Has(VDEV_DESCR_PROP_CELLS) && !obj.Has(VDEV_DESCR_PROP_CONTROLS) {
		return nil
	}

	// determine cells/control property name
	controlsProp := VDEV_DESCR_PROP_CONTROLS
	if obj.Has(VDEV_DESCR_PROP_CELLS) {
		controlsProp = VDEV_DESCR_PROP_CELLS
	}

	// prepare whole description for this device
	devArgs := wbgong.NewLocalDeviceArgs().SetId(devId).SetVirtual(true)

	// try to get title
	if obj.Has(VDEV_DESCR_PROP_TITLE) {
		devArgs.SetTitle(obj.Get(VDEV_DESCR_PROP_TITLE).Str(devId))
	}

	// get controls list
	v := obj.Get(controlsProp)
	var m objx.Map
	switch {
	case v.IsObjxMap():
		m = v.ObjxMap()
	case v.IsMSI():
		m = objx.Map(v.MSI())
	default:
		return fmt.Errorf("device %s doesn't have proper 'controls' or 'cells' property", devId)
	}

	// Sorting controls by their names is not important when defining device
	// while the engine is not active because all the cells will be published
	// all at once when the engine starts.
	// On the other hand, when defining the device for the active engine
	// the newly added cells are published immediately and if their order
	// changes (map key order is random) the tests may break.
	controlIds := make([]string, 0, len(m))
	for ctrlId, _ := range m {
		controlIds = append(controlIds, ctrlId)
	}
	sort.Strings(controlIds)

	controlsArgs := make([]wbgong.ControlArgs, 0, len(m))

	for _, ctrlId := range controlIds {
		// check if this object is a correct control definition (is an object, at least)
		maybeCtrlDef := m[ctrlId]
		ctrlDef, ok := maybeCtrlDef.(objx.Map)
		if !ok {
			cd, ok := maybeCtrlDef.(map[string]interface{})
			if !ok {
				return fmt.Errorf("%s/%s: bad control definition", devId, ctrlId)
			}
			ctrlDef = objx.Map(cd)
		}

		// create control args
		args := wbgong.NewControlArgs().SetId(ctrlId)

		// append args to controls args list
		controlsArgs = append(controlsArgs, args)

		// fill in control args
		errFill := fillControlArgs(devId, ctrlId, ctrlDef, args)
		if errFill != nil {
			return errFill
		}
	}

	// create virtual device using collected descriptions
	var dev wbgong.LocalDevice
	err := engine.driver.Access(func(tx wbgong.DriverTx) (err error) {
		// create device by device description
		dev, err = tx.CreateDevice(devArgs)()
		if err != nil {
			return
		}

		// create controls
		for _, ctrlArgs := range controlsArgs {
			_, err = dev.CreateControl(ctrlArgs)()
			if err != nil {
				// cleanup
				tx.RemoveDevice(dev)()
				return
			}
		}

		return
	})

	if err != nil {
		return err
	}

	// defer cleanup
	engine.cleanup.AddCleanup(func() {
		err := engine.driver.Access(func(tx wbgong.DriverTx) error {
			return tx.RemoveDevice(dev)()
		})
		if err != nil {
			wbgong.Warn.Printf("failed to remove device %s in cleanup: %s", devId, err)
		}
	})

	return err
}

func (engine *RuleEngine) DefineRule(rule *Rule, ctx *ESContext) (id RuleId, err error) {
	engine.rulesMutex.Lock()
	defer engine.rulesMutex.Unlock()

	// for named rule - check for redefinition
	if err = ctx.AddRule(rule.name, rule); err != nil {
		return
	}

	// needed for rules defined after initial file load, for instance in timers or other rules
	rule.MaybeAddToCron(engine.cron)

	engine.ruleList = append(engine.ruleList, rule.id)

	engine.ruleMap[rule.id] = rule

	engine.cleanup.AddCleanup(func() {
		engine.rulesMutex.Lock()
		defer engine.rulesMutex.Unlock()

		delete(engine.ruleMap, rule.id)
		for i, id := range engine.ruleList {
			if id == rule.id {
				engine.ruleList = append(
					engine.ruleList[0:i],
					engine.ruleList[i+1:]...)
				break
			}
		}

		rule.Destroy()
	})

	id = rule.id

	wbgong.Debug.Printf("[ruleengine] defineRule(name='%s') ruleId=%d, cond %T(%v)", rule.name, id, rule.cond, rule.cond)

	return
}

// DefineMqttTracker creates new mqtt tracker and subscribe to specified topic if needed
func (engine *RuleEngine) DefineMqttTracker(topic string, ctx *ESContext) (err error) {
	engine.mqttTrackerMutex.Lock()
	defer engine.mqttTrackerMutex.Unlock()

	trackerID := atomic.AddUint32(&engine.nextTrackID, 1)

	tracker := NewMqttTracker(topic, trackerID)
	tracker.Callback = ctx.WrapCallback(-1)
	if _, ok := engine.tracks[topic]; !ok {
		engine.tracks[topic] = make(MqttTrackerMap)
	}
	engine.mqttClient.Subscribe(engine.newTrackHandler(topic), topic)
	engine.tracks[topic][trackerID] = tracker

	engine.cleanup.AddCleanup(func() {
		engine.mqttTrackerMutex.Lock()
		defer engine.mqttTrackerMutex.Unlock()
		delete(engine.tracks[topic], trackerID)
		if len(engine.tracks[topic]) < 1 {
			engine.mqttClient.Unsubscribe(topic)
		}
	})

	return nil
}

func (engine *RuleEngine) newTrackHandler(subTopic string) func(wbgong.MQTTMessage) {
	return func(msg wbgong.MQTTMessage) {
		var args objx.Map
		if _, ok := engine.tracks[subTopic]; ok {
			for _, tracker := range engine.tracks[subTopic] {
				tr := tracker
				args = objx.New(map[string]interface{}{
					"topic": msg.Topic,
					"value": msg.Payload,
				})
				engine.CallSync(func() {
					tr.Callback(args)
				})
			}
		}
	}
}

// Refresh() should be called after engine rules are altered
// while the engine is running.
func (engine *RuleEngine) Refresh() {
	if wbgong.DebuggingEnabled() {
		wbgong.Debug.Println("[engine] Refresh()")
	}
	atomic.AddUint32(&engine.rev, 1) // invalidate device/control proxies
	engine.setupCron()

	engine.rulesMutex.Lock()
	defer engine.rulesMutex.Unlock()

	// Some cell pointers are now probably invalid
	// FIXME: maybe this problem is gone now
	engine.controlToRulesListMap = make(map[ControlSpec][]*Rule)
	engine.uninitializedRules = make([]*Rule, 0, ENGINE_UNINITIALIZED_RULES_CAPACITY)
	for _, rule := range engine.ruleMap {
		rule.StoreInitiallyKnownDeps()
	}
	engine.rulesWithoutControls = make(map[*Rule]bool)
	engine.timerRules = make(map[string][]*Rule)
}

func (engine *RuleEngine) Driver() wbgong.Driver {
	return engine.driver
}

func (engine *RuleEngine) getRev() uint32 {
	return atomic.LoadUint32(&engine.rev)
}

func (engine *RuleEngine) GetDeviceProxy(name string) *DeviceProxy {
	return makeDeviceProxy(engine, name)
}

func (engine *RuleEngine) Log(level EngineLogLevel, message string) {
	var topicItem string
	switch level {
	case ENGINE_LOG_DEBUG:
		wbgong.Debug.Printf("[rule debug] %s", message)
		if atomic.LoadUint32(&engine.debugEnabled) != ATOMIC_TRUE {
			return
		}
		topicItem = "debug"
	case ENGINE_LOG_INFO:
		wbgong.Info.Printf("[rule info] %s", message)
		topicItem = "info"
	case ENGINE_LOG_WARNING:
		wbgong.Warn.Printf("[rule warning] %s", message)
		topicItem = "warning"
	case ENGINE_LOG_ERROR:
		wbgong.Error.Printf("[rule error] %s", message)
		topicItem = "error"
	}
	engine.Publish("/wbrules/log/"+topicItem, message, 1, false)
}

func (engine *RuleEngine) Logf(level EngineLogLevel, format string, v ...interface{}) {
	engine.Log(level, fmt.Sprintf(format, v...))
}
