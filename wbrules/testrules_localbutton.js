defineVirtualDevice("buttons", {
  title: "Button Test",
  cells: {
    somebutton: {
      type: "pushbutton"
    }
  }
});

defineRule("buttontest", {
  whenChanged: "buttons/somebutton",
  then: function () {
    log("button pressed!");
  }
});
