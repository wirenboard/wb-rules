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
    return !!dev.stabSettings.enabled &&
      dev.Weather["Temp 1"] < dev.stabSettings.lowThreshold &&
      !dev.Relays["Relay 1"]; // FIXME: dev.Relays["Relay 1"] check can be replaced with edge-triggered rule
  },
  then: function () {
    log("heaterOn fired");
    dev.Relays["Relay 1"] = true;
  }
});

defineRule("heaterOff", {
  when: function () {
    return !!dev.Relays["Relay 1"] && // FIXME: dev.Relays["Relay 1"] check can be replaced with edge-triggered rule
      (!dev.stabSettings.enabled ||
       dev.Weather["Temp 1"] >= dev.stabSettings.highThreshold);
  },
  then: function () {
    log("heaterOff fired");
    dev.Relays["Relay 1"] = false;
  }
});
