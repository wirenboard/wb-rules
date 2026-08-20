/* global defineVirtualDevice, defineRule, log */
defineVirtualDevice('async_leak', {
  title: 'async leak probes',
  cells: {
    park: { type: 'switch', value: false },
    churn: { type: 'switch', value: false },
    report: { type: 'switch', value: false },
    a: { type: 'value', value: 0, readonly: false },
  },
});

defineRule('async_leak_probe', {
  whenChanged: 'async_leak/report',
  then: function () {
    log('probe alive');
  },
});
