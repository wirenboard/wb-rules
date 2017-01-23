defineVirtualDevice("vdev", {
  title: "VDev",
  cells: {
    someCell: {
      type: "switch",
      value: false
    }
  }
});

var v = 84;

defineRule("isolated_rule", {
  whenChanged: ["vdev/someCell"],
  then: function () {
    log("isolated_rule (testrules_isolation_1.js) " + v);
  }
});
