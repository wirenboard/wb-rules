// Type declarations for the wb-rules scripting API.
//
// Consumed in two places:
//  - the engine's background TypeScript check (tsgo --noEmit) includes this
//    file so rule scripts see the builtins as typed globals;
//  - the homeui rules editor loads it to provide typed completions.
//
// The API itself is defined by scripts/lib.js, modules/wb-notify.js,
// modules/wb-alarms.js and the engine's DefineFunctions. Everything here is
// grounded in that code: option fields the engine rejects are typed away,
// per-control-type option sets match fillControlArgs exactly, and the
// rule-management functions take the numeric rule id defineRule returns.
//
// Design notes (techniques adapted from the public-domain wb-mirta/core
// `@mirta/globals` package): a single TypeMappings interface derives the
// control-type union and per-type value types; ControlOptions is a
// discriminated union so illegal option/type combinations fail to compile;
// rule ids are a branded number so arbitrary numbers (or rule names) are
// rejected by enableRule/disableRule/runRule.

// ---------------------------------------------------------------------------
// Utility types
// ---------------------------------------------------------------------------

/** Flattens an intersection so editor hovers show a single object literal. */
declare type WbExpand<T> = { [K in keyof T]: T[K] } & {};

/** Like Partial, but at least one of the properties must be present. */
declare type WbAtLeastOne<T, U = { [K in keyof T]: Pick<T, K> }> = Partial<T> & U[keyof U];

/**
 * Nominal (branded) alias of a primitive type. Purely a compile-time
 * marker: values are plain primitives at runtime.
 */
declare type WbBranded<TValue, TBrand extends string> = TValue & { readonly __wbBrand: TBrand };

/** Localized text: language code ("en", "ru", ...) to translation. */
declare type LocalizedText = Record<string, string>;

/** A human-readable title: a plain string ("en") or per-language texts. */
declare type Title = string | LocalizedText;

// ---------------------------------------------------------------------------
// Controls: the type map everything else derives from
// ---------------------------------------------------------------------------

/**
 * Maps every control (cell) type to the JavaScript type its value has when
 * read through `dev`, a rule's `newValue`, or a control object's getValue().
 *
 * Derived from the Wiren Board MQTT conventions; extend here and every
 * dependent type (CellType, ControlOptions, typed controls) follows.
 */
interface TypeMappings {
  /** Boolean switch (writable by default). */
  switch: boolean;
  /** Write-only switch: command out, no state readback. */
  "wo-switch": boolean;
  /** Alarm indicator. */
  alarm: boolean;
  /** Stateless push button; the only type that needs no initial `value`. */
  pushbutton: boolean;

  /** Arbitrary text. */
  text: string;
  /** Color in "R;G;B" form, e.g. "255;127;0". */
  rgb: string;

  /** Generic numeric value. */
  value: number;
  /** Integer slider between min and max (writable by default). */
  range: number;
  /** Unix timestamp, seconds. */
  unixtime: number;
  /** Temperature, °C. */
  temperature: number;
  /** Relative humidity, %. */
  rel_humidity: number;
  /** Atmospheric pressure, mbar. */
  atmospheric_pressure: number;
  /** Rainfall rate, mm/h. */
  rainfall: number;
  /** Wind speed, m/s. */
  wind_speed: number;
  /** Power, W. */
  power: number;
  /** Energy, kWh. */
  power_consumption: number;
  /** Voltage, V. */
  voltage: number;
  /** Water flow, m³/h. */
  water_flow: number;
  /** Water volume, m³. */
  water_consumption: number;
  /** Resistance, Ohm. */
  resistance: number;
  /** Gas concentration, ppm. */
  concentration: number;
  /** Pressure, bar. */
  pressure: number;
  /** Illuminance, lux. */
  lux: number;
  /** Sound level, dB. */
  sound_level: number;
  /** Heat power, Gcal/h. */
  heat_power: number;
  /** Heat energy, Gcal. */
  heat_energy: number;
  /** Current, A. */
  current: number;
}

/** Union of all control (cell) types. */
declare type CellType = keyof TypeMappings;

/** Any control value. */
declare type CellValue = TypeMappings[CellType];

/**
 * Value type of a specific control type: CellValueOf<"switch"> is boolean.
 *
 * A vendor/custom type outside TypeMappings is string-valued: the runtime
 * passes unknown control types through and treats their values as strings.
 */
declare type CellValueOf<T extends CellType | (string & {})> = T extends CellType
  ? TypeMappings[T]
  : string;

// ---------------------------------------------------------------------------
// Control options (defineVirtualDevice cells)
// ---------------------------------------------------------------------------

/** Options every control type accepts. */
interface WbControlOptionsBase<TType extends string> {
  /** Control type; decides the value type and which other options are legal. */
  type: TType;
  /** Title shown in the UI (plain string = English, or per-language map). */
  title?: Title;
  /** Longer description shown in the UI. */
  description?: string;
  /**
   * Forbid writes from the UI and rules. Defaults: switch, pushbutton,
   * range and rgb are writable; every other type is read-only.
   */
  readonly?: boolean;
  /** Position among the device's controls (integer, >= 0). */
  order?: number;
  /**
   * Do not create the MQTT control until a value is first assigned
   * (e.g. `dev["device/control"] = value`).
   */
  lazyInit?: boolean;
  /**
   * Reset to `value` on every engine start instead of restoring the last
   * retained value.
   */
  forceDefault?: boolean;
  /** @deprecated The engine rejects this flag - use `readonly` instead. */
  writeable?: never;
}

/**
 * The initial value. Required for every type except pushbutton
 * (a pushbutton is stateless).
 */
type __WbControlValue<TType extends CellType> = TType extends "pushbutton"
  ? { value?: TypeMappings[TType] }
  : { value: TypeMappings[TType] };

/**
 * Per-type extra options, matching what the engine actually reads:
 * units only on "value"; precision on "value" and "range"; enum titles on
 * "value" and "text"; min/max on "value" and "range".
 *
 * Options a type does not support are declared as `?: never` instead of
 * being omitted: providing one then fails real assignability, so the error
 * fires even where object-literal freshness checks do not reach (e.g.
 * through generic parameter inference in defineVirtualDevice).
 */
type __WbControlExtras<TType extends CellType> = TType extends "value"
  ? {
      /** Unit shown next to the value (e.g. "W", "m³/h"). */
      units?: string;
      /** Number of decimal places shown. */
      precision?: number;
      /**
       * Titles for the allowed values. Note: each title must be a
       * per-language map - the engine silently drops plain strings.
       */
      enum?: Record<number | string, LocalizedText>;
      /** Smallest accepted value. */
      min?: number;
      /** Largest accepted value. */
      max?: number;
    }
  : TType extends "range"
    ? {
        /** Number of decimal places shown. */
        precision?: number;
        /** Smallest accepted value (default 0). */
        min?: number;
        /** Largest accepted value (default 255). */
        max?: number;
        units?: never;
        enum?: never;
      }
    : TType extends "text"
      ? {
          /**
           * Titles for the allowed values. Note: each title must be a
           * per-language map - the engine silently drops plain strings.
           */
          enum?: Record<string, LocalizedText>;
          units?: never;
          precision?: never;
          min?: never;
          max?: never;
        }
      : {
          units?: never;
          precision?: never;
          enum?: never;
          min?: never;
          max?: never;
        };

/**
 * Options of a vendor/custom control type not listed in TypeMappings. The
 * runtime accepts any type string (fillControlArgs passes it through) and
 * treats the value as a string; such controls are read-only by default and
 * none of the per-type extra options apply to them.
 */
declare type CustomControlOptions = WbExpand<
  WbControlOptionsBase<string & {}> & { value: string } & {
    units?: never;
    precision?: never;
    enum?: never;
    min?: never;
    max?: never;
  }
>;

/**
 * A control declaration for defineVirtualDevice()/addControl().
 *
 * A discriminated union over `type`: options illegal for the chosen type do
 * not compile, e.g. `{ type: "switch", min: 0 }` is an error because only
 * "value" and "range" controls have `min`. A type string outside
 * TypeMappings falls into the vendor/custom branch (string-valued).
 */
declare type ControlOptions =
  | {
      [K in CellType]: WbExpand<
        WbControlOptionsBase<K> & __WbControlValue<K> & __WbControlExtras<K>
      >;
    }[CellType]
  | CustomControlOptions;

/** Options of one specific control type: ControlOptionsOfType<"range">. */
declare type ControlOptionsOfType<T extends CellType> = Extract<ControlOptions, { type: T }>;

declare type SwitchControlOptions = ControlOptionsOfType<"switch">;
declare type PushbuttonControlOptions = ControlOptionsOfType<"pushbutton">;
declare type AlarmControlOptions = ControlOptionsOfType<"alarm">;
declare type ValueControlOptions = ControlOptionsOfType<"value">;
declare type RangeControlOptions = ControlOptionsOfType<"range">;
declare type TextControlOptions = ControlOptionsOfType<"text">;
declare type RgbControlOptions = ControlOptionsOfType<"rgb">;

/** @deprecated Old name; use ControlOptions. */
declare type CellSpec = ControlOptions;

// ---------------------------------------------------------------------------
// Virtual devices
// ---------------------------------------------------------------------------

/** The controls of a device declaration, by control name. */
declare type ControlsSpec = Record<string, ControlOptions>;

/** Device declaration: a title plus controls under `cells` or `controls`. */
declare type VirtualDeviceSpec =
  | { title?: Title; cells: ControlsSpec }
  | { title?: Title; controls: ControlsSpec };

/**
 * A control of a virtual device, as returned by getControl().
 *
 * The type parameter tracks the control's declared type, so getValue() on a
 * control obtained from a typed device returns boolean/number/string as
 * declared instead of the full union. A vendor/custom type outside
 * TypeMappings is string-valued (see CellValueOf).
 */
interface VirtualDeviceControl<TType extends CellType | (string & {}) = CellType> {
  getId(): string;
  getValue(): CellValueOf<TType>;
  setValue(value: CellValueOf<TType> | { value: CellValueOf<TType>; notify?: boolean }): void;
  /** Error state: a non-empty string marks the control as failed in the UI. */
  getError(): string;
  setError(error: string): void;
  getType(): TType;
  /** Any type string is accepted, matching the runtime (vendor types included). */
  setType(type: CellType | (string & {})): void;
  getDescription(): string;
  setDescription(description: string): void;
  /** Title in the given language ("en" when omitted). */
  getTitle(lang?: string): string;
  setTitle(title: Title): void;
  getReadonly(): boolean;
  setReadonly(readonly: boolean): void;
  getMax(): number;
  setMax(max: number): void;
  getMin(): number;
  setMin(min: number): void;
  getPrecision(): number;
  setPrecision(precision: number): void;
  getUnits(): string;
  setUnits(units: string): void;
  getOrder(): number;
  setOrder(order: number): void;
  /** Value titles; each title is a per-language map. */
  setEnumTitles(titles: Record<number | string, LocalizedText>): void;
}

/** Cells record of a device spec (whether declared as `cells` or `controls`). */
type __WbCellsOf<S> = S extends { cells: infer C extends ControlsSpec }
  ? C
  : S extends { controls: infer C extends ControlsSpec }
    ? C
    : ControlsSpec;

/**
 * A virtual device. When obtained from defineVirtualDevice(), getControl()
 * with a literal control name returns a control typed by that control's
 * declared type.
 */
interface VirtualDevice<TCells extends ControlsSpec = ControlsSpec> {
  getId(): string;
  /** @deprecated use getId() */
  getDeviceId(): string;
  /** Full "device/control" reference of the named control. */
  getCellId(cellName: string): string;
  /** dev[this device/cellName] */
  getCellValue<K extends keyof TCells & string>(cellName: K): CellValueOf<TCells[K]["type"]>;
  getCellValue(cellName: string): any;
  setCellValue<K extends keyof TCells & string>(cellName: K, value: CellValueOf<TCells[K]["type"]>): void;
  setCellValue(cellName: string, value: any): void;
  /** publish(message) under /devices/<this device>/<topic> */
  publish(topic: string, message: any): void;
  addControl(name: string, spec: ControlOptions): void;
  removeControl(name: string): void;
  /**
   * The named control, or undefined: even a control the declaration lists
   * can be gone at call time (removeControl, an incomplete reload), so the
   * typed overload is control-or-undefined too.
   */
  getControl<K extends keyof TCells & string>(
    name: K
  ): VirtualDeviceControl<TCells[K]["type"]> | undefined;
  getControl(name: string): VirtualDeviceControl | undefined;
  isControlExists(name: string): boolean;
  controlsList(): VirtualDeviceControl[];
  isVirtual(): boolean;
  /** Error state: a non-empty string marks the whole device as failed. */
  getError(): string;
  setError(error: string): void;
}

/**
 * Creates a virtual device backed by MQTT.
 *
 * The returned device is typed by the declaration:
 *
 * ```ts
 * const dv = defineVirtualDevice("climate", {
 *   title: "Climate",
 *   cells: {
 *     temperature: { type: "temperature", value: 0 },
 *     enabled: { type: "switch", value: false },
 *   },
 * });
 * const t = dv.getControl("temperature").getValue(); // number
 * ```
 */
declare function defineVirtualDevice<S extends VirtualDeviceSpec>(
  name: string,
  spec: S
): VirtualDevice<__WbCellsOf<S>>;

/** The device with the given id, or undefined if there is no such device. */
declare function getDevice(id: string): VirtualDevice | undefined;

/**
 * Registry of known "device/control" references to their control type,
 * used to type the stringly-referenced APIs (`getControl("dev/ctrl")` and
 * `dev["dev/ctrl"]`).
 *
 * Empty by default, so those APIs stay loose (`any`/untyped) wherever the
 * registry is not populated - notably the engine's on-controller type
 * check, which must not error on references it cannot know about.
 *
 * Populate it by declaration merging to get real type safety. The homeui
 * rules editor does this automatically from the controller's live device
 * list; you can also do it by hand for your own devices:
 *
 * ```ts
 * declare global {
 *   interface WbControls {
 *     "climate/temperature": "temperature"; // -> number
 *     "living/light": "switch";             // -> boolean
 *   }
 * }
 * ```
 *
 * With that in scope, `getControl("climate/temperature").setValue("x")`
 * and `dev["climate/temperature"] = "x"` are compile errors, while any
 * reference not listed stays loose.
 */
interface WbControls {}

/**
 * The control at "device/control", or undefined if it does not exist (a
 * registry entry proves the control existed when the registry was built,
 * not that it still does). For a reference listed in WbControls the result
 * is typed by that control's type; any other reference returns the loose
 * control-or-undefined.
 */
declare function getControl<K extends keyof WbControls & string>(
  ref: K
): VirtualDeviceControl<WbControls[K] extends CellType ? WbControls[K] : CellType> | undefined;
declare function getControl(ref: string): VirtualDeviceControl | undefined;

// ---------------------------------------------------------------------------
// Rules
// ---------------------------------------------------------------------------

/**
 * Identifier of a rule, returned by defineRule().
 *
 * A branded number: enableRule/disableRule/runRule accept only a value that
 * came from defineRule, so passing a rule name or an arbitrary number is a
 * compile-time error (and would fail at runtime).
 */
declare type RuleId = WbBranded<number, "RuleId">;

interface CronEntry {
  readonly spec: string;
}

/**
 * A cron schedule for a rule's `when`, e.g. cron("@hourly") or
 * cron("0 0 9 * * MON-FRI"). See the robfig/cron expression format.
 */
declare function cron(spec: string): CronEntry;

type RuleCondition = () => unknown;

/** The fields of a rule definition; see RuleSpec. */
interface RuleSpecFields {
  /**
   * Fire on control changes: a "device/control" reference, an alias name,
   * a condition function whose return value is watched, or an array of
   * those.
   */
  whenChanged?: string | RuleCondition | Array<string | RuleCondition>;
  /** Fire whenever the condition is true, or on a cron() schedule. */
  when?: RuleCondition | CronEntry;
  /** Fire once each time the condition switches from false to true. */
  asSoonAs?: RuleCondition;
  _cron?: string;
  /**
   * The rule body. For whenChanged rules the arguments are the new value
   * and the "device", "control" pair that caused the trigger; for other
   * kinds they are undefined. May be async: rejections are reported to the
   * rule engine log. The return value is ignored, so a concise body such as
   * `then: (v) => dev["x/y"] = !!v` is fine.
   */
  then: (newValue?: any, devName?: string, cellName?: string) => void;
  /** Accepted for legacy compatibility; the engine ignores it. */
  readonly?: boolean;
}

/**
 * Defines a rule and returns its id.
 *
 * ```ts
 * const nightLight = defineRule("night-light", {
 *   whenChanged: "motion/detected",
 *   then: (v) => { dev["light/on"] = !!v; },
 * });
 * disableRule(nightLight);
 * ```
 */
/**
 * A rule definition. Inside `then` and the condition functions `this` is
 * the rule object itself; existing rules keep per-rule state on it
 * (`this.counter = ...`), so it is open for any property.
 */
type RuleSpec = RuleSpecFields & ThisType<RuleSpecFields & Record<string, any>>;

declare function defineRule(name: string, spec: RuleSpec): RuleId;
declare function defineRule(spec: RuleSpec): RuleId;

/** Makes `aliasName` usable in place of "device/control" references. */
declare function defineAlias(aliasName: string, cellRef: string): void;

/** Re-enables a rule disabled with disableRule(). */
declare function enableRule(ruleId: RuleId): void;
/** Disables a rule: it stops reacting to events until enableRule(). */
declare function disableRule(ruleId: RuleId): void;
/**
 * Runs a rule's `then` immediately, with no trigger context (newValue and
 * the device/control arguments are undefined).
 */
declare function runRule(ruleId: RuleId): void;
/** Runs all rules, or only the rules watching the given control. */
declare function runRules(): void;
declare function runRules(devId: string, ctrlId: string): void;

// ---------------------------------------------------------------------------
// Device access
// ---------------------------------------------------------------------------

/**
 * Device/control access proxy.
 *
 * Values: `dev["device/control"]`, `dev["device"]["control"]` or
 * `dev.device.control` reads the current value; assignment writes it.
 *
 * Metadata: append `#<field>` to the control name to read or write control
 * metadata, e.g. `dev["device/control#error"] = "sensor offline"` or
 * `const t = dev["device/control#type"]`.
 *
 * A `"device/control"` reference listed in WbControls is typed by that
 * control's type on both read and write (`dev["climate/temperature"]` is a
 * number; assigning a string is an error). Every other key - unlisted
 * references, the nested `dev["device"]["control"]` and `dev.device.control`
 * forms, and the `#meta` suffix - stays loose (`any`). The shipped registry
 * is empty, so without population everything is `any`, exactly as before.
 */
declare const dev: {
  [K in keyof WbControls | (string & {})]: K extends keyof WbControls
    ? WbControls[K] extends CellType
      ? TypeMappings[WbControls[K]]
      : any
    : any;
};

// ---------------------------------------------------------------------------
// Logging
// ---------------------------------------------------------------------------

interface LogFunction {
  /** Logs a message; `{}` placeholders are replaced by the arguments. */
  (format: string, ...args: any[]): void;
  /** Logs an arbitrary value. */
  (value: unknown): void;
  debug(format: string, ...args: any[]): void;
  debug(value: unknown): void;
  info(format: string, ...args: any[]): void;
  info(value: unknown): void;
  warning(format: string, ...args: any[]): void;
  warning(value: unknown): void;
  error(format: string, ...args: any[]): void;
  error(value: unknown): void;
}
/** Engine log; log(...) is the same as log.info(...). */
declare const log: LogFunction;
/** Logs only when rule debugging is enabled. */
declare function debug(format: string, ...args: any[]): void;
/** Replaces `{}` placeholders in the format string with the arguments. */
declare function format(format: string, ...args: any[]): string;

// ---------------------------------------------------------------------------
// MQTT
// ---------------------------------------------------------------------------

/**
 * Publishes a raw MQTT message. Do not use this to change device controls -
 * assign through `dev` instead.
 */
declare function publish(topic: string, payload: CellValue, qos?: 0 | 1 | 2, retain?: boolean): void;

interface MqttMessage {
  topic: string;
  value: string;
  retained: boolean;
  qos: number;
}
/** Subscribes to an MQTT topic ("#" and "+" wildcards are allowed). */
declare function trackMqtt(topic: string, callback: (message: MqttMessage) => void): void;
/**
 * Resolves with the next live (non-retained) MQTT message on the topic.
 * With timeoutMs set, rejects if no message arrives in time.
 */
declare function nextMqtt(topic: string, timeoutMs?: number): Promise<MqttMessage>;

// ---------------------------------------------------------------------------
// Timers
// ---------------------------------------------------------------------------

interface Timer {
  readonly firing: boolean;
  stop(): void;
}
/** Named timers started with startTimer()/startTicker(). */
declare const timers: Record<string, Timer>;
/** One-shot named timer; watch it with `when: () => timers.name.firing`. */
declare function startTimer(name: string, milliseconds: number): void;
/** Periodic named timer. */
declare function startTicker(name: string, milliseconds: number): void;

/**
 * Resolves with the control's new value on its next change - the same
 * semantics (triggers, value conversion) as a rule's whenChanged.
 * With timeoutMs set, rejects if nothing changes in time.
 *
 * A `"device/control"` reference listed in WbControls resolves to that
 * control's value type (so `await changed("climate/temperature")` is a
 * number); any other reference stays loose (`any`). Pass a type argument
 * to pin it explicitly: `const t = await changed<number>("dev/ctrl")`.
 */
declare function changed<K extends keyof WbControls & string>(
  ctrl: K,
  timeoutMs?: number
): Promise<WbControls[K] extends CellType ? TypeMappings[WbControls[K]] : CellValue>;
declare function changed<T extends CellValue = any>(ctrl: string, timeoutMs?: number): Promise<T>;
/** Promise-returning pause: await sleep(1000). The engine is not blocked. */
declare function sleep(milliseconds: number): Promise<void>;
declare function setTimeout(callback: () => void, milliseconds: number): number;
declare function setInterval(callback: () => void, milliseconds: number): number;
declare function clearTimeout(id: number): void;
declare function clearInterval(id: number): void;

// ---------------------------------------------------------------------------
// Processes
// ---------------------------------------------------------------------------

type ExitCallback = (
  exitCode: number,
  capturedOutput?: string | null,
  capturedErrorOutput?: string
) => void;

interface ShellCommandOptions {
  /** Capture stdout and deliver it in the result / exit callback. */
  captureOutput?: boolean;
  /** Capture stderr instead of passing it through to the engine's stderr. */
  captureErrorOutput?: boolean;
  /** Text to feed to the process on stdin. */
  input?: string;
  exitCallback?: ExitCallback;
}
interface SpawnResult {
  /** Process exit code; a nonzero exit resolves the promise, it does not reject. */
  exitCode: number;
  capturedOutput: string | null;
  capturedErrorOutput?: string;
}
/** Resolves on process exit; rejects only when the process cannot start. */
declare function runShellCommand(
  command: string,
  options?: ShellCommandOptions | ExitCallback
): Promise<SpawnResult>;
/** Resolves on process exit; rejects only when the process cannot start. */
declare function spawn(
  command: string,
  args?: string[] | null,
  options?: ShellCommandOptions | ExitCallback
): Promise<SpawnResult>;

// ---------------------------------------------------------------------------
// Configuration and storage
// ---------------------------------------------------------------------------

interface ReadConfigOptions {
  /** Log an error when the file is missing (default true). */
  logErrorOnNoFile?: boolean;
}
/** Parses a JSON configuration file (comments allowed). */
declare function readConfig(path: string, options?: ReadConfigOptions): any;

interface PersistentStorageOptions {
  /** Share the storage between all rule files instead of per-file. */
  global?: boolean;
}
/**
 * A persistent key-value storage that survives engine restarts.
 * Give it a shape for typed access:
 * `const s = PersistentStorage<{ count: number }>("stats", { global: true })`.
 * Callable with or without `new` (both forms are in use; the runtime returns
 * the storage object either way).
 */
declare const PersistentStorage: {
  <T extends Record<string, any> = Record<string, any>>(
    name: string,
    options?: PersistentStorageOptions
  ): T;
  new <T extends Record<string, any> = Record<string, any>>(
    name: string,
    options?: PersistentStorageOptions
  ): T;
};
/**
 * Wraps an object so property changes propagate back to the
 * PersistentStorage slot it is stored in. Callable with or without `new`
 * (the runtime's own error text suggests `new StorableObject(obj)`).
 */
declare const StorableObject: {
  <T extends object>(obj: T): T & Record<string, any>;
  new <T extends object>(obj: T): T & Record<string, any>;
};

// ---------------------------------------------------------------------------
// Notifications (modules/wb-notify.js, available as the Notify global)
// ---------------------------------------------------------------------------

/** Called when the notification has been handed off; error is null on success. */
type NotifyCallback = (error: Error | null) => void;

interface WebhookOptions {
  url: string;
  /** HTTP method; default POST when a body is present, GET otherwise. */
  method?: string;
  /** Request body; objects are JSON-encoded. */
  body?: string | object;
  /** Content-Type header; inferred from the body when omitted. */
  contentType?: string;
  headers?: Record<string, string>;
}

interface TelegramMessageOptions {
  /** Telegram parse_mode, e.g. "MarkdownV2" or "HTML". */
  parseMode?: string;
  disableWebPagePreview?: boolean;
  disableNotification?: boolean;
}

interface NotifyApi {
  /** Sends an email through the local sendmail. */
  sendEmail(to: string, subject: string, text: string, callback?: NotifyCallback): void;
  /** Sends an SMS via ModemManager (or gammu); `command` overrides the tool. */
  sendSMS(to: string, text: string, command?: string, callback?: NotifyCallback): void;
  sendSMS(to: string, text: string, callback: NotifyCallback): void;
  /** Performs an HTTP request (curl) with the given options. */
  sendWebhook(options: WebhookOptions, callback?: NotifyCallback): void;
  /** Sends a message via a Telegram bot. */
  sendTelegramMessage(
    token: string,
    chatId: string,
    text: string,
    options?: TelegramMessageOptions,
    callback?: NotifyCallback
  ): void;
  sendTelegramMessage(token: string, chatId: string, text: string, callback: NotifyCallback): void;
  /** Uppercases/validates an HTTP method name, defaulting appropriately. */
  normalizeWebhookMethod(method?: string): string;
}
/** Notification channels: email, SMS, webhooks, Telegram. */
declare const Notify: NotifyApi;

// ---------------------------------------------------------------------------
// Alarms (modules/wb-alarms.js, available as the Alarms global)
// ---------------------------------------------------------------------------

declare type AlarmRecipient =
  | { type: "email"; to: string; subject?: string }
  | { type: "sms"; to: string; command?: string }
  | { type: "telegram"; token: string; chatId: string }
  | { type: "vk"; token: string; peerId: string; apiVersion?: string }
  | { type: "max"; token: string; chatId: string }
  | { type: "matrix"; homeserver: string; accessToken: string; roomId: string; msgType?: string }
  | { type: "wechat"; key: string }
  | {
      type: "webhook";
      url: string;
      method?: string;
      contentType?: string;
      headers?: Record<string, string>;
      /** Body template; `{}` is replaced with the alarm message. */
      bodyTemplate?: string;
    };

interface AlarmBase {
  /** Alarm cell name; derived from the watched cell when omitted. */
  name?: string;
  /** The watched control, as "device/control". */
  cell: string;
  /** Message sent when the alarm activates; `{}` is replaced by the value. */
  alarmMessage?: string;
  /** Message sent when the alarm deactivates; `{}` is replaced by the value. */
  noAlarmMessage?: string;
  /** Repeat interval for reminders about a still-active alarm, seconds. */
  interval?: number;
  /** Maximum number of messages sent per activation. */
  maxCount?: number;
  /** Require the out-of-range state to persist this long, ms. */
  alarmDelayMs?: number;
  /** Require the back-to-normal state to persist this long, ms. */
  noAlarmDelayMs?: number;
}

/**
 * One alarm: watches a cell and alerts either when its value differs from
 * `expectedValue`, or when it leaves the [minValue, maxValue] range (at
 * least one bound required).
 */
declare type AlarmSpec = AlarmBase &
  ({ expectedValue: CellValue } | WbAtLeastOne<{ minValue: number; maxValue: number }>);

interface AlarmsConfig {
  /** Virtual device created for the alarm cells and log. */
  deviceName: string;
  deviceTitle?: Title;
  recipients: AlarmRecipient[];
  alarms: AlarmSpec[];
}

interface AlarmsApi {
  /** Loads alarms from a JSON config file path or an inline config object. */
  load(config: string | AlarmsConfig): void;
}
/** Threshold alarms with notification fan-out (see AlarmsConfig). */
declare const Alarms: AlarmsApi;

// ---------------------------------------------------------------------------
// String formatting (lib.js augments String.prototype)
// ---------------------------------------------------------------------------

interface String {
  /** Replaces `{}` placeholders with the arguments, like format(). */
  format(...args: any[]): string;
  /**
   * Like format(), but placeholders may contain expressions that are
   * EVALUATED as code. Never use with untrusted input.
   */
  xformat(...args: any[]): string;
}

// ---------------------------------------------------------------------------
// MQTT-RPC (modules/wb-mqtt-rpc.js, available as the MqttRpc global)
// ---------------------------------------------------------------------------

/**
 * MQTT-RPC: call the methods that wb-mqtt-serial, wb-mqtt-db and the other
 * controller services publish, and serve methods of your own. Requests go to
 * /rpc/v1/<driver>/<service>/<method>/<clientId>, replies come back on
 * .../reply; every call returns a Promise. `MqttRpc` and
 * `require("wb-mqtt-rpc")` are the same per-file instance.
 */
declare namespace MqttRpc {
  /** Options accepted by every call. */
  interface CallOptions {
    /**
     * Milliseconds to wait for the reply; default `MqttRpc.defaults.timeout`
     * (60000). 0 waits forever.
     */
    timeout?: number;
    /**
     * Wait for the method's presence topic before sending: `true` waits up
     * to the call timeout, a number is its own limit in ms. Useful at boot,
     * when the rules may start before the service does.
     */
    waitForMethod?: boolean | number;
  }

  interface HasMethodOptions {
    /** Milliseconds to wait for the retained presence; default 3000. */
    timeout?: number;
  }

  /** The `error` member of an MQTT-RPC reply. */
  interface ErrorObject {
    code: number;
    message: string;
    data?: any;
  }

  /**
   * A call rejected by the server (code/message/data as sent), and what a
   * served handler throws to answer with a specific error.
   */
  class RpcError extends Error {
    constructor(code: number, message: string, data?: any);
    code: number;
    data?: any;
  }

  /**
   * No reply within the timeout (or, for waitForMethod, no presence in
   * time): code -32600 and data "MqttTimeoutError", like the web UI's.
   */
  class TimeoutError extends RpcError {
    constructor(message: string, data?: any);
  }

  /** JSON-RPC 2.0 reserved codes plus the client-side timeout code. */
  const ErrorCode: {
    readonly PARSE_ERROR: -32700;
    readonly INVALID_REQUEST: -32600;
    readonly METHOD_NOT_FOUND: -32601;
    readonly INVALID_PARAMS: -32602;
    readonly INTERNAL_ERROR: -32603;
    readonly SERVER_ERROR: -32000;
    readonly TIMEOUT: -32600;
  };

  /** Per-file defaults; assign to change them for this file. */
  const defaults: {
    /** Reply timeout in ms (60000). */
    timeout: number;
    /** hasMethod() wait in ms (3000). */
    hasMethodTimeout: number;
  };

  /** This file's client id: the last level of its request topics. */
  const clientId: string;

  /** The driver id defineService() uses when none is given. */
  const DEFAULT_SERVICE_DRIVER: "wbrules-scripts";

  /**
   * Calls /rpc/v1/<driver>/<service>/<method>. `params` goes out as the
   * request's `params` ({} when omitted). Resolves with the reply's `result`;
   * rejects with RpcError (server error) or TimeoutError.
   */
  function call<R = any>(
    driver: string,
    service: string,
    method: string,
    params?: object | null,
    options?: CallOptions
  ): Promise<R>;

  /**
   * Whether the method is currently served (its retained presence topic
   * holds a value). Resolves false if nothing shows up within the wait; the
   * answer is kept up to date afterwards, so repeated calls are instant.
   */
  function hasMethod(
    driver: string,
    service: string,
    method: string,
    options?: HasMethodOptions
  ): Promise<boolean>;

  /**
   * Resolves as soon as the method is served; rejects with TimeoutError
   * after `timeoutMs` (default: the call timeout; 0 waits forever).
   */
  function waitForMethod(
    driver: string,
    service: string,
    method: string,
    timeoutMs?: number
  ): Promise<void>;

  /** A method of a service proxy: `params` as the request's `params`. */
  type Method<P = any, R = any> = {} extends P
    ? (params?: P | null, options?: CallOptions) => Promise<R>
    : (params: P, options?: CallOptions) => Promise<R>;

  /** call/hasMethod/waitForMethod bound to one driver/service pair. */
  interface ServiceProxy {
    readonly driver: string;
    readonly service: string;
    call<R = any>(method: string, params?: object | null, options?: CallOptions): Promise<R>;
    hasMethod(method: string, options?: HasMethodOptions): Promise<boolean>;
    waitForMethod(method: string, timeoutMs?: number): Promise<void>;
  }

  /**
   * A proxy for one service, plus one function per listed method:
   * `MqttRpc.service("wbrules", "Editor", ["List"]).List()`.
   */
  function service<M extends string = never>(
    driver: string,
    service: string,
    methods?: readonly M[]
  ): ServiceProxy & { [K in M]: Method<any, any> };

  /** What a served handler gets besides the params. */
  interface Request {
    driver: string;
    service: string;
    method: string;
    /** The caller's client id (last level of the request topic). */
    clientId: string;
    id: number | string | null;
    /** The request topic; the reply goes to `topic + "/reply"`. */
    topic: string;
  }

  /**
   * A served method: returns the result (or a Promise of it). Throw an
   * RpcError to answer with a specific code; any other exception answers
   * -32603 and is logged.
   */
  type Handler<P = any, R = any> = (params: P, request: Request) => R | Promise<R>;

  interface ServiceDefinition {
    driver: string;
    service: string;
    /** Every method served under driver/service so far. */
    methods: string[];
  }

  /**
   * Serves methods at /rpc/v1/<driver>/<service>/<name>: each is announced
   * on its retained presence topic and answered until the file unloads
   * (reload, removal, engine stop), which clears the presence. The default
   * driver is "wbrules-scripts" - "wbrules" is the engine's own Editor
   * server, which answers -32601 for services it does not know.
   */
  function defineService(service: string, methods: Record<string, Handler>): ServiceDefinition;
  function defineService(
    driver: string,
    service: string,
    methods: Record<string, Handler>
  ): ServiceDefinition;

  // ---- wb-mqtt-serial ----

  namespace Serial {
    type BaudRate = 110 | 300 | 600 | 1200 | 2400 | 4800 | 9600 | 19200 | 38400 | 57600 | 115200;
    type Parity = "N" | "E" | "O";
    /** A serial port with its line settings (all required). */
    interface SerialPort {
      path: string;
      baud_rate: BaudRate;
      parity: Parity;
      data_bits: 5 | 6 | 7 | 8;
      stop_bits: 1 | 2;
    }
    /** A Modbus TCP endpoint. */
    interface TcpPort {
      ip: string;
      port: number;
    }
    /** A device from wb-mqtt-serial's config: port and protocol settings come from there. */
    interface ConfiguredDevice {
      device_id: string;
    }
    /** Request time budget, all in ms. */
    interface Timeouts {
      /** Per-response wait; default 500 (at least the port's configured value). */
      response_timeout?: number;
      /** Inter-frame gap; default 20. */
      frame_timeout?: number;
      /** Queueing plus execution; default 10000. Exceeding it answers -32600 "Request timeout". */
      total_timeout?: number;
    }
    interface RawRequest {
      protocol?: "raw";
      /** Bytes to send: text, or hex when format is "HEX". */
      msg: string;
      /** Number of response bytes to wait for. */
      response_size: number;
      format?: "STR" | "HEX";
    }
    interface ModbusRequest {
      protocol: "modbus" | "modbus-tcp";
      slave_id: number;
      function: 1 | 2 | 3 | 4 | 5 | 6 | 15 | 16 | 23;
      address: number;
      /** Registers/bits to read; default 1. */
      count?: number;
      write_address?: number;
      write_count?: number;
      /** Data to write, for the write functions. */
      msg?: string;
      format?: "STR" | "HEX";
    }
    type PortLoadParams = (SerialPort | TcpPort | ConfiguredDevice) & (RawRequest | ModbusRequest) & Timeouts;
    interface PortLoadResult {
      /** The response bytes (data only for Modbus), text or hex per `format`. */
      response?: string;
      /** A Modbus exception instead of data. */
      exception?: { code: number; msg: string };
    }
    interface PortSetupItem {
      slave_id: number;
      baud_rate: BaudRate;
      parity: Parity;
      data_bits: number;
      stop_bits: number;
      /** Serial number: address the device by it instead of slave_id. */
      sn?: number;
      /** The new settings; parity as 0 (N), 1 (O), 2 (E). */
      cfg: { baud_rate?: number; parity?: 0 | 1 | 2; stop_bits?: number; slave_id?: number };
    }
    type PortSetupParams = ({ path: string } | TcpPort) & { items: PortSetupItem[]; total_timeout?: number };
    type PortScanParams = (SerialPort | TcpPort) &
      Timeouts & {
        protocol?: "modbus" | "modbus-tcp";
        /** Fast Modbus scan command; default 70. */
        command?: 70 | 96;
        mode?: "all" | "start" | "next";
      };
    interface ScannedDevice {
      sn: number;
      device_signature: string;
      fw_signature: string;
      configured_device_type?: string;
      errors: { id: string; message: string }[];
      cfg: { slave_id: number; baud_rate: number; parity: string; data_bits: number; stop_bits: number };
      fw: { version: string };
    }
    interface PortScanResult {
      devices: ScannedDevice[];
      /** Set when the scan stopped early; `devices` holds what was found. */
      error?: string;
    }
    type DeviceProbeParams = (SerialPort | TcpPort) &
      Timeouts & { slave_id: number; protocol?: "modbus" | "modbus-tcp" };
    /** The probed device, or {} when nothing answers. */
    type DeviceProbeResult = ScannedDevice | {};
    /** A device by port + address + template, or by its config id. */
    type DeviceAddress =
      | ((SerialPort | (TcpPort & { modbus_mode?: "RTU" | "TCP" })) & {
          slave_id: number | string;
          device_type: string;
        })
      | ConfiguredDevice;
    type DeviceLoadConfigParams = DeviceAddress & Timeouts & { force?: boolean };
    interface DeviceLoadConfigResult {
      parameters: Record<string, any>;
      fw?: string;
      model?: string;
    }
    type DeviceLoadParams = DeviceAddress & Timeouts & { channels?: string[]; parameters?: string[] };
    interface DeviceLoadResult {
      channels: Record<string, any>;
      parameters: Record<string, any>;
      readonly: string[];
    }
    type DeviceSetParams = DeviceAddress &
      Timeouts & { channels?: Record<string, any>; parameters?: Record<string, any> };
    type DeviceSetPollParams = (
      | (({ path: string } | TcpPort) & { slave_id: number | string })
      | ConfiguredDevice
    ) & {
      /** false pauses polling of the device (resumes by itself after 10 min). */
      poll: boolean;
    };
    interface ConfigLoadParams {
      /** Language of titles/descriptions; default "en". */
      lang?: string;
    }
    interface DeviceType {
      name: string;
      type: string;
      deprecated: boolean;
      protocol: string;
      "mqtt-id": string;
      "with-subdevices"?: boolean;
      "user-defined"?: boolean;
      hw?: { signature: string; fw?: string }[];
    }
    interface DeviceTypeGroup {
      name: string;
      types: DeviceType[];
    }
    interface ConfigLoadResult {
      config: any;
      schema: any;
      types: DeviceTypeGroup[];
    }
    interface ConfigGetSchemaParams {
      /** A device type, or "protocol:<name>" for a protocol's generic schema. */
      type: string;
    }
    interface TemplatesUploadParams {
      /** The template file's JSON text. */
      content: string;
      filename: string;
      lang?: string;
      /** Replace a template that configured devices use (else error.data is "template-in-use"). */
      force?: boolean;
    }
    interface TemplatesDeleteParams {
      type: string;
      lang?: string;
      force?: boolean;
    }
    interface TemplatesResult {
      types: DeviceTypeGroup[];
    }
    type ConfiguredPort =
      | { path: string; baud_rate: number; data_bits: number; parity: string; stop_bits: number }
      | { address: string; port: number };
    interface FwUpdatePort {
      path: string;
      baud_rate?: number;
      parity?: string;
      data_bits?: number;
      stop_bits?: number;
    }
    interface FwUpdateTarget {
      slave_id: number;
      port: FwUpdatePort | { address: string; port: number };
      protocol?: "modbus" | "modbus-tcp";
    }
    interface FirmwareComponent {
      model: string;
      fw: string;
      available_fw: string;
      has_update: boolean;
    }
    interface FirmwareInfo {
      fw: string;
      available_fw: string;
      can_update: boolean;
      fw_has_update: boolean;
      bootloader: string;
      available_bootloader: string;
      bootloader_has_update: boolean;
      model: string;
      components: Record<string, FirmwareComponent>;
    }
    type FwUpdateParams = FwUpdateTarget & { type?: "firmware" | "bootloader" | "components" | "component" };
    interface FwClearErrorParams {
      slave_id: number;
      port: { path: string };
      type?: "firmware" | "bootloader";
    }
    /** The fw-update service (also served by wb-device-manager). */
    interface FwUpdateService extends ServiceProxy {
      GetFirmwareInfo: Method<FwUpdateTarget, FirmwareInfo>;
      /** Returns "Ok" at once; progress is on the retained .../firmware_update/state topic. */
      Update: Method<FwUpdateParams, "Ok">;
      ClearError: Method<FwClearErrorParams, "Ok">;
      Restore: Method<FwUpdateTarget, "Ok">;
    }
  }

  /** wb-mqtt-serial: the Modbus/serial device driver. */
  const serial: {
    readonly driver: "wb-mqtt-serial";
    config: ServiceProxy & {
      /** The driver config with its schema and the known device types. */
      Load: Method<Serial.ConfigLoadParams, Serial.ConfigLoadResult>;
      /** The JSON schema of one device type. */
      GetSchema: Method<Serial.ConfigGetSchemaParams, any>;
    };
    templates: ServiceProxy & {
      /** Installs a user device template. */
      Upload: Method<Serial.TemplatesUploadParams, Serial.TemplatesResult>;
      Delete: Method<Serial.TemplatesDeleteParams, Serial.TemplatesResult>;
    };
    ports: ServiceProxy & {
      /** The configured ports. */
      Load: Method<{}, Serial.ConfiguredPort[]>;
    };
    port: ServiceProxy & {
      /**
       * Sends one raw or Modbus request through a port (queued after the
       * current poll cycle). Without an explicit timeout the call waits
       * for `total_timeout` plus a margin.
       */
      Load: Method<Serial.PortLoadParams, Serial.PortLoadResult>;
      /** Changes line settings/addresses of devices on a port. */
      Setup: Method<Serial.PortSetupParams, {}>;
      /** Fast Modbus scan of a port. */
      Scan: Method<Serial.PortScanParams, Serial.PortScanResult>;
    };
    device: ServiceProxy & {
      /** Reads a device's settings (cached per configured device unless `force`). */
      LoadConfig: Method<Serial.DeviceLoadConfigParams, Serial.DeviceLoadConfigResult>;
      /** Reads channels and parameters of a device. */
      Load: Method<Serial.DeviceLoadParams, Serial.DeviceLoadResult>;
      /** Writes channels and parameters of a device. */
      Set: Method<Serial.DeviceSetParams, {}>;
      /** Identifies the device at an address. */
      Probe: Method<Serial.DeviceProbeParams, Serial.DeviceProbeResult>;
      /** Pauses/resumes polling of a configured device. */
      SetPoll: Method<Serial.DeviceSetPollParams, {}>;
    };
    fwUpdate: Serial.FwUpdateService;
  };

  // ---- wb-mqtt-db ----

  namespace Db {
    /** A channel as [device, control]. */
    type Channel = [string, string];
    interface GetValuesParams {
      channels: Channel[];
      /** Record layout: 0 (default, verbose) or 1 (compact). */
      ver?: 0 | 1;
      /** Time window, UNIX seconds. */
      timestamp?: { gt?: number; lt?: number };
      /** Start after this record id. */
      uid?: { gt: number };
      /** Records to return; `has_more` tells whether more exist. */
      limit?: number;
      /** Averaging interval, ms. */
      min_interval?: number;
      /** Averaging target: at most this many records (overrides min_interval). */
      max_records?: number;
      /** Server-side budget, seconds (default 9). Without an explicit timeout the call waits for it. */
      request_timeout?: number;
      /** ver 1 only: `t` with a millisecond fraction. */
      with_milliseconds?: boolean;
    }
    /** ver 0 record. */
    interface Value {
      uid: number;
      device: string;
      control: string;
      /** UNIX seconds. */
      timestamp: number;
      value: string;
      min?: string;
      max?: string;
      retain: boolean;
    }
    /** ver 1 record. */
    interface CompactValue {
      i: number;
      /** Channel index in the request's `channels`. */
      c: number;
      /** UNIX seconds. */
      t: number;
      v: string;
      min?: string;
      max?: string;
      retain: boolean;
    }
    interface GetValuesResult<V = Value | CompactValue> {
      values: V[];
      /** More records exist beyond `limit`. */
      has_more?: boolean;
    }
    interface GetChannelsResult {
      /** "device/control" -> record count and last timestamp (UNIX seconds). */
      channels: Record<string, { items: number; last_ts: number }>;
    }
  }

  /** wb-mqtt-db: the history database. */
  const db: {
    readonly driver: "db_logger";
    history: ServiceProxy & {
      /** History records; the layout follows `ver` (0: Value, 1: CompactValue). */
      get_values: {
        (params: Db.GetValuesParams & { ver: 1 }, options?: CallOptions): Promise<
          Db.GetValuesResult<Db.CompactValue>
        >;
        (params: Db.GetValuesParams & { ver?: 0 }, options?: CallOptions): Promise<
          Db.GetValuesResult<Db.Value>
        >;
        (params: Db.GetValuesParams, options?: CallOptions): Promise<Db.GetValuesResult>;
      };
      get_channels: Method<{}, Db.GetChannelsResult>;
    };
  };

  // ---- wb-rules (the editor of this very engine) ----

  namespace RulesEditor {
    interface Location {
      line: number;
      name: string;
    }
    interface ScriptError {
      message: string;
      traceback: Location[];
    }
    interface FileEntry {
      virtualPath: string;
      enabled: boolean;
      error?: ScriptError;
      rules: Location[];
      devices: Location[];
      timers: Location[];
    }
    interface LoadResult {
      content: string;
      enabled: boolean;
      error?: ScriptError;
    }
    interface SaveResult {
      path: string;
      /** A load error of the saved file, when it has one. */
      error?: any;
      traceback?: Location[];
    }
    interface Diag {
      file?: string;
      line: number;
      column: number;
      severity: string;
      message: string;
      code?: number;
    }
    interface CheckResult {
      status: "ready" | "pending" | "not-ts" | "unsupported";
      diags: Diag[];
    }
  }

  /** wb-rules: the rule editor RPC. Error codes 1000-1009 with data "EditorError". */
  const rules: {
    readonly driver: "wbrules";
    Editor: ServiceProxy & {
      List: Method<{}, RulesEditor.FileEntry[]>;
      Load: Method<{ path: string }, RulesEditor.LoadResult>;
      Save: Method<{ path: string; content: string }, RulesEditor.SaveResult>;
      Remove: Method<{ path: string }, boolean>;
      Rename: Method<{ path: string; new_path: string }, boolean>;
      ChangeState: Method<{ path: string; state: boolean }, boolean>;
      /** The background type-check verdict of a file (poll while "pending"). */
      Check: Method<{ path: string }, RulesEditor.CheckResult>;
      /** The API declarations (this file). */
      GetTypes: Method<{}, { content: string }>;
    };
  };

  // ---- wb-mqtt-confed ----

  namespace Confed {
    interface ConfigEntry {
      title: string;
      description: string;
      configPath: string;
      schemaPath: string;
      editor: string;
      titleTranslations?: Record<string, string>;
      descriptionTranslations?: Record<string, string>;
    }
    interface LoadResult {
      configPath: string;
      /** The config file's content, parsed. */
      content: any;
      schema: any;
      editor: string;
    }
  }

  /** wb-mqtt-confed: the configuration editor. Error codes 1002/1003/1006 with data "EditorError". */
  const confed: {
    readonly driver: "confed";
    Editor: ServiceProxy & {
      List: Method<{}, Confed.ConfigEntry[]>;
      /** `path` is a configPath from List. */
      Load: Method<{ path: string }, Confed.LoadResult>;
      /** Writes the config and restarts the services that depend on it. */
      Save: Method<{ path: string; content: any }, { path: string }>;
    };
  };

  // ---- wb-mqtt-logs ----

  namespace Logs {
    interface ListResult {
      boots: { hash: string; start: number; end?: number }[];
      services: string[];
    }
    interface LoadParams {
      /** A boot hash from List; default: the current boot. */
      boot?: string;
      service?: string;
      /** Start time, UNIX seconds. */
      time?: number;
      /** syslog levels 0..7; default all. */
      levels?: number[];
      pattern?: string;
      regex?: boolean;
      "case-sensitive"?: boolean;
      cursor?: { id: string; direction?: "backward" | "forward" };
      /** Records to return; default and maximum 100. */
      limit?: number;
    }
    interface Entry {
      msg: string;
      /** UNIX milliseconds. */
      time: number;
      /** syslog level; absent for 6 (info). */
      level?: number;
      service?: string;
      /** Present on the first and last entries: pass it back to page. */
      cursor?: string;
    }
  }

  /** wb-mqtt-logs: journal access. */
  const logs: {
    readonly driver: "wb_logs";
    logs: ServiceProxy & {
      List: Method<{}, Logs.ListResult>;
      Load: Method<Logs.LoadParams, Logs.Entry[]>;
      /** Aborts a running Load. */
      CancelLoad: Method<{}, null>;
    };
  };

  // ---- wb-diag-collect ----

  /** wb-diag-collect: the diagnostics archive. */
  const diag: {
    readonly driver: "diag";
    main: ServiceProxy & {
      /** Starts collecting; the archive is announced on /wb-diag-collect/artifact. */
      diag: Method<{}, "Ok">;
      /** Liveness probe: "1". */
      status: Method<{}, "1">;
    };
  };

  // ---- wb-device-manager ----

  namespace DeviceManager {
    interface BusScanStartParams {
      scan_type?: "extended" | "standard" | "bootloader";
      preserve_old_results?: boolean;
      /** `path` is a serial device or "IP:PORT". */
      port?: { path: string; protocol?: "modbus" | "modbus-tcp" };
      out_of_order_slave_ids?: number[];
    }
  }

  /** wb-device-manager: the serial bus scanner. Busy answers -33100. */
  const deviceManager: {
    readonly driver: "wb-device-manager";
    busScan: ServiceProxy & {
      /** Returns "Ok" at once; progress and results are on the retained /wb-device-manager/state topic. */
      Start: Method<DeviceManager.BusScanStartParams, "Ok">;
      Stop: Method<{}, "Ok">;
    };
    fwUpdate: Serial.FwUpdateService;
  };

  // ---- wb-mqtt-dali ----

  namespace Dali {
    interface CommandResult {
      status: "ok" | "error";
      response?: { raw: number | null; value: string };
      error?: string;
    }
    interface CommandInfo {
      name: string;
      category: string;
      snippet: string;
    }
    interface GatewayEntry {
      id: string;
      name: string;
      buses: {
        id: string;
        name: string;
        devices: { id: string; name: string; groups: number[] }[];
        commissioning: Record<string, any>;
      }[];
    }
  }

  /** wb-mqtt-dali: the DALI gateway (Editor methods follow its own docs and may change). */
  const dali: {
    readonly driver: "wb-mqtt-dali";
    Editor: ServiceProxy & {
      GetList: Method<{}, Dali.GatewayEntry[]>;
      GetGateway: Method<{ gatewayId: string }, { config: Record<string, any> }>;
      SetGateway: Method<{ gatewayId: string; config: Record<string, any> }, any>;
      GetBus: Method<{ busId: string }, { config: Record<string, any>; schema: any }>;
      SetBus: Method<{ busId: string; config: Record<string, any> }, any>;
      ScanBus: Method<{ busId: string }, { status: "started" | "already_running"; progressTopic: string }>;
      StopScanBus: Method<{ busId: string }, { status: "stopped" | "not_running" }>;
      GetDevice: Method<{ deviceId: string; forceReload?: boolean }, { config: Record<string, any>; schema: any }>;
      SetDevice: Method<{ deviceId: string; config: Record<string, any> }, any>;
      GetGroup: Method<{ groupId: string }, Record<string, any>>;
      SetGroup: Method<{ groupId: string; config: Record<string, any> }, {}>;
      IdentifyDevice: Method<{ deviceId: string }, {}>;
      ResetDeviceSettings: Method<{ deviceId: string }, {}>;
      ResetDevice: Method<{ deviceId: string }, {}>;
    };
    Bus: ServiceProxy & {
      /** Runs DALI commands ("DAPC(A0, 0xFE)") atomically on a bus; one result per command. */
      SendCommand: Method<{ busId: string; commands: string[] }, Dali.CommandResult[]>;
      ListCommands: Method<{}, Dali.CommandInfo[]>;
    };
  };
}

/** The MQTT-RPC module as a module: the same per-file instance as the global. */
declare module "wb-mqtt-rpc" {
  export = MqttRpc;
}

// ---------------------------------------------------------------------------
// Module system
// ---------------------------------------------------------------------------

/** Absolute path of the current rule file. */
declare const __filename: string;

/** Per-file module object (rule files are CommonJS-like scenarios). */
declare const module: {
  readonly filename: string;
  /**
   * Storage shared between reloads of a module file. Present only in
   * files loaded via require(); in rule files it is undefined and
   * writing to it throws at load time.
   */
  readonly static?: Record<string, any>;
  /** What require() of this file returns (rule modules); see also `exports`. */
  exports: Record<string, any>;
};

/** The MQTT-RPC module: the same per-file instance as the MqttRpc global. */
declare function require(id: "wb-mqtt-rpc"): typeof MqttRpc;
declare function require(id: string): any;
/**
 * wb-rules resolves `require("x.mod")` against its own module directories
 * (/usr/share/wb-rules-modules, /etc/wb-rules-modules, ...), which the type
 * checker knows nothing about. In a .js file TypeScript treats a call to an
 * ambient `require` as a CommonJS import and would report "Cannot find module"
 * for every wb-rules module; this wildcard makes any otherwise-unresolved
 * module name (relative ones included) resolve to `any` - the same loose
 * typing `require` has always had in .ts files. A mistyped path is therefore
 * not a type error; it still fails loudly at load time ("cannot find module").
 */
declare module "*";

declare const global: typeof globalThis;

// CommonJS-style module surface available in every rule file
declare var exports: Record<string, any>;
