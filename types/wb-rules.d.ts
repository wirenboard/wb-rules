// Type declarations for the wb-rules scripting API.
//
// Consumed in two places:
//  - the engine's background TypeScript check (tsgo --noEmit) includes this
//    file so rule scripts see the builtins as typed globals;
//  - the homeui rules editor loads it to provide typed completions.
//
// The API itself is defined by scripts/lib.js and the engine's DefineFunctions.

declare type CellType =
  | "switch" | "wo-switch" | "alarm" | "pushbutton"
  | "value" | "temperature" | "rel_humidity" | "atmospheric_pressure"
  | "rainfall" | "wind_speed" | "power" | "power_consumption"
  | "voltage" | "water_flow" | "water_consumption" | "resistance"
  | "concentration" | "heat_power" | "heat_energy" | "current"
  | "pressure" | "range" | "text" | "rgb";

declare type CellValue = string | number | boolean;

interface CellSpec {
  type: CellType;
  value?: CellValue;
  title?: string | Record<string, string>;
  readonly?: boolean;
  writeable?: boolean;
  min?: number;
  max?: number;
  precision?: number;
  units?: string;
  order?: number;
  enum?: Record<string | number, string | Record<string, string>>;
  lazyInit?: boolean;
  forceDefault?: boolean;
}

interface VirtualDeviceSpec {
  title?: string | Record<string, string>;
  cells: Record<string, CellSpec>;
}

interface VirtualDeviceControl {
  getId(): string;
  getValue(): CellValue;
  setValue(value: CellValue | { value: CellValue; notify?: boolean }): void;
  getError(): string;
  setError(error: string): void;
  getType(): string;
  getDescription(): string;
  setDescription(description: string): void;
  getTitle(): string;
  setTitle(title: string | Record<string, string>): void;
  getReadonly(): boolean;
  setReadonly(readonly: boolean): void;
  getMax(): number;
  setMax(max: number): void;
  getMin(): number;
  setMin(min: number): void;
  getUnits(): string;
  setUnits(units: string): void;
  getOrder(): number;
  setOrder(order: number): void;
  getEnumTitles(): Record<string, unknown>;
  setEnumTitles(titles: Record<string, unknown>): void;
}

interface VirtualDevice {
  getId(): string;
  getCellId(cellName: string): string;
  addControl(name: string, spec: CellSpec): void;
  removeControl(name: string): void;
  getControl(name: string): VirtualDeviceControl;
  isControlExists(name: string): boolean;
  controlsList(): VirtualDeviceControl[];
  isVirtual(): boolean;
}

declare function defineVirtualDevice(name: string, spec: VirtualDeviceSpec): VirtualDevice;

interface CronEntry { readonly spec: string; }
declare function cron(spec: string): CronEntry;

type RuleCondition = () => unknown;

interface RuleSpec {
  /** cell refs ("device/control"), alias names, or condition functions */
  whenChanged?: string | RuleCondition | Array<string | RuleCondition>;
  when?: RuleCondition | CronEntry;
  asSoonAs?: RuleCondition;
  _cron?: string;
  then: (newValue?: any, devName?: string, cellName?: string) => void;
  readonly?: boolean;
}

declare function defineRule(name: string, spec: RuleSpec): void;
declare function defineRule(spec: RuleSpec): void;

declare function defineAlias(aliasName: string, cellRef: string): void;

declare function enableRule(name: string): void;
declare function disableRule(name: string): void;
declare function runRule(name: string): void;
declare function runRules(): void;

/**
 * Device/cell access proxy: dev["device"]["control"], dev["device/control"],
 * or dev.device.control. Append "#meta" (e.g. "device/control#type") to read
 * control metadata.
 */
declare const dev: {
  [deviceOrRef: string]: { [control: string]: any } & any;
};

declare function getDevice(id: string): VirtualDevice;
declare function getControl(ref: string): VirtualDeviceControl;

interface LogFunction {
  (format: string, ...args: any[]): void;
  debug(format: string, ...args: any[]): void;
  info(format: string, ...args: any[]): void;
  warning(format: string, ...args: any[]): void;
  error(format: string, ...args: any[]): void;
}
declare const log: LogFunction;
declare function debug(format: string, ...args: any[]): void;
declare function format(format: string, ...args: any[]): string;

declare function publish(topic: string, payload: CellValue, qos?: 0 | 1 | 2, retain?: boolean): void;

interface MqttMessage { topic: string; value: string; }
declare function trackMqtt(topic: string, callback: (message: MqttMessage) => void): void;

interface Timer {
  readonly firing: boolean;
  stop(): void;
}
declare const timers: Record<string, Timer>;
declare function startTimer(name: string, milliseconds: number): void;
declare function startTicker(name: string, milliseconds: number): void;

declare function setTimeout(callback: () => void, milliseconds: number): number;
declare function setInterval(callback: () => void, milliseconds: number): number;
declare function clearTimeout(id: number): void;
declare function clearInterval(id: number): void;

interface ShellCommandOptions {
  captureOutput?: boolean;
  captureErrorOutput?: boolean;
  input?: string;
  exitCallback?: (exitCode: number, capturedOutput?: string, capturedErrorOutput?: string) => void;
}
declare function runShellCommand(command: string, options?: ShellCommandOptions): void;
declare function spawn(command: string, args: string[], options?: ShellCommandOptions): void;

declare function readConfig(path: string): any;

interface PersistentStorageOptions { global?: boolean; }
declare function PersistentStorage(name: string, options?: PersistentStorageOptions): Record<string, any>;
declare function StorableObject<T extends object>(obj: T): T;

/** Per-file module object (rule files are CommonJS-like scenarios). */
declare const module: {
  readonly filename: string;
  /** Storage shared between reloads of this file. */
  readonly static: Record<string, any>;
};

declare function require(id: string): any;

declare const global: typeof globalThis;
