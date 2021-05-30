// rule engine runtime

// this function runs once the context is created
// for each script
function __esInitEnv(glob) {
    glob.String.prototype.format = function () {
      var args = [ this ];
      for (var i = 0; i < arguments.length; ++i)
        args.push(arguments[i]);
      return format.apply(null, args);
    };

    glob.String.prototype.xformat = function () {
      var parts = this.split(/\\\{/g), i = 0,
          args = Array.prototype.slice.apply(arguments);
      return parts.map(function (part) {
        return part.replace(/\{\{(.*?)\}\}/g, function (all, expr) {
          try {
            return eval(expr);
          } catch (e) {
            return "<eval failed: " + expr + ": " + e + ">";
          }
        }).replace(/\{\}/g, function () {
          return i < args.length ? args[i++] : "";
        });
      }).join("{");
    };
}

__esInitEnv(global);

var _WbRules = {
  requireCompleteCells: 0,
  timers: {},
  aliases: {},

  CronEntry: function (spec) {
    if (typeof spec != "string")
      throw new Error("invalid cron spec");
    this.spec = spec;
  },

  IncompleteCellCaught: (function () {
    function IncompleteCellCaught(cellName) {
      this.name = "IncompleteCellCaught";
      this.message = "incomplete cell encountered: " + cellName;
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
        throw new Error("setting unsupported proxy value: " + name);
      }
    });
  },

  getDevValue: function getDevValue (o, name) {
    var slashPosition = name.indexOf("/");
    if (slashPosition > 0 && slashPosition < name.length - 1) {
      var target = _WbRules.getDevValue(o, name.slice(0, slashPosition));
      return target[name.slice(slashPosition + 1)];
    }

    if (name in o)
      return o[name];

    var cells = {};
    function ensureCell (dev, name) {
      return cells.hasOwnProperty(name) ?
        cells[name] :
        cells[name] = _wbCellObject(dev, name);
    }
    return o[name] = new Proxy(_wbDevObject(name), {
      get: function (dev, name) {
        var sharpPosition = name.indexOf("#");
        var metaField = "";
        if (sharpPosition > 0 && sharpPosition < name.length - 1) {
          metaField = name.slice(sharpPosition + 1);
          name = name.slice(0, sharpPosition);
        }
        var cell = ensureCell(dev, name);
        if (_WbRules.requireCompleteCells && !cell.isComplete())
          throw new _WbRules.IncompleteCellCaught(name);
        if (metaField !== "") {
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
        var sharpPosition = name.indexOf("#");
        var metaField = "";
        if (sharpPosition > 0 && sharpPosition < name.length - 1) {
          metaField = name.slice(sharpPosition + 1);
          name = name.slice(0, sharpPosition);
        }
        if (metaField !== "") {
          ensureCell(dev, name).setMeta({ k: metaField, v: value });
        } else {
          ensureCell(dev, name).setValue({ v: value });
        }
      }
    });
  },

  setDevValue: function setDevValue (o, name, value) {
    var slashPosition = name.indexOf("/");
    if (slashPosition > 0 && slashPosition < name.length - 1) {
      var target = _WbRules.getDevValue(o, name.slice(0, slashPosition));
      target[name.slice(slashPosition + 1)] = value;
    } else
      throw new Error("setting unsupported proxy value: " + name);
  },

  parseCellRef: function parseCellRef (cellRef) {
    var m = cellRef.match(/([^\/]+)+\/([^\/]+)+$/);
    if (!m)
      throw new Error("invalid cell reference");
    return {
      device: m[1],
      control: m[2]
    };
  },

  defineAlias: function (name, cellRef) {
    if (!name || !cellRef)
      throw new Error("invalid alias definition");
    var ref = _WbRules.parseCellRef(cellRef);
    _WbRules.aliases[name] = cellRef;
    var d = null;
    Object.defineProperty(
      (function () { return this; })(),
      name,
      {
        configurable: true,
        get: function () {
          if (!d)
            d = dev[ref.device];
          return d[ref.control];
        },
        set: function (value) {
          if (!d)
            d = dev[ref.device];
          d[ref.control] = value;
        }
      });
  },

  defineRule: function (arg1, arg2) {

    var name, def;

    // anonymous rule handling
    if (arg2 == undefined) {
        name = "";
        def = arg1;
    } else {
        name = arg1;
        def = arg2;
    }

    debug("defineRule: {}".format(name == "" ? "(anon)" : name));
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

    // when: cron("...") is converted to cron: "..."
    if (def.hasOwnProperty("when") && def.when instanceof _WbRules.CronEntry) {
      def._cron = def.when.spec;
      delete def.when;
    }

    Object.keys(def).forEach(function (k) {
      var orig = d[k];
      switch(k) {
      case "readonly":
        d[k] = !!d[k]; // avoid type cast error on the Go side
        break;
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

    if (name == "") {
        return _wbDefineRule(d);
    } else {
        return _wbDefineRule(name, d);
    }
  },

  startTimer: function startTimer(name, ms, periodic) {
    _wbStartTimer(name, ms, !!periodic);
  }
};

var dev = new Proxy({}, {
  get: _WbRules.getDevValue,
  set: _WbRules.setDevValue
});

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

var defineRule = _WbRules.defineRule;

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

var defineAlias = _WbRules.defineAlias;

function cron(spec) {
  return new _WbRules.CronEntry(spec);
}

var Notify = (function (){
  var _smsQueue = [],
      _smsBusy = false;

  function _advanceSmsQueue () {
    if (!_smsQueue.length)
      return;
    var next = _smsQueue.shift();
    next();
  }

  return {
    sendEmail: function sendEmail (to, subject, text) {
      log("sending email to {}: {}", to, subject);
      runShellCommand("/usr/sbin/sendmail '{}'".format(to), {
        captureErrorOutput: true,
        captureOutput: true,
        input: "Subject: {}\n\n{}".format(subject, text),
        exitCallback: function exitCallback (exitCode, capturedOutput, capturedErrorOutput) {
          if (exitCode != 0)
            log.error("error sending email to {}:\n{}\n{}", to, capturedOutput, capturedErrorOutput);
        }
      });
    },

    sendSMS: function sendSMS (to, text, command) {
      var doSend = function () {
        _smsBusy = true;
        log("sending sms to {}: {}", to, text);
        command = command || "wb-gsm restart_if_broken && gammu sendsms TEXT '{}' -unicode";

        var input = null;
        var maxPlaceholders = 2;
        var count = (command.match(/\{\}/g) || []).length;

        if (count == maxPlaceholders) {
            command = command.format(to, text);
        } else {
            command = command.format(to);
            input = text;
        }
        runShellCommand(command, {
          captureErrorOutput: true,
          captureOutput: true,
          input: input,
          exitCallback: function (exitCode, capturedOutput, capturedErrorOutput) {
            _smsBusy = false;
            if (exitCode != 0)
              log.error("error sending sms to {}:\n{}\n{}", to, capturedOutput, capturedErrorOutput);
            _advanceSmsQueue();
          }
        });
      };

      if (_smsBusy) {
        debug("queueing sms to {}: {}", to, text);
        _smsQueue.push(doSend);
      } else
        doSend();
    }
  };
})();

var Alarms = (function () {
  var recipientTypes = {
    email: function getEmailSendFunc (src) {
      if (!src.hasOwnProperty("to"))
        throw new Error("email recipient without 'to'");
      var subject = src.hasOwnProperty("subject") ? "" + src.subject : "{}";
      return function sendEmailWrapper (text) {
        Notify.sendEmail(src.to, maybeFormat(subject, text), text);
      };
    },

    sms: function getSMSSendFunc (src) {
      if (!src.hasOwnProperty("to"))
        throw new Error("sms recipient without 'to'");
      return function sendSMSWrapper (text) {
        Notify.sendSMS(src.to, text, src.command || "");
      };
    }
  };

  function maybeFormat(text, arg) {
    return text.indexOf("{}") >= 0 || text.indexOf("{{") > 0 ? text.xformat(arg) : text;
  }

  function getSendFunc (src) {
    if (!src || typeof src != "object" || !src.hasOwnProperty("type") ||
        !recipientTypes.hasOwnProperty(src.type))
      throw new Error("invalid recipient spec: %s", JSON.stringify(src));
    return recipientTypes[src.type](src);
  }

  var seq = 1;

  function loadAlarm (alarmSrc, notify, alarmDeviceName) {
    if (!alarmSrc || typeof alarmSrc != "object" || !alarmSrc.hasOwnProperty("cell"))
      throw new Error("invalid alarm definition");

    function checkHasNumKey (key) {
      if (!alarmSrc.hasOwnProperty(key))
        return false;

      if (typeof alarmSrc[key] != "number")
        throw new Error("{}: {}: number expected!".format(JSON.stringify(alarmSrc), key));

      return true;
    }

    var ref = _WbRules.parseCellRef(alarmSrc.cell);
    var namePrefix = "__alarm{}__{}__".format(seq++, alarmSrc.cell),
        cellName = alarmSrc.hasOwnProperty("name") ? "alarm_" + alarmSrc.name : namePrefix + "cell",
        hasExpectedValue = alarmSrc.hasOwnProperty("expectedValue"),
        hasMinValue = checkHasNumKey("minValue"),
        hasMaxValue = checkHasNumKey("maxValue"),
        alarmMessage = alarmSrc.alarmMessage ||
          alarmSrc.cell + (hasExpectedValue ? " has unexpected value = {}" : " is out of bounds, value = {}"),
        noAlarmMessage = alarmSrc.noAlarmMessage ||
          alarmSrc.cell + " is back to normal, value = {}",
        maxCount = checkHasNumKey("maxCount") ? Math.floor(alarmSrc.maxCount) : null,
        alarmDelayMs = checkHasNumKey("alarmDelayMs") ? alarmDelayMs : 0,
        noAlarmDelayMs = checkHasNumKey("noAlarmDelayMs") ? noAlarmDelayMs : 0,
        min, max, interval = null;

    if (hasExpectedValue) {
        if (hasMinValue || hasMaxValue)
          throw new Error("{}: cannot have both expectedValue and minValue/maxValue"
                          .format(JSON.stringify(alarmSrc)));
    } else {
      if (!hasMinValue && !hasMaxValue)
        throw new Error("{}: must specify either expectedValue or value range"
                        .format(JSON.stringify(alarmSrc)));
      min = hasMinValue ? alarmSrc.minValue : -Infinity;
      max = hasMaxValue ? alarmSrc.maxValue : Infinity;
    }

    if (alarmSrc.hasOwnProperty("interval")) {
      // !(alarmSrc.interval > 0) covers NaN case
      if (typeof alarmSrc.interval != "number" || !(alarmSrc.interval > 0))
        throw new Error("invalid alarm interval");
      interval = alarmSrc.interval * 1000;
    }

    var d = null;
    function cellValue () {
      if (d === null)
        d = dev[ref.device];
      return d[ref.control];
    }

    function setAlarmActiveCell(active) {
      active = !!active;
      if (dev[alarmDeviceName][cellName] !== active)
        dev[alarmDeviceName][cellName] = active;
    }

    var wasActive = false, wasTriggered = false, intervalId = null, remainingCount = null;
    var activateTimerId = null, deactivateTimerId = null;

    function stopRepeating() {
      if (intervalId != null) {
        clearInterval(intervalId);
        intervalId = null;
      }
    }

    function notifyAboutActiveAlarm() {
      if (remainingCount === null || remainingCount > 0)
        notify(maybeFormat(alarmMessage, cellValue()));
      if (remainingCount !== null && --remainingCount <= 0)
        stopRepeating();
    }

    function activateAlarm() {
      setAlarmActiveCell(true);

      remainingCount = maxCount;

      notifyAboutActiveAlarm();

      if (interval !== null)
        intervalId = setInterval(notifyAboutActiveAlarm, interval);

      alarmTimerId = null;
      wasActive = true;
    }

    function deactivateAlarm() {
      setAlarmActiveCell(false);
      stopRepeating();
      notify(maybeFormat(noAlarmMessage, cellValue()));
      wasActive = false;
    }

    return {
      cellName: cellName,
      defineRules: function () {
        defineRule(namePrefix + "activate", {
          asSoonAs: hasExpectedValue ? function () {
            // log("cv={}; ev={}", JSON.stringify(cellValue()), JSON.stringify(alarmSrc.expectedValue));
            return cellValue() != alarmSrc.expectedValue;
          } : function () {
            // log("cv={}; min={}, max={}", JSON.stringify(cellValue()), min, max);
            return cellValue() < min || cellValue() > max;
          },
          then: function () {
            if (wasTriggered)
              return;

            wasTriggered = true;

            if (!wasActive) {
              if (alarmSrc.alarmDelayMs > 0)
                activateTimerId = setTimeout(activateAlarm, alarmSrc.alarmDelayMs);
              else
                activateAlarm();
            }

            if (deactivateTimerId != null) {
              clearTimeout(deactivateTimerId);
              deactivateTimerId = null;
            }
          }
        });

        defineRule(namePrefix + "deactivate", {
          asSoonAs: hasExpectedValue ? function () {
            return cellValue() == alarmSrc.expectedValue;
          } : function () {
            return cellValue() >= min && cellValue() <= max;
          },
          then: function () {
            // Set 'alarm active' cell to false during the
            // first rule run, too. This will clear any
            // alarms remaining from before wb-rules startup /
            // loading of this rule file.
            if (!wasTriggered) {
              setAlarmActiveCell(false);
              return;
            }

            wasTriggered = false;

            if (wasActive) {
              if (alarmSrc.noAlarmDelayMs > 0) {
                deactivateTimerId = setTimeout(deactivateAlarm, alarmSrc.noAlarmDelayMs);
              } else
                deactivateAlarm();
            }

            if (activateTimerId != null) {
              clearTimeout(activateTimerId);
              activateTimerId = null;
            }
          }
        });
      }
    };
  }

  function doLoad (src) {
    if (!src.hasOwnProperty("deviceName"))
      throw new Error("deviceName not specified for alarms");

    if (!src.hasOwnProperty("recipients") || !Array.isArray(src.recipients))
      throw new Error("absent/invalid recipients spec specified for alarms");

    if (!src.hasOwnProperty("alarms") || !Array.isArray(src.alarms))
      throw new Error("absent/invalid alarms spec");

    var sendFuncs = src.recipients.map(getSendFunc);
    function notify (text) {
      dev[src.deviceName].log = text;
      sendFuncs.forEach(function (sendFunc) { sendFunc.call(null, text); });
    }

    var loadedAlarms = src.alarms.map(function (alarmSrc) {
      return loadAlarm(alarmSrc, notify, src.deviceName);
    });

    var deviceDef = {
      cells: {
        log: {
          type: "text",
          value: "",
          readonly: true
        }
      }
    };
    if (src.hasOwnProperty("deviceTitle"))
      deviceDef.title = src.deviceTitle;

    loadedAlarms.forEach(function (alarm) {
      deviceDef.cells[alarm.cellName] = {
        type: "alarm",
        value: false,
        readonly: true
      };
    });

    defineVirtualDevice(src.deviceName, deviceDef);

    loadedAlarms.forEach(function (alarm) {
      alarm.defineRules();
    });
  }

  return {
    load: function (src) {
      return doLoad(typeof src == "string" ? readConfig(src) : src);
    }
  };
})();


global.StorableObject = function(obj, ps, pskey) {
        if (pskey === undefined) {
                pskey = "";
                ps = null;
        }

        // check if this object already has a prototype
        if (obj._ps !== undefined && ps !== null) {
                // just append new listener to the list
                obj._ps.push({
                        s: ps,
                        k: pskey
                });
                return obj;
        }

        // set new prototype for this object
        var p = {
                _ps: [],
                _psself: null
        };
        p.__proto__ = obj.__proto__;

        obj.__proto__ = p;

        if (ps !== null) {
                obj._ps.push({
                        s: ps,
                        k: pskey
                })
        }

        var p = new Proxy(obj, {
                get: function(obj, key) {
                        var val = obj[key];
                        if (typeof val === "object") {
                                return new StorableObject(val, obj._psself, key);
                        }
                        return val;
                },
                set: function(o, key, value) {
                        if (key === "_psself") {
                                o._psself = value;
                                return true;
                        }

                        // check if value is an object without StorableObject's prototype
                        if (typeof value === "object" && value._ps === undefined) {
                                throw new Error("don't write pure objects to PersistentStorage, use new StorableObject(obj) instead");
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
                enumerate: function(o) {
                        var keys = Object.keys(o);
                        keys.splice(keys.indexOf("_psself"), 1);
                        return keys;
                }
        });

        p._psself = p;
        return p;
};

global.PersistentStorage = function(name, options) {
    var p = new Proxy({name: _wbPersistentName(name, options), _psself: null}, {
        get: function (o, key) {
            var val = _wbPersistentGet(o.name, key);
            if (typeof val === "object") {
                val = new StorableObject(val, o._psself, key);
            }
            return val;
        },
        set: function (o, key, value) {
            if (key === "_psself") {
                    o._psself = value;
                    return true;
            }

            // check if this value is an object without StorableObject's prototype
            if (typeof value === "object" && value._ps === undefined) {
                throw new Error("don't write pure objects to PersistentStorage, use new StorableObject(obj) instead");
            } else if (typeof value === "object") {
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
                                k: key
                        });
                    }
            }

            return _wbPersistentSet(o.name, key, value);
        }
    });

    p._psself = p;

    return p;
};

__wbVdevPrototype.getCellValue = function(cell) {
    return dev[this.getCellId(cell)];
};

__wbVdevPrototype.setCellValue = function(cell, value) {
    dev[this.getCellId(cell)] = value;
};

__wbVdevPrototype.publish = function(topic, message) {
    publish("/devices/" + this.__deviceId + "/" + topic, message);
};
