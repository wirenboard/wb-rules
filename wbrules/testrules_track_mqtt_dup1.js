/* global trackMqtt, log */

trackMqtt('/wierd/sub/some', function (obj) {
  log('tmp1: {}={} (retained: {})'.format(obj.topic, obj.value, obj.retained));
});
