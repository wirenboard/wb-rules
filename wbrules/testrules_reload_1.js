defineVirtualDevice("vdev0", {
  title: "VDev0",
  cells: {
    someCell: {
      type: "switch",
      value: false
    }
  }
});

defineRule("detRun", {
  when: function () { return true; },
  then: function () {
    log("detRun");
  }
});
