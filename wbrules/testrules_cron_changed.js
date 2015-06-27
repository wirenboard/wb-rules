defineRule("crontest_hourly", {
  when: cron("@hourly"),
  then: function () {
    log("@hourly rule fired (new)");
  }
});

defineRule("crontest_daily", {
  when: cron("@daily"),
  then: function () {
    log("@daily rule fired (new)");
  }
});
