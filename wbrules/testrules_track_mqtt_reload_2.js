trackMqtt('/tracker/topic', function (obj) {
  log('tracker2: topic={}, value={}'.format(obj.topic, obj.value));
});
