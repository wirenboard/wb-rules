defineRule('multiple_require', {
  whenChanged: 'test/multifile',
  then: function () {
    var m = require('test/multi_init');
    log('[2] My value of multi_init:', m.value);
  },
});

defineRule('static', {
  whenChanged: 'test/static',
  then: function () {
    var m = require('test/static');
    m.count();
  },
});
