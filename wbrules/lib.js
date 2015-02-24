// rule engine runtime

var _WbRules = {
  ruleMap: {},

  ruleNames: [],

  autoload: function (target, acquire) {
    return new Proxy(target, {
      get: function(o, name) {
        if (!(name in o)) {
          o[name] = acquire(name, o);
        }
        return o[name];
      }
    });
  },

  wrapDevice: function (name) {
    return _WbRules.autoload(_wbDevObject(name), _WbRules.wrapCell);
  },

  wrapCell: function (name, dev) {
    var cell = _wbCellObject(dev, name);
    return {
      get s () {
        return cell.rawValue();
      },
      set s (value) {
        cell.setValue({ v: "" + value });
      },
      get v () {
        return cell.value().v; // FIXME (extra wrap due to PushJSObject limitations)
      },
      set v (value) {
        cell.setValue({ v: value });
      },
      get b () {
        return !!cell.value().v; // FIXME (extra wrap due to PushJSObject limitations)
      },
      set b (value) {
        cell.setValue({ v: !!value });
      }
    };
  },

  defineRule: function (name, def) {
    if (typeof name != "string" || !def)
      throw new Error("invalid rule definition");

    if (!_WbRules.ruleMap.hasOwnProperty(name))
      _WbRules.ruleNames.push(name);
    _WbRules.ruleMap[name] = def;
  },

  runRules: function () {
    alert("runRules()");
    _WbRules.ruleNames.forEach(function (name) {
      alert("running rule: " + name);
      var rule = _WbRules.ruleMap[name];
      try {
        if (rule.when()) {
          alert("rule fired: " + name);
          rule.then();
        }
      } catch (e) {
        alert("error running rule " + name + ": " + e.stack || e);
      }
    });
  }
};

var dev = _WbRules.autoload({}, _WbRules.wrapDevice);
defineRule = _WbRules.defineRule;
runRules = _WbRules.runRules;
