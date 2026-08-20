'use strict';
// strict-mode file writing through the dev proxy: must classify as "ok"
defineVirtualDevice('corpus_strict', { cells: { n: { type: 'value', value: 0 } } });
defineRule('corpus_strict_rule', {
  whenChanged: 'corpus_strict/n',
  then: function () { dev['corpus_strict/n'] = dev['corpus_strict/n'] + 1; },
});
