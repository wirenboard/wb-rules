// replacement content for testrules_esm.js: the reload re-evaluates the
// imported modules in the new realm and drops the old rule
import { greet } from "test/esm/helper";

defineVirtualDevice("esm", {
  cells: {
    trigger: { type: "switch", value: false },
    out: { type: "value", value: 0, readonly: true },
  },
});

defineRule("esm_rule_v2", {
  whenChanged: "esm/trigger",
  then: () => {
    log("esm v2: {}", greet("again"));
  },
});
