trackMqtt('/test/reload', function (obj) {
  log('script1: topic={}, value={}'.format(obj.topic, obj.value));
});
