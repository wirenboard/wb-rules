/* global defineVirtualDevice, defineRule, getControl, dev, log */

// A virtual device with a numeric control. Writing a non-numeric value to it is
// rejected by the driver's value conversion. That rejection must be surfaced to
// the rule debug console (engine.Log -> /wbrules/log/error, which homeui shows)
// instead of failing silently to syslog - but it must NOT throw: the rule keeps
// running (no backward-compat break) and the cached value is not poisoned.
defineVirtualDevice('vdev', {
  title: 'VDev',
  cells: {
    num: {
      type: 'value',
      value: 0,
    },
  },
});

defineRule('setValueTypeMismatch', {
  whenChanged: 'somedev/sw',
  then: function () {
    // Both write paths reject a wrong-typed value: reported to the rule log,
    // ignored, and execution continues (neither throws).
    getControl('vdev/num').setValue('not a number'); // via getControl().setValue()
    dev['vdev/num'] = 'also not a number'; // via the dev[...] proxy set trap

    // A write to a control that does not exist is also reported to the rule
    // console and ignored, not thrown.
    dev['vdev/nonexistent'] = 5;

    // reached only because none of the writes threw; value is still the real 0.
    log.info('after bad writes: value=' + dev['vdev/num']);

    // A correctly-typed write still succeeds and updates the control.
    dev['vdev/num'] = 42;
    log.info('correct write: value=' + dev['vdev/num']);
  },
});
