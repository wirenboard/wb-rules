// -*- mode: js2-mode -*-

defineRule("runCommand", {
  whenChanged: "somedev/cmd",
  then: function (cmd, devName, cellName) {
    log("cmd: " + cmd);
    if (dev.somedev.cmdNoCallback) {
      runShellCommand(cmd);
      log("(no callback)"); // make sure the rule didn't fail before here
    } else {
      runShellCommand(cmd, function (exitCode) {
        log("exit({}): {}", exitCode, cmd);
      });
    }
  }
});

function displayOutput(prefix, out) {
  out.split("\n").forEach(function (line) {
    if (line)
      log(prefix + line);
  });
}

defineRule("runCommandWithOutput", {
  whenChanged: "somedev/cmdWithOutput",
  then: function (cmd, devName, cellName) {
    var options = {
      captureOutput: true,
      captureErrorOutput: true,
      exitCallback: function (exitCode, capturedOutput, capturedErrorOutput) {
        log("exit({}): {}", exitCode, cmd);
        displayOutput("output: ", capturedOutput);
        if (exitCode != 0)
          displayOutput("error: ", capturedErrorOutput);
      }
    };
    var p = cmd.indexOf("!");
    if (p >= 0) {
      options.input = cmd.substring(0, p);
      cmd = cmd.substring(p + 1);
    }
    log("cmdWithOutput: " + cmd);
    runShellCommand(cmd, options);
  }
});
