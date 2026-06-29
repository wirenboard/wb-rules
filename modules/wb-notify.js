/* global log, runShellCommand, debug, Duktape */

var _smsQueue = [],
  _smsBusy = false;

function _advanceSmsQueue() {
  if (!_smsQueue.length) return;
  var next = _smsQueue.shift();
  next();
}

function _sendSMSGammuLike(to, text, command, doneCallback) {
  log('sending sms (gammu-like): {}', text);
  command = command || "wb-gsm restart_if_broken && gammu sendsms TEXT '{}' -unicode";

  var input = null;
  var maxPlaceholders = 2;
  var count = (command.match(/\{\}/g) || []).length;

  if (count == maxPlaceholders) {
    command = command.format(to, text);
  } else {
    command = command.format(to);
    input = text;
  }

  debug('sms command: {}'.format(command));

  runShellCommand(command, {
    captureErrorOutput: true,
    captureOutput: true,
    input: input,
    exitCallback: doneCallback,
  });
}

function _sendSMSModemManager(to, text, doneCallback) {
  log('sending sms (via ModemManager): {}', text);
  var command =
    'mmcli -m any --messaging-create-sms="number={},text={}" | sed -n \'s#^Success.*/SMS/\\([0-9]\\+\\).*$#\\1#p\' | xargs mmcli --send -s';

  if (text.indexOf('"') >= 0) {
    // can't send messages with all types of quotes via mmcli,
    // see https://gitlab.freedesktop.org/mobile-broadband/ModemManager/-/issues/275
    log.warning(
      "ModemManager can't handle SMS with double quotes now, auto replaced with single ones"
    );
    text = text.replace(/"/g, "'");
  }
  text = '\\"' + text + '\\"';

  command = command.format(to, text);
  debug('sms command: {}'.format(command));

  runShellCommand(command, {
    captureErrorOutput: true,
    captureOutput: true,
    exitCallback: doneCallback,
  });
}

function _checkUse4gModem(doneCallback) {
  // in case of using 4g modems wb-gsm.service must be started
  // so check its ExecCondition
  // in case of using 2G,3G modems this service will be stopped
  runShellCommand('wb-gsm should_enable', {
    captureOutput: true,
    captureErrorOutput: true,
    exitCallback: function (exitCode) {
      doneCallback(exitCode === 0);
    },
  });
}

function _shellQuote(s) {
  return "'" + String(s).replace(/'/g, "'\\''") + "'";
}

function _isValidJSON(str) {
  try {
    JSON.parse(str);
    return true;
  } catch (e) {
    return false;
  }
}

// Strip path, query and URL userinfo so we don't leak secrets embedded in
// webhook URLs. Discord/Slack tokens live in the path, Gotify/WeChat Work
// keys in the query, and basic-auth style credentials may appear in userinfo.
function _redactUrlForLog(url) {
  var m = /^([a-zA-Z][a-zA-Z0-9+.-]*:\/\/)([^/?#]*)/.exec(String(url));
  if (!m) return '(invalid url)';
  var authority = m[2];
  var atIdx = authority.lastIndexOf('@');
  return m[1] + (atIdx >= 0 ? authority.slice(atIdx + 1) : authority);
}

var ALLOWED_WEBHOOK_METHODS = ['GET', 'POST', 'PUT', 'PATCH', 'DELETE'];

function normalizeWebhookMethod(method) {
  var normalized = method ? String(method).toUpperCase() : 'POST';
  if (ALLOWED_WEBHOOK_METHODS.indexOf(normalized) === -1) {
    throw new Error(
      "invalid webhook method '" + normalized + "', expected one of " + ALLOWED_WEBHOOK_METHODS.join('/')
    );
  }
  return normalized;
}
exports.normalizeWebhookMethod = normalizeWebhookMethod;

// Invoke an optional Node-style callback with an Error (or null on success),
// guarding against a missing or non-function argument.
function _notifyDone(callback, err) {
  if (typeof callback === 'function') callback(err);
}

exports.sendEmail = function (to, subject, text, callback) {
  log('sending email: {}', subject);
  var base64subject = Duktape.enc('base64', subject);
  runShellCommand('/usr/sbin/sendmail -t', {
    captureErrorOutput: true,
    captureOutput: true,
    input: 'To: {}\r\nSubject: =?utf-8?B?{}?=\r\nContent-Type: text/plain; charset=utf-8\n\n{}'.format(to, base64subject, text),
    exitCallback: function exitCallback(exitCode, capturedOutput, capturedErrorOutput) {
      var err = null;
      if (exitCode != 0) {
        err = new Error('error sending email:\n' + capturedOutput + '\n' + capturedErrorOutput);
        log.error('{}', err.message);
      }
      _notifyDone(callback, err);
    },
  });
};

exports.sendSMS = function (to, text, command, callback) {
  // 'command' is optional; allow sendSMS(to, text, callback)
  if (typeof command === 'function') {
    callback = command;
    command = undefined;
  }

  var doneCallback = function (exitCode, capturedOutput, capturedErrorOutput) {
    _smsBusy = false;
    var err = null;
    if (exitCode != 0) {
      err = new Error('error sending sms:\n' + capturedOutput + '\n' + capturedErrorOutput);
      log.error('{}', err.message);
    }
    _advanceSmsQueue();
    _notifyDone(callback, err);
  };

  var sendOrEnqueue = function (doSend) {
    if (_smsBusy) {
      debug('queueing sms: {}', text);
      _smsQueue.push(doSend);
    } else {
      _smsBusy = true;
      doSend();
    }
  };

  if (command) {
    sendOrEnqueue(function () {
      _sendSMSGammuLike(to, text, command, doneCallback);
    });
  } else {
    _checkUse4gModem(function (hasModemManager) {
      if (hasModemManager) {
        sendOrEnqueue(function () {
          _sendSMSModemManager(to, text, doneCallback);
        });
      } else {
        sendOrEnqueue(function () {
          _sendSMSGammuLike(to, text, '', doneCallback);
        });
      }
    });
  }
};

exports.sendWebhook = function (opts, callback) {
  if (!opts || !opts.url) throw new Error("sendWebhook: 'url' required");
  var method = normalizeWebhookMethod(opts.method);
  var body = opts.body;
  var bodyIsObject = body != null && typeof body === 'object';
  if (bodyIsObject) body = JSON.stringify(body);
  var contentType = opts.contentType ||
    (bodyIsObject || (typeof body === 'string' && _isValidJSON(body))
      ? 'application/json'
      : 'text/plain; charset=utf-8');

  var cmd = 'curl -sS --fail -X ' + _shellQuote(method);
  cmd += ' -H ' + _shellQuote('Content-Type: ' + contentType);
  if (opts.headers) {
    var headers = opts.headers;
    Object.keys(headers).forEach(function (k) {
      // eslint-disable-next-line security/detect-object-injection
      var value = headers[k];
      cmd += ' -H ' + _shellQuote(k + ': ' + value);
    });
  }
  if (body != null) cmd += ' --data-binary @-';
  // -- ends option parsing so a URL starting with '-' isn't taken as a curl flag
  cmd += ' -- ' + _shellQuote(opts.url);

  log('sending webhook: {} {}', method, _redactUrlForLog(opts.url));
  runShellCommand(cmd, {
    captureErrorOutput: true,
    captureOutput: true,
    input: body == null ? null : String(body),
    exitCallback: function exitCallback(exitCode, capturedOutput, capturedErrorOutput) {
      var err = null;
      if (exitCode != 0) {
        err = new Error('error sending webhook:\n' + capturedOutput + '\n' + capturedErrorOutput);
        log.error('{}', err.message);
      }
      _notifyDone(callback, err);
    },
  });
};

exports.sendTelegramMessage = function (token, chatId, text, callback) {
  log('sending telegram message: {}', text);
  runShellCommand(
    "curl -s -X POST https://api.telegram.org/bot{}/sendMessage -H 'Content-Type: application/x-www-form-urlencoded' -d @-".format(token),
    {
      captureErrorOutput: true,
      captureOutput: true,
      input: 'chat_id={}&text={}'.format(chatId, encodeURIComponent(text)),
      exitCallback: function exitCallback(exitCode, capturedOutput, capturedErrorOutput) {
        var err = null;
        if (exitCode != 0) {
          err = new Error('error sending telegram message:\n' + capturedOutput + '\n' + capturedErrorOutput);
          log.error('{}', err.message);
        } else {
          // Only inspect the Telegram JSON response when curl itself succeeded,
          // otherwise a parse error would mask the real command failure.
          try {
            var response = JSON.parse(capturedOutput);
            if (!response.ok) {
              err = new Error('error sending telegram message:\n' + response.error_code + ' ' + response.description);
              log.error('{}', err.message);
            }
          } catch (e) {
            err = new Error('error parsing response: ' + e);
            log.error('{}', err.message);
          }
        }
        _notifyDone(callback, err);
      },
    }
  );
};
