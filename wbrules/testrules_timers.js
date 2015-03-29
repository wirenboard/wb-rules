// -*- mode: js2-mode -*-

defineRule("startTimer", {
  asSoonAs: function () {
    return dev.somedev.foo == "t";
  },
  then: function () {
    startTimer("sometimer", 500);
  }
});

defineRule("startTicker", {
  asSoonAs: function () {
    return dev.somedev.foo == "p";
  },
  then: function () {
    startTicker("sometimer", 500);
  }
});

defineRule("stopTimer", {
  asSoonAs: function () {
    return dev.somedev.foo == "s";
  },
  then: function () {
    timers.sometimer.stop();
  }
});

defineRule("timer", {
  when: function () {
    return timers.sometimer.firing;
  },
  then: function () {
    log("timer fired");
  }
});

// setTimeout / setInterval based timers

var timer = null;

defineRule("startTimer1", {
  asSoonAs: function () {
    return dev.somedev.foo == "+t";
  },
  then: function () {
    if (timer)
      clearTimeout(timer);
    timer = setTimeout(function () {
      timer = null;
      log("timer fired");
    }, 500);
  }
});

defineRule("startTicker1", {
  asSoonAs: function () {
    return dev.somedev.foo == "+p";
  },
  then: function () {
    if (timer)
      clearTimeout(timer);
    timer = setInterval(function () {
      log("timer fired");
    }, 500);
  }
});

defineRule("stopTimer1", {
  asSoonAs: function () {
    return dev.somedev.foo == "+s";
  },
  then: function () {
    if (timer) {
      clearTimeout(timer);
      timer = null;
    }
  }
});
