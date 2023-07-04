global.myvar = 84;

function adder(a, b) {
  return a - b;
}

defineRule({
  whenChanged: 'test/isolation',
  then: function () {
    log('2: myvar: {}', global.myvar);
    log('2: add {} and {}: {}', 2, 3, adder(2, 3));
  },
});
