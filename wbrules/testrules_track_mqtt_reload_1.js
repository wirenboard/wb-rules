trackMqtt('/tracker/topic', function (obj) {
  log('tracker1: topic={}, value={}'.format(obj.topic, obj.value));
});
