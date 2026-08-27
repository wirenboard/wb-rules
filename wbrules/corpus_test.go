package wbrules

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/wirenboard/wbgong"
	"github.com/wirenboard/wbgong/testutils"
)

// The internal Wiren Board rules corpus (a private repository) bulk-loaded
// through a real engine (fake broker), each script classified as
//   - ok:              loaded cleanly
//   - module-missing:  requires a user module we don't have (environmental)
//   - <error>:         anything else - a script-side error (syntax errors in
//                      user code, ReferenceErrors, ...) OR an engine bug
//
// The engine cannot tell those two apart, so the test is a SNAPSHOT: every
// script's verdict is recorded in testdata/corpus-verdicts.txt (committed
// here, alongside the engine, and tied to the corpus commit pinned as the
// testdata/corpus submodule). The snapshot is opaque about the corpus: it is
// keyed by a hash of each script's content, and an error verdict records
// only the error class plus a hash of the message ("error SyntaxError
// 3fa2c1d0"); the script name and message text are printed only locally,
// when a verdict differs. A verdict that differs from the snapshot - a script
// that used to load and now does not, a different error, or the other way
// round - fails the test; an engine crash fails it outright. Review the diff
// and, if the change is intended, refresh the snapshot with
//   WB_RULES_CORPUS_UPDATE=1 go test ./wbrules -run TestCorpus
//
// Corpus scripts are untrusted code, and the harness limits - but does not
// eliminate - what they can touch. What IS stubbed or contained: spawn()
// and runShellCommand() (and Notify.*, which shells out through them) are
// replaced by a stub, so no shell command runs on the host; readConfig()
// is stubbed, so no host file is read (nor leaked through a verdict-diff
// message); PersistentStorage writes to a throwaway per-run DB; MQTT goes
// to the in-process fake broker; runaway scripts are bounded by the JS
// execution and heap limits. What is NOT isolated: the scripts execute in
// this test process with its privileges - require() reads *.js through the
// configured modules directories, and any engine API not listed above runs
// its real implementation. This is process-level stubbing, not an OS
// sandbox; scripts needing a stronger boundary must not be pointed at it.
//
// Where the corpus comes from (first match wins):
//   1. WB_RULES_CORPUS=<dir of *.js>  - explicit
//   2. wbrules/testdata/corpus/scripts - the pinned submodule; it is
//      `update = none` in .gitmodules, so `git clone --recursive` and
//      `git submodule update --init --recursive` SKIP it (external
//      contributors have no access and lose nothing). Internal developers
//      opt in with `make corpus`; the CI pipeline has to opt in the same way
//      (git submodule update --init --checkout wbrules/testdata/corpus)
//      before running the tests.
//   3. ../wb-rules-corpus/scripts     - a sibling checkout
// With none of these the test SKIPS - or FAILS when WB_RULES_CORPUS_REQUIRED
// is set (for a CI job that is meant to run the corpus, so a missing
// checkout cannot pass silently). The bundled synthetic example corpus
// (TestCorpusExample) always runs, so the harness itself is exercised
// everywhere and the three buckets are documented by example.
//
// WB_RULES_CORPUS_SHARD="k/N" splits the corpus between processes.

const corpusVerdictsFile = "corpus-verdicts.txt"

// corpusDir resolves the corpus location; "" if none is available.
func corpusDir() string {
	if d := os.Getenv("WB_RULES_CORPUS"); d != "" {
		return d
	}
	repoRoot := filepath.Dir(testModulesDir())
	for _, d := range []string{
		filepath.Join(repoRoot, "wbrules", "testdata", "corpus", "scripts"),
		filepath.Join(repoRoot, "..", "wb-rules-corpus", "scripts"),
	} {
		if files, _ := filepath.Glob(filepath.Join(d, "*.js")); len(files) > 0 {
			return d
		}
	}
	return ""
}

func TestCorpusExample(t *testing.T) {
	dir := filepath.Join(filepath.Dir(testModulesDir()), "wbrules", "testdata", "corpus-example")
	res := runCorpus(t, dir)
	// the example corpus deliberately contains broken files; assert the
	// classifier put everything in the right bucket
	if res.ok != 3 || res.moduleMissing != 1 || res.errs != 2 {
		t.Errorf("example corpus classification changed: ok=%d module-missing=%d errors=%d (want 3/1/2): %v",
			res.ok, res.moduleMissing, res.errs, res.details)
	}
}

func TestCorpus(t *testing.T) {
	dir := corpusDir()
	if dir == "" {
		msg := "corpus not available: set WB_RULES_CORPUS=<dir>, or (with access to the " +
			"internal corpus repository) run `make corpus`; the bundled example corpus " +
			"still runs in TestCorpusExample"
		if os.Getenv("WB_RULES_CORPUS_REQUIRED") != "" {
			t.Fatal(msg + " (WB_RULES_CORPUS_REQUIRED is set)")
		}
		t.Skip(msg)
	}
	res := runCorpus(t, dir)
	t.Logf("corpus %s: %d files, %d ok, %d module-missing, %d errors",
		dir, len(res.verdicts), res.ok, res.moduleMissing, res.errs)

	snapshot := filepath.Join(filepath.Dir(testModulesDir()), "wbrules", "testdata", corpusVerdictsFile)
	shard := os.Getenv("WB_RULES_CORPUS_SHARD")
	if os.Getenv("WB_RULES_CORPUS_UPDATE") != "" {
		if shard != "" {
			t.Fatal("WB_RULES_CORPUS_UPDATE with WB_RULES_CORPUS_SHARD would write a partial snapshot; run the update unsharded")
		}
		if err := writeCorpusVerdicts(snapshot, res.verdicts); err != nil {
			t.Fatalf("write %s: %v", snapshot, err)
		}
		t.Logf("snapshot written: %s (%d verdicts) - review the diff and commit it", snapshot, len(res.verdicts))
		return
	}
	expected, err := readCorpusVerdicts(snapshot)
	if err != nil {
		t.Fatalf("read %s: %v (WB_RULES_CORPUS_UPDATE=1 creates it)", snapshot, err)
	}
	if shard != "" {
		// a shard only saw its slice: compare on that slice
		for key := range expected {
			if _, ran := res.verdicts[key]; !ran {
				delete(expected, key)
			}
		}
	}

	var changed, added, removed []string
	for key, got := range res.verdicts {
		want, known := expected[key]
		switch {
		case !known:
			added = append(added, fmt.Sprintf("%s (%s): %s", res.names[key], key, res.details[key]))
		case want != got:
			changed = append(changed, fmt.Sprintf("%s (%s):\n    was: %s\n    now: %s (%s)",
				res.names[key], key, want, got, res.details[key]))
		}
	}
	for key, want := range expected {
		if _, ran := res.verdicts[key]; !ran {
			removed = append(removed, key+": "+want)
		}
	}
	sort.Strings(changed)
	sort.Strings(added)
	sort.Strings(removed)
	if len(changed)+len(added)+len(removed) == 0 {
		return
	}
	// engine crashes and runaway scripts have already failed the process or
	// shown up as verdict changes; everything reported here is a snapshot
	// discrepancy the developer must look at.
	for _, c := range changed {
		t.Errorf("verdict changed: %s", c)
	}
	for _, a := range added {
		t.Errorf("no recorded verdict (new corpus file): %s", a)
	}
	for _, r := range removed {
		t.Errorf("recorded verdict but file missing from corpus: %s", r)
	}
	t.Errorf("%d changed, %d new, %d missing vs %s - if intended, refresh with "+
		"WB_RULES_CORPUS_UPDATE=1 go test ./wbrules -run TestCorpus (and commit "+
		"the pinned corpus submodule together with the snapshot)",
		len(changed), len(added), len(removed), corpusVerdictsFile)
}

// verdictOf classifies a load result into the snapshot form - "ok",
// "module-missing", or "error <Class> <hash8>" (the error class is the word
// before the first colon of the message's first line; the hash covers the
// whole first line, so a changed message is detected without recording it) -
// plus the bucket and the human-readable detail (first line) for local
// messages.
func verdictOf(err string) (verdict, bucket, detail string) {
	switch {
	case err == "":
		return "ok", "ok", ""
	case strings.Contains(err, "cannot find module") || strings.Contains(err, "requiring module"):
		return "module-missing", "module-missing", firstLine(err)
	default:
		line := firstLine(err)
		class, _, found := strings.Cut(line, ":")
		if !found || strings.ContainsAny(class, " '\"/") {
			class = "Error"
		}
		sum := sha256.Sum256([]byte(line))
		return fmt.Sprintf("error %s %s", class, hex.EncodeToString(sum[:4])), "error", line
	}
}

func readCorpusVerdicts(path string) (map[string]string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	out := map[string]string{}
	for i, line := range strings.Split(string(data), "\n") {
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		key, verdict, found := strings.Cut(line, "\t")
		if !found {
			return nil, fmt.Errorf("%s:%d: expected <hash>TAB<verdict>", path, i+1)
		}
		out[key] = verdict
	}
	return out, nil
}

func writeCorpusVerdicts(path string, verdicts map[string]string) error {
	names := make([]string, 0, len(verdicts))
	for n := range verdicts {
		names = append(names, n)
	}
	sort.Strings(names)
	var b strings.Builder
	b.WriteString("# Load verdicts for the internal rules corpus (see corpus_test.go). One line per script:\n")
	b.WriteString("# <sha256 of the script content, 16 hex>TAB<ok | module-missing | error <class> <sha256 of the message, 8 hex>>\n")
	b.WriteString("# Regenerate: WB_RULES_CORPUS_UPDATE=1 go test ./wbrules -run TestCorpus\n")
	for _, n := range names {
		b.WriteString(n)
		b.WriteByte('\t')
		b.WriteString(verdicts[n])
		b.WriteByte('\n')
	}
	return os.WriteFile(path, []byte(b.String()), 0644)
}

// corpusKey identifies a script in the snapshot: a hash of its content, so
// the committed snapshot carries no file names.
func corpusKey(path string) (string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:8]), nil
}

// corpusResult is what runCorpus returns: snapshot verdicts and local-only
// names/details, both keyed by corpusKey, plus bucket counts.
type corpusResult struct {
	verdicts      map[string]string
	names         map[string]string
	details       map[string]string
	ok            int
	moduleMissing int
	errs          int
}

// runCorpus loads every *.js in dir through a fresh engine each.
func runCorpus(t *testing.T, dir string) corpusResult {
	// The global wbgong loggers stay bound to whichever suite test called
	// SetupTestLogging last; when the corpus runs after a suite, the first
	// engine log line would hit that completed test's T and panic the run.
	// Bind them to this test for the duration of the corpus.
	testutils.SetupTestLogging(t)

	files, err := filepath.Glob(filepath.Join(dir, "*.js"))
	if err != nil || len(files) == 0 {
		t.Fatalf("no corpus files in %s", dir)
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

	res := corpusResult{
		verdicts: make(map[string]string, len(files)),
		names:    make(map[string]string, len(files)),
		details:  make(map[string]string, len(files)),
	}

	// The fake broker records every MQTT message into a bounded channel
	// meant for Verify() assertions. The corpus never asserts on traffic,
	// and a wild script defining dozens of controls overflows the channel,
	// wedging the driver mid-transaction (seen as a defineVirtualDevice
	// deadlock). Share one recorder and drain it forever.
	rec := testutils.NewRecorder(t)
	rec.SetEmptyWaitTime(time.Hour)
	// The drainer keeps the recorder channel from filling up (a full
	// channel deadlocks the driver mid-transaction). It must stop BEFORE
	// the test returns: a Verify timing out post-test calls FailNow,
	// which silently marks the completed test failed even though the
	// panic it raises is recovered. Remove once wbgong#30 (non-blocking
	// Rec) is vendored.
	var stopDrain int32
	drainDone := make(chan struct{})
	go func() {
		defer close(drainDone)
		defer func() { recover() }()
		anything := regexp.MustCompile("(?s).*")
		for atomic.LoadInt32(&stopDrain) == 0 {
			rec.Verify(anything)
		}
	}()
	defer func() {
		atomic.StoreInt32(&stopDrain, 1)
		rec.Rec("corpus drain end") // unblock a Verify waiting for input
		<-drainDone
	}()
	// wbgong's Recorder.Verify arms a fixed 5s per-item timeout that
	// SetEmptyWaitTime does NOT extend; one quiet spell (a slow corpus
	// file on emulated CI) would FailNow the drainer and silently mark
	// the test failed. A heartbeat guarantees an item inside every window.
	hbStop := make(chan struct{})
	hbDone := make(chan struct{})
	go func() {
		defer close(hbDone)
		ticker := time.NewTicker(2 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-hbStop:
				return
			case <-ticker.C:
				rec.Rec("corpus heartbeat")
			}
		}
	}()
	defer func() {
		close(hbStop)
		<-hbDone
	}()

	for i, file := range files {
		key, err := corpusKey(file)
		if err != nil {
			t.Fatalf("hash %s: %v", file, err)
		}
		if _, dup := res.names[key]; dup {
			// identical content loads identically; record it once (key only:
			// the corpus file names stay out of the routine test output)
			t.Logf("corpus: duplicate content %s, counted once", key)
			continue
		}
		start := time.Now()
		loadErr := loadInFreshEngine(t, rec, file)
		verdict, bucket, detail := verdictOf(loadErr)
		// progress by key, not name: this line ends up in CI logs
		fmt.Fprintf(os.Stderr, "CORPUS %d/%d %.1fs %s: %s\n",
			i+1, len(files), time.Since(start).Seconds(), key, verdict)
		res.verdicts[key] = verdict
		res.names[key] = filepath.Base(file)
		res.details[key] = detail
		switch bucket {
		case "ok":
			res.ok++
		case "module-missing":
			res.moduleMissing++
		default:
			res.errs++
		}
	}
	return res
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
	driverClient := broker.MakeClient("driver") // stopped by driver.Close()
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
	// the log client is ours to stop (each fake client owns a broker
	// dispatcher goroutine; unstopped, 663 files leak 663 of them). Deferred
	// here so it runs after engine.Close (registered below).
	defer logClient.Stop()
	options := NewESEngineOptions()
	options.SetModulesDirs([]string{testModulesDir()})
	options.JsExecutionLimit = 2 * time.Second
	// corpus scripts are untrusted: never run their shell commands on the
	// host (top-level runShellCommand/spawn, Notify.* under the hood)...
	options.SetSpawnFunc(func(name string, args []string, _, _ bool, _ *string) (*CommandResult, error) {
		return nil, fmt.Errorf("corpus sandbox: not running %q", name)
	})
	// ...and never read host files for them: readConfig would otherwise open
	// any path with the test process's privileges. The stub fails exactly
	// like a missing file, so verdicts match a host without the configs.
	options.SetReadConfigFunc(func(path string) ([]byte, error) {
		return nil, fmt.Errorf("corpus sandbox: not reading %q", path)
	})
	// wild scripts use PersistentStorage; without a DB it throws rc -100
	options.SetPersistentDBFile(filepath.Join(t.TempDir(), "corpus-pdb.db"))

	engine, err := NewESEngine(driver, logClient, options)
	if err != nil {
		t.Fatalf("engine: %v", err)
	}
	engine.Start()
	// Close stops the engine, closes the DB and frees the QuickJS heap: each
	// corpus file gets a fresh engine, and 663 undestroyed heaps peak at
	// hundreds of MB of test RSS - too much for an emulated ARM runner
	defer engine.Close()

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
