var ctrlID = "wrCtrlID";

defineRule({
    whenChanged: ["spawner/spawn"],
    then: function(newValue) {
        getDevice("spawner").controlsList().forEach(function (ctrl) {
            log("ctrlID: {}, error: {}".format(ctrl.getId(), ctrl.getError()));
        })
        if (getDevice("spawner").isControlExists(ctrlID)) {
            getDevice("spawner").removeControl(ctrlID);
        } else {
            var newControl = {type: "text", value: "test-text", readonly: false};
            getDevice("spawner").addControl(ctrlID, newControl);
        }
    }
});

defineRule({
    whenChanged: ["spawner/check"],
    then: function(newValue) {
        log("ctrlID: somedev, isVirtual: {}".format(getDevice("somedev").isVirtual()));
        log("ctrlID: spawner, isVirtual: {}".format(getDevice("spawner").isVirtual()));

        getDevice("spawner").controlsList().forEach(function (ctrl) {
            if (ctrl.getId() === ctrlID) {
                log("ctrlID: {}, error: {}".format(ctrl.getId(), ctrl.getError()));
                log("ctrlID: {}, type: {}".format(ctrl.getId(), ctrl.getType()));
                log("ctrlID: {}, order: {}".format(ctrl.getId(), ctrl.getOrder()));
                log("ctrlID: {}, max: {}".format(ctrl.getId(), ctrl.getMax()));
                log("ctrlID: {}, readonly: {}".format(ctrl.getId(), ctrl.getReadonly()));
                log("ctrlID: {}, units: {}".format(ctrl.getId(), ctrl.getUnits()));
                log("ctrlID: {}, value: {}".format(ctrl.getId(), ctrl.getValue()));
            }
        })
    }
});

defineRule({
    whenChanged: ["spawner/change"],
    then: function(newValue) {
        ctrl = getDevice("spawner").getControl(ctrlID);
        if (newValue) {
            ctrl.setDescription("true Description");
            ctrl.setType("range");
            ctrl.setOrder(5);
            ctrl.setMax(255);
            ctrl.setReadonly(true);
            ctrl.setUnits("meters");
            ctrl.setValue(42);
            ctrl.setError("new Error");
        } else {
            ctrl.setDescription("new Description");
            ctrl.setError("");
            ctrl.setType("text");
            ctrl.setOrder(4);
            ctrl.setMax(0);
            ctrl.setReadonly(false);
            ctrl.setUnits("chars");
        }
    }
});

defineVirtualDevice("spawner", {
    title: "spawner",
    cells: {
        "spawn": {
            type: "switch",
            value: false,
            readonly: false,
        },
        "check": {
            type: "switch",
            value: false,
            readonly: false,
        },
        "change": {
            type: "switch",
            value: false,
            readonly: false,
        },
    },
});
