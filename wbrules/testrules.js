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
      dev.somedev.temp.v < dev.stabSettings.lowThreshold.v &&
      !dev.somedev.sw.b; // FIXME: dev.somedev.sw check can be replaced with edge-triggered rule
  },
  then: function () {
    log("heaterOn fired");
    dev.somedev.sw.b = true;
  }
});

defineRule("heaterOff", {
  when: function () {
    return dev.somedev.sw.b && // FIXME: dev.somedev.sw check can be replaced with edge-triggered rule
      (!dev.stabSettings.enabled.b ||
       dev.somedev.temp.v >= dev.stabSettings.highThreshold.v);
  },
  then: function () {
    log("heaterOff fired");
    dev.somedev.sw.b = false;
  }
});

// TBD: edge-triggered rules
// TBD: dev.somedev.whatever.s (string value)
// TBD: perhaps should abolish .v / .b / .s, use method valueOf()
// and never apply rules before .../meta/type is received for all
// the cells (during expression evaluation, set something.gotIncompleteCells = true
// when cells without type are encountered; type should be "incomplete" not
// "text" until the type is actually received)
