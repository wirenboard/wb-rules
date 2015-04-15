// rule engine runtime
var _WbRules = {
  requireCompleteCells: 0,
  timers: {},
  aliases: {},

  IncompleteCellCaught: (function () {
    function IncompleteCellCaught(cellName) {
      this.name = "IncompleteCellCaught";
      this.message = "incomplete cell encountered: " + cellName;
    }
    IncompleteCellCaught.prototype = Object.create(Error.prototype);
    return IncompleteCellCaught;
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
          throw new _WbRules.IncompleteCellCaught(name);
        return cell.value().v;
      },
      set: function (dev, name, value) {
        ensureCell(dev, name).setValue({ v: value });
      }
    });
  },

  defineAlias: function (name, fullName) {
    if (!name || !fullName)
      throw new Error("invalid alias definition");
    var m = fullName.match(/([^\/]+)+\/([^\/]+)+$/);
    if (!m)
      throw new Error("invalid cell full name for alias");
    _WbRules.aliases[name] = fullName;
    var devName = m[1], cellName = m[2], d = null;
    Object.defineProperty(
      (function () { return this; })(),
      name,
      {
        get: function () {
          if (!d)
            d = dev[devName];
          return d[cellName];
        },
        set: function (value) {
          if (!d)
            d = dev[devName];
          d[cellName] = value;
        }
      });
  },

  defineRule: function (name, def) {
    debug("defineRule: " + name);
    if (typeof name != "string" || typeof def != "object")
      throw new Error("invalid rule definition");

    function wrapConditionFunc (f, incompleteValue) {
      var conv = typeof incompleteValue == "boolean" ?
            function (v) { return !!v; } : function (v) { return v; };
      return function () {
        _WbRules.requireCompleteCells++;
        try {
          return conv(f.apply(d, arguments));
        } catch (e) {
          if (e instanceof _WbRules.IncompleteCellCaught) {
            debug("skipping rule due to incomplete cell " + name + ": " + e);
            return incompleteValue;
          }
          throw e;
        } finally {
          _WbRules.requireCompleteCells--;
        }
      };
    }

    var d = Object.create(def);
    function transformWhenChangedItem (item) {
      if (typeof item == "string") {
        if (item.indexOf("/") >= 0)
          return item;
        if (!_WbRules.aliases.hasOwnProperty(item))
          throw new Error("invalid cell alias in whenChanged: " + item);
        return _WbRules.aliases[item];
      }
      if (typeof item != "function")
        throw new Error("invalid whenChanged spec");
      return wrapConditionFunc(item, undefined);
    }

    Object.keys(def).forEach(function (k) {
      var orig = d[k];
      switch(k) {
      case "asSoonAs":
      case "when":
        d[k] = wrapConditionFunc(orig, false);
        break;
      case "whenChanged":
        if (Array.isArray(orig))
          d[k] = orig.map(transformWhenChangedItem);
        else
          d[k] = transformWhenChangedItem(orig);
        break;
      case "then":
        d[k] = function (options) {
          if (options) {
            if (options.hasOwnProperty("device"))
              // TBD: pass options.oldValue right after newValue here -- for consistency
              orig.call(d, options.newValue, options.device, options.cell);
            else
              orig.call(d, options.newValue);
          } else
            orig.call(d);
        };
      }
    });
    _wbDefineRule(name, d);
  },

  startTimer: function startTimer(name, ms, periodic) {
    debug("starting timer: " + name);
    _wbStartTimer(name, ms, !!periodic);
  }
};

var dev = _WbRules.autoload({}, _WbRules.wrapDevice);
var timers = _WbRules.autoload(_WbRules.timers, function (name) {
  return {
    get firing() {
      return _wbCheckCurrentTimer(name);
    },
    stop: function () {
      _wbStopTimer(name);
    }
  };
});

defineRule = _WbRules.defineRule;

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

function spawn(cmd, args, options) {
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

  if (options.input != null)
    options.input = "" + options.input;

  _wbSpawn([cmd].concat(args || []), options.exitCallback ? function (args) {
    try {
      options.exitCallback(
        args.exitStatus,
        options.captureOutput ? args.capturedOutput : null,
        args.capturedErrorOutput
      );
    } catch (e) {
      log("error running command callback for " + cmd + ": " + (e.stack || e));
    }
  } : null, !!options.captureOutput, !!options.captureErrorOutput, options.input);
}

function runShellCommand(cmd, options) {
  spawn("/bin/sh", ["-c", cmd], options);
}

defineAlias = _WbRules.defineAlias;

String.prototype.format = function () {
  var args = [ this ];
  for (var i = 0; i < arguments.length; ++i)
    args.push(arguments[i]);
  return format.apply(null, args);
};
