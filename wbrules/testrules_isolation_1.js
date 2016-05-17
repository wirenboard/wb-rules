defineVirtualDevice("vdev", {
  title: "VDev",
  cells: {
    someCell: {
      type: "switch",
      value: false
    }
  }
});

defineRule("isolated_rule", {
  whenChanged: ["vdev/someCell"],
  then: function () {
    log("isolated_rule (testrules_isolation_1.js)");
  }
});
