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

defineAlias("stabEnabled", "stabSettings/enabled");
defineAlias("roomTemp", "Weather/Temp 1");
defineAlias("heaterRelayOn", "Relays/Relay 1");

defineRule("heaterOn", {
  asSoonAs: function () {
    return stabEnabled && roomTemp < dev.stabSettings.lowThreshold;
  },
  then: function () {
    log("heaterOn fired");
    heaterRelayOn = true;
    startTicker("heating", 3000);
  }
});

defineRule("heaterOff", {
  when: function () {
    return heaterRelayOn && (!stabEnabled || roomTemp >= dev.stabSettings.highThreshold);
  },
  then: function () {
    log("heaterOff fired");
    heaterRelayOn = false;
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

defineRule("tempChange", {
  whenChanged: ["Weather/Temp 1", "Weather/Temp 2"],
  then: function (devName, cellName, newValue) {
    log(devName + "/" + cellName + " = " + newValue);
  }
});

defineRule("pressureChange", {
  whenChanged: "Weather/Pressure",
  then: function (devName, cellName, newValue) {
    log("pressure = " + newValue);
    runShellCommand(
      "echo -n 'sampleerr' 1>&2; echo -n " + devName + "/" + cellName + "=" + newValue, {
        captureOutput: true,
        captureErrorOutput: true,
        exitCallback: function (exitCode, capturedOutput, capturedErrorOutput) {
          log("cmd exit code: " + exitCode);
          log("cmd output: " + capturedOutput);
          log("cmd error ouput: " + capturedErrorOutput);
        }
      });
  }
});
