/* global defineVirtualDevice, defineRule, dev, sleep, changed, nextMqtt, runShellCommand */
// sample-async.js — async/await in wb-rules (QuickJS engine showcase).
//
// Promise reactions run when the engine returns to idle, exactly like
// microtasks on an event-loop runtime, and everything a rule does after
// an `await` keeps working as usual: dev[...] writes, timers, spawn,
// PersistentStorage — all still attributed to this file. Unhandled async
// errors do not vanish: they show up in the rule log as
// "async rule error: ..." with a stack trace.
//
// The library is promise-native: spawn()/runShellCommand() return a
// Promise of {exitCode, capturedOutput, capturedErrorOutput}, sleep(ms)
// is the async setTimeout, nextMqtt(topic[, timeoutMs]) resolves with
// the next live MQTT message on a topic.
//
// Deployable as-is: creates an "async_demo" virtual device with four
// buttons to play with.

defineVirtualDevice('async_demo', {
  title: 'Async/await demo',
  cells: {
    sequence: { type: 'pushbutton' },
    parallel: { type: 'pushbutton' },
    listen: { type: 'pushbutton' },
    watch: { type: 'pushbutton' },
    motion: { type: 'switch', value: false },
    light: { type: 'switch', value: false, readonly: true },
    fail: { type: 'pushbutton' },
    status: { type: 'text', value: 'idle', readonly: true },
  },
});

// A timed sequence reads top to bottom — no nested setTimeout callbacks
defineRule('async_sequence', {
  whenChanged: 'async_demo/sequence',
  then: async () => {
    for (let i = 3; i >= 1; i--) {
      dev['async_demo/status'] = 'countdown: ' + i;
      await sleep(1000);
    }
    dev['async_demo/status'] = 'liftoff!';
  },
});

// Two commands run concurrently; await Promise.all() joins the results
defineRule('async_parallel', {
  whenChanged: 'async_demo/parallel',
  then: async () => {
    dev['async_demo/status'] = 'gathering...';
    const [host, time] = await Promise.all([
      runShellCommand('hostname', { captureOutput: true }),
      runShellCommand('date +%T', { captureOutput: true }),
    ]);
    dev['async_demo/status'] = host.capturedOutput.trim() + ' @ ' + time.capturedOutput.trim();
  },
});

// Await the next live MQTT message — here, a press of the "sequence"
// button within 10 seconds
defineRule('async_listen', {
  whenChanged: 'async_demo/listen',
  then: async () => {
    dev['async_demo/status'] = 'press "sequence" within 10s...';
    try {
      const msg = await nextMqtt('/devices/async_demo/controls/sequence/on', 10000);
      dev['async_demo/status'] = 'caught it: ' + msg.topic + ' = ' + msg.value;
    } catch (e) {
      dev['async_demo/status'] = 'no press within 10s';
    }
  },
});

// Await the next change of a CONTROL (whenChanged semantics, converted
// values) - here, the light the motion detector below drives. Note: do
// not await a control this same rule just wrote - the change event of
// your own write is still queued and would resolve you immediately.
defineRule('async_watch', {
  whenChanged: 'async_demo/watch',
  then: async () => {
    dev['async_demo/status'] = 'watching the light: toggle "motion" (10s)...';
    try {
      const val = await changed('async_demo/light', 10000);
      dev['async_demo/status'] = 'light changed to: ' + val;
    } catch (e) {
      dev['async_demo/status'] = 'light did not change in 10s';
    }
  },
});

// The documentation's classic "motion detector with off-delay",
// rewritten: no setTimeout/clearTimeout juggling, no timer-id state -
// one linear loop. Toggle "motion" in the UI; "light" follows with a
// 10 s off-delay (the doc example uses 30 s).
const OFF_DELAY_MS = 10 * 1000;
(async () => {
  for (;;) {
    while (!(await changed('async_demo/motion'))) { /* wait for motion */ }
    dev['async_demo/light'] = true;
    try {
      for (;;) {
        while (await changed('async_demo/motion')) { /* wait for it to clear */ }
        dev['async_demo/status'] = 'no motion: light off in 10s...';
        await changed('async_demo/motion', OFF_DELAY_MS);
        dev['async_demo/status'] = 'motion is back';
      }
    } catch (e) {
      dev['async_demo/light'] = false; // 10 s of stillness
      dev['async_demo/status'] = 'no motion for 10s: light off';
    }
  }
})();

// Deliberately no try/catch: half a second after the press, the rejection
// lands in the rule log as "async rule error: ..." — watch the Rules page
// log (or /wbrules/log/error) when you press this one
defineRule('async_fail', {
  whenChanged: 'async_demo/fail',
  then: async () => {
    dev['async_demo/status'] = 'failing asynchronously in 0.5s...';
    await sleep(500);
    throw new Error('demo async failure (this IS supposed to appear in the log)');
  },
});
