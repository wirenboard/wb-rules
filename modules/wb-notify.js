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

function isValidJSON(str) {
  try {
    JSON.parse(str);
    return true;
  } catch (e) {
    return false;
  }
}

exports.sendEmail = function (to, subject, text) {
  log('sending email: {}', subject);
  var base64subject = Duktape.enc('base64', subject);
  runShellCommand('/usr/sbin/sendmail -t', {
    captureErrorOutput: true,
    captureOutput: true,
    input: 'To: {}\r\nSubject: =?utf-8?B?{}?=\r\nContent-Type: text/plain; charset=utf-8\n\n{}'.format(to, base64subject, text),
    exitCallback: function exitCallback(exitCode, capturedOutput, capturedErrorOutput) {
      if (exitCode != 0)
        log.error(
          'error sending email:\n{}\n{}',
          capturedOutput,
          capturedErrorOutput
        );
    },
  });
};

exports.sendSMS = function (to, text, command) {
  var doneCallback = function (exitCode, capturedOutput, capturedErrorOutput) {
    _smsBusy = false;
    if (exitCode != 0)
      log.error('error sending sms:\n{}\n{}', capturedOutput, capturedErrorOutput);
    _advanceSmsQueue();
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

exports.sendWebhook = function (opts) {
  if (!opts || !opts.url) throw new Error("sendWebhook: 'url' required");
  var method = (opts.method || 'POST').toUpperCase();
  var body = opts.body;
  if (body != null && typeof body === 'object') body = JSON.stringify(body);
  var contentType = opts.contentType ||
    (body != null && isValidJSON(body) ? 'application/json' : 'text/plain; charset=utf-8');

  var cmd = 'curl -s -X ' + method + ' ' + _shellQuote(opts.url);
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

  log('sending webhook: {} {}', method, opts.url);
  runShellCommand(cmd, {
    captureErrorOutput: true,
    captureOutput: true,
    input: body == null ? null : String(body),
    exitCallback: function exitCallback(exitCode, capturedOutput, capturedErrorOutput) {
      if (exitCode != 0)
        log.error(
          'error sending webhook:\n{}\n{}',
          capturedOutput,
          capturedErrorOutput
        );
    },
  });
};

exports.sendTelegramMessage = function (token, chatId, text) {
  log('sending telegram message: {}', text);
  runShellCommand(
    "curl -s -X POST https://api.telegram.org/bot{}/sendMessage -H 'Content-Type: application/x-www-form-urlencoded' -d @-".format(token),
    {
      captureErrorOutput: true,
      captureOutput: true,
      input: 'chat_id={}&text={}'.format(chatId, encodeURIComponent(text)),
      exitCallback: function exitCallback(exitCode, capturedOutput, capturedErrorOutput) {
        if (exitCode != 0)
          log.error(
            'error sending telegram message:\n{}\n{}',
            capturedOutput,
            capturedErrorOutput
          );
        try {
          var response = JSON.parse(capturedOutput);
          if (!response.ok)
            log.error(
              'error sending telegram message:\n{} {}',
              response.error_code,
              response.description
            );
        } catch (e) {
          log.error('error parsing response: {}', e);
        }
      },
    }
  );
};
