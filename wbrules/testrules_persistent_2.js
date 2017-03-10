defineRule("testPersistentGlobalRead", {
    whenChanged: ["vdev/read"],
    then: function() {
        var ps = new PersistentStorage("test_storage", { global: true });
        log("read objects " + JSON.stringify(ps["key1"]) + ", " + JSON.stringify(ps["key2"]) + ", " + JSON.stringify(ps["obj"]));
    }
});

defineRule("testPersistentLocalWrite", {
    whenChanged: "vdev/localWrite",
    then: function() {
        var ps = new PersistentStorage("test_local");
        ps["key2"] = "hello_from_2";
        log("file2: write to local PS");
    }
});

defineRule("testPersistentLocalRead", {
    whenChanged: "vdev/localRead",
    then: function() {
        var ps = new PersistentStorage("test_local");
        log("file2: read objects " + JSON.stringify(ps["key1"]) + ", " + JSON.stringify(ps["key2"]));
    }
});
