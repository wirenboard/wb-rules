/* global defineRule, defineVirtualDevice, getDevicesList, log */

defineVirtualDevice('listdev_b', {
  title: 'ListDevB',
  cells: {
    x: { type: 'switch', value: false },
  },
});

defineVirtualDevice('listdev_a', {
  title: 'ListDevA',
  cells: {
    x: { type: 'switch', value: false },
    y: { type: 'value', value: 1 },
  },
});

defineVirtualDevice('lctl', {
  title: 'LCtl',
  cells: {
    list: { type: 'switch', value: false },
  },
});

defineRule('list', {
  whenChanged: 'lctl/list',
  then: function () {
    var devs = getDevicesList();
    log('ids: {}', devs.map(function (d) { return d.getId(); }).join(','));
    log('virtual: {}', devs.filter(function (d) { return d.isVirtual(); })
      .map(function (d) { return d.getId(); }).join(','));
    devs.forEach(function (d) {
      log('driver of {}: [{}]', d.getId(), d.getDriverId());
    });

    // list elements are full device objects
    var a = devs.filter(function (d) { return d.getId() === 'listdev_a'; })[0];
    log('listdev_a controls via list element: {}', a ? a.controlsList().length : 'NOT FOUND');
  },
});
