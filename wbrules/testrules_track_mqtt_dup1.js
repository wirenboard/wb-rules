/* global trackMqtt, log */

trackMqtt('/weird/sub/some', function (obj) {
  log('tmp1: {}={} (retained: {})'.format(obj.topic, obj.value, obj.retained));
});
