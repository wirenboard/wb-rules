// Compile-time assertions for types/wb-rules.d.ts.
//
// Loaded by RuleTypeScriptSuite: the background check must report ZERO
// diagnostics for this file. Positive cases assert that idiomatic rule
// code type-checks; negative cases are marked @ts-expect-error, which
// itself becomes a diagnostic if the marked line stops being an error.
//
// Everything lives in never-called functions so loading the file executes
// nothing. Keep every assertion valid under BOTH `--strict false` (the
// engine's background check and the homeui editor) and `--strict true`
// (external IDEs; TestTypeAssertionsFixtureStrict pins it).

// Populate the reference registry the way homeui's editor does from the
// live device list, to pin the typed behavior of the stringly-referenced
// APIs. The shipped registry is empty (everything stays loose); this
// augmentation is local to this file's check. The check runs with
// moduleDetection:force (so top-level await is allowed), which makes this
// file a module, so the augmentation goes through `declare global`.
declare global {
  interface WbControls {
    "assert_reg/level": "value"; // -> number
    "assert_reg/lamp": "switch"; // -> boolean
  }
}

function __registryTypedRefs() {
  // registered references are typed on both read and write; getControl is
  // control-or-undefined even for registered references (the control can be
  // gone by the time the rule runs), hence the assertions/guards
  const lvl: number = dev["assert_reg/level"];
  dev["assert_reg/lamp"] = true;
  getControl("assert_reg/level")!.setValue(5);
  const lit: boolean = getControl("assert_reg/lamp")!.getValue();
  const maybeLvl: VirtualDeviceControl<"value"> | undefined = getControl("assert_reg/level");

  // @ts-expect-error - registered numeric control rejects a string write
  getControl("assert_reg/level")!.setValue("nope");
  // @ts-expect-error - registered switch rejects a numeric assignment
  dev["assert_reg/lamp"] = 5;
  // @ts-expect-error - registered numeric control read is not a string
  const bad: string = dev["assert_reg/level"];

  // unregistered references stay loose (any), as before
  dev["whatever/thing"] = "anything";
  const loose = getControl("whatever/thing"); // VirtualDeviceControl | undefined
  // nested and #meta forms are always loose
  const nested = dev["assert_reg"]["level"];
  const meta = dev["assert_reg/level#type"];

  return [lvl, lit, bad, loose, nested, meta, maybeLvl];
}

function __typedVirtualDevices() {
  const dv = defineVirtualDevice("ts_assert", {
    title: { en: "Assertions", ru: "Проверки" },
    cells: {
      temperature: { type: "temperature", value: 21.5 },
      enabled: { type: "switch", value: false },
      mode: {
        type: "value",
        value: 0,
        units: "mode",
        precision: 0,
        min: 0,
        max: 2,
        enum: { 0: { en: "Off" }, 1: { en: "Eco" }, 2: { en: "Boost" } },
      },
      level: { type: "range", value: 128, min: 0, max: 255 },
      note: { type: "text", value: "", enum: { day: { en: "Day" } } },
      fire: { type: "pushbutton" }, // pushbutton is the one type without a value
    },
  });

  // control types flow from the declaration to getControl(); the result is
  // control-or-undefined even for declared controls (removeControl can take
  // them away at run time), hence the assertions
  const t: number = dv.getControl("temperature")!.getValue();
  const e: boolean = dv.getControl("enabled")!.getValue();
  dv.getControl("enabled")!.setValue(true);
  dv.getControl("note")!.setValue({ value: "hi", notify: false });
  const maybeSwitch: VirtualDeviceControl<"switch"> | undefined = dv.getControl("enabled");

  // @ts-expect-error - temperature values are numbers, not booleans
  const wrongT: boolean = dv.getControl("temperature")!.getValue();
  // @ts-expect-error - a switch takes booleans
  dv.getControl("enabled")!.setValue(7);

  // controls not present in the declaration fall back to the plain
  // union and may be missing - guard first
  const unknownCtl: CellValue | undefined = dv.getControl("elsewhere")?.getValue();

  return [t, e, unknownCtl, maybeSwitch];
}

// Vendor/custom control types outside TypeMappings: the runtime accepts
// them (fillControlArgs passes the type string through) and treats their
// values as strings, so the declarations must accept them too - without
// weakening the discriminated-union errors for the known types above.
function __customControlTypes() {
  const vd = defineVirtualDevice("vendor_dev", {
    cells: {
      mode: { type: "fan_mode", value: "auto" },
    },
  });

  // a custom-typed control reads and writes strings
  const m: string = vd.getControl("mode")!.getValue();
  vd.getControl("mode")!.setValue("eco");
  vd.setCellValue("mode", "boost");
  const mv: string = vd.getCellValue("mode");
  // getType stays a plain string for custom types (the declaration-side
  // literal widens through inference); setType takes vendor strings exactly
  // as the runtime does
  const tt: string = vd.getControl("mode")!.getType();
  vd.getControl("mode")!.setType("fan_mode_v2");
  vd.getControl("mode")!.setType("switch");

  // @ts-expect-error - a custom-typed value is a string, not a number
  vd.getControl("mode")!.setValue(3);
  // @ts-expect-error - the per-type extras stay illegal on custom types
  defineVirtualDevice("vendor_bad", { cells: { m: { type: "fan_mode", value: "x", min: 0 } } });
  // @ts-expect-error - a custom type still requires a value
  defineVirtualDevice("vendor_bad2", { cells: { m: { type: "fan_mode" } } });

  return [m, mv, tt];
}

function __controlOptionValidation() {
  defineVirtualDevice("opts", {
    cells: {
      // @ts-expect-error - min is only legal on value/range controls
      bad1: { type: "switch", value: false, min: 0 },
      // @ts-expect-error - a switch value is boolean, not number
      bad2: { type: "switch", value: 1 },
      // @ts-expect-error - value is required for non-pushbutton controls
      bad3: { type: "value" },
      // @ts-expect-error - precision is not legal on text controls
      bad4: { type: "text", value: "x", precision: 2 },
      // @ts-expect-error - the engine rejects writeable; use readonly
      bad5: { type: "value", value: 0, writeable: false },
      // @ts-expect-error - enum titles must be per-language maps
      bad6: { type: "value", value: 0, enum: { 0: "Off" } },
      // @ts-expect-error - units is only legal on value controls
      bad7: { type: "range", value: 0, units: "%" },
      good: { type: "wo-switch", value: false, readonly: false, order: 1 },
    },
  });

  // the same vocabulary is available as named per-type options
  const sw: SwitchControlOptions = { type: "switch", value: true };
  const rg: ControlOptionsOfType<"range"> = { type: "range", value: 0, max: 100 };
  return [sw, rg];
}

function __ruleHandles() {
  const id = defineRule("assert-rule", {
    whenChanged: "ts_assert/enabled",
    then: (newValue, devName, cellName) => {
      log("changed: {} {}/{}", newValue, devName, cellName);
    },
  });
  const anon = defineRule({
    when: cron("@hourly"),
    then: async () => {
      await sleep(100);
    },
  });

  disableRule(id);
  enableRule(id);
  runRule(anon);

  // @ts-expect-error - rule names are not rule ids
  disableRule("assert-rule");
  // @ts-expect-error - arbitrary numbers are not rule ids
  enableRule(42);

  // a RuleId is still a number where one is needed
  const asNumber: number = id;
  return asNumber;
}

async function __asyncPrimitives() {
  // an UNregistered reference stays loose: value is `any`, so `+ 1` is fine
  // (ts_demo/temperature is not in this fixture's WbControls augmentation)
  let value = await changed("ts_demo/temperature");
  log(`got ${value}`);
  dev["ts_demo/new_temperature"] = value + 1;

  // a control listed in WbControls types changed() from the registry, the
  // same way getControl()/dev[] are typed
  const lvlC: number = await changed("assert_reg/level");
  const lampC: boolean = await changed("assert_reg/lamp");
  // @ts-expect-error - a registered numeric changed() is not a boolean
  const lvlBad: boolean = await changed("assert_reg/level");
  // @ts-expect-error - a registered switch changed() is not a number
  const lampBad: number = await changed("assert_reg/lamp");

  // opt-in typing via the type argument still overrides the registry
  const typed: number = await changed<number>("ts_demo/temperature", 5000);
  // @ts-expect-error - only cell value types can be requested
  changed<Date>("ts_demo/temperature");

  const msg = await nextMqtt("zigbee2mqtt/#", 1000);
  const topic: string = msg.topic;
  const retained: boolean = msg.retained;

  const r = await runShellCommand("wb-gen-serial -u", { captureOutput: true });
  const code: number = r.exitCode;
  await spawn("logger", ["hello"], (exitCode) => log("exit {}", exitCode));

  return [value, lvlC, lampC, lvlBad, lampBad, typed, topic, retained, code];
}

function __devicesAndControlsLookup() {
  // getDevice/getControl return undefined for missing ids - guard first
  const d = getDevice("ts_assert");
  if (d) {
    const err: string = d.getError();
    d.setError("");
    log(d.getCellId("enabled"), err);
  }
  const c = getControl("ts_assert/enabled");
  if (c) {
    c.setReadonly(false);
    const title: string = c.getTitle("ru");
    const prec: number = c.getPrecision();
    log("{} {}", title, prec);
  }
}

function __loggingAndFormatting() {
  log("power: {} W", 42);
  log({ raw: true });
  log.info(3.14);
  log.warning("careful: {}", "reason");
  log.error(new Error("boom"));
  debug("only when debugging: {}", 1);
  const s: string = format("a {} b {}", 1, true);
  const f: string = "x = {}".format(7);
  return [s, f];
}

function __notifyAndAlarms() {
  Notify.sendEmail("user@example.com", "subject", "body");
  Notify.sendSMS("+70000000000", "text", (err) => log(err === null));
  Notify.sendSMS("+70000000000", "text", "gammu-smsd-inject");
  Notify.sendTelegramMessage("token", "@channel", "text", { parseMode: "HTML" });
  Notify.sendWebhook({ url: "https://example.com/hook", body: { a: 1 } });
  // @ts-expect-error - a webhook needs a url
  Notify.sendWebhook({ method: "POST" });

  Alarms.load("/etc/wb-rules/alarms.conf");
  Alarms.load({
    deviceName: "alarms",
    recipients: [
      { type: "email", to: "user@example.com" },
      { type: "telegram", token: "t", chatId: "c" },
    ],
    alarms: [
      { cell: "climate/temperature", minValue: 5, maxValue: 35, interval: 600 },
      { cell: "door/open", expectedValue: false },
    ],
  });
  Alarms.load({
    deviceName: "bad",
    recipients: [
      // @ts-expect-error - a telegram recipient needs a chatId
      { type: "telegram", token: "t" },
    ],
    alarms: [
      // @ts-expect-error - an alarm needs expectedValue or a min/max bound
      { cell: "climate/temperature" },
    ],
  });
}

function __storageAndConfig() {
  const stats = PersistentStorage<{ count: number; last: string }>("stats", { global: true });
  stats.count = (stats.count || 0) + 1;
  const untyped = PersistentStorage("plain");
  untyped.anything = 1;

  const cfg = readConfig("/etc/wb-rules/settings.conf", { logErrorOnNoFile: false });
  const tracked = StorableObject({ n: 1 });
  tracked.n = 2;

  // both PersistentStorage and StorableObject are also constructible: the
  // shipped system rules write `new PersistentStorage(...)`, and the runtime
  // recommends `new StorableObject(obj)`
  const built = new PersistentStorage<{ hits: number }>("built", { global: true });
  built.hits = 1;
  // @ts-expect-error - typed shape is enforced through `new` too
  built.hits = "one";
  const builtTracked = new StorableObject({ n: 1 });
  builtTracked.n = 2;

  // require() stays loose (any); wb-rules modules resolve at runtime through
  // its own module dirs the checker cannot see, so no "Cannot find module"
  const helper = require("some-helper.mod");
  helper.anything(1, "two");

  return [stats.last, cfg, module.filename, __filename];
}

// Idioms from existing rule files that the checker must accept as they are.
function __legacyIdioms() {
  // a concise body returns the assignment; a body may return a value; an
  // async body returns a promise - the return value of `then` is ignored
  const r1 = defineRule("concise", { whenChanged: "assert_reg/lamp", then: (v) => (dev["assert_reg/lamp"] = !!v) });
  const r2 = defineRule("returns", { whenChanged: "assert_reg/lamp", then: function () { return 5; } });
  const r3 = defineRule("async", { whenChanged: "assert_reg/lamp", then: async function () { await sleep(1); } });
  // per-rule state kept on `this` inside the body
  const r4 = defineRule("state", {
    whenChanged: "assert_reg/lamp",
    then: function () { this.count = (this.count || 0) + 1; },
  });
  // rule modules export through module.exports or exports
  module.exports = { a: 1 };
  exports.b = 2;
  // a tracked object may grow new properties later (sloppy JS style)
  const so = new StorableObject({ a: 1 });
  so.b = 2;
  // the virtual device helpers from lib.js and the deprecated getDeviceId
  const vd = defineVirtualDevice("legacy_vd", { cells: { level: { type: "value", value: 0 } } });
  const lvl: number = vd.getCellValue("level");
  vd.setCellValue("level", 3);
  vd.publish("topic", "payload");
  vd.getDeviceId();
  return [r1, r2, r3, r4, lvl];
}

// ---------------------------------------------------------------------------
// the built-in fs module: typed through require() overloads and as a module
function __fsModule() {
  const fs = require("fs");
  const fsp = require("fs/promises");
  const nodeFs = require("node:fs");
  const text: string = fs.readFileSync("/etc/hostname");
  const text2: string = fs.readFileSync("/etc/hostname", "utf8");
  const text3: string = fs.readFileSync("/etc/hostname", { encoding: "utf8", flag: "r" });
  // @ts-expect-error - only utf8 text: no other encoding
  fs.readFileSync("/etc/hostname", "latin1");
  // @ts-expect-error - callbacks are not part of the API
  fs.readFile("/etc/hostname", (err: unknown, data: string) => data);
  fs.writeFileSync("/tmp/x", "data", { mode: "644", flag: "wx" });
  // @ts-expect-error - strings only, no Buffer/number
  fs.writeFileSync("/tmp/x", 42);
  const st = fs.statSync("/tmp/x");
  const size: number = st.size;
  const isFile: boolean = st.isFile();
  const mtime: Date = st.mtime;
  const maybe: import("fs").Stats | undefined = fs.statSync("/tmp/x", { throwIfNoEntry: false });
  // @ts-expect-error - without throwIfNoEntry:false the result is never undefined
  const notMaybe: undefined = fs.statSync("/tmp/x");
  const names: string[] = fs.readdirSync("/tmp");
  const dirents = fs.readdirSync("/tmp", { withFileTypes: true });
  const direntName: string = dirents[0].name;
  const isDir: boolean = dirents[0].isDirectory();
  // @ts-expect-error - names are strings, not Dirents
  names[0].isDirectory();
  const created: string | undefined = fs.mkdirSync("/tmp/a/b", { recursive: true });
  const nothing: void = fs.mkdirSync("/tmp/c");
  fs.rmSync("/tmp/a", { recursive: true, force: true });
  fs.copyFileSync("/tmp/x", "/tmp/y", fs.constants.COPYFILE_EXCL);
  fs.accessSync("/tmp/x", fs.constants.R_OK | fs.constants.W_OK);
  fs.utimesSync("/tmp/x", new Date(), Date.now() / 1000);
  const w = fs.watch("/tmp", (eventType, filename) => {
    const t: "rename" | "change" = eventType;
    const f: string = filename;
    return [t, f];
  });
  w.close();
  // @ts-expect-error - recursive watching is not available
  fs.watch("/tmp", { recursive: true }, () => {});
  const exists: boolean = fs.existsSync("/tmp/x");
  const p1: Promise<string> = fs.readFile("/tmp/x");
  const p2: Promise<string> = fsp.readFile("/tmp/x");
  const p3: Promise<boolean> = fs.exists("/tmp/x");
  const p4: Promise<import("fs").Dirent[]> = fs.promises.readdir("/tmp", { withFileTypes: true });
  const p5: Promise<void> = nodeFs.writeFile("/tmp/x", "y");
  // @ts-expect-error - the promise variant returns a Promise, not the text
  const notText: string = fs.readFile("/tmp/x");
  // an unknown module is still `any`
  const other = require("some-other-module");
  other.anything();
  return [text, text2, text3, size, isFile, mtime, maybe, notMaybe, direntName, isDir, created, nothing, exists, p1, p2, p3, p4, p5, notText];
}

// keep "unused function" style checkers quiet without executing anything
exports.__wbTypeAssertions = [
  __fsModule,
  __legacyIdioms,
  __registryTypedRefs,
  __typedVirtualDevices,
  __customControlTypes,
  __controlOptionValidation,
  __ruleHandles,
  __asyncPrimitives,
  __devicesAndControlsLookup,
  __loggingAndFormatting,
  __notifyAndAlarms,
  __storageAndConfig,
];
