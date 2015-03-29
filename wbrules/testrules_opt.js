var asSoonAsCount = 0, runRuleWithoutCells = false;
defineRule("condCount", {
  asSoonAs: function () {
    ++asSoonAsCount;
    log("condCount: asSoonAs()");
    return dev.somedev.countIt == "42";
  },
  then: function () {
    log("condCount fired, count=" + asSoonAsCount);
    runRuleWithoutCells = true;
  }
});

defineRule("ruleWithoutCells", {
  asSoonAs: function () {
    return runRuleWithoutCells;
  },
  then: function () {
    log("ruleWithoutCells fired");
  }
});
