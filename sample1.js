defineVirtualDevice("relayClicker", {
  title: "Relay Clicker",
  cells: {
    enabled: {
      type: "switch",
      value: false
    }
  }
});

defineRule("startClicking", {
  asSoonAs: function () {
    return dev.relayClicker.enabled && (dev.uchm121rx["Input 0"] == "0");
  },
  then: function () {
    startTicker("clickTimer", 1000);
  }
});

defineRule("stopClicking", {
  asSoonAs: function () {
    return !dev.relayClicker.enabled || dev.uchm121rx["Input 0"] != "0";
  },
  then: function () {
    timers.clickTimer.stop();
  }
});

defineRule("doClick", {
  when: function () {
    return timers.clickTimer.firing;
  },
  then: function () {
    dev.uchm121rx["Relay 0"] = !dev.uchm121rx["Relay 0"];
  }
});

defineRule("echo", {
  whenChanged: "wb-w1/00042d40ffff",
  then: function (newValue, devName, cellName) {
    runShellCommand("echo {}/{} = {}".format(devName, cellName, newValue), {
      captureOutput: true,
      exitCallback: function (exitCode, capturedOutput) {
        log("cmd output: " + capturedOutput);
      }
    });
  }
});
