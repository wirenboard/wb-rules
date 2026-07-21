/* global defineVirtualDevice, defineRule, cron, log, enableRule, disableRule */

defineVirtualDevice('cron_switch', {
  cells: {
    enabled: {
      type: 'switch',
      value: true,
    },
  },
});

var cronRule = defineRule('cron_disable_test', {
  when: cron('@hourly'),
  then: function () {
    log('cron rule fired');
  },
});

defineRule('cron_toggle', {
  whenChanged: 'cron_switch/enabled',
  then: function (newValue) {
    if (newValue) {
      log('cron rule enabled');
      enableRule(cronRule);
    } else {
      log('cron rule disabled');
      disableRule(cronRule);
    }
  },
});
