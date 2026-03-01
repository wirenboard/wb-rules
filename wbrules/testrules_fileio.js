/* global defineRule, fs, log */
/* eslint-disable no-restricted-syntax, no-unused-vars, security/detect-object-injection */

// Helper: standard async callback that logs "opName: ok" or "opName error: ..."
function asyncCallback(opName, formatResult) {
  return function (err, result) {
    if (err) {
      log(opName + ' error: {}', err.message);
    } else if (formatResult) {
      formatResult(result);
    } else {
      log(opName + ': ok');
    }
  };
}

// Sync operation handlers: each receives parts[] from the command string
var syncOps = {
  readFile: function (parts) {
    log('readFile: [{}]', fs.readFileSync(parts[1]));
  },
  writeFile: function (parts) {
    fs.writeFileSync(parts[1], parts.slice(2).join('|'));
    log('writeFile: ok');
  },
  appendFile: function (parts) {
    fs.appendFileSync(parts[1], parts.slice(2).join('|'));
    log('appendFile: ok');
  },
  stat: function (parts) {
    var st = fs.statSync(parts[1]);
    log('stat: size={} isFile={} isDirectory={} mode={} mtime={}',
      st.size, st.isFile, st.isDirectory, st.mode, st.mtime);
  },
  readDir: function (parts) {
    var entries = fs.readdirSync(parts[1]);
    var names = [];
    for (var i = 0; i < entries.length; i++) {
      names.push(entries[i].name + '(file=' + entries[i].isFile + ',dir=' + entries[i].isDirectory + ')');
    }
    names.sort();
    log('readDir: {}', names.join(','));
  },
  exists: function (parts) {
    log('exists: {}', fs.existsSync(parts[1]));
  },
  mkdir: function (parts) {
    var opts = parts[2] === 'recursive' ? {recursive: true} : undefined;
    opts ? fs.mkdirSync(parts[1], opts) : fs.mkdirSync(parts[1]);
    log('mkdir: ok');
  },
  unlink: function (parts) {
    fs.unlinkSync(parts[1]);
    log('unlink: ok');
  },
  rename: function (parts) {
    fs.renameSync(parts[1], parts[2]);
    log('rename: ok');
  },
  rmdir: function (parts) {
    var opts = parts[2] === 'recursive' ? {recursive: true} : undefined;
    opts ? fs.rmdirSync(parts[1], opts) : fs.rmdirSync(parts[1]);
    log('rmdir: ok');
  },
  copyFile: function (parts) {
    fs.copyFileSync(parts[1], parts[2]);
    log('copyFile: ok');
  },
  access: function (parts) {
    parts[2] ? fs.accessSync(parts[1], parseInt(parts[2])) : fs.accessSync(parts[1]);
    log('access: ok');
  },
  realpath: function (parts) {
    log('realpath: {}', fs.realpathSync(parts[1]));
  },
  readlink: function (parts) {
    log('readlink: {}', fs.readlinkSync(parts[1]));
  },
  accessConstants: function () {
    log('F_OK={} R_OK={} W_OK={} X_OK={}',
      fs.constants.F_OK, fs.constants.R_OK, fs.constants.W_OK, fs.constants.X_OK);
  },

  // Error cases: wrong args
  readFileNoArgs: function () {
    fs.readFileSync();
    log('readFile: should not reach');
  },
  writeFileOneArg: function (parts) {
    fs.writeFileSync(parts[1]);
    log('writeFile: should not reach');
  },
  statNoArgs: function () {
    fs.statSync();
    log('stat: should not reach');
  },
  readFileInt: function () {
    fs.readFileSync(123);
    log('readFile: should not reach');
  },
  unlinkDir: function (parts) {
    fs.unlinkSync(parts[1]);
    log('unlink: should not reach');
  },
};

// Async operation handlers
var asyncOps = {
  asyncReadFile: function (parts) {
    fs.readFile(parts[1], asyncCallback('asyncReadFile', function (data) {
      log('asyncReadFile: [{}]', data);
    }));
  },
  asyncWriteFile: function (parts) {
    fs.writeFile(parts[1], parts.slice(2).join('|'), asyncCallback('asyncWriteFile'));
  },
  asyncAppendFile: function (parts) {
    fs.appendFile(parts[1], parts.slice(2).join('|'), asyncCallback('asyncAppendFile'));
  },
  asyncStat: function (parts) {
    fs.stat(parts[1], asyncCallback('asyncStat', function (st) {
      log('asyncStat: size={} isFile={} isDirectory={}', st.size, st.isFile, st.isDirectory);
    }));
  },
  asyncReaddir: function (parts) {
    fs.readdir(parts[1], asyncCallback('asyncReaddir', function (entries) {
      var names = [];
      for (var i = 0; i < entries.length; i++) {
        names.push(entries[i].name);
      }
      names.sort();
      log('asyncReaddir: {}', names.join(','));
    }));
  },
  asyncExists: function (parts) {
    fs.exists(parts[1], function (exists) {
      log('asyncExists: {}', exists);
    });
  },
  asyncMkdir: function (parts) {
    var cb = asyncCallback('asyncMkdir');
    parts[2] === 'recursive' ? fs.mkdir(parts[1], {recursive: true}, cb) : fs.mkdir(parts[1], cb);
  },
  asyncUnlink: function (parts) {
    fs.unlink(parts[1], asyncCallback('asyncUnlink'));
  },
  asyncRename: function (parts) {
    fs.rename(parts[1], parts[2], asyncCallback('asyncRename'));
  },
  asyncRmdir: function (parts) {
    var cb = asyncCallback('asyncRmdir');
    parts[2] === 'recursive' ? fs.rmdir(parts[1], {recursive: true}, cb) : fs.rmdir(parts[1], cb);
  },
  asyncCopyFile: function (parts) {
    fs.copyFile(parts[1], parts[2], asyncCallback('asyncCopyFile'));
  },
  asyncAccess: function (parts) {
    var cb = asyncCallback('asyncAccess');
    parts[2] ? fs.access(parts[1], parseInt(parts[2]), cb) : fs.access(parts[1], cb);
  },
  asyncRealpath: function (parts) {
    fs.realpath(parts[1], asyncCallback('asyncRealpath', function (resolved) {
      log('asyncRealpath: {}', resolved);
    }));
  },
  asyncReadlink: function (parts) {
    fs.readlink(parts[1], asyncCallback('asyncReadlink', function (target) {
      log('asyncReadlink: {}', target);
    }));
  },

  // Watch
  watch: function (parts) {
    var watcher = fs.watch(parts[1], function (eventType, filename) {
      log('watch: {} {}', eventType, filename);
    });
    global._testWatcher = watcher;
    log('watch: started');
  },
  watchClose: function () {
    if (global._testWatcher) {
      global._testWatcher.close();
      log('watch: closed');
    }
  },
  watchDir: function (parts) {
    var watcher = fs.watch(parts[1], function (eventType, filename) {
      log('watchDir: {} {}', eventType, filename);
    });
    global._testDirWatcher = watcher;
    log('watchDir: started');
  },
  watchDirClose: function () {
    if (global._testDirWatcher) {
      global._testDirWatcher.close();
      log('watchDir: closed');
    }
  },
  watchNonExistent: function (parts) {
    var w = fs.watch(parts[1], function (eventType, filename) {
      log('watch: {} {}', eventType, filename);
    });
    log('watch: should not reach');
  },

  // Error cases: missing callback
  asyncReadFileNoCallback: function (parts) {
    fs.readFile(parts[1]);
    log('asyncReadFile: should not reach');
  },
  asyncWriteFileNoCallback: function (parts) {
    fs.writeFile(parts[1], parts.slice(2).join('|'));
    log('asyncWriteFile: should not reach');
  },
  asyncStatNoCallback: function (parts) {
    fs.stat(parts[1]);
    log('asyncStat: should not reach');
  },
};

defineRule('fileCmd', {
  whenChanged: 'somedev/fileCmd',
  then: function (cmd) {
    var parts = cmd.split('|');
    var op = parts[0];
    var handler = syncOps[op] || asyncOps[op];

    try {
      if (handler) {
        handler(parts);
      }
    } catch (e) {
      log.error('caught error');
    }
  },
});
