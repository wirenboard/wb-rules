// -*- mode: js2-mode -*-

defineRule("brokenCellChange", {
  asSoonAs: function () {
    return dev.somedev.foobar;
  },
  then: function () {
    badvar;
  }
});
