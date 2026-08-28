// ES module rule file with native top-level await, importing a module that
// awaits at its top level too
import { v } from "test/esm/tla";

defineVirtualDevice("esmtla", {
  cells: {
    trigger: { type: "switch", value: false },
    out: { type: "value", value: 0, readonly: true },
  },
});

const w = await Promise.resolve(v + 1);

defineRule("esmtla_rule", {
  whenChanged: "esmtla/trigger",
  then: () => {
    dev["esmtla/out"] = w;
  },
});

log("esmtla loaded: {}", w);
