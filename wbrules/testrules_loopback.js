defineVirtualDevice("loopback", {
    cells: {
        gauge: {
            type: "value",
            value: 0
        },
        set_loud: {
            type: "pushbutton"
        },
        set_silent: {
            type: "pushbutton"
        },

        relay_main: {
            type: "switch",
            value: false
        },
        relay_silent: {
            type: "switch",
            value: false
        }
    }
});

defineRule({
    whenChanged: "loopback/gauge",
    then: function(newValue) {
        log("gauge set to " + newValue);
    }
});

defineRule({
    whenChanged: "loopback/set_loud",
    then: function() {
        log("set_loud button pressed");
        getControl("loopback/gauge").setValue(42);
    }
});

defineRule({
    whenChanged: "loopback/set_silent",
    then: function() {
        log("set_silent button pressed");
        getControl("loopback/gauge").setValue({
            value: 84,
            notify: false
        });
    }
});

defineRule({
    whenChanged: "loopback/relay_main",
    then: function(newValue) {
        log("relay_main: " + newValue);
    }
});

defineRule({
    whenChanged: "loopback/relay_silent",
    then: function(newValue) {
        log("relay_silent: " + newValue);
        getControl("loopback/relay_main").setValue({
            value: newValue,
            notify: false
        });
    }
});
