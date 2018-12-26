// this stubs out the default alarm object
global.Notify = {
  sendEmail: function sendEmail (to, subject, text) {
    log("EMAIL TO: {} SUBJ: {} TEXT: {}", to, subject, text);
  },

  sendSMS: function sendSMS (to, text) {
    log("SMS TO: {} TEXT: {}", to, text);
  }
};
