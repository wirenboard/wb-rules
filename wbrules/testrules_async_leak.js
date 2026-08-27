/* global defineVirtualDevice, defineRule, log, sleep, changed, nextMqtt, __wbAsyncWaiters */
defineVirtualDevice('async_leak', {
  title: 'async leak probes',
  cells: {
    park: { type: 'switch', value: false },
    churn: { type: 'switch', value: false },
    report: { type: 'switch', value: false },
    a: { type: 'value', value: 0, readonly: false },
  },
});

// a parked one-shot timer: must be stopped when the file reloads
sleep(3600000).then(function () {
  log('leak: hour sleep fired (must not happen in tests)');
});

defineRule('async_leak_park', {
  whenChanged: 'async_leak/park',
  then: function () {
    for (var i = 0; i < 10; i++) {
      changed('async_leak/a');
      nextMqtt('/async-leak/topic');
    }
    log('parked; waiters: {}', __wbAsyncWaiters());
  },
});

defineRule('async_leak_churn', {
  whenChanged: 'async_leak/churn',
  then: function () {
    for (var i = 0; i < 10; i++) {
      changed('async_leak/a', 50).catch(function () {
        log('churn timeout');
      });
    }
    log('churn armed; waiters: {}', __wbAsyncWaiters());
  },
});

defineRule('async_leak_report', {
  whenChanged: 'async_leak/report',
  then: function () {
    log('report waiters: {}', __wbAsyncWaiters());
  },
});
