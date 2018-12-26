// -*- mode: js2-mode -*-

defineVirtualDevice("vdev", {
    title: "VDev",
    cells: {
        write: {
            type: "switch",
            value: false
        },
        read: {
            type: "switch",
            value: false
        }
    }
});

defineRule("testPersistentGlobalWrite", {
    whenChanged: ["vdev/write"],
    then: function() {
        ps = new PersistentStorage("test_storage", {global: true});
        ps["key1"] = 42;
        ps["key2"] = "HelloWorld";
        ps["obj"] = { name: "MyObj", foo: "bar", baz: 84 };
        log("write objects " + JSON.stringify(ps["key1"]) + ", " + JSON.stringify(ps["key2"]) + ", " + JSON.stringify(ps["obj"]))
    }
});

defineRule("testPersistentGlobalRead", {
    whenChanged: ["vdev/read"],
    then: function() {
        ps = new PersistentStorage("test_storage", {global: true});
        log("read objects " + JSON.stringify(ps["key1"]) + ", " + JSON.stringify(ps["key2"]) + ", " + JSON.stringify(ps["obj"]))
    }
});
