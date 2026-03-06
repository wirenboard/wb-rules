/* global trackMqtt, log */
trackMqtt('/test/solo', function (obj) {
  log('solo: topic={}, value={}'.format(obj.topic, obj.value));
});
