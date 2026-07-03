/* global defineRule */

defineRule('brokenCellChange', {
  asSoonAs: function () {
    return dev.somedev.foobar;
  },
  then: function () {
    badvar;
  },
});
