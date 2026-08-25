/* global trackMqtt, log */
// a tracker that opts out of the last-value replay cache
trackMqtt(
  '/nocache/+',
  function (obj) {
    log('nocache A: {} = {} retained={}', obj.topic, obj.value, obj.retained);
  },
  { cache: false }
);
