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
      value: "some text",
      readonly: false
    },
    startControl: {
      type: "switch",
      value: false
    }
  }
});

defineRule("onChangeStartControl", {
  whenChanged: "testDevice/startControl",
  then: function (newValue, devName, cellName) {
    log("got startControl, changed: {} -> {}", cellSpec(devName, cellName),
       newValue === undefined ? "(none)" : newValue);
    if (newValue) {
      dev["testDevice/textControl#error"] = "error text";
      dev["testDevice/textControl#description"] = "new description";
      dev["testDevice/textControl#type"] = "range";
      dev["testDevice/textControl#max"] = "255";
      dev["testDevice/textControl#order"] = "4";
      dev["testDevice/textControl#units"] = "meters";
      dev["testDevice/textControl#readonly"] = "1";
    } else {
      dev["testDevice/textControl#error"] = "";
      dev["testDevice/textControl#description"] = "old description";
      dev["testDevice/textControl#type"] = "text";
      dev["testDevice/textControl#max"] = "0";
      dev["testDevice/textControl#order"] = "5";
      dev["testDevice/textControl#units"] = "chars";
      dev["testDevice/textControl#readonly"] = "0";
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
