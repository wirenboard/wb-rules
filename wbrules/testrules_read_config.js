defineRule("readSampleConfig", {
  whenChanged: "somedev/readSampleConfig",
  then: function (path) {
    try {
      var conf = readConfig(path);
    } catch (e) {
      log.error("readConfig error!");
      return;
    }
    log("config: {}", JSON.stringify(conf));
  }
});
