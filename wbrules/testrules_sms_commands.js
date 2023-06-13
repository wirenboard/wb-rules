var exitCodes = [];

global.__proto__.runShellCommand = function (command, options) {
  if (exitCodes.length == 0) {
    exitCodes = [dev['test_sms/exit_code_2'], dev['test_sms/exit_code_1']];
  }

  log('run command: {}', command);
  if (options.input) {
    log('input: {}', options.input);
  }
  if (options.exitCallback) {
    options.exitCallback(exitCodes.pop(), 'stdout', 'stderr');
  }
};

defineVirtualDevice('test_sms', {
  cells: {
    exit_code_1: {
      type: 'value',
      readonly: false,
      value: 0,
    },
    exit_code_2: {
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
  whenChanged: 'test_sms/send',
  then: function () {
    Notify.sendSMS('88005553535', 'test value');
  },
});

defineRule({
  whenChanged: 'test_sms/send_quoted',
  then: function () {
    // can't send messages with all types of quotes via mmcli,
    // see https://gitlab.freedesktop.org/mobile-broadband/ModemManager/-/issues/275
    Notify.sendSMS('88005553535', 'test "value" \'single\'');
  },
});
