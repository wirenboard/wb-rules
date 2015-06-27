// -*- mode: js2-mode -*-

// The location of device "misc" is testrules_locations.js:4
defineSomeDevice("misc");

// The location of the rule "whatever" is testrules_locations.js:7
defineSomeRule("whatever");

function defBarDev(name) {
  defineSomeDevice(name + "Bar");
}

// The location of the rule "fooBar" is testrules_locations.js:14
defineSomeDevice("foo");

// The location of the rule "another" is testrules_locations.js:24 (the end of the defineRule call)
defineRule("another", {
  asSoonAs: function () {
    return !!dev.somedev.another;
  },
  then: function () {
    log("another!");
  }
});
