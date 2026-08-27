// An ES module rule file: static imports of ES and CommonJS modules from the
// module directories, import.meta, rules and devices defined at module level.
import { greet, counter, whereAmI } from "test/esm/helper";
import cjsDefault, { hello, adder } from "test/helloworld";
import * as util from "test/esm/util";

defineVirtualDevice("esm", {
  cells: {
    trigger: { type: "switch", value: false },
    out: { type: "value", value: 0, readonly: true },
  },
});

defineRule("esm_rule", {
  whenChanged: "esm/trigger",
  then: () => {
    log("esm: {} {} {} {} {}", greet("world"), hello, adder(10, 20), cjsDefault.hello === hello, util.default);
    dev["esm/out"] = util.double(21);
    log("esm counter: {}", counter());
    log("esm where: {}", whereAmI());
  },
});

log("esm meta: {} {} {}",
  import.meta.filename.endsWith("/testrules_esm.js"),
  import.meta.url.startsWith("file:///"),
  import.meta.dirname === __filename.slice(0, __filename.lastIndexOf("/")));

export const marker = "exported";
