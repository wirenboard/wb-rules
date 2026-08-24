/* global defineVirtualDevice, defineRule, log, MqttRpc, require */
// MQTT-RPC module coverage: each switch triggers one client scenario; the
// served methods are defined at load time so their presence is announced
// with the file.

defineVirtualDevice('rpctest', {
  title: 'rpc test',
  cells: {
    call: { type: 'switch', value: false },
    err: { type: 'switch', value: false },
    timeout: { type: 'switch', value: false },
    has: { type: 'switch', value: false },
    wait: { type: 'switch', value: false },
    proxy: { type: 'switch', value: false },
    typed: { type: 'switch', value: false },
    same: { type: 'switch', value: false },
    bad: { type: 'switch', value: false },
  },
});

function describeError(e) {
  return (
    'name=' +
    e.name +
    ' code=' +
    e.code +
    ' data=' +
    JSON.stringify(e.data) +
    ' rpc=' +
    (e instanceof MqttRpc.RpcError) +
    ' timeout=' +
    (e instanceof MqttRpc.TimeoutError) +
    ' error=' +
    (e instanceof Error) +
    ' msg=' +
    e.message
  );
}

defineRule('rpc_call', {
  whenChanged: 'rpctest/call',
  then: async function () {
    var r = await MqttRpc.call('svc', 'S', 'M', { a: 1 });
    log('call result: {}', JSON.stringify(r));
    // a second call reuses the subscription and gets the next id
    var r2 = await MqttRpc.call('svc', 'S', 'M');
    log('call result 2: {}', JSON.stringify(r2));
  },
});

defineRule('rpc_err', {
  whenChanged: 'rpctest/err',
  then: async function () {
    try {
      await MqttRpc.call('svc', 'S', 'Err', {});
      log('err: unexpectedly resolved');
    } catch (e) {
      log('err: {}', describeError(e));
    }
  },
});

defineRule('rpc_timeout', {
  whenChanged: 'rpctest/timeout',
  then: async function () {
    try {
      await MqttRpc.call('svc', 'S', 'Slow', {}, { timeout: 500 });
      log('timeout: unexpectedly resolved');
    } catch (e) {
      log('timeout: {}', describeError(e));
    }
  },
});

defineRule('rpc_has', {
  whenChanged: 'rpctest/has',
  then: async function () {
    var present = await MqttRpc.hasMethod('svc', 'S', 'M');
    log('has M: {}', present);
    var again = await MqttRpc.hasMethod('svc', 'S', 'M');
    log('has M again: {}', again);
    var absent = await MqttRpc.hasMethod('svc', 'S', 'Nope', { timeout: 300 });
    log('has Nope: {}', absent);
  },
});

defineRule('rpc_wait', {
  whenChanged: 'rpctest/wait',
  then: async function () {
    await MqttRpc.waitForMethod('svc', 'S', 'Later', 1000);
    log('Later available');
    try {
      await MqttRpc.waitForMethod('svc', 'S', 'Never', 200);
      log('Never: unexpectedly available');
    } catch (e) {
      log('Never: {}', describeError(e));
    }
  },
});

defineRule('rpc_proxy', {
  whenChanged: 'rpctest/proxy',
  then: async function () {
    var s = MqttRpc.service('svc', 'S', ['M']);
    var r = await s.M({ via: 'proxy' });
    log('proxy result: {}', JSON.stringify(r));
    var r2 = await s.call('M', { via: 'call' });
    log('proxy call result: {}', JSON.stringify(r2));
  },
});

defineRule('rpc_typed', {
  whenChanged: 'rpctest/typed',
  then: async function () {
    var v = await MqttRpc.db.history.get_values({ channels: [['wb-adc', 'Vin']], limit: 1 });
    log('typed result: {}', JSON.stringify(v));
    // the serial port budget stretches the client timeout beyond the default
    MqttRpc.serial.port.Load({ path: '/dev/ttyRS485-1', total_timeout: 90000 }).catch(function () {});
  },
});

defineRule('rpc_same', {
  whenChanged: 'rpctest/same',
  then: function () {
    var m = require('wb-mqtt-rpc');
    log('same instance: {}, clientId ok: {}', m === MqttRpc, /^wbrules-[A-Za-z0-9]{10}$/.test(m.clientId));
  },
});

defineRule('rpc_bad', {
  whenChanged: 'rpctest/bad',
  then: function () {
    var checks = [];
    try {
      MqttRpc.call('a/b', 'S', 'M');
    } catch (e) {
      checks.push(e instanceof TypeError);
    }
    try {
      MqttRpc.call('svc', 'S', 'M', 42);
    } catch (e) {
      checks.push(e instanceof TypeError);
    }
    try {
      MqttRpc.defineService('Bad', {});
    } catch (e) {
      checks.push(e instanceof TypeError);
    }
    try {
      MqttRpc.defineService('Bad', { X: 1 });
    } catch (e) {
      checks.push(e instanceof TypeError);
    }
    log('bad args rejected: {}', checks.join(','));
  },
});

// ---- server side ----

MqttRpc.defineService('Demo', {
  Echo: function (params, request) {
    return { params: params, method: request.method, client: request.clientId };
  },
  Fail: function () {
    throw new MqttRpc.RpcError(1234, 'nope', { why: 'test' });
  },
  Boom: function () {
    throw new TypeError('bad handler');
  },
  Later: function (params) {
    return Promise.resolve(params.v * 2);
  },
  Nothing: function () {},
});

MqttRpc.defineService('custom-driver', 'Other', {
  Ping: function () {
    return 'pong';
  },
});
