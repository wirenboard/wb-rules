global.__proto__.runShellCommand = function (command, options) {
  log('run command: {}', command);
  if (options.input) {
    log('input: {}', options.input);
  }
  if (options.exitCallback) {
    options.exitCallback(dev['test_tg/exit_code'], '{"ok": true}', 'stderr');
  }
};

defineVirtualDevice('test_tg', {
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
  whenChanged: 'test_tg/send',
  then: function () {
    Notify.sendTelegramMessage('1234567890:abcdefghijklmnopqrstuvwxyz123456789', '12345678', 'Test message');
  },
});

defineRule({
  whenChanged: 'test_tg/send_quoted',
  then: function () {
    Notify.sendTelegramMessage('1234567890:abcdefghijklmnopqrstuvwxyz123456789', '12345678', 'Test "message" \'single\'');
  },
});
