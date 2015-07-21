var devCells = {
  someCell: {
    type: "switch",
    value: false
  }
};

defineAlias("smc", "vdev/someCell");

defineVirtualDevice("vdev", {
  title: "VDev",
  cells: devCells
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
      log("{}: {} (s={})",
          name,
          cellSpec(devName, cellName),
          dev.vdev.someCell);
    }
  });
}

defDetectRun("detectRun");
defChangeRule("rule1", "vdev/someCell");

testrules_reload_2_n++;
