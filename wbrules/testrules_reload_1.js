defineVirtualDevice('vdev0', {
  title: 'VDev0',
  cells: {
    someCell: {
      type: 'switch',
      value: false,
    },
  },
});

defineRule('detRun', {
  when: function () {
    return true;
  },
  then: function () {
    log('detRun');
  },
});

// create rule indirectly to check dynamic cleanups
setTimeout(function () {
  defineRule('checkIndirect', {
    when: function () {
      return true;
    },
    then: function () {
      log('checkIndirect');
    },
  });
  log('timeout set');
}, 0);

testrules_reload_1_loaded = true;
