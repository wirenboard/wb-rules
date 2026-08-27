// The registry for the on-controller check is snapshotted after the file
// has run, so the virtual device this very file defines is in it: a
// wrong-typed write to own control is flagged on first load and on every
// reload (when the device is removed and re-created).
defineVirtualDevice("ownvdev", {
  title: "Own vdev",
  cells: { level: { type: "value", value: 0 }, on: { type: "switch", value: false } },
});
function __ownCheck() {
  dev["ownvdev/level"] = "not a number"; // line 10: own numeric control <- string, error
  dev["ownvdev/on"] = 5; // line 11: own switch <- number, error
  dev["ownvdev/level"] = 42; // fine
}
