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
    nullid: { type: 'switch', value: false },
    waitcall: { type: 'switch', value: false },
    forever: { type: 'switch', value: false },
    redefine: { type: 'switch', value: false },
    validate: { type: 'switch', value: false },
  },
});

defineRule('rpc_forever', {
  whenChanged: 'rpctest/forever',
  then: async function () {
    // Infinity and 0 both mean "no limit": no timer, the reply settles it
    var r = await MqttRpc.call('svc', 'S', 'M', { limit: 'none' }, { timeout: Infinity });
    log('forever result: {}', JSON.stringify(r));
    // the same through the per-file default
    MqttRpc.defaults.timeout = Infinity;
    var r2 = await MqttRpc.call('svc', 'S', 'M', { limit: 'default' });
    MqttRpc.defaults.timeout = 60000;
    log('forever result 2: {}', JSON.stringify(r2));
  },
});

defineRule('rpc_redefine', {
  whenChanged: 'rpctest/redefine',
  then: function () {
    // the same file may redefine a method (a warning, the new handler wins)
    MqttRpc.defineService('Demo', {
      Nothing: function () {
        return 'replaced';
      },
    });
  },
});

defineRule('rpc_nullid', {
  whenChanged: 'rpctest/nullid',
  then: async function () {
    try {
      await MqttRpc.call('svc', 'S', 'Garbled', {});
      log('nullid: unexpectedly resolved');
    } catch (e) {
      log('nullid: {}', describeError(e));
    }
  },
});

defineRule('rpc_waitcall', {
  whenChanged: 'rpctest/waitcall',
  then: async function () {
    var r = await MqttRpc.call('svc', 'S', 'M', { after: 'presence' }, { waitForMethod: 1000 });
    log('waitcall result: {}', JSON.stringify(r));
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
    e.message +
    ' target=' +
    e.driver +
    '/' +
    e.service +
    '/' +
    e.method
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
    var v = await MqttRpc.db.rpc.history.get_values({ channels: [['wb-adc', 'Vin']], limit: 1 });
    log('typed result: {}', JSON.stringify(v));
    // the serial port budget stretches the client timeout beyond the default
    MqttRpc.serial.rpc.port
      .Load({
        path: '/dev/ttyRS485-1',
        baud_rate: 9600,
        parity: 'N',
        data_bits: 8,
        stop_bits: 2,
        msg: '0A03008000018499',
        response_size: 8,
        total_timeout: 90000,
      })
      .catch(function () {});
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
      // @ts-expect-error - params must be an object (the runtime check is what is tested)
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
      MqttRpc.service('svc', 'S', ['call']);
    } catch (e) {
      checks.push(e instanceof TypeError);
    }
    try {
      // @ts-expect-error - waitForMethod is true or a number (the runtime check is what is tested)
      MqttRpc.call('svc', 'S', 'M', {}, { waitForMethod: '5' });
    } catch (e) {
      checks.push(e instanceof TypeError);
    }
    try {
      // @ts-expect-error - a handler must be a function (the runtime check is what is tested)
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
  Circular: function () {
    var o = { name: 'loop' };
    o.self = o;
    return o;
  },
  Func: function () {
    return function () {};
  },
  BadData: function () {
    var o = {};
    o.self = o;
    throw new MqttRpc.RpcError(4321, 'with unserializable data', o);
  },
  // params validated against a JSON Schema before the handler runs
  SetTarget: MqttRpc.method(
    {
      type: 'object',
      properties: {
        room: { type: 'string', minLength: 1 },
        t: { type: 'number', minimum: 5, maximum: 35 },
        mode: { enum: ['eco', 'comfort'] },
        zones: { type: 'array', items: { type: 'integer' }, maxItems: 3 },
      },
      required: ['room', 't'],
      additionalProperties: false,
    },
    function (params) {
      return { room: params.room, t: params.t, mode: params.mode || 'default' };
    }
  ),
  Loose: { params: { type: 'object' }, handler: function (p) { return Object.keys(p).length; } },
  Union: MqttRpc.method(
    { anyOf: [{ type: 'string', pattern: '^[a-z]+$' }, { type: 'integer', multipleOf: 5 }] },
    function (p) {
      return typeof p;
    }
  ),
});

defineRule('rpc_validate', {
  whenChanged: 'rpctest/validate',
  then: function () {
    var out = [];
    var check = function (schema, value) {
      out.push(
        MqttRpc.validate(schema, value)
          .map(function (p) {
            return p.path + ':' + p.message;
          })
          .join(';') || 'ok'
      );
    };
    check({ type: 'number' }, 1);
    check({ type: 'number' }, 'x');
    check({ type: ['number', 'null'] }, null);
    check({ type: 'integer' }, 1.5);
    check({ const: 3 }, 3);
    check({ enum: [1, 'a'] }, 'b');
    check({ type: 'string', maxLength: 2 }, 'abc');
    check({ type: 'array', items: { type: 'boolean' }, minItems: 1 }, []);
    check({ type: 'array', items: { type: 'boolean' } }, [true, 1]);
    check({ oneOf: [{ type: 'number' }, { type: 'integer' }] }, 2);
    check({ not: { type: 'string' } }, 'x');
    check({ allOf: [{ type: 'number' }, { maximum: 1 }] }, 2);
    check({ type: 'object', properties: { a: { type: 'string' } }, additionalProperties: { type: 'number' } }, { a: 'x', b: 'y' });
    check({ type: 'object', exclusiveMaximum: 1, unknownKeyword: true }, {});
    log('validate: {}', out.join(' | '));
  },
});

MqttRpc.defineService('custom-driver', 'Other', {
  Ping: function () {
    return 'pong';
  },
});
