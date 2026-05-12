// Override Notify used by modules loaded from lib.js.
Notify.sendEmail = function (to, subject, text) {
  log('EMAIL TO: {} SUBJ: {} TEXT: {}', to, subject, text);
};

Notify.sendSMS = function (to, text) {
  log('SMS TO: {} TEXT: {}', to, text);
};

Notify.sendTelegramMessage = function (token, chatId, text) {
  log("TELEGRAM MESSAGE TOKEN: {} CHATID: {} TEXT: {}", token, chatId, text);
};
