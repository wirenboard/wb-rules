/* global defineVirtualDevice, defineRule, dev, log, sleep, nextMqtt, spawn, runShellCommand, changed */
defineVirtualDevice('async_api', {
  title: 'async api',
  cells: {
    shell: { type: 'switch', value: false },
    fail: { type: 'switch', value: false },
    wait: { type: 'switch', value: false },
    mqtt: { type: 'switch', value: false },
    track: { type: 'switch', value: false },
    level: { type: 'value', value: 0, readonly: false },
    motion: { type: 'switch', value: false },
    light: { type: 'switch', value: false, readonly: true },
  },
});

defineRule('async_api_shell', {
  whenChanged: 'async_api/shell',
  then: async function () {
    var r = await runShellCommand('echo -n hello-async', { captureOutput: true });
    log('shell done: {} [{}]', r.exitCode, r.capturedOutput);
  },
});

defineRule('async_api_fail', {
  whenChanged: 'async_api/fail',
  then: async function () {
    try {
      await spawn('/definitely-no-such-binary-wb', []);
      log('fail rule: unexpectedly resolved');
    } catch (e) {
      log('spawn rejected: {}', e.message.indexOf('no-such-binary') >= 0);
    }
  },
});

defineRule('async_api_wait', {
  whenChanged: 'async_api/wait',
  then: async function () {
    await sleep(50);
    log('sleep done');
  },
});

defineRule('async_api_mqtt', {
  whenChanged: 'async_api/mqtt',
  then: async function () {
    var msg = await nextMqtt('/test/async/next');
    log('next mqtt: {} = {}', msg.topic, msg.value);
  },
});

defineRule('async_api_track', {
  whenChanged: 'async_api/track',
  then: async function () {
    var v = await changed('async_api/level');
    log('changed resolved: {}', v);
  },
});

// the README's motion-detector pattern, compressed for regression:
// light on at motion, off after 10s of stillness, re-motion cancels
(async function () {
  for (;;) {
    while (!(await changed('async_api/motion'))) {}
    dev['async_api/light'] = true;
    try {
      for (;;) {
        while (await changed('async_api/motion')) {}
        await changed('async_api/motion', 10000);
      }
    } catch (e) {
      dev['async_api/light'] = false;
    }
  }
})();
