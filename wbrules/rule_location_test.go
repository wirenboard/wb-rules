package wbrules

import (
	"sync"
	"testing"

	"github.com/evgeny-boger/wbgo/testutils"
)

type RuleLocationSuite struct {
	RuleSuiteBase
}

func (s *RuleLocationSuite) SetupTest() {
	s.SetupSkippingDefs(
		"testrules_defhelper.js",
		"testrules_locations.js",
		"testrules_locations_dis.js.disabled",
		"loc1/testrules_more.js")

	// FIXME: need to wait for the engine to become ready because
	// the engine cannot be stopped before it's ready in the
	// context of the tests.
	ready := false
	var mtx sync.Mutex
	s.engine.WhenEngineReady(func() {
		mtx.Lock()
		ready = true
		mtx.Unlock()
	})
	s.WaitFor(func() bool {
		mtx.Lock()
		defer mtx.Unlock()
		return ready
	})
}

func (s *RuleLocationSuite) listSourceFiles() (entries []LocFileEntry) {
	var err error
	entries, err = s.engine.ListSourceFiles()
	s.Ck("ListSourceFiles", err)
	return
}

func (s *RuleLocationSuite) TestLocations() {
	s.Equal([]LocFileEntry{
		{
			VirtualPath:  "loc1/testrules_more.js",
			PhysicalPath: s.DataFilePath("loc1/testrules_more.js"),
			Enabled:      true,
			Devices: []LocItem{
				{4, "qqq"},
			},
			Rules:  []LocItem{},
			Timers: []LocItem{},
		},
		{
			VirtualPath:  "testrules_defhelper.js",
			PhysicalPath: s.DataFilePath("testrules_defhelper.js"),
			Enabled:      true,
			Devices:      []LocItem{},
			Rules:        []LocItem{},
			Timers:       []LocItem{},
		},
		{
			VirtualPath:  "testrules_locations.js",
			PhysicalPath: s.DataFilePath("testrules_locations.js"),
			Enabled:      true,
			Devices: []LocItem{
				{4, "misc"},
				{14, "foo"},
			},
			Rules: []LocItem{
				{7, "whateverRule"},
				// the problem with duktape: the last line of the
				// defineRule() call is recorded
				{24, "another"},
			},
			Timers: []LocItem{},
		},
		{
			VirtualPath:  "testrules_locations_dis.js",
			PhysicalPath: s.DataFilePath("testrules_locations_dis.js.disabled"),
			Enabled:      false,
			Devices:      []LocItem{},
			Rules:        []LocItem{},
			Timers:       []LocItem{},
		},
	}, s.listSourceFiles())
}

func (s *RuleLocationSuite) TestUpdatingLocations() {
	s.ReplaceScript("testrules_locations.js", "testrules_locations_changed.js")
	s.ReplaceScript("loc1/testrules_more.js", "loc1/testrules_more_changed.js")
	s.Equal([]LocFileEntry{
		{
			VirtualPath:  "loc1/testrules_more.js",
			PhysicalPath: s.DataFilePath("loc1/testrules_more.js"),
			Enabled:      true,
			Devices: []LocItem{
				{4, "qqqNew"},
			},
			Rules:  []LocItem{},
			Timers: []LocItem{},
		},
		{
			VirtualPath:  "testrules_defhelper.js",
			PhysicalPath: s.DataFilePath("testrules_defhelper.js"),
			Enabled:      true,
			Devices:      []LocItem{},
			Rules:        []LocItem{},
			Timers:       []LocItem{},
		},
		{
			VirtualPath:  "testrules_locations.js",
			PhysicalPath: s.DataFilePath("testrules_locations.js"),
			Enabled:      true,
			Devices: []LocItem{
				{4, "miscNew"},
				{14, "foo"},
			},
			Rules: []LocItem{
				{7, "whateverNewRule"},
				// a problem with duktape: the last line of the
				// defineRule() call is recorded
				{24, "another"},
			},
			Timers: []LocItem{},
		},
		{
			VirtualPath:  "testrules_locations_dis.js",
			PhysicalPath: s.DataFilePath("testrules_locations_dis.js.disabled"),
			Enabled:      false,
			Devices:      []LocItem{},
			Rules:        []LocItem{},
			Timers:       []LocItem{},
		},
	}, s.listSourceFiles())
	s.SkipTill("[changed] loc1/testrules_more.js")
}

func (s *RuleLocationSuite) TestRemoval() {
	s.RemoveScript("testrules_locations.js")
	s.WaitFor(func() bool {
		return len(s.listSourceFiles()) == 3
	})
	s.Equal([]LocFileEntry{
		{
			VirtualPath:  "loc1/testrules_more.js",
			PhysicalPath: s.DataFilePath("loc1/testrules_more.js"),
			Enabled:      true,
			Devices: []LocItem{
				{4, "qqq"},
			},
			Rules:  []LocItem{},
			Timers: []LocItem{},
		},
		{
			VirtualPath:  "testrules_defhelper.js",
			PhysicalPath: s.DataFilePath("testrules_defhelper.js"),
			Enabled:      true,
			Devices:      []LocItem{},
			Rules:        []LocItem{},
			Timers:       []LocItem{},
		},
		{
			VirtualPath:  "testrules_locations_dis.js",
			PhysicalPath: s.DataFilePath("testrules_locations_dis.js.disabled"),
			Enabled:      false,
			Devices:      []LocItem{},
			Rules:        []LocItem{},
			Timers:       []LocItem{},
		},
	}, s.listSourceFiles())
	s.SkipTill("[removed] testrules_locations.js")

	s.RemoveScript("loc1/testrules_more.js")
	s.WaitFor(func() bool {
		return len(s.listSourceFiles()) == 2
	})
	s.Equal([]LocFileEntry{
		{
			VirtualPath:  "testrules_defhelper.js",
			PhysicalPath: s.DataFilePath("testrules_defhelper.js"),
			Enabled:      true,
			Devices:      []LocItem{},
			Rules:        []LocItem{},
			Timers:       []LocItem{},
		},
		{
			VirtualPath:  "testrules_locations_dis.js",
			PhysicalPath: s.DataFilePath("testrules_locations_dis.js.disabled"),
			Enabled:      false,
			Devices:      []LocItem{},
			Rules:        []LocItem{},
			Timers:       []LocItem{},
		},
	}, s.listSourceFiles())
	s.SkipTill("[removed] loc1/testrules_more.js")
}

func (s *RuleLocationSuite) TestFaultyScript() {
	err := s.OverwriteScript("testrules_locations_faulty.js", "testrules_locations_faulty.js")
	s.NotNil(err, "error expected")
	scriptErr, ok := err.(ScriptError)
	s.Require().True(ok, "ScriptError expected")
	s.Contains(scriptErr.Message, "ReferenceError")
	s.Equal([]LocItem{
		{6, "testrules_locations_faulty.js"},
	}, scriptErr.Traceback)
	s.Equal([]LocFileEntry{
		{
			VirtualPath:  "loc1/testrules_more.js",
			PhysicalPath: s.DataFilePath("loc1/testrules_more.js"),
			Enabled:      true,
			Devices: []LocItem{
				{4, "qqq"},
			},
			Rules:  []LocItem{},
			Timers: []LocItem{},
		},
		{
			VirtualPath:  "testrules_defhelper.js",
			PhysicalPath: s.DataFilePath("testrules_defhelper.js"),
			Enabled:      true,
			Devices:      []LocItem{},
			Rules:        []LocItem{},
			Timers:       []LocItem{},
		},
		{
			VirtualPath:  "testrules_locations.js",
			PhysicalPath: s.DataFilePath("testrules_locations.js"),
			Enabled:      true,
			Devices: []LocItem{
				{4, "misc"},
				{14, "foo"},
			},
			Rules: []LocItem{
				{7, "whateverRule"},
				// the problem with duktape: the last line of the
				// defineRule() call is recorded
				{24, "another"},
			},
			Timers: []LocItem{},
		},
		{
			VirtualPath:  "testrules_locations_dis.js",
			PhysicalPath: s.DataFilePath("testrules_locations_dis.js.disabled"),
			Enabled:      false,
			Devices:      []LocItem{},
			Rules:        []LocItem{},
			Timers:       []LocItem{},
		},
		{
			VirtualPath:  "testrules_locations_faulty.js",
			PhysicalPath: s.DataFilePath("testrules_locations_faulty.js"),
			Enabled:      true,
			Devices: []LocItem{
				{4, "nonFaultyDev"},
			},
			Rules:  []LocItem{},
			Timers: []LocItem{},
			Error:  &scriptErr,
		},
	}, s.listSourceFiles())

	s.SkipTill("[changed] testrules_locations_faulty.js")
}

func (s *RuleLocationSuite) TestSyntaxError() {
	err := s.OverwriteScript(
		"testrules_locations_syntax_error.js",
		"testrules_locations_syntax_error.js")
	s.NotNil(err, "error expected")
	scriptErr, ok := err.(ScriptError)
	s.Require().True(ok, "ScriptError expected")
	s.Contains(scriptErr.Message, "SyntaxError")
	s.Equal([]LocItem{
		{4, "testrules_locations_syntax_error.js"},
	}, scriptErr.Traceback)

	s.SkipTill("[changed] testrules_locations_syntax_error.js")
}

func TestRuleLocationSuite(t *testing.T) {
	testutils.RunSuites(t,
		new(RuleLocationSuite),
	)
}
