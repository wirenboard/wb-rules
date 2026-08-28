// the background check resolves a relative .ts import and types it
import { takesString } from "./esmlib/tslib.ts";

defineVirtualDevice("tscheck", {
  cells: { trigger: { type: "switch", value: false } },
});

log(takesString(42)); // line 8: number is not a string
export {};
