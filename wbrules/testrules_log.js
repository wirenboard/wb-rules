global.testLog = function testLog () {
  log("log()");
  debug("debug()");
  log.debug("log.debug({})", 42);
  log.info("log.info({})", 42);
  log.warning("log.warning({})", 42);
  log.error("log.error({})", 42);
}
