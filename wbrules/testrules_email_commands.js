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
    send_emoji: {
      type: 'pushbutton',
    },
    send_lone_surrogate: {
      type: 'pushbutton',
    },
    send_header_injection: {
      type: 'pushbutton',
    },
  },
});

defineRule({
  whenChanged: 'test_email/send',
  then: function () {
    Notify.sendEmail('me@example.org', 'Test subject', 'Test text', function (err) {
      log('email send status: {}', err ? 'error' : 'ok');
    });
  },
});

defineRule({
  whenChanged: 'test_email/send_quoted',
  then: function () {
    Notify.sendEmail('me@example.org', 'Test "subject" \'single\'', 'Test "text" \'single\'');
  },
});

defineRule({
  whenChanged: 'test_email/send_emoji',
  then: function () {
    Notify.sendEmail('me@example.org', '🏠 тема', '🏠 текст');
  },
});

defineRule({
  whenChanged: 'test_email/send_lone_surrogate',
  then: function () {
    // malformed UTF-16 (lone surrogate) must not crash sendEmail
    Notify.sendEmail('me@example.org', 'a\uD800b', 'x\uD800y');
  },
});

defineRule({
  whenChanged: 'test_email/send_header_injection',
  then: function () {
    // a CR/LF in 'to' must be rejected, not turned into extra headers
    Notify.sendEmail('me@example.org\r\nBcc: evil@example.org', 'subj', 'body', function (err) {
      log('email send status: {}', err ? err.message : 'ok');
    });
  },
});
