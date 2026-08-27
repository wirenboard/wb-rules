// Fractional numbers through the vdev surface: on 32-bit builds a raw-tag
// check once typed every float as an object (armhf field failure in the
// stock hwmon rules), so this pins the whole float path - addControl,
// setValue and reads, all with non-integer values, from a callback like
// the stock rules do it.
defineVirtualDevice("floatdev", { cells: { pre: { type: "value", value: 1.5, readonly: false } } });

defineRule("float_probe", {
  whenChanged: "floatdev/pre",
  then: function (newValue) {
    var vdev = getDevice("floatdev");
    if (!vdev.isControlExists("temp")) {
      vdev.addControl("temp", { type: "temperature", title: "Temp", value: 42.5 });
    }
    vdev.getControl("temp").setValue({ value: 36.6 });
    log("float values: " + newValue + " " + dev["floatdev/temp"]);
  },
});
