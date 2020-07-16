defineRule("test_test", { 
  whenChanged: "testControl/switch_control",
  then: function (newValue, devName, cellName) {
    if (newValue) {
      dev["testControl"]["pers_text"] = "Test text";
      log("text: {}", dev["testControl"]["pers_text"]);
    } else {
      dev["testControl"]["pers_text"] = ""; 
      log("text: {}", dev["testControl"]["pers_text"]);
    }
  }
});