defineVirtualDevice('emptyStrTest', {
  title: 'Empty String Test',
  cells: {
    text: {
      type: 'text',
      value: '',
    },
  },
});

defineRule('textChanged', {
  whenChanged: 'emptyStrTest/text',
  then: function () {
    log('textChanged fired');
  },
});
