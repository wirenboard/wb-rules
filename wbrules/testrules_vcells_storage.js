defineVirtualDevice("test-vdev", {
    title: "Test virtual device",

    cells: {
        cell1: {
            type: "switch",
            value: false
        },
        cell2: {
            type: "switch",
            value: true
        },
        cell3: {
            type: "switch",
            value: false,
            forceDefault: true
        },
        cellText: {
            type: "text",
            readonly: false,
            value: "foo"
        }
    }
})

defineVirtualDevice("test-trigger", {
    title: "Trigger device",

    cells: {
        echo: {
            type: "pushbutton"
        },
        change1: {
            type: "pushbutton"
        }
    }
})

defineRule("testChange1", {
    whenChanged: ["test-trigger/change1"],
    then: function() {
        dev["test-vdev/cell1"] = true
        dev["test-vdev/cell3"] = true
        dev["test-vdev/cellText"] = "bar"
    }
})

defineRule("testEcho", {
    whenChanged: ["test-trigger/echo"],
    then: function() {
        log("vdev " + dev["test-vdev/cell1"] + ", " + dev["test-vdev/cell2"] + ", " + dev["test-vdev/cell3"] + ", " + dev["test-vdev/cellText"])
    }
})
