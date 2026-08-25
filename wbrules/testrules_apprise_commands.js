/* global log, dev, defineVirtualDevice, defineRule, Notify */

global.__proto__.runShellCommand = function (command, options) {
  log('run command: {}', command);
  if (options.input != null) {
    log('input: {}', options.input);
  }
  if (options.exitCallback) {
    options.exitCallback(dev['test_apprise/exit_code'], 'out', 'stderr');
  }
};

defineVirtualDevice('test_apprise', {
  cells: {
    exit_code: {
      type: 'value',
      readonly: false,
      value: 0,
    },
    send_url: { type: 'pushbutton' },
    send_tag: { type: 'pushbutton' },
    send_titled: { type: 'pushbutton' },
    send_no_callback: { type: 'pushbutton' },
    send_empty_target: { type: 'pushbutton' },
  },
});

defineRule({
  whenChanged: 'test_apprise/send_url',
  then: function () {
    Notify.sendApprise('ntfy://ntfy.example/topic', '', 'plain body', function (err) {
      log('apprise send status: {}', err ? 'error' : 'ok');
    });
  },
});

defineRule({
  whenChanged: 'test_apprise/send_tag',
  then: function () {
    Notify.sendApprise('alarm', '', 'tagged body', function (err) {
      log('apprise send status: {}', err ? 'error' : 'ok');
    });
  },
});

defineRule({
  whenChanged: 'test_apprise/send_titled',
  then: function () {
    // Cyrillic title and body: both must go base64-encoded, so non-ASCII
    // characters survive the trip through the shell command line
    Notify.sendApprise('ntfy://ntfy.example/topic', 'Тревога', 'Насос', function (err) {
      log('apprise send status: {}', err ? 'error' : 'ok');
    });
  },
});

defineRule({
  whenChanged: 'test_apprise/send_no_callback',
  then: function () {
    // no callback: an error must be logged by the notify module itself
    Notify.sendApprise('ntfy://ntfy.example/topic', '', 'no callback body');
  },
});

defineRule({
  whenChanged: 'test_apprise/send_empty_target',
  then: function () {
    Notify.sendApprise('', '', 'whatever', function (err) {
      log('apprise send status: {}', err ? 'error' : 'ok');
    });
  },
});
