defineVirtualDevice("test", {
    cells: {
        test: {
            type: "pushbutton"
        },
        isolation: {
            type: "pushbutton"
        },
        sync: {
            type: "pushbutton"
        }
    }
});


defineRule({
    whenChanged: "test/test",
    then: function() {
        log("it works!");
    }
});

global.myvar = 42;

function adder(a, b) {
    return a + b;
}

defineRule({
    whenChanged: "test/isolation",
    then: function() {
        log("1: myvar: {}", global.myvar);
        log("1: add {} and {}: {}", 2, 3, adder(2, 3));
    }
});
