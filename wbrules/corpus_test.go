package wbrules

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/wirenboard/wbgong"
	"github.com/wirenboard/wbgong/testutils"
)

// TestWildCorpus loads every script from the directory named by
// WB_RULES_CORPUS through a real engine (fake broker) and reports
// per-script compatibility. Scripts harvested from wiki.wirenboard.com and
// support.wirenboard.com are user-written ES5 for the old Duktape engine —
// the point is to prove the QuickJS engine loads them all.
//
// Classification:
//   - ok:               loaded cleanly
//   - module-missing:   requires a user module we don't have (environmental)
//   - engine-error:     anything else — a genuine compatibility problem
func TestWildCorpus(t *testing.T) {
	corpusDir := os.Getenv("WB_RULES_CORPUS")
	if corpusDir == "" {
		t.Skip("WB_RULES_CORPUS not set")
	}
	files, err := filepath.Glob(filepath.Join(corpusDir, "*.js"))
	if err != nil || len(files) == 0 {
		t.Fatalf("no corpus files in %s", corpusDir)
	}
	sort.Strings(files)

	// WB_RULES_CORPUS_SHARD="k/N" lets several test processes split the
	// corpus; combined with per-file progress on stderr this keeps the
	// wall time and the slow files visible.
	if shard := os.Getenv("WB_RULES_CORPUS_SHARD"); shard != "" {
		var k, n int
		if _, err := fmt.Sscanf(shard, "%d/%d", &k, &n); err != nil || n <= 0 || k < 0 || k >= n {
			t.Fatalf("bad WB_RULES_CORPUS_SHARD %q", shard)
		}
		var mine []string
		for i, f := range files {
			if i%n == k {
				mine = append(mine, f)
			}
		}
		files = mine
	}

	var ok, moduleMissing int
	var engineErrors []string

	// The fake broker records every MQTT message into a bounded channel
	// meant for Verify() assertions. The corpus never asserts on traffic,
	// and a wild script defining dozens of controls overflows the channel,
	// wedging the driver mid-transaction (seen as a defineVirtualDevice
	// deadlock). Share one recorder and drain it forever.
	rec := testutils.NewRecorder(t)
	rec.SetEmptyWaitTime(time.Hour)
	go func() {
		anything := regexp.MustCompile("(?s).*")
		for {
			rec.Verify(anything)
		}
	}()

	for i, file := range files {
		start := time.Now()
		err := loadInFreshEngine(t, rec, file)
		verdict := "ok"
		if err != "" {
			verdict = firstLine(err)
		}
		fmt.Fprintf(os.Stderr, "CORPUS %d/%d %.1fs %s: %s\n",
			i+1, len(files), time.Since(start).Seconds(), filepath.Base(file), verdict)
		switch {
		case err == "":
			ok++
		case strings.Contains(err, "requiring module"):
			moduleMissing++
		default:
			engineErrors = append(engineErrors, fmt.Sprintf("%s: %s", filepath.Base(file), firstLine(err)))
		}
	}

	t.Logf("corpus: %d files, %d ok, %d module-missing, %d engine errors",
		len(files), ok, moduleMissing, len(engineErrors))
	for _, e := range engineErrors {
		t.Errorf("engine error: %s", e)
	}
}

func firstLine(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return s[:i]
	}
	return s
}

// loadInFreshEngine spins up an isolated engine, loads one file, and
// returns the script error text ("" if it loaded cleanly).
func loadInFreshEngine(t *testing.T, rec *testutils.Recorder, path string) (scriptErr string) {
	broker := testutils.NewFakeMQTTBroker(t, rec)
	broker.SetWaitForRetained(false)
	driverClient := broker.MakeClient("driver")
	dargs := wbgong.NewDriverArgs().
		SetId("corpus-driver").
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
	defer driver.Close()
	defer driver.StopLoop()
	driver.WaitForReady()
	driver.SetFilter(&wbgong.AllDevicesFilter{})

	logClient := broker.MakeClient("corpus-log")
	options := NewESEngineOptions()
	_, thisFile, _, _ := runtime.Caller(0)
	options.SetModulesDirs([]string{filepath.Join(filepath.Dir(thisFile), "..", "modules")})
	options.JsExecutionLimit = 2 * time.Second
	// wild scripts use PersistentStorage; without a DB it throws rc -100
	options.SetPersistentDBFile(filepath.Join(t.TempDir(), "corpus-pdb.db"))

	engine, err := NewESEngine(driver, logClient, options)
	if err != nil {
		t.Fatalf("engine: %v", err)
	}
	engine.Start()
	defer engine.Stop()

	if err := engine.LiveLoadFile(path); err != nil {
		return err.Error()
	}
	// find the freshly loaded entry and report its script error, if any
	entries, lerr := engine.ListSourceFiles()
	if lerr != nil {
		return lerr.Error()
	}
	for _, entry := range entries {
		if entry.PhysicalPath == path && entry.Error != nil {
			return entry.Error.Message
		}
	}
	return ""
}
