trackMqtt('/wierd/sub/some', function (obj) {
  log('1. wierd topic got value');
  log('topic: {}, value: {}'.format(obj.topic, obj.value));
});

trackMqtt('/wierd/+/some', function (obj) {
  log('2. wierd topic got value');
  log('topic: {}, value: {}'.format(obj.topic, obj.value));
});

trackMqtt('/wierd/+/another', function (obj) {
  log('3. wierd topic got value');
  log('topic: {}, value: {}'.format(obj.topic, obj.value));
});

trackMqtt('/wierd/#', function (obj) {
  log('4. wierd topic got value');
  log('topic: {}, value: {}'.format(obj.topic, obj.value));
});
