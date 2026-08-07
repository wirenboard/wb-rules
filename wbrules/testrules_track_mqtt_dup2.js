/* global trackMqtt, log */

trackMqtt('/wierd/sub/some', function (obj) {
  log('tmp2: {}={} (retained: {})'.format(obj.topic, obj.value, obj.retained));
});
