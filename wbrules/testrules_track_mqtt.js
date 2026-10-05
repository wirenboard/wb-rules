/* global trackMqtt, log */

trackMqtt('/weird/sub/some', function (obj) {
  log('1. weird topic got value');
  log('topic: {}, value: {}, retained: {}, qos: {}'.format(obj.topic, obj.value, obj.retained, obj.qos));
});

trackMqtt('/weird/+/some', function (obj) {
  log('2. weird topic got value');
  log('topic: {}, value: {}, retained: {}, qos: {}'.format(obj.topic, obj.value, obj.retained, obj.qos));
});

trackMqtt('/weird/+/another', function (obj) {
  log('3. weird topic got value');
  log('topic: {}, value: {}, retained: {}, qos: {}'.format(obj.topic, obj.value, obj.retained, obj.qos));
});

trackMqtt('/weird/#', function (obj) {
  log('4. weird topic got value');
  log('topic: {}, value: {}, retained: {}, qos: {}'.format(obj.topic, obj.value, obj.retained, obj.qos));
});
