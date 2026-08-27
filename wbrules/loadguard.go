package wbrules

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"sync"

	"github.com/wirenboard/wbgong"
)

const (
	// After this many consecutive load-crashes a file is quarantined.
	LOAD_CRASH_QUARANTINE_THRESHOLD = 3

	loadGuardStateFile  = "wbrules-loadguard.json"
	loadGuardMarkerFile = "wbrules-loading.marker"
)

// loadGuardEntry records how a single file has behaved across load attempts.
type loadGuardEntry struct {
	Crashes          int   `json:"crashes"`          // consecutive load-crashes detected
	QuarantinedMtime int64 `json:"quarantinedMtime"` // file mtime (unix ns) when quarantined; 0 = not quarantined
}

// loadGuard protects the engine from a rule file that crashes the whole
// process while it is being evaluated. Such a file would otherwise be
// reloaded on every restart and crash-loop the engine forever (taking the
// Editor RPC down with it). The guard writes the path of the file currently
// being evaluated into a marker file; if that marker survives into the next
// run, the file crashed the process. After a few crashes the file is
// quarantined - skipped on load - until its content changes (an edit is
// treated as a fix attempt).
//
// A nil *loadGuard is a valid, disabled guard: all methods are no-ops. This
// keeps the guard optional (tests and setups without a writable state dir
// simply pass an empty dir).
type loadGuard struct {
	statePath  string
	markerPath string
	threshold  int
	mu         sync.Mutex
	state      map[string]*loadGuardEntry
}

// newLoadGuard loads persisted state from dir and, if a marker from a prior
// run survived, blames the file it names for a load-crash. Returns nil (a
// disabled guard) when dir is empty.
func newLoadGuard(dir string, threshold int) *loadGuard {
	if dir == "" {
		return nil
	}
	g := &loadGuard{
		statePath:  filepath.Join(dir, loadGuardStateFile),
		markerPath: filepath.Join(dir, loadGuardMarkerFile),
		threshold:  threshold,
		state:      map[string]*loadGuardEntry{},
	}
	g.load()
	g.detectCrash()
	return g
}

func (g *loadGuard) load() {
	data, err := os.ReadFile(g.statePath)
	if err != nil {
		return
	}
	if err := json.Unmarshal(data, &g.state); err != nil || g.state == nil {
		g.state = map[string]*loadGuardEntry{}
	}
}

func (g *loadGuard) save() {
	data, err := json.Marshal(g.state)
	if err != nil {
		return
	}
	// write-then-rename so a crash mid-write can't corrupt the state file
	tmp := g.statePath + ".tmp"
	if os.WriteFile(tmp, data, 0640) == nil {
		os.Rename(tmp, g.statePath)
	}
}

func (g *loadGuard) entry(path string) *loadGuardEntry {
	e := g.state[path]
	if e == nil {
		e = &loadGuardEntry{}
		g.state[path] = e
	}
	return e
}

func fileMtimeNs(path string) int64 {
	if fi, err := os.Stat(path); err == nil {
		return fi.ModTime().UnixNano()
	}
	return 0
}

// detectCrash runs once at startup. If a marker survived from a previous run,
// the file it names died mid-load: count the crash, and quarantine the file
// once it has crashed too many times in a row.
func (g *loadGuard) detectCrash() {
	data, err := os.ReadFile(g.markerPath)
	os.Remove(g.markerPath)
	if err != nil {
		return
	}
	path := string(bytes.TrimSpace(data))
	if path == "" {
		return
	}

	g.mu.Lock()
	defer g.mu.Unlock()
	e := g.entry(path)
	e.Crashes++
	wbgong.Error.Printf("[loadguard] %s crashed the engine while loading (%d in a row)", path, e.Crashes)
	if e.Crashes >= g.threshold && e.QuarantinedMtime == 0 {
		e.QuarantinedMtime = fileMtimeNs(path)
		wbgong.Error.Printf("[loadguard] quarantining %s after %d load-crashes; it will be skipped until its content changes", path, e.Crashes)
	}
	g.save()
}

// quarantined reports whether path must be skipped. A file edited since it was
// quarantined (mtime changed) is released and gets a fresh start.
func (g *loadGuard) quarantined(path string) bool {
	if g == nil {
		return false
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	e := g.state[path]
	if e == nil || e.QuarantinedMtime == 0 {
		return false
	}
	if fileMtimeNs(path) != e.QuarantinedMtime {
		delete(g.state, path)
		g.save()
		return false
	}
	return true
}

// beginLoad marks path as the file being evaluated right now, so a crash
// during its load is attributable on the next run.
func (g *loadGuard) beginLoad(path string) {
	if g == nil {
		return
	}
	f, err := os.OpenFile(g.markerPath, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0640)
	if err != nil {
		return
	}
	f.WriteString(path)
	// No fsync: the marker detects a process crash (the page cache survives
	// it), not a power cut - a boot interrupted by a power cut must not count
	// as a crash of whatever file was loading, and syncing on every load
	// would wear the flash for nothing.
	f.Close()
}

// endLoad clears the marker after a load returns. A load that finished - even
// with a JS error - did not crash the process, so a clean finish resets the
// consecutive-crash counter (transient crashes are forgiven).
func (g *loadGuard) endLoad(path string) {
	if g == nil {
		return
	}
	os.Remove(g.markerPath)
	g.mu.Lock()
	defer g.mu.Unlock()
	if e := g.state[path]; e != nil && e.Crashes > 0 && e.QuarantinedMtime == 0 {
		e.Crashes = 0
		g.save()
	}
}
