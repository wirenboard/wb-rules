/* global defineVirtualDevice, defineRule, dev, log, Notify */

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
    send_with_options: {
      type: 'pushbutton',
    },
    send_options_no_callback: {
      type: 'pushbutton',
    },
  },
});

defineRule({
  whenChanged: 'test_tg/send',
  then: function () {
    Notify.sendTelegramMessage('1234567890:abcdefghijklmnopqrstuvwxyz123456789', '12345678', 'Test message', function (err) {
      log('telegram send status: {}', err ? 'error' : 'ok');
    });
  },
});

defineRule({
  whenChanged: 'test_tg/send_quoted',
  then: function () {
    Notify.sendTelegramMessage('1234567890:abcdefghijklmnopqrstuvwxyz123456789', '12345678', 'Test "message" \'single\'');
  },
});

defineRule({
  whenChanged: 'test_tg/send_with_options',
  then: function () {
    Notify.sendTelegramMessage(
      '1234567890:abcdefghijklmnopqrstuvwxyz123456789',
      '12345678',
      'Test message',
      {
        parseMode: 'HTML',
        disableWebPagePreview: true,
        disableNotification: true,
      },
      function (err) {
        log('telegram send status: {}', err ? 'error' : 'ok');
      }
    );
  },
});

defineRule({
  whenChanged: 'test_tg/send_options_no_callback',
  then: function () {
    Notify.sendTelegramMessage(
      '1234567890:abcdefghijklmnopqrstuvwxyz123456789',
      '12345678',
      'Test message',
      { parseMode: 'Markdown' }
    );
  },
});
