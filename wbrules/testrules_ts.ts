// TypeScript rule file used by RuleTypeScriptSuite.
interface CounterState {
  count: number;
  label: string;
}

const state: CounterState = { count: 0, label: "ts" };

defineVirtualDevice("tsdev", {
  title: "TS Test Device",
  cells: {
    trigger: { type: "switch", value: false, readonly: false },
    count: { type: "value", value: 0, readonly: true },
  },
});

defineRule("ts_counter", {
  whenChanged: "tsdev/trigger",
  then: (newValue: boolean) => {
    state.count++;
    dev.tsdev.count = state.count;
    log("ts rule fired: {} count={}", newValue, state.count);
  },
});

defineRule("ts_thrower", {
  whenChanged: "somedev/sw",
  then: () => {
    throw new Error("ts-boom"); // line 29: asserted by TestTsErrorLineNumbers
  },
});
