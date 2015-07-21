var devCells = {
  someCell: {
    type: "switch",
    value: false
  }
};

// removed after reload
devCells.anotherCell = {
  type: "range",
  max: 42,
  value: 10
};

defineAlias("smc", "vdev/someCell");

defineVirtualDevice("vdev", {
  title: "VDev",
  cells: devCells
});

// removed after reload
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
          // doesn't log anotherCell value in the altered version
          ", a={}".format(dev.vdev.anotherCell));
    }
  });
}

defDetectRun("detectRun");
defDetectRun("detectRun1"); // removed in the altered version
defChangeRule("rule1", "vdev/someCell");
defChangeRule("rule2", "vdev/someCell"); // removed in the altered version
defChangeRule("rule3", "vdev/anotherCell"); // removed in the altered version

testrules_reload_2_n = 0;
