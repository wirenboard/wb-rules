// -*- mode: js2-mode -*-

defineVirtualDevice("stabSettings", {
  title: "Stabilization Settings",
  cells: {
    enabled: {
      type: "switch",
      value: false
    },
    lowThreshold: {
      type: "temperature", // FIXME: type should range
      value: 20
    },
    highThreshold: {
      type: "temperature", // FIXME: type should range
      value: 22
    }
  }
});

defineRule("heaterOn", {
  when: function () {
    return dev.stabSettings.enabled.b &&
      dev.somedev.temp.v < dev.stabSettings.lowThreshold.v;
  },
  then: function () {
    log("heaterOn fired");
    dev.somedev.sw.b = true;
  }
});

defineRule("heaterOff", {
  when: function () {
    return !dev.stabSettings.enabled.b ||
      dev.somedev.temp.v >= dev.stabSettings.highThreshold.v;
  },
  then: function () {
    dev.somedev.sw.b = false;
    log("heaterOff rule fired");
  }
});

// TBD: edge-triggered rules
// TBD: dev.somedev.whatever.s (string value)
// TBD: perhaps should abolish .v / .b / .s, use method valueOf()
// and never apply rules before .../meta/type is received for all
// the cells (during expression evaluation, set something.gotIncompleteCells = true
// when cells without type are encountered; type should be "incomplete" not
// "text" until the type is actually received)
