defineVirtualDevice("test", {
    cells: {
        getid: {
            type: "pushbutton",
            value: false
        },
        local: {
            type: "pushbutton",
            value: false
        }
    }
});


// define local device
var localDev = module.defineVirtualDevice("test", {
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
        localDev.publish("controls/myCell/on", "1");
    }
});

defineRule("localTestSub", {
    whenChanged: localDev.getCellId("myCell"),
    then: function() {
        log("triggered local device");
    }
});

defineRule("getid", {
    whenChanged: "test/getid",
    then: function() {
        log("device id: '" + localDev.getDeviceId() + "'")
    }
});
