// A vendor/custom control type ("fan_mode") outside the wb-rules type map:
// the runtime accepts it and treats the value as a string, and the check
// must accept it too - both the declaration in this file and the registry
// entry generated from the live control.
defineVirtualDevice("vendor_ts", {
  title: "Vendor typed device",
  cells: {
    mode: { type: "fan_mode", value: "auto", readonly: false },
  },
});

defineRule("vendor_probe", {
  whenChanged: "vendor_ts/mode",
  then: (newValue) => {
    log("vendor mode: {} ({})", newValue, typeof newValue);
  },
});

// the registry maps vendor_ts/mode to "fan_mode", which is not a CellType:
// the stringly-referenced APIs stay loose (any) instead of erroring
function __vendorRefs() {
  dev["vendor_ts/mode"] = "eco";
  const c = getControl("vendor_ts/mode");
  if (c) {
    c.setValue("turbo");
  }
}
