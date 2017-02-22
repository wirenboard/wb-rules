// -*- mode: js2-mode -*-

defineVirtualDevice("vdev", {
    cells: {
        write: {
            type: "switch",
            value: false
        },
        read: {
            type: "switch",
            value: false
        },
        localWrite: {
            type: "switch",
            value: false
        },
        localRead: {
            type: "switch",
            value: false
        }
    }
});

defineRule("testPersistentGlobalWrite", {
    whenChanged: ["vdev/write"],
    then: function() {
        var ps = PersistentStorage("test_storage");
        ps["key1"] = 42;
        ps["key2"] = "HelloWorld";
        ps["obj"] = { name: "MyObj", foo: "bar", baz: 84 };
        log("write objects " + JSON.stringify(ps["key1"]) + ", " + JSON.stringify(ps["key2"]) + ", " + JSON.stringify(ps["obj"]));
    }
});

defineRule("testPersistentGlobalRead", {
    whenChanged: ["vdev/read"],
    then: function() {
        var ps = PersistentStorage("test_storage");
        log("read objects " + JSON.stringify(ps["key1"]) + ", " + JSON.stringify(ps["key2"]) + ", " + JSON.stringify(ps["obj"]));
    }
});

defineRule("testPersistentLocalWrite", {
    whenChanged: "vdev/localWrite",
    then: function() {
        var ps = module.PersistentStorage("test_local");
        ps["key1"] = "hello_from_1";
        log("file1: write to local PS");
    }
});

defineRule("testPersistentLocalRead", {
    whenChanged: "vdev/localRead",
    then: function() {
        var ps = module.PersistentStorage("test_local");
        log("file1: read objects " + JSON.stringify(ps["key1"]) + ", " + JSON.stringify(ps["key2"]));
    }
});
