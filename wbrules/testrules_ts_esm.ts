// TypeScript ES module rule file: typed imports from a .ts module in the
// module directories and from a JavaScript ES module
import { typedAdd, explode, type Point } from "test/esm/typed";
import { greet } from "test/esm/helper";

const p: Point = { x: 1, y: 2 };

defineVirtualDevice("tsesm", {
  cells: {
    trigger: { type: "switch", value: false },
    boom: { type: "switch", value: false },
    out: { type: "value", value: 0, readonly: true },
  },
});

defineRule("tsesm_rule", {
  whenChanged: "tsesm/trigger",
  then: () => {
    dev["tsesm/out"] = typedAdd(p.x, p.y);
    log("tsesm: {}", greet("ts"));
  },
});

defineRule("tsesm_boom", {
  whenChanged: "tsesm/boom",
  then: () => {
    explode("rule"); // line 27 in this file; line 13 in typed.ts
  },
});

export const tsMarker: string = "ts";
