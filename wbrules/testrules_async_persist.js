/* global defineVirtualDevice, defineRule, log, PersistentStorage */
defineVirtualDevice('async_ps', {
  title: 'async ps',
  cells: {
    probe: { type: 'switch', value: false },
    cyc: { type: 'switch', value: false },
  },
});

var ps = new PersistentStorage('asyncps');
ps.marker = 'from-sync';

defineRule('async_ps_cyclic', {
  whenChanged: 'async_ps/cyc',
  then: function () {
    var o = { name: 'loop' };
    o.self = o;
    try {
      ps.cyclic = new StorableObject(o);
      log('cyclic write unexpectedly succeeded');
    } catch (e) {
      // the storage bucket name embeds a per-file hash; assert only the
      // stable part of the message
      log('cyclic write rejected: {}', String(e.message).indexOf('cannot serialize') >= 0);
    }
  },
});

defineRule('async_ps_probe', {
  whenChanged: 'async_ps/probe',
  then: function () {
    (async function () {
      await Promise.resolve();
      // must resolve to the same per-file bucket as the sync handle
      var ps2 = new PersistentStorage('asyncps');
      log('async ps sees: {}', ps2.marker);
      ps2.asyncMarker = 'from-async';
      log('sync handle sees: {}', ps.asyncMarker);
    })();
  },
});
