/* global defineVirtualDevice, defineRule, getControl, dev, log */

// A wrong-typed control write must NOT throw (the rule keeps running - no
// backward-compat break) but must be surfaced to the rule debug console
// (engine.Log -> /wbrules/log/error, which homeui shows) WITH the location of
// the offending write, and it must not poison the cached value.
defineVirtualDevice('vdev', {
  title: 'VDev',
  cells: {
    num: { type: 'value', value: 0 },
  },
});

defineRule('badWriteVisible', {
  whenChanged: 'somedev/sw',
  then: function () {
    dev['vdev/num'] = 'not a number'; // line 17: rejected via the dev[] proxy - logged with "at ...:17"
    getControl('vdev/num').setValue('nope'); // line 18: rejected via setValue() - "at ...:18"
    dev['vdev/nonexistent'] = 5; // line 19: no such control - reported, not thrown
    // reached only because none of the writes threw; value must still be 0
    log.info('after bad writes: value=' + dev['vdev/num']);
  },
});
