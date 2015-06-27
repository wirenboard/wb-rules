// -*- mode: js2-mode -*-

// When a rule is defined inside a module the editor must use the
// topmost stack frame in the rule file to determine the location of
// the definition, even if some helper functions are used to define
// rules or devices.

function defineSomeRule(name) {
  var ruleName = name + "Rule";
  defineRule(ruleName, {
    asSoonAs: function () {
      return !!dev.somedev[name];
    },
    then: function () {
      log("{} fired", ruleName);
    }
  });
}

function defineSomeDevice(name) {
  defineVirtualDevice(name, {
    title: name,
    cells: {
      sw: {
        type: "switch",
        value: false
      }
    }
  });
}
