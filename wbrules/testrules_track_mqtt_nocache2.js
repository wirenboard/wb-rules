/* global trackMqtt, log */
// joins the pattern testrules_track_mqtt_nocache.js subscribed without a
// cache: it must not be handed a replay of the earlier message
trackMqtt('/nocache/+', function (obj) {
  log('nocache B: {} = {} retained={}', obj.topic, obj.value, obj.retained);
});
