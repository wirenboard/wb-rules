package wbrules

import (
	wbgo "github.com/contactless/wbgo"
	"github.com/stretchr/objx"
)

type DepTracker interface {
	StartTrackingDeps()
	StoreRuleCellSpec(rule *Rule, cellSpec *CellSpec)
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
	Check(cell *Cell) (bool, interface{})
	GetCells() []*CellSpec
	MaybeAddToCron(cron Cron, thunk func()) (added bool, err error)
}

type RuleConditionBase struct{}

func (ruleCond *RuleConditionBase) Check(Cell *Cell) (bool, interface{}) {
	return false, nil
}

func (ruleCond *RuleConditionBase) GetCells() []*CellSpec {
	return []*CellSpec{}
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

func (ruleCond *LevelTriggeredRuleCondition) Check(cell *Cell) (bool, interface{}) {
	return ruleCond.cond(), nil
}

type DestroyedRuleCondition struct {
	RuleConditionBase
}

func NewDestroyedRuleCondition() *DestroyedRuleCondition {
	return &DestroyedRuleCondition{}
}

func (ruleCond *DestroyedRuleCondition) Check(cell *Cell) (bool, interface{}) {
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

func (ruleCond *EdgeTriggeredRuleCondition) Check(cell *Cell) (bool, interface{}) {
	current := ruleCond.cond()
	shouldFire := current && (ruleCond.firstRun || current != ruleCond.prevCondValue)
	ruleCond.prevCondValue = current
	ruleCond.firstRun = false
	return shouldFire, nil
}

type CellChangedRuleCondition struct {
	RuleConditionBase
	cellSpec CellSpec
	oldValue interface{}
}

func NewCellChangedRuleCondition(cellSpec CellSpec) (*CellChangedRuleCondition, error) {
	return &CellChangedRuleCondition{
		cellSpec: cellSpec,
		oldValue: nil,
	}, nil
}

func (ruleCond *CellChangedRuleCondition) GetCells() []*CellSpec {
	return []*CellSpec{&ruleCond.cellSpec}
}

func (ruleCond *CellChangedRuleCondition) Check(cell *Cell) (bool, interface{}) {
	if cell == nil || cell.DevName() != ruleCond.cellSpec.DevName ||
		cell.Name() != ruleCond.cellSpec.CellName {
		return false, nil
	}

	if !cell.IsComplete() {
		wbgo.Debug.Printf("skipping rule due to incomplete cell in whenChanged: %s/%s",
			cell.DevName(), cell.Name())
		return false, nil
	}

	v := cell.Value()
	if ruleCond.oldValue == v && !cell.IsButton() {
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

func (ruleCond *FuncValueChangedRuleCondition) Check(cell *Cell) (bool, interface{}) {
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

func (ruleCond *OrRuleCondition) GetCells() []*CellSpec {
	r := make([]*CellSpec, 0, 10)
	for _, cond := range ruleCond.conds {
		r = append(r, cond.GetCells()...)
	}
	return r
}

func (ruleCond *OrRuleCondition) Check(cell *Cell) (bool, interface{}) {
	for _, cond := range ruleCond.conds {
		if shouldFire, newValue := cond.Check(cell); shouldFire {
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

type Rule struct {
	tracker     DepTracker
	name        string
	cond        RuleCondition
	then        ESCallbackFunc
	shouldCheck bool
	nonCellRule bool
}

func NewRule(tracker DepTracker, name string, cond RuleCondition, then ESCallbackFunc) *Rule {
	rule := &Rule{
		tracker:     tracker,
		name:        name,
		cond:        cond,
		then:        then,
		shouldCheck: false,
		nonCellRule: false,
	}
	rule.StoreInitiallyKnownDeps()
	return rule
}

func (rule *Rule) StoreInitiallyKnownDeps() {
	for _, cellSpec := range rule.cond.GetCells() {
		rule.tracker.StoreRuleCellSpec(rule, cellSpec)
	}
}

func (rule *Rule) ShouldCheck() {
	rule.shouldCheck = true
}

func (rule *Rule) Check(cell *Cell) {
	if cell != nil && !rule.shouldCheck {
		// Don't invoke js if no cells mentioned in the
		// condition callback changed. If rules are run
		// not due to a cell being changed, still need
		// to call JS though.
		return
	}
	rule.tracker.StartTrackingDeps()
	shouldFire, newValue := rule.cond.Check(cell)
	var args objx.Map
	rule.tracker.StoreRuleDeps(rule)
	rule.shouldCheck = false

	switch {
	case !shouldFire:
		return
	case newValue != nil:
		args = objx.New(map[string]interface{}{
			"newValue": newValue,
		})
	case cell != nil:
		args = objx.New(map[string]interface{}{
			"device":   cell.DevName(),
			"cell":     cell.Name(),
			"newValue": cell.Value(),
		})
	}
	rule.then(args)
}

func (rule *Rule) MaybeAddToCron(cron Cron) {
	var err error
	rule.nonCellRule, err = rule.cond.MaybeAddToCron(cron, func() {
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

// IsNonCellRule() returns true if the rule doesn't use any cells for
// its condition yet shouldn't be invoked upon every RunRules().
// This is currently used for cron rules.
func (rule *Rule) IsNonCellRule() bool {
	return rule.nonCellRule
}
