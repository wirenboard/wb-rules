// A plain .js rule using the MQTT-RPC module: require('wb-mqtt-rpc') resolves
// to the module's declarations (an exact ambient module beats the wildcard),
// so its calls are type-checked like the MqttRpc global's. Never executed.
function __rpcCheck() {
  var rpc = require('wb-mqtt-rpc'); // line 5: a known module, typed
  rpc.db.rpc.history.get_channels().then(function (r) { log(r.channels); }); // line 6: fine
  MqttRpc.rules.rpc.Editor.List().then(function (files) { log(files[0].virtualPath); }); // line 7: fine
  rpc.db.rpc.history.get_values({ channels: 'wb-adc/Vin' }); // line 8: flagged - channels are pairs
  MqttRpc.call('d', 's', 'm', 42); // line 9: flagged - params must be an object
}
