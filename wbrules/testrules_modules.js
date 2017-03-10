defineVirtualDevice("test", {
    cells: {
        helloworld: {
            type: "switch",
            value: false
        },
        multifile: {
            type: "switch",
            value: false
        },
        error: {
            type: "switch",
            value: false
        },
        cross: {
            type: "switch",
            value: false
        },
        params: {
            type: "switch",
            value: false
        },
        static: {
            type: "switch",
            value: false
        },
        cache: {
            type: "switch",
            value: false
        }
    }
});

defineRule("helloworld", {
    whenChanged: "test/helloworld",
    then: function() {
        var m = require("test/helloworld");
        // var m = {hello: 42};
        log("Required module value:", m.hello);
        log("Function test:", m.adder(10, 20));
    }
});

defineRule("error", {
    whenChanged: "test/error",
    then: function() {
        try {
            var m = require("notfound");
            log("ERROR: Found non-existing module");
        } catch (e) {
            log("Module not found");
        }
    }
});

defineRule("multiple_require", {
    whenChanged: "test/multifile",
    then: function() {
        var m = require("test/multi_init");
        log("[1] My value of multi_init:", m.value);
    }
});

defineRule("cross-dep", {
    whenChanged: "test/cross",
    then: function() {
        require("test/with_require");
        log("Module loaded");
    }
});

defineRule("params", {
    whenChanged: "test/params",
    then: function() {
        var m = require("test/params");
        log(m.params());
    }
});

defineRule("static", {
    whenChanged: "test/static",
    then: function() {
        var m = require("test/static");
        m.count();
    }
});

defineRule("cache1", {
    whenChanged: "test/cache",
    then: function() {
        var m = require("test/helloworld");
        log("Value: {}", m.hello);
    }
});

defineRule("cache2", {
    whenChanged: "test/cache",
    then: function() {
        var m = require("test/helloworld");
        log("Value: {}", m.hello);
    }
});
