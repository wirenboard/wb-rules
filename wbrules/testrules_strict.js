'use strict';
/* global defineVirtualDevice, defineRule, dev, log, PersistentStorage, StorableObject */

// Strict mode makes JS throw TypeError when a Proxy set trap returns a
// falsy value. The dev/PersistentStorage/StorableObject traps must all
// return true or every write below explodes.

defineVirtualDevice('strict_demo', {
  cells: {
    trigger: { type: 'switch', value: false },
    count: { type: 'value', value: 0, readonly: true },
  },
});

var ps = new PersistentStorage('strict-test', { global: true });

defineRule('strict_rule', {
  whenChanged: 'strict_demo/trigger',
  then: function () {
    dev['strict_demo/count'] = dev['strict_demo/count'] + 1; // dev set trap
    ps['n'] = dev['strict_demo/count']; // PersistentStorage set trap
    ps['obj'] = new StorableObject({ nested: 0 });
    ps['obj'].nested = ps['n']; // StorableObject set trap
    log('strict count {} ps {}', dev['strict_demo/count'], ps['n']);
  },
});

