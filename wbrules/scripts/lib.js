// rule engine runtime
var _WbRules = {
  ruleMap: {},
  ruleNames: [],
  requireCompleteCells: 0,
  timers: {},

  IncompleteCellError: (function () {
    function IncompleteCellError(cellName) {
      this.name = "IncompleteCellError";
      this.message = "incomplete cell encountered: " + cellName;
    }
    IncompleteCellError.prototype = Object.create(Error.prototype);
    return IncompleteCellError;
  })(),

  autoload: function (target, acquire, setValue) {
    return new Proxy(target, {
      get: function (o, name) {
        if (!(name in o)) {
          o[name] = acquire(name, o);
        }
        return o[name];
      },
      set: function (o, name, value) {
        throw new Error("setting unsupported proxy value: " + name);
      }
    });
  },

  wrapDevice: function (name) {
    var cells = {};
    function ensureCell (dev, name) {
      return cells.hasOwnProperty(name) ?
        cells[name] :
        cells[name] = _wbCellObject(dev, name);
    }
    return new Proxy(_wbDevObject(name), {
      get: function (dev, name) {
        var cell = ensureCell(dev, name);
        if (_WbRules.requireCompleteCells && !cell.isComplete())
          throw new _WbRules.IncompleteCellError(name);
        return cell.value().v;
      },
      set: function (dev, name, value) {
        ensureCell(dev, name).setValue({ v: value });
      }
    });
  },

  defineRule: function (name, def) {
    if (typeof name != "string" || !def)
      throw new Error("invalid rule definition");

    if (!_WbRules.ruleMap.hasOwnProperty(name))
      _WbRules.ruleNames.push(name);
    def.cached = null;
    _WbRules.ruleMap[name] = def;
  },

  runRules: function () {
    debug("runRules()");
    _WbRules.ruleNames.forEach(function (name) {
      debug("checking rule: " + name);
      var rule = _WbRules.ruleMap[name];
      try {
        _WbRules.requireCompleteCells++;
        try {
          var shouldFire;
          if (rule.asSoonAs) {
            var cur = rule.asSoonAs();
            shouldFire = cur && (!rule.cached || !!rule.cached.value != !!cur);
            debug((shouldFire ? "(firing)" : "(not firing)") + "caching rule value: " + name + ": " + !!cur);
            if (rule.cached) {
              rule.cached.value = !!cur;
            } else {
              rule.cached = { value: !!cur };
            }
          } else
            shouldFire = !!rule.when();
        } catch (e) {
          if (e instanceof _WbRules.IncompleteCellError) {
            debug("skipping rule due to incomplete cells " + name + ": " + e);
            return;
          }
          throw e;
        } finally {
          _WbRules.requireCompleteCells--;
        }
        if (shouldFire) {
          debug("rule fired: " + name);
          rule.then();
        }
      } catch (e) {
        debug("error running rule " + name + ": " + e.stack || e);
      }
    });
  },

  runTimer: function runTimer(name) {
    debug("runTimer(): " + name);
    if (!_WbRules.timers.hasOwnProperty(name)) {
      debug("WARNING: unknown timer fired: " + name);
      return;
    }
    _WbRules.timers[name]._fire();
  },

  startTimer: function startTimer(name, ms, periodic) {
    if (_WbRules.timers.hasOwnProperty(name))
      _WbRules.timers[name].stop();
    debug("starting timer: " + name);
    _WbRules.timers[name] = {
      firing: false,
      stop: function () {
        _wbStopTimer(name);
        debug("deleting timer: " + name);
        delete _WbRules.timers[name];
      },
      _fire: function () {
        this.firing = true;
        try {
          _WbRules.runRules();
        } finally {
          this.firing = false;
        }
      }
    };
    _wbStartTimer(name, ms, !!periodic);
  }
};

var dev = _WbRules.autoload({}, _WbRules.wrapDevice);
var timers = _WbRules.autoload(_WbRules.timers, function () {
  return {
    firing: false,
    stop: function () {}
  };
});

defineRule = _WbRules.defineRule;
runRules = _WbRules.runRules;
_runTimer = _WbRules.runTimer;

function startTimer (name, ms) {
  _WbRules.startTimer(name, ms, false);
}

function startTicker (name, ms) {
  _WbRules.startTimer(name, ms, true);
}
