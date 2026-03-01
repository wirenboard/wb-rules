trackMqtt('/test/reload', function (obj) {
  log('script1_v2: topic={}, value={}'.format(obj.topic, obj.value));
});
