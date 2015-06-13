// -*- mode: js2-mode -*-

var timerId = setTimeout(function () {
  log("this one should never fire");
}, 999);

clearTimeout(timerId); // remove timeout before the engine is ready

setTimeout(function () {
  log("timer fired");
}, 1000);
