package wbrules

import (
	wbgo "github.com/contactless/wbgo"
	"github.com/stretchr/objx"
)

const (
	RULE_OR_COND_CAPACITY = 10
)

type DepTracker interface {
	StartTrackingDeps()
	StoreRuleControlSpec(rule *Rule, ctrlSpec ControlSpec)
	StoreRuleDeps(rule *Rule)
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
	Check(e *ControlChangeEvent) (bool, interface{})
	GetControlSpecs() []ControlSpec
	MaybeAddToCron(cron Cron, thunk func()) (added bool, err error)
}

type RuleConditionBase struct{}

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
	oldValue interface{}
}

func NewCellChangedRuleCondition(ctrlSpec ControlSpec) (*CellChangedRuleCondition, error) {
	return &CellChangedRuleCondition{
		ctrlSpec: ctrlSpec,
		oldValue: nil,
	}, nil
}

func (ruleCond *CellChangedRuleCondition) GetControlSpecs() []ControlSpec {
	return []ControlSpec{ruleCond.ctrlSpec}
}

func (ruleCond *CellChangedRuleCondition) Check(e *ControlChangeEvent) (bool, interface{}) {
	if e == nil || e.Spec != ruleCond.ctrlSpec {
		return false, nil
	}

	if !e.IsComplete {
		wbgo.Debug.Printf("skipping rule due to incomplete cell in whenChanged: %s", e.Spec)
		return false, nil
	}

	v := e.Value
	if ruleCond.oldValue == v && !e.IsRetained {
		return false, nil
	}

	ruleCond.oldValue = v
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
	conds []RuleCondition
}

func NewOrRuleCondition(conds []RuleCondition) *OrRuleCondition {
	return &OrRuleCondition{conds: conds}
}

func (ruleCond *OrRuleCondition) GetControlSpecs() []ControlSpec {
	r := make([]ControlSpec, 0, RULE_OR_COND_CAPACITY)
	for _, cond := range ruleCond.conds {
		r = append(r, cond.GetControlSpecs()...)
	}
	return r
}

func (ruleCond *OrRuleCondition) Check(e *ControlChangeEvent) (bool, interface{}) {
	for _, cond := range ruleCond.conds {
		if shouldFire, newValue := cond.Check(e); shouldFire {
			return true, newValue
		}
	}
	return false, nil
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
	name          string // FIXME: deprecated
	cond          RuleCondition
	then          ESCallbackFunc
	shouldCheck   bool
	isIndependent bool
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
		enabled:       true,
	}
	rule.StoreInitiallyKnownDeps()
	return rule
}

func (rule *Rule) StoreInitiallyKnownDeps() {
	for _, ctrlSpec := range rule.cond.GetControlSpecs() {
		rule.tracker.StoreRuleControlSpec(rule, ctrlSpec)
	}
}

func (rule *Rule) ShouldCheck() {
	rule.shouldCheck = true
}

func (rule *Rule) Check(e *ControlChangeEvent) {
	if !rule.shouldCheck {
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
		case e != nil:
			args = objx.New(map[string]interface{}{
				"device":   e.Spec.DeviceId,
				"cell":     e.Spec.ControlId,
				"newValue": e.Value,
			})
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
		wbgo.Error.Printf("rule %s: invalid cron spec: %s", rule.name, err)
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
