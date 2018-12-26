var v = 42;

defineRule("isolated_rule", {
  whenChanged: ["vdev/someCell"],
  then: function () {
    log("isolated_rule (testrules_isolation_2.js) " + v);
  }
});
