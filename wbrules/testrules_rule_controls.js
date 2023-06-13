defineVirtualDevice('ctrltest', {
  cells: {
    disable: {
      type: 'pushbutton',
    },
    enable: {
      type: 'pushbutton',
    },
    trigger: {
      type: 'pushbutton',
    },
    run: {
      type: 'pushbutton',
    },
  },
});

var m = defineRule({
  whenChanged: 'ctrltest/trigger',
  then: function () {
    log('controllable rule fired');
  },
});

defineRule({
  whenChanged: 'ctrltest/disable',
  then: function () {
    log('disable');
    disableRule(m);
  },
});

defineRule({
  whenChanged: 'ctrltest/enable',
  then: function () {
    log('enable');
    enableRule(m);
  },
});

defineRule({
  whenChanged: 'ctrltest/run',
  then: function () {
    log('run');
    runRule(m);
  },
});
