defineRule("testPersistentLocalWrite", {
    whenChanged: "vdev/localWrite",
    then: function() {
        var ps = module.PersistentStorage("test_local");
        ps["key2"] = "hello_from_2";
        log("file2: write to local PS");
    }
});

defineRule("testPersistentLocalRead", {
    whenChanged: "vdev/localRead",
    then: function() {
        var ps = module.PersistentStorage("test_local");
        log("file2: read objects " + JSON.stringify(ps["key1"]) + ", " + JSON.stringify(ps["key2"]));
    }
});
