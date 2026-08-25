/* global defineRule, defineVirtualDevice, removeVirtualDevice, wipeDevice, getDevice, log */

function defineTarget(title, value) {
  defineVirtualDevice('vdev_rm', {
    title: title,
    cells: {
      sw: { type: 'switch', value: value },
    },
  });
}

defineTarget('VDevRm', false);

defineVirtualDevice('ctl', {
  title: 'Ctl',
  cells: {
    remove: { type: 'switch', value: false },
    redefine: { type: 'switch', value: false },
    removeByMethod: { type: 'switch', value: false },
    removeBad: { type: 'switch', value: false },
    redefineByTimer: { type: 'switch', value: false },
    wipeExternal: { type: 'switch', value: false },
    wipeBad: { type: 'switch', value: false },
  },
});

function tryRemove(id) {
  try {
    removeVirtualDevice(id);
    log('removed {}', id);
  } catch (e) {
    log('error: {}', e.message);
  }
}

function tryWipe(id) {
  try {
    wipeDevice(id);
    log('wiped {}', id);
  } catch (e) {
    log('error: {}', e.message);
  }
}

defineRule('remove', {
  whenChanged: 'ctl/remove',
  then: function () {
    removeVirtualDevice('vdev_rm');
    log('exists after remove: {}', getDevice('vdev_rm') !== undefined);
  },
});

defineRule('redefine', {
  whenChanged: 'ctl/redefine',
  then: function () {
    // the id of a removed device can be used again
    defineTarget('VDevRm2', true);
    log('exists after redefine: {}', getDevice('vdev_rm') !== undefined);
  },
});

defineRule('removeByMethod', {
  whenChanged: 'ctl/removeByMethod',
  then: function () {
    getDevice('vdev_rm').remove();
    log('exists after remove(): {}', getDevice('vdev_rm') !== undefined);
  },
});

defineRule('removeBad', {
  whenChanged: 'ctl/removeBad',
  then: function () {
    tryRemove('vdev_rm'); // ok
    tryRemove('vdev_rm'); // already removed
    tryRemove('somedev'); // external device
    tryRemove('nonexistent'); // never existed
    tryRemove('wbrules'); // rule engine settings device
  },
});

defineRule('redefineByTimer', {
  whenChanged: 'ctl/redefineByTimer',
  then: function () {
    // timer callbacks run in the file cleanup scope, unlike rule callbacks
    setTimeout(function () {
      defineTarget('VDevRm3', true);
      log('redefined by timer');
    }, 1000);
  },
});

defineRule('wipeExternal', {
  whenChanged: 'ctl/wipeExternal',
  then: function () {
    getDevice('somedev').wipe(); // same as wipeDevice('somedev')
    log('external wiped');
  },
});

defineRule('wipeBad', {
  whenChanged: 'ctl/wipeBad',
  then: function () {
    tryWipe('vdev_rm'); // virtual - use removeVirtualDevice()
    tryWipe('nonexistent'); // never existed
    tryWipe('wbrules'); // local (engine settings device)
  },
});
