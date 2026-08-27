// relative import of a library next to the rule file (not a module directory)
import { sib } from "./esmlib/sib.js";

defineVirtualDevice("esmsib", {
  cells: { trigger: { type: "switch", value: false } },
});

defineRule("esmsib_rule", {
  whenChanged: "esmsib/trigger",
  then: () => {
    log("sib: {}", sib);
  },
});
