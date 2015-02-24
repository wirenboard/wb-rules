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
    return !!dev.stabSettings.enabled.v &&
      dev.somedev.temp.v < dev.stabSettings.lowThreshold.v &&
      !dev.somedev.sw.v; // FIXME: dev.somedev.sw check can be replaced with edge-triggered rule
  },
  then: function () {
    log("heaterOn fired");
    dev.somedev.sw.v = true;
  }
});

defineRule("heaterOff", {
  when: function () {
    return !!dev.somedev.sw.v && // FIXME: dev.somedev.sw check can be replaced with edge-triggered rule
      (!dev.stabSettings.enabled.v ||
       dev.somedev.temp.v >= dev.stabSettings.highThreshold.v);
  },
  then: function () {
    log("heaterOff fired");
    dev.somedev.sw.v = false;
  }
});

defineRule("initiallyIncomplete", {
  when: function () {
    return dev.somedev.foobar.v;
  },
  then: function () {
    log("initiallyIncomplete fired");
  }
});
