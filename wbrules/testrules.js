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
    return dev.somedev.foo == "s";
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

// setTimeout / setInterval based timers

var timer = null;

defineRule("startTimer1", {
  asSoonAs: function () {
    return dev.somedev.foo == "+t";
  },
  then: function () {
    if (timer)
      clearTimeout(timer);
    timer = setTimeout(function () {
      timer = null;
      log("timer fired");
    }, 500);
  }
});

defineRule("startTicker1", {
  asSoonAs: function () {
    return dev.somedev.foo == "+p";
  },
  then: function () {
    if (timer)
      clearTimeout(timer);
    timer = setInterval(function () {
      log("timer fired");
    }, 500);
  }
});

defineRule("stopTimer1", {
  asSoonAs: function () {
    return dev.somedev.foo == "+s";
  },
  then: function () {
    if (timer) {
      clearTimeout(timer);
      timer = null;
    }
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
  onCellChange: "somedev/foobarbaz",
  then: function (devName, cellName, newValue) {
    var v = dev[devName][cellName];
    if (v !== newValue)
      throw new Error("bad newValue! " + newValue);
    log("cellChange1: " + devName + "/" + cellName + "=" + v + " (" + typeof(v) + ")");
  }
});

defineRule("cellChange2", {
  onCellChange: ["somedev/foobarbaz", "somedev/tempx"],
  then: function (devName, cellName, newValue) {
    var v = dev[devName][cellName];
    if (v !== newValue)
      throw new Error("bad newValue! " + newValue);
    log("cellChange2: " + devName + "/" + cellName + "=" + v + " (" + typeof(v) + ")");
  }
});


defineRule("runCommand", {
  onCellChange: "somedev/cmd",
  then: function (devName, cellName, cmd) {
    log("cmd: " + cmd);
    if (dev.somedev.cmdNoCallback) {
      runShellCommand(cmd);
      log("(no callback)"); // make sure the rule didn't fail before here
    } else {
      runShellCommand(cmd, function (exitCode) {
        log("exit(" + exitCode + "): " + cmd);
      });
    }
  }
});

function displayOutput(prefix, out) {
  out.split("\n").forEach(function (line) {
    if (line)
      log(prefix + line);
  });
}

defineRule("runCommandWithOutput", {
  onCellChange: "somedev/cmdWithOutput",
  then: function (devName, cellName, cmd) {
    var options = {
      captureOutput: true,
      captureErrorOutput: true,
      exitCallback: function (exitCode, capturedOutput, capturedErrorOutput) {
        log("exit(" + exitCode + "): " + cmd);
        displayOutput("output: ", capturedOutput);
        if (exitCode != 0)
          displayOutput("error: ", capturedErrorOutput);
      }
    };
    var p = cmd.indexOf("!");
    if (p >= 0) {
      options.input = cmd.substring(0, p);
      cmd = cmd.substring(p + 1);
    }
    log("cmdWithOutput: " + cmd);
    runShellCommand(cmd, options);
  }
});
