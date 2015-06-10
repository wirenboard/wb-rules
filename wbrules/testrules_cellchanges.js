defineVirtualDevice("cellch", {
  title: "Cell Change Test",
  cells: {
    sw: {
      type: "switch",
      value: false
    },
    misc: {
      type: "text",
      value: "0"
    },
    button: {
      type: "pushbutton"
    }
  }
});

defineRule("startCellChange", {
  whenChanged: "cellch/button",
  then: function () {
    dev.cellch.sw = !dev.cellch.sw;
    dev.cellch.misc = "1";
    log("startCellChange: sw <- {}", dev.cellch.sw);
  }
});

defineRule("switchChanged", {
  whenChanged: "cellch/sw",
  then: function () {
    log("switchChanged: sw={}", dev.cellch.sw);
    dev.somedev.sw = true;
  }
});
