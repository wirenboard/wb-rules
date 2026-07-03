/* global defineRule, dev */

defineRule('brokenCellChange', {
  asSoonAs: function () {
    return dev.somedev.foobar;
  },
  then: function () {
    badvar;
  },
});
