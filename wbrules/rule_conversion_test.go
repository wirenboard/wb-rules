package wbrules

import (
	"strings"
	"testing"
)

// A getter or Proxy trap that throws while a script-supplied object is being
// converted for a builtin must propagate to the script (as Duktape did), not
// silently produce a partial conversion.
func TestThrowingGetterPropagatesFromConversion(t *testing.T) {
	h := newChurnHarness(t, nil)

	// top-level defineVirtualDevice: the getter's throw becomes the load error
	path := h.writeFile("getter.js", `
defineVirtualDevice("gdev", {get cells() { throw new TypeError("boom in getter"); }});
`)
	msg := ""
	if err := h.engine.LiveLoadFile(path); err != nil {
		msg = err.Error()
	} else {
		msg = loadedEntryError(t, h.engine, path)
	}
	if !strings.Contains(msg, "boom in getter") {
		t.Fatalf("expected the getter's own error to propagate as the load error, got %q", msg)
	}
	if got := h.evalStr(`'' + (getDevice("gdev") !== undefined)`); got != "false" {
		t.Fatal("a device was created from the partially converted object")
	}

	// a Proxy whose ownKeys trap throws: same propagation, catchable in-script
	got := h.evalStr(`
(function () {
  try {
    defineVirtualDevice("pdev", new Proxy({}, {ownKeys: function () { throw new Error("trap boom"); }}));
    return "no throw";
  } catch (e) { return String(e); }
})()`)
	if !strings.Contains(got, "trap boom") {
		t.Fatalf("Proxy ownKeys throw not propagated: %q", got)
	}

	// nested: a throwing getter inside a cell-method argument
	got = h.evalStr(`
(function () {
  defineVirtualDevice("cdev", {cells: {c: {type: "value", value: 1}}});
  try {
    getDevice("cdev").getControl("c").setValue({get value() { throw new Error("value boom"); }});
    return "no throw";
  } catch (e) { return String(e); }
})()`)
	if !strings.Contains(got, "value boom") {
		t.Fatalf("throwing getter in setValue argument not propagated: %q", got)
	}

	// and a healthy object still converts fine afterwards (no stale error)
	got = h.evalStr(`
(function () {
  defineVirtualDevice("okdev", {cells: {c: {type: "value", value: 7}}});
  return '' + (getDevice("okdev") !== undefined);
})()`)
	if got != "true" {
		t.Fatalf("conversion state leaked into the next call: %q", got)
	}
}
