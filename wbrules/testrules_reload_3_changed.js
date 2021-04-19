// the same code as in testrules_reload_3.js,
// but it should cause script reloading

defineVirtualDevice("testNulledControl", {
    cells: {
        pers_text: {
            type: "text",
            readonly: false,
            forceDefault: true,
            value: ""
        },
        trigger: {
            type: "pushbutton"
        }
    }
});

defineRule({
    whenChanged: "testNulledControl/trigger", 
    then: function() {
        log.info("before: {}".format(dev["testNulledControl"]["pers_text"]))
        dev["testNulledControl"]["pers_text"] = "someTextString"
        log.info("after: {}".format(dev["testNulledControl"]["pers_text"]))
    }
});;
