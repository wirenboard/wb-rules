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
  asSoonAs: function () {
    return dev.stabSettings.enabled && dev.Weather["Temp 1"] < dev.stabSettings.lowThreshold;
  },
  then: function () {
    log("heaterOn fired");
    dev.Relays["Relay 1"] = true;
  }
});

defineRule("heaterOff", {
  asSoonAs: function () {
    return !dev.stabSettings.enabled || dev.Weather["Temp 1"] >= dev.stabSettings.highThreshold;
  },
  then: function () {
    log("heaterOff fired");
    dev.Relays["Relay 1"] = false;
  }
});
