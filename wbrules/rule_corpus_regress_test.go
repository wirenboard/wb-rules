package wbrules

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/wirenboard/wbgong"
	"github.com/wirenboard/wbgong/testutils"
)

// Synthetic regressions distilled from running the internal rules corpus
// through the engine (see corpus_test.go). Each test is the minimal
// reproduction of a bug that the corpus surfaced and the regular suites
// missed.

// StrictRegressSuite: corpus scripts use 'use strict'; every proxy set
// trap (dev cell, PersistentStorage, StorableObject) must return true or
// strict-mode assignments throw "proxy: cannot set property".
type StrictRegressSuite struct {
	RuleSuiteBase
}

func (s *StrictRegressSuite) SetupTest() {
	s.SetupSkippingDefs("testrules_strict.js")
}

func (s *StrictRegressSuite) TestStrictModeProxyWrites() {
	s.publish("/devices/strict_demo/controls/trigger/on", "1", "strict_demo/trigger", "strict_demo/count")
	s.VerifyUnordered(
		"tst -> /devices/strict_demo/controls/trigger/on: [1] (QoS 1)",
		"driver -> /devices/strict_demo/controls/trigger: [1] (QoS 1, retained)",
		"driver -> /devices/strict_demo/controls/count: [1] (QoS 1, retained)",
		"wbrules-log -> /wbrules/log/info: [strict count 1 ps 1] (QoS 1)",
	)
}

// ExportsRegressSuite: module files pasted as rule files must load;
// exports is in scope for rule files and aliases module.exports.
type ExportsRegressSuite struct {
	RuleSuiteBase
}

func (s *ExportsRegressSuite) SetupTest() {
	s.SetupSkippingDefs("testrules_exports.js")
}

func (s *ExportsRegressSuite) TestExportsInRuleFile() {
	s.publish("/devices/exports_demo/controls/poke/on", "1", "exports_demo/poke")
	s.VerifyUnordered(
		"tst -> /devices/exports_demo/controls/poke/on: [1] (QoS 1)",
		"driver -> /devices/exports_demo/controls/poke: [1] (QoS 1, retained)",
		"wbrules-log -> /wbrules/log/info: [exports ok: 42 six by nine] (QoS 1)",
	)
}

func TestCorpusRegressSuites(t *testing.T) {
	testutils.RunSuites(t, new(StrictRegressSuite), new(ExportsRegressSuite))
}

// bareCorpusEngine mirrors how corpus scripts met the engine: no prior
// activity, default options except what the test overrides.
func bareCorpusEngine(t *testing.T, tweak func(*ESEngineOptions)) (*ESEngine, func()) {
	broker := testutils.NewFakeMQTTBroker(t, nil)
	broker.SetWaitForRetained(false)
	driverClient := broker.MakeClient("driver")
	dargs := wbgong.NewDriverArgs().
		SetId("bare-driver").
		SetMqtt(driverClient).
		SetTesting()
	dargs.SetUseStorage(false)

	driver, err := wbgong.NewDriverBase(dargs)
	if err != nil {
		t.Fatalf("driver: %v", err)
	}
	if err := driver.StartLoop(); err != nil {
		t.Fatalf("driver loop: %v", err)
	}
	driver.WaitForReady()
	driver.SetFilter(&wbgong.AllDevicesFilter{})

	logClient := broker.MakeClient("bare-log")
	options := NewESEngineOptions()
	options.SetModulesDirs([]string{testModulesDir()})
	if tweak != nil {
		tweak(options)
	}
	engine, err := NewESEngine(driver, logClient, options)
	if err != nil {
		t.Fatalf("engine: %v", err)
	}
	engine.Start()
	return engine, func() {
		engine.Stop()
		driver.StopLoop()
		driver.Close()
	}
}

func writeScript(t *testing.T, name, body string) string {
	path := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func loadedEntryError(t *testing.T, engine *ESEngine, path string) string {
	entries, err := engine.ListSourceFiles()
	if err != nil {
		t.Fatalf("ListSourceFiles: %v", err)
	}
	for _, entry := range entries {
		if entry.PhysicalPath == path && entry.Error != nil {
			return entry.Error.Message
		}
	}
	return ""
}

// A corpus script's very first action was trackMqtt(): the subscribe path
// must lazily start the engine MQTT client just like Publish does, or it
// panics with "client not started".
func TestTrackMqttAsFirstAction(t *testing.T) {
	engine, cleanup := bareCorpusEngine(t, nil)
	defer cleanup()
	path := writeScript(t, "first.js", `
trackMqtt("/corpus/first/#", function (msg) {
  log("seen {}", msg.value);
});
`)
	if err := engine.LiveLoadFile(path); err != nil {
		t.Fatalf("load: %v", err)
	}
	if msg := loadedEntryError(t, engine, path); msg != "" {
		t.Fatalf("script error: %s", msg)
	}
}

// require() of an unknown module must fail with a readable message, not
// a bare "error error (rc -100)".
func TestMissingModuleErrorMessage(t *testing.T) {
	engine, cleanup := bareCorpusEngine(t, nil)
	defer cleanup()
	path := writeScript(t, "missing.js", `var x = require("no-such-module-xyz");`)
	msg := ""
	if err := engine.LiveLoadFile(path); err != nil {
		msg = err.Error()
	} else {
		msg = loadedEntryError(t, engine, path)
	}
	if !strings.Contains(msg, `cannot find module "no-such-module-xyz"`) {
		t.Fatalf("expected descriptive missing-module error, got: %q", msg)
	}
}

// A runaway script (while(true)) froze the old engine forever; with
// JsExecutionLimit it must error out and leave the engine usable.
func TestRunawayScriptInterrupted(t *testing.T) {
	engine, cleanup := bareCorpusEngine(t, func(o *ESEngineOptions) {
		o.JsExecutionLimit = 300 * time.Millisecond
	})
	defer cleanup()

	bad := writeScript(t, "runaway.js", `while (true) {}`)
	loadErr := make(chan error, 1)
	go func() { loadErr <- engine.LiveLoadFile(bad) }()
	var err error
	select {
	case err = <-loadErr:
	case <-time.After(15 * time.Second):
		// fail cleanly instead of hanging the whole package on regression
		t.Fatal("interrupt did not fire: load still blocked after 15s")
	}
	msg := ""
	if err != nil {
		msg = err.Error()
	} else {
		msg = loadedEntryError(t, engine, bad)
	}
	if msg == "" {
		t.Fatal("runaway script loaded without error")
	}
	if !strings.Contains(msg, "execution timed out") {
		t.Fatalf("runaway abort must carry the clear watchdog message, got: %q", msg)
	}

	// the engine must still load scripts afterwards
	good := writeScript(t, "fine.js", `log("still alive");`)
	if err := engine.LiveLoadFile(good); err != nil {
		t.Fatalf("engine unusable after interrupt: %v", err)
	}
	if m := loadedEntryError(t, engine, good); m != "" {
		t.Fatalf("good script errored after interrupt: %s", m)
	}
}

// A builtin called with fewer arguments than it inspects (trackMqtt without
// a callback) is a rule error, never a process crash: the shim answers
// Duktape-style "not a function" for the missing argument.
func TestBuiltinWithMissingArgumentIsARuleError(t *testing.T) {
	engine, cleanup := bareCorpusEngine(t, nil)
	defer cleanup()
	path := writeScript(t, "short.js", `trackMqtt("/corpus/short/#"); log("after");`)
	msg := ""
	if err := engine.LiveLoadFile(path); err != nil {
		msg = err.Error()
	} else {
		msg = loadedEntryError(t, engine, path)
	}
	if msg == "" {
		t.Fatal("trackMqtt(topic) without a callback must be reported as an error")
	}
	good := writeScript(t, "after.js", `log("still alive");`)
	if err := engine.LiveLoadFile(good); err != nil {
		t.Fatalf("engine unusable afterwards: %v", err)
	}
}

// Rule code that recurses through the engine - a rule running itself with
// runRule(), runRules() from a level-triggered rule, a toString that calls
// format() - used to run the process off its C stack (each JS -> Go -> JS
// hop re-anchored QuickJS's stack check). It is a rule error now, and the
// engine keeps working.
func TestHostRecursionFromRulesIsAnError(t *testing.T) {
	engine, cleanup := bareCorpusEngine(t, nil)
	defer cleanup()
	cases := map[string]string{
		"runrule.js": `
var id = defineRule("self", {
  whenChanged: "recursion/never",
  then: function () { runRule(id); }
});
runRule(id);
log("after self-run");
`,
		"tostring.js": `
var o = { toString: function () { return format("{}", o); } };
log("{}", o);
log("after toString");
`,
	}
	for name, src := range cases {
		path := writeScript(t, name, src)
		errCh := make(chan error, 1)
		go func() { errCh <- engine.LiveLoadFile(path) }()
		select {
		case <-errCh:
		case <-time.After(60 * time.Second):
			t.Fatalf("%s: load did not finish", name)
		}
		// either the load reports the error or the script logged it and went on
		msg := loadedEntryError(t, engine, path)
		if msg != "" && !strings.Contains(msg, "recursion limit") && !strings.Contains(msg, "stack overflow") {
			t.Fatalf("%s: unexpected error: %s", name, msg)
		}
	}
	good := writeScript(t, "after-recursion.js", `log("still alive");`)
	if err := engine.LiveLoadFile(good); err != nil {
		t.Fatalf("engine unusable after recursion: %v", err)
	}
	if m := loadedEntryError(t, engine, good); m != "" {
		t.Fatalf("good script errored after recursion: %s", m)
	}
}
