// Top-level await: the awaited value is available to code - and rules -
// defined after the await. If the file stopped at the await, tla_rule would
// never be defined and tla/out would stay 0.
defineVirtualDevice("tla", {
  title: "top-level await probe",
  cells: {
    trigger: { type: "switch", value: false },
    out: { type: "value", value: 0, readonly: true },
  },
});
var base = await Promise.resolve(100);
defineRule("tla_rule", {
  whenChanged: "tla/trigger",
  then: function () {
    dev["tla/out"] = base + 1;
  },
});
