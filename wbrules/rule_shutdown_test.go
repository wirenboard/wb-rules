package wbrules

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/wirenboard/wbgong"
	"github.com/wirenboard/wbgong/testutils"
)

// TestStopUnderLoad stops the engine while rule callbacks, shell commands
// and real timers are still in flight. Every regular suite tears down a
// quiet engine, which is why the shutdown races found by the internal
// rules corpus (esWbSpawn CallSync panic, MaybeCallSync running JS on a foreign
// goroutine, log-during-teardown) survived for years. This test pins the
// fixes: it passes iff the process does not panic.
func TestStopUnderLoad(t *testing.T) {
	script := `
defineVirtualDevice("shutdown_race", {
  cells: { tick: { type: "value", value: 0, readonly: true } }
});
var n = 0;
setInterval(function () {
  n++;
  dev.shutdown_race.tick = n;
  log("tick {}", n);
}, 20);
runShellCommand("sleep 0.2; echo done", {
  captureOutput: true,
  exitCallback: function (code, out) {
    log("late shell callback: {} {}", code, out);
  }
});
log("shutdown-race file loaded");
`
	dir := t.TempDir()
	path := filepath.Join(dir, "shutdown_race.js")
	if err := os.WriteFile(path, []byte(script), 0o644); err != nil {
		t.Fatal(err)
	}

	for i := 0; i < 5; i++ { // several rounds to catch the timing window
		broker := testutils.NewFakeMQTTBroker(t, nil)
		broker.SetWaitForRetained(false)
		driverClient := broker.MakeClient("driver")
		dargs := wbgong.NewDriverArgs().
			SetId("shutdown-driver").
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

		logClient := broker.MakeClient("shutdown-log")
		options := NewESEngineOptions()
		options.SetModulesDirs([]string{testModulesDir()})
		engine, err := NewESEngine(driver, logClient, options)
		if err != nil {
			t.Fatalf("engine: %v", err)
		}
		engine.Start()
		if err := engine.LiveLoadFile(path); err != nil {
			t.Fatalf("load: %v", err)
		}
		// let the ticker fire at least once and the shell command remain
		// in flight, then stop mid-activity. Close also destroys the JS
		// heap, so late completions must go through the (heapless) drop
		// path rather than touch freed memory.
		time.Sleep(time.Duration(10+i*30) * time.Millisecond)
		engine.Close()
		driver.StopLoop()
		driver.Close()
	}
	// give late shell callbacks time to hit the (now safe) drop path
	time.Sleep(300 * time.Millisecond)
}
