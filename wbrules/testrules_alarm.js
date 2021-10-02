// this stubs out the default alarm object
global.__proto__.Notify = {
  sendEmail: function sendEmail (to, subject, text) {
    log("EMAIL TO: {} SUBJ: {} TEXT: {}", to, subject, text);
  },
  
  sendTelegram: function sendTelegramMessage (token, chatId, text) {
    log("TELEGRAM MESSAGE TOKEN: {} CHATID: {} TEXT: {}", token, chatId, text);
  },  

  sendSMS: function sendSMS (to, text) {
    log("SMS TO: {} TEXT: {}", to, text);
  }
};
