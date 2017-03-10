exports.count = function() {
    if (module.static.counter == undefined) {
        module.static.counter = 0;
    }

    module.static.counter++;
    log("Value: {}", module.static.counter)
};

log("Module static init");
