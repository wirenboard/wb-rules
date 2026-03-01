trackMqtt('/test/solo', function (obj) {
  log('solo_v2: topic={}, value={}'.format(obj.topic, obj.value));
});
