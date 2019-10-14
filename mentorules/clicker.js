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
    return dev.relayClicker.enabled;
  },
  then: function () {
    startTicker("K1", getRandomInt(5000)+5000);
    startTicker("K2", getRandomInt(5000)+5000);
    startTicker("K3", getRandomInt(5000)+5000);
  }
});

defineRule("stopClicking", {
  asSoonAs: function () {
    return !dev.relayClicker.enabled;
  },
  then: function () {
    timers["K1"].stop();
    timers["K2"].stop();
    timers["K3"].stop();
  }
});

function getRandomInt(max) {
  return Math.floor(Math.random() * Math.floor(max));
}

function defTimer(port) {
  defineRule("doClick"+port, {
    when: function () {
      return timers[port].firing;
    },
    then: function () {
      dev["wb-mr3_48"][port] = !dev["wb-mr3_48"][port];
    }
  });
};

defTimer("K1");
defTimer("K2");
defTimer("K3");
