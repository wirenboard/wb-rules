/* global defineVirtualDevice, defineRule, log, MqttRpc */
// The friendly layer of the MQTT-RPC module: helpers over the raw RPC
// methods, driven by a fake server in the Go test that answers the
// wb-mqtt-serial / wb-mqtt-db / wb-mqtt-logs / wbrules / confed methods.

defineVirtualDevice('rpchelp', {
  title: 'rpc helpers',
  cells: {
    modbus: { type: 'switch', value: false },
    modbusPort: { type: 'switch', value: false },
    devices: { type: 'switch', value: false },
    deviceOps: { type: 'switch', value: false },
    history: { type: 'switch', value: false },
    editors: { type: 'switch', value: false },
    logs: { type: 'switch', value: false },
    state: { type: 'switch', value: false },
    avail: { type: 'switch', value: false },
  },
});

function fail(what, e) {
  log('{} FAILED: {}', what, e && e.stack ? e + '\n' + e.stack : e);
}

defineRule('help_modbus', {
  whenChanged: 'rpchelp/modbus',
  then: async function () {
    try {
      var d = MqttRpc.serial.device('wb-map12e_1');
      var regs = await d.readHolding(0x80, 2);
      log('holding: {}', JSON.stringify(regs));
      var inputs = await d.readInput(0x10);
      log('input: {}', JSON.stringify(inputs));
      var coils = await d.readCoils(0, 10);
      log('coils: {}', JSON.stringify(coils));
      await d.writeHolding(0x20, 4660);
      await d.writeHolding(0x21, [1, 65535]);
      await d.writeCoil(5, true);
      await d.writeCoil(6, [true, false, false, true, true, false, false, false, true]);
      log('writes done');
      try {
        await d.readHolding(0x9999, 1);
        log('exception: none');
      } catch (e) {
        log('exception: {} code={} modbus={}', e.name, e.code, e instanceof MqttRpc.ModbusError);
      }
      var checks = [];
      try {
        await d.raw('0102', 4);
      } catch (e) {
        checks.push(e instanceof TypeError);
      }
      try {
        await d.writeHolding(0, 70000);
      } catch (e) {
        checks.push(e instanceof TypeError);
      }
      try {
        // @ts-expect-error - a port target needs its slaveId (the runtime check is what is tested)
        MqttRpc.serial.device({ port: '/dev/ttyRS485-1' });
      } catch (e) {
        checks.push(e instanceof TypeError);
      }
      log('modbus checks: {}', checks.join(','));
    } catch (e) {
      fail('modbus', e);
    }
  },
});

defineRule('help_modbus_port', {
  whenChanged: 'rpchelp/modbusPort',
  then: async function () {
    try {
      var d = MqttRpc.serial.device({ port: '/dev/ttyRS485-2', slaveId: 7 });
      var regs = await d.readHolding(0x80, 2, { totalTimeout: 3000 });
      log('port holding: {}', JSON.stringify(regs));
      var tcp = MqttRpc.serial.device({ port: '10.0.0.5:502', slaveId: 1 });
      await tcp.readInput(1);
      var rtu = MqttRpc.serial.device({ port: { ip: '10.0.0.6', port: 502 }, slaveId: 2, rtuOverTcp: true });
      await rtu.readInput(1);
      var raw = await d.raw('0a03008000018499', 8);
      log('raw: {}', raw);
      // function 23: read and write in one request
      await d.modbus(23, 0x10, { count: 2, writeAddress: 0x20, writeCount: 1, data: '1234' });
      // a Modbus TCP device: settings go with modbus_mode TCP, the probe with modbus-tcp
      var tcpDev = MqttRpc.serial.device({ port: '10.0.0.5:502', slaveId: 1, deviceType: 'WB-MR6C' });
      await tcpDev.settings();
      await tcpDev.probe();
      await rtu.probe();
      var found = await MqttRpc.serial.probe({ path: '/dev/ttyRS485-2', baudRate: 115200 }, 7);
      log('probe: {}', JSON.stringify(found));
      var nothing = await MqttRpc.serial.probe('/dev/ttyRS485-2', 8);
      log('probe empty: {}', nothing);
      var scan = await MqttRpc.serial.scan('/dev/ttyRS485-2', { mode: 'all' });
      log('scan: {} devices', scan.length);
      try {
        await MqttRpc.serial.scan('/dev/ttyRS485-3');
      } catch (e) {
        log('scan error: {} partial={}', e.message, e.devices.length);
      }
      await MqttRpc.serial.setup('/dev/ttyRS485-2', [{ sn: 4265607, set: { slaveId: 12, parity: 'E' } }]);
      log('setup done');
    } catch (e) {
      fail('modbusPort', e);
    }
  },
});

defineRule('help_devices', {
  whenChanged: 'rpchelp/devices',
  then: async function () {
    try {
      var devices = await MqttRpc.serial.devices();
      log(
        'devices: {}',
        devices
          .map(function (d) {
            var where = 'path' in d.port ? d.port.path : d.port.ip;
            return d.id + ':' + d.type + ':' + d.slaveId + ':' + typeof d.slaveId + ':' + where + ':' + d.enabled;
          })
          .join(' ')
      );
      // the listed device feeds back into a handle
      await MqttRpc.serial.device({ port: devices[0].port, slaveId: devices[0].slaveId }).readHolding(0);
      var types = await MqttRpc.serial.deviceTypes();
      log('types: {}', types.map(function (t) { return t.type + '@' + t.group; }).join(' '));
      var ports = await MqttRpc.serial.ports();
      log('ports: {}', ports.length);
      var info = await MqttRpc.serial.device('wb-map12e_1').firmwareInfo();
      log('fw: {} update={}', info.fw, info.fw_has_update);
    } catch (e) {
      fail('devices', e);
    }
  },
});

defineRule('help_device_ops', {
  whenChanged: 'rpchelp/deviceOps',
  then: async function () {
    try {
      var d = MqttRpc.serial.device('wb-map12e_1');
      var settings = await d.settings({ force: true });
      log('settings: {}', JSON.stringify(settings.parameters));
      var data = await d.read({ channels: ['Urms L1'] });
      log('read: {}', JSON.stringify(data.channels));
      await d.write({ parameters: { baud_rate: 96 } });
      var one = await d.readChannel('Urms L1');
      var many = await d.readChannels('Urms L1', 'Irms L1');
      var param = await d.readParameter('baud_rate');
      log('channel: {} channels: {} parameter: {}', one, JSON.stringify(many), param);
      await d.writeChannel('K1', 1);
      await d.writeChannels({ K1: 0, K2: 1 }, { totalTimeout: 2000 });
      await d.setParameter('in1_mode', 2);
      await d.setParameters({ in2_mode: 3 });
      var order = [];
      var result = await d.withPollingPaused(async function () {
        order.push('inside');
        return 42;
      });
      log('paused result: {} order: {}', result, order.join(','));
      try {
        await d.withPollingPaused(function () {
          throw new Error('boom');
        });
      } catch (e) {
        log('paused rethrow: {}', e.message);
      }
    } catch (e) {
      fail('deviceOps', e);
    }
  },
});

defineRule('help_history', {
  whenChanged: 'rpchelp/history',
  then: async function () {
    try {
      var res = await MqttRpc.db.query('wb-adc/Vin', { last: 3600000, limit: 10 });
      log(
        'query: {} hasMore={} first={} {} {}',
        res.values.length,
        res.hasMore,
        res.values[0].channel,
        res.values[0].value,
        res.values[0].time.toISOString()
      );
      var multi = await MqttRpc.db.query(['wb-adc/Vin', ['wb-adc', 'A1']], {
        since: new Date('2026-08-24T00:00:00Z'),
        until: 1756080000000,
      });
      log(
        'multi: {}',
        multi.values
          .map(function (r) {
            return r.channel + '=' + r.value + '@' + r.time.getTime();
          })
          .join(' ')
      );
      var chans = await MqttRpc.db.channels();
      log('channels: {}', chans.map(function (c) { return c.channel + ':' + c.items + ':' + c.lastTime.toISOString(); }).join(' '));
      var last = await MqttRpc.db.lastValue('wb-adc/Vin');
      log('last: {} {}', last.value, last.time.toISOString());
      var none = await MqttRpc.db.lastValue('nope/x');
      log('last none: {}', none);
      var avg = await MqttRpc.db.average('wb-adc/Vin', { last: 60000 });
      log('avg: {}', avg);
      try {
        await MqttRpc.db.query('wb-adc/Vin', { last: 1000, since: 0 });
      } catch (e) {
        log('last+since: {}', e instanceof TypeError);
      }
      try {
        await MqttRpc.db.query('bad');
      } catch (e) {
        log('bad channel: {}', e instanceof TypeError);
      }
    } catch (e) {
      fail('history', e);
    }
  },
});

defineRule('help_editors', {
  whenChanged: 'rpchelp/editors',
  then: async function () {
    try {
      var files = await MqttRpc.rules.list();
      log('rules: {}', files.map(function (f) { return f.virtualPath; }).join(','));
      var loaded = await MqttRpc.rules.load('a.js');
      log('rule content: {}', loaded.content);
      await MqttRpc.rules.save('a.js', '// new');
      await MqttRpc.rules.disable('a.js');
      await MqttRpc.rules.enable('a.js');
      await MqttRpc.rules.rename('a.js', 'b.js');
      await MqttRpc.rules.remove('b.js');
      var check = await MqttRpc.rules.check('b.ts');
      log('check: {} {}', check.status, check.diags.length);
      var types = await MqttRpc.rules.types();
      log('types: {}', types.length);
      var configs = await MqttRpc.confed.list();
      log('configs: {}', configs.map(function (c) { return c.configPath; }).join(','));
      var updated = await MqttRpc.confed.update('/etc/x.conf', function (content) {
        content.debug = true;
      });
      log('updated: {}', JSON.stringify(updated));
      var replaced = await MqttRpc.confed.update('/etc/x.conf', function () {
        return { replaced: true };
      });
      log('replaced: {}', JSON.stringify(replaced));
    } catch (e) {
      fail('editors', e);
    }
  },
});

defineRule('help_logs', {
  whenChanged: 'rpchelp/logs',
  then: async function () {
    try {
      var entries = await MqttRpc.logs.tail('wb-rules.service', 2);
      log(
        'tail: {}',
        entries
          .map(function (e) {
            return e.time.toISOString() + ' ' + e.level + ' ' + e.msg + ' ' + e.cursor;
          })
          .join(' | ')
      );
      await MqttRpc.logs.read({ since: new Date('2026-08-24T10:00:00Z'), levels: [3], pattern: 'x', caseSensitive: false, cursor: 'c1', direction: 'forward', limit: 5 });
      await MqttRpc.logs.read({ since: new Date('2026-08-24T10:00:00Z'), limit: 3 });
      await MqttRpc.logs.read({ cursor: 'c2', limit: 3 });
      var services = await MqttRpc.logs.services();
      var boots = await MqttRpc.logs.boots();
      log('services: {} boots: {} {}', services.join(','), boots[0].hash, boots[0].start.toISOString());
    } catch (e) {
      fail('logs', e);
    }
  },
});

defineRule('help_state', {
  whenChanged: 'rpchelp/state',
  then: async function () {
    try {
      var progress = [];
      // the very first firmware call of this file, for a device whose failed
      // earlier attempt is still on the retained state topic
      var stages = [];
      await MqttRpc.serial.device({ port: '/dev/ttyRS485-1', slaveId: 5 }).updateFirmware({
        onProgress: function (e) {
          stages.push((e.type === 'component' ? 'cmp' : 'fw') + e.progress);
        },
      });
      log('fw stale-first done: {}', stages.join(','));
      // a recorded error is what a standalone wait reports
      try {
        await MqttRpc.serial.waitForFirmwareUpdate('/dev/ttyRS485-1', 6);
        log('wait recorded error: unexpectedly resolved');
      } catch (e) {
        log('wait recorded error: {}', e.message);
      }
      var found = await MqttRpc.deviceManager.scan({
        port: '/dev/ttyRS485-1',
        type: 'standard',
        onProgress: function (s) {
          progress.push(s.progress);
        },
      });
      log('scan found: {} progress: {}', found.map(function (d) { return d.sn; }).join(','), progress.join(','));
      var state = await MqttRpc.deviceManager.state();
      log('scan state: scanning={}', state.scanning);
      await MqttRpc.serial.updateFirmware('/dev/ttyRS485-1', 3, {
        onProgress: function (e) {
          progress.push('fw' + e.progress);
        },
        stageTimeout: 0, // no components stage in this scenario
      });
      log('fw update done: {}', progress.join(','));
      try {
        await MqttRpc.serial.device({ port: '/dev/ttyRS485-1', slaveId: 4 }).updateFirmware({ stageTimeout: 0 });
      } catch (e) {
        log('fw update failed: {} state={}', e.message, e.state && e.state.progress);
      }
      // the stale error of slave 4 stays on the topic: a new update of it must
      // not be mistaken for that failure
      await MqttRpc.serial.updateFirmware('/dev/ttyRS485-1', 4, { type: 'bootloader', stageTimeout: 0 });
      log('fw retry done');
      await MqttRpc.deviceManager.clearFirmwareError('10.0.0.5:502', 1);
      await MqttRpc.deviceManager.firmwareInfo('10.0.0.5:502', 1);
      var artifact = await MqttRpc.diag.collect({ timeout: 5000 });
      log('diag: {}', artifact.basename);
    } catch (e) {
      fail('state', e);
    }
  },
});

defineRule('help_avail', {
  whenChanged: 'rpchelp/avail',
  then: async function () {
    try {
      var up = await MqttRpc.serial.isAvailable();
      var down = await MqttRpc.dali.isAvailable(200);
      log('available: serial={} dali={}', up, down);
      await MqttRpc.db.waitUntilAvailable(1000);
      log('db available');
      log('tcp target: {}', JSON.stringify(MqttRpc.serial.device({ port: '10.0.0.5:502', slaveId: 1 }).port));
    } catch (e) {
      fail('avail', e);
    }
  },
});
