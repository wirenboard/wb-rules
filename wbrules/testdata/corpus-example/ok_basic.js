// clean ES5-style rule file: must classify as "ok"
defineVirtualDevice('corpus_ok', { cells: { c: { type: 'value', value: 0 } } });
defineRule('corpus_ok_rule', {
  whenChanged: 'corpus_ok/c',
  then: function (v) { log('corpus ok: {}', v); },
});
