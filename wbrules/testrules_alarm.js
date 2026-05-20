/* global Notify, log */

// Override Notify used by modules loaded from lib.js.
Notify.sendEmail = function (to, subject, text) {
  log('EMAIL TO: {} SUBJ: {} TEXT: {}', to, subject, text);
};

Notify.sendSMS = function (to, text) {
  log('SMS TO: {} TEXT: {}', to, text);
};

Notify.sendTelegramMessage = function (token, chatId, text) {
  log('TELEGRAM MESSAGE TOKEN: {} CHATID: {} TEXT: {}', token, chatId, text);
};

Notify.sendWebhook = function (opts) {
  // Matrix txnId uses Date.now()+Math.random() — normalize so the log is deterministic.
  var url = String(opts.url).replace(
    /(\/send\/m\.room\.message\/)[^/?#]+/,
    '$1[txnId]'
  );
  var method = opts.method || 'POST';
  var contentType = opts.contentType || '(default)';
  var headers = opts.headers ? JSON.stringify(opts.headers) : '(none)';
  var body;
  if (opts.body == null) body = '(none)';
  else if (typeof opts.body === 'object') body = JSON.stringify(opts.body);
  else body = String(opts.body);
  log('WEBHOOK URL: {} METHOD: {} CONTENT-TYPE: {} HEADERS: {} BODY: {}',
    url, method, contentType, headers, body);
};
