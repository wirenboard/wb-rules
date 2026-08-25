/* global MqttRpc */
// a second file trying to serve a driver/service another file already
// serves: refused at load, so a request never gets two answers
MqttRpc.defineService('Demo', {
  Echo: function (params) {
    return params;
  },
});
