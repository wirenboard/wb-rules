// MQTT-RPC for rules: a client for the Wiren Board MQTT-RPC convention
// (the protocol every wb-mqtt-* service and the web UI speak), typed
// wrappers for the services that ship with the controller, and a server
// so a rule file can publish methods of its own.
//
// Protocol (see wbgo's rpc package and homeui's services/rpc.ts):
//   request  -> /rpc/v1/<driver>/<service>/<method>/<clientId>
//               {"id": <number>, "params": {...}}
//   reply    <- /rpc/v1/<driver>/<service>/<method>/<clientId>/reply
//               {"id": <same>, "result": ...} | {"id": <same>, "error": {"code", "message", "data"}}
//   presence <- /rpc/v1/<driver>/<service>/<method>   retained "1" while the
//               method is served, retained "" once the server clears it
//
// Everything here runs on the JS side over the plain rule API
// (trackMqtt/publish/setTimeout); the engine only contributes
// _wbAddCleanup, the unload hook the server needs to clear its presence
// topics. The module is instantiated PER RULE FILE (require caches per
// realm and the MqttRpc global resolves through require), so each file
// owns its client id, reply subscription, presence watchers and served
// methods - all attributed to the file and released when it reloads.

/* global trackMqtt, publish, setTimeout, clearTimeout, _wbAddCleanup, log, module, __filename */
/* eslint-disable security/detect-object-injection */

var RPC_PREFIX = '/rpc/v1/';
var DEFAULT_TIMEOUT_MS = 60000; // same as the web UI's RPC_TIMEOUT
var HAS_METHOD_TIMEOUT_MS = 3000; // same as the web UI's METHOD_AVAILABLE_TIMEOUT
var DEFAULT_SERVICE_DRIVER = 'wbrules-scripts';

// JSON-RPC 2.0 reserved codes plus the client-side timeout code
// (wb-device-manager's -33000: the org's precedent for "the caller gave
// up"; server-side timeouts keep whatever code the service sends)
var ErrorCode = {
  PARSE_ERROR: -32700,
  INVALID_REQUEST: -32600,
  METHOD_NOT_FOUND: -32601,
  INVALID_PARAMS: -32602,
  INTERNAL_ERROR: -32603,
  SERVER_ERROR: -32000,
  TIMEOUT: -33000,
};

// ---------------------------------------------------------------------------
// errors
// ---------------------------------------------------------------------------

// RpcError(code, message[, data]) - what a call rejects with when the
// server answers with an error object (code/message/data exactly as sent;
// the target goes into driver/service/method), and what a served handler
// throws to answer with a specific code. ES5 subclassing so instanceof works.
function RpcError(code, message, data) {
  var e = new Error(message);
  this.name = 'RpcError';
  this.message = message;
  this.stack = e.stack;
  this.code = code;
  if (data !== undefined) this.data = data;
}
RpcError.prototype = Object.create(Error.prototype);
RpcError.prototype.constructor = RpcError;
RpcError.prototype.toString = function () {
  var s = this.name + ' ' + this.code;
  if (this.driver) s += ' (' + this.driver + '/' + this.service + '/' + this.method + ')';
  s += ': ' + this.message;
  if (this.data !== undefined) s += ' [' + tryStringify(this.data) + ']';
  return s;
};

function withTarget(err, driver, service, method) {
  err.driver = driver;
  err.service = service;
  err.method = method;
  return err;
}

// TimeoutError - the client gave up: no reply in time (data
// "MqttTimeoutError", the marker the web UI puts on its own timeouts) or,
// for waitForMethod, no presence in time (data "MqttMethodUnavailable").
function TimeoutError(message, data) {
  RpcError.call(this, ErrorCode.TIMEOUT, message, data === undefined ? 'MqttTimeoutError' : data);
  this.name = 'TimeoutError';
}
TimeoutError.prototype = Object.create(RpcError.prototype);
TimeoutError.prototype.constructor = TimeoutError;

function tryStringify(v) {
  try {
    return typeof v === 'string' ? v : JSON.stringify(v);
  } catch (e) {
    return String(v);
  }
}

// ---------------------------------------------------------------------------
// helpers
// ---------------------------------------------------------------------------

function isPlainObject(v) {
  return v !== null && typeof v === 'object' && !Array.isArray(v);
}

function hasOwn(o, k) {
  return Object.prototype.hasOwnProperty.call(o, k);
}

// one topic level: non-empty and free of separators/wildcards
function checkTopicPart(name, value) {
  if (typeof value !== 'string' || value === '') {
    throw new TypeError('MqttRpc: ' + name + ' must be a non-empty string');
  }
  if (/[/+#]/.test(value)) {
    throw new TypeError('MqttRpc: ' + name + ' must not contain "/", "+" or "#": ' + value);
  }
  return value;
}

function checkParams(params) {
  if (params === undefined || params === null) return {};
  if (typeof params !== 'object') {
    throw new TypeError('MqttRpc: params must be an object, got ' + typeof params);
  }
  return params;
}

// a wait in ms; 0 and Infinity both mean "no limit"
function checkTimeout(value) {
  if (typeof value !== 'number' || value < 0 || isNaN(value)) {
    throw new TypeError('MqttRpc: timeout must be a non-negative number of milliseconds');
  }
  return value === Infinity ? 0 : value;
}

// the fallback is checked too: defaults.timeout is a plain assignable field
function timeoutOf(options, fallback) {
  if (options && options.timeout !== undefined && options.timeout !== null) {
    return checkTimeout(options.timeout);
  }
  return checkTimeout(fallback);
}

// hasMethod/waitForMethod take the wait either positionally (like
// nextMqtt(topic, timeoutMs)) or as {timeout} (like call's options)
function presenceTimeoutOf(arg, fallback) {
  if (arg === undefined || arg === null) return checkTimeout(fallback);
  if (typeof arg === 'number') return checkTimeout(arg);
  if (typeof arg !== 'object') {
    throw new TypeError('MqttRpc: the wait must be a number of milliseconds or {timeout}');
  }
  return timeoutOf(arg, fallback);
}

function randomId(length) {
  var chars = 'ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789';
  var s = '';
  for (var i = 0; i < length; i++) s += chars.charAt(Math.floor(Math.random() * chars.length));
  return s;
}

function methodTopic(driver, service, method) {
  return RPC_PREFIX + driver + '/' + service + '/' + method;
}

// ---------------------------------------------------------------------------
// client
// ---------------------------------------------------------------------------

// per-file identity: the last topic level of every request this file
// sends, and the level its reply subscription is keyed on
var clientId = 'wbrules-' + randomId(10);
var nextId = 1;
var inflight = {}; // id -> { resolve, reject, replyTopic, timer }
var replySubscribed = false;

var defaults = {
  timeout: DEFAULT_TIMEOUT_MS,
  hasMethodTimeout: HAS_METHOD_TIMEOUT_MS,
};

function ensureReplySubscription() {
  if (replySubscribed) return;
  replySubscribed = true;
  // one subscription per file for every reply addressed to its client id;
  // trackMqtt attributes it to this file, so a reload drops it together
  // with the (then stale) client id. No replay cache: replies are one-off
  // and can be large (a history query, a log page)
  trackMqtt(RPC_PREFIX + '+/+/+/' + clientId + '/reply', onReply, { cache: false });
}

function onReply(msg) {
  // replies are never retained; a retained delivery is the engine
  // replaying a cached value to a late subscriber, never a live answer
  if (msg.retained) return;
  var reply;
  try {
    reply = JSON.parse(msg.value);
  } catch (e) {
    log.warning('MqttRpc: unparsable reply on {}: {}', msg.topic, msg.value);
    return;
  }
  if (!isPlainObject(reply) || !hasOwn(reply, 'id')) return;
  var id = reply.id;
  if (id === null) {
    // a server that could not read the request (-32700/-32600) answers
    // with a null id; when exactly one call waits on that reply topic the
    // error is unambiguously its own - better than waiting out the timeout
    var only = null;
    Object.keys(inflight).forEach(function (k) {
      if (inflight[k].replyTopic === msg.topic) only = only === null ? k : false;
    });
    if (!only || reply.error === undefined || reply.error === null) return;
    id = only;
  }
  var call = inflight[id];
  // unknown id: a late reply to a call that already timed out, or a
  // duplicate; nothing waits for it
  if (!call) return;
  // ids are unique per client, so the topic can only differ if a server
  // echoed a foreign id - never settle a call with somebody else's answer
  if (msg.topic !== call.replyTopic) return;
  delete inflight[id];
  if (call.timer !== null) clearTimeout(call.timer);
  if (reply.error !== undefined && reply.error !== null) {
    call.reject(errorFromReply(reply.error, call));
  } else {
    call.resolve(reply.result);
  }
}

function errorFromReply(err, call) {
  var e;
  if (isPlainObject(err)) {
    var code = typeof err.code === 'number' ? err.code : ErrorCode.SERVER_ERROR;
    var message = typeof err.message === 'string' ? err.message : tryStringify(err);
    e = new RpcError(code, message, err.data);
  } else {
    e = new RpcError(ErrorCode.SERVER_ERROR, tryStringify(err), err);
  }
  return withTarget(e, call.driver, call.service, call.method);
}

// call(driver, service, method[, params[, options]]) -> Promise<result>
//   options.timeout        ms to wait for the reply (default 60000; 0 = forever)
//   options.waitForMethod  true or ms (0 = forever): wait for the method's
//                          presence topic before sending (services that
//                          start later than the rules would otherwise get
//                          a timeout); the reply timeout starts after that
function call(driver, service, method, params, options) {
  checkTopicPart('driver', driver);
  checkTopicPart('service', service);
  checkTopicPart('method', method);
  var p = checkParams(params);
  var timeout = timeoutOf(options, defaults.timeout);
  var send = function () {
    return sendRequest(driver, service, method, p, timeout);
  };
  var wait = options ? options.waitForMethod : undefined;
  if (wait !== undefined && wait !== null && wait !== false) {
    if (wait !== true && typeof wait !== 'number') {
      throw new TypeError('MqttRpc: waitForMethod must be true or a number of milliseconds');
    }
    // true: as long as the call itself may take (0 = forever carries over)
    return waitForMethod(driver, service, method, wait === true ? timeout : wait).then(send);
  }
  return send();
}

function sendRequest(driver, service, method, params, timeout) {
  ensureReplySubscription();
  var id = nextId++;
  var topic = methodTopic(driver, service, method) + '/' + clientId;
  return new Promise(function (resolve, reject) {
    var entry = {
      resolve: resolve,
      reject: reject,
      replyTopic: topic + '/reply',
      timer: null,
      driver: driver,
      service: service,
      method: method,
    };
    if (timeout > 0) {
      entry.timer = setTimeout(function () {
        delete inflight[id];
        reject(
          withTarget(
            new TimeoutError(
              'no reply from ' + driver + '/' + service + '/' + method + ' in ' + timeout + ' ms'
            ),
            driver,
            service,
            method
          )
        );
      }, timeout);
    }
    inflight[id] = entry;
    publish(topic, JSON.stringify({ id: id, params: params }), 1, false);
  });
}

// ---- presence ----

var presence = {}; // topic -> { known, available, waiters: [fn] }

function watchPresence(topic) {
  var entry = presence[topic];
  if (entry) return entry;
  entry = presence[topic] = { known: false, available: false, waiters: [] };
  // the retained "1" arrives right after subscribing when the method is
  // served; a later "1" (service started) or "" (service stopped, cleared
  // its presence) keeps the entry current for as long as the file lives
  trackMqtt(topic, function (msg) {
    var available = msg.value !== '';
    entry.known = true;
    entry.available = available;
    if (available) {
      var ws = entry.waiters;
      entry.waiters = [];
      for (var i = 0; i < ws.length; i++) ws[i]();
    }
  });
  return entry;
}

function dropWaiter(entry, fn) {
  var idx = entry.waiters.indexOf(fn);
  if (idx >= 0) entry.waiters.splice(idx, 1);
}

// hasMethod(driver, service, method[, timeoutMs | {timeout}]) -> Promise<boolean>
//   resolves true once the presence topic is seen, false when nothing is
//   retained there within the wait (default 3000 ms; 0 = wait for true
//   forever). The answer is remembered and kept up to date by the
//   subscription, so repeated calls are instant and a service that
//   appears later flips to true.
function hasMethod(driver, service, method, timeoutArg) {
  checkTopicPart('driver', driver);
  checkTopicPart('service', service);
  checkTopicPart('method', method);
  var timeout = presenceTimeoutOf(timeoutArg, defaults.hasMethodTimeout);
  var entry = watchPresence(methodTopic(driver, service, method));
  if (entry.known) return Promise.resolve(entry.available);
  return new Promise(function (resolve) {
    var timer = null;
    var settle = function () {
      if (timer !== null) clearTimeout(timer);
      resolve(true);
    };
    if (timeout > 0) {
      timer = setTimeout(function () {
        dropWaiter(entry, settle);
        // nothing retained: remember "absent" until the subscription says
        // otherwise, so callers polling hasMethod do not wait every time
        if (!entry.known) {
          entry.known = true;
          entry.available = false;
        }
        resolve(entry.available);
      }, timeout);
    }
    entry.waiters.push(settle);
  });
}

// waitForMethod(driver, service, method[, timeoutMs | {timeout}]) -> Promise<void>
//   resolves as soon as the method is served; rejects with TimeoutError
//   (data "MqttMethodUnavailable") after the wait (default: the call
//   timeout; 0 = wait forever)
function waitForMethod(driver, service, method, timeoutArg) {
  checkTopicPart('driver', driver);
  checkTopicPart('service', service);
  checkTopicPart('method', method);
  var timeout = presenceTimeoutOf(timeoutArg, defaults.timeout);
  var entry = watchPresence(methodTopic(driver, service, method));
  if (entry.available) return Promise.resolve();
  return new Promise(function (resolve, reject) {
    var timer = null;
    var settle = function () {
      if (timer !== null) clearTimeout(timer);
      resolve();
    };
    if (timeout > 0) {
      timer = setTimeout(function () {
        dropWaiter(entry, settle);
        reject(
          withTarget(
            new TimeoutError(
              driver + '/' + service + '/' + method + ' not available after ' + timeout + ' ms',
              'MqttMethodUnavailable'
            ),
            driver,
            service,
            method
          )
        );
      }, timeout);
    }
    entry.waiters.push(settle);
  });
}

// ---- service proxies ----

// service(driver, service[, methods]) -> proxy with call/hasMethod/
// waitForMethod bound to the target plus one function per listed method:
//   var editor = MqttRpc.service('wbrules', 'Editor', ['List', 'Load']);
//   var files = await editor.List();
function service(driver, svc, methods) {
  checkTopicPart('driver', driver);
  checkTopicPart('service', svc);
  var proxy = {
    driver: driver,
    service: svc,
    call: function (method, params, options) {
      return call(driver, svc, method, params, options);
    },
    hasMethod: function (method, timeoutArg) {
      return hasMethod(driver, svc, method, timeoutArg);
    },
    waitForMethod: function (method, timeoutArg) {
      return waitForMethod(driver, svc, method, timeoutArg);
    },
  };
  (methods || []).forEach(function (method) {
    checkTopicPart('method', method);
    if (hasOwn(proxy, method)) {
      throw new TypeError('MqttRpc.service: method name ' + method + ' clashes with the proxy API');
    }
    proxy[method] = function (params, options) {
      return call(driver, svc, method, params, options);
    };
  });
  return proxy;
}

// ---------------------------------------------------------------------------
// server
// ---------------------------------------------------------------------------

var served = {}; // "driver/service" -> { driver, service, methods: {name: handler} }
var cleanupRegistered = false;
// heap-wide (every file's instance shares module.static): which file
// serves which driver/service, so two files cannot answer the same
// requests - the engine would fan a request out to both subscriptions
// and the caller would get two replies (one of them -32601)
var registry = module.static;
if (!registry.owners) registry.owners = {}; // "driver/service" -> { clientId, file }
// the file this instance belongs to (realm global), for the owner message
var ownFile = typeof __filename === 'string' ? __filename : clientId;

function replyTo(topic, body) {
  var text;
  try {
    text = JSON.stringify(body);
  } catch (e) {
    // a circular result, a BigInt in data, ...: the caller still gets an
    // answer (never a silent timeout), the author gets a log line
    log.error('MqttRpc: reply on {} is not JSON-serializable: {}', topic, e.message);
    if (body.error) {
      // an error whose data cannot travel keeps its code and message
      text = JSON.stringify(errorReply(body.id, body.error.code, body.error.message));
    } else {
      text = JSON.stringify(
        errorReply(body.id, ErrorCode.INTERNAL_ERROR, 'reply is not JSON-serializable: ' + e.message)
      );
    }
  }
  publish(topic + '/reply', text, 1, false);
}

function errorReply(id, code, message, data) {
  var error = { code: code, message: message };
  if (data !== undefined) error.data = data;
  return { id: id, error: error };
}

function errorObjectFor(err) {
  if (err instanceof RpcError) {
    var e = { code: err.code, message: err.message };
    if (err.data !== undefined) e.data = err.data;
    return e;
  }
  var message = err && err.message !== undefined ? String(err.message) : String(err);
  var data = err && err.name && err.name !== 'Error' ? String(err.name) : undefined;
  var out = { code: ErrorCode.INTERNAL_ERROR, message: message };
  if (data !== undefined) out.data = data;
  return out;
}

function makeRequestHandler(entry) {
  return function (msg) {
    // a retained delivery is a replayed cached request, not a live call
    if (msg.retained) return;
    // /rpc/v1/<driver>/<service>/<method>/<clientId>
    var parts = msg.topic.split('/');
    if (parts.length !== 7) return;
    var method = parts[5];
    var requester = parts[6];
    var req;
    try {
      req = JSON.parse(msg.value);
    } catch (e) {
      replyTo(msg.topic, errorReply(null, ErrorCode.PARSE_ERROR, 'parse error: ' + e.message));
      return;
    }
    if (!isPlainObject(req) || !hasOwn(req, 'id')) {
      replyTo(msg.topic, errorReply(null, ErrorCode.INVALID_REQUEST, 'invalid request: no id'));
      return;
    }
    var id = req.id;
    if (id !== null && typeof id !== 'number' && typeof id !== 'string') {
      replyTo(msg.topic, errorReply(null, ErrorCode.INVALID_REQUEST, 'invalid request: bad id'));
      return;
    }
    var handler = hasOwn(entry.methods, method) ? entry.methods[method] : null;
    if (!handler) {
      replyTo(msg.topic, errorReply(id, ErrorCode.METHOD_NOT_FOUND, 'unknown method: ' + method));
      return;
    }
    var params = req.params === undefined || req.params === null ? {} : req.params;
    if (typeof params !== 'object') {
      // JSON-RPC params are by-name (object) or by-position (array)
      replyTo(msg.topic, errorReply(id, ErrorCode.INVALID_PARAMS, 'invalid params: not an object'));
      return;
    }
    var request = {
      driver: entry.driver,
      service: entry.service,
      method: method,
      clientId: requester,
      id: id,
      topic: msg.topic,
    };
    // handlers may be sync or async; whatever they return is the result
    new Promise(function (resolve) {
      resolve(handler(params, request));
    }).then(
      function (result) {
        // undefined, a function, a symbol: nothing JSON can carry -> null
        // (JSON.stringify would silently drop the result member instead)
        if (result === undefined || typeof result === 'function' || typeof result === 'symbol') {
          result = null;
        }
        replyTo(msg.topic, { id: id, result: result });
      },
      function (err) {
        if (!(err instanceof RpcError)) {
          // a handler bug: the caller gets -32603, the rule author gets a log line
          log.error(
            'MqttRpc: {}/{}/{} handler failed: {}',
            entry.driver,
            entry.service,
            method,
            // QuickJS stacks carry frames only: prefix the error itself
            err && err.stack ? String(err) + '\n' + err.stack : String(err)
          );
        }
        replyTo(msg.topic, { id: id, error: errorObjectFor(err) });
      }
    );
  };
}

function clearPresence() {
  Object.keys(served).forEach(function (key) {
    var entry = served[key];
    if (registry.owners[key] && registry.owners[key].clientId === clientId) delete registry.owners[key];
    Object.keys(entry.methods).forEach(function (method) {
      try {
        publish(methodTopic(entry.driver, entry.service, method), '', 1, true);
      } catch (e) {
        log.error('MqttRpc: cannot clear the presence of {}/{}/{}: {}', entry.driver, entry.service, method, e);
      }
    });
  });
  served = {};
}

// defineService([driver,] service, methods) -> { driver, service, methods }
//   methods: { MethodName: function (params, request) { return result } }
//   Each method is announced on its presence topic (retained "1") and
//   answered on /rpc/v1/<driver>/<service>/<MethodName>/<clientId>; the
//   presence is cleared when the file is unloaded. The default driver is
//   "wbrules-scripts" - "wbrules" itself belongs to the engine's Editor
//   server, which answers -32601 for any service it does not know.
function defineService(driver, svc, methods) {
  if (methods === undefined && isPlainObject(svc)) {
    methods = svc;
    svc = driver;
    driver = DEFAULT_SERVICE_DRIVER;
  }
  checkTopicPart('driver', driver);
  checkTopicPart('service', svc);
  if (!isPlainObject(methods) || Object.keys(methods).length === 0) {
    throw new TypeError('MqttRpc.defineService: methods must be a non-empty object of functions');
  }
  Object.keys(methods).forEach(function (name) {
    checkTopicPart('method', name);
    if (typeof methods[name] !== 'function') {
      throw new TypeError('MqttRpc.defineService: method ' + name + ' is not a function');
    }
  });
  if (!cleanupRegistered) {
    cleanupRegistered = true;
    _wbAddCleanup(clearPresence);
  }
  var key = driver + '/' + svc;
  var entry = served[key];
  if (!entry) {
    var owner = registry.owners[key];
    if (owner !== undefined && owner.clientId !== clientId) {
      throw new Error(
        'MqttRpc.defineService: ' + key + ' is already served by another rule file (' + owner.file + ')'
      );
    }
    registry.owners[key] = { clientId: clientId, file: ownFile };
    entry = served[key] = { driver: driver, service: svc, methods: {} };
    // one subscription per service covers every method and requester; no
    // replay cache: requests are one-off, and every requester id would
    // otherwise pin its last request for the file's lifetime
    trackMqtt(RPC_PREFIX + driver + '/' + svc + '/+/+', makeRequestHandler(entry), { cache: false });
  }
  Object.keys(methods).forEach(function (name) {
    if (hasOwn(entry.methods, name)) {
      log.warning('MqttRpc: {}/{}/{} redefined', driver, svc, name);
    }
    entry.methods[name] = methods[name];
    publish(methodTopic(driver, svc, name), '1', 1, true);
  });
  return { driver: driver, service: svc, methods: Object.keys(entry.methods) };
}

// ---------------------------------------------------------------------------
// the controller's own services
// ---------------------------------------------------------------------------

// A method whose params carry the server-side time budget (wb-mqtt-serial
// port/device operations take total_timeout in ms, wb-mqtt-db get_values
// takes request_timeout in s) must not be cut off by the client before the
// server gives up: without an explicit timeout the call waits for that
// budget plus a margin whenever it exceeds the default.
function budgetedMethod(driver, svc, method, budgetField, unitMs, marginMs) {
  return function (params, options) {
    if (
      (!options || options.timeout === undefined || options.timeout === null) &&
      defaults.timeout > 0 && // 0 = no limit already
      isPlainObject(params) &&
      typeof params[budgetField] === 'number' &&
      params[budgetField] * unitMs + marginMs > defaults.timeout
    ) {
      var o = {};
      Object.keys(options || {}).forEach(function (k) {
        o[k] = options[k];
      });
      o.timeout = params[budgetField] * unitMs + marginMs;
      options = o;
    }
    return call(driver, svc, method, params, options);
  };
}

var FW_UPDATE_METHODS = ['GetFirmwareInfo', 'Update', 'ClearError', 'Restore'];

var services = {
  // wb-mqtt-serial: the Modbus/serial device driver
  serial: {
    driver: 'wb-mqtt-serial',
    config: service('wb-mqtt-serial', 'config', ['Load', 'GetSchema']),
    templates: service('wb-mqtt-serial', 'templates', ['Upload', 'Delete']),
    ports: service('wb-mqtt-serial', 'ports', ['Load']),
    port: service('wb-mqtt-serial', 'port', ['Load', 'Setup', 'Scan']),
    device: service('wb-mqtt-serial', 'device', ['LoadConfig', 'Load', 'Set', 'Probe', 'SetPoll']),
    fwUpdate: service('wb-mqtt-serial', 'fw-update', FW_UPDATE_METHODS),
  },
  // wb-mqtt-db: the history database
  db: {
    driver: 'db_logger',
    history: service('db_logger', 'history', ['get_values', 'get_channels']),
  },
  // wb-rules: the rule editor (this very engine)
  rules: {
    driver: 'wbrules',
    Editor: service('wbrules', 'Editor', [
      'List',
      'Load',
      'Save',
      'Remove',
      'Rename',
      'ChangeState',
      'Check',
      'GetTypes',
    ]),
  },
  // wb-mqtt-confed: the configuration editor
  confed: {
    driver: 'confed',
    Editor: service('confed', 'Editor', ['List', 'Load', 'Save']),
  },
  // wb-mqtt-logs: journal access
  logs: {
    driver: 'wb_logs',
    logs: service('wb_logs', 'logs', ['List', 'Load', 'CancelLoad']),
  },
  // wb-diag-collect: the diagnostics archive
  diag: {
    driver: 'diag',
    main: service('diag', 'main', ['diag', 'status']),
  },
  // wb-device-manager: the serial bus scanner (and its firmware updater)
  deviceManager: {
    driver: 'wb-device-manager',
    busScan: service('wb-device-manager', 'bus-scan', ['Start', 'Stop']),
    fwUpdate: service('wb-device-manager', 'fw-update', FW_UPDATE_METHODS),
  },
  // wb-mqtt-dali: the DALI gateway
  dali: {
    driver: 'wb-mqtt-dali',
    Editor: service('wb-mqtt-dali', 'Editor', [
      'GetList',
      'GetGateway',
      'SetGateway',
      'GetBus',
      'SetBus',
      'ScanBus',
      'StopScanBus',
      'GetDevice',
      'SetDevice',
      'GetGroup',
      'SetGroup',
      'IdentifyDevice',
      'ResetDeviceSettings',
      'ResetDevice',
    ]),
    Bus: service('wb-mqtt-dali', 'Bus', ['SendCommand', 'ListCommands']),
  },
};

// serial port/device operations carry their own time budget (total_timeout, ms)
['Load', 'Setup', 'Scan'].forEach(function (method) {
  services.serial.port[method] = budgetedMethod('wb-mqtt-serial', 'port', method, 'total_timeout', 1, 10000);
});
// (Probe runs on a fixed server-side budget: nothing to stretch)
['LoadConfig', 'Load', 'Set'].forEach(function (method) {
  services.serial.device[method] = budgetedMethod(
    'wb-mqtt-serial',
    'device',
    method,
    'total_timeout',
    1,
    10000
  );
});
// so does a history query (request_timeout, s)
services.db.history.get_values = budgetedMethod(
  'db_logger',
  'history',
  'get_values',
  'request_timeout',
  1000,
  10000
);

// ---------------------------------------------------------------------------
// exports
// ---------------------------------------------------------------------------

var api = {
  call: call,
  hasMethod: hasMethod,
  waitForMethod: waitForMethod,
  service: service,
  defineService: defineService,
  RpcError: RpcError,
  TimeoutError: TimeoutError,
  ErrorCode: ErrorCode,
  defaults: defaults,
  clientId: clientId,
  DEFAULT_SERVICE_DRIVER: DEFAULT_SERVICE_DRIVER,
};
Object.keys(services).forEach(function (name) {
  api[name] = services[name];
});

// the reply subscription is requested at load, so it is normally in place
// long before the first request goes out (the MQTT client queues both; a
// request sent in the same tick as the module load still has a tiny
// window in which a very fast reply could precede the SUBSCRIBE)
ensureReplySubscription();

module.exports = api;
