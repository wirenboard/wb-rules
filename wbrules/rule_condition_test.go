package wbrules

import (
	"testing"

	wbgong "github.com/wirenboard/wbgong"
)

// TestCellChangedEmptyString tests SOFT-5446: whenChanged must fire
// when a text control value changes to or from an empty string.
// The fix uses IsFirstValue flag set by the engine to distinguish
// first value events from subsequent events where PrevValue is nil
// because ToTypedValue("", "text") returns nil.
func TestCellChangedEmptyString(t *testing.T) {
	spec := ControlSpec{DeviceId: "testdev", ControlId: "text"}

	t.Run("first value event is suppressed", func(t *testing.T) {
		cond, _ := NewCellChangedRuleCondition(spec)
		e := &ControlChangeEvent{
			Spec:         spec,
			ControlType:  wbgong.CONV_TYPE_TEXT,
			IsComplete:   true,
			IsRetained:   true,
			IsFirstValue: true,
			Value:        "test1",
			PrevValue:    nil,
		}
		gotFire, _ := cond.Check(e)
		if gotFire {
			t.Error("first value event should be suppressed")
		}
	})

	t.Run("non-first event with nil PrevValue fires", func(t *testing.T) {
		cond, _ := NewCellChangedRuleCondition(spec)
		e := &ControlChangeEvent{
			Spec:         spec,
			ControlType:  wbgong.CONV_TYPE_TEXT,
			IsComplete:   true,
			IsRetained:   true,
			IsFirstValue: false,
			Value:        "test1",
			PrevValue:    nil,
		}
		gotFire, _ := cond.Check(e)
		if !gotFire {
			t.Error("non-first event with nil PrevValue should fire (SOFT-5446)")
		}
	})

	t.Run("normal changes fire", func(t *testing.T) {
		cond, _ := NewCellChangedRuleCondition(spec)
		e := &ControlChangeEvent{
			Spec:        spec,
			ControlType: wbgong.CONV_TYPE_TEXT,
			IsComplete:  true,
			IsRetained:  true,
			Value:       "test2",
			PrevValue:   "test1",
		}
		gotFire, _ := cond.Check(e)
		if !gotFire {
			t.Error("normal change should fire")
		}
	})

	t.Run("same retained value does not fire", func(t *testing.T) {
		cond, _ := NewCellChangedRuleCondition(spec)
		e := &ControlChangeEvent{
			Spec:        spec,
			ControlType: wbgong.CONV_TYPE_TEXT,
			IsComplete:  true,
			IsRetained:  true,
			Value:       "test1",
			PrevValue:   "test1",
		}
		gotFire, _ := cond.Check(e)
		if gotFire {
			t.Error("same retained value should not fire")
		}
	})

	t.Run("change to empty string fires", func(t *testing.T) {
		cond, _ := NewCellChangedRuleCondition(spec)
		e := &ControlChangeEvent{
			Spec:        spec,
			ControlType: wbgong.CONV_TYPE_TEXT,
			IsComplete:  true,
			IsRetained:  true,
			Value:       "",
			PrevValue:   "test1",
		}
		gotFire, _ := cond.Check(e)
		if !gotFire {
			t.Error("change to empty string should fire")
		}
	})

	t.Run("change from empty string fires", func(t *testing.T) {
		cond, _ := NewCellChangedRuleCondition(spec)
		e := &ControlChangeEvent{
			Spec:        spec,
			ControlType: wbgong.CONV_TYPE_TEXT,
			IsComplete:  true,
			IsRetained:  true,
			Value:       "aaa",
			PrevValue:   "",
		}
		gotFire, _ := cond.Check(e)
		if !gotFire {
			t.Error("change from empty string should fire")
		}
	})

	t.Run("pushbutton with IsFirstValue still fires", func(t *testing.T) {
		cond, _ := NewCellChangedRuleCondition(spec)
		e := &ControlChangeEvent{
			Spec:         spec,
			ControlType:  wbgong.CONV_TYPE_PUSHBUTTON,
			IsComplete:   true,
			IsRetained:   false,
			IsFirstValue: true,
			Value:        "1",
			PrevValue:    nil,
		}
		gotFire, _ := cond.Check(e)
		if !gotFire {
			t.Error("pushbutton should fire even on first value event")
		}
	})
}

// TestFuncValueChangedEmptyString tests SOFT-5446 for function-based whenChanged conditions.
func TestFuncValueChangedEmptyString(t *testing.T) {
	spec := ControlSpec{DeviceId: "testdev", ControlId: "text"}

	t.Run("first value event is suppressed", func(t *testing.T) {
		currentValue := "test1"
		cond := NewFuncValueChangedRuleCondition(func() interface{} {
			return currentValue
		})
		e := &ControlChangeEvent{
			Spec:         spec,
			ControlType:  wbgong.CONV_TYPE_TEXT,
			IsComplete:   true,
			IsRetained:   true,
			IsFirstValue: true,
			Value:        "test1",
			PrevValue:    nil,
		}
		gotFire, _ := cond.Check(e)
		if gotFire {
			t.Error("first value event should be suppressed")
		}
	})

	t.Run("non-first event with nil PrevValue allows thunk evaluation", func(t *testing.T) {
		currentValue := "test1"
		cond := NewFuncValueChangedRuleCondition(func() interface{} {
			return currentValue
		})

		e := &ControlChangeEvent{
			Spec:         spec,
			ControlType:  wbgong.CONV_TYPE_TEXT,
			IsComplete:   true,
			IsRetained:   true,
			IsFirstValue: false,
			Value:        "test1",
			PrevValue:    nil,
		}
		gotFire, _ := cond.Check(e)
		if !gotFire {
			t.Error("non-first event with nil PrevValue should allow thunk evaluation (SOFT-5446)")
		}
	})

	t.Run("normal change fires", func(t *testing.T) {
		currentValue := "test1"
		cond := NewFuncValueChangedRuleCondition(func() interface{} {
			return currentValue
		})
		currentValue = "test2"
		e := &ControlChangeEvent{
			Spec:        spec,
			ControlType: wbgong.CONV_TYPE_TEXT,
			IsComplete:  true,
			IsRetained:  true,
			Value:       "test2",
			PrevValue:   "test1",
		}
		gotFire, _ := cond.Check(e)
		if !gotFire {
			t.Error("normal change should fire")
		}
	})
}
