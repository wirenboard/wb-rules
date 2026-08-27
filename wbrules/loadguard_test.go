package wbrules

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

// writeRuleFile creates a stand-in rule file and returns its path.
func writeRuleFile(t *testing.T, dir string) string {
	t.Helper()
	p := filepath.Join(dir, "rule.js")
	if err := os.WriteFile(p, []byte("// rule\n"), 0644); err != nil {
		t.Fatal(err)
	}
	return p
}

// crashCycle simulates one run that died mid-load: a fresh guard (which runs
// crash detection for any surviving marker) marks the file as loading and
// then "crashes" - i.e. never calls endLoad, so the marker survives.
func crashCycle(dir string, threshold int, path string) *loadGuard {
	g := newLoadGuard(dir, threshold)
	g.beginLoad(path)
	return g
}

func TestLoadGuardQuarantinesAfterRepeatedLoadCrashes(t *testing.T) {
	dir := t.TempDir()
	path := writeRuleFile(t, dir)
	const threshold = 3

	// three consecutive runs die while loading the file
	for i := 0; i < threshold; i++ {
		g := crashCycle(dir, threshold, path)
		if g.quarantined(path) {
			t.Fatalf("must not be quarantined before %d crashes (crash %d)", threshold, i+1)
		}
	}

	// the next startup detects the third surviving marker and quarantines
	g := newLoadGuard(dir, threshold)
	if !g.quarantined(path) {
		t.Fatalf("file must be quarantined after %d load-crashes", threshold)
	}
}

func TestLoadGuardCleanLoadResetsCrashCount(t *testing.T) {
	dir := t.TempDir()
	path := writeRuleFile(t, dir)
	const threshold = 3

	// crash twice
	crashCycle(dir, threshold, path)
	crashCycle(dir, threshold, path)

	// then a run that loads cleanly (endLoad after beginLoad) - resets the count
	g := newLoadGuard(dir, threshold)
	g.beginLoad(path)
	g.endLoad(path)

	// two more crashes must not be enough to quarantine (counter was reset)
	crashCycle(dir, threshold, path)
	crashCycle(dir, threshold, path)
	g = newLoadGuard(dir, threshold)
	if g.quarantined(path) {
		t.Fatal("a clean load must reset the consecutive-crash counter")
	}
}

func TestLoadGuardReleasesOnEdit(t *testing.T) {
	dir := t.TempDir()
	path := writeRuleFile(t, dir)
	const threshold = 2

	crashCycle(dir, threshold, path)
	crashCycle(dir, threshold, path)
	g := newLoadGuard(dir, threshold)
	if !g.quarantined(path) {
		t.Fatal("precondition: file should be quarantined")
	}

	// editing the file (mtime changes) is a fix attempt: release the quarantine
	future := time.Now().Add(2 * time.Second)
	if err := os.Chtimes(path, future, future); err != nil {
		t.Fatal(err)
	}
	if g.quarantined(path) {
		t.Fatal("editing the file must release the quarantine")
	}
	// and it stays released on the next startup
	if newLoadGuard(dir, threshold).quarantined(path) {
		t.Fatal("released quarantine must persist")
	}
}

func TestLoadGuardNilIsDisabledNoop(t *testing.T) {
	var g *loadGuard = newLoadGuard("", 3) // empty dir => disabled (nil)
	if g != nil {
		t.Fatal("an empty dir must yield a disabled (nil) guard")
	}
	// all methods must be safe no-ops on the nil guard
	g.beginLoad("/whatever")
	g.endLoad("/whatever")
	if g.quarantined("/whatever") {
		t.Fatal("a disabled guard never quarantines")
	}
}

func TestLoadGuardCrashAfterQuarantineDoesNotUnderflow(t *testing.T) {
	// once quarantined the file is skipped, so no further begin/endLoad runs;
	// a leftover marker from before quarantine must not double-count or panic
	dir := t.TempDir()
	path := writeRuleFile(t, dir)
	const threshold = 2
	crashCycle(dir, threshold, path)
	crashCycle(dir, threshold, path)
	g := newLoadGuard(dir, threshold)
	if !g.quarantined(path) {
		t.Fatal("precondition: quarantined")
	}
	// a fresh startup with no marker keeps it quarantined, stably
	if !newLoadGuard(dir, threshold).quarantined(path) {
		t.Fatal("quarantine must persist across a clean startup")
	}
}
