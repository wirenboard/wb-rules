/* global defineVirtualDevice, defineRule, dev, log, setTimeout */

// Rules and timers registered AFTER an await must still work and stay
// attributed to this file (async continuations run as promise jobs with
// no calling-realm context; per-realm builtin bindings fix dispatch).

defineVirtualDevice('async_reg', {
  cells: {
    boot: { type: 'switch', value: false },
    probe: { type: 'switch', value: false },
  },
});

defineRule('async_boot', {
  whenChanged: 'async_reg/boot',
  then: async function () {
    await Promise.resolve();
    // both of these run inside a promise job
    defineRule('late_rule', {
      whenChanged: 'async_reg/probe',
      then: function (v) {
        log('late rule fired: {}', v);
      },
    });
    setTimeout(function () {
      log('late timer fired');
    }, 1);
    log('late registration done');
  },
});
