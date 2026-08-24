// Compile-time assertions for the MqttRpc declarations in types/wb-rules.d.ts
// (same conventions as testrules_ts_typeassert.ts: zero diagnostics expected,
// negatives marked @ts-expect-error, nothing here ever runs, valid under both
// --strict false and --strict true).

async function __mqttRpcGeneric() {
  const untyped: any = await MqttRpc.call("wb-mqtt-serial", "ports", "Load");
  const typed = await MqttRpc.call<{ ok: boolean }>(
    "d",
    "s",
    "m",
    { a: 1 },
    { timeout: 1000, waitForMethod: true }
  );
  const ok: boolean = typed.ok;
  const present: boolean = await MqttRpc.hasMethod("d", "s", "m", { timeout: 500 });
  const present1: boolean = await MqttRpc.hasMethod("d", "s", "m", 500);
  await MqttRpc.waitForMethod("d", "s", "m", 0);
  await MqttRpc.waitForMethod("d", "s", "m", { timeout: 1000 });
  await MqttRpc.call("d", "s", "m", {}, { waitForMethod: 5000 });

  // a proxy carries the listed methods, and call() for the rest
  const editor = MqttRpc.service("wbrules", "Editor", ["List", "Load"]);
  const files: any = await editor.List();
  await editor.Load({ path: "x.js" });
  await editor.call("Remove", { path: "x.js" });
  const present2: boolean = await editor.hasMethod("Check");
  const plain = MqttRpc.service("d", "s");
  await plain.call("m");
  // a typed proxy for a service the module does not know
  type MyApi = { Get: MqttRpc.Method<{ key: string }, { value: number }>; Ping: MqttRpc.Method<{}, "pong"> };
  const mine = MqttRpc.service<MyApi>("my-driver", "Store", ["Get", "Ping"]);
  const value: number = (await mine.Get({ key: "k" })).value;
  const pong: "pong" = await mine.Ping();
  // @ts-expect-error - Get needs its key
  await mine.Get({});
  // @ts-expect-error - a method that was not listed is not a property
  await editor.Remove({ path: "x.js" });
  // @ts-expect-error - params must be an object
  await MqttRpc.call("d", "s", "m", 42);
  // @ts-expect-error - timeout is a number of milliseconds
  await MqttRpc.call("d", "s", "m", {}, { timeout: "1s" });

  // the module and the global are the same thing
  const same: typeof MqttRpc = require("wb-mqtt-rpc");
  const viaModule = await same.db.history.get_channels();

  const err = new MqttRpc.RpcError(-32000, "x", { why: 1 });
  const code: number = err.code;
  const target: string | undefined = err.method;
  const timedOut: boolean = err instanceof MqttRpc.TimeoutError;
  const t: -33000 = MqttRpc.ErrorCode.TIMEOUT;
  MqttRpc.defaults.timeout = 5000;
  const id: string = MqttRpc.clientId;
  const driver: "wbrules-scripts" = MqttRpc.DEFAULT_SERVICE_DRIVER;
}

function __mqttRpcServer() {
  const def = MqttRpc.defineService("Demo", {
    Echo: (params, request) => ({ params, from: request.clientId, id: request.id }),
    Later: async (params: { v: number }) => params.v * 2,
    Fail: () => {
      throw new MqttRpc.RpcError(1, "no");
    },
  });
  const names: string[] = def.methods;
  MqttRpc.defineService("my-driver", "Other", { Ping: () => "pong" });
  // @ts-expect-error - a handler must be a function
  MqttRpc.defineService("Demo", { X: 1 });
}

async function __mqttRpcServices() {
  // wb-mqtt-serial
  const raw = await MqttRpc.serial.port.Load({
    path: "/dev/ttyRS485-1",
    baud_rate: 9600,
    parity: "N",
    data_bits: 8,
    stop_bits: 2,
    msg: "0A03008000018499",
    response_size: 8,
    format: "HEX",
    total_timeout: 10000,
  });
  const resp: string | undefined = raw.response;
  // a configured device: port, protocol and address come from the config
  const modbus = await MqttRpc.serial.port.Load({ device_id: "wb-map12e_1", function: 3, address: 0x80, count: 2 });
  if (modbus.exception) {
    const c: number = modbus.exception.code;
  }
  // Modbus TCP with an explicit endpoint
  await MqttRpc.serial.port.Load({ ip: "192.168.1.50", port: 502, protocol: "modbus-tcp", slave_id: 1, function: 3, address: 0 });
  // port settings held in a variable (widened types) are accepted
  const port = { path: "/dev/ttyRS485-2", baud_rate: 9600, parity: "N", data_bits: 8, stop_bits: 2 };
  await MqttRpc.serial.port.Scan(port);
  // the module's own results feed back into requests
  const ports = await MqttRpc.serial.ports.Load();
  const firstPort = ports[0];
  if ("path" in firstPort) {
    await MqttRpc.serial.port.Scan(firstPort);
  }
  const cfg = await MqttRpc.serial.config.Load({ lang: "ru" });
  const groups: MqttRpc.Serial.DeviceTypeGroup[] = cfg.types;
  const schema: any = await MqttRpc.serial.config.GetSchema({ type: "WB-MR6C" });
  const scan = await MqttRpc.serial.port.Scan({
    path: "/dev/ttyRS485-1",
    baud_rate: 9600,
    parity: "N",
    data_bits: 8,
    stop_bits: 2,
  });
  const sn: string | undefined = scan.devices[0].sn;
  const probed = await MqttRpc.serial.device.Probe({ ...port, slave_id: 1 });
  const probedSn: string | undefined = probed.sn;
  const found = scan.devices[0];
  if (found.cfg) {
    await MqttRpc.serial.port.Load({ ...port, protocol: "modbus", slave_id: found.cfg.slave_id, function: 3, address: 0 });
  }
  const info = await MqttRpc.serial.fwUpdate.GetFirmwareInfo({
    slave_id: 1,
    port: { path: "/dev/ttyRS485-1" },
  });
  const canUpdate: boolean = info.can_update;
  await MqttRpc.serial.device.LoadConfig({ device_id: "wb-mr6c_1", force: true });
  const loaded = await MqttRpc.serial.device.Load({ device_id: "wb-mr6c_1", channels: ["Input 1"] });
  const ro: string[] = loaded.readonly;
  await MqttRpc.serial.device.Set({ device_id: "wb-mr6c_1", channels: { K1: 1 } });
  await MqttRpc.serial.device.SetPoll({ device_id: "wb-mr6c_1", poll: false });
  await MqttRpc.serial.templates.Upload({ content: "{}", filename: "my.json" });
  await MqttRpc.serial.port.Setup({ path: "/dev/ttyRS485-1", items: [{ sn: 4265607, cfg: { slave_id: 12 } }] });
  await MqttRpc.serial.fwUpdate.Update({ slave_id: 1, port: { path: "/dev/ttyRS485-1" }, type: "bootloader" });
  // @ts-expect-error - a serial port needs its line settings
  await MqttRpc.serial.port.Load({ path: "/dev/ttyRS485-1", msg: "00", response_size: 1 });
  // @ts-expect-error - a Modbus request needs the function code
  await MqttRpc.serial.port.Load({ device_id: "x", protocol: "modbus", slave_id: 1, address: 0 });
  // @ts-expect-error - a raw request needs an explicit port, not a configured device
  await MqttRpc.serial.port.Load({ device_id: "x", msg: "00", response_size: 1 });
  // @ts-expect-error - wb-mqtt-serial flashes over serial ports only
  await MqttRpc.serial.fwUpdate.GetFirmwareInfo({ slave_id: 1, port: { address: "10.0.0.1", port: 502 } });
  // @ts-expect-error - "components" is not a software type
  await MqttRpc.serial.fwUpdate.Update({ slave_id: 1, port: { path: "/dev/ttyRS485-1" }, type: "components" });

  // wb-mqtt-db
  const hist = await MqttRpc.db.history.get_values({
    channels: [["wb-adc", "Vin"]],
    limit: 10,
    ver: 1,
    timestamp: { gt: 0 },
    request_timeout: 20,
  });
  const more: boolean | undefined = hist.has_more;
  // the record layout follows ver: compact fields for 1, verbose for 0/default
  const compact: string = hist.values[0].v;
  const verbose = await MqttRpc.db.history.get_values({ channels: [["wb-adc", "Vin"]] });
  const device: string = verbose.values[0].device;
  const explicit0 = await MqttRpc.db.history.get_values({ channels: [["wb-adc", "Vin"]], ver: 0 });
  const ts: number = explicit0.values[0].timestamp;
  // @ts-expect-error - a ver 1 record has no verbose `value`
  const noValue: string = hist.values[0].value;
  const chans = await MqttRpc.db.history.get_channels();
  const count: number = chans.channels["wb-adc/Vin"].items;
  // @ts-expect-error - channels are [device, control] pairs
  await MqttRpc.db.history.get_values({ channels: ["wb-adc/Vin"] });
  // @ts-expect-error - channels are required
  await MqttRpc.db.history.get_values({ limit: 1 });

  // wb-rules
  const list = await MqttRpc.rules.Editor.List();
  const first: string = list[0].virtualPath;
  const rule = await MqttRpc.rules.Editor.Load({ path: "rules/a.js" });
  const content: string = rule.content;
  await MqttRpc.rules.Editor.Save({ path: "rules/a.js", content: "// x" });
  const removed: boolean = await MqttRpc.rules.Editor.Remove({ path: "rules/a.js" });
  const check = await MqttRpc.rules.Editor.Check({ path: "rules/a.ts" });
  if (check.status === "ready") {
    const n: number = check.diags.length;
  }
  // @ts-expect-error - Save needs the content
  await MqttRpc.rules.Editor.Save({ path: "rules/a.js" });

  // wb-mqtt-confed
  const configs = await MqttRpc.confed.Editor.List();
  const conf = await MqttRpc.confed.Editor.Load({ path: configs[0].configPath });
  await MqttRpc.confed.Editor.Save({ path: conf.configPath, content: conf.content });

  // wb-mqtt-logs
  const logsList = await MqttRpc.logs.logs.List();
  const entries = await MqttRpc.logs.logs.Load({
    service: "wb-rules.service",
    limit: 20,
    boot: logsList.boots[0].hash,
    "case-sensitive": false,
  });
  const msg: string = entries[0].msg;
  await MqttRpc.logs.logs.CancelLoad();

  // wb-diag-collect, wb-device-manager
  const started: "Ok" = await MqttRpc.diag.main.diag();
  const alive: "1" = await MqttRpc.diag.main.status();
  await MqttRpc.deviceManager.busScan.Start({ scan_type: "standard", port: { path: "/dev/ttyRS485-1" } });
  await MqttRpc.deviceManager.busScan.Stop();
  const dm = await MqttRpc.deviceManager.fwUpdate.GetFirmwareInfo({
    slave_id: 1,
    port: { path: "/dev/ttyRS485-1" },
  });
  // wb-device-manager also flashes over Modbus TCP
  await MqttRpc.deviceManager.fwUpdate.GetFirmwareInfo({ slave_id: 1, port: { address: "10.0.0.1", port: 502 } });
  // @ts-expect-error - scan_type is one of the known kinds
  await MqttRpc.deviceManager.busScan.Start({ scan_type: "slow" });

  // wb-mqtt-dali
  const gateways = await MqttRpc.dali.Editor.GetList();
  const busId: string = gateways[0].buses[0].id;
  const results = await MqttRpc.dali.Bus.SendCommand({ busId, commands: ["DAPC(A0, 0xFE)"] });
  const status: "ok" | "error" = results[0].status;
  const cmds = await MqttRpc.dali.Bus.ListCommands();
}
