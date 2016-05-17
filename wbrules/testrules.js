// -*- mode: js2-mode -*-

if ((function () { return this; })() !== global)
  throw new Error("global object not defined!");

// extra test for format() / xformat()
(function () {
  var formatted = "{{}abc {{} {} {{}".format(1);
  if (formatted != "{}abc {} 1 {}")
    throw new Error("oops! format error: " + formatted);

  var xformatted = "\\{}abc \\{} {} {} -- {{ (31).toString(16) }} \\{}".xformat(1, "zz");
  if (xformatted != "{}abc {} 1 zz -- 1f {}")
    throw new Error("oops! xformat error: " + xformatted);
  xformatted = "{{ (function(){ throw new Error('zzzerr'); })() }}".xformat();
  if (xformatted != "<eval failed:  (function(){ throw new Error('zzzerr'); })() : Error: zzzerr>")
    throw new Error("oops! xformat exception handling error: " + xformatted);
})();

function cellSpec(devName, cellName) {
  return devName === undefined ? "(no cell)" : "{}/{}".format(devName, cellName);
}

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
    if (dev["stabSettings/lowThreshold"] !== dev.stabSettings.lowThreshold)
      throw new Error("/-notation in dev name doesn't work");
    return stabEnabled && temp < dev.stabSettings.lowThreshold;
  },
  then: function (newValue, devName, cellName) {
    log("heaterOn fired, changed: {} -> {}", cellSpec(devName, cellName),
       newValue === undefined ? "(none)" : newValue);
    sw = true;
  }
});

defineRule("heaterOff", {
  when: function () {
    return sw && (!stabEnabled || temp >= dev.stabSettings.highThreshold);
  },
  then: function (newValue, devName, cellName) {
    log("heaterOff fired, changed: {} -> {}", cellSpec(devName, cellName),
        newValue === undefined ? "(none)" : newValue);
    dev["somedev/sw"] = false;
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

defineAlias("tempx", "somedev/tempx");

defineRule("cellChange2", {
  whenChanged: ["somedev/foobarbaz", "tempx" /* an alias */, "somedev/abutton"],
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
    log("funcValueChange2: {}: {} ({})", cellSpec(devName, cellName),
        newValue, typeof(newValue));
  }
});
