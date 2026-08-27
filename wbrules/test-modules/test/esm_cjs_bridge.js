// CommonJS module requiring an ES module (synchronously: no top-level await)
var h = require("./esm/helper");
exports.viaCjs = h.greet("cjs");
exports.tlaCode = (function () {
  try {
    require("./esm/tla");
    return "no-error";
  } catch (e) {
    return e.code;
  }
})();
log("Module esm_cjs_bridge init");
