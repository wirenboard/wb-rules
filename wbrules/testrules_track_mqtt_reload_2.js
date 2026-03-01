/* global trackMqtt, log */
trackMqtt('/test/reload', function (obj) {
  log('script2: topic={}, value={}'.format(obj.topic, obj.value));
});
