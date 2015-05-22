// This script provides a bit different definitions
// based on the value of global 'alteredMode' variable,
// thus simulating editing & reloading of a script file.

var altered = global.alteredMode; // ignore further changes while the script runs

debug("altered=" + altered);

var devCells = {
  someCell: {
    type: "switch",
    value: false
  }
};

if (!altered) {
  devCells.anotherCell = {
    type: "range",
    max: 42,
    value: 10
  };
}

defineAlias("smc", "vdev/someCell");
debug("devCells=" + JSON.stringify(devCells));

defineVirtualDevice("vdev", {
  title: "VDev",
  cells: devCells
});

if (!altered)
  defineVirtualDevice("vdev1", {
    title: "VDev1",
    cells: {
      qqq: {
        type: "switch",
        value: false
      }
    }
  });

function cellSpec(devName, cellName) {
  return devName === undefined ? "(no cell)" : "{}/{}".format(devName, cellName);
}

function defChangeRule(name, cell) {
  defineRule(name, {
    whenChanged: cell,
    then: function (newValue, devName, cellName) {
      log("{}: {}={}", name, cellSpec(devName, cellName), newValue);
    }
  });
}

function defDetectRun(name) {
  defineRule(name, {
    when: function () { return true; },
    then: function (newValue, devName, cellName) {
      if (smc !== dev.vdev.someCell)
        throw new Error("cell alias value mismatch!");
      log("{}: {} (s={}{})",
          name,
          cellSpec(devName, cellName),
          dev.vdev.someCell,
          altered ? "" : ", a={}".format(dev.vdev.anotherCell));
    }
  });
}

defDetectRun("detectRun");
if (!altered)
  defDetectRun("detectRun1");
defChangeRule("rule1", "vdev/someCell");
if (!altered) {
  defChangeRule("rule2", "vdev/someCell");
  defChangeRule("rule3", "vdev/anotherCell");
}
