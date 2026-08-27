// @ts-nocheck - the deliberate implicit global below is the point of the test
// an ES module by extension: no import/export in the file, yet strict mode,
// import.meta and no `module`/`exports`
defineVirtualDevice("esmx", {
  cells: { trigger: { type: "switch", value: false } },
});

let mode;
try {
  undeclaredVar = 1;
  mode = "sloppy";
} catch (e) {
  mode = e.name;
}
const cts = require("test/esm/legacy.cts");
const mts = await import("test/esm/typed-only.mts");

defineRule("esmx_rule", {
  whenChanged: "esmx/trigger",
  then: () => {
    log("esmx: {} {} {} {} {}", mode, typeof module, import.meta.filename.endsWith(".mjs"),
      cts.answer + ":" + cts.kind, globalThis.mtsLoaded);
  },
});
