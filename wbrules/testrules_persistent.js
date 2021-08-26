// -*- mode: js2-mode -*-

defineVirtualDevice("vdev", {
    cells: {
        write: {
            type: "switch",
            value: false,
            forceDefault: true
        },
        read: {
            type: "switch",
            value: false,
            forceDefault: true
        },
        localWrite1: {
            type: "switch",
            value: false,
            forceDefault: true
        },
        localRead1: {
            type: "switch",
            value: false,
            forceDefault: true
        },
        localWrite2: {
            type: "switch",
            value: false,
            forceDefault: true
        },
        localRead2: {
            type: "switch",
            value: false,
            forceDefault: true
        }
    }
});

defineRule("testPersistentGlobalWrite", {
    whenChanged: ["vdev/write"],
    then: function() {
        var ps = new PersistentStorage("test_storage", { global: true });

        // try to write a pure object to persistent storage - must get an error
        try {
                ps["pure"] = { name: "pure object", foo: "baz" };
                log("pure object created successfully!");
        } catch (e) {
                log("pure object is not created");
        }

        var obj = StorableObject({ name: "MyObj", foo: "bar", baz: 126 });

        ps["key1"] = 42;
        ps["key2"] = "HelloWorld";
        ps["obj"] = obj;

        // post-write value to object
        obj.baz = 84;

        // create subobject for object
        // also try to create a pure object
        try {
                obj.pure_sub = { name: "another pure" };
                log("pure subobject created successfully!");
        } catch (e) {
                log("pure subobject is not created");
        }

        // create a correct subobject
        obj.sub = StorableObject({
               hello: "world",
        });

        log("write objects " + JSON.stringify(ps["key1"]) + ", " + JSON.stringify(ps["key2"]) + ", " + JSON.stringify(ps["obj"]));
    }
});

defineRule("testPersistentLocalWrite", {
    whenChanged: "vdev/localWrite1",
    then: function() {
        var ps = new PersistentStorage("test_local");
        ps["key1"] = "hello_from_1";
        log("file1: write to local PS");
    }
});

defineRule("testPersistentLocalRead", {
    whenChanged: "vdev/localRead1",
    then: function() {
        var ps = new PersistentStorage("test_local");
        log("file1: read objects " + JSON.stringify(ps["key1"]) + ", " + JSON.stringify(ps["key2"]));
    }
});

log("loaded file 1");
