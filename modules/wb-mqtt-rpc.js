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

/* global trackMqtt, publish, setTimeout, clearTimeout, _wbAddCleanup, log, module, __filename, nextMqtt, sleep */
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
// JSON Schema validation of served params
// ---------------------------------------------------------------------------
//
// A served method may describe its params with a JSON Schema (the same
// convention wb-mqtt-serial and confed use for their RPC requests); the
// request is checked before the handler runs and a bad one is answered
// -32602 with the problems listed in data. The supported subset: type
// (incl. arrays of types), enum, const, properties/required/
// additionalProperties, items/minItems/maxItems, minLength/maxLength/
// pattern, minimum/maximum/exclusiveMinimum/exclusiveMaximum/multipleOf,
// anyOf/oneOf/allOf/not. Other keywords are ignored.

function jsonTypeOf(v) {
  if (v === null) return 'null';
  if (Array.isArray(v)) return 'array';
  return typeof v;
}

function typeMatches(want, v) {
  var actual = jsonTypeOf(v);
  if (want === 'integer') return actual === 'number' && v % 1 === 0;
  return want === actual;
}

function validateSchema(schema, v, path, problems) {
  if (schema === true || schema === undefined || schema === null) return problems;
  if (schema === false) {
    problems.push({ path: path, message: 'not allowed' });
    return problems;
  }
  var here = path || '/';
  if (schema.type !== undefined) {
    var types = Array.isArray(schema.type) ? schema.type : [schema.type];
    var okType = false;
    for (var i = 0; i < types.length; i++) if (typeMatches(types[i], v)) okType = true;
    if (!okType) {
      problems.push({ path: here, message: 'must be ' + types.join(' or ') + ', got ' + jsonTypeOf(v) });
      return problems; // the other keywords assume the type
    }
  }
  if (schema.enum !== undefined) {
    var found = false;
    for (var e = 0; e < schema.enum.length; e++) if (sameValue(schema.enum[e], v)) found = true;
    if (!found) problems.push({ path: here, message: 'must be one of ' + tryStringify(schema.enum) });
  }
  if (schema.const !== undefined && !sameValue(schema.const, v)) {
    problems.push({ path: here, message: 'must be ' + tryStringify(schema.const) });
  }
  if (typeof v === 'number') {
    if (schema.minimum !== undefined && v < schema.minimum) problems.push({ path: here, message: 'must be >= ' + schema.minimum });
    if (schema.maximum !== undefined && v > schema.maximum) problems.push({ path: here, message: 'must be <= ' + schema.maximum });
    if (schema.exclusiveMinimum !== undefined && v <= schema.exclusiveMinimum) {
      problems.push({ path: here, message: 'must be > ' + schema.exclusiveMinimum });
    }
    if (schema.exclusiveMaximum !== undefined && v >= schema.exclusiveMaximum) {
      problems.push({ path: here, message: 'must be < ' + schema.exclusiveMaximum });
    }
    if (schema.multipleOf !== undefined) {
      var ratio = v / schema.multipleOf;
      if (Math.abs(ratio - Math.round(ratio)) > 1e-9) {
        problems.push({ path: here, message: 'must be a multiple of ' + schema.multipleOf });
      }
    }
  }
  if (typeof v === 'string') {
    if (schema.minLength !== undefined && v.length < schema.minLength) problems.push({ path: here, message: 'must be at least ' + schema.minLength + ' characters' });
    if (schema.maxLength !== undefined && v.length > schema.maxLength) problems.push({ path: here, message: 'must be at most ' + schema.maxLength + ' characters' });
    if (schema.pattern !== undefined) {
      var re;
      try {
        re = new RegExp(schema.pattern);
      } catch (e) {
        throw new TypeError('MqttRpc: invalid pattern in schema at ' + here + ': ' + e.message);
      }
      if (!re.test(v)) problems.push({ path: here, message: 'must match ' + schema.pattern });
    }
  }
  if (Array.isArray(v)) {
    if (schema.minItems !== undefined && v.length < schema.minItems) problems.push({ path: here, message: 'must have at least ' + schema.minItems + ' items' });
    if (schema.maxItems !== undefined && v.length > schema.maxItems) problems.push({ path: here, message: 'must have at most ' + schema.maxItems + ' items' });
    if (schema.items !== undefined) {
      for (var a = 0; a < v.length; a++) validateSchema(schema.items, v[a], pointer(path, a), problems);
    }
  }
  if (isPlainObject(v)) {
    var props = schema.properties || {};
    if (schema.required) {
      for (var r = 0; r < schema.required.length; r++) {
        if (!hasOwn(v, schema.required[r])) problems.push({ path: pointer(path, schema.required[r]), message: 'is required' });
      }
    }
    Object.keys(v).forEach(function (k) {
      if (hasOwn(props, k)) {
        validateSchema(props[k], v[k], pointer(path, k), problems);
      } else if (schema.additionalProperties === false) {
        problems.push({ path: pointer(path, k), message: 'is not allowed' });
      } else if (isPlainObject(schema.additionalProperties)) {
        validateSchema(schema.additionalProperties, v[k], pointer(path, k), problems);
      }
    });
  }
  if (schema.allOf) {
    for (var al = 0; al < schema.allOf.length; al++) validateSchema(schema.allOf[al], v, path, problems);
  }
  if (schema.anyOf || schema.oneOf) {
    var alternatives = schema.anyOf || schema.oneOf;
    var matches = 0;
    for (var o = 0; o < alternatives.length; o++) {
      if (validateSchema(alternatives[o], v, path, []).length === 0) matches++;
    }
    if (matches === 0 || (schema.oneOf && matches !== 1)) {
      problems.push({ path: here, message: schema.oneOf ? 'must match exactly one alternative' : 'must match an alternative' });
    }
  }
  if (schema.not !== undefined && validateSchema(schema.not, v, path, []).length === 0) {
    problems.push({ path: here, message: 'must not match the excluded schema' });
  }
  return problems;
}

// JSON equality: arrays element-wise, objects key by key (any order)
function sameValue(a, b) {
  if (a === b) return true;
  if (typeof a !== 'object' || typeof b !== 'object' || a === null || b === null) return false;
  if (Array.isArray(a) !== Array.isArray(b)) return false;
  if (Array.isArray(a)) {
    if (a.length !== b.length) return false;
    for (var i = 0; i < a.length; i++) if (!sameValue(a[i], b[i])) return false;
    return true;
  }
  var ka = Object.keys(a);
  var kb = Object.keys(b);
  if (ka.length !== kb.length) return false;
  for (var k = 0; k < ka.length; k++) {
    if (!hasOwn(b, ka[k]) || !sameValue(a[ka[k]], b[ka[k]])) return false;
  }
  return true;
}

// a JSON pointer segment (RFC 6901)
function pointer(path, key) {
  return path + '/' + String(key).replace(/~/g, '~0').replace(/\//g, '~1');
}

// validate(schema, value) -> [] or [{path, message}]
function validate(schema, value) {
  return validateSchema(schema, value, '', []);
}

// method(paramsSchema, handler) -> a served method with validated params;
// TypeScript types the handler's params from the schema
function method(params, handler) {
  if (!isPlainObject(params) && typeof params !== 'boolean') {
    throw new TypeError('MqttRpc.method: the params schema must be a JSON Schema object');
  }
  if (typeof handler !== 'function') {
    throw new TypeError('MqttRpc.method: the handler must be a function');
  }
  return { params: params, handler: handler };
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
    var spec = hasOwn(entry.methods, method) ? entry.methods[method] : null;
    if (!spec) {
      replyTo(msg.topic, errorReply(id, ErrorCode.METHOD_NOT_FOUND, 'unknown method: ' + method));
      return;
    }
    var handler = spec.handler;
    var params = req.params === undefined || req.params === null ? {} : req.params;
    if (typeof params !== 'object') {
      // JSON-RPC params are by-name (object) or by-position (array)
      replyTo(msg.topic, errorReply(id, ErrorCode.INVALID_PARAMS, 'invalid params: not an object'));
      return;
    }
    if (spec.params !== undefined) {
      var problems;
      try {
        problems = validate(spec.params, params);
      } catch (e) {
        log.error('MqttRpc: {}/{}/{}: {}', entry.driver, entry.service, method, e.message);
        replyTo(msg.topic, errorReply(id, ErrorCode.INTERNAL_ERROR, 'params schema is invalid: ' + e.message));
        return;
      }
      if (problems.length) {
        var first = problems[0];
        replyTo(
          msg.topic,
          errorReply(id, ErrorCode.INVALID_PARAMS, 'invalid params: ' + first.path + ' ' + first.message, problems)
        );
        return;
      }
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
  var specs = {};
  Object.keys(methods).forEach(function (name) {
    checkTopicPart('method', name);
    var m = methods[name];
    if (typeof m === 'function') {
      specs[name] = { handler: m };
    } else if (isPlainObject(m) && typeof m.handler === 'function') {
      if (m.params !== undefined && !isPlainObject(m.params) && typeof m.params !== 'boolean') {
        throw new TypeError('MqttRpc.defineService: params of ' + name + ' must be a JSON Schema object');
      }
      specs[name] = { handler: m.handler, params: m.params };
    } else {
      throw new TypeError(
        'MqttRpc.defineService: method ' + name + ' must be a function or { params, handler }'
      );
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
  Object.keys(specs).forEach(function (name) {
    if (hasOwn(entry.methods, name)) {
      log.warning('MqttRpc: {}/{}/{} redefined', driver, svc, name);
    }
    entry.methods[name] = specs[name];
    publish(methodTopic(driver, svc, name), '1', 1, true);
  });
  return { driver: driver, service: svc, methods: Object.keys(entry.methods) };
}

// ---------------------------------------------------------------------------
// the controller's own services
// ---------------------------------------------------------------------------
//
// Every service group (MqttRpc.serial, .db, .rules, ...) has two layers:
//   group.rpc.<service>.<Method>(params)  - the RPC methods exactly as the
//                                           service documents them
//   group.<helper>(...)                    - what a rule usually wants:
//                                           plain arguments, parsed results
// plus isAvailable()/waitUntilAvailable() on the group itself.

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

// ---- small helpers shared by the friendly layer ----

function isDate(v) {
  return v instanceof Date || Object.prototype.toString.call(v) === '[object Date]';
}

// Date or milliseconds since the epoch (Date.now() style) -> UNIX seconds
function toSeconds(t, name) {
  if (isDate(t)) return t.getTime() / 1000;
  if (typeof t === 'number' && isFinite(t)) return t / 1000;
  throw new TypeError('MqttRpc: ' + name + ' must be a Date or milliseconds since the epoch');
}

function fromSeconds(s) {
  return new Date(Math.round(s * 1000));
}

// the value of a history record: numbers as numbers, the rest as strings
function parseValue(v) {
  if (typeof v === 'number') return v;
  if (typeof v !== 'string' || v.trim() === '') return v;
  var n = Number(v);
  return isFinite(n) ? n : v;
}

// a "device/control" string or a [device, control] pair -> the pair
function channelPair(ch) {
  if (typeof ch === 'string') {
    var i = ch.indexOf('/');
    if (i <= 0 || i === ch.length - 1) {
      throw new TypeError('MqttRpc: a channel is "device/control", got ' + ch);
    }
    return [ch.slice(0, i), ch.slice(i + 1)];
  }
  if (Array.isArray(ch) && ch.length === 2) return [String(ch[0]), String(ch[1])];
  throw new TypeError('MqttRpc: a channel is "device/control" or [device, control]');
}

function optionsOf(options, names) {
  // the subset of options that are call options (timeout, waitForMethod)
  var o = {};
  var any = false;
  (names || ['timeout', 'waitForMethod']).forEach(function (k) {
    if (options && options[k] !== undefined) {
      o[k] = options[k];
      any = true;
    }
  });
  return any ? o : undefined;
}

// copies the request-time budget fields a caller may pass in camelCase
function timeoutsOf(options, into) {
  if (!options) return into;
  if (options.totalTimeout !== undefined) into.total_timeout = options.totalTimeout;
  if (options.responseTimeout !== undefined) into.response_timeout = options.responseTimeout;
  if (options.frameTimeout !== undefined) into.frame_timeout = options.frameTimeout;
  return into;
}

// A port as a rule author writes it -> the port block wb-mqtt-serial expects:
//   "/dev/ttyRS485-1"                               serial, 9600 N 8 2
//   { path, baudRate?, parity?, dataBits?, stopBits? }  (snake_case accepted too)
//   "192.168.1.50:502" | { ip, port }               Modbus TCP
function normalizePort(port) {
  if (typeof port === 'string') {
    var m = /^([^/\s:]+):(\d+)$/.exec(port);
    if (m) return { ip: m[1], port: Number(m[2]) };
    return { path: port, baud_rate: 9600, parity: 'N', data_bits: 8, stop_bits: 2 };
  }
  if (!isPlainObject(port)) {
    throw new TypeError('MqttRpc: a port is a path, "host:port" or an object with path or ip/port');
  }
  if (port.ip !== undefined || port.address !== undefined) {
    return { ip: String(port.ip !== undefined ? port.ip : port.address), port: Number(port.port) };
  }
  if (typeof port.path !== 'string' || port.path === '') {
    throw new TypeError('MqttRpc: a serial port needs its path');
  }
  var v = function (camel, snake, dflt) {
    if (port[camel] !== undefined) return port[camel];
    if (port[snake] !== undefined) return port[snake];
    return dflt;
  };
  return {
    path: port.path,
    baud_rate: v('baudRate', 'baud_rate', 9600),
    parity: v('parity', 'parity', 'N'),
    data_bits: v('dataBits', 'data_bits', 8),
    stop_bits: v('stopBits', 'stop_bits', 2),
  };
}

function isTcpPort(p) {
  return p.ip !== undefined;
}

// "12" -> 12, "0x0a" -> 10; anything else as is (undefined for none)
function numericId(v) {
  if (v === undefined || v === null || v === '') return undefined;
  if (typeof v === 'number') return v;
  var text = String(v).trim();
  if (/^\d+$/.test(text)) return Number(text);
  if (/^0x[0-9a-f]+$/i.test(text)) return parseInt(text, 16);
  return v;
}

function tcpProtocol(rtuOverTcp) {
  return rtuOverTcp ? 'modbus' : 'modbus-tcp';
}

function assign(target) {
  for (var i = 1; i < arguments.length; i++) {
    var src = arguments[i];
    if (!src) continue;
    Object.keys(src).forEach(function (k) {
      if (src[k] !== undefined) target[k] = src[k];
    });
  }
  return target;
}

// ---- retained state topics (scan progress, firmware updates) ----

var topicWatch = {}; // topic -> { known, value, waiters: [fn] }

function watchTopic(topic) {
  var entry = topicWatch[topic];
  if (entry) return entry;
  entry = topicWatch[topic] = { known: false, value: undefined, waiters: [] };
  trackMqtt(topic, function (msg) {
    entry.known = true;
    entry.value = msg.value;
    var ws = entry.waiters;
    entry.waiters = [];
    for (var i = 0; i < ws.length; i++) ws[i](msg.value);
  });
  return entry;
}

// the current (retained) value of a topic, or undefined when nothing is
// there within timeoutMs; later reads are instant and follow updates
function readTopic(topic, timeoutMs) {
  var entry = watchTopic(topic);
  if (entry.known) return Promise.resolve(entry.value);
  return new Promise(function (resolve) {
    var timer = null;
    var settle = function (v) {
      if (timer !== null) clearTimeout(timer);
      resolve(v);
    };
    if (timeoutMs > 0) {
      timer = setTimeout(function () {
        var idx = entry.waiters.indexOf(settle);
        if (idx >= 0) entry.waiters.splice(idx, 1);
        resolve(undefined);
      }, timeoutMs);
    }
    entry.waiters.push(settle);
  });
}

// resolves with the first value (current or later) accepted by the predicate
function waitTopic(topic, predicate, timeoutMs, what) {
  var entry = watchTopic(topic);
  if (entry.known && predicate(entry.value)) return Promise.resolve(entry.value);
  return new Promise(function (resolve, reject) {
    var timer = null;
    var check = function (v) {
      if (!predicate(v)) {
        entry.waiters.push(check);
        return;
      }
      if (timer !== null) clearTimeout(timer);
      resolve(v);
    };
    if (timeoutMs > 0) {
      timer = setTimeout(function () {
        var idx = entry.waiters.indexOf(check);
        if (idx >= 0) entry.waiters.splice(idx, 1);
        reject(new TimeoutError(what + ' did not finish in ' + timeoutMs + ' ms', 'MqttTimeoutError'));
      }, timeoutMs);
    }
    entry.waiters.push(check);
  });
}

function parseJsonOr(text, fallback) {
  if (typeof text !== 'string' || text === '') return fallback;
  try {
    return JSON.parse(text);
  } catch (e) {
    return fallback;
  }
}

function serviceGroup(driver, rpc, probeService, probeMethod) {
  return {
    driver: driver,
    rpc: rpc,
    // whether the service is up (its presence topic for a representative method)
    isAvailable: function (timeout) {
      return hasMethod(driver, probeService, probeMethod, timeout);
    },
    waitUntilAvailable: function (timeout) {
      return waitForMethod(driver, probeService, probeMethod, timeout);
    },
  };
}

// ---- Modbus encoding ----

// a Modbus exception returned by the device (function 1-6, 15, 16, 23)
function ModbusError(code, message) {
  var e = new Error(message);
  this.name = 'ModbusError';
  this.message = message;
  this.stack = e.stack;
  this.code = code;
}
ModbusError.prototype = Object.create(Error.prototype);
ModbusError.prototype.constructor = ModbusError;

function hexOfU16(n) {
  if (typeof n !== 'number' || !isFinite(n) || n < 0 || n > 0xffff || n % 1 !== 0) {
    throw new TypeError('MqttRpc: a register value must be an integer 0..65535, got ' + n);
  }
  return ('000' + n.toString(16)).slice(-4);
}

function u16ArrayFromHex(hex) {
  var out = [];
  for (var i = 0; i + 4 <= hex.length; i += 4) out.push(parseInt(hex.slice(i, i + 4), 16));
  return out;
}

function bitsFromHex(hex, count) {
  var out = [];
  for (var i = 0; i < count; i++) {
    var byte = parseInt(hex.slice((i >> 3) * 2, (i >> 3) * 2 + 2), 16);
    out.push(((byte >> (i & 7)) & 1) === 1);
  }
  return out;
}

function hexOfBits(bools) {
  var bytes = [];
  for (var i = 0; i < bools.length; i++) {
    if (i % 8 === 0) bytes.push(0);
    if (bools[i]) bytes[bytes.length - 1] |= 1 << i % 8;
  }
  return bytes
    .map(function (b) {
      return ('0' + b.toString(16)).slice(-2);
    })
    .join('');
}

function checkAddress(address) {
  if (typeof address !== 'number' || address < 0 || address > 0xffff || address % 1 !== 0) {
    throw new TypeError('MqttRpc: a register address must be an integer 0..65535, got ' + address);
  }
  return address;
}

function checkCount(count, max) {
  if (count === undefined) return 1;
  if (typeof count !== 'number' || count < 1 || count % 1 !== 0) {
    throw new TypeError('MqttRpc: count must be a positive integer, got ' + count);
  }
  if (max !== undefined && count > max) {
    throw new TypeError('MqttRpc: at most ' + max + ' items per Modbus request, got ' + count);
  }
  return count;
}

// ---- wb-mqtt-serial ----

var serialRpc = {
  config: service('wb-mqtt-serial', 'config', ['Load', 'GetSchema']),
  templates: service('wb-mqtt-serial', 'templates', ['Upload', 'Delete']),
  ports: service('wb-mqtt-serial', 'ports', ['Load']),
  port: service('wb-mqtt-serial', 'port', ['Load', 'Setup', 'Scan']),
  device: service('wb-mqtt-serial', 'device', ['LoadConfig', 'Load', 'Set', 'Probe', 'SetPoll']),
  fwUpdate: service('wb-mqtt-serial', 'fw-update', FW_UPDATE_METHODS),
};
// serial port/device operations carry their own time budget (total_timeout, ms);
// Probe runs on a fixed server-side budget: nothing to stretch
['Load', 'Setup', 'Scan'].forEach(function (method) {
  serialRpc.port[method] = budgetedMethod('wb-mqtt-serial', 'port', method, 'total_timeout', 1, 10000);
});
['LoadConfig', 'Load', 'Set'].forEach(function (method) {
  serialRpc.device[method] = budgetedMethod('wb-mqtt-serial', 'device', method, 'total_timeout', 1, 10000);
});

var SERIAL_FW_STATE_TOPIC = '/wb-mqtt-serial/firmware_update/state';
var DEVICE_MANAGER_FW_STATE_TOPIC = '/wb-device-manager/firmware_update/state';
var DEVICE_MANAGER_STATE_TOPIC = '/wb-device-manager/state';

// The device list as the config editor shows it: every device of every
// port with its MQTT id (the explicit "id", else "<template mqtt-id>_<slave_id>")
function listSerialDevices(cfg) {
  var mqttIdOf = {};
  (cfg.types || []).forEach(function (group) {
    (group.types || []).forEach(function (t) {
      mqttIdOf[t.type] = t['mqtt-id'];
    });
  });
  var out = [];
  var ports = (cfg.config && cfg.config.ports) || [];
  ports.forEach(function (port) {
    // a port without explicit line settings runs on the driver's defaults (9600 N 8 1)
    var portInfo =
      port.path !== undefined
        ? {
            path: port.path,
            baud_rate: port.baud_rate !== undefined ? port.baud_rate : 9600,
            parity: port.parity !== undefined ? port.parity : 'N',
            data_bits: port.data_bits !== undefined ? port.data_bits : 8,
            stop_bits: port.stop_bits !== undefined ? port.stop_bits : 1,
          }
        : { ip: port.address, port: port.port };
    (port.devices || []).forEach(function (d) {
      var slaveText = d.slave_id !== undefined && d.slave_id !== null ? String(d.slave_id) : '';
      var id = d.id;
      if (!id && mqttIdOf[d.device_type]) {
        id = mqttIdOf[d.device_type] + (slaveText !== '' ? '_' + slaveText : '');
      }
      out.push({
        id: id,
        type: d.device_type,
        name: d.name,
        // Modbus addresses as numbers (the firmware services insist on
        // integers); other protocols may use non-numeric ids
        slaveId: numericId(d.slave_id),
        enabled: d.enabled !== false && port.enabled !== false,
        port: portInfo,
        config: d,
      });
    });
  });
  return out;
}

var serial = serviceGroup('wb-mqtt-serial', serialRpc, 'config', 'Load');

serial.ports = function (options) {
  return serialRpc.ports.Load({}, optionsOf(options));
};
serial.config = function (options) {
  var params = {};
  if (options && options.lang) params.lang = options.lang;
  return serialRpc.config.Load(params, optionsOf(options));
};
serial.deviceTypes = function (options) {
  return serial.config(options).then(function (cfg) {
    var out = [];
    (cfg.types || []).forEach(function (group) {
      (group.types || []).forEach(function (t) {
        out.push(assign({ group: group.name }, t));
      });
    });
    return out;
  });
};
serial.deviceSchema = function (type, options) {
  return serialRpc.config.GetSchema({ type: type }, optionsOf(options));
};
serial.devices = function (options) {
  return serial.config(options).then(listSerialDevices);
};
serial.uploadTemplate = function (filename, content, options) {
  var params = { filename: filename, content: typeof content === 'string' ? content : JSON.stringify(content) };
  if (options && options.force) params.force = true;
  if (options && options.lang) params.lang = options.lang;
  return serialRpc.templates.Upload(params, optionsOf(options));
};
serial.deleteTemplate = function (type, options) {
  var params = { type: type };
  if (options && options.force) params.force = true;
  if (options && options.lang) params.lang = options.lang;
  return serialRpc.templates.Delete(params, optionsOf(options));
};
// serial.scan(port[, options]) -> ScannedDevice[]; a scan that stopped
// early rejects with an Error carrying the devices found so far
serial.scan = function (port, options) {
  var params = normalizePort(port);
  if (options && options.command !== undefined) params.command = options.command;
  if (options && options.mode !== undefined) params.mode = options.mode;
  if (options && options.totalTimeout !== undefined) params.total_timeout = options.totalTimeout;
  return serialRpc.port.Scan(params, optionsOf(options)).then(function (r) {
    var devices = (r && r.devices) || [];
    if (r && r.error) {
      var err = new Error('scan of ' + (params.path || params.ip + ':' + params.port) + ' failed: ' + r.error);
      err.devices = devices;
      throw err;
    }
    return devices;
  });
};
serial.probe = function (port, slaveId, options) {
  return serial.device({ port: port, slaveId: slaveId }).probe(options);
};
serial.setup = function (port, items, options) {
  var p = normalizePort(port);
  var params = isTcpPort(p) ? { ip: p.ip, port: p.port } : { path: p.path };
  params.items = (items || []).map(function (item) {
    var out = {};
    if (item.slaveId !== undefined) out.slave_id = item.slaveId;
    if (item.slave_id !== undefined) out.slave_id = item.slave_id;
    if (item.sn !== undefined) out.sn = item.sn;
    var v = function (camel, snake) {
      if (item[camel] !== undefined) return item[camel];
      return item[snake];
    };
    if (v('baudRate', 'baud_rate') !== undefined) out.baud_rate = v('baudRate', 'baud_rate');
    if (item.parity !== undefined) out.parity = item.parity;
    if (v('dataBits', 'data_bits') !== undefined) out.data_bits = v('dataBits', 'data_bits');
    if (v('stopBits', 'stop_bits') !== undefined) out.stop_bits = v('stopBits', 'stop_bits');
    var cfg = item.set || item.cfg;
    if (cfg) {
      out.cfg = {};
      if (cfg.baudRate !== undefined) out.cfg.baud_rate = cfg.baudRate;
      if (cfg.baud_rate !== undefined) out.cfg.baud_rate = cfg.baud_rate;
      if (cfg.parity !== undefined) {
        // the driver takes the new parity as a number: 0 N, 1 O, 2 E
        var code = typeof cfg.parity === 'number' ? cfg.parity : { N: 0, O: 1, E: 2 }[cfg.parity];
        if (code === undefined) throw new TypeError('MqttRpc.serial.setup: parity is N, O or E');
        out.cfg.parity = code;
      }
      if (cfg.stopBits !== undefined) out.cfg.stop_bits = cfg.stopBits;
      if (cfg.stop_bits !== undefined) out.cfg.stop_bits = cfg.stop_bits;
      if (cfg.slaveId !== undefined) out.cfg.slave_id = cfg.slaveId;
      if (cfg.slave_id !== undefined) out.cfg.slave_id = cfg.slave_id;
    }
    return out;
  });
  if (options && options.totalTimeout !== undefined) params.total_timeout = options.totalTimeout;
  return serialRpc.port.Setup(params, optionsOf(options));
};

// firmware helpers shared by wb-mqtt-serial and wb-device-manager
function fwTarget(port, slaveId, allowTcp) {
  var p = normalizePort(port);
  if (isTcpPort(p)) {
    if (!allowTcp) throw new TypeError('MqttRpc: wb-mqtt-serial flashes over serial ports only');
    return { slave_id: numericId(slaveId), port: { address: p.ip, port: p.port } };
  }
  return {
    slave_id: numericId(slaveId),
    port: { path: p.path, baud_rate: p.baud_rate, parity: p.parity, data_bits: p.data_bits, stop_bits: p.stop_bits },
  };
}

function fwHelpers(rpc, stateTopic, allowTcp) {
  var portKey = function (port) {
    var p = normalizePort(port);
    return isTcpPort(p) ? p.ip + ':' + p.port : p.path;
  };
  // the state lists devices as { port: { path }, slave_id, progress, error }
  var entryOf = function (state, port, slaveId) {
    var list = (state && state.devices) || [];
    var key = portKey(port);
    for (var i = 0; i < list.length; i++) {
      var p = list[i].port;
      var path = isPlainObject(p) ? p.path : p;
      if (path === key && String(list[i].slave_id) === String(slaveId)) return list[i];
    }
    return null;
  };
  var withProtocol = function (t, port, options) {
    if (options && options.protocol) t.protocol = options.protocol;
    else if (isTcpPort(normalizePort(port))) t.protocol = tcpProtocol(options && options.rtuOverTcp);
    return t;
  };
  var h = {};
  h.firmwareInfo = function (port, slaveId, options) {
    var t = withProtocol(fwTarget(port, slaveId, allowTcp), port, options);
    return rpc.GetFirmwareInfo(t, optionsOf(options));
  };
  h.firmwareUpdateState = function (options) {
    var timeout = options && options.timeout !== undefined ? options.timeout : defaults.hasMethodTimeout;
    return readTopic(stateTopic, timeout).then(function (v) {
      return parseJsonOr(v, { devices: [] });
    });
  };
  // resolves when the device is no longer being flashed; rejects with the
  // error recorded for it (clear it with clearFirmwareError). The state
  // topic may still show the previous picture right after Update returns,
  // so the device is first awaited to appear (startTimeout, default 10 s -
  // none: nothing to wait for), then to disappear.
  // the device's entry as the topic shows it now, as text (or 'null')
  var currentEntry = function (port, slaveId) {
    var entry = watchTopic(stateTopic);
    return entry.known ? tryStringify(entryOf(parseJsonOr(entry.value, null), port, slaveId)) : 'null';
  };
  // stale: what the topic said before the update was requested - an old
  // entry (a past error stays listed until it is cleared) must not pass
  // for the new one. updateFirmware captures it before sending the request.
  // Standalone, it waits for whatever the topic shows for the device now
  // (an update in progress, or a recorded failure). Called by
  // updateFirmware, it gets `stale` - the entry seen before the request
  // was sent - and ignores it, so a failure left from an earlier attempt
  // is not taken for this one.
  h.waitForFirmwareUpdate = function (port, slaveId, options, stale) {
    var timeout = options && options.timeout !== undefined ? options.timeout : 600000;
    var startTimeout = options && options.startTimeout !== undefined ? options.startTimeout : 10000;
    var stageTimeout = options && options.stageTimeout !== undefined ? options.stageTimeout : 2000;
    var onProgress = options && options.onProgress;
    var what = 'firmware update of ' + portKey(port) + ':' + slaveId;
    if (stale === undefined) stale = 'null';
    var last = null;
    var track = function (v) {
      var entry = entryOf(parseJsonOr(v, null), port, slaveId);
      var fresh = entry !== null && tryStringify(entry) !== stale;
      if (fresh && onProgress && entry.progress !== (last && last.progress)) {
        try {
          onProgress(entry);
        } catch (e) {
          log.error('MqttRpc: onProgress of {} failed: {}', what, e);
        }
      }
      last = fresh ? entry : null;
      return fresh ? entry : null;
    };
    var finished = function (entry) {
      return !entry || (entry.error !== undefined && entry.error !== null);
    };
    var untilDone = function () {
      return waitTopic(
        stateTopic,
        function (v) {
          return finished(track(v));
        },
        timeout,
        what
      ).then(function () {
        if (last && last.error) {
          var err = new Error('firmware update failed: ' + (last.error.message || JSON.stringify(last.error)));
          err.state = last;
          throw err;
        }
        // the services flash the components of a device under a fresh entry
        // after the firmware one is gone: give the next stage a moment to show
        // up (stageTimeout 0: do not wait for one)
        if (!(stageTimeout > 0)) return undefined;
        stale = 'null';
        return waitTopic(
          stateTopic,
          function (v) {
            return track(v) !== null;
          },
          stageTimeout,
          what
        ).then(untilDone, function (e) {
          if (e instanceof TimeoutError) return undefined; // nothing followed: done
          throw e;
        });
      });
    };
    return waitTopic(
      stateTopic,
      function (v) {
        var entry = track(v);
        return entry !== null && tryStringify(entry) !== stale;
      },
      startTimeout,
      what
    ).then(untilDone, function (e) {
      if (e instanceof TimeoutError) return undefined; // never listed: nothing in progress
      throw e;
    });
  };
  h.updateFirmware = function (port, slaveId, options) {
    var t = withProtocol(fwTarget(port, slaveId, allowTcp), port, options);
    if (options && options.type) t.type = options.type;
    if (options && options.wait === false) return rpc.Update(t, optionsOf(options));
    // read the retained state first (a failure left from an earlier attempt
    // stays listed until cleared), remember the device's entry, then send:
    // whatever is listed for the device after that is this update's
    return readTopic(stateTopic, 1000).then(function () {
      var stale = currentEntry(port, slaveId);
      return rpc.Update(t, optionsOf(options)).then(function () {
        return h.waitForFirmwareUpdate(port, slaveId, options, stale);
      });
    });
  };
  h.restoreFirmware = function (port, slaveId, options) {
    var t = withProtocol(fwTarget(port, slaveId, allowTcp), port, options);
    return rpc.Restore(t, optionsOf(options));
  };
  h.clearFirmwareError = function (port, slaveId, options) {
    var p = normalizePort(port);
    if (isTcpPort(p) && !allowTcp) throw new TypeError('MqttRpc: wb-mqtt-serial flashes over serial ports only');
    // ClearError takes the port as a path; a TCP port is "host:port" here
    var t = { slave_id: numericId(slaveId), port: { path: isTcpPort(p) ? p.ip + ':' + p.port : p.path } };
    if (options && options.type) t.type = options.type;
    return rpc.ClearError(t, optionsOf(options));
  };
  return h;
}

assign(serial, fwHelpers(serialRpc.fwUpdate, SERIAL_FW_STATE_TOPIC, false));

// A device handle: a configured device by its MQTT id, or an address on a port.
//   MqttRpc.serial.device('wb-mr6c_12')
//   MqttRpc.serial.device({ port: '/dev/ttyRS485-1', slaveId: 12 })
//   MqttRpc.serial.device({ port: { ip: '10.0.0.5', port: 502 }, slaveId: 1, deviceType: 'WB-MR6C' })
function SerialDevice(target) {
  if (typeof target === 'string') {
    checkTopicPart('device id', target);
    this.id = target;
    this.port = undefined;
    this.slaveId = undefined;
  } else if (isPlainObject(target) && target.port !== undefined) {
    this.id = undefined;
    this.port = normalizePort(target.port);
    if (target.slaveId === undefined || target.slaveId === null) {
      throw new TypeError('MqttRpc.serial.device: slaveId is required with a port');
    }
    this.slaveId = numericId(target.slaveId);
    this.deviceType = target.deviceType;
    // Modbus RTU frames over a TCP socket (a transparent RTU-over-TCP gateway)
    this.rtuOverTcp = !!target.rtuOverTcp;
  } else {
    throw new TypeError('MqttRpc.serial.device: a device id or { port, slaveId }');
  }
}

// the config entry of a configured device (port, address, type), read
// from the driver's config every time (it may have been edited)
SerialDevice.prototype.resolve = function () {
  var self = this;
  if (this.port) {
    return Promise.resolve({ port: this.port, slaveId: this.slaveId, type: this.deviceType });
  }
  return serial.devices().then(function (devices) {
    for (var i = 0; i < devices.length; i++) {
      if (devices[i].id === self.id) {
        return { port: devices[i].port, slaveId: devices[i].slaveId, type: devices[i].type };
      }
    }
    throw new Error('MqttRpc.serial: device ' + self.id + ' is not in the wb-mqtt-serial config');
  });
};

SerialDevice.prototype._portParams = function () {
  var p = assign({}, this.port);
  p.protocol = isTcpPort(p) ? tcpProtocol(this.rtuOverTcp) : 'modbus';
  p.slave_id = this.slaveId;
  return p;
};

// a Modbus request; resolves with the response data as a hex string
// (empty for writes), rejects with ModbusError on a device exception
SerialDevice.prototype.modbus = function (fn, address, options) {
  var params = this.id ? { device_id: this.id } : this._portParams();
  params.function = fn;
  params.address = checkAddress(address);
  if (options && options.count !== undefined) params.count = checkCount(options.count);
  // function 23 (read/write): the write side
  if (options && options.writeAddress !== undefined) params.write_address = checkAddress(options.writeAddress);
  if (options && options.writeCount !== undefined) params.write_count = checkCount(options.writeCount);
  if (options && options.data !== undefined) {
    params.msg = options.data;
    params.format = 'HEX';
  } else {
    params.format = 'HEX';
  }
  timeoutsOf(options, params);
  return serialRpc.port.Load(params, optionsOf(options)).then(function (r) {
    if (r && r.exception) throw new ModbusError(r.exception.code, r.exception.msg);
    return (r && r.response) || '';
  });
};

// Modbus limits one request to 125 registers or 2000 bits read, 123
// registers or 1968 bits written
SerialDevice.prototype.readHolding = function (address, count, options) {
  return this.modbus(3, address, assign({ count: checkCount(count, 125) }, options)).then(u16ArrayFromHex);
};
SerialDevice.prototype.readInput = function (address, count, options) {
  return this.modbus(4, address, assign({ count: checkCount(count, 125) }, options)).then(u16ArrayFromHex);
};
SerialDevice.prototype.readCoils = function (address, count, options) {
  var n = checkCount(count, 2000);
  return this.modbus(1, address, assign({ count: n }, options)).then(function (hex) {
    return bitsFromHex(hex, n);
  });
};
SerialDevice.prototype.readDiscrete = function (address, count, options) {
  var n = checkCount(count, 2000);
  return this.modbus(2, address, assign({ count: n }, options)).then(function (hex) {
    return bitsFromHex(hex, n);
  });
};
// one register -> function 6 (an unsigned 16-bit value)
SerialDevice.prototype.writeHolding = function (address, value, options) {
  if (Array.isArray(value)) throw new TypeError('MqttRpc: writeHolding takes one value; use writeHoldings for several');
  return this.modbus(6, address, assign({ data: hexOfU16(value) }, options)).then(function () {});
};
// several registers -> function 16
SerialDevice.prototype.writeHoldings = function (address, values, options) {
  if (!Array.isArray(values)) throw new TypeError('MqttRpc: writeHoldings takes an array of values');
  checkCount(values.length, 123);
  return this.modbus(16, address, assign({ count: values.length, data: values.map(hexOfU16).join('') }, options)).then(
    function () {}
  );
};
// one coil -> function 5
SerialDevice.prototype.writeCoil = function (address, value, options) {
  if (Array.isArray(value)) throw new TypeError('MqttRpc: writeCoil takes one value; use writeCoils for several');
  return this.modbus(5, address, assign({ data: value ? 'ff00' : '0000' }, options)).then(function () {});
};
// several coils -> function 15
SerialDevice.prototype.writeCoils = function (address, values, options) {
  if (!Array.isArray(values)) throw new TypeError('MqttRpc: writeCoils takes an array of values');
  checkCount(values.length, 1968);
  var bools = values.map(function (v) {
    return !!v;
  });
  return this.modbus(15, address, assign({ count: bools.length, data: hexOfBits(bools) }, options)).then(
    function () {}
  );
};
// arbitrary bytes through the port (explicit ports only); hex in, hex out
SerialDevice.prototype.raw = function (hex, responseSize, options) {
  if (!this.port) throw new TypeError('MqttRpc: raw requests need an explicit port, not a device id');
  var params = assign({}, this.port);
  params.protocol = 'raw';
  params.msg = hex;
  params.response_size = responseSize;
  params.format = 'HEX';
  timeoutsOf(options, params);
  return serialRpc.port.Load(params, optionsOf(options)).then(function (r) {
    return (r && r.response) || '';
  });
};

SerialDevice.prototype._deviceParams = function (extra) {
  var params;
  if (this.id) {
    params = { device_id: this.id };
  } else {
    if (!this.deviceType) {
      throw new TypeError('MqttRpc: settings/read/write of a device on a port need its deviceType');
    }
    params = assign({}, this.port);
    params.slave_id = this.slaveId;
    params.device_type = this.deviceType;
    // the driver's default over TCP is RTU frames: say so explicitly either way
    if (isTcpPort(params)) params.modbus_mode = this.rtuOverTcp ? 'RTU' : 'TCP';
  }
  return assign(params, extra);
};
// the device's settings (its "parameters" in the template), plus fw/model
SerialDevice.prototype.settings = function (options) {
  var extra = {};
  if (options && options.force) extra.force = true;
  timeoutsOf(options, extra);
  return serialRpc.device.LoadConfig(this._deviceParams(extra), optionsOf(options));
};
// channels and parameters by name: { channels: [...], parameters: [...] }
SerialDevice.prototype.read = function (what, options) {
  var extra = {};
  if (what && what.channels) extra.channels = what.channels;
  if (what && what.parameters) extra.parameters = what.parameters;
  timeoutsOf(options, extra);
  return serialRpc.device.Load(this._deviceParams(extra), optionsOf(options));
};
// the driver reads channel and parameter values the way it reads its
// config: numbers and strings, no JSON booleans - true/false go as 1/0
function driverValues(values) {
  var out = {};
  Object.keys(values).forEach(function (k) {
    var v = values[k];
    out[k] = typeof v === 'boolean' ? (v ? 1 : 0) : v;
  });
  return out;
}

SerialDevice.prototype.write = function (what, options) {
  var extra = {};
  if (what && what.channels) extra.channels = driverValues(what.channels);
  if (what && what.parameters) extra.parameters = driverValues(what.parameters);
  timeoutsOf(options, extra);
  return serialRpc.device.Set(this._deviceParams(extra), optionsOf(options)).then(function () {});
};

// names as arguments or one array; a trailing options object is allowed
function namesAndOptions(args) {
  var list = Array.prototype.slice.call(args);
  while (list.length && list[list.length - 1] === undefined) list.pop(); // an omitted options argument
  var options;
  if (list.length && isPlainObject(list[list.length - 1])) options = list.pop();
  if (list.length === 1 && Array.isArray(list[0])) list = list[0];
  list.forEach(function (n) {
    if (typeof n !== 'string' || n === '') throw new TypeError('MqttRpc: a channel or parameter name is a non-empty string');
  });
  return { names: list, options: options };
}

// readChannels('Urms L1', 'Irms L1') -> { 'Urms L1': 230.1, 'Irms L1': 0.5 }
SerialDevice.prototype.readChannels = function () {
  var a = namesAndOptions(arguments);
  return this.read({ channels: a.names }, a.options).then(function (r) {
    return r.channels || {};
  });
};
// readChannel('Urms L1') -> 230.1
SerialDevice.prototype.readChannel = function (name, options) {
  return this.readChannels([name], options).then(function (values) {
    return values[name];
  });
};
SerialDevice.prototype.readParameters = function () {
  var a = namesAndOptions(arguments);
  return this.read({ parameters: a.names }, a.options).then(function (r) {
    return r.parameters || {};
  });
};
SerialDevice.prototype.readParameter = function (id, options) {
  return this.readParameters([id], options).then(function (values) {
    return values[id];
  });
};
// writeChannels({ K1: 1, K2: 0 }), writeChannel('K1', 1)
SerialDevice.prototype.writeChannels = function (values, options) {
  if (!isPlainObject(values)) throw new TypeError('MqttRpc: channel values are an object { name: value }');
  return this.write({ channels: values }, options);
};
SerialDevice.prototype.writeChannel = function (name, value, options) {
  var values = {};
  values[name] = value;
  return this.writeChannels(values, options);
};
SerialDevice.prototype.setParameters = function (values, options) {
  if (!isPlainObject(values)) throw new TypeError('MqttRpc: parameter values are an object { id: value }');
  return this.write({ parameters: values }, options);
};
SerialDevice.prototype.setParameter = function (id, value, options) {
  var values = {};
  values[id] = value;
  return this.setParameters(values, options);
};
// who is at this address: a ScannedDevice, or null when nothing answers
SerialDevice.prototype.probe = function (options) {
  if (!this.port) throw new TypeError('MqttRpc: probe needs a port and a slaveId, not a device id');
  var params = assign({}, this.port);
  params.slave_id = this.slaveId;
  if (options && options.protocol) params.protocol = options.protocol;
  else if (isTcpPort(params)) params.protocol = tcpProtocol(this.rtuOverTcp); // the driver's default is RTU over TCP
  return serialRpc.device.Probe(params, optionsOf(options)).then(function (r) {
    return r && Object.keys(r).length > 0 ? r : null;
  });
};
SerialDevice.prototype.setPolling = function (enabled, options) {
  var params;
  if (this.id) {
    params = { device_id: this.id };
  } else {
    params = isTcpPort(this.port) ? { ip: this.port.ip, port: this.port.port } : { path: this.port.path };
    params.slave_id = this.slaveId;
  }
  params.poll = !!enabled;
  return serialRpc.device.SetPoll(params, optionsOf(options)).then(function () {});
};
SerialDevice.prototype.pausePolling = function (options) {
  return this.setPolling(false, options);
};
SerialDevice.prototype.resumePolling = function (options) {
  return this.setPolling(true, options);
};
// pauses polling around fn() (resumed even when fn throws)
SerialDevice.prototype.withPollingPaused = function (fn, options) {
  var self = this;
  return this.pausePolling(options).then(function () {
    return new Promise(function (resolve) {
      resolve(fn(self));
    }).then(
      function (result) {
        return self.resumePolling(options).then(function () {
          return result;
        });
      },
      function (err) {
        return self.resumePolling(options).then(
          function () {
            throw err;
          },
          function () {
            throw err; // fn's error is the one to report
          }
        );
      }
    );
  });
};
// firmware: resolved through the config for a device id
['firmwareInfo', 'updateFirmware', 'waitForFirmwareUpdate', 'restoreFirmware', 'clearFirmwareError'].forEach(
  function (name) {
    SerialDevice.prototype[name] = function (options) {
      return this.resolve().then(function (r) {
        return serial[name](r.port, r.slaveId, options);
      });
    };
  }
);

serial.device = function (target) {
  return new SerialDevice(target);
};

// ---- wb-mqtt-db ----

var dbRpc = {
  history: service('db_logger', 'history', ['get_values', 'get_channels']),
};
// a history query carries its own budget (request_timeout, s)
dbRpc.history.get_values = budgetedMethod('db_logger', 'history', 'get_values', 'request_timeout', 1000, 10000);

var db = serviceGroup('db_logger', dbRpc, 'history', 'get_values');

// every channel the database knows: "device/control", record count, last time
db.channels = function (options) {
  return dbRpc.history.get_channels({}, optionsOf(options)).then(function (r) {
    var chans = (r && r.channels) || {};
    return Object.keys(chans).map(function (key) {
      var pair = channelPair(key);
      return {
        channel: key,
        device: pair[0],
        control: pair[1],
        items: chans[key].items,
        lastTime: fromSeconds(chans[key].last_ts),
      };
    });
  });
};

function historyParams(pair, options) {
  var params = { channels: [pair], ver: 1, with_milliseconds: true };
  var ts = {};
  if (options) {
    if (options.last !== undefined) {
      if (typeof options.last !== 'number' || !(options.last > 0)) {
        throw new TypeError('MqttRpc: last must be a positive number of milliseconds');
      }
      if (options.since !== undefined) throw new TypeError('MqttRpc: last and since exclude each other');
      ts.gt = Date.now() / 1000 - options.last / 1000;
    }
    // the database compares whole seconds: widen the window to the second
    if (options.since !== undefined) ts.gt = toSeconds(options.since, 'since');
    if (options.until !== undefined) ts.lt = Math.ceil(toSeconds(options.until, 'until'));
    if (ts.gt !== undefined) ts.gt = Math.floor(ts.gt);
    if (options.limit !== undefined) params.limit = options.limit;
    if (options.minInterval !== undefined) params.min_interval = options.minInterval;
    if (options.maxRecords !== undefined) params.max_records = options.maxRecords;
    if (options.requestTimeout !== undefined) params.request_timeout = options.requestTimeout;
    if (options.afterUid !== undefined) params.uid = { gt: options.afterUid };
  }
  if (Object.keys(ts).length) params.timestamp = ts;
  return params;
}

function historyRecord(pair, row) {
  return {
    channel: pair[0] + '/' + pair[1],
    device: pair[0],
    control: pair[1],
    time: fromSeconds(row.t),
    value: parseValue(row.v),
    min: row.min !== undefined ? parseValue(row.min) : undefined,
    max: row.max !== undefined ? parseValue(row.max) : undefined,
    retain: !!row.retain,
    uid: row.i,
  };
}

// db.query("wb-adc/Vin", { last: 3600000 }) -> { values: [record...], hasMore }
//   channel: "device/control", [device, control], or an array of those
//   options: since/until (Date or ms), last (ms), limit, minInterval (ms),
//   maxRecords (averaging), requestTimeout (s), afterUid
//   records: { channel, device, control, time: Date, value, min, max, retain, uid }
db.query = function (channels, options) {
  var pairs;
  if (typeof channels === 'string') {
    pairs = [channelPair(channels)];
  } else if (
    Array.isArray(channels) &&
    channels.length === 2 &&
    typeof channels[0] === 'string' &&
    typeof channels[1] === 'string' &&
    channels[0].indexOf('/') < 0 &&
    channels[1].indexOf('/') < 0
  ) {
    pairs = [channelPair(channels)]; // one [device, control] pair
  } else if (Array.isArray(channels) && channels.length) {
    pairs = channels.map(channelPair);
  } else {
    throw new TypeError('MqttRpc.db.query: channels are "device/control" strings or [device, control] pairs');
  }
  // one request per channel: the compact (ver 1) layout keys records by an
  // internal channel id, so a joint request could not be told apart
  return Promise.all(
    pairs.map(function (pair) {
      return dbRpc.history.get_values(historyParams(pair, options), optionsOf(options)).then(function (r) {
        return {
          values: ((r && r.values) || []).map(function (row) {
            return historyRecord(pair, row);
          }),
          hasMore: !!(r && r.has_more),
        };
      });
    })
  ).then(function (parts) {
    var values = [];
    var hasMore = false;
    parts.forEach(function (p) {
      values = values.concat(p.values);
      hasMore = hasMore || p.hasMore;
    });
    if (pairs.length > 1) {
      values.sort(function (a, b) {
        return a.time - b.time;
      });
    }
    return { values: values, hasMore: hasMore };
  });
};

// the latest record of a channel, or undefined when it was never logged
db.lastValue = function (channel, options) {
  var pair = channelPair(channel);
  return dbRpc.history.get_channels({}, optionsOf(options)).then(function (r) {
    var info = r && r.channels && r.channels[pair[0] + '/' + pair[1]];
    if (!info || !info.items) return undefined;
    return db
      .query(pair, assign({ since: (info.last_ts - 1) * 1000, until: (info.last_ts + 1) * 1000 }, options))
      .then(function (res) {
        return res.values.length ? res.values[res.values.length - 1] : undefined;
      });
  });
};

// the average over a period ({ last } or { since, until }): the mean of
// up to options.buckets (100) server-side averaged intervals (the database
// aligns them to the epoch, so the ends of the window weigh a little
// differently); undefined when nothing numeric was logged
db.average = function (channel, options) {
  var o = assign({}, options);
  var buckets = o.buckets !== undefined ? o.buckets : 100;
  delete o.buckets;
  delete o.limit;
  o.maxRecords = buckets;
  return db.query(channel, o).then(function (res) {
    var sum = 0;
    var n = 0;
    res.values.forEach(function (r) {
      if (typeof r.value === 'number') {
        sum += r.value;
        n++;
      }
    });
    return n ? sum / n : undefined;
  });
};

// ---- wb-rules editor ----

var rulesRpc = {
  Editor: service('wbrules', 'Editor', ['List', 'Load', 'Save', 'Remove', 'Rename', 'ChangeState', 'Check', 'GetTypes']),
};
var rules = serviceGroup('wbrules', rulesRpc, 'Editor', 'List');
rules.list = function (options) {
  return rulesRpc.Editor.List({}, optionsOf(options));
};
rules.load = function (path, options) {
  return rulesRpc.Editor.Load({ path: path }, optionsOf(options));
};
rules.save = function (path, content, options) {
  return rulesRpc.Editor.Save({ path: path, content: content }, optionsOf(options));
};
rules.remove = function (path, options) {
  return rulesRpc.Editor.Remove({ path: path }, optionsOf(options)).then(function () {});
};

// The editor learns about a renamed/enabled/disabled file from the file
// watcher a moment later; until then it still lists the old state and a
// follow-up call on the same file would see it (an enable right after a
// disable was silently a no-op). These helpers wait for the listing to
// catch up (settleTimeout, 5000 ms; 0: do not wait).
function untilListed(predicate, what, options) {
  var timeout = options && options.settleTimeout !== undefined ? options.settleTimeout : 5000;
  if (!(timeout > 0)) return Promise.resolve();
  var deadline = Date.now() + timeout;
  var poll = function () {
    return rules.list(options).then(function (files) {
      if (predicate(files)) return undefined;
      if (Date.now() >= deadline) {
        throw new TimeoutError(what + ' is not reflected by the editor after ' + timeout + ' ms', 'MqttTimeoutError');
      }
      return sleep(200).then(poll);
    });
  };
  return poll();
}
function listedState(files, path) {
  for (var i = 0; i < files.length; i++) if (files[i].virtualPath === path) return files[i].enabled;
  return undefined;
}
rules.rename = function (path, newPath, options) {
  return rulesRpc.Editor.Rename({ path: path, new_path: newPath }, optionsOf(options)).then(function () {
    return untilListed(
      function (files) {
        return listedState(files, newPath) !== undefined && listedState(files, path) === undefined;
      },
      'renaming ' + path,
      options
    );
  });
};
function changeState(path, state, options) {
  return rulesRpc.Editor.ChangeState({ path: path, state: state }, optionsOf(options)).then(function () {
    return untilListed(
      function (files) {
        return listedState(files, path) === state;
      },
      (state ? 'enabling ' : 'disabling ') + path,
      options
    );
  });
}
rules.enable = function (path, options) {
  return changeState(path, true, options);
};
rules.disable = function (path, options) {
  return changeState(path, false, options);
};
// the type-check verdict, polled (every options.interval ms, 200) while
// the check is still running
rules.check = function (path, options) {
  var deadline = Date.now() + (options && options.timeout !== undefined ? options.timeout : 30000);
  var interval = options && options.interval !== undefined ? options.interval : 200;
  var poll = function () {
    return rulesRpc.Editor.Check({ path: path }, optionsOf(options)).then(function (r) {
      if (r.status !== 'pending') return r;
      if (Date.now() >= deadline) throw new TimeoutError('type check of ' + path + ' still pending', 'MqttTimeoutError');
      return sleep(interval).then(poll);
    });
  };
  return poll();
};
rules.types = function (options) {
  return rulesRpc.Editor.GetTypes({}, optionsOf(options)).then(function (r) {
    return r.content;
  });
};

// ---- wb-mqtt-confed ----

var confedRpc = {
  Editor: service('confed', 'Editor', ['List', 'Load', 'Save']),
};
var confed = serviceGroup('confed', confedRpc, 'Editor', 'List');
confed.list = function (options) {
  return confedRpc.Editor.List({}, optionsOf(options));
};
confed.load = function (path, options) {
  return confedRpc.Editor.Load({ path: path }, optionsOf(options));
};
confed.save = function (path, content, options) {
  return confedRpc.Editor.Save({ path: path, content: content }, optionsOf(options)).then(function () {});
};
// load, let fn change the content (in place or by returning a new one), save
confed.update = function (path, fn, options) {
  return confed.load(path, options).then(function (loaded) {
    return new Promise(function (resolve) {
      resolve(fn(loaded.content, loaded));
    }).then(function (next) {
      var content = next === undefined ? loaded.content : next;
      return confed.save(path, content, options).then(function () {
        return content;
      });
    });
  });
};

// ---- wb-mqtt-logs ----

var logsRpc = {
  logs: service('wb_logs', 'logs', ['List', 'Load', 'CancelLoad']),
};
var logs = serviceGroup('wb_logs', logsRpc, 'logs', 'Load');
logs.services = function (options) {
  return logsRpc.logs.List({}, optionsOf(options)).then(function (r) {
    return (r && r.services) || [];
  });
};
logs.boots = function (options) {
  return logsRpc.logs.List({}, optionsOf(options)).then(function (r) {
    return ((r && r.boots) || []).map(function (b) {
      return { hash: b.hash, start: fromSeconds(b.start), end: b.end !== undefined ? fromSeconds(b.end) : undefined };
    });
  });
};
// logs.read({ service, since, levels, pattern, regex, caseSensitive, limit, boot, cursor, direction })
//   -> [{ time: Date, level, msg, service, cursor }]: the newest entries
//      first; with `since` (or direction "forward") the oldest first, from
//      that moment on; at most 100 per call
logs.read = function (options) {
  var params = {};
  var o = options || {};
  if (o.service !== undefined) params.service = o.service;
  if (o.boot !== undefined) params.boot = o.boot;
  if (o.since !== undefined) params.time = Math.floor(toSeconds(o.since, 'since'));
  if (o.levels !== undefined) params.levels = o.levels;
  if (o.pattern !== undefined) params.pattern = o.pattern;
  if (o.regex !== undefined) params.regex = o.regex;
  if (o.caseSensitive !== undefined) params['case-sensitive'] = o.caseSensitive;
  if (o.limit !== undefined) params.limit = o.limit;
  // the journal is walked backward from the cursor (or from the tail)
  // unless told otherwise; `since` alone means "from since onwards"
  var direction = o.direction || (o.since !== undefined ? 'forward' : undefined);
  if (o.cursor !== undefined) params.cursor = { id: o.cursor, direction: direction || 'backward' };
  else if (direction !== undefined) params.cursor = { direction: direction };
  return logsRpc.logs.Load(params, optionsOf(options)).then(function (entries) {
    var records = (entries || []).map(function (e) {
      return {
        time: new Date(e.time),
        level: e.level !== undefined ? e.level : 6,
        msg: e.msg,
        service: e.service,
        cursor: e.cursor,
      };
    });
    // the service hands out every page newest first; a forward read is
    // meant to be read in order, so it is turned around here
    if (direction === 'forward') records.reverse();
    return records;
  });
};
logs.tail = function (serviceName, count, options) {
  return logs.read(assign({ service: serviceName, limit: count || 50 }, options));
};
logs.cancel = function (options) {
  return logsRpc.logs.CancelLoad({}, optionsOf(options)).then(function () {});
};

// ---- wb-diag-collect ----

var diagRpc = {
  main: service('diag', 'main', ['diag', 'status']),
};
var diag = serviceGroup('diag', diagRpc, 'main', 'diag');
// collects a diagnostics archive; resolves with { basename, fullname }
diag.collect = function (options) {
  var timeout = options && options.timeout !== undefined ? options.timeout : 300000;
  // armed before the request: the artifact message is not retained
  var artifact = nextMqtt('/wb-diag-collect/artifact', timeout > 0 ? timeout : undefined);
  artifact.catch(function () {}); // reported through the chain below
  return diagRpc.main.diag({}, optionsOf(options)).then(function () {
    return artifact.then(function (msg) {
      var info = parseJsonOr(msg.value, null);
      if (!info || !info.fullname) throw new Error('diagnostics collection failed');
      return info;
    });
  });
};
diag.isAlive = function (options) {
  return diagRpc.main.status({}, optionsOf(options)).then(
    function (r) {
      return r === '1' || r === 1;
    },
    function () {
      return false;
    }
  );
};

// ---- wb-device-manager ----

var deviceManagerRpc = {
  busScan: service('wb-device-manager', 'bus-scan', ['Start', 'Stop']),
  fwUpdate: service('wb-device-manager', 'fw-update', FW_UPDATE_METHODS),
};
var deviceManager = serviceGroup('wb-device-manager', deviceManagerRpc, 'bus-scan', 'Start');
// the retained scan state: { scanning, progress, scanning_ports, devices, error }
deviceManager.state = function (options) {
  var timeout = options && options.timeout !== undefined ? options.timeout : defaults.hasMethodTimeout;
  return readTopic(DEVICE_MANAGER_STATE_TOPIC, timeout).then(function (v) {
    return parseJsonOr(v, null);
  });
};
// deviceManager.scan({ port, type, preserveOldResults, timeout, onProgress })
//   starts a scan and resolves with the devices found once it completes
deviceManager.scan = function (options) {
  var o = options || {};
  var params = {};
  if (o.type !== undefined) params.scan_type = o.type;
  if (o.preserveOldResults !== undefined) params.preserve_old_results = o.preserveOldResults;
  if (o.port !== undefined) {
    var p = normalizePort(o.port);
    params.port = { path: isTcpPort(p) ? p.ip + ':' + p.port : p.path };
    if (o.protocol) params.port.protocol = o.protocol;
    else if (isTcpPort(p)) params.port.protocol = tcpProtocol(o.rtuOverTcp); // the scanner's default is RTU over TCP
  }
  if (o.outOfOrderSlaveIds !== undefined) params.out_of_order_slave_ids = o.outOfOrderSlaveIds;
  var timeout = o.timeout !== undefined ? o.timeout : 600000;
  var startTimeout = o.startTimeout !== undefined ? o.startTimeout : 10000;
  watchTopic(DEVICE_MANAGER_STATE_TOPIC);
  var lastProgress = -1;
  var track = function (v) {
    var s = parseJsonOr(v, null);
    if (s && o.onProgress && s.progress !== lastProgress) {
      lastProgress = s.progress;
      o.onProgress(s);
    }
    return s;
  };
  return deviceManagerRpc.busScan.Start(params, optionsOf(options)).then(function () {
    // the retained state may still describe the previous scan: wait for
    // this one to start (or give up after startTimeout), then to finish
    return waitTopic(
      DEVICE_MANAGER_STATE_TOPIC,
      function (v) {
        var s = track(v);
        return !!s && s.scanning === true;
      },
      startTimeout,
      'bus scan'
    ).catch(function (e) {
      if (!(e instanceof TimeoutError)) throw e;
    }).then(function () {
      return waitTopic(
        DEVICE_MANAGER_STATE_TOPIC,
        function (v) {
          var s = track(v);
          return !!s && s.scanning === false;
        },
        timeout,
        'bus scan'
      );
    }).then(function (v) {
      var s = parseJsonOr(v, {});
      if (s.error) {
        var err = new Error('bus scan failed: ' + (s.error.message || JSON.stringify(s.error)));
        err.state = s;
        throw err;
      }
      return s.devices || [];
    });
  });
};
deviceManager.stopScan = function (options) {
  return deviceManagerRpc.busScan.Stop({}, optionsOf(options)).then(function () {});
};
assign(deviceManager, fwHelpers(deviceManagerRpc.fwUpdate, DEVICE_MANAGER_FW_STATE_TOPIC, true));

// ---- wb-mqtt-dali ----

var daliRpc = {
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
};
var dali = serviceGroup('wb-mqtt-dali', daliRpc, 'Bus', 'SendCommand');
// every bus of every gateway, flat: { id, name, gateway: { id, name }, devices, ... }
dali.buses = function (options) {
  return daliRpc.Editor.GetList({}, optionsOf(options)).then(function (gateways) {
    var out = [];
    (gateways || []).forEach(function (g) {
      (g.buses || []).forEach(function (b) {
        out.push(assign({ gateway: { id: g.id, name: g.name } }, b));
      });
    });
    return out;
  });
};
// runs DALI commands on a bus ("DAPC(A0, 0xFE)"); one result per command
dali.send = function (busId, commands, options) {
  var list = typeof commands === 'string' ? [commands] : commands;
  return daliRpc.Bus.SendCommand({ busId: busId, commands: list }, optionsOf(options));
};
dali.commands = function (options) {
  return daliRpc.Bus.ListCommands({}, optionsOf(options));
};

var services = {
  serial: serial,
  db: db,
  rules: rules,
  confed: confed,
  logs: logs,
  diag: diag,
  deviceManager: deviceManager,
  dali: dali,
};

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
  ModbusError: ModbusError,
  validate: validate,
  method: method,
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
