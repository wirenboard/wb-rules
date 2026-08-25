// rule engine runtime

// builtins installed by the Go side (wbrules/esengine.go) before this file
// is evaluated
/* global _wbDefineRule, _wbDevObject, _wbCellObject,
   _wbStartTimer, _wbStopTimer, _wbCheckCurrentTimer, _wbSpawn,
   _wbPersistentName, _wbPersistentGet, _wbPersistentSet,
   __wbGlobalPrototype, __wbModulePrototype, __wbVdevPrototype,
   __wbVdevCellPrototype, log, trackMqtt, require */
// the runtime is built around dictionary tables (timers, aliases, waiters);
// dynamic keys are the design, not an injection surface
/* eslint-disable security/detect-object-injection */
// created here for the Go side (realm construction) and for rule scripts
/* exported __wbGlobalPrototype, __wbModulePrototype, __wbVdevCellPrototype, sleep */

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

    var devObject = _wbDevObject(name);

    var cells = Object.create(null);
    function cellObject(cellName) {
      var cell = cells[cellName];
      if (cell === undefined) cell = cells[cellName] = _wbCellObject(devObject, cellName);
      return cell;
    }

    return (o[name] = new Proxy(devObject, {
      get: function (dev, name) {
        var sharpPosition = name.indexOf('#');
        var metaField = '';
        if (sharpPosition > 0 && sharpPosition < name.length - 1) {
          metaField = name.slice(sharpPosition + 1);
          name = name.slice(0, sharpPosition);
        }
        var cell = cellObject(name);
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
          cellObject(name).setMeta({ k: metaField, v: value });
        } else {
          cellObject(name).setValue({ v: value });
        }
        return true;
      },
    }));
  },

  setDevValue: function setDevValue(o, name, value) {
    var slashPosition = name.indexOf('/');
    if (slashPosition > 0 && slashPosition < name.length - 1) {
      var target = _WbRules.getDevValue(o, name.slice(0, slashPosition));
      target[name.slice(slashPosition + 1)] = value;
      return true; // strict-mode callers throw if a set trap returns falsy
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
    var r = _WbRules.prepareRuleDef(arg1, arg2);
    return r.name === '' ? _wbDefineRule(r.def) : _wbDefineRule(r.name, r.def);
  },

  // transforms defineRule arguments into the engine-ready definition;
  // the caller invokes the _wbDefineRule builtin itself so the C call
  // happens in the caller's realm (see __wbBindRealmAPI)
  prepareRuleDef: function (arg1, arg2) {
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

    // when: cron('...') is converted to cron: '...'
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

    return { name: name, def: d };
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
  var settle;
  var p = new Promise(function (resolve, reject) {
    settle = function (result, spawnError) {
      if (spawnError) reject(new Error('spawn ' + cmd + ': ' + spawnError));
      else resolve(result);
    };
  });
  var a = _WbRules.prepareSpawnArgs(cmd, args, options, settle);
  _wbSpawn(a[0], a[1], a[2], a[3], a[4]);
  return p;
}

// normalizes spawn arguments; the caller invokes the _wbSpawn builtin
// itself so the C call happens in the caller's realm. settle(result,
// spawnError) reports the outcome to the caller's promise (created in
// the calling realm so post-await code keeps its file attribution).
_WbRules.prepareSpawnArgs = function (cmd, args, options, settle) {
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

  var argv = [cmd].concat(args || []);
  return [
    argv,
    function (args) {
      if (args.spawnError !== undefined) {
        // the process never started; the legacy exitCallback contract
        // never fired for exec failures and still does not - the
        // rejection (or the engine's own error log) carries it
        if (settle) settle(null, args.spawnError);
        return;
      }
      var result = {
        exitCode: args.exitStatus,
        capturedOutput: options.captureOutput ? args.capturedOutput : null,
        capturedErrorOutput: args.capturedErrorOutput,
      };
      if (settle) settle(result, null);
      if (options.exitCallback) {
        try {
          options.exitCallback(result.exitCode, result.capturedOutput, result.capturedErrorOutput);
        } catch (e) {
          log('error running command callback for ' + cmd + ': ' + (e.stack || e));
        }
      } else if (result.exitCode !== 0) {
        // fire-and-forget callers keep the failure visible in the log
        // (callers that check exitCode via await can silence this by
        // passing a no-op exitCallback - documented in README)
        log.warning('command ' + argv.join(' ') + ' failed with exit status ' + result.exitCode);
      }
    },
    !!options.captureOutput,
    !!options.captureErrorOutput,
    options.input,
  ];
};

function runShellCommand(cmd, options) {
  return spawn('/bin/sh', ['-c', cmd], options);
}

// part of the rule API surface: realms inherit it via the global prototype
// eslint-disable-next-line no-unused-vars, @typescript-eslint/no-unused-vars
function sleep(ms) {
  return new Promise(function (resolve) {
    setTimeout(resolve, ms);
  });
}

var _mqttNextWaiters = {};
// part of the rule API surface: realms inherit it via the global prototype
// eslint-disable-next-line no-unused-vars, @typescript-eslint/no-unused-vars
function nextMqtt(topic, timeoutMs) {
  if (!_mqttNextWaiters[topic]) {
    _mqttNextWaiters[topic] = [];
    // one persistent subscription per topic feeds every waiter; it lives
    // until the file is unloaded (bounded by distinct topics, not calls)
    trackMqtt(topic, function (msg) {
      if (msg.retained) return; // "next" means a live message, not the broker cache
      var waiters = _mqttNextWaiters[topic];
      _mqttNextWaiters[topic] = [];
      for (var i = 0; i < waiters.length; i++) waiters[i](msg);
    });
  }
  if (timeoutMs == null) {
    return new Promise(function (resolve) {
      _mqttNextWaiters[topic].push(resolve);
    });
  }
  return new Promise(function (resolve, reject) {
    var settle = function (v) {
      clearTimeout(timer);
      resolve(v);
    };
    var timer = setTimeout(function () {
      // a timed-out waiter must not pile up in the queue forever
      var ws = _mqttNextWaiters[topic];
      var idx = ws.indexOf(settle);
      if (idx >= 0) ws.splice(idx, 1);
      reject(new Error('nextMqtt: no message on ' + topic + ' in ' + timeoutMs + ' ms'));
    }, timeoutMs);
    _mqttNextWaiters[topic].push(settle);
  });
}

var _changeWaiters = {};
function changed(ctrl, timeoutMs) {
  if (!_changeWaiters[ctrl]) {
    _changeWaiters[ctrl] = [];
    // one persistent anonymous rule per control feeds every waiter -
    // exact whenChanged semantics (same triggers, same value conversion)
    defineRule({
      whenChanged: ctrl,
      then: function (newValue) {
        var ws = _changeWaiters[ctrl];
        _changeWaiters[ctrl] = [];
        for (var i = 0; i < ws.length; i++) ws[i](newValue);
      },
    });
  }
  // not Promise.race(p, sleep(...).then(throw)): the losing timeout
  // would later reject unhandled and land in the log as a phantom
  // "async rule error"
  if (timeoutMs == null) {
    return new Promise(function (resolve) {
      _changeWaiters[ctrl].push(resolve);
    });
  }
  return new Promise(function (resolve, reject) {
    var settle = function (v) {
      clearTimeout(timer);
      resolve(v);
    };
    var timer = setTimeout(function () {
      // a timed-out waiter must not pile up in the queue forever
      var ws = _changeWaiters[ctrl];
      var idx = ws.indexOf(settle);
      if (idx >= 0) ws.splice(idx, 1);
      reject(new Error('changed: no change of ' + ctrl + ' in ' + timeoutMs + ' ms'));
    }, timeoutMs);
    _changeWaiters[ctrl].push(settle);
  });
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

  // set new prototype for this object; the bookkeeping fields are
  // non-enumerable so a spec-correct for-in (QuickJS walks the proxy's
  // prototype chain, unlike Duktape's legacy 'enumerate' trap) skips them
  var p = {};
  Object.defineProperty(p, '_ps', { value: [], writable: true });
  Object.defineProperty(p, '_psself', { value: null, writable: true });
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
      return true;
    },
    enumerate: function (o) {
      var keys = Object.keys(o);
      keys.splice(keys.indexOf('_psself'), 1);
      return keys;
    },
    ownKeys: function (o) {
      var keys = Object.keys(o);
      keys.splice(keys.indexOf('_psself'), 1);
      return keys;
    },
  });

  p._psself = p;
  return p;
};

// Builds the storage proxy for an already-expanded storage name (the
// per-file hash or global name _wbPersistentName produced). Everything
// here is realm-neutral - _wbPersistentGet/Set take the expanded name -
// so realm-local PersistentStorage wrappers (see __wbBindRealmAPI) can
// delegate to it after resolving the name in the calling file's realm.
_WbRules.makePersistentStorage = function (expandedName) {
  var p = new Proxy(
    { name: expandedName, _psself: null },
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

        _wbPersistentSet(o.name, key, value);
        return true;
      },
    }
  );

  p._psself = p;

  return p;
};

global.PersistentStorage = function (name, options) {
  return _WbRules.makePersistentStorage(_wbPersistentName(name, options));
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

var Notify = require('wb-notify');
var Alarms = require('wb-alarms');

// Per-realm API rebinding: promise jobs (code after an await) execute
// under the realm their functions were created in, so wrappers inherited
// from the shared prototype would call the shared realm's builtins and
// lose file attribution. The engine compiles THIS function's source in
// each script realm (see prepareNewContext) - the resulting closures and
// the builtins they reference (installed as realm-local own properties)
// all live in the script's realm, keeping attribution across awaits.
global.__wbBindRealmAPI = function (g) {
  g.defineRule = function (arg1, arg2) {
    var r = _WbRules.prepareRuleDef(arg1, arg2);
    return r.name === '' ? _wbDefineRule(r.def) : _wbDefineRule(r.name, r.def);
  };
  g.setTimeout = function (callback, ms) {
    return _wbStartTimer(callback, ms, false);
  };
  g.setInterval = function (callback, ms) {
    return _wbStartTimer(callback, ms, true);
  };
  g.startTimer = function (name, ms) {
    _wbStartTimer(name, ms, false);
  };
  g.startTicker = function (name, ms) {
    _wbStartTimer(name, ms, true);
  };
  g.spawn = function (cmd, args, options) {
    var settle;
    var p = new Promise(function (resolve, reject) {
      settle = function (result, spawnError) {
        if (spawnError) reject(new Error('spawn ' + cmd + ': ' + spawnError));
        else resolve(result);
      };
    });
    var a = _WbRules.prepareSpawnArgs(cmd, args, options, settle);
    _wbSpawn(a[0], a[1], a[2], a[3], a[4]);
    return p;
  };
  g.runShellCommand = function (cmd, options) {
    return g.spawn('/bin/sh', ['-c', cmd], options);
  };
  // diagnostics: total parked changed()/nextMqtt() waiters in this file
  // (leak canary for tests; not a public API)
  g.__wbAsyncWaiters = function () {
    var n = 0;
    var count = function (m) {
      Object.keys(m).forEach(function (k) {
        n += m[k].length;
      });
    };
    count(changeWaiters);
    count(mqttNextWaiters);
    return n;
  };
  g.sleep = function (ms) {
    return new Promise(function (resolve) {
      g.setTimeout(resolve, ms);
    });
  };
  var changeWaiters = {};
  g.changed = function (ctrl, timeoutMs) {
    if (!changeWaiters[ctrl]) {
      changeWaiters[ctrl] = [];
      // defineRule here is the realm-local wrapper: the rule belongs to
      // THIS file and is cleaned up when it reloads
      g.defineRule({
        whenChanged: ctrl,
        then: function (newValue) {
          var ws = changeWaiters[ctrl];
          changeWaiters[ctrl] = [];
          for (var i = 0; i < ws.length; i++) ws[i](newValue);
        },
      });
    }
    // not Promise.race with a throwing sleep: the losing timeout would
    // later reject unhandled - a phantom "async rule error" in the log
    if (timeoutMs == null) {
      return new Promise(function (resolve) {
        changeWaiters[ctrl].push(resolve);
      });
    }
    return new Promise(function (resolve, reject) {
      var settle = function (v) {
        clearTimeout(timer);
        resolve(v);
      };
      var timer = g.setTimeout(function () {
        // a timed-out waiter must not pile up in the queue forever
        var ws = changeWaiters[ctrl];
        var idx = ws.indexOf(settle);
        if (idx >= 0) ws.splice(idx, 1);
        reject(new Error('changed: no change of ' + ctrl + ' in ' + timeoutMs + ' ms'));
      }, timeoutMs);
      changeWaiters[ctrl].push(settle);
    });
  };
  var mqttNextWaiters = {};
  g.nextMqtt = function (topic, timeoutMs) {
    if (!mqttNextWaiters[topic]) {
      mqttNextWaiters[topic] = [];
      // trackMqtt here is the realm-local builtin: the subscription is
      // attributed to this file and cleaned up when it reloads
      trackMqtt(topic, function (msg) {
        if (msg.retained) return; // "next" means a live message, not the broker cache
        var waiters = mqttNextWaiters[topic];
        mqttNextWaiters[topic] = [];
        for (var i = 0; i < waiters.length; i++) waiters[i](msg);
      });
    }
    if (timeoutMs == null) {
      return new Promise(function (resolve) {
        mqttNextWaiters[topic].push(resolve);
      });
    }
    return new Promise(function (resolve, reject) {
      var settle = function (v) {
        clearTimeout(timer);
        resolve(v);
      };
      var timer = g.setTimeout(function () {
        // a timed-out waiter must not pile up in the queue forever
        var ws = mqttNextWaiters[topic];
        var idx = ws.indexOf(settle);
        if (idx >= 0) ws.splice(idx, 1);
        reject(new Error('nextMqtt: no message on ' + topic + ' in ' + timeoutMs + ' ms'));
      }, timeoutMs);
      mqttNextWaiters[topic].push(settle);
    });
  };
  g.PersistentStorage = function (name, options) {
    // _wbPersistentName is the realm-local builtin: it attributes a
    // local storage to THIS file even after an await (promise jobs run
    // under the function's creation realm, not the shared one)
    return _WbRules.makePersistentStorage(_wbPersistentName(name, options));
  };
  // MQTT-RPC (modules/wb-mqtt-rpc.js) as a global, like Notify/Alarms -
  // but resolved lazily and PER REALM: the module instance owns this
  // file's reply subscription, client id and served methods, all of which
  // are attributed to the file and cleaned up when it reloads (require()
  // caches per realm, so the getter and require('wb-mqtt-rpc') agree).
  // Files that never touch MqttRpc pay nothing.
  Object.defineProperty(g, 'MqttRpc', {
    configurable: true,
    enumerable: false,
    get: function () {
      var m = require('wb-mqtt-rpc');
      // resolved once: from now on a plain property (require would still
      // return the cached instance, but not for free)
      Object.defineProperty(g, 'MqttRpc', { configurable: true, enumerable: false, value: m });
      return m;
    },
  });
};
