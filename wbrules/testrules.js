// -*- mode: js2-mode -*-

// extra test for format()
(function () {
  var formatted = "{{}abc {{} {} {{}".format(1);
  if (formatted != "{}abc {} 1 {}")
    throw new Error("oops! format error: " + formatted);
})();

defineAlias("stabEnabled", "stabSettings/enabled");
defineAlias("temp", "somedev/temp");
defineAlias("sw", "somedev/sw");

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
    return stabEnabled && temp < dev.stabSettings.lowThreshold;
  },
  then: function () {
    log("heaterOn fired");
    sw = true;
  }
});

defineRule("heaterOff", {
  when: function () {
    return sw && (!stabEnabled || temp >= dev.stabSettings.highThreshold);
  },
  then: function () {
    log("heaterOff fired");
    sw = false;
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

defineRule("cellChange1", {
  whenChanged: "somedev/foobarbaz",
  then: function (newValue, devName, cellName) {
    if (arguments.length != 3)
      throw new Error("invalid arguments for then");
    var v = dev[devName][cellName];
    if (v !== newValue)
      throw new Error("bad newValue! " + newValue);
    log("cellChange1: {}/{}={} ({})", devName, cellName, v, typeof(v));
  }
});

defineRule("cellChange2", {
  whenChanged: ["somedev/foobarbaz", "somedev/tempx"],
  then: function (newValue, devName, cellName) {
    if (arguments.length != 3)
      throw new Error("invalid arguments for then");
    var v = dev[devName][cellName];
    if (v !== newValue)
      throw new Error("bad newValue! " + newValue);
    // just make sure that format works here, too
    log("cellChange2: {}/{}={} ({})".format(devName, cellName, v, typeof(v)));
  }
});

defineRule("funcValueChange", {
  whenChanged: function () {
    return dev.somedev.cellforfunc > 3;
  },
  then: function (newValue, devName, cellName) {
    if (arguments.length != 1)
      throw new Error("invalid arguments for then");
    log("funcValueChange: {} ({})", newValue, typeof(newValue));
  }
});

defineRule("funcValueChange2", {
  whenChanged: [
    "somedev/cellforfunc1",
    function () {
      return dev.somedev.cellforfunc2 > 3;
    }
  ],
  then: function (newValue, devName, cellName) {
    var cellSpec = devName === undefined ? "(no cell)" :
          "{}/{}:".format(devName, cellName);
    log("funcValueChange2: {} {} ({})", cellSpec, newValue, typeof(newValue));
  }
});

// TBD: pass changed cell info for plain rules
// TBD: fix 'when' optimization
// TBD: document rule optimization in README.md
