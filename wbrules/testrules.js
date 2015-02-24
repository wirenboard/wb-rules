// -*- mode: js2-mode -*-

defineVirtualDevice("stabSettings", {
  title: "Stabilization Settings",
  cells: {
    enabled: {
      type: "switch",
      value: false
    },
    lowThreshold: {
      type: "range",
      max: 40,
      value: 20
    },
    highThreshold: {
      type: "range",
      max: 50,
      value: 22
    }
  }
});

defineRule("heaterOn", {
  when: function () {
    return !!dev.stabSettings.enabled &&
      dev.somedev.temp < dev.stabSettings.lowThreshold &&
      !dev.somedev.sw; // FIXME: dev.somedev.sw check can be replaced with edge-triggered rule
  },
  then: function () {
    log("heaterOn fired");
    dev.somedev.sw = true;
  }
});

defineRule("heaterOff", {
  when: function () {
    return !!dev.somedev.sw && // FIXME: dev.somedev.sw check can be replaced with edge-triggered rule
      (!dev.stabSettings.enabled ||
       dev.somedev.temp >= dev.stabSettings.highThreshold);
  },
  then: function () {
    log("heaterOff fired");
    dev.somedev.sw = false;
  }
});

defineRule("initiallyIncomplete", {
  when: function () {
    return dev.somedev.foobar;
  },
  then: function () {
    log("initiallyIncomplete fired");
  }
});
