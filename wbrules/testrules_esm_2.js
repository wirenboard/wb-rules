// a second rule file importing the same module: its own instance (the module
// initialises again in this realm), but import.meta.static is shared
import { counter } from "test/esm/helper";

defineVirtualDevice("esm2", {
  cells: { trigger: { type: "switch", value: false } },
});

defineRule("esm2_rule", {
  whenChanged: "esm2/trigger",
  then: () => {
    log("esm2 counter: {}", counter());
  },
});
