defineRule('test', {
  whenChanged: 'somedev/bar',
  then: function () {
    log('bar!');
  },
});

defineRule('test', {
  whenChanged: 'somedev/baz',
  then: function () {
    log('baz!');
  },
});
