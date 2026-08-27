// a classic script by extension: an ESM-looking line inside a template
// literal does not make the loader try the module parse
const banner = `
export const notReally = 1;
`;

defineVirtualDevice("cjsx", {
  cells: { trigger: { type: "switch", value: false } },
});

defineRule("cjsx_rule", {
  whenChanged: "cjsx/trigger",
  then: function () {
    log("cjsx: {} {}", typeof module, banner.length);
  },
});
