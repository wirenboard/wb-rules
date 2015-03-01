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
    startTicker("heating", 3000);
  }
});

defineRule("heaterOff", {
  when: function () {
    return dev.Relays["Relay 1"] &&
      (!dev.stabSettings.enabled || dev.Weather["Temp 1"] >= dev.stabSettings.highThreshold);
  },
  then: function () {
    log("heaterOff fired");
    dev.Relays["Relay 1"] = false;
    timers.heating.stop();
    startTimer("heatingOff", 1000);
  }
});

defineRule("ht", {
  when: function () {
    return timers.heating.firing;
  },
  then: function () {
    log("heating timer fired");
  }
});

defineRule("htoff", {
  when: function () {
    return timers.heatingOff.firing;
  },
  then: function () {
    log("heating-off timer fired");
  }
});
