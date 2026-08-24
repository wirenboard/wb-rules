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

/** The built-in filesystem module (declared at the end of this file). */
declare function require(id: "fs" | "node:fs"): typeof import("fs");
declare function require(id: "fs/promises" | "node:fs/promises"): typeof import("fs/promises");
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

// ---------------------------------------------------------------------------
// Built-in module "fs" - require("fs"), require("fs/promises")
// ---------------------------------------------------------------------------

/**
 * Node.js-shaped filesystem API provided by the engine itself.
 *
 * Differences from Node: files are read and written as UTF-8 strings (no
 * Buffer, no other encoding); the asynchronous functions return promises
 * instead of taking callbacks (`fs.readFile === fs.promises.readFile`);
 * `exists()` has a promise form; `fs.watch()` takes its listener directly
 * and returns an object with `close()`; `readFile` refuses files over 10 MiB.
 * Paths are used as given (relative ones resolve against the engine
 * process's working directory) with the engine's own privileges.
 */
declare module "fs" {
  export type Encoding = "utf8" | "utf-8";
  /** Node's file system flags (the `flag` option of writeFile/appendFile). */
  export type OpenFlag = "r" | "r+" | "w" | "wx" | "w+" | "wx+" | "a" | "ax" | "a+" | "ax+";
  /** A permission mode: an integer or an octal string such as "644". */
  export type Mode = number | string;
  /** A timestamp for utimes: seconds since the epoch, a numeric string or a Date. */
  export type TimeLike = number | string | Date;

  export interface ReadFileOptions {
    encoding?: Encoding | null;
    flag?: OpenFlag;
  }
  export interface WriteFileOptions {
    encoding?: Encoding | null;
    /** Mode of a newly created file (default 0o666, subject to umask). */
    mode?: Mode;
    /** Default "w" for writeFile, "a" for appendFile. */
    flag?: OpenFlag;
  }
  export interface StatOptions {
    /** BigInt stats are not supported. */
    bigint?: false;
    /** false: return undefined for a missing path instead of throwing ENOENT. */
    throwIfNoEntry?: boolean;
  }
  export interface ReaddirOptions {
    encoding?: Encoding | null;
    /** true: Dirent objects instead of names. */
    withFileTypes?: boolean;
    /** true: descend into subdirectories (names come relative to path). */
    recursive?: boolean;
  }
  export interface MkdirOptions {
    /** true: create missing parents; the first directory created is returned. */
    recursive?: boolean;
    /** Default 0o777, subject to umask. */
    mode?: Mode;
  }
  export interface RmOptions {
    /** Remove directories and their contents. */
    recursive?: boolean;
    /** Ignore a missing path. */
    force?: boolean;
  }
  export interface RmdirOptions {
    /** @deprecated Node deprecated this spelling; use rm(path, { recursive: true }). */
    recursive?: boolean;
  }
  export interface EncodingOptions {
    encoding?: Encoding | null;
  }
  export interface WatchOptions {
    persistent?: boolean;
    /** Recursive watching is not supported. */
    recursive?: false;
    encoding?: Encoding | null;
  }
  export type WatchEventType = "rename" | "change";
  /** filename is the changed entry (directory watch) or the watched file's base name. */
  export type WatchListener = (eventType: WatchEventType, filename: string) => void;

  /** Result of stat()/lstat(): the POSIX fields plus the type predicates. */
  export class Stats {
    private constructor();
    dev: number;
    ino: number;
    /** File type and permission bits (compare with constants.S_IF*, mask 0o777). */
    mode: number;
    nlink: number;
    uid: number;
    gid: number;
    rdev: number;
    size: number;
    blksize: number;
    blocks: number;
    atimeMs: number;
    mtimeMs: number;
    ctimeMs: number;
    /** stat(2) has no birth time on Linux; this is ctime. */
    birthtimeMs: number;
    atime: Date;
    mtime: Date;
    ctime: Date;
    birthtime: Date;
    isFile(): boolean;
    isDirectory(): boolean;
    isSymbolicLink(): boolean;
    isBlockDevice(): boolean;
    isCharacterDevice(): boolean;
    isFIFO(): boolean;
    isSocket(): boolean;
  }

  /** A directory entry from readdir(path, { withFileTypes: true }). */
  export class Dirent {
    private constructor();
    name: string;
    /** The directory this entry was read from. */
    parentPath: string;
    /** @deprecated Node's older name of parentPath. */
    path: string;
    isFile(): boolean;
    isDirectory(): boolean;
    isSymbolicLink(): boolean;
    isFIFO(): boolean;
    isSocket(): boolean;
    isCharacterDevice(): boolean;
    isBlockDevice(): boolean;
  }

  /** Returned by watch(); close() stops the watcher (idempotent). */
  export class FSWatcher {
    private constructor();
    close(): void;
  }

  export const constants: {
    readonly F_OK: 0;
    readonly R_OK: 4;
    readonly W_OK: 2;
    readonly X_OK: 1;
    /** copyFile mode bit: fail if the destination exists. */
    readonly COPYFILE_EXCL: 1;
    readonly COPYFILE_FICLONE: 2;
    readonly COPYFILE_FICLONE_FORCE: 4;
    readonly S_IFMT: number;
    readonly S_IFREG: number;
    readonly S_IFDIR: number;
    readonly S_IFCHR: number;
    readonly S_IFBLK: number;
    readonly S_IFIFO: number;
    readonly S_IFLNK: number;
    readonly S_IFSOCK: number;
  };

  // --- synchronous API: blocks the engine loop for the duration of the call

  /** Reads a whole file as a UTF-8 string (at most 10 MiB). */
  export function readFileSync(path: string, options?: ReadFileOptions | Encoding | null): string;
  /** Writes data, creating or truncating the file (flag "w"); not atomic. */
  export function writeFileSync(path: string, data: string, options?: WriteFileOptions | Encoding | null): void;
  /** Appends data, creating the file if needed (flag "a"). */
  export function appendFileSync(path: string, data: string, options?: WriteFileOptions | Encoding | null): void;
  export function statSync(path: string, options: StatOptions & { throwIfNoEntry: false }): Stats | undefined;
  export function statSync(path: string, options?: StatOptions): Stats;
  /** Like statSync but does not follow a final symbolic link. */
  export function lstatSync(path: string, options: StatOptions & { throwIfNoEntry: false }): Stats | undefined;
  export function lstatSync(path: string, options?: StatOptions): Stats;
  export function readdirSync(path: string, options: ReaddirOptions & { withFileTypes: true }): Dirent[];
  export function readdirSync(path: string, options?: (ReaddirOptions & { withFileTypes?: false }) | Encoding | null): string[];
  /** true if the path exists (follows symlinks); never throws. */
  export function existsSync(path: string): boolean;
  export function mkdirSync(path: string, options: MkdirOptions & { recursive: true }): string | undefined;
  export function mkdirSync(path: string, options?: (MkdirOptions & { recursive?: false }) | Mode | null): void;
  /** Removes an empty directory. */
  export function rmdirSync(path: string, options?: RmdirOptions): void;
  /** Removes a file, or a directory tree with { recursive: true }. */
  export function rmSync(path: string, options?: RmOptions): void;
  /** Removes a file (a directory fails with EISDIR). */
  export function unlinkSync(path: string): void;
  export function renameSync(oldPath: string, newPath: string): void;
  /** Copies a file preserving its mode; mode: constants.COPYFILE_EXCL to refuse overwriting. */
  export function copyFileSync(src: string, dest: string, mode?: number): void;
  /** Throws unless the path is accessible for mode (constants.F_OK/R_OK/W_OK/X_OK, default F_OK). */
  export function accessSync(path: string, mode?: number): void;
  /** Resolves symbolic links and returns an absolute path. */
  export function realpathSync(path: string, options?: EncodingOptions | Encoding | null): string;
  /** Returns the target of a symbolic link. */
  export function readlinkSync(path: string, options?: EncodingOptions | Encoding | null): string;
  /** Creates a symbolic link at path pointing to target (type is ignored). */
  export function symlinkSync(target: string, path: string, type?: string | null): void;
  export function chmodSync(path: string, mode: Mode): void;
  /** Creates a unique directory prefix + six random characters and returns its path. */
  export function mkdtempSync(prefix: string, options?: EncodingOptions | Encoding | null): string;
  export function truncateSync(path: string, len?: number | null): void;
  export function utimesSync(path: string, atime: TimeLike, mtime: TimeLike): void;

  /**
   * Watches a file or a directory (not recursive). The listener runs on the
   * engine loop; the watcher is closed automatically when the rule file is
   * reloaded or removed, and when the watched path itself disappears.
   */
  export function watch(filename: string, listener: WatchListener): FSWatcher;
  export function watch(
    filename: string,
    options: WatchOptions | Encoding | null | undefined,
    listener: WatchListener
  ): FSWatcher;

  // --- asynchronous API: the I/O runs off the engine loop, the promise
  // settles on it (the same functions as fs.promises)

  export function readFile(path: string, options?: ReadFileOptions | Encoding | null): Promise<string>;
  export function writeFile(path: string, data: string, options?: WriteFileOptions | Encoding | null): Promise<void>;
  export function appendFile(path: string, data: string, options?: WriteFileOptions | Encoding | null): Promise<void>;
  export function stat(path: string, options: StatOptions & { throwIfNoEntry: false }): Promise<Stats | undefined>;
  export function stat(path: string, options?: StatOptions): Promise<Stats>;
  export function lstat(path: string, options: StatOptions & { throwIfNoEntry: false }): Promise<Stats | undefined>;
  export function lstat(path: string, options?: StatOptions): Promise<Stats>;
  export function readdir(path: string, options: ReaddirOptions & { withFileTypes: true }): Promise<Dirent[]>;
  export function readdir(path: string, options?: (ReaddirOptions & { withFileTypes?: false }) | Encoding | null): Promise<string[]>;
  /** Promise form of existsSync (a wb-rules addition; Node's fs.promises has no exists). */
  export function exists(path: string): Promise<boolean>;
  export function mkdir(path: string, options: MkdirOptions & { recursive: true }): Promise<string | undefined>;
  export function mkdir(path: string, options?: (MkdirOptions & { recursive?: false }) | Mode | null): Promise<void>;
  export function rmdir(path: string, options?: RmdirOptions): Promise<void>;
  export function rm(path: string, options?: RmOptions): Promise<void>;
  export function unlink(path: string): Promise<void>;
  export function rename(oldPath: string, newPath: string): Promise<void>;
  export function copyFile(src: string, dest: string, mode?: number): Promise<void>;
  export function access(path: string, mode?: number): Promise<void>;
  export function realpath(path: string, options?: EncodingOptions | Encoding | null): Promise<string>;
  export function readlink(path: string, options?: EncodingOptions | Encoding | null): Promise<string>;
  export function symlink(target: string, path: string, type?: string | null): Promise<void>;
  export function chmod(path: string, mode: Mode): Promise<void>;
  export function mkdtemp(prefix: string, options?: EncodingOptions | Encoding | null): Promise<string>;
  export function truncate(path: string, len?: number | null): Promise<void>;
  export function utimes(path: string, atime: TimeLike, mtime: TimeLike): Promise<void>;

  /** The asynchronous API as one object; also what require("fs/promises") returns. */
  export const promises: typeof import("fs/promises");
}

/** The promise API of the "fs" module (the same functions as fs.promises). */
declare module "fs/promises" {
  import type {
    Dirent,
    Encoding,
    EncodingOptions,
    MkdirOptions,
    Mode,
    ReaddirOptions,
    ReadFileOptions,
    RmdirOptions,
    RmOptions,
    Stats,
    StatOptions,
    TimeLike,
    WriteFileOptions,
  } from "fs";

  export const constants: typeof import("fs").constants;
  export function readFile(path: string, options?: ReadFileOptions | Encoding | null): Promise<string>;
  export function writeFile(path: string, data: string, options?: WriteFileOptions | Encoding | null): Promise<void>;
  export function appendFile(path: string, data: string, options?: WriteFileOptions | Encoding | null): Promise<void>;
  export function stat(path: string, options: StatOptions & { throwIfNoEntry: false }): Promise<Stats | undefined>;
  export function stat(path: string, options?: StatOptions): Promise<Stats>;
  export function lstat(path: string, options: StatOptions & { throwIfNoEntry: false }): Promise<Stats | undefined>;
  export function lstat(path: string, options?: StatOptions): Promise<Stats>;
  export function readdir(path: string, options: ReaddirOptions & { withFileTypes: true }): Promise<Dirent[]>;
  export function readdir(path: string, options?: (ReaddirOptions & { withFileTypes?: false }) | Encoding | null): Promise<string[]>;
  export function exists(path: string): Promise<boolean>;
  export function mkdir(path: string, options: MkdirOptions & { recursive: true }): Promise<string | undefined>;
  export function mkdir(path: string, options?: (MkdirOptions & { recursive?: false }) | Mode | null): Promise<void>;
  export function rmdir(path: string, options?: RmdirOptions): Promise<void>;
  export function rm(path: string, options?: RmOptions): Promise<void>;
  export function unlink(path: string): Promise<void>;
  export function rename(oldPath: string, newPath: string): Promise<void>;
  export function copyFile(src: string, dest: string, mode?: number): Promise<void>;
  export function access(path: string, mode?: number): Promise<void>;
  export function realpath(path: string, options?: EncodingOptions | Encoding | null): Promise<string>;
  export function readlink(path: string, options?: EncodingOptions | Encoding | null): Promise<string>;
  export function symlink(target: string, path: string, type?: string | null): Promise<void>;
  export function chmod(path: string, mode: Mode): Promise<void>;
  export function mkdtemp(prefix: string, options?: EncodingOptions | Encoding | null): Promise<string>;
  export function truncate(path: string, len?: number | null): Promise<void>;
  export function utimes(path: string, atime: TimeLike, mtime: TimeLike): Promise<void>;
}

declare module "node:fs" {
  export * from "fs";
}
declare module "node:fs/promises" {
  export * from "fs/promises";
}
