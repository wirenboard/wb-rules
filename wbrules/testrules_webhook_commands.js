global.__proto__.runShellCommand = function (command, options) {
  log('run command: {}', command);
  if (options.input != null) {
    log('input: {}', options.input);
  }
  if (options.exitCallback) {
    options.exitCallback(dev['test_webhook/exit_code'], 'ok', 'stderr');
  }
};

defineVirtualDevice('test_webhook', {
  cells: {
    exit_code: {
      type: 'value',
      readonly: false,
      value: 0,
    },
    send_text: { type: 'pushbutton' },
    send_json_object: { type: 'pushbutton' },
    send_json_string: { type: 'pushbutton' },
    send_get_no_body: { type: 'pushbutton' },
    send_with_headers: { type: 'pushbutton' },
    send_custom_ct: { type: 'pushbutton' },
  },
});

defineRule({
  whenChanged: 'test_webhook/send_text',
  then: function () {
    Notify.sendWebhook({
      url: 'https://example.com/hook',
      body: 'plain text body',
    });
  },
});

defineRule({
  whenChanged: 'test_webhook/send_json_object',
  then: function () {
    Notify.sendWebhook({
      url: 'https://example.com/hook',
      body: { event: 'alarm', value: 42 },
    });
  },
});

defineRule({
  whenChanged: 'test_webhook/send_json_string',
  then: function () {
    Notify.sendWebhook({
      url: 'https://example.com/hook',
      body: '{"alert":"text"}',
    });
  },
});

defineRule({
  whenChanged: 'test_webhook/send_get_no_body',
  then: function () {
    Notify.sendWebhook({
      url: 'https://example.com/ping',
      method: 'GET',
    });
  },
});

defineRule({
  whenChanged: 'test_webhook/send_with_headers',
  then: function () {
    Notify.sendWebhook({
      url: 'https://example.com/hook',
      headers: { Authorization: 'Bearer xyz' },
      body: { ok: true },
    });
  },
});

defineRule({
  whenChanged: 'test_webhook/send_custom_ct',
  then: function () {
    Notify.sendWebhook({
      url: 'https://example.com/hook',
      contentType: 'application/xml',
      body: '<event>alarm</event>',
    });
  },
});
