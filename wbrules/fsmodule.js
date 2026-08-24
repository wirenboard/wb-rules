'use strict';
/*
 * The built-in "fs" module of wb-rules: Node.js-shaped filesystem access
 * for rule scripts (require('fs'), require('fs/promises')).
 *
 * This file is embedded into the engine and compiled once per rule file
 * (modules are per realm), so everything here - the promises it creates,
 * the errors it throws - belongs to the requiring file. The actual I/O is
 * done by two realm-local builtins: _wbFsSync(op, args) runs an operation
 * on the engine loop, _wbFsAsync(op, args, cb) on a goroutine and reports
 * {error} or {result} back through cb. Argument validation, option
 * defaults and the Stats/Dirent/FSWatcher shapes live here.
 *
 * Differences from Node, deliberately: files are text (UTF-8 strings) -
 * there is no Buffer and no encoding other than utf8; the asynchronous
 * functions return promises instead of taking callbacks (fs.promises and
 * fs/promises expose the same functions); exists() exists as a promise
 * variant; fs.watch takes its listener directly and returns an object
 * with close() (no EventEmitter); readFile refuses files over 10 MiB.
 */

/* global _wbFsSync, _wbFsAsync, _wbFsWatch, _wbFsWatchClose */

const S_IFMT = 0o170000;
const S_IFREG = 0o100000;
const S_IFDIR = 0o040000;
const S_IFCHR = 0o020000;
const S_IFBLK = 0o060000;
const S_IFIFO = 0o010000;
const S_IFLNK = 0o120000;
const S_IFSOCK = 0o140000;

const constants = Object.freeze({
  F_OK: 0,
  R_OK: 4,
  W_OK: 2,
  X_OK: 1,
  COPYFILE_EXCL: 1,
  COPYFILE_FICLONE: 2,
  COPYFILE_FICLONE_FORCE: 4,
  S_IFMT,
  S_IFREG,
  S_IFDIR,
  S_IFCHR,
  S_IFBLK,
  S_IFIFO,
  S_IFLNK,
  S_IFSOCK,
});

const FLAGS = new Set(['r', 'r+', 'w', 'wx', 'w+', 'wx+', 'a', 'ax', 'a+', 'ax+']);

// ---------------------------------------------------------------------------
// errors (Node's shapes: TypeError with code for bad arguments, Error with
// code/errno/syscall/path/dest for failed operations)

function describe(value) {
  if (value === null || value === undefined) return String(value);
  if (typeof value === 'string') {
    const shown = value.length > 28 ? value.slice(0, 25) + '...' : value;
    return `type string ('${shown}')`;
  }
  if (typeof value === 'function') return `function ${value.name || '(anonymous)'}`;
  if (typeof value === 'object') {
    const ctor = value.constructor && value.constructor.name;
    return ctor ? `an instance of ${ctor}` : 'an object';
  }
  return `type ${typeof value} (${String(value)})`;
}

function codedError(Ctor, code, message) {
  const e = new Ctor(message);
  e.code = code;
  return e;
}

function invalidArgType(name, expected, actual) {
  return codedError(
    TypeError,
    'ERR_INVALID_ARG_TYPE',
    `The "${name}" argument must be of type ${expected}. Received ${describe(actual)}`
  );
}

function invalidArgValue(name, actual, reason) {
  return codedError(
    TypeError,
    'ERR_INVALID_ARG_VALUE',
    `The argument '${name}' ${reason}. Received ${describe(actual)}`
  );
}

function callbacksUnsupported(name) {
  return codedError(
    TypeError,
    'ERR_INVALID_ARG_TYPE',
    `fs.${name}() takes no callback: it returns a promise (use await or .then); ` +
      `the synchronous variant is fs.${name}Sync()`
  );
}

// fromDescriptor rebuilds the Error the engine described for a failed
// asynchronous operation (synchronous ones throw the Error directly).
function fromDescriptor(d) {
  const e = new Error(d.message);
  e.code = d.code;
  if (d.errno !== undefined) e.errno = d.errno;
  e.syscall = d.syscall;
  if (d.path !== undefined) e.path = d.path;
  if (d.dest !== undefined) e.dest = d.dest;
  return e;
}

// ---------------------------------------------------------------------------
// argument validation

function getPath(value, name = 'path') {
  if (typeof value !== 'string') throw invalidArgType(name, 'string', value);
  if (value.indexOf('\0') !== -1) {
    throw invalidArgValue(name, value, 'must be a string without null bytes');
  }
  return value;
}

function getOptions(options, defaults) {
  if (options === null || options === undefined) return defaults;
  if (typeof options === 'string') return Object.assign({}, defaults, { encoding: options });
  if (typeof options !== 'object') throw invalidArgType('options', 'Object or string', options);
  return Object.assign({}, defaults, options);
}

function checkEncoding(encoding) {
  if (encoding === null || encoding === undefined) return;
  if (typeof encoding !== 'string' || !/^utf-?8$/i.test(encoding)) {
    throw invalidArgValue(
      'encoding',
      encoding,
      "is not supported: the fs module reads and writes UTF-8 strings only ('utf8')"
    );
  }
}

function getMode(value, name, fallback) {
  if (value === null || value === undefined) {
    if (fallback === undefined) throw invalidArgType(name, 'number or octal string', value);
    return fallback;
  }
  if (typeof value === 'string') {
    if (!/^[0-7]+$/.test(value)) throw invalidArgValue(name, value, 'must be an octal string');
    return parseInt(value, 8);
  }
  if (typeof value !== 'number' || !Number.isInteger(value) || value < 0 || value > 0o7777) {
    throw invalidArgValue(name, value, 'must be an integer in the range 0..0o7777 or an octal string');
  }
  return value;
}

function getFlag(value, fallback) {
  if (value === null || value === undefined) return fallback;
  if (typeof value !== 'string' || !FLAGS.has(value)) {
    throw invalidArgValue('flag', value, "must be one of 'r', 'r+', 'w', 'wx', 'w+', 'wx+', 'a', 'ax', 'a+', 'ax+'");
  }
  return value;
}

function getData(value) {
  if (typeof value !== 'string') throw invalidArgType('data', 'string', value);
  return value;
}

function getInteger(value, name, fallback, min = 0) {
  if (value === null || value === undefined) return fallback;
  if (typeof value !== 'number' || !Number.isInteger(value) || value < min) {
    throw invalidArgValue(name, value, `must be an integer >= ${min}`);
  }
  return value;
}

function toSeconds(value, name) {
  if (typeof value === 'number') {
    if (!Number.isFinite(value)) throw invalidArgValue(name, value, 'must be a finite number');
    return value;
  }
  if (value instanceof Date) return value.getTime() / 1000;
  if (typeof value === 'string' && value.trim() !== '' && Number.isFinite(Number(value))) {
    return Number(value);
  }
  throw invalidArgType(name, 'number, Date or numeric string', value);
}

// ---------------------------------------------------------------------------
// result shapes

class Stats {
  constructor(r) {
    this.dev = r.dev;
    this.mode = r.mode;
    this.nlink = r.nlink;
    this.uid = r.uid;
    this.gid = r.gid;
    this.rdev = r.rdev;
    this.blksize = r.blksize;
    this.ino = r.ino;
    this.size = r.size;
    this.blocks = r.blocks;
    this.atimeMs = r.atimeMs;
    this.mtimeMs = r.mtimeMs;
    this.ctimeMs = r.ctimeMs;
    this.birthtimeMs = r.birthtimeMs;
    this.atime = new Date(r.atimeMs);
    this.mtime = new Date(r.mtimeMs);
    this.ctime = new Date(r.ctimeMs);
    this.birthtime = new Date(r.birthtimeMs);
  }
  isFile() {
    return (this.mode & S_IFMT) === S_IFREG;
  }
  isDirectory() {
    return (this.mode & S_IFMT) === S_IFDIR;
  }
  isSymbolicLink() {
    return (this.mode & S_IFMT) === S_IFLNK;
  }
  isBlockDevice() {
    return (this.mode & S_IFMT) === S_IFBLK;
  }
  isCharacterDevice() {
    return (this.mode & S_IFMT) === S_IFCHR;
  }
  isFIFO() {
    return (this.mode & S_IFMT) === S_IFIFO;
  }
  isSocket() {
    return (this.mode & S_IFMT) === S_IFSOCK;
  }
}

// uv_dirent_type_t values the engine reports
const DT_FILE = 1;
const DT_DIR = 2;
const DT_LINK = 3;
const DT_FIFO = 4;
const DT_SOCKET = 5;
const DT_CHAR = 6;
const DT_BLOCK = 7;

const kType = Symbol('type');

class Dirent {
  constructor(name, parentPath, type) {
    this.name = name;
    this.parentPath = parentPath;
    // Node's deprecated alias of parentPath, kept for code written against it
    this.path = parentPath;
    this[kType] = type;
  }
  isFile() {
    return this[kType] === DT_FILE;
  }
  isDirectory() {
    return this[kType] === DT_DIR;
  }
  isSymbolicLink() {
    return this[kType] === DT_LINK;
  }
  isFIFO() {
    return this[kType] === DT_FIFO;
  }
  isSocket() {
    return this[kType] === DT_SOCKET;
  }
  isCharacterDevice() {
    return this[kType] === DT_CHAR;
  }
  isBlockDevice() {
    return this[kType] === DT_BLOCK;
  }
}

const toStats = (r) => new Stats(r);
const toDirents = (list) => list.map((d) => new Dirent(d.name, d.parentPath, d.type));
const nullToUndefined = (r) => (r === null ? undefined : r);

// ---------------------------------------------------------------------------
// operations: each entry validates its arguments and describes one engine
// call - {op, args, map?, ifNoEntry?} or {value} for an answer that needs
// no I/O. Both the Sync and the promise variant of every function are
// generated from this table.

const ops = {
  readFile(path, options) {
    const o = getOptions(options, { encoding: 'utf8', flag: 'r' });
    checkEncoding(o.encoding);
    return { op: 'readFile', args: [getPath(path)] };
  },

  writeFile(path, data, options) {
    const o = getOptions(options, { encoding: 'utf8', mode: 0o666, flag: 'w' });
    checkEncoding(o.encoding);
    return {
      op: 'writeFile',
      args: [getPath(path), getData(data), getFlag(o.flag, 'w'), getMode(o.mode, 'mode', 0o666)],
    };
  },

  appendFile(path, data, options) {
    const o = getOptions(options, { encoding: 'utf8', mode: 0o666, flag: 'a' });
    checkEncoding(o.encoding);
    return {
      op: 'writeFile',
      args: [getPath(path), getData(data), getFlag(o.flag, 'a'), getMode(o.mode, 'mode', 0o666)],
    };
  },

  stat(path, options) {
    const o = getOptions(options, { bigint: false, throwIfNoEntry: true });
    if (o.bigint) throw invalidArgValue('options.bigint', o.bigint, 'is not supported');
    return { op: 'stat', args: [getPath(path)], map: toStats, ifNoEntry: o.throwIfNoEntry === false };
  },

  lstat(path, options) {
    const o = getOptions(options, { bigint: false, throwIfNoEntry: true });
    if (o.bigint) throw invalidArgValue('options.bigint', o.bigint, 'is not supported');
    return { op: 'lstat', args: [getPath(path)], map: toStats, ifNoEntry: o.throwIfNoEntry === false };
  },

  readdir(path, options) {
    const o = getOptions(options, { encoding: 'utf8', withFileTypes: false, recursive: false });
    checkEncoding(o.encoding);
    return {
      op: 'readdir',
      args: [getPath(path), !!o.withFileTypes, !!o.recursive],
      map: o.withFileTypes ? toDirents : undefined,
    };
  },

  // Node's existsSync answers false for anything that is not a usable path
  exists(path) {
    if (typeof path !== 'string' || path.indexOf('\0') !== -1) return { value: false };
    return { op: 'exists', args: [path] };
  },

  mkdir(path, options) {
    let recursive = false;
    let mode = 0o777;
    if (typeof options === 'number' || typeof options === 'string') {
      mode = getMode(options, 'mode', 0o777);
    } else if (options !== null && options !== undefined) {
      const o = getOptions(options, { recursive: false, mode: 0o777 });
      recursive = !!o.recursive;
      mode = getMode(o.mode, 'options.mode', 0o777);
    }
    return { op: 'mkdir', args: [getPath(path), recursive, mode], map: nullToUndefined };
  },

  // {recursive: true} is Node's deprecated spelling of rm(path, {recursive: true})
  rmdir(path, options) {
    const o = getOptions(options, { recursive: false });
    if (o.recursive) return { op: 'rm', args: [getPath(path), true, false] };
    return { op: 'rmdir', args: [getPath(path)] };
  },

  rm(path, options) {
    const o = getOptions(options, { recursive: false, force: false });
    return { op: 'rm', args: [getPath(path), !!o.recursive, !!o.force] };
  },

  unlink(path) {
    return { op: 'unlink', args: [getPath(path)] };
  },

  rename(oldPath, newPath) {
    return { op: 'rename', args: [getPath(oldPath, 'oldPath'), getPath(newPath, 'newPath')] };
  },

  copyFile(src, dest, mode) {
    const m = getInteger(mode, 'mode', 0);
    return {
      op: 'copyFile',
      args: [getPath(src, 'src'), getPath(dest, 'dest'), (m & constants.COPYFILE_EXCL) !== 0],
    };
  },

  access(path, mode) {
    return { op: 'access', args: [getPath(path), getInteger(mode, 'mode', constants.F_OK)] };
  },

  realpath(path, options) {
    const o = getOptions(options, { encoding: 'utf8' });
    checkEncoding(o.encoding);
    return { op: 'realpath', args: [getPath(path)] };
  },

  readlink(path, options) {
    const o = getOptions(options, { encoding: 'utf8' });
    checkEncoding(o.encoding);
    return { op: 'readlink', args: [getPath(path)] };
  },

  // the type argument only matters on Windows
  symlink(target, path) {
    return { op: 'symlink', args: [getPath(target, 'target'), getPath(path)] };
  },

  chmod(path, mode) {
    return { op: 'chmod', args: [getPath(path), getMode(mode, 'mode')] };
  },

  mkdtemp(prefix, options) {
    const o = getOptions(options, { encoding: 'utf8' });
    checkEncoding(o.encoding);
    return { op: 'mkdtemp', args: [getPath(prefix, 'prefix')] };
  },

  truncate(path, len) {
    return { op: 'truncate', args: [getPath(path), getInteger(len, 'len', 0)] };
  },

  utimes(path, atime, mtime) {
    return { op: 'utimes', args: [getPath(path), toSeconds(atime, 'atime'), toSeconds(mtime, 'mtime')] };
  },
};

function prepare(name, args) {
  if (args.length > 0 && typeof args[args.length - 1] === 'function') {
    throw callbacksUnsupported(name);
  }
  return ops[name].apply(null, args);
}

function finish(spec, result) {
  return spec.map ? spec.map(result) : result;
}

function callAsync(op, args) {
  return new Promise((resolve, reject) => {
    _wbFsAsync(op, args, (reply) => {
      if (reply.error) reject(fromDescriptor(reply.error));
      else resolve(reply.result === null ? undefined : reply.result);
    });
  });
}

const fs = {};
const promises = {};

Object.keys(ops).forEach((name) => {
  const syncFn = function (...args) {
    const spec = prepare(name, args);
    if ('value' in spec) return spec.value;
    let result;
    try {
      result = _wbFsSync(spec.op, spec.args);
    } catch (e) {
      if (spec.ifNoEntry && e && e.code === 'ENOENT') return undefined;
      throw e;
    }
    return finish(spec, result);
  };
  Object.defineProperty(syncFn, 'name', { value: name + 'Sync' });

  const asyncFn = function (...args) {
    let spec;
    try {
      spec = prepare(name, args);
    } catch (e) {
      return Promise.reject(e);
    }
    if ('value' in spec) return Promise.resolve(spec.value);
    return callAsync(spec.op, spec.args).then(
      (result) => finish(spec, result),
      (e) => {
        if (spec.ifNoEntry && e.code === 'ENOENT') return undefined;
        throw e;
      }
    );
  };
  Object.defineProperty(asyncFn, 'name', { value: name });

  fs[name + 'Sync'] = syncFn;
  fs[name] = asyncFn;
  promises[name] = asyncFn;
});

// ---------------------------------------------------------------------------
// fs.watch

const kWatcherId = Symbol('watcherId');

class FSWatcher {
  constructor(id) {
    this[kWatcherId] = id;
  }
  close() {
    const id = this[kWatcherId];
    if (id === null) return;
    this[kWatcherId] = null;
    _wbFsWatchClose(id);
  }
}

function watch(filename, options, listener) {
  const path = getPath(filename, 'filename');
  if (typeof options === 'function') {
    listener = options;
    options = undefined;
  }
  const o = getOptions(options, { persistent: true, recursive: false, encoding: 'utf8' });
  checkEncoding(o.encoding);
  if (o.recursive) {
    throw codedError(
      TypeError,
      'ERR_FEATURE_UNAVAILABLE_ON_PLATFORM',
      'The feature recursive watch is unavailable in wb-rules'
    );
  }
  if (typeof listener !== 'function') throw invalidArgType('listener', 'function', listener);
  const id = _wbFsWatch(path, (event) => listener(event.eventType, event.filename));
  return new FSWatcher(id);
}

Object.assign(fs, { watch, FSWatcher, Stats, Dirent, constants, promises });
promises.constants = constants;

module.exports = fs;
