trackMqtt('/tracker/topic', function (obj) {
  log('tracker2_v2: topic={}, value={}'.format(obj.topic, obj.value));
});
