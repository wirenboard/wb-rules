function cellSpec(devName, cellName) {
  return devName === undefined ? "(no cell)" : "{}/{}".format(devName, cellName);
}

defineVirtualDevice("testDevice", {
  title: "Test Device",
  cells: {
    switchControl: {
      type: "switch",
      value: false
    },
    rangeControl: {
      type: "range",
      max: 100,
      value: 50
    },
    textControl: {
      type: "text",
      value: "some text"
    }
  }
});

defineRule("onChangetextControl", {
  whenChanged: "testDevice/textControl",
  then: function (newValue, devName, cellName) {
    log("got textControl, changed: {} -> {}", cellSpec(devName, cellName),
       newValue === undefined ? "(none)" : newValue);
    switch(newValue) {
    case "setError":
      dev["testDevice/textControl#error"] = "error text";
      break;
    case "unsetError":
        dev["testDevice/textControl#error"] = "";
        break;
    }
  }
});

defineRule("onChangeSwitchControl", {
  whenChanged: "testDevice/switchControl#error",
  then: function (newValue, devName, cellName) {
    log("got switchControl, changed: {} -> {}", cellSpec(devName, cellName),
       newValue === undefined ? "(none)" : newValue);
  }
});

defineRule("onChangeSw", {
  whenChanged: "somedev/sw#error",
  then: function (newValue, devName, cellName) {
    log("got sw, changed: {} -> {}", cellSpec(devName, cellName),
       newValue === undefined ? "(none)" : newValue);
    if(newValue !== "") {
      dev["testDevice/switchControl"] = true;
    } else {
      dev["testDevice/switchControl"] = false;
    }
  }
});

defineRule("asSoonAsExtError", {
  asSoonAs: function () {
    return (dev["somedev/sw#error"]);
  },
  then: function (newValue, devName, cellName) {
    log(devName + "/" + cellName + " = " + newValue);
  }
});
