/* global log */

var Notify = require('wb-notify');

var recipientTypes = {
  email: function getEmailSendFunc(src) {
    if (!Object.prototype.hasOwnProperty.call(src,'to')) throw new Error("email recipient without 'to'");
    var subject = Object.prototype.hasOwnProperty.call(src,'subject') ? '' + src.subject : '{}';
    return function sendEmailWrapper(text) {
      Notify.sendEmail(src.to, maybeFormat(subject, text), text);
    };
  },

  sms: function getSMSSendFunc(src) {
    if (!Object.prototype.hasOwnProperty.call(src,'to')) throw new Error("sms recipient without 'to'");
    return function sendSMSWrapper(text) {
      Notify.sendSMS(src.to, text, src.command || '');
    };
  },

  telegram: function getTelegramSendFunc(src) {
    if (!Object.prototype.hasOwnProperty.call(src,'chatId')) throw new Error("telegram message recipient without 'chatId'");
    return function sendTelegramWrapper(text) {
      Notify.sendTelegramMessage(src.token, src.chatId, text);
    };
  },

  webhook: function getWebhookSendFunc(src) {
    if (!Object.prototype.hasOwnProperty.call(src,'url')) throw new Error("webhook recipient without 'url'");
    return function sendWebhookWrapper(text) {
      var method = src.method ? ('' + src.method).toUpperCase() : src.method;
      var opts = {
        url: src.url,
        method: src.method,
        headers: src.headers,
        contentType: src.contentType,
      };
      if (src.bodyTemplate || !method || method === 'POST' || method === 'PUT' || method === 'PATCH') {
        opts.body = src.bodyTemplate ? maybeFormat(src.bodyTemplate, text) : text;
      }
      Notify.sendWebhook(opts);
    };
  },

  vk: function getVkSendFunc(src) {
    if (!Object.prototype.hasOwnProperty.call(src,'token')) throw new Error("vk recipient without 'token'");
    if (!Object.prototype.hasOwnProperty.call(src,'peerId')) throw new Error("vk recipient without 'peerId'");
    var peerId = Number(src.peerId);
    if (!isFinite(peerId)) throw new Error("vk recipient with non-numeric 'peerId'");
    var apiVersion = src.apiVersion || '5.131';
    return function sendVkWrapper(text) {
      var body = 'access_token=' + encodeURIComponent(src.token) +
        '&peer_id=' + peerId +
        '&random_id=0' +
        '&v=' + encodeURIComponent(apiVersion) +
        '&message=' + encodeURIComponent(text);
      Notify.sendWebhook({
        url: 'https://api.vk.com/method/messages.send',
        method: 'POST',
        contentType: 'application/x-www-form-urlencoded',
        body: body,
      });
    };
  },

  max: function getMaxSendFunc(src) {
    if (!Object.prototype.hasOwnProperty.call(src,'token')) throw new Error("max recipient without 'token'");
    if (!Object.prototype.hasOwnProperty.call(src,'chatId')) throw new Error("max recipient without 'chatId'");
    var chatId = Number(src.chatId);
    if (!isFinite(chatId)) throw new Error("max recipient with non-numeric 'chatId'");
    return function sendMaxWrapper(text) {
      Notify.sendWebhook({
        url: 'https://platform-api.max.ru/messages',
        method: 'POST',
        contentType: 'application/json',
        headers: { Authorization: src.token },
        body: JSON.stringify({ chat_id: chatId, text: text }),
      });
    };
  },

  matrix: function getMatrixSendFunc(src) {
    if (!Object.prototype.hasOwnProperty.call(src,'homeserver')) throw new Error("matrix recipient without 'homeserver'");
    if (!Object.prototype.hasOwnProperty.call(src,'accessToken')) throw new Error("matrix recipient without 'accessToken'");
    if (!Object.prototype.hasOwnProperty.call(src,'roomId')) throw new Error("matrix recipient without 'roomId'");
    var baseUrl = src.homeserver.replace(/\/+$/, '');
    var msgType = src.msgType || 'm.text';
    return function sendMatrixWrapper(text) {
      var txnId = 'wbrules-' + Date.now() + '-' + Math.random().toString(36).slice(2, 11);
      var url = baseUrl + '/_matrix/client/v3/rooms/' + encodeURIComponent(src.roomId) +
        '/send/m.room.message/' + encodeURIComponent(txnId);
      Notify.sendWebhook({
        url: url,
        method: 'PUT',
        contentType: 'application/json',
        headers: { Authorization: 'Bearer ' + src.accessToken },
        body: JSON.stringify({ msgtype: msgType, body: text }),
      });
    };
  },

  wechat: function getWechatSendFunc(src) {
    if (!Object.prototype.hasOwnProperty.call(src,'key')) throw new Error("wechat recipient without 'key'");
    return function sendWechatWrapper(text) {
      Notify.sendWebhook({
        url: 'https://qyapi.weixin.qq.com/cgi-bin/webhook/send?key=' + encodeURIComponent(src.key),
        method: 'POST',
        contentType: 'application/json',
        body: JSON.stringify({ msgtype: 'text', text: { content: text } }),
      });
    };
  },
};

function maybeFormat(text, arg) {
  return text.indexOf('{}') >= 0 || text.indexOf('{{') >= 0 ? text.xformat(arg) : text;
}

function getSendFunc(src) {
  if (
    !src ||
    typeof src != 'object' ||
    !Object.prototype.hasOwnProperty.call(src,'type') ||
    !recipientTypes.hasOwnProperty(src.type)
  )
    throw new Error('invalid recipient spec: ' + JSON.stringify(src));
  return recipientTypes[src.type](src);
}

var seq = 1;

function loadAlarm(alarmSrc, notify, alarmDeviceName) {
  if (!alarmSrc || typeof alarmSrc != 'object' || !alarmSrc.hasOwnProperty('cell'))
    throw new Error('invalid alarm definition');

  function checkHasNumKey(key) {
    if (!alarmSrc.hasOwnProperty(key)) return false;

    if (typeof alarmSrc[key] != 'number')
      throw new Error('{}: {}: number expected!'.format(JSON.stringify(alarmSrc), key));

    return true;
  }

  var ref = _WbRules.parseCellRef(alarmSrc.cell);
  var namePrefix = '__alarm{}__{}__'.format(seq++, alarmSrc.cell),
    cellName = alarmSrc.hasOwnProperty('name') ? 'alarm_' + alarmSrc.name : namePrefix + 'cell',
    hasExpectedValue = alarmSrc.hasOwnProperty('expectedValue'),
    hasMinValue = checkHasNumKey('minValue'),
    hasMaxValue = checkHasNumKey('maxValue'),
    alarmMessage =
      alarmSrc.alarmMessage ||
      alarmSrc.cell +
        (hasExpectedValue ? ' has unexpected value = {}' : ' is out of bounds, value = {}'),
    noAlarmMessage = alarmSrc.noAlarmMessage || alarmSrc.cell + ' is back to normal, value = {}',
    maxCount = checkHasNumKey('maxCount') ? Math.floor(alarmSrc.maxCount) : null,
    alarmDelayMs = checkHasNumKey('alarmDelayMs') ? alarmSrc.alarmDelayMs : 0,
    noAlarmDelayMs = checkHasNumKey('noAlarmDelayMs') ? alarmSrc.noAlarmDelayMs : 0,
    min,
    max,
    interval = null;

  if (hasExpectedValue) {
    if (hasMinValue || hasMaxValue)
      throw new Error(
        '{}: cannot have both expectedValue and minValue/maxValue'.format(
          JSON.stringify(alarmSrc)
        )
      );
  } else {
    if (!hasMinValue && !hasMaxValue)
      throw new Error(
        '{}: must specify either expectedValue or value range'.format(JSON.stringify(alarmSrc))
      );
    min = hasMinValue ? alarmSrc.minValue : -Infinity;
    max = hasMaxValue ? alarmSrc.maxValue : Infinity;
  }

  if (alarmSrc.hasOwnProperty('interval')) {
    // !(alarmSrc.interval > 0) covers NaN case
    if (typeof alarmSrc.interval != 'number' || !(alarmSrc.interval > 0))
      throw new Error('invalid alarm interval');
    interval = alarmSrc.interval * 1000;
  }

  var d = null;
  function cellValue() {
    if (d === null) d = dev[ref.device];
    return d[ref.control];
  }

  function setAlarmActiveCell(active, title) {
    active = !!active;
    if (dev[alarmDeviceName][cellName] !== active) dev[alarmDeviceName][cellName] = active;
    getDevice(alarmDeviceName).getControl(cellName).setTitle(title);
  }

  var wasActive = false,
    wasTriggered = false,
    intervalId = null,
    remainingCount = null;
  var activateTimerId = null,
    deactivateTimerId = null;

  function stopRepeating() {
    if (intervalId != null) {
      clearInterval(intervalId);
      intervalId = null;
    }
  }

  function notifyAboutActiveAlarm() {
    if (remainingCount === null || remainingCount > 0)
      notify(maybeFormat(alarmMessage, cellValue()));
    if (remainingCount !== null && --remainingCount <= 0) stopRepeating();
  }

  function activateAlarm() {
    setAlarmActiveCell(true, maybeFormat(alarmMessage, cellValue()));

    remainingCount = maxCount;

    notifyAboutActiveAlarm();

    if (interval !== null) intervalId = setInterval(notifyAboutActiveAlarm, interval);

    wasActive = true;
  }

  function deactivateAlarm() {
    setAlarmActiveCell(false, maybeFormat(noAlarmMessage, cellValue()));
    stopRepeating();
    notify(maybeFormat(noAlarmMessage, cellValue()));
    wasActive = false;
  }

  return {
    cellName: cellName,
    defineRules: function () {
      defineRule(namePrefix + 'activate', {
        asSoonAs: hasExpectedValue
          ? function () {
              // log("cv={}; ev={}", JSON.stringify(cellValue()), JSON.stringify(alarmSrc.expectedValue));
              return cellValue() != alarmSrc.expectedValue;
            }
          : function () {
              // log("cv={}; min={}, max={}", JSON.stringify(cellValue()), min, max);
              return cellValue() < min || cellValue() > max;
            },
        then: function () {
          if (wasTriggered) return;

          wasTriggered = true;

          if (!wasActive) {
            if (alarmDelayMs > 0)
              activateTimerId = setTimeout(function () {
                activateTimerId = null;
                activateAlarm();
              }, alarmDelayMs);
            else activateAlarm();
          }

          if (deactivateTimerId != null) {
            clearTimeout(deactivateTimerId);
            deactivateTimerId = null;
          }
        },
      });

      defineRule(namePrefix + 'deactivate', {
        asSoonAs: hasExpectedValue
          ? function () {
              return cellValue() == alarmSrc.expectedValue;
            }
          : function () {
              return cellValue() >= min && cellValue() <= max;
            },
        then: function () {
          // Set 'alarm active' cell to false during the
          // first rule run, too. This will clear any
          // alarms remaining from before wb-rules startup /
          // loading of this rule file.
          if (!wasTriggered) {
            setAlarmActiveCell(false, maybeFormat(noAlarmMessage, cellValue()));
            return;
          }

          wasTriggered = false;

          if (wasActive) {
            if (noAlarmDelayMs > 0) {
              deactivateTimerId = setTimeout(function () {
                deactivateTimerId = null;
                deactivateAlarm();
              }, noAlarmDelayMs);
            } else deactivateAlarm();
          }

          if (activateTimerId != null) {
            clearTimeout(activateTimerId);
            activateTimerId = null;
          }
        },
      });
    },
  };
}

function doLoad(src) {
  if (!Object.prototype.hasOwnProperty.call(src,'deviceName')) throw new Error('deviceName not specified for alarms');

  if (!Object.prototype.hasOwnProperty.call(src,'recipients') || !Array.isArray(src.recipients))
    throw new Error('absent/invalid recipients spec specified for alarms');

  if (!Object.prototype.hasOwnProperty.call(src,'alarms') || !Array.isArray(src.alarms))
    throw new Error('absent/invalid alarms spec');

  var sendFuncs = [];
  src.recipients.forEach(function (recipient) {
    try {
      sendFuncs.push(getSendFunc(recipient));
    } catch (e) {
      log.warning('skipping recipient: {}', e.message || e);
    }
  });
  function notify(text) {
    dev[src.deviceName].log = text;
    sendFuncs.forEach(function (sendFunc) {
      sendFunc.call(null, text);
    });
  }

  var loadedAlarms = src.alarms.map(function (alarmSrc) {
    return loadAlarm(alarmSrc, notify, src.deviceName);
  });

  var deviceDef = {
    cells: {
      log: {
        title: { en: 'Log', ru: 'Лог' },
        type: 'text',
        value: '',
        readonly: true,
      },
    },
  };
  if (Object.prototype.hasOwnProperty.call(src,'deviceTitle')) deviceDef.title = src.deviceTitle;

  loadedAlarms.forEach(function (alarm) {
    deviceDef.cells[alarm.cellName] = {
      type: 'alarm',
      value: false,
      readonly: true,
    };
  });

  defineVirtualDevice(src.deviceName, deviceDef);

  loadedAlarms.forEach(function (alarm) {
    alarm.defineRules();
  });
}

exports.load = function (src) {
  return doLoad(typeof src == 'string' ? readConfig(src) : src);
};
