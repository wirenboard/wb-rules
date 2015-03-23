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

  runRules: function (cellName) {
    debug("runRules(): " + (cellName ? "cell changed: " + cellName : "(no cells changed)"));
    _WbRules.ruleNames.forEach(function (name) {
      debug("checking rule: " + name);
      var rule = _WbRules.ruleMap[name], thenArgs = null;
      if (typeof rule.then != "function") {
        log("invalid rule " + name + ": no proper 'then' clause");
        return;
      }
      try {
        _WbRules.requireCompleteCells++;
        try {
          var shouldFire = false;
          if (rule.onCellChange) {
            if (typeof rule.onCellChange == "string")
              shouldFire = rule.onCellChange == cellName;
            else if (rule.onCellChange.indexOf)
              shouldFire = cellName && rule.onCellChange.indexOf(cellName) >= 0;
            else
              log("invalid onCellChange value in rule " + name);
            if (shouldFire) {
              var p = cellName.indexOf("/");
              if (p < 0) {
                log("INTERNAL ERROR -- invalid cell name: " + cellName);
                return;
              }
              var devName = cellName.substring(0, p),
                  actualCellName = cellName.substring(p + 1);
              // this will cause IncompleteCellError if the cell is not complete
              var value = dev[devName][actualCellName];
              thenArgs = [ devName, actualCellName, value ];
            }
          } else if (typeof rule.asSoonAs == "function") {
            var cur = rule.asSoonAs();
            shouldFire = cur && (!rule.cached || !!rule.cached.value != !!cur);
            debug((shouldFire ? "(firing)" : "(not firing)") + "caching rule value: " + name + ": " + !!cur);
            if (rule.cached) {
              rule.cached.value = !!cur;
            } else {
              rule.cached = { value: !!cur };
            }
          } else if (typeof rule.when == "function") {
            shouldFire = !!rule.when();
          } else {
            log("invalid rule " + name + " -- no proper condition clause");
          }
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
          if (!thenArgs)
            rule.then();
          else
            rule.then.apply(rule, thenArgs);
        }
      } catch (e) {
        log("error running rule " + name + ": " + e.stack || e);
      }
    });
  },

  startTimer: function startTimer(name, ms, periodic) {
    if (_WbRules.timers.hasOwnProperty(name))
      _WbRules.timers[name].stop();
    debug("starting timer: " + name);
    var timer = _WbRules.timers[name] = {
      firing: false,
      stop: function () {
        if (!this.id)
          return;
        _wbStopTimer(this.id);
        debug("deleting timer: " + name);
        delete _WbRules.timers[name];
        this.id = null;
      },
      _fire: function () {
        this.firing = true;
        try {
          _WbRules.runRules();
        } finally {
          if (!periodic)
            delete _WbRules.timers[name];
          this.firing = false;
        }
      }
    };
    timer.id =_wbStartTimer(timer._fire.bind(timer), ms, !!periodic);
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

function startTimer (name, ms) {
  _WbRules.startTimer(name, ms, false);
}

function startTicker (name, ms) {
  _WbRules.startTimer(name, ms, true);
}

function setTimeout(callback, ms) {
  return _wbStartTimer(callback, ms, false);
}

function setInterval(callback, ms) {
  return _wbStartTimer(callback, ms, true);
}

function clearTimeout(id) {
  _wbStopTimer(id);
}

function clearInterval(id) {
  clearTimeout(id);
}

function runShellCommand(cmd, options) {
  if (typeof options == "function")
    options = {
      exitCallback: options,
      captureOutput: false,
      captureErrorOutput: false
    };
  else if (!options)
    options = {
      exitCallback: null,
      captureOutput: false,
      captureErrorOutput: false
    };
  else {
    if (!options.hasOwnProperty("captureOutput"))
      options.captureOutput = false;
    if (!options.hasOwnProperty("captureErrorOutput"))
      options.captureErrorOutput = false;
  }

  _wbShellCommand(cmd, options.exitCallback ? function (args) {
    try {
      options.exitCallback(
        args.exitStatus,
        options.captureOutput ? args.capturedOutput : null,
        args.capturedErrorOutput
      );
    } catch (e) {
      log("error running command callback for " + cmd + ": " + e.stack || e);
    }
  } : null, !!options.captureOutput, !!options.captureErrorOutput);
}
