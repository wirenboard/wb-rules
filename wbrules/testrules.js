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
    return dev.stabSettings.enabled && dev.somedev.temp < dev.stabSettings.lowThreshold;
  },
  then: function () {
    log("heaterOn fired");
    dev.somedev.sw = true;
  }
});

defineRule("heaterOff", {
  when: function () {
    return dev.somedev.sw &&
      (!dev.stabSettings.enabled || dev.somedev.temp >= dev.stabSettings.highThreshold);
  },
  then: function () {
    log("heaterOff fired");
    dev.somedev.sw = false;
  }
});

defineRule("initiallyIncompleteLevelTriggered", {
  when: function () {
    return dev.somedev.foobar != "0";
  },
  then: function () {
    log("initiallyIncompleteLevelTriggered fired");
  }
});

defineRule("startTimer", {
  asSoonAs: function () {
    return dev.somedev.foo == "t";
  },
  then: function () {
    startTimer("sometimer", 500);
  }
});

defineRule("startTicker", {
  asSoonAs: function () {
    return dev.somedev.foo == "p";
  },
  then: function () {
    startTicker("sometimer", 500);
  }
});

defineRule("stopTimer", {
  asSoonAs: function () {
    return dev.somedev.foo != "p" && dev.somedev.foo != "t";
  },
  then: function () {
    timers.sometimer.stop();
  }
});

defineRule("timer", {
  when: function () {
    return timers.sometimer.firing;
  },
  then: function () {
    log("timer fired");
  }
});

defineRule("sendmqtt", {
  asSoonAs: function () {
    return dev.somedev.sendit;
  },
  then: function () {
    publish("/abc/def/ghi", "0", 0);
    publish("/misc/whatever", "abcdef", 1);
    publish("/zzz/foo", "qqq", 2);
    publish("/zzz/foo/qwerty", "42", 2, true);
  }
});
