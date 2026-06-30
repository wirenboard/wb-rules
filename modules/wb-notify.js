/* global log, runShellCommand, debug */

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

// Report the send result: if a callback is provided, hand the error (or null on
// success) to it and let the caller decide what to do. Otherwise, fall back to
// logging the error so failures are not silently swallowed.
function _notifyDone(callback, err) {
  if (typeof callback === 'function') {
    // Isolate user code: a throwing callback must not abort our exitCallback
    try {
      callback(err);
    } catch (e) {
      log.error('error in notify callback: {}', e);
    }
  } else if (err) {
    log.error('{}', err.message);
  }
}

var _BASE64_ALPHABET =
  'ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789+/';

// Replace unpaired UTF-16 surrogates with U+FFFD. encodeURIComponent (used by
// _utf8ToBase64) throws URIError on malformed UTF-16 such as a lone surrogate,
// which would otherwise crash sendEmail; sanitizing keeps notifications working.
function _sanitizeSurrogates(str) {
  var out = '';
  for (var i = 0; i < str.length; i++) {
    var c = str.charCodeAt(i);
    if (c >= 0xd800 && c <= 0xdbff) {
      // high surrogate: valid only when immediately followed by a low surrogate
      var next = i + 1 < str.length ? str.charCodeAt(i + 1) : 0;
      if (next >= 0xdc00 && next <= 0xdfff) {
        out += str.charAt(i) + str.charAt(i + 1);
        i++;
      } else {
        out += '�';
      }
    } else if (c >= 0xdc00 && c <= 0xdfff) {
      // lone low surrogate
      out += '�';
    } else {
      out += str.charAt(i);
    }
  }
  return out;
}

// Encode a string as base64 of its UTF-8 bytes.
//
// Duktape keeps strings as CESU-8 internally, so Duktape.enc('base64', str)
// (and passing a string straight to a shell command) yields invalid UTF-8 for
// characters outside the BMP — e.g. emoji — which mail clients then render as
// garbage. encodeURIComponent produces the correct UTF-8 byte sequence for
// every code point (it combines surrogate pairs), which we base64-encode here.
function _utf8ToBase64(str) {
  var enc = encodeURIComponent(_sanitizeSurrogates(String(str)));
  var bytes = [];
  var i = 0;
  while (i < enc.length) {
    if (enc.charAt(i) === '%') {
      bytes.push(parseInt(enc.substr(i + 1, 2), 16));
      i += 3;
    } else {
      bytes.push(enc.charCodeAt(i));
      i += 1;
    }
  }

  var out = '';
  for (i = 0; i + 3 <= bytes.length; i += 3) {
    var n = (bytes[i] << 16) | (bytes[i + 1] << 8) | bytes[i + 2];
    out +=
      _BASE64_ALPHABET.charAt((n >> 18) & 63) +
      _BASE64_ALPHABET.charAt((n >> 12) & 63) +
      _BASE64_ALPHABET.charAt((n >> 6) & 63) +
      _BASE64_ALPHABET.charAt(n & 63);
  }
  var rem = bytes.length - i;
  if (rem === 1) {
    var a = bytes[i] << 16;
    out +=
      _BASE64_ALPHABET.charAt((a >> 18) & 63) +
      _BASE64_ALPHABET.charAt((a >> 12) & 63) +
      '==';
  } else if (rem === 2) {
    var b = (bytes[i] << 16) | (bytes[i + 1] << 8);
    out +=
      _BASE64_ALPHABET.charAt((b >> 18) & 63) +
      _BASE64_ALPHABET.charAt((b >> 12) & 63) +
      _BASE64_ALPHABET.charAt((b >> 6) & 63) +
      '=';
  }
  return out;
}

// Split a base64 payload into 76-character lines per RFC 2045.
function _wrapBase64(b64) {
  var lines = [];
  for (var i = 0; i < b64.length; i += 76) {
    lines.push(b64.substr(i, 76));
  }
  return lines.join('\r\n');
}

exports.sendEmail = function (to, subject, text, callback) {
  log('sending email: {}', subject);
  var input =
    'To: ' + to + '\r\n' +
    'Subject: =?utf-8?B?' + _utf8ToBase64(subject) + '?=\r\n' +
    'MIME-Version: 1.0\r\n' +
    'Content-Type: text/plain; charset=utf-8\r\n' +
    'Content-Transfer-Encoding: base64\r\n\r\n' +
    _wrapBase64(_utf8ToBase64(text));
  runShellCommand('/usr/sbin/sendmail -t', {
    captureErrorOutput: true,
    captureOutput: true,
    input: input,
    exitCallback: function exitCallback(exitCode, capturedOutput, capturedErrorOutput) {
      var err = null;
      if (exitCode != 0) {
        err = new Error('error sending email:\n' + capturedOutput + '\n' + capturedErrorOutput);
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
        } else {
          // Only inspect the Telegram JSON response when curl itself succeeded,
          // otherwise a parse error would mask the real command failure.
          try {
            var response = JSON.parse(capturedOutput);
            if (!response.ok) {
              err = new Error('error sending telegram message:\n' + response.error_code + ' ' + response.description);
            }
          } catch (e) {
            err = new Error('error parsing response: ' + e);
          }
        }
        _notifyDone(callback, err);
      },
    }
  );
};
