global.__proto__.runShellCommand = function (command, options) {
  log('run command: {}', command);
  if (options.input) {
    log('input: {}', options.input);
  }
  if (options.exitCallback) {
    options.exitCallback(dev['test_email/exit_code'], 'stdout', 'stderr');
  }
};

defineVirtualDevice('test_email', {
  cells: {
    exit_code: {
      type: 'value',
      readonly: false,
      value: 0,
    },
    send: {
      type: 'pushbutton',
    },
    send_quoted: {
      type: 'pushbutton',
    },
  },
});

defineRule({
  whenChanged: 'test_email/send',
  then: function () {
    Notify.sendEmail('me@example.org', 'Test subject', 'Test text');
  },
});

defineRule({
  whenChanged: 'test_email/send_quoted',
  then: function () {
    Notify.sendEmail('me@example.org', 'Test "subject" \'single\'', 'Test "text" \'single\'');
  },
});
