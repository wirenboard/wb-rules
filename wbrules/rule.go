package wbrules

import (
	"github.com/stretchr/objx"
	wbgong "github.com/wirenboard/wbgong"
)

const (
	RULE_OR_COND_CAPACITY = 10
)

type DepTracker interface {
	StartTrackingDeps()
	StoreRuleControlSpec(rule *Rule, ctrlSpec ControlSpec)
	StoreRuleDeps(rule *Rule)
	SetUninitializedRule(rule *Rule)
}

type Cron interface {
	AddFunc(spec string, cmd func()) error
	Start()
	Stop()
}

type RuleCondition interface {
	// Check checks whether the rule should be run
	// and returns a boolean value indicating whether
	// it should be run and an optional value
	// to be passed as newValue to the rule. In
	// case nil is returned as the optional value,
	// the value of cell must be used.
	RequireInitialization() bool
	Check(e *ControlChangeEvent) (bool, interface{})
	GetControlSpecs() []ControlSpec
	MaybeAddToCron(cron Cron, thunk func()) (added bool, err error)
}

type RuleConditionBase struct{}

func (ruleCond *RuleConditionBase) RequireInitialization() bool {
	return true
}

func (ruleCond *RuleConditionBase) Check(e *ControlChangeEvent) (bool, interface{}) {
	return false, nil
}

func (ruleCond *RuleConditionBase) GetControlSpecs() []ControlSpec {
	return []ControlSpec{}
}

func (ruleCond *RuleConditionBase) MaybeAddToCron(cron Cron, thunk func()) (bool, error) {
	return false, nil
}

type SimpleCallbackCondition struct {
	RuleConditionBase
	cond func() bool
}

type LevelTriggeredRuleCondition struct {
	SimpleCallbackCondition
}

func NewLevelTriggeredRuleCondition(cond func() bool) *LevelTriggeredRuleCondition {
	return &LevelTriggeredRuleCondition{
		SimpleCallbackCondition: SimpleCallbackCondition{cond: cond},
	}
}

func (ruleCond *LevelTriggeredRuleCondition) Check(e *ControlChangeEvent) (bool, interface{}) {
	return ruleCond.cond(), nil
}

type DestroyedRuleCondition struct {
	RuleConditionBase
}

func NewDestroyedRuleCondition() *DestroyedRuleCondition {
	return &DestroyedRuleCondition{}
}

func (ruleCond *DestroyedRuleCondition) Check(e *ControlChangeEvent) (bool, interface{}) {
	panic("invoking a destroyed rule")
}

type EdgeTriggeredRuleCondition struct {
	SimpleCallbackCondition
	prevCondValue bool
	firstRun      bool
}

func NewEdgeTriggeredRuleCondition(cond func() bool) *EdgeTriggeredRuleCondition {
	return &EdgeTriggeredRuleCondition{
		SimpleCallbackCondition: SimpleCallbackCondition{cond: cond},
		prevCondValue:           false,
		firstRun:                false,
	}
}

func (ruleCond *EdgeTriggeredRuleCondition) Check(e *ControlChangeEvent) (bool, interface{}) {
	current := ruleCond.cond()
	shouldFire := current && (ruleCond.firstRun || current != ruleCond.prevCondValue)
	ruleCond.prevCondValue = current
	ruleCond.firstRun = false
	return shouldFire, nil
}

type CellChangedRuleCondition struct {
	RuleConditionBase
	ctrlSpec ControlSpec
}

func NewCellChangedRuleCondition(ctrlSpec ControlSpec) (*CellChangedRuleCondition, error) {
	return &CellChangedRuleCondition{
		ctrlSpec: ctrlSpec,
	}, nil
}

func (ruleCond *CellChangedRuleCondition) RequireInitialization() bool {
	return false
}

func (ruleCond *CellChangedRuleCondition) GetControlSpecs() []ControlSpec {
	return []ControlSpec{ruleCond.ctrlSpec}
}

func (ruleCond *CellChangedRuleCondition) Check(e *ControlChangeEvent) (bool, interface{}) {
	if e == nil || e.Spec != ruleCond.ctrlSpec {
		return false, nil
	}

	if !e.IsComplete {
		wbgong.Debug.Printf("skipping rule due to incomplete cell in whenChanged: %s", e.Spec)
		return false, nil
	}

	if e.IsRetained && e.PrevValue == e.Value {
		return false, nil
	}

	return true, nil
}

type FuncValueChangedRuleCondition struct {
	RuleConditionBase
	thunk    func() interface{}
	oldValue interface{}
}

func NewFuncValueChangedRuleCondition(f func() interface{}) *FuncValueChangedRuleCondition {
	return &FuncValueChangedRuleCondition{
		thunk:    f,
		oldValue: nil,
	}
}

func (ruleCond *FuncValueChangedRuleCondition) Check(e *ControlChangeEvent) (bool, interface{}) {
	v := ruleCond.thunk()
	if ruleCond.oldValue == v {
		return false, nil
	}
	ruleCond.oldValue = v
	return true, v
}

type OrRuleCondition struct {
	RuleConditionBase
	initialized bool
	conds       []RuleCondition
}

func NewOrRuleCondition(conds []RuleCondition) *OrRuleCondition {
	ret := &OrRuleCondition{initialized: false, conds: conds}
	if !ret.RequireInitialization() {
		ret.initialized = true
	}
	return ret
}

func (ruleCond *OrRuleCondition) RequireInitialization() bool {
	for i := range ruleCond.conds {
		if ruleCond.conds[i].RequireInitialization() {
			return true
		}
	}
	return false
}

func (ruleCond *OrRuleCondition) GetControlSpecs() []ControlSpec {
	r := make([]ControlSpec, 0, RULE_OR_COND_CAPACITY)
	for _, cond := range ruleCond.conds {
		r = append(r, cond.GetControlSpecs()...)
	}
	return r
}

func (ruleCond *OrRuleCondition) Check(e *ControlChangeEvent) (bool, interface{}) {
	// if condition is not initialized, we need to check all subconditions to collect deps
	// 'Or' condition is initialized by default if no subconditions requires initialization
	var res = false
	var newValue interface{}
	var gotValue = false

	for _, cond := range ruleCond.conds {
		if shouldFire, newVal := cond.Check(e); shouldFire {
			// this condition is to keep
			if !ruleCond.initialized {
				if !gotValue {
					gotValue = true
					newValue = newVal
					res = true
				}
			} else {
				return true, newVal
			}
		}
	}

	ruleCond.initialized = true
	return res, newValue
}

type CronRuleCondition struct {
	RuleConditionBase
	spec string
}

func NewCronRuleCondition(spec string) *CronRuleCondition {
	return &CronRuleCondition{spec: spec}
}

func (ruleCond *CronRuleCondition) MaybeAddToCron(cron Cron, thunk func()) (added bool, err error) {
	err = cron.AddFunc(ruleCond.spec, thunk)
	added = err == nil
	return
}

// RuleId is returned from defineRule to control rule
type RuleId uint32

type Rule struct {
	tracker       DepTracker
	id            RuleId
	context       *ESContext
	name          string // optional, but will be checked for redefinition if set
	cond          RuleCondition
	then          ESCallbackFunc
	shouldCheck   bool
	isIndependent bool
	hasDeps       bool
	enabled       bool
}

func NewRule(tracker DepTracker, id RuleId, name string, cond RuleCondition, then ESCallbackFunc) *Rule {
	rule := &Rule{
		tracker:       tracker,
		id:            id,
		name:          name,
		cond:          cond,
		then:          then,
		shouldCheck:   false,
		isIndependent: false,
		hasDeps:       false,
		enabled:       true,
	}
	rule.StoreInitiallyKnownDeps()
	return rule
}

func (rule *Rule) StoreInitiallyKnownDeps() {
	for _, ctrlSpec := range rule.cond.GetControlSpecs() {
		rule.tracker.StoreRuleControlSpec(rule, ctrlSpec)
		rule.hasDeps = true
	}
	if rule.cond.RequireInitialization() {
		rule.tracker.SetUninitializedRule(rule)
	}
}

func (rule *Rule) ShouldCheck() {
	rule.shouldCheck = true
}

func (rule *Rule) Check(e *ControlChangeEvent) {
	if e != nil && !rule.shouldCheck {
		// Don't invoke js if no cells mentioned in the
		// condition callback changed. If rules are run
		// not due to a cell being changed, still need
		// to call JS though.
		return
	}
	rule.tracker.StartTrackingDeps()
	shouldFire, newValue := rule.cond.Check(e)
	var args objx.Map
	rule.tracker.StoreRuleDeps(rule)
	rule.shouldCheck = false

	if rule.enabled {
		switch {
		case !shouldFire:
			return
		case newValue != nil:
			args = objx.New(map[string]interface{}{
				"newValue": newValue,
			})
		case e != nil: // newValue == nil
			args = objx.New(map[string]interface{}{
				"device":   e.Spec.DeviceId,
				"cell":     e.Spec.ControlId,
				"newValue": e.Value,
			})
		}
		if wbgong.DebuggingEnabled() {
			wbgong.Debug.Printf("[rule] firing Rule ruleId=%d", rule.id)
		}
		rule.then(args)
	}
}

func (rule *Rule) MaybeAddToCron(cron Cron) {
	var err error
	rule.isIndependent, err = rule.cond.MaybeAddToCron(cron, func() {
		rule.then(nil)
	})
	if err != nil {
		wbgong.Error.Printf("rule %s: invalid cron spec: %s", rule.name, err)
	}
}

func (rule *Rule) Destroy() {
	rule.then = nil
	rule.cond = NewDestroyedRuleCondition()
}

// IsIndependent() returns true if the rule doesn't use any controls for
// its condition yet shouldn't be invoked upon every RunRules().
// This is currently used for cron rules.
func (rule *Rule) IsIndependent() bool {
	return rule.isIndependent
}

// HasDeps checks whether the rule has dependencies
func (rule *Rule) HasDeps() bool {
	return rule.isIndependent || rule.hasDeps
}
