// the background check maps bare specifiers onto the module directories:
// a wrong call into a .ts module there is flagged
import { typedAdd } from "test/esm/typed";

defineVirtualDevice("tsbad", {
  cells: { trigger: { type: "switch", value: false } },
});

log(typedAdd("one", 2)); // line 9: string is not a number
export {};
