defineVirtualDevice("test", {
    cells: {
        local: {
            type: "pushbutton",
            value: false
        }
    }
});


// define local device
module.defineVirtualDevice("test", {
    cells: {
        myCell: {
            type: "pushbutton",
            value: false
        }
    }
});

defineRule("localTest", {
    whenChanged: "test/local",
    then: function() {
        log("triggered global device");
        publish("/devices/" + module.virtualDeviceName("test") + "/controls/myCell/on", "1");
    }
});

defineRule("localTestSub", {
    whenChanged: module.virtualDeviceName("test") + "/myCell",
    then: function() {
        log("triggered local device");
    }
});