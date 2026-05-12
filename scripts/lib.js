// rule engine runtime

// this function runs once the context is created
// for each script
function __esInitEnv(glob) {
  glob.String.prototype.format = function () {
    var args = [this];
    for (var i = 0; i < arguments.length; ++i) args.push(arguments[i]);
    return format.apply(null, args);
  };

  glob.String.prototype.xformat = function () {
    var parts = this.split(/\\\{/g),
      i = 0,
      args = Array.prototype.slice.apply(arguments);
    return parts
      .map(function (part) {
        return part
          .replace(/\{\{(.*?)\}\}/g, function (all, expr) {
            try {
              return eval(expr);
            } catch (e) {
              return '<eval failed: ' + expr + ': ' + e + '>';
            }
          })
          .replace(/\{\}/g, function () {
            return i < args.length ? args[i++] : '';
          });
      })
      .join('{');
  };
}

__esInitEnv(global);

var _WbRules = {
  requireCompleteCells: 0,
  timers: {},
  aliases: {},

  CronEntry: function (spec) {
    if (typeof spec != 'string') throw new Error('invalid cron spec');
    this.spec = spec;
  },

  IncompleteCellCaught: (function () {
    function IncompleteCellCaught(cellName) {
      this.name = 'IncompleteCellCaught';
      this.message = 'incomplete cell encountered: ' + cellName;
    }
    IncompleteCellCaught.prototype = Object.create(Error.prototype);
    return IncompleteCellCaught;
  })(),

  autoload: function (target, acquire) {
    return new Proxy(target, {
      get: function (o, name) {
        if (!(name in o)) {
          o[name] = acquire(name, o);
        }
        return o[name];
      },
      set: function (o, name, value) {
        throw new Error('setting unsupported proxy value: ' + name);
      },
    });
  },

  getDevValue: function getDevValue(o, name) {
    var slashPosition = name.indexOf('/');
    if (slashPosition > 0 && slashPosition < name.length - 1) {
      var target = _WbRules.getDevValue(o, name.slice(0, slashPosition));
      return target[name.slice(slashPosition + 1)];
    }

    if (name in o) return o[name];

    return (o[name] = new Proxy(_wbDevObject(name), {
      get: function (dev, name) {
        var sharpPosition = name.indexOf('#');
        var metaField = '';
        if (sharpPosition > 0 && sharpPosition < name.length - 1) {
          metaField = name.slice(sharpPosition + 1);
          name = name.slice(0, sharpPosition);
        }
        var cell = _wbCellObject(dev, name);
        if (_WbRules.requireCompleteCells && !cell.isComplete())
          throw new _WbRules.IncompleteCellCaught(name);
        if (metaField !== '') {
          var m = cell.getMeta();
          if (m !== null) {
            return m[metaField];
          } else {
            return null;
          }
        }
        return cell.value().v;
      },
      set: function (dev, name, value) {
        var sharpPosition = name.indexOf('#');
        var metaField = '';
        if (sharpPosition > 0 && sharpPosition < name.length - 1) {
          metaField = name.slice(sharpPosition + 1);
          name = name.slice(0, sharpPosition);
        }
        if (metaField !== '') {
          _wbCellObject(dev, name).setMeta({ k: metaField, v: value });
        } else {
          _wbCellObject(dev, name).setValue({ v: value });
        }
      },
    }));
  },

  setDevValue: function setDevValue(o, name, value) {
    var slashPosition = name.indexOf('/');
    if (slashPosition > 0 && slashPosition < name.length - 1) {
      var target = _WbRules.getDevValue(o, name.slice(0, slashPosition));
      target[name.slice(slashPosition + 1)] = value;
    } else throw new Error('setting unsupported proxy value: ' + name);
  },

  parseCellRef: function parseCellRef(cellRef) {
    var m = cellRef.match(/([^\/]+)+\/([^\/]+)+$/);
    if (!m) throw new Error('invalid cell reference');
    return {
      device: m[1],
      control: m[2],
    };
  },

  defineAlias: function (name, cellRef) {
    if (!name || !cellRef) throw new Error('invalid alias definition');
    var ref = _WbRules.parseCellRef(cellRef);
    _WbRules.aliases[name] = cellRef;
    var d = null;
    Object.defineProperty(
      (function () {
        return this;
      })(),
      name,
      {
        configurable: true,
        get: function () {
          if (!d) d = dev[ref.device];
          return d[ref.control];
        },
        set: function (value) {
          if (!d) d = dev[ref.device];
          d[ref.control] = value;
        },
      }
    );
  },

  defineRule: function (arg1, arg2) {
    var name, def;

    // anonymous rule handling
    if (arg2 == undefined) {
      name = '';
      def = arg1;
    } else {
      name = arg1;
      def = arg2;
    }

    debug('defineRule: {}'.format(name == '' ? '(anon)' : name));
    if (typeof name != 'string' || typeof def != 'object')
      throw new Error('invalid rule definition');

    function wrapConditionFunc(f, incompleteValue) {
      var conv =
        typeof incompleteValue == 'boolean'
          ? function (v) {
              return !!v;
            }
          : function (v) {
              return v;
            };
      return function () {
        _WbRules.requireCompleteCells++;
        try {
          return conv(f.apply(d, arguments));
        } catch (e) {
          if (e instanceof _WbRules.IncompleteCellCaught) {
            debug('skipping rule due to incomplete cell ' + name + ': ' + e);
            return incompleteValue;
          }
          throw e;
        } finally {
          _WbRules.requireCompleteCells--;
        }
      };
    }

    var d = Object.create(def);
    function transformWhenChangedItem(item) {
      if (typeof item == 'string') {
        if (item.indexOf('/') >= 0) return item;
        if (!_WbRules.aliases.hasOwnProperty(item))
          throw new Error('invalid cell alias in whenChanged: ' + item);
        return _WbRules.aliases[item];
      }
      if (typeof item != 'function') throw new Error('invalid whenChanged spec');
      return wrapConditionFunc(item, undefined);
    }

    // when: cron("...") is converted to cron: "..."
    if (def.hasOwnProperty('when') && def.when instanceof _WbRules.CronEntry) {
      def._cron = def.when.spec;
      delete def.when;
    }

    Object.keys(def).forEach(function (k) {
      var orig = d[k];
      switch (k) {
        case 'readonly':
          d[k] = !!d[k]; // avoid type cast error on the Go side
          break;
        case 'asSoonAs':
        case 'when':
          d[k] = wrapConditionFunc(orig, false);
          break;
        case 'whenChanged':
          if (Array.isArray(orig)) d[k] = orig.map(transformWhenChangedItem);
          else d[k] = transformWhenChangedItem(orig);
          break;
        case 'then':
          d[k] = function (options) {
            if (options) {
              if (options.hasOwnProperty('device'))
                // TBD: pass options.oldValue right after newValue here -- for consistency
                orig.call(d, options.newValue, options.device, options.cell);
              else orig.call(d, options.newValue);
            } else orig.call(d);
          };
      }
    });

    if (name == '') {
      return _wbDefineRule(d);
    } else {
      return _wbDefineRule(name, d);
    }
  },

  startTimer: function startTimer(name, ms, periodic) {
    _wbStartTimer(name, ms, !!periodic);
  },
};

var dev = new Proxy(
  {},
  {
    get: _WbRules.getDevValue,
    set: _WbRules.setDevValue,
  }
);

var timers = _WbRules.autoload(_WbRules.timers, function (name) {
  return {
    get firing() {
      return _wbCheckCurrentTimer(name);
    },
    stop: function () {
      _wbStopTimer(name);
    },
  };
});

var defineRule = _WbRules.defineRule;

function startTimer(name, ms) {
  _WbRules.startTimer(name, ms, false);
}

function startTicker(name, ms) {
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
  if (typeof options == 'function')
    options = {
      exitCallback: options,
      captureOutput: false,
      captureErrorOutput: false,
    };
  else if (!options)
    options = {
      exitCallback: null,
      captureOutput: false,
      captureErrorOutput: false,
    };
  else {
    if (!options.hasOwnProperty('captureOutput')) options.captureOutput = false;
    if (!options.hasOwnProperty('captureErrorOutput')) options.captureErrorOutput = false;

    var keys = Object.keys(options);
    var knownOptions = ['captureOutput', 'captureErrorOutput', 'input', 'exitCallback'];
    for (var i = 0; i < keys.length; i++) {
      if (knownOptions.indexOf(keys[i]) < 0) {
        log.warning('spawn: unknown option: ' + keys[i]);
      }
    }
  }

  if (options.input != null) options.input = '' + options.input;

  _wbSpawn(
    [cmd].concat(args || []),
    options.exitCallback
      ? function (args) {
          try {
            options.exitCallback(
              args.exitStatus,
              options.captureOutput ? args.capturedOutput : null,
              args.capturedErrorOutput
            );
          } catch (e) {
            log('error running command callback for ' + cmd + ': ' + (e.stack || e));
          }
        }
      : null,
    !!options.captureOutput,
    !!options.captureErrorOutput,
    options.input
  );
}

function runShellCommand(cmd, options) {
  spawn('/bin/sh', ['-c', cmd], options);
}

var defineAlias = _WbRules.defineAlias;

function cron(spec) {
  return new _WbRules.CronEntry(spec);
}

global.StorableObject = function (obj, ps, pskey) {
  if (pskey === undefined) {
    pskey = '';
    ps = null;
  }

  // check if this object already has a prototype
  if (obj._ps !== undefined && ps !== null) {
    // just append new listener to the list
    obj._ps.push({
      s: ps,
      k: pskey,
    });
    return obj;
  }

  // set new prototype for this object
  var p = {
    _ps: [],
    _psself: null,
  };
  p.__proto__ = obj.__proto__;

  obj.__proto__ = p;

  if (ps !== null) {
    obj._ps.push({
      s: ps,
      k: pskey,
    });
  }

  var p = new Proxy(obj, {
    get: function (obj, key) {
      var val = obj[key];
      if (typeof val === 'object') {
        return new StorableObject(val, obj._psself, key);
      }
      return val;
    },
    set: function (o, key, value) {
      if (key === '_psself') {
        o._psself = value;
        return true;
      }

      // check if value is an object without StorableObject's prototype
      if (typeof value === 'object' && value._ps === undefined) {
        throw new Error(
          "don't write pure objects to PersistentStorage, use new StorableObject(obj) instead"
        );
      }

      o[key] = value;

      // write updated object to all listeners
      var len = o._ps.length;
      for (var i = 0; i < len; i++) {
        ps = o._ps[i];

        // update is written here
        ps.s[ps.k] = o._psself;
      }
    },
    enumerate: function (o) {
      var keys = Object.keys(o);
      keys.splice(keys.indexOf('_psself'), 1);
      return keys;
    },
  });

  p._psself = p;
  return p;
};

global.PersistentStorage = function (name, options) {
  var p = new Proxy(
    { name: _wbPersistentName(name, options), _psself: null },
    {
      get: function (o, key) {
        var val = _wbPersistentGet(o.name, key);
        if (typeof val === 'object') {
          val = new StorableObject(val, o._psself, key);
        }
        return val;
      },
      set: function (o, key, value) {
        if (key === '_psself') {
          o._psself = value;
          return true;
        }

        // typeof null is 'object', so check for null separately
        if (value !== null) {
          // check if this value is an object without StorableObject's prototype
          if (typeof value === 'object' && value._ps === undefined) {
            throw new Error(
              "don't write pure objects to PersistentStorage, use new StorableObject(obj) instead"
            );
          } else if (typeof value === 'object') {
            // check if this storage is not a listener for the object
  
            var len = value._ps.length;
            var found = false;
            for (var i = 0; i < len; i++) {
              if (value._ps[i].p == o._psself && value._ps[i].k == key) {
                found = true;
                break;
              }
            }
            if (!found) {
              value._ps.push({
                s: o._psself,
                k: key,
              });
            }
          }
        }

        return _wbPersistentSet(o.name, key, value);
      },
    }
  );

  p._psself = p;

  return p;
};

__wbVdevPrototype.getCellValue = function (cell) {
  return dev[this.getCellId(cell)];
};

__wbVdevPrototype.setCellValue = function (cell, value) {
  dev[this.getCellId(cell)] = value;
};

__wbVdevPrototype.publish = function (topic, message) {
  publish('/devices/' + this.__deviceId + '/' + topic, message);
};

var Notify = require("wb-notify");
var Alarms = require("wb-alarms");
