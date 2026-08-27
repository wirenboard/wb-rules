// error line numbers in an ES module rule file
import { double } from "test/esm/util";

defineVirtualDevice("esmerr", {
  cells: { trigger: { type: "switch", value: false } },
});

defineRule("esmerr_rule", {
  whenChanged: "esmerr/trigger",
  then: () => {
    // line 12
    throw new Error("esm-boom " + double(1));
  },
});
