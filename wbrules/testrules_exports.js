/* global defineVirtualDevice, defineRule, log, module, exports */

// CommonJS-style module files must also load as plain rule files:
// exports is in scope and aliases module.exports.

exports.answer = 42;
module.exports.question = 'six by nine';

defineVirtualDevice('exports_demo', {
  cells: { poke: { type: 'switch', value: false } },
});

defineRule('exports_rule', {
  whenChanged: 'exports_demo/poke',
  then: function () {
    log('exports ok: {} {}', exports.answer, module.exports.question);
  },
});
