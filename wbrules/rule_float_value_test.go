package wbrules

import (
	"testing"

	"github.com/wirenboard/wbgong/testutils"
)

type FloatValueSuite struct {
	RuleSuiteBase
}

func (s *FloatValueSuite) SetupTest() {
	s.SetupSkippingDefs("testrules_float_value.js")
}

// Fractional control values must survive the whole vdev path: cell
// definition, addControl, setValue and the dev[] read-back. A 32-bit build
// once classified every float as an object (raw QuickJS tag under
// JS_NAN_BOXING), which sent them down object-conversion paths.
func (s *FloatValueSuite) TestFloatControlValues() {
	s.publish("/devices/floatdev/controls/pre/on", "2.25", "floatdev/pre", "floatdev/temp")
	s.SkipTill("[info] float values: 2.25 36.6")
}

func TestFloatValueSuite(t *testing.T) {
	testutils.RunSuites(t, new(FloatValueSuite))
}
