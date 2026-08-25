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
  const viaModule = await same.db.rpc.history.get_channels();

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

  // params validated against a JSON Schema: the handler's params are typed from it
  MqttRpc.defineService("Heating", {
    SetTarget: MqttRpc.method(
      {
        type: "object",
        properties: {
          room: { type: "string" },
          t: { type: "number", minimum: 5, maximum: 35 },
          mode: { enum: ["eco", "comfort"] },
          zones: { type: "array", items: { type: "integer" } },
          flags: { type: ["boolean", "null"] },
        },
        required: ["room", "t"],
        additionalProperties: false,
      },
      (params, request) => {
        const room: string = params.room;
        const t: number = params.t;
        const mode: "eco" | "comfort" | undefined = params.mode;
        const zones: number[] | undefined = params.zones;
        const flags: boolean | null | undefined = params.flags;
        const client: string = request.clientId;
        // @ts-expect-error - t is a number
        const bad: string = params.t;
        // @ts-expect-error - no other properties
        const nope: any = params.other;
        return { ok: true };
      }
    ),
    Loose: { params: { type: "object" }, handler: (p) => p },
    Described: { handler: () => "x", description: "no schema" },
  });
  const problems: MqttRpc.ValidationProblem[] = MqttRpc.validate({ type: "number" }, "x");
  type Target = MqttRpc.FromSchema<{ type: "object"; properties: { a: { type: "string" } }; required: ["a"] }>;
  const target: Target = { a: "x" };
  // @ts-expect-error - a is required
  const missing: Target = {};
}

// ---- the raw RPC layer, exactly as the services document it ----

async function __mqttRpcRawServices() {
  // wb-mqtt-serial
  const raw = await MqttRpc.serial.rpc.port.Load({
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
  const modbus = await MqttRpc.serial.rpc.port.Load({ device_id: "wb-map12e_1", function: 3, address: 0x80, count: 2 });
  if (modbus.exception) {
    const c: number = modbus.exception.code;
  }
  // Modbus TCP with an explicit endpoint
  await MqttRpc.serial.rpc.port.Load({ ip: "192.168.1.50", port: 502, protocol: "modbus-tcp", slave_id: 1, function: 3, address: 0 });
  // port settings held in a variable (widened types) are accepted
  const port = { path: "/dev/ttyRS485-2", baud_rate: 9600, parity: "N", data_bits: 8, stop_bits: 2 };
  await MqttRpc.serial.rpc.port.Scan(port);
  // the module's own results feed back into requests
  const ports = await MqttRpc.serial.rpc.ports.Load();
  const firstPort = ports[0];
  if ("path" in firstPort) {
    await MqttRpc.serial.rpc.port.Scan(firstPort);
  }
  const cfg = await MqttRpc.serial.rpc.config.Load({ lang: "ru" });
  const groups: MqttRpc.Serial.DeviceTypeGroup[] = cfg.types;
  const schema: any = await MqttRpc.serial.rpc.config.GetSchema({ type: "WB-MR6C" });
  const scan = await MqttRpc.serial.rpc.port.Scan({
    path: "/dev/ttyRS485-1",
    baud_rate: 9600,
    parity: "N",
    data_bits: 8,
    stop_bits: 2,
  });
  const sn: string | undefined = scan.devices[0].sn;
  const probed = await MqttRpc.serial.rpc.device.Probe({ ...port, slave_id: 1 });
  const probedSn: string | undefined = probed.sn;
  const found = scan.devices[0];
  if (found.cfg) {
    await MqttRpc.serial.rpc.port.Load({ ...port, protocol: "modbus", slave_id: found.cfg.slave_id, function: 3, address: 0 });
  }
  const info = await MqttRpc.serial.rpc.fwUpdate.GetFirmwareInfo({
    slave_id: 1,
    port: { path: "/dev/ttyRS485-1" },
  });
  const canUpdate: boolean = info.can_update;
  await MqttRpc.serial.rpc.device.LoadConfig({ device_id: "wb-mr6c_1", force: true });
  const loaded = await MqttRpc.serial.rpc.device.Load({ device_id: "wb-mr6c_1", channels: ["Input 1"] });
  const ro: string[] = loaded.readonly;
  await MqttRpc.serial.rpc.device.Set({ device_id: "wb-mr6c_1", channels: { K1: 1 } });
  await MqttRpc.serial.rpc.device.SetPoll({ device_id: "wb-mr6c_1", poll: false });
  await MqttRpc.serial.rpc.templates.Upload({ content: "{}", filename: "my.json" });
  await MqttRpc.serial.rpc.port.Setup({ path: "/dev/ttyRS485-1", items: [{ sn: 4265607, cfg: { slave_id: 12 } }] });
  await MqttRpc.serial.rpc.fwUpdate.Update({ slave_id: 1, port: { path: "/dev/ttyRS485-1" }, type: "bootloader" });
  // @ts-expect-error - a serial port needs its line settings
  await MqttRpc.serial.rpc.port.Load({ path: "/dev/ttyRS485-1", msg: "00", response_size: 1 });
  // @ts-expect-error - a Modbus request needs the function code
  await MqttRpc.serial.rpc.port.Load({ device_id: "x", protocol: "modbus", slave_id: 1, address: 0 });
  // @ts-expect-error - a raw request needs an explicit port, not a configured device
  await MqttRpc.serial.rpc.port.Load({ device_id: "x", msg: "00", response_size: 1 });
  // @ts-expect-error - wb-mqtt-serial flashes over serial ports only
  await MqttRpc.serial.rpc.fwUpdate.GetFirmwareInfo({ slave_id: 1, port: { address: "10.0.0.1", port: 502 } });
  // @ts-expect-error - "components" is not a software type
  await MqttRpc.serial.rpc.fwUpdate.Update({ slave_id: 1, port: { path: "/dev/ttyRS485-1" }, type: "components" });

  // wb-mqtt-db
  const hist = await MqttRpc.db.rpc.history.get_values({
    channels: [["wb-adc", "Vin"]],
    limit: 10,
    ver: 1,
    timestamp: { gt: 0 },
    request_timeout: 20,
  });
  const more: boolean | undefined = hist.has_more;
  // the record layout follows ver: compact fields for 1, verbose for 0/default
  const compact: string = hist.values[0].v;
  const verbose = await MqttRpc.db.rpc.history.get_values({ channels: [["wb-adc", "Vin"]] });
  const device: string = verbose.values[0].device;
  const explicit0 = await MqttRpc.db.rpc.history.get_values({ channels: [["wb-adc", "Vin"]], ver: 0 });
  const ts: number = explicit0.values[0].timestamp;
  // @ts-expect-error - a ver 1 record has no verbose `value`
  const noValue: string = hist.values[0].value;
  const chans = await MqttRpc.db.rpc.history.get_channels();
  const count: number = chans.channels["wb-adc/Vin"].items;
  // @ts-expect-error - channels are [device, control] pairs
  await MqttRpc.db.rpc.history.get_values({ channels: ["wb-adc/Vin"] });
  // @ts-expect-error - channels are required
  await MqttRpc.db.rpc.history.get_values({ limit: 1 });

  // wb-rules
  const list = await MqttRpc.rules.rpc.Editor.List();
  const first: string = list[0].virtualPath;
  const rule = await MqttRpc.rules.rpc.Editor.Load({ path: "rules/a.js" });
  const content: string = rule.content;
  await MqttRpc.rules.rpc.Editor.Save({ path: "rules/a.js", content: "// x" });
  const removed: boolean = await MqttRpc.rules.rpc.Editor.Remove({ path: "rules/a.js" });
  const check = await MqttRpc.rules.rpc.Editor.Check({ path: "rules/a.ts" });
  if (check.status === "ready") {
    const n: number = check.diags.length;
  }
  // @ts-expect-error - Save needs the content
  await MqttRpc.rules.rpc.Editor.Save({ path: "rules/a.js" });

  // wb-mqtt-confed
  const configs = await MqttRpc.confed.rpc.Editor.List();
  const conf = await MqttRpc.confed.rpc.Editor.Load({ path: configs[0].configPath });
  await MqttRpc.confed.rpc.Editor.Save({ path: conf.configPath, content: conf.content });

  // wb-mqtt-logs
  const logsList = await MqttRpc.logs.rpc.logs.List();
  const entries = await MqttRpc.logs.rpc.logs.Load({
    service: "wb-rules.service",
    limit: 20,
    boot: logsList.boots[0].hash,
    "case-sensitive": false,
  });
  const msg: string = entries[0].msg;
  await MqttRpc.logs.rpc.logs.CancelLoad();

  // wb-diag-collect, wb-device-manager
  const started: "Ok" = await MqttRpc.diag.rpc.main.diag();
  const alive: "1" = await MqttRpc.diag.rpc.main.status();
  await MqttRpc.deviceManager.rpc.busScan.Start({ scan_type: "standard", port: { path: "/dev/ttyRS485-1" } });
  await MqttRpc.deviceManager.rpc.busScan.Stop();
  const dm = await MqttRpc.deviceManager.rpc.fwUpdate.GetFirmwareInfo({
    slave_id: 1,
    port: { path: "/dev/ttyRS485-1" },
  });
  // wb-device-manager also flashes over Modbus TCP
  await MqttRpc.deviceManager.rpc.fwUpdate.GetFirmwareInfo({ slave_id: 1, port: { address: "10.0.0.1", port: 502 } });
  // @ts-expect-error - scan_type is one of the known kinds
  await MqttRpc.deviceManager.rpc.busScan.Start({ scan_type: "slow" });

  // wb-mqtt-dali
  const gateways = await MqttRpc.dali.rpc.Editor.GetList();
  const busId: string = gateways[0].buses[0].id;
  const results = await MqttRpc.dali.rpc.Bus.SendCommand({ busId, commands: ["DAPC(A0, 0xFE)"] });
  const status: "ok" | "error" = results[0].status;
  const cmds = await MqttRpc.dali.rpc.Bus.ListCommands();
}

// ---- the helpers ----

async function __mqttRpcHelpers() {
  // availability
  const up: boolean = await MqttRpc.serial.isAvailable();
  await MqttRpc.db.waitUntilAvailable(30000);

  // serial devices: by MQTT id or by port + address
  const meter = MqttRpc.serial.device("wb-map12e_1");
  const regs: number[] = await meter.readHolding(0x80, 2);
  const one: number[] = await meter.readInput(0x10);
  const coils: boolean[] = await meter.readCoils(0, 8, { totalTimeout: 5000 });
  await meter.writeHolding(0x20, 4660);
  await meter.writeHolding(0x21, [1, 2, 3]);
  await meter.writeCoil(5, true);
  await meter.writeCoil(6, [true, false]);
  const hex: string = await meter.modbus(23, 0, { count: 2, data: "00010002" });
  const settings = await meter.settings({ force: true });
  const params: Record<string, any> = settings.parameters;
  const data = await meter.read({ channels: ["Urms L1"] });
  const urms: any = data.channels["Urms L1"];
  await meter.write({ parameters: { baud_rate: 96 } });
  const oneChannel: any = await meter.readChannel("Urms L1");
  const some: Record<string, any> = await meter.readChannels("Urms L1", "Irms L1");
  const listed: Record<string, any> = await meter.readChannels(["Urms L1"], { totalTimeout: 3000 });
  const baud: any = await meter.readParameter("baud_rate");
  await meter.writeChannel("K1", 1);
  await meter.writeChannels({ K1: 0, K2: 1 });
  await meter.setParameter("in1_mode", 2);
  await meter.setParameters({ in2_mode: 3 });
  const rw: string = await meter.modbus(23, 0x10, { count: 2, writeAddress: 0x20, writeCount: 1, data: "1234" });
  await meter.pausePolling();
  await meter.resumePolling();
  const n: number = await meter.withPollingPaused(async (d) => (await d.readHolding(0))[0]);
  const fw = await meter.firmwareInfo();
  const hasUpdate: boolean = fw.fw_has_update;
  await meter.updateFirmware({ type: "bootloader", onProgress: (e) => log("{}% on {}", e.progress, e.port.path) });
  const ok: "Ok" = await meter.updateFirmware({ wait: false });
  const done: void = await meter.updateFirmware();
  const where = await meter.resolve();
  const onPort = MqttRpc.serial.device({ port: "/dev/ttyRS485-1", slaveId: 12, deviceType: "WB-MR6C" });
  const raw: string = await onPort.raw("0a03008000018499", 8);
  const who: MqttRpc.Serial.ScannedDevice | null = await onPort.probe();
  const tcp = MqttRpc.serial.device({ port: { ip: "10.0.0.5", port: 502 }, slaveId: 1, rtuOverTcp: true });
  await tcp.readHolding(0);
  const tcp2 = MqttRpc.serial.device({ port: "10.0.0.5:502", slaveId: 1 });
  // @ts-expect-error - a port target needs its slaveId
  MqttRpc.serial.device({ port: "/dev/ttyRS485-1" });
  // @ts-expect-error - a register value is a number or numbers
  await meter.writeHolding(0, "1");
  try {
    await meter.readHolding(0x9999);
  } catch (e) {
    if (e instanceof MqttRpc.ModbusError) {
      const exc: number = e.code;
    }
  }

  // the config: devices, ports, types, templates
  const devices = await MqttRpc.serial.devices();
  const idOfFirst: string | undefined = devices[0].id;
  const enabled: boolean = devices[0].enabled;
  // a listed device feeds back into a handle
  if (devices[0].slaveId !== undefined) {
    await MqttRpc.serial.device({ port: devices[0].port, slaveId: devices[0].slaveId }).readHolding(0);
  }
  const ports = await MqttRpc.serial.ports();
  const types = await MqttRpc.serial.deviceTypes({ lang: "ru" });
  const group: string = types[0].group;
  const schema: any = await MqttRpc.serial.deviceSchema("WB-MR6C");
  await MqttRpc.serial.uploadTemplate("my.json", { device: {} }, { force: true });
  await MqttRpc.serial.deleteTemplate("MY-TYPE");
  // scanning and setup
  const found: MqttRpc.Serial.ScannedDevice[] = await MqttRpc.serial.scan("/dev/ttyRS485-1", { mode: "all" });
  const probed = await MqttRpc.serial.probe({ path: "/dev/ttyRS485-1", baudRate: 115200 }, 7);
  await MqttRpc.serial.setup("/dev/ttyRS485-1", [{ sn: 4265607, set: { slaveId: 12, parity: "E" } }]);
  await MqttRpc.serial.updateFirmware("/dev/ttyRS485-1", 12, { onProgress: (e) => log("{}", e.progress) });
  const state = await MqttRpc.serial.firmwareUpdateState();
  const flashing: number = state.devices.length;
  // @ts-expect-error - a port is a path, "host:port" or an object
  await MqttRpc.serial.scan(42);

  // history
  const hist = await MqttRpc.db.query("wb-adc/Vin", { last: 3600000, limit: 100 });
  const v: number | string = hist.values[0].value;
  const when: Date = hist.values[0].time;
  const more: boolean = hist.hasMore;
  const multi = await MqttRpc.db.query(["wb-adc/Vin", ["wb-adc", "A1"]], { since: new Date(), until: Date.now() });
  const last = await MqttRpc.db.lastValue(["wb-adc", "Vin"]);
  if (last) {
    const lastTime: Date = last.time;
  }
  const avg: number | undefined = await MqttRpc.db.average("wb-adc/Vin", { since: Date.now() - 60000 });
  const chans = await MqttRpc.db.channels();
  const lastSeen: Date = chans[0].lastTime;
  // @ts-expect-error - a window bound is a Date or milliseconds
  await MqttRpc.db.query("wb-adc/Vin", { since: "yesterday" });

  // editors
  const files = await MqttRpc.rules.list();
  const path: string = files[0].virtualPath;
  const rule = await MqttRpc.rules.load(path);
  await MqttRpc.rules.save(path, rule.content);
  await MqttRpc.rules.disable(path);
  await MqttRpc.rules.enable(path);
  await MqttRpc.rules.rename(path, "b.js");
  await MqttRpc.rules.remove("b.js");
  const verdict = await MqttRpc.rules.check("a.ts", { interval: 500 });
  const diags: MqttRpc.RulesEditor.Diag[] = verdict.diags;
  const dts: string = await MqttRpc.rules.types();
  const configs = await MqttRpc.confed.list();
  const conf = await MqttRpc.confed.load(configs[0].configPath);
  await MqttRpc.confed.save(conf.configPath, conf.content);
  const saved = await MqttRpc.confed.update<{ debug: boolean }>("/etc/wb-rules.conf", (c) => {
    c.debug = true;
  });
  const debug: boolean = saved.debug;

  // logs, diagnostics, scanning, dali
  const tail = await MqttRpc.logs.tail("wb-rules.service", 20);
  const line: string = tail[0].msg;
  const at: Date = tail[0].time;
  const errors = await MqttRpc.logs.read({ levels: [0, 1, 2, 3], since: Date.now() - 3600000, pattern: "panic" });
  const services: string[] = await MqttRpc.logs.services();
  const boots = await MqttRpc.logs.boots();
  const bootStart: Date = boots[0].start;
  const artifact = await MqttRpc.diag.collect({ timeout: 120000 });
  const file: string = artifact.fullname;
  const alive: boolean = await MqttRpc.diag.isAlive();
  const scanned = await MqttRpc.deviceManager.scan({ port: "/dev/ttyRS485-1", type: "extended", onProgress: (s) => log("{}", s.progress) });
  const serial: string | undefined = scanned[0].sn;
  const scanState = await MqttRpc.deviceManager.state();
  if (scanState) {
    const scanning: boolean = scanState.scanning;
  }
  await MqttRpc.deviceManager.updateFirmware("10.0.0.5:502", 1);
  const buses = await MqttRpc.dali.buses();
  const busName: string = buses[0].gateway.name;
  const results = await MqttRpc.dali.send(buses[0].id, "DAPC(A0, 0xFE)");
  const st: "ok" | "error" = results[0].status;
}
