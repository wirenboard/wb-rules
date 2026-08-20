package wbrules

import (
	"os"
	"testing"
)

// A genuinely pending top-level await resumes after loadScript has popped
// the file's ambient cleanup scope. Resources created by the continuation
// (defineVirtualDevice here) must still land in the owning file's scope, or
// they survive the file's reload and removal.
func TestPendingTopLevelAwaitResourceScope(t *testing.T) {
	h := newChurnHarness(t, nil)
	path := h.mustLoad("tla.js", `
defineVirtualDevice("tlakick", {cells: {go: {type: "value", value: 0}}});
await changed("tlakick/go");
defineVirtualDevice("tladev", {cells: {c: {type: "value", value: 1}}});
`)
	// the await is parked: the post-await device must not exist yet
	if got := h.evalStr(`'' + (getDevice("tladev") !== undefined)`); got != "false" {
		t.Fatal("the device defined after the top-level await exists before the await resumed")
	}

	// fire the awaited control change; the continuation defines tladev
	h.evalStr(`dev["tlakick/go"] = 1; 'ok'`)
	h.waitEval(`'' + (getDevice("tladev") !== undefined)`, "true")

	// removing the file must remove BOTH devices - the one created by the
	// resumed continuation included
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if err := h.engine.LiveRemoveFile(path); err != nil {
		t.Fatalf("remove: %v", err)
	}
	h.sync()
	h.waitEval(`'' + (getDevice("tlakick") !== undefined)`, "false")
	h.waitEval(`'' + (getDevice("tladev") !== undefined)`, "false")
}

// The same continuation across a reload: the reloaded file's fresh realm
// parks a new await, and the OLD run's post-await device (created before the
// reload) must have been cleaned up by the reload.
func TestPendingTopLevelAwaitReloadCleansResources(t *testing.T) {
	h := newChurnHarness(t, nil)
	h.mustLoad("tlar.js", `
defineVirtualDevice("tlrkick", {cells: {go: {type: "value", value: 0}}});
await changed("tlrkick/go");
defineVirtualDevice("tlrdev", {cells: {c: {type: "value", value: 1}}});
`)
	h.evalStr(`dev["tlrkick/go"] = 1; 'ok'`)
	h.waitEval(`'' + (getDevice("tlrdev") !== undefined)`, "true")

	// reload with content that no longer defines tlrdev: it must disappear
	h.mustLoad("tlar.js", `
defineVirtualDevice("tlrkick", {cells: {go: {type: "value", value: 0}}});
`)
	h.waitEval(`'' + (getDevice("tlrdev") !== undefined)`, "false")
	h.waitEval(`'' + (getDevice("tlrkick") !== undefined)`, "true")
}
