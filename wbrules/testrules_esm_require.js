// a classic rule file reaching ES modules through require() and import()
defineVirtualDevice("esmreq", {
  cells: {
    req: { type: "switch", value: false },
    dyn: { type: "switch", value: false },
  },
});

defineRule("esmreq_require", {
  whenChanged: "esmreq/req",
  then: function () {
    var h = require("test/esm/helper");
    log("req: {}", h.greet("req"));
    var bridge = require("test/esm_cjs_bridge");
    log("bridge: {} {}", bridge.viaCjs, bridge.tlaCode);
    try {
      require("test/esm/tla");
      log("req tla: no error");
    } catch (e) {
      log("req tla: {}", e.code);
    }
  },
});

defineRule("esmreq_dynamic", {
  whenChanged: "esmreq/dyn",
  then: async function () {
    var m = await import("test/esm/util");
    var tla = await import("test/esm/tla");
    log("dyn: {} {} {}", m.double(4), m.default, tla.v);
  },
});
