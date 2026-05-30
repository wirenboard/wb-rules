trackMqtt('/wierd/sub/some', function (obj) {
  log('1. wierd topic got value');
  log('topic: {}, value: {}, retained: {}, qos: {}'.format(obj.topic, obj.value, obj.retained, obj.qos));
});

trackMqtt('/wierd/+/some', function (obj) {
  log('2. wierd topic got value');
  log('topic: {}, value: {}, retained: {}, qos: {}'.format(obj.topic, obj.value, obj.retained, obj.qos));
});

trackMqtt('/wierd/+/another', function (obj) {
  log('3. wierd topic got value');
  log('topic: {}, value: {}, retained: {}, qos: {}'.format(obj.topic, obj.value, obj.retained, obj.qos));
});

trackMqtt('/wierd/#', function (obj) {
  log('4. wierd topic got value');
  log('topic: {}, value: {}, retained: {}, qos: {}'.format(obj.topic, obj.value, obj.retained, obj.qos));
});
